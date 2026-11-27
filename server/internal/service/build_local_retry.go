// build_local_retry.go · 手动重试「下载到本机」。
//
// 职责：受理用户主动重试，把失败态的包重新排队拉回。
// 输入：包的 id 与请求者身份。
// 输出：无；进度与结果通过 local_state / local_pulled_bytes 反映。
// 边界：只做准入判断与状态重置，真正的搬运复用 PullLocal，不重复实现下载逻辑。
//
// 为什么需要它：自动重试要等下一轮巡检（最长 2 小时）。
// 管理员看到红色失败提示时，第一反应是「再点一次」——
// 若只能干等，这个提示就变成了无从下手的死信息。
package service

import (
	"context"
	"fmt"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// RetryLocalPull 手动触发一次拉回，异步执行，立即返回。
//
// 异步而非等待：整包可能有上百 MB，慢链路上几分钟起步，
// 让 HTTP 请求等到完成会被 Cloudflare 的 100 秒代理超时切断。
// 前端改为轮询 local_pulled_bytes 看进度，与自动巡检走同一条路径。
func (s *BuildService) RetryLocalPull(ctx context.Context, id string, identity contract.Identity) (contract.Build, error) {
	build, err := s.authorizedBuild(ctx, id, identity)
	if err != nil {
		return contract.Build{}, err
	}
	// 先取包再判断 R2：包带着归属公司，才知道该查哪家公司的通道。
	// 顺序颠倒会变成「用全局是否启用」判断，那在按公司隔离下已经是错误的问题。
	if !s.r2.Enabled(build.TenantID) {
		return contract.Build{}, &contract.ValidationError{Message: "对象存储未配置，无法下载"}
	}
	if err := checkPullable(build); err != nil {
		return contract.Build{}, err
	}

	// 先重置成 absent 再交给 PullLocal：
	// PullLocal 的抢占条件是「状态不是 pulling」，而失败态本身就能被抢，
	// 之所以仍显式重置，是为了同时清掉上一次的失败原因与残留进度——
	// 否则重试成功后卡片上还挂着旧的红色文字，看起来像没生效。
	repo := s.repos.For(identity.Mode)
	if err := s.resetLocalForRetry(ctx, repo, build.ID); err != nil {
		return contract.Build{}, err
	}

	go s.PullLocal(context.Background(), identity.Mode, build.ID)

	// 回填内存态，让前端立刻看到「重新排队中」，不用等下一次轮询。
	build.LocalState = contract.LocalAbsent
	build.LocalPulledBytes = 0
	build.LocalError = ""
	return build, nil
}

// checkPullable 判断一个包能否重新拉回，返回 nil 表示可以。
//
// 抽成纯函数以便直接测试：它只依赖 r2State 与 localState 两个输入。
// 若混在 RetryLocalPull 里，任何验证都要备好数据库与完整权限链，
// 成本高到没人愿意写，这条规则就会长期无人看管。
//
// 三种不可重试的情况：
//   - R2 没有对象（非 synced）：源端就没有字节，重试多少次都没用；
//   - 本机已有副本：没有可下载的目标，重试只会白占一个并发槽；
//   - 正在下载：重复触发会让同一对象被下载两份。
func checkPullable(build contract.Build) error {
	if build.R2State != contract.R2StateSynced {
		return &contract.ValidationError{Message: "该版本不在对象存储中，无法下载到本机"}
	}
	switch build.LocalState {
	case contract.LocalPresent:
		return &contract.ValidationError{Message: "本机已有该版本的副本，无需重试"}
	case contract.LocalPulling:
		return &contract.ValidationError{Message: "该版本正在下载中，请稍候"}
	}
	return nil
}

// resetLocalForRetry 把本机状态清回起点，让 PullLocal 能重新抢占。
//
// 只清本地相关字段，不动 r2_state：那个字段描述对象在桶里的事实，
// 与「本机要不要重下一遍」无关，改了会让下载通道判断出错。
func (s *BuildService) resetLocalForRetry(ctx context.Context, repo *repository.BuildRepository, id string) error {
	if err := repo.SetLocalError(ctx, id, ""); err != nil {
		return fmt.Errorf("清理失败原因失败: %w", err)
	}
	if err := repo.SetLocalProgress(ctx, id, 0); err != nil {
		return fmt.Errorf("重置下载进度失败: %w", err)
	}
	return nil
}

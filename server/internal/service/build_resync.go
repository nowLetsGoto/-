// build_resync.go · 把已淘汰的包重新推回对象存储。
//
// 职责：用户显式触发的一次重推——校验前置条件、上传、回写状态，并把失败原因如实返回。
// 输入：包的 id 与请求者身份。
// 输出：更新后的 Build；失败时返回可直接展示给用户的错误。
// 边界：只处理「本机有副本、R2 没有」这一种情况，不负责拉回本机（那是 PullLocal）。
//
// 与 syncToR2 的区别：那个是后台旁路，失败只记日志、没人等它；
// 这个是用户点了按钮在等结果的，必须返回明确结果，且不能静默跳过。
// 两者的前置校验与错误处理不同，硬合成一个函数会让两边都难读。
package service

import (
	"context"
	"fmt"
	"os"

	"dandelion/server/internal/contract"
)

// SyncToR2 把一个已被淘汰的包重新推到对象存储，供用户恢复 CDN 直链下载。
//
// 为什么只允许 evicted：synced 的包对象还在桶里，重推纯属浪费带宽；
// pending 的包从未同步过，属于上传流程的中间态，不该由这个入口介入。
// 限定单一入口，是为了让「什么时候该重推」只有一个答案。
//
// 保留原有 r2_key 而不重新生成：那个 key 里带着 {租户}/{项目}/direct/ 前缀，
// 是按归属组织对象的唯一线索。重新生成会丢掉归属，对象成为孤儿——
// 将来做反向同步时再也无法判断它属于哪个项目。
func (s *BuildService) SyncToR2(ctx context.Context, id string, identity contract.Identity) (contract.Build, error) {
	if s.r2 == nil {
		return contract.Build{}, &contract.ValidationError{Message: "对象存储未配置，无法同步"}
	}

	build, err := s.authorizedBuild(ctx, id, identity)
	if err != nil {
		return contract.Build{}, err
	}

	if err := checkResyncable(build); err != nil {
		return contract.Build{}, err
	}
	if build.R2State == contract.R2StateSynced {
		return build, nil // 幂等：已在桶里，直接回当前状态
	}

	abs, err := s.paths.SafeResolve(build.RelativePath)
	if err != nil {
		return contract.Build{}, contract.NotFound(contract.ResourceBuild)
	}
	file, err := os.Open(abs)
	if err != nil {
		// 本机副本缺失是「同步到 R2」唯一无法自行恢复的前置条件：
		// 这个接口不做拉回，也就没有别的字节来源。
		return contract.Build{}, &contract.ValidationError{
			Message: "本机副本已丢失，无法同步到对象存储",
		}
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return contract.Build{}, fmt.Errorf("读取本机副本失败: %w", err)
	}

	// 沿用记录里的 key 而非重新生成：见函数注释。
	key := build.R2Key
	if key == "" {
		return contract.Build{}, &contract.ValidationError{
			Message: "该版本缺少对象名，无法同步",
		}
	}

	contentType, err := s.contentTypeOf(build.Platform)
	if err != nil {
		return contract.Build{}, err
	}

	if err := s.r2.Put(ctx, key, file, info.Size(), contentType, build.OriginalFileName); err != nil {
		return contract.Build{}, fmt.Errorf("上传到对象存储失败: %w", err)
	}

	if err := s.repos.For(identity.Mode).MarkR2Synced(ctx, build.ID, key, info.Size()); err != nil {
		// 对象已上传成功、只是状态没写上：不该当作失败——
		// 下次下载仍会走 CDN，巡检也会把状态补齐。
		return contract.Build{}, fmt.Errorf("同步已完成但状态更新失败: %w", err)
	}

	// 预热 CDN：让首个下载者命中边缘缓存，而不是等待回源。
	s.prewarmCDNAsync(key)

	// 回填内存态，让调用方拿到的响应与数据库一致，省一次查询。
	build.R2State = contract.R2StateSynced
	return build, nil
}

// checkResyncable 判断一个包当前是否允许重推，返回 nil 表示可以。
//
// 抽成纯函数是为了能被直接测到：它只依赖 r2State 这一个输入，
// 若混在 SyncToR2 里，任何验证都得先备好数据库、对象存储与完整权限链，
// 成本高到没人愿意写，这条规则就会长期无人看管。
//
// 三种状态的去留各有理由：
//   - synced：对象还在桶里，重推纯属浪费一次整包上传，但也不该报错
//     （用户可能连点两次），由调用方直接返回当前状态。
//   - evicted：唯一允许重推的状态，本机副本是字节的唯一来源。
//   - pending / failed / 其他：上传流程的中间态，由后台镜像负责，
//     不该由这个人工入口介入，否则两条路径会互相覆盖状态。
func checkResyncable(build contract.Build) error {
	switch build.R2State {
	case contract.R2StateSynced, contract.R2StateEvicted:
		return nil
	default:
		return &contract.ValidationError{Message: "该版本尚未同步到对象存储，无需重推"}
	}
}

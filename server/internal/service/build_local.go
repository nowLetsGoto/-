// build_local.go · 把 R2 对象拉回本机留底。
//
// 职责：把一个已在 R2 的包落成本机副本，并推进 local_state。
// 输入：包的 id 与分区模式。
// 输出：无返回值；结果通过 local_state 表达（present / failed）。
// 边界：只搬字节，不改 r2_state、不删 R2 对象——那些是同步与淘汰的职责。
//
// 为什么需要它：浏览器直传时字节直接进了 R2，本机从未落盘。
// 一旦淘汰删掉对象，这个包就再无副本可下载。留底让本机成为权威副本，
// R2 退化成可回收的缓存——这才谈得上用磁盘换 R2 免费额度。
package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// localPullSpaceRatio 是拉回前要求的空间余量倍数。
//
// 按包大小的 1.2 倍判断：写文件本身需要 size，临时文件与文件系统开销还会占一些，
// 贴着可用空间写会把磁盘写满，连带影响数据库与日志的写入。
const localPullSpaceRatio = 1.2

// localPullSlots 限制同时进行的拉回数量。
//
// 给 2：单机部署下用户下载会与拉回抢带宽，超过两条会让页面下载明显变慢。
// 用带缓冲 channel 当信号量，比引入 errgroup + 限流器更直白——
// 这里只需要「最多 N 个同时进行」，不需要取消传播或错误聚合。
var localPullSlots = make(chan struct{}, 2)

// PullLocal 把一个包的字节从 R2 拉到本机正式目录，并推进 local_state。
//
// 四步：排队 → 抢占 → 落盘 → 收尾，顺序不可交换：
//
//  0. 排队：占一个并发槽。放在抢占之前是刻意的——若先标 pulling 再排队，
//     状态会在等带宽期间一直显示"归档中"，而这个包其实还没开始搬。
//  1. 抢占：条件更新 local_state=pulling，抢不到说明别的执行体正在拉，直接返回。
//     这不是可选的优化——没有它，提交后的即时拉取与巡检会同时写同一个文件。
//  2. 落盘：先写临时文件，校验大小后 rename 到正式路径。
//     直接往正式路径写会在中断时留下半截文件，而下载逻辑只看「文件是否存在」，
//     半截文件会被当成有效副本下发给用户。
//  3. 收尾：成功标 present，失败标 failed 并记日志，等下一轮巡检重试。
//
// 不返回错误：调用方（提交后的 goroutine、巡检）都不该因单个包搬运失败而中断。
// 结果已经落到 local_state，可观测、可重试。
func (s *BuildService) PullLocal(ctx context.Context, mode contract.SessionMode, id string) {
	localPullSlots <- struct{}{}
	defer func() { <-localPullSlots }()

	repo := s.repos.For(mode)

	claimed, err := repo.ClaimLocalPull(ctx, id)
	if err != nil {
		log.Printf("本机留底抢占失败 id=%s err=%v", id, err)
		return
	}
	if !claimed {
		return // 已有执行体在搬这个包，本次让路。
	}

	if err := s.pullLocalToDisk(ctx, repo, id); err != nil {
		log.Printf("本机留底失败 id=%s err=%v（下一轮巡检会重试）", id, err)
		s.setLocalState(repo, id, contract.LocalFailed)
		return
	}

	s.setLocalState(repo, id, contract.LocalPresent)
	log.Printf("本机留底完成 id=%s", id)
}

// setLocalState 回写状态，失败只记日志。
//
// 单独收拢成一个方法，是因为 PullLocal 的成功与失败两条路径都要写状态，
// 而「写失败时该记什么日志」这件事只该有一份实现。
func (s *BuildService) setLocalState(repo *repository.BuildRepository, id, state string) {
	if err := repo.SetLocalState(context.Background(), id, state); err != nil {
		// 文件可能已经落好，只是状态没写上：下一轮巡检会因状态不是 absent/failed 而跳过，
		// 因此这条日志必须显眼，否则这个包会静默停在错误状态。
		log.Printf("本机留底状态回写失败 id=%s state=%s err=%v", id, state, err)
	}
}

// pullLocalToDisk 落盘并校验，是唯一碰文件系统的部分。
//
// 从 PullLocal 拆出来，是为了让状态机读起来只有三步，
// 而不被 open / rename / stat 的细节淹没。
func (s *BuildService) pullLocalToDisk(ctx context.Context, repo *repository.BuildRepository, id string) error {
	target, err := repo.GetLocalTarget(ctx, id)
	if err != nil {
		return err
	}
	if target.R2Key == "" {
		return fmt.Errorf("包没有对象名，无法拉回")
	}

	dst, err := s.paths.SafeResolve(target.RelativePath)
	if err != nil {
		return fmt.Errorf("落盘路径不合法: %w", err)
	}

	// 文件已在本机就直接认定归档完成，不再从 R2 下一遍。
	//
	// 这不只是省带宽：本次改造前所有包都没有 local_state 列，
	// 加列后一律是默认值 absent，与磁盘上的实际情况并不相符。
	// 这一句让存量数据在第一次巡检时自然对齐——有文件的直接转 present，
	// 没文件的才真正去下载，不必另写一段一次性迁移脚本。
	if info, statErr := os.Stat(dst); statErr == nil && info.Size() == target.FileSize {
		log.Printf("本机已有副本，无需拉回 id=%s", target.ID)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	if err := s.ensureDiskSpace(target); err != nil {
		return err
	}

	// 临时文件与目标同目录：跨文件系统 rename 会退化成复制，那样就没有原子性。
	tmp := dst + ".pulling"
	written, err := s.downloadToFile(ctx, target.R2Key, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if written != target.FileSize {
		_ = os.Remove(tmp)
		return fmt.Errorf("拉回大小不一致：期望 %d 实际 %d", target.FileSize, written)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("落盘失败: %w", err)
	}
	return nil
}

// downloadToFile 把 R2 对象写进指定路径，返回写入字节数。
//
// 失败时不在内部删半截文件：清理属于收尾，与下载不是同一件事，
// 混在一起会让错误路径难以追踪。由调用方统一处理。
func (s *BuildService) downloadToFile(ctx context.Context, key, path string) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("创建临时文件失败: %w", err)
	}
	written, copyErr := s.r2.DownloadTo(ctx, key, f)
	closeErr := f.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("从对象存储下载失败: %w", copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("关闭临时文件失败: %w", closeErr)
	}
	return written, nil
}

// ensureDiskSpace 在下载前确认磁盘放得下。
//
// 查不到可用空间时放行：宁可让写入自己失败，也不要因为一次查询失败就永久拒绝留底。
// 留底失败只影响本机副本，而误判会让功能整体不可用。
func (s *BuildService) ensureDiskSpace(target repository.LocalTarget) error {
	free, err := s.paths.FreeBytes()
	if err != nil {
		log.Printf("磁盘空间查询失败，跳过预检 id=%s err=%v", target.ID, err)
		return nil
	}
	need := int64(float64(target.FileSize) * localPullSpaceRatio)
	if free < need {
		return fmt.Errorf("磁盘空间不足：需要 %d 字节，可用 %d 字节", need, free)
	}
	return nil
}

// localStateFor 根据「这个包是否只有 R2 副本」给出建记录时的本机状态。
//
// 判据是直传对象的 key 是否为空，而不是去检查文件系统：
// 建记录这一刻，普通上传的文件刚刚 rename 到位、直传的文件从未落盘，
// 用 key 就能确定答案，多一次 stat 只是徒增一次系统调用。
func localStateFor(r2Key string) string {
	if r2Key == "" {
		return contract.LocalPresent // 普通上传：本机就是权威副本
	}
	return contract.LocalAbsent // 直传：字节只在 R2，等后台拉回
}

// pullPendingLocal 把一个租户下所有「R2 有、本机没有」的包补拉回本机，返回尝试个数。
//
// 串行执行而非并发：拉回是磁盘与带宽密集型操作，并发只会让两者互相拖慢，
// 且单机部署下用户下载会与它抢带宽。当前包量与体积（119MB 级、十余个）
// 串行完全够用，简单实现比并发调度更容易排查。
//
// 返回值是「尝试搬运的个数」而非「成功个数」：单个包的成功与否已经写进
// local_state，日志里再区分一次只会让两个地方的统计口径逐渐分叉。
func (s *BuildService) pullPendingLocal(ctx context.Context, mode contract.SessionMode, tenantID string) (int, error) {
	if s.r2 == nil {
		return 0, nil
	}
	rows, err := s.repos.For(mode).ListLocalPending(ctx, tenantID)
	if err != nil {
		return 0, err
	}

	for _, row := range rows {
		// 单个失败不中断整批：一个包搬不动不该让其余包继续缺副本。
		// panic 也收在这里，理由同上——批量任务里一个坏数据不该让整批失效。
		s.pullOneSafely(ctx, mode, row.ID)
	}
	return len(rows), nil
}

// pullOneSafely 给单个包的拉回加上 panic 保护。
func (s *BuildService) pullOneSafely(ctx context.Context, mode contract.SessionMode, id string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("拉回 panic，跳过该包 id=%s err=%v", id, r)
		}
	}()
	s.PullLocal(ctx, mode, id)
}

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
	"sync/atomic"
	"time"

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

// localPullStallTimeout 是「多久没有任何进展」就判定下载已死。
//
// 为什么用停滞时间而不是总时长：
// 总时长超时无法区分「慢但在动」与「彻底卡死」，只能取一个宽松上限，
// 于是正常要 3 分钟的包被给了 20 分钟，真卡死时用户要盯 20 分钟进度条不动。
// 实测本机下行约 600KB/s，119MB 约 3 分钟；若链路更慢到 10 分钟也仍然在动，
// 用总时长判就会误杀，用停滞判则完全不受影响。
//
// 给 90 秒：两倍于常用的 TCP 重传窗口，网络抖动造成的短暂停顿不会误判，
// 而真正卡死的连接（实测停了两小时）会在 90 秒内被发现。
const localPullStallTimeout = 90 * time.Second

// 单个包不再设总时长上限：停滞检测已经能兜住死连接，
// 再加一个总时长只会误杀慢速链路上的正常下载。

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

	// 停滞检测取代总时长超时。
	//
	// 原先一个包可以独占整批 30 分钟预算一直挂着：实测有个包卡在 22%
	// 两个多小时，进度条一动不动，而数据库里它始终是 pulling，看不出死活。
	// 改成总时长 20 分钟又太长——正常只需 3 分钟，用户要盯着不动的进度条等 20 分钟。
	//
	// 现在的判据是「有没有进展」而非「花了多久」：只要字节数还在增长就继续下，
	// 一旦连续 localPullStallTimeout 没有任何新增就判定连接已死。
	// 这样慢速链路不会被误杀，真卡死也能在一分半内发现。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 进度用原子变量在下载与守护之间共享：
	// 守护需要看到「最新写到哪了」，而下载协程正在用它写文件，不能加锁阻塞。
	var pulled atomic.Int64
	go watchStall(runCtx, cancel, id, &pulled, localPullStallTimeout)

	if err := s.pullLocalToDisk(runCtx, repo, id, &pulled); err != nil {
		log.Printf("本机留底失败 id=%s err=%v（下一轮巡检会重试）", id, err)
		// 先记原因再改状态：卡片要同时看到「失败」和「为什么失败」，
		// 顺序反了会让前端在两次查询之间读到「失败但没有原因」。
		s.setLocalError(repo, id, err.Error())
		s.setLocalState(repo, id, contract.LocalFailed)
		return
	}

	s.setLocalError(repo, id, "") // 成功时清掉上一次的失败原因
	s.setLocalState(repo, id, contract.LocalPresent)
	log.Printf("本机留底完成 id=%s", id)
}

// setLocalError 记录失败原因，失败只记日志。
func (s *BuildService) setLocalError(repo *repository.BuildRepository, id, reason string) {
	if err := repo.SetLocalError(context.Background(), id, reason); err != nil {
		log.Printf("记录失败原因失败 id=%s err=%v", id, err)
	}
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
// pullLocalToDisk 真正搬字节。
//
// pulled 与下载共享同一个原子计数：守护协程靠它判断「还在不在动」，
// 每次写入既上报数据库（给用户看）也更新它（给守护看）。
func (s *BuildService) pullLocalToDisk(
	ctx context.Context, repo *repository.BuildRepository, id string, pulled *atomic.Int64,
) error {
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
	onProgress := func(n int64) {
		pulled.Store(n)
		s.reportProgress(repo, target.ID, n)
	}
	written, err := s.downloadToFile(ctx, target.TenantID, target.R2Key, tmp, target.FileSize, onProgress)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if written != target.FileSize {
		_ = os.Remove(tmp)
		return fmt.Errorf("拉回大小不一致：期望 %d 实际 %d", target.FileSize, written)
	}
	// 落盘后再补一次满进度：progressWriter 按台阶上报，最后一段可能没跨过台阶，
	// 前端就会停在 98% 而不是 100%。
	s.reportProgress(repo, target.ID, written)
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("落盘失败: %w", err)
	}
	return nil
}

// watchStall 在下载停滞超过 threshold 时取消下载。
//
// 参数：ctx（下载的上下文，取消它会中断 io.Copy）、cancel（同一上下文的取消函数）、
// id（仅用于日志）、pulled（下载协程持续更新的字节数）、threshold（停滞多久算死）。
// 边界：只观察与取消，不重试、不改状态——那些是 PullLocal 的收尾工作。
//
// 阈值作为参数而不是直接读常量：测试要把它压到毫秒级才能在秒内跑完，
// 而全局可变状态会让并行测试互相干扰。
//
// 判据是「与上次相比有没有增长」而不是「绝对值够不够大」：
// 后者会把刚开始下载的包（字节数还很少）误判为停滞。
func watchStall(ctx context.Context, cancel context.CancelFunc, id string, pulled *atomic.Int64, threshold time.Duration) {
	// 检查间隔取阈值的 1/6：这个比例保证「刚好跨过阈值」能被察觉，
	// 又不会让高频轮询本身成为负担。写死 15 秒会出问题——
	// 阈值被调小时（测试里要压到毫秒级）间隔反而比阈值大，永远等不到超时。
	checkEvery := threshold / 6
	if checkEvery <= 0 {
		checkEvery = time.Millisecond
	}

	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()

	last := pulled.Load()
	stalledFor := time.Duration(0)

	for {
		select {
		case <-ctx.Done():
			return // 下载正常结束或被其他地方取消，守护随之退出。
		case <-ticker.C:
			current := pulled.Load()
			if current != last {
				last = current
				stalledFor = 0
				continue
			}

			stalledFor += checkEvery
			if stalledFor >= threshold {
				log.Printf("下载停滞 %s 无进展，取消本次拉回 id=%s（已下 %d 字节）",
					stalledFor, id, current)
				cancel()
				return
			}
		}
	}
}

// reportProgress 上报进度，失败只记日志。
//
// 用独立 context 而非下载的 ctx：下载结束时 ctx 可能已被取消，
// 用它写库会让最后一笔进度（恰恰是 100%）永远写不进去。
func (s *BuildService) reportProgress(repo *repository.BuildRepository, id string, pulled int64) {
	if err := repo.SetLocalProgress(context.Background(), id, pulled); err != nil {
		log.Printf("更新下载进度失败 id=%s pulled=%d err=%v", id, pulled, err)
	}
}

// downloadToFile 把 R2 对象写进指定路径，按进度回调上报已下载字节。
//
// 失败时不在内部删半截文件：清理属于收尾，与下载不是同一件事，
// 混在一起会让错误路径难以追踪。由调用方统一处理。
func (s *BuildService) downloadToFile(
	ctx context.Context, tenantID, key, path string, total int64, onProgress func(int64),
) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("创建临时文件失败: %w", err)
	}
	// 入口取快照：这是一次可能持续数分钟的下载，中途重取会换凭据。
	r2 := s.r2.Get(tenantID)
	if r2 == nil {
		return 0, fmt.Errorf("对象存储未启用，无法拉回")
	}
	written, copyErr := r2.DownloadTo(ctx, key, newProgressWriter(f, total, onProgress))
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
	// 按公司判断：严格隔离下，未配置 R2 的公司没有可拉回的源。
	if !s.r2.Enabled(tenantID) {
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

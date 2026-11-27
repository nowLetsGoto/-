// retention_worker.go · 存储淘汰的定时巡检。
//
// 职责：按固定间隔遍历所有租户，对每个租户执行一次容量淘汰。
// 输入：BuildService（决策与执行）、租户列表、巡检间隔。
// 输出：无；副作用是 R2 对象被删除、包的 r2_* 字段被收敛。
// 边界：只做调度——不判断该淘汰谁（service.PlanRetention），不执行删除细节
//
//	（service.EvictTenant）。这样调度频率与淘汰规则可以各自演进。
//
// 为什么用 time.Ticker 而不是引入队列：单机部署、任务量小（每 2 小时一次，
// 每个租户一次查询 + 若干删除），一个 goroutine 足够；
// 引入 Redis/Celery 之类只为这点工作量，会让部署与排障都变复杂。
package service

import (
	"context"
	"log"
	"time"

	"dandelion/server/internal/contract"
)

// RetentionInterval 是**淘汰**的巡检间隔。
//
// 2 小时是在「及时收敛」与「少打扰存储」之间的折中：
// 淘汰是容量回收，慢一点没有感知，没必要频繁遍历租户查候选集。
const RetentionInterval = 2 * time.Hour

// localPullInterval 是**补拉**的巡检间隔。
//
// 比淘汰短得多，因为两者的可感知程度完全不同：
// 淘汰是后台容量管理，用户看不见；补拉会显示成卡片上的进度条，
// 一旦卡住，管理员看到的就是一条永远停在 22% 的进度条。
//
// 为什么是 5 分钟：它必须明显小于 localPullStaleAfter（30 分钟），
// 否则「发现卡死」的延迟会接近阈值本身。5 分钟意味着一个卡住的下载
// 最迟约 35 分钟被重新捡起；若跟着淘汰走 2 小时一轮，
// 实测最长要等 2 小时 30 分钟，用户只能干看着。
const localPullInterval = 5 * time.Minute

// retentionTenantLister 列出所有需要巡检的租户 id。
//
// 单独定义接口而不直接依赖租户仓储，是为了让 Worker 只认识"能给我一串租户 id"这件事，
// 不必知道租户表长什么样，测试时也不必将就整张表。
type retentionTenantLister interface {
	ListTenantIDs(ctx context.Context) ([]string, error)
}

// RunRetentionWorker 启动巡检循环，随 ctx 取消而退出。
//
// 启动后立即跑一次：服务重启往往意味着上次运行被中断，
// 先把积压的超额情况处理掉，比干等一个完整周期更符合预期。
//
// 拆成两个独立循环而不是共用一条 ticker：
// 补拉和淘汰的时间要求差 24 倍（5 分钟 vs 2 小时）。共用一个周期时，
// 补拉被迫跟着淘汰的慢节奏走，卡住的下载要等一两小时才被重新捡起——
// 而它恰恰是用户唯一能看见的那部分（卡片上的进度条）。
// 拆开后各自按需要的节奏运行，代价只是多一个 goroutine。
func (s *BuildService) RunRetentionWorker(ctx context.Context, tenants retentionTenantLister) {
	if s.r2 == nil {
		log.Printf("淘汰巡检未启动：未配置对象存储")
		return
	}
	if tenants == nil {
		log.Printf("淘汰巡检未启动：缺少租户来源")
		return
	}

	go s.loopSafely(ctx, "补拉", localPullInterval, func() { s.pullSweepSafely(ctx, tenants) })
	go s.loopSafely(ctx, "淘汰", RetentionInterval, func() { s.sweepOnceSafely(ctx, tenants) })
}

// loopSafely 按固定间隔重复执行一轮任务，随 ctx 取消而退出。
//
// 参数：名称（用于日志）、间隔、单轮任务。
// 边界：只管节奏与 panic 兜底，不认识补拉或淘汰的任何细节。
//
// 两处保护都不能省：
//   - panic 兜底：单个 goroutine 意外退出会让巡检永久停止，
//     且没有任何日志，排查时只能看到「服务在跑但什么都没发生」。
//   - 先跑一轮再进 ticker：服务重启往往意味着上次被中断，
//     积压该立刻处理，而不是干等一个完整周期。
func (s *BuildService) loopSafely(ctx context.Context, name string, interval time.Duration, round func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("%s巡检 goroutine 发生 panic，已恢复：%v", name, r)
		}
	}()

	log.Printf("%s巡检已启动，间隔 %s", name, interval)
	round()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("%s巡检已停止", name)
			return
		case <-ticker.C:
			round()
		}
	}
}

// pullSweepSafely 只做补拉，遍历所有租户的每个分区。
//
// 与淘汰分开的另一个好处：补拉失败时不会连带淘汰也被跳过，
// 反之亦然。原先合在一起，任何一侧出错都会让整轮作废。
func (s *BuildService) pullSweepSafely(ctx context.Context, tenants retentionTenantLister) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("补拉本轮 panic，已恢复（下轮继续）：%v", r)
		}
	}()

	ids, err := tenants.ListTenantIDs(ctx)
	if err != nil {
		log.Printf("补拉巡检：列举租户失败 err=%v", err)
		return
	}

	start := time.Now()
	pulled, failed := 0, 0
	for _, tenantID := range ids {
		for _, mode := range []contract.SessionMode{contract.ModeTeam, contract.ModeSolo} {
			pullCtx, cancel := context.WithTimeout(ctx, localPullBatchTimeout)
			n, err := s.pullPendingLocal(pullCtx, mode, tenantID)
			cancel()
			if err != nil {
				log.Printf("补拉本机：租户 %s 分区 %s 失败 err=%v", tenantID, mode, err)
				failed++
			}
			pulled += n
		}
	}
	log.Printf("补拉巡检完成：%d 个租户 × 2 分区，补拉 %d 个，失败 %d 次，耗时 %s",
		len(ids), pulled, failed, time.Since(start).Round(time.Millisecond))
}

// sweepOnce 对所有租户的每个分区执行一次淘汰。
//
// sweepOnceSafely 给一轮巡检加上 panic 保护。
//
// 单个租户或单个包出问题不该让整轮巡检失效：把 panic 收在这一层，
// 保证「某轮失败」能被日志看到，而不是 goroutine 静默退出、功能永久消失。
func (s *BuildService) sweepOnceSafely(ctx context.Context, tenants retentionTenantLister) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("巡检本轮 panic，已恢复（下轮继续）：%v", r)
		}
	}()
	s.sweepOnce(ctx, tenants)
}

// 为什么要遍历两个分区：同一个租户在 team 与 solo 里都可能有项目
// （实测默认公司既有 2 个团队项目也有 1 个单人项目）。只扫一个分区
// 会让另一边的包永远超额而无人处理。
//
// 为什么先补拉后淘汰：淘汰要求本机有副本才敢删 R2，否则包会彻底消失。
// 先补拉能让本轮淘汰多一些可用副本，减少「因缺副本而跳过」的情况。
//
// 单个租户失败只记日志并继续：某个租户的数据库抖动不该让其余租户
// 长期超额。整轮结束后汇总一行，便于在日志里一眼看出这轮做了多少事。
func (s *BuildService) sweepOnce(ctx context.Context, tenants retentionTenantLister) {
	ids, err := tenants.ListTenantIDs(ctx)
	if err != nil {
		log.Printf("淘汰巡检：读取租户列表失败 err=%v", err)
		return
	}

	start := time.Now()
	evicted, failed := 0, 0
	for _, tenantID := range ids {
		for _, mode := range []contract.SessionMode{contract.ModeTeam, contract.ModeSolo} {
			// 淘汰用短超时：它只是删对象，超过 5 分钟通常意味着对象存储异常，
			// 早退出比拖住整轮更有价值。
			//
			// 补拉不在这里：它要下载上百 MB，需要长得多的时间预算与更密的节奏，
			// 由 pullSweepSafely 单独驱动。曾经合在一起，结果补拉被迫跟着
			// 淘汰的 2 小时周期走，卡住的下载要等一两小时才被重新捡起。
			evictCtx, evictCancel := context.WithTimeout(ctx, retentionTenantTimeout)
			n, err := s.EvictTenant(evictCtx, mode, tenantID)
			evictCancel()
			if err != nil {
				log.Printf("淘汰巡检：租户 %s 分区 %s 失败 err=%v", tenantID, mode, err)
				failed++
			}
			evicted += n
		}
	}

	log.Printf("淘汰巡检完成：%d 个租户 × 2 分区，淘汰 %d 个对象，失败 %d 次，耗时 %s",
		len(ids), evicted, failed, time.Since(start).Round(time.Millisecond))
}

// localPullBatchTimeout 限制单个租户一轮补拉的总时长。
//
// 给 30 分钟：一个租户可能有多个 119MB 包要串行下载，慢链路上十几分钟很正常。
// 曾与淘汰共用 5 分钟超时，实测第 1 个包刚下完第 2 个就被取消，后续包连抢都抢不到。
const localPullBatchTimeout = 30 * time.Minute

// retentionTenantTimeout 限制单个租户的淘汰时长。
// 给 5 分钟：淘汰只是删对象，超过这个时间通常意味着对象存储异常，
// 早退出比拖住整轮更有价值。
const retentionTenantTimeout = 5 * time.Minute

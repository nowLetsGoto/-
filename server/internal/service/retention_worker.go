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

// RetentionInterval 是巡检间隔。2 小时是在"及时收敛"与"少打扰存储"之间的折中：
// 更频繁能让超额更快被纠正，但每次巡检都要遍历租户并查询候选集。
const RetentionInterval = 2 * time.Hour

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
func (s *BuildService) RunRetentionWorker(ctx context.Context, tenants retentionTenantLister) {
	if s.r2 == nil {
		log.Printf("淘汰巡检未启动：未配置对象存储")
		return
	}
	if tenants == nil {
		log.Printf("淘汰巡检未启动：缺少租户来源")
		return
	}

	go func() {
		// panic 必须兜住：这个 goroutine 一旦意外退出，巡检就永久停了，
		// 而且不会有任何日志——排查时只能看到「服务在跑但什么都没发生」，
		// 极难定位。恢复后继续下一轮，让故障表现为「某轮失败」而非「功能消失」。
		defer func() {
			if r := recover(); r != nil {
				log.Printf("巡检 goroutine 发生 panic，已恢复（下一轮继续）：%v", r)
			}
		}()

		log.Printf("巡检循环已启动，间隔 %s", RetentionInterval)
		s.sweepOnceSafely(ctx, tenants)

		ticker := time.NewTicker(RetentionInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("淘汰巡检已停止")
				return
			case <-ticker.C:
				s.sweepOnceSafely(ctx, tenants)
			}
		}
	}()
}

// sweepOnce 对所有租户的每个分区各执行一次「补拉 + 淘汰」。
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
	pulled, evicted, failed := 0, 0, 0
	for _, tenantID := range ids {
		for _, mode := range []contract.SessionMode{contract.ModeTeam, contract.ModeSolo} {
			// 两件事用各自的超时，不能共用一个：
			// 淘汰只删对象（秒级），拉回要下载上百 MB 的包（可能十几分钟）。
			// 共用一个 5 分钟曾导致实测失败——第 1 个包刚下完，第 2 个就超时，
			// 第 3、4 个连抢占都被取消，一轮白跑。
			pullCtx, pullCancel := context.WithTimeout(ctx, localPullBatchTimeout)
			n, err := s.pullPendingLocal(pullCtx, mode, tenantID)
			pullCancel()
			if err != nil {
				log.Printf("补拉本机：租户 %s 分区 %s 失败 err=%v", tenantID, mode, err)
				failed++
			}
			pulled += n

			// 淘汰保持短超时：它只是删对象，超过 5 分钟通常意味着对象存储异常，
			// 早退出比拖住整轮更有价值。
			evictCtx, evictCancel := context.WithTimeout(ctx, retentionTenantTimeout)
			n, err = s.EvictTenant(evictCtx, mode, tenantID)
			evictCancel()
			if err != nil {
				log.Printf("淘汰巡检：租户 %s 分区 %s 失败 err=%v", tenantID, mode, err)
				failed++
			}
			evicted += n
		}
	}

	log.Printf("巡检完成：%d 个租户 × 2 分区，补拉 %d 个、淘汰 %d 个对象，失败 %d 次，耗时 %s",
		len(ids), pulled, evicted, failed, time.Since(start).Round(time.Millisecond))
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

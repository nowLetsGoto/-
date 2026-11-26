// build_retention.go · 版本淘汰的决策内核。
//
// 职责：在给定的一组候选包中，算出"该淘汰哪些"。
// 输入：一个租户的全部包（含大小的快照）。
// 输出：待淘汰的包列表，按淘汰优先级排序。
//
// 边界：纯函数——不读数据库、不发网络、不删任何东西。
//
//	调用方负责把结果落到 R2 与数据库；这样算法可以穷尽单测边界，
//	而不必为每个用例搭一套存储环境。
//
// 两条约束同时生效，先到者为准：
//  1. 每个租户最多保留 retentionMaxVersions 个版本；
//  2. 每个租户的 R2 占用不超过 retentionMaxBytes。
//
// 为什么两条都要：只限个数时，300MB 的包会让总量远超预算；
// 只限容量时，一堆 1MB 的小包会撑爆列表与查询。两者互补。
package service

import (
	"sort"
	"time"
)

const (
	// retentionMaxVersions 是每个租户保留的版本数上限。
	// 与产品口径一致：单个平台管理员（单个租户、单个公司）66 个。
	retentionMaxVersions = 66

	// retentionMaxBytes 是每个租户的 R2 占用预算：9 GiB。
	// 取 9 而非常见的 10，是给免费额度留 1 GiB 余量——
	// 统计与实际之间总有抖动（并发上传、统计延迟），贴边会直接超额。
	retentionMaxBytes int64 = 9 * 1024 * 1024 * 1024
)

// RetentionCandidate 是参与淘汰决策的最小信息集。
//
// 刻意只保留决策真正需要的四件事：身份、占多少、什么时候传的、最后一次被下载是什么时候。
// 其余业务字段（应用名、版本号、项目）不影响"该不该淘汰"，
// 带进来只会让算法与业务模型耦合。
type RetentionCandidate struct {
	ID        string
	Bytes     int64 // R2 上实际占用；0 表示不在 R2，不占预算
	Pinned    bool  // 人工固定：永不淘汰
	CreatedAt time.Time
	// LastAccessedAt 为 nil 表示从未被下载过。
	// 这类包在容量紧张时优先淘汰——没人用的东西最该让位。
	LastAccessedAt *time.Time
}

// RetentionPlan 是一次决策的结果。
type RetentionPlan struct {
	// Evict 是待淘汰的候选，已按优先级排序（最先淘汰的在前）。
	Evict []RetentionCandidate
	// Keep 是保留的候选，顺序与输入无关，仅供调用方核对。
	Keep []RetentionCandidate
	// TotalBytes 是输入的 R2 总占用（含保留与待淘汰），用于日志与展示。
	TotalBytes int64
}

// PlanRetention 计算一个租户该淘汰哪些包。
//
// 决策顺序（每一步都不可交换）：
//  1. 固定的包直接进保留集——它们不参与任何容量竞争；
//  2. 其余按"最该保留的排前面"排序：最近被访问过的优先，其次最近上传的优先。
//     用访问时间作首要判据，是因为容量紧张时牺牲"没人下载的旧包"
//     比纯按时间更能保住实际价值；
//  3. 从队首依次保留，直到个数或容量任一约束被撑破；此后剩下的全部淘汰。
//
// 为什么按"保留优先级"排序而非"淘汰优先级"：保留是逐个累加的过程，
// 排在前面的必须先拿额度。若按淘汰优先级排（最该淘汰的在前），
// 就会把它们优先保留下来，方向正好相反。
func PlanRetention(candidates []RetentionCandidate) RetentionPlan {
	plan := RetentionPlan{}

	// 固定包不参与竞争，直接保留。
	var evictable []RetentionCandidate
	for _, c := range candidates {
		plan.TotalBytes += c.Bytes
		if c.Pinned {
			plan.Keep = append(plan.Keep, c)
			continue
		}
		evictable = append(evictable, c)
	}

	// 保留优先级：最近访问 → 最近上传。
	// LastAccessedAt 为 nil（从未被下载）价值最低，排在最后。
	sort.SliceStable(evictable, func(i, j int) bool {
		a, b := evictable[i], evictable[j]
		if a.LastAccessedAt == nil || b.LastAccessedAt == nil {
			if a.LastAccessedAt == nil && b.LastAccessedAt == nil {
				return a.CreatedAt.After(b.CreatedAt)
			}
			// 有访问记录的优先于从未访问的。
			return a.LastAccessedAt != nil
		}
		if !a.LastAccessedAt.Equal(*b.LastAccessedAt) {
			return a.LastAccessedAt.After(*b.LastAccessedAt)
		}
		return a.CreatedAt.After(b.CreatedAt)
	})

	// 固定包也占额度——它们虽不可淘汰，但空间是实打实的。
	var keptBytes int64
	for _, c := range plan.Keep {
		keptBytes += c.Bytes
	}
	keptCount := len(plan.Keep)

	// 从最有价值的开始保留，一旦某个包放不下，它和它之后的全部淘汰。
	// 只截断一次而不是逐条判断，是为了保证"价值更高的一定优先于价值更低的"：
	// 否则会出现"留下一个更大的旧包却删掉一个更小的新包"这种反直觉结果。
	for i, c := range evictable {
		if keptCount+1 > retentionMaxVersions || keptBytes+c.Bytes > retentionMaxBytes {
			plan.Evict = append(plan.Evict, evictable[i:]...)
			break
		}
		plan.Keep = append(plan.Keep, c)
		keptCount++
		keptBytes += c.Bytes
	}

	return plan
}

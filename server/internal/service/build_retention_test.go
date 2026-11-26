// build_retention_test.go · 淘汰算法的边界验证。
//
// 职责：锁死"该淘汰谁"的判定，防止后续改动悄悄改变保留口径。
// 输入：构造的候选快照。
// 输出：对保留/淘汰集合与顺序的断言。
//
// 边界：只测纯函数，不碰数据库与存储，因此每个用例都能精确控制
//
//	个数、字节、固定标记与时间，不受环境影响。
package service

import (
	"fmt"
	"testing"
	"time"
)

// at 构造一个固定时刻，让用例里的时间关系一眼可读。
func at(hour int) time.Time {
	return time.Date(2026, 11, 26, hour, 0, 0, 0, time.UTC)
}

func atPtr(hour int) *time.Time {
	t := at(hour)
	return &t
}

// ids 提取候选 id，便于断言集合内容。
func ids(candidates []RetentionCandidate) []string {
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.ID
	}
	return out
}

// assertSameSet 断言集合内容一致，但忽略顺序。
func assertSameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 数量不符 got=%v want=%v", label, got, want)
	}
	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("%s: 缺少 %s，got=%v want=%v", label, id, got, want)
		}
	}
}

// TestPlanRetentionKeepsEverythingUnderLimits 验证未超限时一个都不淘汰。
func TestPlanRetentionKeepsEverythingUnderLimits(t *testing.T) {
	candidates := []RetentionCandidate{
		{ID: "a", Bytes: 100, CreatedAt: at(1), LastAccessedAt: atPtr(2)},
		{ID: "b", Bytes: 200, CreatedAt: at(2), LastAccessedAt: atPtr(3)},
	}
	plan := PlanRetention(candidates)
	if len(plan.Evict) != 0 {
		t.Fatalf("未超限不应淘汰，got=%v", ids(plan.Evict))
	}
	assertSameSet(t, "保留", ids(plan.Keep), []string{"a", "b"})
	if plan.TotalBytes != 300 {
		t.Fatalf("总字节应为 300，got=%d", plan.TotalBytes)
	}
}

// TestPlanRetentionTrimsByCount 验证超过个数上限时淘汰最久未访问的。
// 造 68 个包：访问时间按序号递增，于是前 2 个（最久未访问）该被淘汰。
func TestPlanRetentionTrimsByCount(t *testing.T) {
	candidates := make([]RetentionCandidate, 68)
	for i := range candidates {
		candidates[i] = RetentionCandidate{
			ID:             fmt.Sprintf("p%02d", i),
			Bytes:          1,
			CreatedAt:      at(0).Add(time.Duration(i) * time.Minute),
			LastAccessedAt: atPtr(i), // i 越小越久未被访问
		}
	}
	plan := PlanRetention(candidates)
	if len(plan.Evict) != 2 {
		t.Fatalf("68 个应淘汰 2 个，got=%d(%v)", len(plan.Evict), ids(plan.Evict))
	}
	if len(plan.Keep) != retentionMaxVersions {
		t.Fatalf("应保留 %d 个，got=%d", retentionMaxVersions, len(plan.Keep))
	}
	assertSameSet(t, "淘汰", ids(plan.Evict), []string{"p00", "p01"})
}

// TestPlanRetentionEvictsNeverAccessedFirst 验证从未被下载的包优先让位。
// 场景：三个包各占预算的 40%，合计 120% 必超；容量只够放两个。
// 常被下载的老包与刚传的新包都有访问记录，未下载的那个该被淘汰。
func TestPlanRetentionEvictsNeverAccessedFirst(t *testing.T) {
	share := retentionMaxBytes * 2 / 5 // 每个占 40%，两个 80% 放得下，三个 120% 超
	candidates := []RetentionCandidate{
		// 传得最早但常被下载：价值最高，必须保留。
		{ID: "hot-old", Bytes: share, CreatedAt: at(1), LastAccessedAt: atPtr(23)},
		// 传得晚且刚被下载过：次高，也应保留。
		{ID: "warm-new", Bytes: share, CreatedAt: at(10), LastAccessedAt: atPtr(22)},
		// 从未被下载：价值最低，该淘汰。
		{ID: "cold-never", Bytes: share, CreatedAt: at(5), LastAccessedAt: nil},
	}
	plan := PlanRetention(candidates)
	if len(plan.Evict) != 1 {
		t.Fatalf("容量只够两个，应淘汰 1 个，got=%v", ids(plan.Evict))
	}
	if plan.Evict[0].ID != "cold-never" {
		t.Fatalf("从未下载的包应被淘汰，got=%s", plan.Evict[0].ID)
	}
	assertSameSet(t, "保留", ids(plan.Keep), []string{"hot-old", "warm-new"})
}

// TestPlanRetentionNeverEvictsPinned 验证固定包永不淘汰，即使超额。
func TestPlanRetentionNeverEvictsPinned(t *testing.T) {
	candidates := []RetentionCandidate{
		{ID: "pinned-huge", Bytes: retentionMaxBytes + 1, Pinned: true, CreatedAt: at(1), LastAccessedAt: atPtr(2)},
		{ID: "free-small", Bytes: 1, Pinned: false, CreatedAt: at(2), LastAccessedAt: atPtr(3)},
	}
	plan := PlanRetention(candidates)
	if len(plan.Evict) != 1 {
		t.Fatalf("应淘汰 1 个（非固定），got=%v", ids(plan.Evict))
	}
	if plan.Evict[0].ID != "free-small" {
		t.Fatalf("固定包不应被淘汰，got=%s", plan.Evict[0].ID)
	}
	assertSameSet(t, "保留", ids(plan.Keep), []string{"pinned-huge"})
}

// TestPlanRetentionAllPinnedOverBudgetEvictsNothing 验证全部固定时宁可超额也不删。
// 这是有意为之：固定是人工判断"这个不能删"，算法无权覆盖人的决定。
func TestPlanRetentionAllPinnedOverBudgetEvictsNothing(t *testing.T) {
	candidates := []RetentionCandidate{
		{ID: "a", Bytes: retentionMaxBytes, Pinned: true, CreatedAt: at(1)},
		{ID: "b", Bytes: retentionMaxBytes, Pinned: true, CreatedAt: at(2)},
	}
	plan := PlanRetention(candidates)
	if len(plan.Evict) != 0 {
		t.Fatalf("全部固定时不应淘汰，got=%v", ids(plan.Evict))
	}
}

// TestPlanRetentionEmptyInput 验证空输入不 panic。
func TestPlanRetentionEmptyInput(t *testing.T) {
	plan := PlanRetention(nil)
	if len(plan.Evict) != 0 || len(plan.Keep) != 0 || plan.TotalBytes != 0 {
		t.Fatalf("空输入应得空结果，got=%+v", plan)
	}
}

// TestPlanRetentionZeroBytesDoesNotTriggerByteLimit 验证 r2_bytes=0 不占预算。
// 这类包还没同步或已被淘汰，占 0 字节，只受个数约束。
func TestPlanRetentionZeroBytesDoesNotTriggerByteLimit(t *testing.T) {
	candidates := make([]RetentionCandidate, 3)
	for i := range candidates {
		candidates[i] = RetentionCandidate{
			ID: "z" + string(rune('a'+i)), Bytes: 0, CreatedAt: at(i), LastAccessedAt: atPtr(i),
		}
	}
	plan := PlanRetention(candidates)
	if len(plan.Evict) != 0 {
		t.Fatalf("0 字节不应触发容量淘汰，got=%v", ids(plan.Evict))
	}
}

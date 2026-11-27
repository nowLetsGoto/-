// r2_ref_test.go · 按公司持有者的并发与隔离语义测试。
//
// 职责：锁定三条性质——公司之间互不串、替换后新调用生效、已取快照不受影响。
// 输入：构造的持有者与若干客户端占位值。
// 输出：对 Get / Set 返回值的断言，以及竞态检测下的并发结论。
// 边界：不连 R2、不构造真实客户端（用零值指针区分身份即可）。
//
// 为什么三条都要测：
//   - 串了公司 → A 公司的包被写进 B 公司的桶，是不可挽回的数据归属错误；
//   - 替换不生效 → 页面保存了配置但实际仍在用旧凭据；
//   - 快照被影响 → 下载到一半失败，且无法复现（取决于保存配置的时机）。
package storage

import (
	"fmt"
	"sync"
	"testing"
)

// testClient 返回一个可作身份标记的客户端。
// 不用真实构造：NewR2 需要凭据，而这里只想验证指针语义。
func testClient() *R2 { return &R2{} }

const (
	tenantA = "11111111-1111-4111-8111-111111111111"
	tenantB = "22222222-2222-4222-8222-222222222222"
)

func TestR2RefNilSafe(t *testing.T) {
	// nil 持有者必须安全：装配层在未配置任何 R2 时也会传它进来，
	// 若这里 panic，会让「完全不用 R2」的部署直接崩溃。
	var ref *R2Ref
	if got := ref.Get(tenantA); got != nil {
		t.Fatalf("nil 持有者的 Get 应为 nil，got %v", got)
	}
	if ref.Enabled(tenantA) {
		t.Fatal("nil 持有者不应报告已启用")
	}
	ref.Set(tenantA, testClient()) // 不应 panic
	if ref.TenantCount() != 0 {
		t.Fatal("nil 持有者的公司数应为 0")
	}
}

func TestR2RefIsolatesTenants(t *testing.T) {
	ref := NewR2Ref()
	a := testClient()
	ref.Set(tenantA, a)

	if ref.Get(tenantA) != a {
		t.Fatal("A 公司应取到自己设置的客户端")
	}
	// 这是本设计的核心断言：B 公司没配就不能拿到 A 的客户端。
	// 若此处返回非 nil，B 公司的包会进入 A 公司的桶。
	if got := ref.Get(tenantB); got != nil {
		t.Fatal("B 公司未配置却拿到了客户端，公司隔离失效")
	}
	if ref.Enabled(tenantB) {
		t.Fatal("B 公司未配置不应报告已启用（严格隔离，无全局兜底）")
	}
}

func TestR2RefReplace(t *testing.T) {
	ref := NewR2Ref()
	ref.Set(tenantA, testClient())

	second := testClient()
	ref.Set(tenantA, second)
	if ref.Get(tenantA) != second {
		t.Fatal("替换后应返回新客户端")
	}

	// 关闭 A 不应影响 B。
	b := testClient()
	ref.Set(tenantB, b)
	ref.Set(tenantA, nil)
	if ref.Get(tenantA) != nil || ref.Enabled(tenantA) {
		t.Fatal("设为 nil 后应报告未启用")
	}
	if ref.Get(tenantB) != b {
		t.Fatal("关闭 A 不应影响 B 的客户端")
	}
}

func TestR2RefSnapshotUnaffectedByReplace(t *testing.T) {
	ref := NewR2Ref()
	first := testClient()
	ref.Set(tenantA, first)

	// 模拟一次长任务的开头：取快照后用局部变量跑完全程。
	snapshot := ref.Get(tenantA)

	// 任务进行中，管理员在页面上保存了新配置。
	ref.Set(tenantA, testClient())

	// 快照必须仍是旧客户端：这是「在途任务不受切换影响」的直接断言。
	if snapshot != first {
		t.Fatal("已取快照被替换影响，在途下载会在中途换凭据而失败")
	}
	// 同时新调用必须拿到新值，证明替换本身生效了。
	if ref.Get(tenantA) == first {
		t.Fatal("替换未生效，新调用仍拿到旧客户端")
	}
}

func TestR2RefConcurrentSetAndGet(t *testing.T) {
	// 并发读写必须无竞态。用 -race 运行时这道测试会真正检查数据竞争。
	ref := NewR2Ref()
	ref.Set(tenantA, testClient())

	var wg sync.WaitGroup
	const rounds = 200

	// 读侧：多个 goroutine 同时读不同公司，不应读到半状态。
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tenant := tenantA
			if i%2 == 1 {
				tenant = tenantB
			}
			for j := 0; j < rounds; j++ {
				_ = ref.Get(tenant)
			}
		}(i)
	}
	// 写侧：同时替换，模拟管理员反复保存。
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				ref.Set(tenantB, testClient())
				ref.Set(tenantA, testClient())
			}
		}()
	}
	wg.Wait()

	if got := ref.TenantCount(); got != 2 {
		t.Fatalf("两家公司都应存在，实际 %d", got)
	}
}

func TestR2RefTenantCount(t *testing.T) {
	ref := NewR2Ref()
	if ref.TenantCount() != 0 {
		t.Fatal("初始应为 0")
	}
	for i := 0; i < 3; i++ {
		ref.Set(fmt.Sprintf("tenant-%d", i), testClient())
	}
	if got := ref.TenantCount(); got != 3 {
		t.Fatalf("应为 3，实际 %d", got)
	}
}

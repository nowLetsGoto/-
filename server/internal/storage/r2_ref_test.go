// r2_ref_test.go · 可替换持有者的并发语义测试。
//
// 职责：锁定两条性质——「切换后新调用拿到新值」与「已取快照的调用不受影响」。
// 输入：构造的持有者与若干客户端占位值。
// 输出：对 Get / Swap 返回值的断言，以及竞态检测下的并发结论。
// 边界：不连 R2、不构造真实客户端（用零值指针区分身份即可）。
//
// 为什么必须测第二条：R2 配置保存后立即热生效，而下载动辄持续数分钟。
// 若切换会让在途下载中途换凭据，用户看到的是「下载到一半失败」，
// 且无法复现（取决于保存配置的时机）。这条性质是本设计能成立的前提。
//
// 用 R2 的零值指针作身份标记：测试只需要区分「是哪一个」，不关心字段内容。
package storage

import (
	"sync"
	"testing"
)

// testClient 返回一个可作身份标记的客户端。
// 不用真实构造：NewR2 需要网络凭据，而这里只想验证指针语义。
func testClient() *R2 { return &R2{} }

func TestR2RefNilSafe(t *testing.T) {
	// nil 持有者必须安全：装配层在某些路径可能传 nil，
	// 若这里 panic，会让"未配置 R2"的部署直接崩溃而不是降级。
	var ref *R2Ref
	if got := ref.Get(); got != nil {
		t.Fatalf("nil 持有者的 Get 应为 nil，got %v", got)
	}
	if ref.Enabled() {
		t.Fatal("nil 持有者不应报告已启用")
	}
	if got := ref.Swap(testClient()); got != nil {
		t.Fatal("nil 持有者的 Swap 应返回 nil")
	}
}

func TestR2RefSwap(t *testing.T) {
	first := testClient()
	ref := NewR2Ref(first)

	if ref.Get() != first {
		t.Fatal("初始值应为构造时传入的客户端")
	}
	if !ref.Enabled() {
		t.Fatal("有客户端时应报告已启用")
	}

	second := testClient()
	old := ref.Swap(second)
	if old != first {
		t.Fatal("Swap 应返回被替换掉的旧客户端")
	}
	if ref.Get() != second {
		t.Fatal("Swap 后 Get 应返回新客户端")
	}

	// 换回 nil 表示关闭：此后新操作必须降级为本地行为。
	ref.Swap(nil)
	if ref.Get() != nil || ref.Enabled() {
		t.Fatal("Swap(nil) 后应报告未启用")
	}
}

func TestR2RefSnapshotUnaffectedBySwap(t *testing.T) {
	first := testClient()
	ref := NewR2Ref(first)

	// 模拟一次长任务的开头：取快照后用局部变量跑完全程。
	snapshot := ref.Get()

	// 任务进行中，管理员在页面上保存了新配置。
	ref.Swap(testClient())

	// 快照必须仍是旧客户端：这是「在途任务不受切换影响」的直接断言。
	if snapshot != first {
		t.Fatal("已取快照被切换影响，在途下载会在中途换凭据而失败")
	}
	// 同时新调用必须拿到新值，证明切换本身生效了。
	if ref.Get() == first {
		t.Fatal("切换未生效，新调用仍拿到旧客户端")
	}
}

func TestR2RefConcurrentSwapAndGet(t *testing.T) {
	// 并发读写必须无竞态。用 -race 运行时这道测试会真正检查数据竞争；
	// 不用 mutex 而用原子指针，正是为了让读路径无锁——下载热路径每次操作都会 Get。
	ref := NewR2Ref(testClient())

	var wg sync.WaitGroup
	const workers = 8
	const rounds = 200

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				// 读侧：只做 Get，不应被写侧阻塞。
				if c := ref.Get(); c != nil {
					_ = c
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				ref.Swap(testClient())
			}
		}()
	}
	wg.Wait()
}

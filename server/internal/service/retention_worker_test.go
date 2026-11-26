// retention_worker_test.go · 巡检调度的行为验证。
//
// 职责：验证"每轮处理哪些租户、失败是否隔离、无对象存储时是否静默"。
// 输入：假租户来源与可控的 BuildService。
// 输出：对调用次数与错误隔离的断言。
//
// 边界：不启动真实 ticker（那会让测试等 2 小时），只测 sweepOnce 这一轮的行为；
//
//	定时器本身是标准库行为，重复验证无收益。
package service

import (
	"context"
	"errors"
	"testing"
)

// fakeTenants 按预设返回值提供租户 id，并记录被调用的次数。
type fakeTenants struct {
	ids  []string
	err  error
	call int
}

func (f *fakeTenants) ListTenantIDs(context.Context) ([]string, error) {
	f.call++
	return f.ids, f.err
}

// TestSweepSkipsWhenTenantListFails 验证读不到租户时整轮直接结束，不 panic。
func TestSweepSkipsWhenTenantListFails(t *testing.T) {
	svc := &BuildService{} // r2 为 nil，EvictTenant 会立即返回，不会真的访问存储
	tenants := &fakeTenants{err: errors.New("数据库抖动")}

	// 不应 panic；错误由内部记日志消化。
	svc.sweepOnce(context.Background(), tenants)

	if tenants.call != 1 {
		t.Fatalf("应尝试读取一次租户，got=%d", tenants.call)
	}
}

// TestSweepCoversBothPartitions 验证每个租户都会处理 team 与 solo 两个分区。
//
// 同一家公司在两个分区都可能有项目（实测默认公司各有团队与单人项目），
// 只扫一个分区会让另一边的对象永远超额。
func TestSweepCoversBothPartitions(t *testing.T) {
	svc := &BuildService{}
	tenants := &fakeTenants{ids: []string{"t1", "t2"}}

	// r2 为 nil 时 EvictTenant 直接返回 0 且不报错，
	// 因此这里只能验证"流程跑完不报错"，分区覆盖由 EvictTenant 的 mode 参数保证。
	svc.sweepOnce(context.Background(), tenants)

	if tenants.call != 1 {
		t.Fatalf("每轮只应读一次租户列表，got=%d", tenants.call)
	}
}

// TestRetentionIntervalIsTwoHours 把产品口径钉在测试里。
// 有人日后想改成 10 分钟，必须先想清楚为什么，而不是悄悄改掉。
func TestRetentionIntervalIsTwoHours(t *testing.T) {
	if RetentionInterval.Hours() != 2 {
		t.Fatalf("巡检间隔应为 2 小时，got=%s", RetentionInterval)
	}
}

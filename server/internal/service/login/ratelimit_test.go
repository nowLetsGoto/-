// ratelimit_test.go · 验证登录失败限流的计数、锁定、解锁与清零。
// 用注入的时钟跨越 15 分钟，避免测试真的等下去。
package login

import (
	"testing"
	"time"
)

// newTestLimiter 构造一个可以手动推进时间的限流器。
func newTestLimiter() (*Limiter, *time.Time) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	l := NewLimiter()
	l.now = func() time.Time { return now }
	return l, &now
}

func TestLimiterLocksAfterMaxFailures(t *testing.T) {
	l, _ := newTestLimiter()

	// 前 4 次失败：还没到阈值，仍然允许尝试。
	for i := 1; i <= 4; i++ {
		locked := l.Fail("alice", "1.2.3.4")
		if locked {
			t.Fatalf("第 %d 次失败就锁定了，阈值应为 5", i)
		}
		if ok, _ := l.Allowed("alice", "1.2.3.4"); !ok {
			t.Fatalf("第 %d 次失败后不该被拦", i)
		}
	}

	// 第 5 次失败：开始锁定。
	if locked := l.Fail("alice", "1.2.3.4"); !locked {
		t.Fatal("第 5 次失败应触发锁定")
	}
	ok, wait := l.Allowed("alice", "1.2.3.4")
	if ok {
		t.Fatal("锁定期间应拒绝登录")
	}
	if wait <= 0 || wait > 15*time.Minute {
		t.Fatalf("剩余锁定时间异常: %s", wait)
	}
}

func TestLimiterUnlocksAfterDuration(t *testing.T) {
	l, now := newTestLimiter()
	for i := 0; i < 5; i++ {
		l.Fail("bob", "1.2.3.4")
	}
	if ok, _ := l.Allowed("bob", "1.2.3.4"); ok {
		t.Fatal("应处于锁定状态")
	}

	// 推进 15 分零 1 秒：锁定应解除。
	*now = now.Add(15*time.Minute + time.Second)
	if ok, _ := l.Allowed("bob", "1.2.3.4"); !ok {
		t.Fatal("锁定到期后应允许再试")
	}
}

func TestLimiterScopedByAccountAndIP(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 5; i++ {
		l.Fail("carol", "1.1.1.1")
	}

	// 同账号换 IP：不受影响（避免别人用一个 IP 打满就把真用户锁死）。
	if ok, _ := l.Allowed("carol", "2.2.2.2"); !ok {
		t.Fatal("换 IP 后不该继承另一个 IP 的失败计数")
	}
	// 同 IP 换账号：也不受影响（避免一个代理 IP 被用来锁死所有账号）。
	if ok, _ := l.Allowed("dave", "1.1.1.1"); !ok {
		t.Fatal("换账号后不该继承另一个账号的失败计数")
	}
}

func TestLimiterSucceedResets(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 4; i++ {
		l.Fail("erin", "1.1.1.1")
	}
	// 第 4 次后登录成功：计数清零，不能"差一次就锁"地背着走。
	l.Succeed("erin", "1.1.1.1")

	for i := 1; i <= 4; i++ {
		if locked := l.Fail("erin", "1.1.1.1"); locked {
			t.Fatalf("清零后第 %d 次失败不该锁定", i)
		}
	}
	// 第 5 次才该锁。
	if locked := l.Fail("erin", "1.1.1.1"); !locked {
		t.Fatal("清零后重新累计到 5 次应锁定")
	}
}

func TestLimiterUsernameCaseInsensitive(t *testing.T) {
	l, _ := newTestLimiter()
	// 大小写不同不该被当成两个账号各算 5 次机会。
	for i := 0; i < 5; i++ {
		l.Fail("Frank", "1.1.1.1")
	}
	if ok, _ := l.Allowed("frank", "1.1.1.1"); ok {
		t.Fatal("账号名应大小写不敏感，否则可以靠变换大小写绕过限流")
	}
}

func TestLimiterSweepsExpiredEntries(t *testing.T) {
	l, now := newTestLimiter()
	l.Fail("ghost", "1.1.1.1")
	if len(l.entries) != 1 {
		t.Fatalf("应有一条记账，实际 %d", len(l.entries))
	}

	// 推进到超过条目保留期，再触发一次会调用 sweep 的操作。
	*now = now.Add(2 * time.Hour)
	l.Allowed("someone-else", "2.2.2.2")

	if len(l.entries) != 0 {
		t.Fatalf("过期条目应被清理，实际还剩 %d 条", len(l.entries))
	}
}

// 登录失败的限流与锁定（login 包的一部分）。
// 职责：按「账号 + 来源 IP」记录连续失败次数，超过阈值后在一段时间内拒绝该组合继续尝试；
//
//	登录成功后清零。
//
// 输入：账号名、IP；输出：是否允许本次尝试、失败/成功后的记账调用。
// 边界：**只防在线爆破，不防分布式撞库**；不持久化（进程重启即清零）；
//
//	单实例内存实现——多实例部署时各实例各算各的，实际阈值会被放大 N 倍，
//	那时应换成 Redis 或数据库计数。当前是单实例部署，够用且零依赖。
//
// 为什么按「账号 + IP」而不是只按账号：只按账号会让攻击者用错误密码锁死别人的账号（拒绝服务）；
// 只按 IP 则换个代理就绕过。两个维度取交集，既拖慢爆破又不会误伤同公司其他人。
package login

import (
	"sync"
	"time"
)

const (
	// maxFailures 是允许的连续失败次数：第 5 次失败后开始锁定。
	maxFailures = 5
	// lockDuration 是锁定时长：15 分钟足够让在线爆破变得不划算，又不至于让真用户等太久。
	lockDuration = 15 * time.Minute
	// entryTTL 是计数条目的保留时长：超过这个时间没再失败就从表里清掉，
	// 避免长期运行后 map 无限增长（每个从没成功过的账号名都会留一条）。
	entryTTL = 1 * time.Hour
)

// entry 是某个「账号 + IP」组合的失败记账。
type entry struct {
	failures    int
	lockedUntil time.Time
	lastFailure time.Time
}

// Limiter 是登录失败的记账簿。零值不可用，请用 New。
type Limiter struct {
	mu        sync.Mutex
	entries   map[string]*entry
	now       func() time.Time // 可注入，便于单测跨越 15 分钟
	maxFail   int
	lockFor   time.Duration
	entryLife time.Duration
	// lastSweep 上次清理时间：把清理摊到每次记账上，避免单独起 goroutine 做定时任务。
	lastSweep time.Time
}

// NewLimiter 构造一个限流器。
func NewLimiter() *Limiter {
	return &Limiter{
		entries:   make(map[string]*entry),
		now:       time.Now,
		maxFail:   maxFailures,
		lockFor:   lockDuration,
		entryLife: entryTTL,
	}
}

// key 把账号与 IP 拼成记账键。
// 账号统一转小写：否则「Admin」和「admin」会被当成两个账号各试 5 次。
func key(username, ip string) string {
	return normalize(username) + "|" + ip
}

// normalize 与登录时的账号处理保持一致（去空白 + 转小写）。
func normalize(username string) string {
	trimmed := make([]byte, 0, len(username))
	for i := 0; i < len(username); i++ {
		c := username[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		trimmed = append(trimmed, c)
	}
	return string(trimmed)
}

// Allowed 判断该「账号 + IP」当前是否允许尝试登录。
// 返回 false 时还会给出还需等待多久（用于日志与响应文案）。
func (l *Limiter) Allowed(username, ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked()

	e, ok := l.entries[key(username, ip)]
	if !ok {
		return true, 0
	}
	now := l.now()
	if e.lockedUntil.After(now) {
		return false, e.lockedUntil.Sub(now)
	}
	return true, 0
}

// Fail 记一次失败；达到阈值时开始锁定，并返回当前是否已锁。
func (l *Limiter) Fail(username, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	k := key(username, ip)
	now := l.now()
	e, ok := l.entries[k]
	if !ok {
		e = &entry{}
		l.entries[k] = e
	}
	e.failures++
	e.lastFailure = now
	if e.failures >= l.maxFail {
		e.lockedUntil = now.Add(l.lockFor)
		return true
	}
	return false
}

// Succeed 在登录成功后清零：用户想起来密码了，不该继续背着之前的失败记录。
func (l *Limiter) Succeed(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key(username, ip))
}

// sweepLocked 清掉过期条目。调用方必须已持锁。
// 每分钟最多扫一次，避免每次登录都全表遍历。
func (l *Limiter) sweepLocked() {
	now := l.now()
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for k, e := range l.entries {
		// 已解锁且长时间没再失败 → 可以忘掉这个组合。
		if e.lockedUntil.Before(now) && now.Sub(e.lastFailure) > l.entryLife {
			delete(l.entries, k)
		}
	}
}

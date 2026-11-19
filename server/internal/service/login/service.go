// Package login · 登录与令牌的业务层。
// 职责：校验账号密码（PBKDF2 派生比对）、签发带版本号的 HMAC 令牌、校验令牌并返回调用者身份、
//
//	首次启动时把环境变量里的管理员作为种子写进 users 表。
//
// 输入：config 的种子账号与签名密钥、repository/user 的读写能力。
// 输出：contract.LoginResult / contract.Identity，失败给 contract.ErrInvalidCredentials。
// 边界：不碰 HTTP、不打日志（凭据与密钥绝不外泄）；
//
//	令牌无服务端会话，但带 token_version —— 改密码/禁用/强制下线只需把版本 +1 即可让旧令牌失效。
package login

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	userrepo "dandelion/server/internal/repository/user"
)

// 密码派生参数：10 万次迭代在登录场景约 60~90ms，足以拖慢离线爆破。
const (
	hashAlgorithm  = "pbkdf2-sha256"
	hashIterations = 100_000
	hashKeyLength  = 32
	hashSaltLength = 16
)

// tenantStatus 是登录链路需要的最小公司能力。
// 声明成窄接口而不是直接依赖 tenant 仓储：本包只用"公司还启用着吗"这一个判断，
// 窄接口让单测可以注入假实现，也不必为一次登录把整个公司模块拉进来。
type tenantStatus interface {
	IsActive(ctx context.Context, tenantID string) (bool, error)
}

// Service 是账号鉴权服务。
type Service struct {
	users        *userrepo.Repository
	tenants      tenantStatus
	secret       []byte
	ttl          time.Duration
	seedUsername string
	seedPassword string
	// limiter 记账登录失败，防在线爆破。单实例内存实现，见 ratelimit.go 的边界说明。
	limiter *Limiter
}

// NewService 从配置、账号仓储与公司状态读取器构造；种子凭据只在首次启动建号时使用。
func NewService(cfg config.Config, users *userrepo.Repository, tenants tenantStatus) *Service {
	return &Service{
		users:        users,
		tenants:      tenants,
		secret:       []byte(cfg.AuthSecret),
		ttl:          contract.AuthTokenTTLSeconds * time.Second,
		seedUsername: cfg.AdminUsername,
		seedPassword: cfg.AdminPassword,
		limiter:      NewLimiter(),
	}
}

// EnsureAdmin 首次启动时把环境变量里的管理员写进 users 表（users 为空才写）。
// 之后账号只在页面/库里维护，环境变量仅作为「首次种子」，改它不会再影响已有账号。
//
// 同时对齐存量超管的层级：is_super_admin 为真但 account_level 不是 super_admin 的账号
// （account_level 是 P0 才加的列，早于它的账号会停留在这个不一致状态）。
// 运行时靠 EffectiveAccountLevel 折算不影响功能，但数据层应当自洽——
// 否则任何直接读 account_level 的统计/报表都会把超管算成普通成员。
func (s *Service) EnsureAdmin(ctx context.Context) error {
	if _, err := s.users.AlignSuperAdminLevel(ctx); err != nil {
		return err
	}
	// 顺手禁用历史遗留的「无公司平台管理员」：曾经的账号接口漏洞产物，
	// 能登录但读不到任何数据（scope 落到 1 = 0），留着只会在账号列表里制造困惑。
	// 只禁用不删除，理由见仓储里该方法上的说明。
	if count, err := s.users.DisableOrphanPlatformAdmins(ctx); err != nil {
		return err
	} else if count > 0 {
		log.Printf("已禁用 %d 个无公司归属的平台管理员账号（历史遗留，无可用数据范围）", count)
	}

	total, err := s.users.Count(ctx)
	if err != nil {
		return err
	}
	if total > 0 {
		return nil
	}

	hash, err := hashPassword(s.seedPassword)
	if err != nil {
		return fmt.Errorf("生成种子密码失败: %w", err)
	}
	// 种子管理员是平台级超管：层级 super_admin、无公司归属；不绑人员、不挂团队。
	if _, err := s.users.Create(ctx, s.seedUsername, hash, contract.ModeTeam, "", "", true,
		contract.AccountLevelSuperAdmin, "", nil); err != nil {
		return fmt.Errorf("创建种子管理员失败: %w", err)
	}
	return nil
}

// Authenticate 校验凭据并签发令牌。
// 账号不存在与密码错误返回同一个错误，避免被用来枚举账号。
//
// 失败限流：同一「账号 + IP」连续失败 5 次后锁定 15 分钟。
// 锁定检查放在密码校验之前——否则爆破者至少能通过响应时间差判断账号是否存在。
// 锁定时返回的错误与凭据错误完全相同，不告诉攻击者「你被锁了」，
// 只在服务端日志里能看到（运维需要知道有人在爆破）。
func (s *Service) Authenticate(ctx context.Context, username, password, ip string, mode contract.SessionMode) (contract.LoginResult, error) {
	if allowed, wait := s.limiter.Allowed(username, ip); !allowed {
		// 明确返回「被限流」而不是含糊的「密码错误」：不存在的账号同样会被计数，
		// 所以这个回答不泄漏账号是否存在；而含糊其辞会让真实用户以为密码记错了，
		// 反复重试只会把锁定时间续下去。
		log.Printf("登录被限流：username=%s ip=%s 剩余锁定 %s",
			strings.TrimSpace(username), ip, wait.Round(time.Second))
		return contract.LoginResult{}, contract.ErrTooManyAttempts
	}

	record, err := s.users.FindByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			// 账号不存在也记账：否则攻击者可以靠「这个账号不锁」识别出哪些账号真实存在。
			s.limiter.Fail(username, ip)
			return contract.LoginResult{}, contract.ErrInvalidCredentials
		}
		return contract.LoginResult{}, err
	}
	if record.User.Status != statusActive {
		return contract.LoginResult{}, contract.ErrInvalidCredentials
	}
	// 公司被停用时，该公司所有账号都不能登录。
	// 校验放在密码校验**之后**：否则"这家公司被停用了"这个消息会先于密码正确性泄露出去，
	// 变成一种账号探测手段。密码错了就是密码错，与公司状态无关。
	if !verifyPassword(record.PasswordHash, password) {
		if locked := s.limiter.Fail(username, ip); locked {
			log.Printf("登录失败次数超限，已临时锁定 username=%s ip=%s", strings.TrimSpace(username), ip)
		}
		return contract.LoginResult{}, contract.ErrInvalidCredentials
	}
	// 密码正确：清掉该组合的失败记录（公司停用是管理状态，不该让人继续背着失败计数）。
	s.limiter.Succeed(username, ip)

	active, err := s.tenants.IsActive(ctx, record.TenantID)
	if err != nil {
		return contract.LoginResult{}, err
	}
	if !active {
		// 用专门的错误而不是"密码错误"：此时凭据是对的，含糊其辞只会让用户反复重试。
		// 不泄露敏感信息——能走到这里说明用户名与密码都正确，他本来就是这家公司的人。
		return contract.LoginResult{}, contract.ErrTenantDisabled
	}

	mode, err = contract.NormalizeMode(mode)
	if err != nil {
		return contract.LoginResult{}, err
	}
	expiresAt := time.Now().Add(s.ttl)
	token, err := s.issue(record.User.Username, record.TokenVersion, expiresAt.Unix(), mode)
	if err != nil {
		return contract.LoginResult{}, err
	}

	// 登录信息是旁路：写失败不影响本次登录。
	s.users.TouchLogin(ctx, record.User.ID, ip)

	// 身份层级随令牌响应一并返回给前端首屏；真正权威的身份是 Verify 每次请求重建的那份。
	// 折算放在这里而不是前端：旧账号的 is_super_admin 布尔必须在服务端收敛成四级枚举。
	accountLevel := contract.EffectiveAccountLevel(record.AccountLevel, record.User.IsSuperAdmin)

	return contract.LoginResult{
		Token:        token,
		Username:     record.User.Username,
		ExpiresAt:    expiresAt.UTC().Format(time.RFC3339),
		Mode:         mode,
		AccountLevel: accountLevel,
		TenantID:     record.TenantID,
	}, nil
}

// Verify 校验令牌：签名 → 有效期 → 账号状态 → 令牌版本。
// 版本不一致说明该账号改过密码、被禁用或被强制下线，旧令牌立即作废。
func (s *Service) Verify(ctx context.Context, token string) (contract.Identity, bool) {
	payload, signature, found := strings.Cut(token, ".")
	if !found || payload == "" || signature == "" {
		return contract.Identity{}, false
	}
	// hmac.Equal 是定长比较，防止通过签名比对耗时反推正确签名。
	if !hmac.Equal([]byte(signature), []byte(s.mac(payload))) {
		return contract.Identity{}, false
	}

	parts := strings.Split(payload, "|")
	if len(parts) != 4 {
		return contract.Identity{}, false
	}
	version, err := strconv.Atoi(parts[1])
	if err != nil {
		return contract.Identity{}, false
	}
	expiresAt, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().Unix() >= expiresAt {
		return contract.Identity{}, false
	}

	mode := contract.SessionMode(parts[3])
	if !mode.IsValid() {
		return contract.Identity{}, false
	}

	record, err := s.users.FindByUsername(ctx, parts[0])
	if err != nil || record.User.Status != statusActive || record.TokenVersion != version {
		return contract.Identity{}, false
	}
	// 身份每次请求都从库里重建：层级、租户与超管状态随账号变更立即生效，
	// 不需要写进令牌载荷，也就不存在"改了层级旧令牌还拿着旧权限"的窗口。
	// IsSuperAdmin 是派生字段：新账号用 account_level 表达超管，旧账号靠布尔折算，
	// 两者在这里收敛成一个结论，避免后续代码各读各的来源得出不同权限。
	effectiveLevel := contract.EffectiveAccountLevel(record.AccountLevel, record.User.IsSuperAdmin)
	return contract.Identity{
		UserID:       record.User.ID,
		Username:     record.User.Username,
		AccountLevel: effectiveLevel,
		TenantID:     record.TenantID,
		IsSuperAdmin: effectiveLevel == contract.AccountLevelSuperAdmin,
		Mode:         mode,
	}, true
}

const statusActive = "active"

// issue 生成「账号|版本|到期时间戳|模式.签名」。
// 版本让"禁用/改密"立即使旧令牌失效；模式决定服务端数据视图，必须进签名防止篡改。
func (s *Service) issue(username string, version int, expiresAt int64, mode contract.SessionMode) (string, error) {
	if strings.ContainsAny(username, "|.") {
		return "", fmt.Errorf("账号名不能包含 | 或 .")
	}
	payload := fmt.Sprintf("%s|%d|%d|%s", username, version, expiresAt, mode)
	return payload + "." + s.mac(payload), nil
}

// mac 计算 BASE64URL(SHA-256 HMAC)，无填充以便安全放进请求头。
func (s *Service) mac(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// HashPassword 供账号模块在「新建/重置密码」时复用同一套派生参数。
func HashPassword(password string) (string, error) {
	return hashPassword(password)
}

// VerifyPassword 供「修改自己的密码」校验旧密码：派生规则只此一处，不复制第二份。
func VerifyPassword(stored, password string) bool {
	return verifyPassword(stored, password)
}

// hashPassword 生成 pbkdf2-sha256$迭代次数$盐$派生值。
// 盐随机且随密码一起存，同一密码每次的结果都不同。
func hashPassword(password string) (string, error) {
	salt := make([]byte, hashSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	derived, err := pbkdf2.Key(sha256.New, password, salt, hashIterations, hashKeyLength)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s$%d$%s$%s", hashAlgorithm, hashIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived)), nil
}

// verifyPassword 按库里记录的算法与迭代次数重算，再用定长比较判定。
func verifyPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != hashAlgorithm {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	actual, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(expected))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

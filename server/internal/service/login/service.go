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

// Service 是账号鉴权服务。
type Service struct {
	users        *userrepo.Repository
	secret       []byte
	ttl          time.Duration
	seedUsername string
	seedPassword string
}

// NewService 从配置与账号仓储构造；种子凭据只在首次启动建号时使用。
func NewService(cfg config.Config, users *userrepo.Repository) *Service {
	return &Service{
		users:        users,
		secret:       []byte(cfg.AuthSecret),
		ttl:          contract.AuthTokenTTLSeconds * time.Second,
		seedUsername: cfg.AdminUsername,
		seedPassword: cfg.AdminPassword,
	}
}

// EnsureAdmin 首次启动时把环境变量里的管理员写进 users 表（users 为空才写）。
// 之后账号只在页面/库里维护，环境变量仅作为「首次种子」，改它不会再影响已有账号。
func (s *Service) EnsureAdmin(ctx context.Context) error {
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
	if _, err := s.users.Create(ctx, s.seedUsername, hash, "", true); err != nil {
		return fmt.Errorf("创建种子管理员失败: %w", err)
	}
	return nil
}

// Authenticate 校验凭据并签发令牌。
// 账号不存在与密码错误返回同一个错误，避免被用来枚举账号。
func (s *Service) Authenticate(ctx context.Context, username, password, ip string, mode contract.SessionMode) (contract.LoginResult, error) {
	record, err := s.users.FindByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return contract.LoginResult{}, contract.ErrInvalidCredentials
		}
		return contract.LoginResult{}, err
	}
	if record.User.Status != statusActive {
		return contract.LoginResult{}, contract.ErrInvalidCredentials
	}
	if !verifyPassword(record.PasswordHash, password) {
		return contract.LoginResult{}, contract.ErrInvalidCredentials
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

	return contract.LoginResult{
		Token:     token,
		Username:  record.User.Username,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		Mode:      mode,
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
	return contract.Identity{
		UserID:       record.User.ID,
		Username:     record.User.Username,
		IsSuperAdmin: record.User.IsSuperAdmin,
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

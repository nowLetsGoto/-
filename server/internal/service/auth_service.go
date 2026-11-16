// auth_service.go · 管理员登录与令牌签发。
// 职责：校验单管理员凭据、签发无状态 HMAC 令牌、校验令牌并返回用户名。
// 输入：config 中的管理员账号与签名密钥；调用方传入的用户名 / 密码 / 令牌。
// 输出：contract.LoginResult，或 contract.ErrInvalidCredentials；Verify 返回用户名与是否有效。
// 边界：不碰 HTTP、不写 DB、不落盘、不打日志（凭据与密钥绝不外泄）；
//       令牌不存服务端状态，进程重启后仍然有效，因为密钥来自部署配置。
package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
)

// AuthService 是单账号鉴权服务：一个管理员账号 + 一枚签名密钥。
type AuthService struct {
	username string
	password string
	secret   []byte
	ttl      time.Duration
}

// NewAuthService 从配置快照构造，凭据只在内存中保留。
func NewAuthService(cfg config.Config) *AuthService {
	return &AuthService{
		username: cfg.AdminUsername,
		password: cfg.AdminPassword,
		secret:   []byte(cfg.AuthSecret),
		ttl:      contract.AuthTokenTTLSeconds * time.Second,
	}
}

// Login 校验凭据并签发令牌。
// 两个字段都用 ConstantTimeCompare：避免通过响应耗时逐字符试探出账号或口令。
func (s *AuthService) Login(username string, password string) (contract.LoginResult, error) {
	usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(s.username)) == 1
	passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) == 1
	if !usernameOK || !passwordOK {
		return contract.LoginResult{}, contract.ErrInvalidCredentials
	}

	expiresAt := time.Now().Add(s.ttl)
	return contract.LoginResult{
		Token:     s.issue(expiresAt.Unix()),
		Username:  s.username,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
	}, nil
}

// Verify 校验令牌签名与有效期；有效时返回用户名。
// 未携带令牌走的是同一个出口（token 为空串），调用方无法从响应区分「没带」与「伪造」。
func (s *AuthService) Verify(token string) (string, bool) {
	payload, signature, found := strings.Cut(token, ".")
	if !found || payload == "" || signature == "" {
		return "", false
	}
	// hmac.Equal 也是定长比较，防止通过签名比对耗时反推正确签名。
	if !hmac.Equal([]byte(signature), []byte(s.mac(payload))) {
		return "", false
	}
	expiresAt, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || time.Now().Unix() >= expiresAt {
		return "", false
	}
	return s.username, true
}

// issue 生成「到期时间戳.签名」。
// 载荷只有到期时间：第一版只有一个管理员账号，用户名不需要随令牌传递；
// 将来支持多成员时，把载荷换成用户 id 即可，签名与校验逻辑不用改。
func (s *AuthService) issue(expiresAt int64) string {
	payload := strconv.FormatInt(expiresAt, 10)
	return payload + "." + s.mac(payload)
}

// mac 计算 BASE64URL(SHA-256 HMAC)，无填充以便安全放进请求头。
func (s *AuthService) mac(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

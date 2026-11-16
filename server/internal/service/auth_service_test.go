// auth_service_test.go · 鉴权服务的边界行为。
// 职责：验证凭据校验、令牌签发与校验、篡改 / 换密钥 / 过期一律拒绝。
// 输入：手工构造的 config.Config；输出：断言结果。
// 边界：不启动 HTTP、不连数据库、不读环境变量，纯内存验证。
package service

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
)

func newTestAuth(t *testing.T) *AuthService {
	t.Helper()
	return NewAuthService(config.Config{
		AdminUsername: "admin",
		AdminPassword: "admin123456",
		AuthSecret:    "test-secret",
	})
}

func TestLoginRejectsWrongCredentials(t *testing.T) {
	svc := newTestAuth(t)
	cases := []struct {
		name     string
		username string
		password string
	}{
		{"密码错误", "admin", "wrong-password"},
		{"用户名错误", "root", "admin123456"},
		{"空口令", "admin", ""},
	}
	for _, tc := range cases {
		if _, err := svc.Login(tc.username, tc.password); err != contract.ErrInvalidCredentials {
			t.Fatalf("%s: 期望 ErrInvalidCredentials，实际 %v", tc.name, err)
		}
	}
}

func TestLoginIssuesVerifiableToken(t *testing.T) {
	svc := newTestAuth(t)
	result, err := svc.Login("admin", "admin123456")
	if err != nil {
		t.Fatalf("正确凭据应登录成功: %v", err)
	}
	if result.Token == "" || result.Username != "admin" || result.ExpiresAt == "" {
		t.Fatalf("登录结果不完整: %+v", result)
	}

	username, ok := svc.Verify(result.Token)
	if !ok || username != "admin" {
		t.Fatalf("签发的令牌应通过校验，实际 ok=%v username=%q", ok, username)
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	svc := newTestAuth(t)
	result, err := svc.Login("admin", "admin123456")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	if _, ok := svc.Verify(result.Token + "x"); ok {
		t.Fatal("签名被追加字符后必须校验失败")
	}

	exp, signature, found := strings.Cut(result.Token, ".")
	if !found {
		t.Fatalf("令牌格式应为「载荷.签名」: %s", result.Token)
	}
	expiresAt, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		t.Fatalf("令牌载荷应为到期时间戳: %v", err)
	}
	if _, ok := svc.Verify(strconv.FormatInt(expiresAt+60, 10) + "." + signature); ok {
		t.Fatal("延长到期时间的令牌必须校验失败（载荷受签名保护）")
	}
}

func TestVerifyRejectsMalformedExpiredAndForeignToken(t *testing.T) {
	svc := newTestAuth(t)

	for _, token := range []string{"", ".", "abc", "abc.", ".abc", "abc.def"} {
		if _, ok := svc.Verify(token); ok {
			t.Fatalf("畸形令牌 %q 必须校验失败", token)
		}
	}

	// 用同一密钥直接签发一个已过期的时间戳，验证有效期判断。
	if _, ok := svc.Verify(svc.issue(time.Now().Add(-time.Minute).Unix())); ok {
		t.Fatal("过期令牌必须校验失败")
	}

	// 换一枚密钥签发的令牌不能通过本服务校验。
	result, err := svc.Login("admin", "admin123456")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	foreign := NewAuthService(config.Config{
		AdminUsername: "admin",
		AdminPassword: "admin123456",
		AuthSecret:    "another-secret",
	})
	if _, ok := foreign.Verify(result.Token); ok {
		t.Fatal("换密钥后旧令牌必须失效")
	}
}

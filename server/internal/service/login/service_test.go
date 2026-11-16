// service_test.go · 密码派生与令牌载荷的边界行为。
// 职责：验证密码哈希能自洽校验、盐随机、篡改必拒，以及令牌载荷对非法账号名的拒绝。
// 输入：纯函数入参；输出：断言结果。
// 边界：不启动 HTTP、**不连数据库**——Authenticate / Verify 依赖 users 表，
//       属于集成测试范畴（见 docs/07-QA-TEST-PLAN.md），这里只锁纯逻辑。
package login

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("admin123456")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	if !verifyPassword(hash, "admin123456") {
		t.Fatal("正确密码应校验通过")
	}
	if verifyPassword(hash, "admin1234567") {
		t.Fatal("错误密码不应通过")
	}
}

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	first, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	second, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	if first == second {
		t.Fatal("同一密码两次哈希结果不应相同（盐必须随机）")
	}
	if !verifyPassword(first, "same-password") || !verifyPassword(second, "same-password") {
		t.Fatal("两个哈希都应能校验通过")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	cases := map[string]string{
		"空串":        "",
		"段数不足":      "pbkdf2-sha256$100000$abc",
		"算法不符":      "bcrypt$100000$c2FsdA$aGFzaA",
		"迭代次数非法":    "pbkdf2-sha256$abc$c2FsdA$aGFzaA",
		"盐不是 base64": "pbkdf2-sha256$100000$%%%$%%%",
	}
	for name, stored := range cases {
		if verifyPassword(stored, "whatever") {
			t.Fatalf("%s：非法哈希不应通过校验", name)
		}
	}
}

func TestVerifyPasswordRejectsTamperedHash(t *testing.T) {
	hash, err := HashPassword("admin123456")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	// 篡改派生值最后一个字符
	tampered := hash[:len(hash)-1] + string(hash[len(hash)-1]^0x01)
	if verifyPassword(tampered, "admin123456") {
		t.Fatal("被篡改的哈希不应通过校验")
	}
}

func TestIssueRejectsUnsafeUsername(t *testing.T) {
	// 账号名会进令牌载荷（| 分段、. 分隔签名），必须挡住这些字符。
	svc := &Service{secret: []byte("test-secret")}
	for _, username := range []string{"a|b", "a.b", "admin|1|999"} {
		if _, err := svc.issue(username, 1, 9999999999); err == nil {
			t.Fatalf("账号名 %q 含分隔符，应被拒绝", username)
		}
	}

	token, err := svc.issue("admin", 3, 9999999999)
	if err != nil {
		t.Fatalf("合法账号名不应报错: %v", err)
	}
	payload, signature, found := strings.Cut(token, ".")
	if !found || payload != "admin|3|9999999999" {
		t.Fatalf("令牌载荷格式不符: %q", payload)
	}
	if signature != svc.mac(payload) {
		t.Fatal("签名应与载荷一致")
	}
}

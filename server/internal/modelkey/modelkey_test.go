// modelkey_test.go · 加密与掩码口径测试。
// 职责：钉死 AES-GCM roundtrip、篡改失败、掩码金标准与 Redact 不残留 Key。
// 输入：内存中的测试主密钥与测试字面量（均非真实 Key，仓库内不存在任何可用凭据）。
// 输出：全部断言通过；任一处泄露或口径漂移即失败。
// 边界：不连数据库、不读环境变量、不发起网络请求。
package modelkey

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	// 固定测试主密钥：仅用于本包单测，不是任何环境或用户的真实密钥。
	return []byte("01234567890123456789012345678901")
}

func TestNewRejectsWrongLength(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33} {
		if _, err := New(make([]byte, size)); err == nil {
			t.Fatalf("主密钥长度 %d 必须被拒绝", size)
		}
	}
	codec, err := New(testKey(t))
	if err != nil || codec == nil {
		t.Fatalf("32 字节主密钥应通过: err=%v", err)
	}
}

func TestSealOpenRoundtripAndTamper(t *testing.T) {
	codec, err := New(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plain := "sk-test-not-a-real-key-1234"
	ciphertext, nonce, err := codec.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, plain) || strings.Contains(nonce, plain) {
		t.Fatal("密文与 nonce 不得包含明文")
	}
	got, err := codec.Open(ciphertext, nonce)
	if err != nil || got != plain {
		t.Fatalf("roundtrip 失败: got=%q err=%v", got, err)
	}

	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil || len(raw) == 0 {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := codec.Open(tampered, nonce); err == nil {
		t.Fatal("篡改密文必须解密失败")
	}
	badNonce := base64.StdEncoding.EncodeToString(make([]byte, nonceBytes))
	if _, err := codec.Open(ciphertext, badNonce); err == nil {
		t.Fatal("错误 nonce 必须解密失败")
	}
	if _, err := codec.Open("not-base64", nonce); err == nil {
		t.Fatal("非法 base64 必须失败")
	}
}

func TestMaskGolden(t *testing.T) {
	for _, tc := range []struct{ plain, want string }{
		{"", "****"},
		{"sk-1234", "****"},
		{"sk-abcdefghijk", "sk-****hijk"},
		{"abcdefghijkl", "****ijkl"},
	} {
		if got := Mask(tc.plain); got != tc.want {
			t.Fatalf("Mask(%q)=%q，期望 %q", tc.plain, got, tc.want)
		}
	}
}

func TestRedactRemovesPlainAndCommonPattern(t *testing.T) {
	plain := "sk-test-not-a-real-key-1234"
	text := "upstream said " + plain + " and sk-another-fake-key-9999"
	got := Redact(plain, text)
	if strings.Contains(got, plain) || strings.Contains(got, "sk-another-fake-key-9999") {
		t.Fatalf("脱敏后仍残留 Key: %s", got)
	}
	if !strings.Contains(got, "[已隐藏]") {
		t.Fatal("脱敏结果应含替代标记")
	}
}

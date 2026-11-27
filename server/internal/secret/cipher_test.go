// cipher_test.go · 凭据加解密的单测。
//
// 职责：锁定「同一密钥可还原、不同密钥解不开、篡改能被发现」三条性质。
// 输入：构造的 Cipher 与样例明文。
// 输出：对返回值与错误分支的断言。
// 边界：不连库、不碰环境变量、不测密钥轮换流程。
//
// 为什么这几条必须测：加密出错不会立刻暴露，往往等到某天重启服务、
// R2 全部请求 401 才发现。三条性质各自对应一次真实故障——
// 还原不了 = 密钥派生不稳定；不同密钥能解开 = 算法用错；
// 篡改不报错 = 没走 GCM 认证。
package secret

import (
	"strings"
	"testing"
)

const testMaster = "test-master-secret-for-unit-test"

func mustCipher(t *testing.T, master string) *Cipher {
	t.Helper()
	c, err := New(master)
	if err != nil {
		t.Fatalf("New(%q) 失败: %v", master, err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := mustCipher(t, testMaster)

	cases := []struct{ name, plain string }{
		{"普通密钥", "05ea820f5e58c8afb30e335d6046cea1d776c91da93e09b13bd8b3f83910d354"},
		{"含空格与中文", "  a b c 中文凭据  "},
		{"单字符", "x"},
		{"较长明文", strings.Repeat("abcdefghij", 100)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := c.Encrypt(tc.plain)
			if err != nil {
				t.Fatalf("加密失败: %v", err)
			}
			if !strings.HasPrefix(enc, versionPrefix) {
				t.Fatalf("密文缺少版本前缀 %q: %s", versionPrefix, enc)
			}
			// 只对足够长的明文检查「密文不含明文」：
			// 短明文（如单字符）会偶然出现在 base64 串里，
			// 那种命中是概率巧合，不是加密失效。
			// 长明文的字符序列偶然全现的概率可忽略，检查才有意义。
			if len(tc.plain) >= 16 && strings.Contains(enc, tc.plain) {
				t.Fatal("密文里出现了明文，加密没有生效")
			}

			got, err := c.Decrypt(enc)
			if err != nil {
				t.Fatalf("解密失败: %v", err)
			}
			if got != tc.plain {
				t.Fatalf("明文不一致: want %q, got %q", tc.plain, got)
			}
		})
	}
}

func TestEncryptIsRandomized(t *testing.T) {
	c := mustCipher(t, testMaster)
	const plain = "same-plaintext"

	first, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	second, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	// 同一明文两次加密必须不同：说明 nonce 每次随机，
	// 而不是把 nonce 固定成某个值（那会让 GCM 直接失去语义安全性）。
	if first == second {
		t.Fatal("两次加密结果相同，nonce 可能没有随机化")
	}
}

func TestEmptyPlainStaysEmpty(t *testing.T) {
	c := mustCipher(t, testMaster)

	enc, err := c.Encrypt("")
	if err != nil {
		t.Fatalf("加密空串失败: %v", err)
	}
	// 空串必须保持空串：调用方用它表达「未配置」。
	// 若加密成一段非空密文，"是否配置过" 就得多一个字段判断。
	if enc != "" {
		t.Fatalf("空串被加密成了 %q，应保持空串", enc)
	}

	got, err := c.Decrypt("")
	if err != nil {
		t.Fatalf("解密空串失败: %v", err)
	}
	if got != "" {
		t.Fatalf("空串解密应得空串，got %q", got)
	}
}

func TestDecryptWithDifferentMasterFails(t *testing.T) {
	enc, err := mustCipher(t, testMaster).Encrypt("sensitive-value")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	// 模拟 AUTH_SECRET 被改动：必须解不开，且必须报错而不是返回脏数据。
	if _, err := mustCipher(t, "another-master").Decrypt(enc); err == nil {
		t.Fatal("换了主密钥仍能解密，密钥隔离失效")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	c := mustCipher(t, testMaster)
	enc, err := c.Encrypt("sensitive-value")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	// 篡改密文本体中的一个字节，GCM 必须校验失败。
	//
	// 不解码到字节再改，而是直接替换 base64 中间的一个字符：
	// 中段字符的改动一定会映射到密文字节，而末位字符可能因 base64
	// 补齐位被忽略而解出相同字节（实测末位改动有时不改变解码结果，
	// 那时解密成功是正确行为，不是漏洞）。
	body := strings.TrimPrefix(enc, versionPrefix)
	if len(body) < 8 {
		t.Fatalf("密文过短，无法安全篡改: %q", enc)
	}
	mid := len(body) / 2
	replacement := byte('A')
	if body[mid] == 'A' {
		replacement = 'B'
	}
	tampered := versionPrefix + body[:mid] + string(replacement) + body[mid+1:]

	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("篡改后的密文仍能解密，缺少完整性校验")
	}
}

func TestDecryptRejectsUnknownFormat(t *testing.T) {
	c := mustCipher(t, testMaster)

	cases := []struct{ name, stored string }{
		{"无版本前缀", "YWJjZGVm"},
		{"前缀但非 base64", versionPrefix + "!!!not-base64!!!"},
		{"前缀但长度不足", versionPrefix + "YWJj"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Decrypt(tc.stored); err == nil {
				t.Fatalf("%q 应被拒绝", tc.stored)
			}
		})
	}
}

func TestNewRejectsEmptyMaster(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("空主密钥应被拒绝：那会让派生密钥可预测")
	}
}

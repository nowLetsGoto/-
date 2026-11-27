// r2_service_test.go · R2 配置业务层的单测。
//
// 职责：锁定 URL 校验、字段截断规则与「保存时密钥三种意图」的判定。
// 输入：构造的请求值与 Cipher。
// 输出：对 normalizeR2 与 Update 结果的断言。
// 边界：不连库（用假仓储）时只测纯函数；涉及仓储的部分走集成测试。
//
// 为什么重点测 normalizeR2：它挡在「用户粘贴错东西」与「数据库」之间。
// 自定义域名写错的表现是「页面正常但下载 404」，是最难现场排查的一类故障，
// 因此这条校验必须有测试守着。
package settings

import (
	"errors"
	"strings"
	"testing"

	"dandelion/server/internal/contract"
	settingsrepo "dandelion/server/internal/repository/settings"
	"dandelion/server/internal/secret"
	"dandelion/server/internal/storage"
)

// testMasterKey 是测试用主密钥，与 secret 包的测试各自独立。
const testMasterKey = "test-master-for-settings-service"

func TestNormalizeR2AcceptsValidInput(t *testing.T) {
	in := contract.R2Config{
		AccountID:     "  7ab4437f542d777cbb9948413f2d66cd  ",
		AccessKeyID:   " 3eeb40a699a24fd7a6f6cd412983f896 ",
		Bucket:        " api-4 ",
		PublicBaseURL: "https://download.example.com/",
	}

	cfg, err := normalizeR2(in)
	if err != nil {
		t.Fatalf("应通过校验，got err=%v", err)
	}

	// 两端空白必须被裁掉：粘贴时常带上空格，留着会让签名结果完全不同。
	if cfg.AccountID != "7ab4437f542d777cbb9948413f2d66cd" {
		t.Fatalf("AccountID 未裁剪空格: %q", cfg.AccountID)
	}
	if cfg.AccessKeyID != "3eeb40a699a24fd7a6f6cd412983f896" {
		t.Fatalf("AccessKeyID 未裁剪空格: %q", cfg.AccessKeyID)
	}
	if cfg.Bucket != "api-4" {
		t.Fatalf("Bucket 未裁剪空格: %q", cfg.Bucket)
	}
	// 末尾斜杠要归一：拼下载地址时会再补一个 /，两个连起来就是 404。
	if cfg.PublicBaseURL != "https://download.example.com" {
		t.Fatalf("PublicBaseURL 未去掉末尾斜杠: %q", cfg.PublicBaseURL)
	}
}

func TestNormalizeR2AllowsEmptyToDisable(t *testing.T) {
	// 全部留空必须通过：那是「关闭 R2，走本机下载」的合法表达。
	// 若这里报错，管理员想关掉 R2 就得先把五项逐个清空才能保存。
	cfg, err := normalizeR2(contract.R2Config{})
	if err != nil {
		t.Fatalf("全空应允许（表示关闭 R2），got err=%v", err)
	}
	if cfg.AccountID != "" || cfg.Bucket != "" || cfg.PublicBaseURL != "" {
		t.Fatal("全空输入不应产生任何值")
	}
}

func TestNormalizeR2RejectsInvalidPublicURL(t *testing.T) {
	cases := []struct{ name, url string }{
		{"缺少协议", "download.example.com"},
		{"非 http 协议", "ftp://download.example.com"},
		{"没有主机", "https://"},
		{"带查询串", "https://download.example.com?a=1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeR2(contract.R2Config{PublicBaseURL: tc.url})
			if err == nil {
				t.Fatalf("非法域名 %q 应被拒绝", tc.url)
			}
			// 校验失败必须映射成 400 而不是 500：这是用户输入问题。
			var invalid *contract.ValidationError
			if !asValidationError(err, &invalid) {
				t.Fatalf("应返回 ValidationError（映射 400），got %T: %v", err, err)
			}
		})
	}
}

func TestNormalizeR2RejectsOversizedField(t *testing.T) {
	// 误把整段 JSON 粘进输入框是最常见的手残，长度上限用来挡住它。
	_, err := normalizeR2(contract.R2Config{AccountID: strings.Repeat("x", 600)})
	if err == nil {
		t.Fatal("超长字段应被拒绝")
	}
}

func TestEnabledRequiresAllFourFields(t *testing.T) {
	// 判据的唯一来源是 storage.R2Config.Enabled：
	// service 层不再维护第二份，避免出现「页面说配好了、客户端却不启用」的割裂。
	full := r2Fixture()
	if !toStorage(full).Enabled() {
		t.Fatal("四项齐全应判定为可用")
	}

	// 逐个清空，任一缺失都必须判定为不可用——
	// 这与 storage.R2Config.Enabled 的判据一致，避免出现
	// 「页面说配置好了，客户端却因为缺项不启用」的割裂。
	fields := []struct {
		name  string
		clear func(*r2RepoFixture)
	}{
		{"缺 AccountID", func(f *r2RepoFixture) { f.AccountID = "" }},
		{"缺 AccessKeyID", func(f *r2RepoFixture) { f.AccessKeyID = "" }},
		{"缺 SecretKey", func(f *r2RepoFixture) { f.SecretKey = "" }},
		{"缺 Bucket", func(f *r2RepoFixture) { f.Bucket = "" }},
	}

	for _, tc := range fields {
		t.Run(tc.name, func(t *testing.T) {
			cfg := r2Fixture()
			tc.clear(&cfg)
			if toStorage(cfg).Enabled() {
				t.Fatalf("%s 时应判定不可用", tc.name)
			}
		})
	}
}

// r2RepoFixture 是仓储层的 R2 结构，测试里用别名让断言更短。
type r2RepoFixture = settingsrepo.R2

// r2Fixture 返回一份四项齐全的配置，供 Complete 的各分支复用。
func r2Fixture() r2RepoFixture {
	return r2RepoFixture{
		AccountID:   "7ab4437f542d777cbb9948413f2d66cd",
		AccessKeyID: "3eeb40a699a24fd7a6f6cd412983f896",
		SecretKey:   "v1:encrypted-placeholder",
		Bucket:      "api-4",
	}
}

// toStorage 把仓储结构转成存储层配置，便于复用 Enabled 的判据。
func toStorage(cfg r2RepoFixture) storage.R2Config {
	return storage.R2Config{
		AccountID:     cfg.AccountID,
		AccessKeyID:   cfg.AccessKeyID,
		SecretKey:     cfg.SecretKey,
		Bucket:        cfg.Bucket,
		PublicBaseURL: cfg.PublicBaseURL,
	}
}

// asValidationError 判断 err 是否为校验错误。
// 用 errors.As 而不是类型断言：service 层可能包装一层再加进来。
func asValidationError(err error, target **contract.ValidationError) bool {
	return errors.As(err, target)
}

// TestStoredDeterminesFallback 锁定「何时用环境变量兜底」的判定。
//
// 这条规则错一次就会产生用户可见的怪现象：
// 判定太松 → 页面上关掉 R2，刷新后又看到环境变量里的旧值，改了像没改；
// 判定太紧 → 库里只填了桶名，其余项被当成"没配置"，预填反而不出现。
func TestStoredDeterminesFallback(t *testing.T) {
	if stored(r2RepoFixture{}) {
		t.Fatal("全空应判定为「从未写入过」，否则环境变量预填永远不会生效")
	}

	// 任一项有值即视为写入过：管理员可能只改一项就保存。
	for _, tc := range []struct {
		name string
		cfg  r2RepoFixture
	}{
		{"只有桶名", r2RepoFixture{Bucket: "api-4"}},
		{"只有账号", r2RepoFixture{AccountID: "abc"}},
		{"只有密钥", r2RepoFixture{SecretKey: "v1:xxx"}},
		{"只有域名", r2RepoFixture{PublicBaseURL: "https://x.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !stored(tc.cfg) {
				t.Fatalf("%s 时应判定为已写入，否则用户的修改会被环境变量覆盖", tc.name)
			}
		})
	}
}

// TestKeepSecret 验证「用户没改密钥时该写入什么」的三档优先级。
//
// 直接调用真实的 keepSecret，而不是复刻判断逻辑：
// 复刻式测试在实现被改坏时仍会通过，等于没测。
//
// 第一档对应一个真实修复的数据丢失缺陷——全新安装时库里全空、
// 页面显示环境变量预填值，用户只改桶名保存会把密钥清空、R2 立刻不可用。
func TestKeepSecret(t *testing.T) {
	c, err := secret.New(testMasterKey)
	if err != nil {
		t.Fatalf("构造 Cipher 失败: %v", err)
	}

	const fallbackPlain = "05ea820f5e58c8afb30e335d6046cea1d776c91da93e09b13bd8b3f83910d354"

	t.Run("库里有密文时原样沿用", func(t *testing.T) {
		svc := &R2Service{cipher: c, fallback: storage.R2Config{SecretKey: fallbackPlain}}
		got, err := svc.keepSecret("v1:existing-ciphertext")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got != "v1:existing-ciphertext" {
			t.Fatalf("应原样沿用库里密文，got %q", got)
		}
	})

	t.Run("库里空、环境变量有则加密固化", func(t *testing.T) {
		svc := &R2Service{cipher: c, fallback: storage.R2Config{SecretKey: fallbackPlain}}
		got, err := svc.keepSecret("")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got == "" {
			t.Fatal("必须把环境变量的密钥固化进库，否则只改桶名就会把密钥清空（真实缺陷）")
		}
		// 必须真的加密过：能解回原值，且不含明文。
		plain, err := c.Decrypt(got)
		if err != nil {
			t.Fatalf("固化的密文应可解密: %v", err)
		}
		if plain != fallbackPlain {
			t.Fatalf("解出的明文不一致: got %q", plain)
		}
	})

	t.Run("两处都空则保持未配置", func(t *testing.T) {
		svc := &R2Service{cipher: c}
		got, err := svc.keepSecret("")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got != "" {
			t.Fatalf("应保持空串，got %q", got)
		}
	})
}

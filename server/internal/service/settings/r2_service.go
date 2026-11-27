// Package settings · R2 对象存储配置的业务层。
// 职责：组装页面所需的 R2 配置（脱敏）、加密保存密钥、把密文还原给存储层使用。
// 输入：repository/settings 的读写、internal/secret 的加解密、contract 的 DTO。
// 输出：contract.R2Config（不含明文密钥）、storage.R2Config（含明文，仅供内部使用）。
// 边界：不碰 HTTP、不构造 R2 客户端（在 service/r2registry）、不发起网络请求。
//
// 两层配置形态是刻意区分的：
//   - contract.R2Config 给前端看，secret 只以 secretConfigured 布尔表达；
//   - storage.R2Config 给 AWS SDK 用，需要明文。
//
// 用同一个结构体兼做两件事，迟早会把明文泄露到响应里。
package settings

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"dandelion/server/internal/contract"
	settingsrepo "dandelion/server/internal/repository/settings"
	"dandelion/server/internal/secret"
	"dandelion/server/internal/storage"
)

// R2Service 是 R2 配置的读写入口。
//
// 与 Service 分开而不是并入：Service 持 Webhook / API Key 两条流程，
// 而这个只关心「一套凭据如何安全地存取与校验」。合在一起会让两个不相关的
// 变化原因（Webhook 字段变更、存储通道调整）耦合在同一个类型上。
type R2Service struct {
	repo   *settingsrepo.Repository
	cipher *secret.Cipher
	// fallback 是首次打开设置页时用于预填的配置，来源是进程环境变量。
	//
	// 为什么需要它：换机器部署时，R2 参数原本只存在于 .env 与 plist 里，
	// 页面一片空白，管理员必须从别处抄一遍才能保存。预填让「打开就能看到当前值」，
	// 也顺带把"机器上实际在用什么"变得可见。
	//
	// 只作为初始值：一旦库里存了任何一项，就以库为准（见 Get 的判定），
	// 否则用户在页面上把 R2 关掉后，刷新又会看到环境变量里的旧值。
	fallback storage.R2Config
}

func NewR2Service(repo *settingsrepo.Repository, cipher *secret.Cipher, fallback storage.R2Config) *R2Service {
	return &R2Service{repo: repo, cipher: cipher, fallback: fallback}
}

// stored 判断库里的配置是否已经有过任何写入。
//
// 判据是「任一项非空」而不是「五项齐全」：管理员可能只想先改桶名，
// 或故意只保留部分字段。只要有值就说明他动过，之后一律以库为准。
func stored(cfg settingsrepo.R2) bool {
	return cfg.AccountID != "" || cfg.AccessKeyID != "" || cfg.SecretKey != "" ||
		cfg.Bucket != "" || cfg.PublicBaseURL != ""
}

// Get 返回页面可展示的 R2 配置：密钥只表达「配没配过」，不回传明文或密文。
//
// 密文也不该回传给前端：它对前端没有用处（前端无法解密），
// 而一旦出现在响应里，就会进浏览器缓存、日志、以及任何抓包记录。
func (s *R2Service) Get(ctx context.Context, scope contract.Scope) (contract.R2Config, error) {
	tenantID, err := settingsTenant(scope)
	if err != nil {
		return contract.R2Config{}, err
	}
	cfg, err := s.repo.LoadR2(ctx, tenantID)
	if err != nil {
		return contract.R2Config{}, err
	}

	// 库里没写过时用环境变量预填，让页面一打开就能看到机器上实际在用的值。
	// 一旦库里有值就以库为准，否则用户关掉 R2 后刷新又会看到旧配置。
	if !stored(cfg) {
		cfg = settingsrepo.R2{
			AccountID:     s.fallback.AccountID,
			AccessKeyID:   s.fallback.AccessKeyID,
			SecretKey:     s.fallback.SecretKey,
			Bucket:        s.fallback.Bucket,
			PublicBaseURL: s.fallback.PublicBaseURL,
		}
	}

	return contract.R2Config{
		AccountID:        cfg.AccountID,
		AccessKeyID:      cfg.AccessKeyID,
		Bucket:           cfg.Bucket,
		PublicBaseURL:    cfg.PublicBaseURL,
		SecretConfigured: cfg.SecretKey != "",
	}, nil
}

// Update 校验并整体保存 R2 配置，返回保存后的页面数据。
//
// secret 的三种意图，用两个入参区分（避免"空串"承担多重含义）：
//   - ChangeSecret 为 false：沿用库里的旧密文，忽略 Secret 入参；
//   - ChangeSecret 为 true 且 Secret 非空：加密后保存新值；
//   - ChangeSecret 为 true 且 Secret 为空：清除密钥。
func (s *R2Service) Update(
	ctx context.Context, scope contract.Scope, in contract.R2Config, newSecret string, changeSecret bool,
) (contract.R2Config, error) {
	tenantID, err := settingsTenant(scope)
	if err != nil {
		return contract.R2Config{}, err
	}

	cfg, err := normalizeR2(in)
	if err != nil {
		return contract.R2Config{}, err
	}

	if changeSecret {
		// 空串表示清除，Encrypt 对空串返回空串，正好落到「未配置」。
		encrypted, err := s.cipher.Encrypt(strings.TrimSpace(newSecret))
		if err != nil {
			return contract.R2Config{}, fmt.Errorf("加密 R2 密钥失败: %w", err)
		}
		cfg.SecretKey = encrypted
	} else {
		storedCfg, err := s.repo.LoadR2(ctx, tenantID)
		if err != nil {
			return contract.R2Config{}, err
		}
		encrypted, err := s.keepSecret(storedCfg.SecretKey)
		if err != nil {
			return contract.R2Config{}, err
		}
		cfg.SecretKey = encrypted
	}

	if err := s.repo.SaveR2(ctx, tenantID, cfg); err != nil {
		return contract.R2Config{}, err
	}
	return s.Get(ctx, scope)
}

// Runtime 返回可直接用于构造 R2 客户端的配置（密钥已解密）。
//
// 这是唯一出口把明文密钥交出去，且只交给 storage 层构造客户端。
//
// 解密失败（AUTH_SECRET 变更）返回空配置并记日志，而不是返回错误：
// 此时应表现为「R2 未启用，下载走本机」，服务继续可用。
// 若把错误抛给调用方，一次密钥变更会让所有下载路径都失败——
// 而降级为本地下载是可接受的，只是慢。
//
// 但必须留下日志：否则管理员只会发现「下载变慢了」，
// 完全想不到是凭据解不开，页面上也只会看到 secretConfigured=true 却总是失败。
func (s *R2Service) Runtime(ctx context.Context, tenantID string) (storage.R2Config, error) {
	cfg, err := s.repo.LoadR2(ctx, tenantID)
	if err != nil {
		return storage.R2Config{}, err
	}

	// 与 Get 用同一个判定：库里没写过就回落到环境变量。
	// 两处必须一致，否则会出现「页面显示一套、实际用另一套」的错位——
	// 那是本设计最容易出错的地方，所以判定逻辑只允许有一份（stored）。
	if !stored(cfg) {
		return s.fallback, nil
	}
	if cfg.SecretKey == "" {
		return storage.R2Config{}, nil
	}

	plain, err := s.cipher.Decrypt(cfg.SecretKey)
	if err != nil {
		log.Printf("R2 凭据解密失败，已降级为本地下载（AUTH_SECRET 可能已变更，请在平台集成重新填写）: %v", err)
		return storage.R2Config{}, nil
	}
	return storage.R2Config{
		AccountID:     cfg.AccountID,
		AccessKeyID:   cfg.AccessKeyID,
		SecretKey:     plain,
		Bucket:        cfg.Bucket,
		PublicBaseURL: cfg.PublicBaseURL,
	}, nil
}

// keepSecret 在「用户没有主动改密钥」时，决定该往库里写什么密文。
//
// 参数：storedCipher 是库里当前那一列的原值（可能是空串）。
// 返回：应写入的密文；空串表示保持未配置。
//
// 三档优先级，缺一不可：
//
//  1. 库里有密文 → 原样沿用。不解密重加密：密文无端变化会让排查时分不清
//     「用户真的换了密钥」还是「保存时被重算了一遍」。
//  2. 库里没有、环境变量有 → 把预填的明文加密后固化。
//     这一档是修复数据丢失缺陷的关键：全新安装时页面显示的是环境变量预填值，
//     用户只改桶名点保存时若直接从库里读，会拿到空串并把密钥清空，
//     导致 R2 立刻不可用（实测复现过）。固化同时也让配置不再依赖环境变量，
//     下次换机器部署时页面不会是空白。
//  3. 两处都没有 → 保持空。
func (s *R2Service) keepSecret(storedCipher string) (string, error) {
	if storedCipher != "" {
		return storedCipher, nil
	}
	if s.fallback.SecretKey == "" {
		return "", nil
	}
	encrypted, err := s.cipher.Encrypt(s.fallback.SecretKey)
	if err != nil {
		return "", fmt.Errorf("加密 R2 密钥失败: %w", err)
	}
	return encrypted, nil
}

// normalizeR2 收敛请求值并校验；返回仓储结构（SecretKey 由调用方填）。
//
// 校验只在「有值」时做格式检查：允许整体留空表示关闭 R2，
// 否则管理员想关掉 R2 时还得先把五项逐个清空才能保存。
func normalizeR2(in contract.R2Config) (settingsrepo.R2, error) {
	cfg := settingsrepo.R2{
		AccountID:     strings.TrimSpace(in.AccountID),
		AccessKeyID:   strings.TrimSpace(in.AccessKeyID),
		Bucket:        strings.TrimSpace(in.Bucket),
		PublicBaseURL: strings.TrimSpace(in.PublicBaseURL),
	}

	// PublicBaseURL 是唯一有格式要求的项：它会被拼成下载地址交给浏览器，
	// 写错的表现是"页面显示正常但下载 404"，不校验就很难查。
	//
	// 不能只靠 contract.NormalizeHTTPURL：那个函数是通用工具，
	// 只校验协议与主机，允许查询串与片段——对 Webhook 地址是必要的，
	// 但下载域名带上 ?a=1 会让拼接结果变成 "https://host?v=1/key"，
	// 整个 key 被当成查询参数，请求必然 404。
	if cfg.PublicBaseURL != "" {
		normalized, err := contract.NormalizeHTTPURL(cfg.PublicBaseURL)
		if err != nil {
			return settingsrepo.R2{}, &contract.ValidationError{Message: "自定义域名" + err.Error()}
		}
		parsed, err := url.Parse(normalized)
		if err != nil {
			return settingsrepo.R2{}, &contract.ValidationError{Message: "自定义域名不合法"}
		}
		// 查询串与片段在拼接时无法保留语义，直接拒绝而不是静默丢弃：
		// 静默丢弃会让用户以为配置生效了，实际拼出的地址与他填的不是一回事。
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return settingsrepo.R2{}, &contract.ValidationError{
				Message: "自定义域名不能带查询参数或锚点，只填到域名（可含路径前缀）",
			}
		}
		cfg.PublicBaseURL = strings.TrimSuffix(normalized, "/")
	}

	// AccessKeyID 与 Bucket 的长度上限按 Cloudflare 的实际约束取一个宽松值，
	// 目的只是挡住误粘贴整段 JSON 的情况，不是精确校验。
	const maxFieldLen = 512
	for _, field := range []struct{ name, value string }{
		{"Account ID", cfg.AccountID},
		{"Access Key ID", cfg.AccessKeyID},
		{"Bucket", cfg.Bucket},
		{"自定义域名", cfg.PublicBaseURL},
	} {
		if len(field.value) > maxFieldLen {
			return settingsrepo.R2{}, &contract.ValidationError{
				Message: fmt.Sprintf("%s 不能超过 %d 个字符", field.name, maxFieldLen),
			}
		}
	}
	return cfg, nil
}

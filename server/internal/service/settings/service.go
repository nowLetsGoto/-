// Package settings · 系统设置的业务层。
// 职责：组装设置页数据、校验并保存 Webhook 配置、对外提供 API Key。
// 输入：repository/settings 的读写能力、contract 的字段目录与上限。
// 输出：contract.Settings（含可选字段与 API Key 配置状态）。
// 边界：不碰 HTTP、不投递 Webhook（在 service/webhook）、不做数据库方言拼装。
package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"dandelion/server/internal/contract"
	settingsrepo "dandelion/server/internal/repository/settings"
	webhooksvc "dandelion/server/internal/service/webhook"
)

// Service 是设置页的唯一业务入口。
type Service struct {
	repo *settingsrepo.Repository
}

func NewService(repo *settingsrepo.Repository) *Service {
	return &Service{repo: repo}
}

// settingsTenant 从 scope 解析「这次操作哪家公司的配置」。
//
// 空串代表平台兜底行，只有两种情况会用到：
//   - 超管（scope.All）查看/维护平台兜底配置；
//   - 没有公司归属的调用（理论上不该发生，兜底行不会凭空多出来）。
//
// 平台管理员只能落到自己的公司，请求参数无法改变这个归属。
func settingsTenant(scope contract.Scope) (string, error) {
	if scope.All {
		return "", nil
	}
	if scope.TenantID == "" {
		// 非超管又没有公司：失败关闭，绝不能让它去改平台兜底配置。
		return "", contract.ErrForbidden
	}
	return scope.TenantID, nil
}

// Get 返回设置页所需的 Webhook 配置、API Key 配置状态与可选字段目录。
// 字段目录由契约生成，前端据此渲染复选框，不再硬编码一份中文标签。
func (s *Service) Get(ctx context.Context, scope contract.Scope) (contract.Settings, error) {
	tenantID, err := settingsTenant(scope)
	if err != nil {
		return contract.Settings{}, err
	}
	// EnsureRow：新建的公司还没有配置行时按默认值创建，首次打开也能读取配置。
	cfg, err := s.repo.EnsureRow(ctx, tenantID)
	if err != nil {
		return contract.Settings{}, err
	}
	return contract.Settings{
		Webhook: contract.WebhookConfig{
			Provider: cfg.Provider,
			Enabled:  cfg.Enabled,
			URL:      cfg.URL,
			Secret:   cfg.Secret,
			Fields:   cfg.Fields,
		},
		// 只告诉前端「配没配过」，不返回 Key 本身：库里是摘要，也还原不出明文。
		// 要拿明文只能走轮换（一次性）。
		APIKeyConfigured: cfg.APIKey != "",
		AvailableFields:  fieldOptions(),
	}, nil
}

// APIKeyByRequest 供外部接口的中间件用：拿调用方提供的 Key 反查它属于哪家公司。
// 返回空串表示平台兜底 Key（包按项目归属推导公司）。
func (s *Service) APIKeyByRequest(ctx context.Context, provided string) (string, error) {
	return s.repo.TenantByAPIKey(ctx, provided)
}

// RotateAPIKey 轮换当前公司的外部接口 Key：旧 Key 立即失效（外部系统需同步更新）。
// 明文只返回一次，不落任何日志。
func (s *Service) RotateAPIKey(ctx context.Context, scope contract.Scope) (string, error) {
	tenantID, err := settingsTenant(scope)
	if err != nil {
		return "", err
	}
	return s.repo.RotateAPIKey(ctx, tenantID)
}

// UpdateWebhook 校验后整体保存，并返回更新后的设置，前端一次拿到权威值。
func (s *Service) UpdateWebhook(ctx context.Context, scope contract.Scope, in contract.WebhookConfig) (contract.Settings, error) {
	tenantID, err := settingsTenant(scope)
	if err != nil {
		return contract.Settings{}, err
	}
	hook, err := normalize(in)
	if err != nil {
		return contract.Settings{}, err
	}
	if err := s.repo.SaveWebhook(ctx, tenantID, hook); err != nil {
		return contract.Settings{}, err
	}
	return s.Get(ctx, scope)
}

// normalize 把请求值收敛成可入库的配置；校验失败一律返回 ValidationError（映射 400）。
func normalize(in contract.WebhookConfig) (settingsrepo.Webhook, error) {
	provider := in.Provider
	if provider == "" {
		provider = contract.WebhookProviderGeneric
	}
	if !slices.Contains(contract.WebhookProviders, provider) {
		return settingsrepo.Webhook{}, &contract.ValidationError{Message: "不支持的 Webhook 类型"}
	}
	target := strings.TrimSpace(in.URL)
	secret := strings.TrimSpace(in.Secret)

	if len(target) > contract.MaxWebhookURLLen {
		return settingsrepo.Webhook{}, &contract.ValidationError{
			Message: fmt.Sprintf("Webhook 地址不能超过 %d 个字符", contract.MaxWebhookURLLen),
		}
	}
	if len(secret) > contract.MaxWebhookSecretLen {
		return settingsrepo.Webhook{}, &contract.ValidationError{
			Message: fmt.Sprintf("签名密钥不能超过 %d 个字符", contract.MaxWebhookSecretLen),
		}
	}
	// 只有启用时才要求地址合法：关掉 Webhook 的人不必先清空地址。
	if in.Enabled {
		normalized, err := contract.NormalizeHTTPURL(target)
		if err != nil {
			return settingsrepo.Webhook{}, &contract.ValidationError{Message: "Webhook 地址" + err.Error()}
		}
		target = normalized
		if err := webhooksvc.ValidateProviderURL(provider, target); err != nil {
			return settingsrepo.Webhook{}, err
		}
	}

	fields := dedupe(in.Fields)
	if len(fields) == 0 {
		return settingsrepo.Webhook{}, &contract.ValidationError{Message: "至少选择一个推送字段"}
	}
	for _, field := range fields {
		if !contract.IsValidWebhookField(field) {
			return settingsrepo.Webhook{}, &contract.ValidationError{
				Message: fmt.Sprintf("不支持推送字段：%s", field),
			}
		}
	}
	if provider == contract.WebhookProviderFeishu && !slices.Contains(fields, contract.FieldDownloadPageURL) {
		return settingsrepo.Webhook{}, &contract.ValidationError{Message: "飞书通知必须包含下载页地址"}
	}

	return settingsrepo.Webhook{Provider: provider, Enabled: in.Enabled, URL: target, Secret: secret, Fields: fields}, nil
}

// dedupe 去重并保持前端勾选顺序，避免同一字段推两遍。
func dedupe(fields []string) []string {
	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		result = append(result, field)
	}
	return result
}

// fieldOptions 按契约定义的展示顺序生成字段目录。
func fieldOptions() []contract.WebhookFieldOption {
	options := make([]contract.WebhookFieldOption, 0, len(contract.WebhookFields))
	for _, key := range contract.WebhookFields {
		options = append(options, contract.WebhookFieldOption{Key: key, Label: contract.WebhookFieldLabels[key]})
	}
	return options
}

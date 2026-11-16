// Package settings · 系统设置的业务层。
// 职责：组装设置页数据、校验并保存 Webhook 配置、对外提供 API Key。
// 输入：repository/settings 的读写能力、contract 的字段目录与上限。
// 输出：contract.Settings（含 availableFields 与 apiKey）。
// 边界：不碰 HTTP、不投递 Webhook（在 service/webhook）、不做数据库方言拼装。
package settings

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"dandelion/server/internal/contract"
	settingsrepo "dandelion/server/internal/repository/settings"
)

// Service 是设置页的唯一业务入口。
type Service struct {
	repo *settingsrepo.Repository
}

func NewService(repo *settingsrepo.Repository) *Service {
	return &Service{repo: repo}
}

// Get 返回设置页所需的全部数据：Webhook 配置、API Key、可选字段目录。
// 字段目录由契约生成，前端据此渲染复选框，不再硬编码一份中文标签。
func (s *Service) Get(ctx context.Context) (contract.Settings, error) {
	cfg, err := s.repo.Load(ctx)
	if err != nil {
		return contract.Settings{}, err
	}
	return contract.Settings{
		Webhook: contract.WebhookConfig{
			Enabled: cfg.Enabled,
			URL:     cfg.URL,
			Secret:  cfg.Secret,
			Fields:  cfg.Fields,
		},
		APIKey:          cfg.APIKey,
		AvailableFields: fieldOptions(),
	}, nil
}

// APIKey 供外部接口的中间件取当前 Key；未配置时返回空串，由中间件按 401 处理。
func (s *Service) APIKey(ctx context.Context) (string, error) {
	cfg, err := s.repo.Load(ctx)
	if err != nil {
		return "", err
	}
	return cfg.APIKey, nil
}

// UpdateWebhook 校验后整体保存，并返回更新后的设置，前端一次拿到权威值。
func (s *Service) UpdateWebhook(ctx context.Context, in contract.WebhookConfig) (contract.Settings, error) {
	hook, err := normalize(in)
	if err != nil {
		return contract.Settings{}, err
	}
	if err := s.repo.SaveWebhook(ctx, hook); err != nil {
		return contract.Settings{}, err
	}
	return s.Get(ctx)
}

// normalize 把请求值收敛成可入库的配置；校验失败一律返回 ValidationError（映射 400）。
func normalize(in contract.WebhookConfig) (settingsrepo.Webhook, error) {
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
		parsed, err := url.Parse(target)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return settingsrepo.Webhook{}, &contract.ValidationError{
				Message: "Webhook 地址必须是 http:// 或 https:// 开头的绝对地址",
			}
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

	return settingsrepo.Webhook{Enabled: in.Enabled, URL: target, Secret: secret, Fields: fields}, nil
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

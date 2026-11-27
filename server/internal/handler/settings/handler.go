// Package settings · 按公司范围与 settings.read/write 权限控制的平台集成接口。
// 职责：读取设置、轮换 API Key、保存 Webhook 配置、触发一次测试投递。
// 输入：HTTP 请求（JSON）；输出：统一 JSON 响应。
// 边界：不做校验（在 service/settings）、不实现发送（在 service/webhook）；
//
//	归属由登录身份推导；超级管理员维护平台兜底配置。
package settings

import (
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	settingssvc "dandelion/server/internal/service/settings"
	tunnelsvc "dandelion/server/internal/service/tunnel"
	webhooksvc "dandelion/server/internal/service/webhook"
)

// Handler 持有设置业务、Webhook 投递与审计三个能力。
// 测试投递属于「设置页的动作」，所以放在这里，而不是再开一个 webhook 接口组。
type Handler struct {
	settings *settingssvc.Service
	// r2 是对象存储配置的独立业务入口，与 settings 无耦合：
	// 前者管「文件存哪」，后者管「发布后通知谁」，两个变化原因互不相干。
	r2      *settingssvc.R2Service
	tunnel  *tunnelsvc.Service
	webhook *webhooksvc.Service
	audit   *auditsvc.Service
}

func NewHandler(
	settings *settingssvc.Service,
	r2 *settingssvc.R2Service,
	tunnel *tunnelsvc.Service,
	webhook *webhooksvc.Service,
	audit *auditsvc.Service,
) *Handler {
	return &Handler{settings: settings, r2: r2, tunnel: tunnel, webhook: webhook, audit: audit}
}

// Get 返回 Webhook 配置、API Key 与可选字段目录。
// 归属公司从 scope 推导：平台管理员只能看到本公司配置，超管看平台兜底配置。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	data, err := h.settings.Get(r.Context(), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Update 覆盖式保存 Webhook 配置；省略 provider 时兼容通用 JSON。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.WebhookConfig
	if err := handler.DecodeJSON(w, r, &in, "provider/enabled/url/secret/fields"); err != nil {
		return err
	}
	data, err := h.settings.UpdateWebhook(r.Context(), authctx.ScopeFrom(r.Context()), in)
	if err != nil {
		return err
	}
	// 改 Webhook 等于改「发布成功会通知谁」，必须留痕。secret 不进审计。
	h.audit.Record(r.Context(), authctx.From(r.Context()), "settings.webhook_updated", "settings", "webhook",
		map[string]any{"enabled": data.Webhook.Enabled, "provider": data.Webhook.Provider, "fields": data.Webhook.Fields},
		handler.ClientIP(r))
	return handler.OK(w, data)
}

// RotateAPIKey 轮换本公司外部接口 Key：旧 Key 立即失效，新 Key 只在本响应里出现一次。
func (h *Handler) RotateAPIKey(w http.ResponseWriter, r *http.Request) error {
	key, err := h.settings.RotateAPIKey(r.Context(), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	// 只记「发生了轮换」，绝不记录 Key 本身。
	h.audit.Record(r.Context(), authctx.From(r.Context()), "apikey.rotated", "settings", "api-key",
		map[string]any{}, handler.ClientIP(r))
	return handler.OK(w, contract.ApiKeyResult{ApiKey: key})
}

// Test 发一条示例消息；目标不可达也返回 200，结果由 delivered / statusCode / message 表达。
// 用当前 scope 对应公司的配置投递——测的必须是"我这家的回调能不能通"。
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) error {
	result, err := h.webhook.Test(r.Context(), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, result)
}

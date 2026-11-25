// Package settings · 平台设置接口（仅超级管理员）。
// 职责：读取设置、轮换 API Key、保存 Webhook 配置、触发一次测试投递。
// 输入：HTTP 请求（JSON）；输出：统一 JSON 响应。
// 边界：不做校验（在 service/settings）、不实现发送（在 service/webhook）；
//
//	整组由路由层的 RequireSuperAdmin 守门——密钥与平台配置不对普通账号开放。
package settings

import (
	"context"
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	settingssvc "dandelion/server/internal/service/settings"
	webhooksvc "dandelion/server/internal/service/webhook"
)

// Handler 持有设置业务、Webhook 投递与审计三个能力。
// 测试投递属于「设置页的动作」，所以放在这里，而不是再开一个 webhook 接口组。
type Handler struct {
	settings *settingssvc.Service
	webhook  *webhooksvc.Service
	audit    *auditsvc.Service
}

func NewHandler(settings *settingssvc.Service, webhook *webhooksvc.Service, audit *auditsvc.Service) *Handler {
	return &Handler{settings: settings, webhook: webhook, audit: audit}
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

// Update 覆盖式保存 Webhook 配置；请求体恰好 4 个字段。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.WebhookConfig
	if err := handler.DecodeJSON(w, r, &in, "enabled/url/secret/fields"); err != nil {
		return err
	}
	data, err := h.settings.UpdateWebhook(r.Context(), authctx.ScopeFrom(r.Context()), in)
	if err != nil {
		return err
	}
	// 改 Webhook 等于改「发布成功会通知谁」，必须留痕。secret 不进审计。
	h.audit.Record(r.Context(), authctx.From(r.Context()), "settings.webhook_updated", "settings", "webhook",
		map[string]any{"enabled": data.Webhook.Enabled, "url": data.Webhook.URL, "fields": data.Webhook.Fields},
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

// GetGitRepo 返回当前公司精确配置和只读允许根提示。
func (h *Handler) GetGitRepo(w http.ResponseWriter, r *http.Request) error {
	data, err := h.settings.GetGitRepo(r.Context(), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// UpdateGitRepo 写入配置；审计只记 configured 布尔，不记录绝对路径。
func (h *Handler) UpdateGitRepo(w http.ResponseWriter, r *http.Request) error {
	var in contract.GitRepoInput
	if err := handler.DecodeJSON(w, r, &in, "gitRepoPath"); err != nil {
		return err
	}
	data, err := h.settings.UpdateGitRepo(r.Context(), authctx.ScopeFrom(r.Context()), in)
	if err != nil {
		return err
	}
	scope := authctx.ScopeFrom(r.Context())
	h.audit.Record(context.WithoutCancel(r.Context()), authctx.From(r.Context()), "rdc.delivery.repo_configured", "settings", scope.TenantID, map[string]any{"configured": data.Configured}, handler.ClientIP(r))
	return handler.OK(w, data)
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

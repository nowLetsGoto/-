// Package settings · 系统设置页的三个接口。
// 职责：读取设置、保存 Webhook 配置、触发一次测试投递。
// 输入：HTTP 请求（JSON）；输出：统一 JSON 响应。
// 边界：不做校验（在 service/settings）、不实现发送（在 service/webhook）。
package settings

import (
	"net/http"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	settingssvc "dandelion/server/internal/service/settings"
	webhooksvc "dandelion/server/internal/service/webhook"
)

// Handler 持有设置业务与 Webhook 投递两个能力。
// 测试投递属于「设置页的动作」，所以放在这里，而不是再开一个 webhook 接口组。
type Handler struct {
	settings *settingssvc.Service
	webhook  *webhooksvc.Service
}

func NewHandler(settings *settingssvc.Service, webhook *webhooksvc.Service) *Handler {
	return &Handler{settings: settings, webhook: webhook}
}

// Get 返回 Webhook 配置、API Key 与可选字段目录。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	data, err := h.settings.Get(r.Context())
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
	data, err := h.settings.UpdateWebhook(r.Context(), in)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Test 发一条示例消息；目标不可达也返回 200，结果由 delivered / statusCode / message 表达。
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) error {
	result, err := h.webhook.Test(r.Context())
	if err != nil {
		return err
	}
	return handler.OK(w, result)
}

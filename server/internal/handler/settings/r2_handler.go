// Package settings · R2 配置的 HTTP 语义层。
// 职责：读取 R2 配置、保存配置、触发一次连接测试。
// 输入：HTTP 请求（JSON）；输出：统一 JSON 响应。
// 边界：不做校验（在 service/settings）、不构造客户端（在 service/r2registry）、
// 不实现测试逻辑（在 service/settings 的 Test）。
//
// 与 handler.go 分开的原因：那个文件已承载 Webhook 与 API Key 两条流程。
// R2 配置是独立的功能面（存储通道），单独成文件便于按功能定位。
package settings

import (
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
)

// GetR2 返回 R2 配置（密钥只表达「配没配过」）。
func (h *Handler) GetR2(w http.ResponseWriter, r *http.Request) error {
	data, err := h.r2.Get(r.Context(), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// UpdateR2 覆盖式保存 R2 配置，返回保存后的页面数据。
//
// 保存成功后立即热生效：由 service 层的注册表重建客户端，
// 无需重启进程。页面收到响应即可认为新配置已可用。
func (h *Handler) UpdateR2(w http.ResponseWriter, r *http.Request) error {
	var in contract.R2UpdateRequest
	if err := handler.DecodeJSON(w, r, &in, "accountId/accessKeyId/bucket/publicBaseUrl/secret/changeSecret"); err != nil {
		return err
	}

	data, err := h.r2.Update(r.Context(), authctx.ScopeFrom(r.Context()),
		contract.R2Config{
			AccountID:     in.AccountID,
			AccessKeyID:   in.AccessKeyID,
			Bucket:        in.Bucket,
			PublicBaseURL: in.PublicBaseURL,
		}, in.Secret, in.ChangeSecret)
	if err != nil {
		return err
	}

	// 改存储凭据等于改「文件存到哪里」，必须留痕。密钥本身不进审计。
	h.audit.Record(r.Context(), authctx.From(r.Context()), "settings.r2_updated", "settings", "r2",
		map[string]any{
			"accountId":        data.AccountID,
			"bucket":           data.Bucket,
			"publicBaseUrl":    data.PublicBaseURL,
			"secretChanged":    in.ChangeSecret,
			"secretConfigured": data.SecretConfigured,
		}, handler.ClientIP(r))
	return handler.OK(w, data)
}

// TestR2 用当前保存的配置做一次连通性测试。
//
// 测的是「已保存的配置」而不是请求体里的值：
// 页面应在保存后再点测试，这样测的与线上实际用的是同一份，
// 避免"测通了但保存的不是测的那份"。
func (h *Handler) TestR2(w http.ResponseWriter, r *http.Request) error {
	result, err := h.r2.Test(r.Context(), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, result)
}

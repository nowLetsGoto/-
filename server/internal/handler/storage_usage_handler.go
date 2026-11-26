// storage_usage_handler.go · 存储用量接口。
//
// 职责：取当前会话的租户，返回该租户的存储用量视图。
// 输入：HTTP 请求（无参数，租户来自登录态）。
// 输出：contract.StorageUsage。
// 边界：不做配额计算（在 service），不查库（在 repository）。
//
// 租户一律取自登录态而非请求参数：让前端传 tenantId 等于允许越权查别人用量。
package handler

import (
	"net/http"

	"dandelion/server/internal/authctx"
)

// StorageUsage 返回当前租户的存储用量，供「应用」页头部容量条展示。
func (h *BuildHandler) StorageUsage(w http.ResponseWriter, r *http.Request) error {
	identity := authctx.From(r.Context())
	tenantID := authctx.ScopeFrom(r.Context()).TenantID
	usage, err := h.svc.StorageUsage(r.Context(), identity.Mode, tenantID)
	if err != nil {
		return err
	}
	return OK(w, usage)
}

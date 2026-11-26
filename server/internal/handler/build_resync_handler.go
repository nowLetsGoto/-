// build_resync_handler.go · 「同步到 R2」的 HTTP 语义层。
//
// 职责：取路径参数与登录态，调用 service 完成重推，返回更新后的包。
// 输入：HTTP 请求（路径含包 id，租户与分区来自登录态）。
// 输出：统一 JSON 包裹的 Build。
// 边界：不拼 storage key、不判断状态——「什么情况能重推」只由 service 决定。
//
// 独立成文件而非塞进 build_handler.go：那个文件已经承载上传、发布、下载、
// 删除等多条流程，再混入一个状态修复入口会让它继续膨胀。
package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
)

// SyncToR2 把已被容量淘汰的包重新推回对象存储。
//
// 方法必须是 POST：这个动作会真实上传对象、改变服务端状态，
// 用 GET 会让浏览器预取、代理缓存、甚至一个 <img> 标签都能触发它。
func (h *BuildHandler) SyncToR2(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)

	build, err := h.svc.SyncToR2(r.Context(), id, authctx.From(r.Context()))
	if err != nil {
		return err
	}
	return OK(w, build)
}

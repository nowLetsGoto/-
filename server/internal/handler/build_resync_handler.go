// build_resync_handler.go · 「同步到 R2」的 HTTP 语义层。
//
// 职责：取路径参数与登录态，受理重推并查询其进度。
// 输入：HTTP 请求（路径含包 id，租户与分区来自登录态）。
// 输出：202 与当前包状态；进度查询返回统一 JSON 包裹的 SyncProgress。
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

// SyncToR2 受理「把已被容量淘汰的包重新推回对象存储」。
//
// 方法必须是 POST：这个动作会真实上传对象、改变服务端状态，
// 用 GET 会让浏览器预取、代理缓存、甚至一个 <img> 标签都能触发它。
//
// 返回 202 而非等上传完成：整包上传可能超过 Cloudflare 的 100 秒代理超时。
func (h *BuildHandler) SyncToR2(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)

	build, err := h.svc.SyncToR2(r.Context(), id, authctx.From(r.Context()))
	if err != nil {
		return err
	}
	return OKWithStatus(w, http.StatusAccepted, build)
}

// RetryLocalPull 受理「重新下载到本机」。
//
// 与 SyncToR2 方向相反：那个把本机副本推上云端，这个把云端对象拉回本机。
// 同样必须 POST——它会真实下载上百 MB 并改变服务端状态。
//
// 返回 200 而非 202：调用后立刻回状态（已置为重新排队），
// 前端据此马上能画出进度条，不必等第一次轮询。
func (h *BuildHandler) RetryLocalPull(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)

	build, err := h.svc.RetryLocalPull(r.Context(), id, authctx.From(r.Context()))
	if err != nil {
		return err
	}
	return OK(w, build)
}

// SyncProgress 返回一次后台重推的实时进度。
func (h *BuildHandler) SyncProgress(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)

	progress, err := h.svc.SyncProgress(r.Context(), id, authctx.From(r.Context()))
	if err != nil {
		return err
	}
	return OK(w, progress)
}

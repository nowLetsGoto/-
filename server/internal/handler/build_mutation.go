// 职责：解码应用包编辑请求、执行删除并追加成功审计。
// 输入：登录请求、包 ID 与 JSON；输出：统一响应外壳。
// 边界：字段与权限规则由 service 处理，不写 SQL、不操作文件、不信任项目查询参数。
package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
)

func (h *BuildHandler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.BuildUpdateInput
	if err := DecodeJSON(w, r, &in, "appName/version/buildNumber/environment/changelog"); err != nil {
		return err
	}
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)
	identity := authctx.From(r.Context())
	build, err := h.svc.Update(r.Context(), id, in, identity)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), identity, "build.updated", "build", id,
		map[string]any{"appName": build.AppName, "version": build.Version, "buildNumber": build.BuildNumber,
			"environment": build.Environment, "platform": build.Platform}, ClientIP(r))
	return OK(w, build)
}

func (h *BuildHandler) Delete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)
	identity := authctx.From(r.Context())
	build, err := h.svc.Delete(r.Context(), id, identity)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), identity, "build.deleted", "build", id,
		map[string]any{"appName": build.AppName, "version": build.Version, "buildNumber": build.BuildNumber,
			"environment": build.Environment, "platform": build.Platform}, ClientIP(r))
	return OK(w, nil)
}

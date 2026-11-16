// Package project · 项目接口。
// 职责：列出项目、查看详情、新建项目、编辑项目。
// 输入：HTTP 请求（GET / POST / PUT JSON）；输出：统一 JSON 响应。
// 边界：不做校验（在 service/project）、不做权限判断（P2 的成员体系负责）。
package project

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	projectsvc "dandelion/server/internal/service/project"
)

// Handler 只依赖项目业务层。
type Handler struct {
	svc *projectsvc.Service
}

func NewHandler(svc *projectsvc.Service) *Handler {
	return &Handler{svc: svc}
}

// List 返回全部项目；下拉筛选与人员/团队关联选择都用它。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.List(r.Context())
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 返回项目详情（含关联人员/团队），供基础资料里的项目详情页使用。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	key := chi.URLParam(r, "key")
	data, err := h.svc.Detail(r.Context(), key)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Create 新建项目；请求体恰好 4 个字段（key / name / description / repoUrl）。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.ProjectInput
	if err := handler.DecodeJSON(w, r, &in, "key/name/description/repoUrl"); err != nil {
		return err
	}
	item, err := h.svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	return handler.OKWithStatus(w, http.StatusCreated, item)
}

// Update 按 key 编辑项目；key 本身不可改，因此请求体里即使带上也会被忽略。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.ProjectInput
	if err := handler.DecodeJSON(w, r, &in, "key/name/description/repoUrl"); err != nil {
		return err
	}
	key := chi.URLParam(r, "key")
	item, err := h.svc.Update(r.Context(), key, in)
	if err != nil {
		return err
	}
	return handler.OK(w, item)
}

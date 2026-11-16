// Package role · 角色与权限接口。
// 职责：列出角色（含权限点目录）、查看单个角色、新建与编辑角色。
// 输入：HTTP 请求；输出：统一 JSON 响应。
// 边界：不做校验（在 service/role）；**写操作仅超管**，由路由分组上的 RequireSuperAdmin 保证；
//       读操作对所有登录用户开放，便于大家看"系统里有哪些角色、各有哪些权限"。
package role

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	rolesvc "dandelion/server/internal/service/role"
)

// Handler 依赖角色业务层与审计。
type Handler struct {
	svc   *rolesvc.Service
	audit *auditsvc.Service
}

func NewHandler(svc *rolesvc.Service, audit *auditsvc.Service) *Handler {
	return &Handler{svc: svc, audit: audit}
}

// Permissions 返回权限点目录：前端据此渲染勾选矩阵，避免把目录抄进前端。
func (h *Handler) Permissions(w http.ResponseWriter, r *http.Request) error {
	return handler.OK(w, map[string]any{"items": contract.Permissions})
}

// List 返回全部角色（含各自权限点）。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.List(r.Context())
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 返回单个角色，供编辑页回填。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.Get(r.Context(), chi.URLParam(r, "key"))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Create 新建自定义角色。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.RoleInput
	if err := handler.DecodeJSON(w, r, &in, "key/name/description/permissions"); err != nil {
		return err
	}
	item, err := h.svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "role.created", "role", item.Key,
		map[string]any{"name": item.Name, "permissions": item.Permissions}, handler.ClientIP(r))
	return handler.OKWithStatus(w, http.StatusCreated, item)
}

// Update 覆盖角色名称 / 描述 / 权限。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.RoleInput
	if err := handler.DecodeJSON(w, r, &in, "key/name/description/permissions"); err != nil {
		return err
	}
	key := chi.URLParam(r, "key")
	item, err := h.svc.Update(r.Context(), key, in)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "role.updated", "role", item.Key,
		map[string]any{"name": item.Name, "permissions": item.Permissions}, handler.ClientIP(r))
	return handler.OK(w, item)
}

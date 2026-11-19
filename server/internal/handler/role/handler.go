// Package role · 角色与权限接口。
// 职责：列出角色（含权限点目录）、查看单个角色、新建与编辑角色。
// 输入：HTTP 请求；输出：统一 JSON 响应。
// 边界：不做校验（在 service/role）；
//       **整块接口仅超管可用**（含权限目录与角色列表），由路由分组上的
//       RequireSuperAdmin 保证——角色模型本身就是管理信息，普通账号不需要看到。
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

// Menus 返回平台级菜单目录，供角色授权页渲染菜单树。
//
// 只读且无业务参数：目录由 contract.Menus 静态定义，任何公司都不能增删改，
// 所以这里不需要 scope，也不需要 service 层——纯粹把契约投影成 JSON。
func (h *Handler) Menus(w http.ResponseWriter, r *http.Request) error {
	return handler.OK(w, map[string]any{"items": contract.AllMenus()})
}

// List 返回全部角色（含各自权限点）。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.List(r.Context(), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 返回单个角色，供编辑页回填。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"), authctx.ScopeFrom(r.Context()))
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
	item, err := h.svc.Create(r.Context(), in, authctx.ScopeFrom(r.Context()))
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
	id := chi.URLParam(r, "id")
	item, err := h.svc.Update(r.Context(), id, in, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "role.updated", "role", item.Key,
		map[string]any{"name": item.Name, "permissions": item.Permissions}, handler.ClientIP(r))
	return handler.OK(w, item)
}

// Delete 删除本公司自定义角色；内置角色、超管角色、有引用的角色都会被 service 拒绝。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	// 先取角色再删：审计要记 key（人可读），而请求只带 id。
	role, err := h.svc.Get(r.Context(), id, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	if err := h.svc.Delete(r.Context(), id, authctx.ScopeFrom(r.Context())); err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "role.deleted", "role", role.Key,
		nil, handler.ClientIP(r))
	return handler.OK(w, nil)
}

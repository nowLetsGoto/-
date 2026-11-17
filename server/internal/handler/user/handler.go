// Package user · 账号管理接口（仅超级管理员可访问，由路由分组上的中间件保证）。
// 职责：列表 / 新建 / 编辑 / 重置密码 / 启用禁用 / 删除，以及「登录人改自己的密码」。
// 输入：HTTP 请求；输出：统一 JSON 响应。
// 边界：不做校验与护栏（在 service/user）、不判断超管身份（在 middleware/login）；
//
//	改自己密码只依赖登录态，不要求超管，因此它挂在 private 分组而不是 admin 分组。
package user

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	usersvc "dandelion/server/internal/service/user"
)

// Handler 依赖账号业务层与审计：每个写操作都要留下"谁改了谁"。
type Handler struct {
	svc   *usersvc.Service
	audit *auditsvc.Service
}

func NewHandler(svc *usersvc.Service, audit *auditsvc.Service) *Handler {
	return &Handler{svc: svc, audit: audit}
}

// List 按关键字与分页返回账号列表。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.List(r.Context(), handler.ReadPage(r), r.URL.Query().Get("keyword"))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Create 新建账号；请求体 4 个字段（username / password / personId / isSuperAdmin）。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.UserInput
	if err := handler.DecodeJSON(w, r, &in, "username/password/personId/isSuperAdmin"); err != nil {
		return err
	}
	item, err := h.svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "user.created", "user", item.ID,
		map[string]any{"username": item.Username, "isSuperAdmin": item.IsSuperAdmin}, handler.ClientIP(r))
	return handler.OKWithStatus(w, http.StatusCreated, item)
}

// Update 改超管标记与绑定人员。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.UserUpdate
	if err := handler.DecodeJSON(w, r, &in, "personId/isSuperAdmin"); err != nil {
		return err
	}
	caller := authctx.From(r.Context())
	item, err := h.svc.Update(r.Context(), caller, chi.URLParam(r, "id"), in)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), caller, "user.updated", "user", item.ID,
		map[string]any{"username": item.Username, "isSuperAdmin": item.IsSuperAdmin, "personId": item.PersonID}, handler.ClientIP(r))
	return handler.OK(w, item)
}

// ResetPassword 生成一次性临时密码；明文只出现在本次响应里。
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) error {
	password, err := h.svc.ResetPassword(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	// 只记"发生了重置"，绝不记录密码本身。
	h.audit.Record(r.Context(), authctx.From(r.Context()), "user.password_reset", "user",
		chi.URLParam(r, "id"), map[string]any{}, handler.ClientIP(r))
	return handler.OK(w, contract.PasswordReset{Password: password})
}

// SetStatus 启用 / 禁用账号。
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) error {
	var in contract.UserStatusInput
	if err := handler.DecodeJSON(w, r, &in, "status"); err != nil {
		return err
	}
	caller := authctx.From(r.Context())
	item, err := h.svc.SetStatus(r.Context(), caller, chi.URLParam(r, "id"), in.Status)
	if err != nil {
		return err
	}
	action := "user.enabled"
	if item.Status == "disabled" {
		action = "user.disabled"
	}
	h.audit.Record(r.Context(), caller, action, "user", item.ID,
		map[string]any{"username": item.Username, "status": item.Status}, handler.ClientIP(r))
	return handler.OK(w, item)
}

// Delete 物理删除账号；成功返回被删的记录（前端据此提示"已删除 xxx"）。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	caller := authctx.From(r.Context())
	item, err := h.svc.Delete(r.Context(), caller, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), caller, "user.deleted", "user", item.ID,
		map[string]any{"username": item.Username, "isSuperAdmin": item.IsSuperAdmin}, handler.ClientIP(r))
	return handler.OK(w, item)
}

// ChangePassword 修改当前登录账号自己的密码；成功后旧令牌立即失效。
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) error {
	var in contract.PasswordChange
	if err := handler.DecodeJSON(w, r, &in, "oldPassword/newPassword"); err != nil {
		return err
	}
	caller := authctx.From(r.Context())
	if err := h.svc.ChangeOwnPassword(r.Context(), caller, in); err != nil {
		return err
	}
	// 同样只记"发生了修改"，绝不记录新旧密码本身。
	h.audit.Record(r.Context(), caller, "user.password_changed", "user", caller.UserID,
		map[string]any{"self": true}, handler.ClientIP(r))
	return handler.OK(w, nil)
}

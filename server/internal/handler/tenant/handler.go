// Package tenant · 公司管理接口（仅平台超级管理员可访问，由路由中间件保证）。
// 职责：列出公司、新建公司（连同平台管理员账号）。
// 输入：HTTP 请求；输出：统一 JSON 响应。
// 边界：不做校验与业务判断（在 service/tenant）、不判断超管身份（在 middleware）；
//
//	新建返回的明文密码只在本次响应出现，后续只能重置。
package tenant

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	tenantsvc "dandelion/server/internal/service/tenant"
)

// Handler 依赖公司业务层与审计：新建公司是最高的管理动作，必须留痕。
type Handler struct {
	svc   *tenantsvc.Service
	audit *auditsvc.Service
}

func NewHandler(svc *tenantsvc.Service, audit *auditsvc.Service) *Handler {
	return &Handler{svc: svc, audit: audit}
}

// List 分页返回公司列表。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.List(r.Context(), handler.ReadPage(r))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Create 新建公司与它的平台管理员；请求体 4 个字段。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.TenantInput
	if err := handler.DecodeJSON(w, r, &in, "name/code/adminUsername/adminPassword"); err != nil {
		return err
	}
	result, err := h.svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	caller := authctx.From(r.Context())
	h.audit.Record(r.Context(), caller, "tenant.created", "tenant", result.Tenant.ID,
		map[string]any{
			"name": result.Tenant.Name, "code": result.Tenant.Code,
			"adminUsername": result.Tenant.AdminUsername,
		}, handler.ClientIP(r))
	return handler.OKWithStatus(w, http.StatusCreated, result)
}

// SetStatus 启用 / 停用公司；请求体只有 status 一个字段。
//
// 停用是最影响面最大的管理动作之一（该公司所有人立刻登不上），因此留痕，
// 且把"改成什么状态"写进审计详情——只记"状态变了"无法回答"是谁停的、停成什么"。
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Status string `json:"status"`
	}
	if err := handler.DecodeJSON(w, r, &in, "status"); err != nil {
		return err
	}
	item, err := h.svc.SetStatus(r.Context(), chi.URLParam(r, "id"), in.Status)
	if err != nil {
		return err
	}
	caller := authctx.From(r.Context())
	h.audit.Record(r.Context(), caller, "tenant.status_changed", "tenant", item.ID,
		map[string]any{"name": item.Name, "status": item.Status}, handler.ClientIP(r))
	return handler.OK(w, item)
}

// Delete 删除空公司；有数据时由 service 拒绝并说明还剩什么。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "tenant.deleted", "tenant", id,
		nil, handler.ClientIP(r))
	return handler.OK(w, nil)
}

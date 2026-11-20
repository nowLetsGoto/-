// Package project · 项目接口。
// 职责：列出项目、查看详情、新建项目、编辑项目。
// 输入：HTTP 请求（GET / POST / PUT JSON）与登录身份；输出：统一 JSON 响应。
// 边界：不做校验（在 service/project）、不做权限判断（中间件与 service 负责）；
// 这里只负责把「身份」翻译成「视图」，不做任何 SQL 或分区选择。
package project

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	projectsvc "dandelion/server/internal/service/project"
)

// Handler 只依赖项目业务层。
// Handler 依赖项目业务层与审计。项目是包的一级归属，
// 改名/删除都影响历史可追溯性，因此增删改一律留痕。
type Handler struct {
	svc   *projectsvc.Service
	audit *auditsvc.Service
}

func NewHandler(svc *projectsvc.Service, audit *auditsvc.Service) *Handler {
	return &Handler{svc: svc, audit: audit}
}

// List 返回项目：单人模式只有本人的项目，团队模式是全量。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	identity := authctx.From(r.Context())
	data, err := h.svc.List(r.Context(), handler.ReadPage(r), projectView(identity))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 返回项目详情（含关联人员/团队），供基础资料里的项目详情页使用。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	key := chi.URLParam(r, "key")
	identity := authctx.From(r.Context())
	data, err := h.svc.Detail(r.Context(), key, projectView(identity))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// projectView 把会话身份翻译成项目视图。
// 单人模式下 owner 条件恒为当前账号：solo 分区的数据按人隔离，不是可选开关。
// 团队项目是共享资产，不做 owner 过滤。
func projectView(identity contract.Identity) projectsvc.View {
	return projectsvc.View{
		Solo:        identity.Mode == contract.ModeSolo,
		Mode:        identity.Mode,
		OwnerUserID: identity.UserID, // 团队模式下仓储不认这个字段，填了也无影响
	}
}

// Create 新建项目；请求体恰好 5 个字段（key / name / description / repoUrl / roleKey）。
// 单人模式也允许自建：项目是自己上传包的前提，不需要管理员先建好。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.ProjectInput
	if err := handler.DecodeJSON(w, r, &in, "key/name/description/repoUrl/iconUrl/roleKey"); err != nil {
		return err
	}
	identity := authctx.From(r.Context())
	// 项目角色决定所有成员的权限；不能随普通项目创建一起授予。
	if in.RoleKey != "" && !identity.IsSuperAdmin {
		return contract.ErrForbidden
	}
	item, err := h.svc.Create(r.Context(), in, projectView(identity))
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), identity, "project.created", "project", item.Key,
		map[string]any{"name": item.Name, "roleKey": item.RoleKey}, handler.ClientIP(r))
	return handler.OKWithStatus(w, http.StatusCreated, item)
}

// Update 按 key 编辑项目；key 本身不可改，因此请求体里即使带上也会被忽略。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.ProjectInput
	if err := handler.DecodeJSON(w, r, &in, "key/name/description/repoUrl/iconUrl/roleKey"); err != nil {
		return err
	}
	identity := authctx.From(r.Context())
	key := chi.URLParam(r, "key")
	view := projectView(identity)
	if !identity.IsSuperAdmin {
		current, err := h.svc.Detail(r.Context(), key, view)
		if err != nil {
			return err
		}
		// 只有超管能改项目角色；普通项目编辑者不能借此给自己提权。
		if in.RoleKey != current.RoleKey {
			return contract.ErrForbidden
		}
	}
	item, err := h.svc.Update(r.Context(), key, in, view)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), identity, "project.updated", "project", item.Key,
		map[string]any{"name": item.Name, "roleKey": item.RoleKey}, handler.ClientIP(r))
	return handler.OK(w, item)
}

// Delete 按 key 删除项目；有包/成员/团队关联时由 service 拒绝并说明原因。
//
// 这里不记审计：项目的创建与编辑本来就不记（Handler 没有 audit 依赖），
// 只为"删除"单独引入会让审计日志出现残缺的单一事件——要么整套补，要么都不记。
// 删除是破坏性操作，确实该有审计，但那属于"给项目模块整体接审计"的独立改动。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	key := chi.URLParam(r, "key")
	identity := authctx.From(r.Context())
	if err := h.svc.Delete(r.Context(), key, projectView(identity)); err != nil {
		return err
	}
	h.audit.Record(r.Context(), identity, "project.deleted", "project", key,
		nil, handler.ClientIP(r))
	return handler.OK(w, nil)
}

// Package teams · 团队接口。
// 职责：列出团队、新建 / 编辑团队（含关联人员与项目的多选结果）。
// 输入：HTTP 请求（GET / POST / PUT JSON）；输出：统一 JSON 响应。
// 边界：不做校验（在 service/teams）、不做权限判断。
package teams

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	"dandelion/server/internal/authctx"
	auditsvc "dandelion/server/internal/service/audit"
	teamssvc "dandelion/server/internal/service/teams"
)

// Handler 只依赖团队业务层。
// Handler 依赖团队业务层与审计：团队决定成员能看到哪些数据，
// 增删改必须在审计里留痕，否则"某天某人突然看不到数据"无从追溯。
type Handler struct {
	svc   *teamssvc.Service
	audit *auditsvc.Service
}

func NewHandler(svc *teamssvc.Service, audit *auditsvc.Service) *Handler {
	return &Handler{svc: svc, audit: audit}
}

// List 返回全部团队，已带关联的人员与项目。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.List(r.Context(), handler.ReadPage(r))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 返回单个团队（含关联），供编辑页回填。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	item, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	return handler.OK(w, item)
}

// Create 新建团队；请求体 4 个字段（含两个多选 ID 数组）。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.TeamInput
	if err := handler.DecodeJSON(w, r, &in, "name/description/personIds/projectIds"); err != nil {
		return err
	}
	item, err := h.svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "team.created", "team", item.ID,
		map[string]any{"name": item.Name}, handler.ClientIP(r))
	return handler.OKWithStatus(w, http.StatusCreated, item)
}

// Update 全量覆盖团队字段与关联。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.TeamInput
	if err := handler.DecodeJSON(w, r, &in, "name/description/personIds/projectIds"); err != nil {
		return err
	}
	item, err := h.svc.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		return err
	}
	// 记下成员数与项目数：团队关联的变化直接改变成员的可视范围，
	// 只记"团队被编辑"无法回答"是谁把谁加进来/移出去"。
	h.audit.Record(r.Context(), authctx.From(r.Context()), "team.updated", "team", item.ID,
		map[string]any{
			"name":      item.Name,
			"personIds": len(item.Persons),
			"projectIds": len(item.Projects),
		}, handler.ClientIP(r))
	return handler.OK(w, item)
}

// Delete 删除团队；只允许删没有任何引用（成员/项目/管理员）的团队。
// 拒绝原因由 service 写进 400 的 message，这里只做转发。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "team.deleted", "team", id,
		nil, handler.ClientIP(r))
	return handler.OK(w, nil)
}

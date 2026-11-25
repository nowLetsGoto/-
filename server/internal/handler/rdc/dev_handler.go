// Package rdc · 研发任务编排层的 HTTP 语义层。
// 职责：读取分页/筛选与路径参数、严格 DecodeJSON、调用业务层、写 5 类审计、统一响应。
// 输入：HTTP 请求（列表/详情/新建/编辑/删除/推进/跳过）与上下文中的身份、scope。
// 输出：{code,data,message} JSON 外壳；新建 201，其余成功 200。
// 边界：不写 SQL、不做字段业务校验（在 service）、不判权限（在路由中间件）；
//
//	编辑白名单不含 status，DisallowUnknownFields 拒绝任意层级的 status；
//	审计只记日志不回滚，使用 WithoutCancel 保证客户端断开也能写完。
package rdc

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	rdcsvc "dandelion/server/internal/service/rdc"
)

// 5 类审计 action，与 api/openapi.yaml 描述和设计 §3.6 一一对应。
const (
	devActionCreated      = "rdc.dev_task.created"
	devActionUpdated      = "rdc.dev_task.updated"
	devActionDeleted      = "rdc.dev_task.deleted"
	devActionTransitioned = "rdc.dev_subtask.transitioned"
	devActionSkipped      = "rdc.dev_subtask.skipped"

	devObjectTask    = "rdc_dev_task"
	devObjectSubtask = "rdc_dev_subtask"
)

// DevHandler 依赖研发任务业务层与审计服务。
type DevHandler struct {
	svc   *rdcsvc.DevService
	audit *auditsvc.Service
}

// NewDevHandler 构造研发任务 HTTP 处理器。
func NewDevHandler(svc *rdcsvc.DevService, audit *auditsvc.Service) *DevHandler {
	return &DevHandler{svc: svc, audit: audit}
}

// List 返回 scope 内的研发任务分页列表；page/pageSize/reqKey/status 来自查询参数。
func (h *DevHandler) List(w http.ResponseWriter, r *http.Request) error {
	query := contract.DevTaskQuery{
		Page:   handler.ReadPage(r),
		ReqKey: r.URL.Query().Get("reqKey"),
		Status: r.URL.Query().Get("status"),
	}
	data, err := h.svc.List(r.Context(), query, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 按路径 id 返回任务详情（含两子任务）；非法 id 与越界统一 404。
func (h *DevHandler) Get(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Create 新建任务并自动拆两条 pending 子任务；请求体只接受 title/requirementId。
func (h *DevHandler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.DevTaskInput
	if err := decodeDevCreate(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	identity := authctx.From(ctx)
	detail, err := h.svc.Create(ctx, identity, authctx.ScopeFrom(ctx), in)
	if err != nil {
		return err
	}
	h.record(ctx, identity, r, devActionCreated, devObjectTask, detail.ID, map[string]any{
		"taskId": detail.ID, "title": detail.Title,
		"requirementId": detail.RequirementID, "reqKey": detail.ReqKey,
		"creatorUserId": identity.UserID,
	})
	return handler.OKWithStatus(w, http.StatusCreated, detail)
}

// Update 只改任务名 / 绑定需求 / 子任务备注。
// 顶层或 subtasks[] 内出现 status 都会在 decodeDevUpdate 被拒（400），绝不进入 service。
func (h *DevHandler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.DevTaskUpdate
	if err := decodeDevUpdate(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	identity := authctx.From(ctx)
	id := chi.URLParam(r, "id")
	detail, err := h.svc.Update(ctx, id, in, authctx.ScopeFrom(ctx))
	if err != nil {
		return err
	}
	subtaskIDs := make([]string, 0, len(detail.Subtasks))
	for _, subtask := range detail.Subtasks {
		subtaskIDs = append(subtaskIDs, subtask.ID)
	}
	h.record(ctx, identity, r, devActionUpdated, devObjectTask, id, map[string]any{
		"taskId": id, "title": detail.Title,
		"requirementId": detail.RequirementID, "reqKey": detail.ReqKey,
		"subtaskIds": subtaskIDs,
	})
	return handler.OK(w, detail)
}

// Delete 删除任务（子任务级联）。审计需要标题与需求编号，因此先按 scope 读一次再删。
func (h *DevHandler) Delete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	identity := authctx.From(ctx)
	id := chi.URLParam(r, "id")
	before, err := h.svc.Get(ctx, id, authctx.ScopeFrom(ctx))
	if err != nil {
		return err
	}
	if err := h.svc.Delete(ctx, id, authctx.ScopeFrom(ctx)); err != nil {
		return err
	}
	h.record(ctx, identity, r, devActionDeleted, devObjectTask, id, map[string]any{
		"taskId": id, "title": before.Title, "reqKey": before.ReqKey,
		"subtaskCount": len(before.Subtasks),
	})
	return handler.OK(w, nil)
}

// Advance 手动推进子任务一态；审计 detail 记录 taskId / kind / from / to。
func (h *DevHandler) Advance(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	identity := authctx.From(ctx)
	taskID := chi.URLParam(r, "id")
	subtaskID := chi.URLParam(r, "subtaskId")
	// from 直接取 service 实际用于条件更新的快照值，避免二次预读在并发下拼出非法跳级对。
	subtask, from, err := h.svc.Advance(ctx, taskID, subtaskID, authctx.ScopeFrom(ctx))
	if err != nil {
		return err
	}
	h.record(ctx, identity, r, devActionTransitioned, devObjectSubtask, subtaskID, map[string]any{
		"taskId": taskID, "kind": subtask.Kind, "from": from, "to": subtask.Status,
	})
	return handler.OK(w, subtask)
}

// Skip 标记子任务 SKIPPED（永久不可恢复）；审计 detail 同样带 kind / from / to。
func (h *DevHandler) Skip(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	identity := authctx.From(ctx)
	taskID := chi.URLParam(r, "id")
	subtaskID := chi.URLParam(r, "subtaskId")
	// 同上：审计 from 与条件更新的 from 必须来自同一次状态快照。
	subtask, from, err := h.svc.Skip(ctx, taskID, subtaskID, authctx.ScopeFrom(ctx))
	if err != nil {
		return err
	}
	h.record(ctx, identity, r, devActionSkipped, devObjectSubtask, subtaskID, map[string]any{
		"taskId": taskID, "kind": subtask.Kind, "from": from, "to": subtask.Status,
	})
	return handler.OK(w, subtask)
}

// Resubmit 驳回后重新研发（rejected → ai_completed），研发中心触发。
// 与 Advance/Skip 同构：审计 from 与条件更新的 from 来自同一次状态快照。
func (h *DevHandler) Resubmit(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	identity := authctx.From(ctx)
	taskID := chi.URLParam(r, "id")
	subtaskID := chi.URLParam(r, "subtaskId")
	subtask, from, err := h.svc.ApplyReviewAction(
		ctx, contract.ReviewActionResubmit, taskID, subtaskID, authctx.ScopeFrom(ctx))
	if err != nil {
		return err
	}
	h.record(ctx, identity, r, devActionTransitioned, devObjectSubtask, subtaskID, map[string]any{
		"taskId": taskID, "kind": subtask.Kind, "from": from, "to": subtask.Status, "resubmit": true,
	})
	return handler.OK(w, subtask)
}

// record 写审计；WithoutCancel 让客户端断开时仍能写完，Record 内部失败只记日志不回滚。
func (h *DevHandler) record(
	ctx context.Context,
	identity contract.Identity,
	r *http.Request,
	action, objectType, objectID string,
	detail map[string]any,
) {
	if h.audit == nil {
		return
	}
	h.audit.Record(context.WithoutCancel(ctx), identity, action, objectType, objectID,
		detail, handler.ClientIP(r))
}

// decodeDevCreate 严格白名单：title/requirementId，未知字段一律 400。
func decodeDevCreate(w http.ResponseWriter, r *http.Request, target *contract.DevTaskInput) error {
	return handler.DecodeJSON(w, r, target, "title/requirementId")
}

// decodeDevUpdate 严格白名单：title/requirementId/subtasks，**不含 status**。
// json.Decoder 的 DisallowUnknownFields 递归生效，subtasks[] 里的 status 同样被拒。
func decodeDevUpdate(w http.ResponseWriter, r *http.Request, target *contract.DevTaskUpdate) error {
	return handler.DecodeJSON(w, r, target, "title/requirementId/subtasks")
}

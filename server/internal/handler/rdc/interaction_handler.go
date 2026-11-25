// 职责：AI 交互层 9 个 HTTP 操作的语义层：取路径参数、严格解码、调业务层、写审计、统一响应。
// 输入：HTTP 请求与上下文身份 / scope；请求体只接受契约白名单字段。
// 输出：{code,data,message} JSON 外壳；成功一律 200，冲突 409，越界 404，参数非法 400。
// 边界：不写 SQL、不做业务判断（在 service）、不判权限（在路由中间件）；
//
//	审计失败只记日志不回滚，WithoutCancel 保证客户端断开也能写完。
package rdc

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	rdcsvc "dandelion/server/internal/service/rdc"
)

// interactionAuditor 是审计服务的最小投影，便于 handler 单测替身注入。
type interactionAuditor interface {
	Record(ctx context.Context, actor contract.Identity, action, objectType, objectID string,
		detail map[string]any, ip string)
}

// InteractionHandler 依赖交互业务层与审计服务。
type InteractionHandler struct {
	svc   *rdcsvc.InteractionService
	audit interactionAuditor
}

// NewInteractionHandler 构造交互 HTTP 处理器；审计服务由装配层注入。
func NewInteractionHandler(svc *rdcsvc.InteractionService, audit interactionAuditor) *InteractionHandler {
	return &InteractionHandler{svc: svc, audit: audit}
}

// GetDiffReview 读取 Diff 审查；无产物时 data:null，UI 显示真实空态。
func (h *InteractionHandler) GetDiffReview(w http.ResponseWriter, r *http.Request) error {
	taskID, subtaskID, err := taskSubtaskParams(r)
	if err != nil {
		return err
	}
	data, err := h.svc.GetDiffReview(r.Context(), authctx.ScopeFrom(r.Context()), taskID, subtaskID)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// SubmitDiffReview 提交整份三态决策；service 门禁拒绝 pending，成功后写一条证据 + 审计。
func (h *InteractionHandler) SubmitDiffReview(w http.ResponseWriter, r *http.Request) error {
	var in contract.DiffReviewInput
	if err := handler.DecodeJSON(w, r, &in, "decisions"); err != nil {
		return err
	}
	// 契约路径是 /tasks/{id}/subtasks/{subtaskId}/diff-review/submit，没有 reviewId；
	// 必须按关联键定位最新审查，不能从 URL 读不存在的参数（否则恒定 404）。
	taskID, subtaskID, err := taskSubtaskParams(r)
	if err != nil {
		return err
	}
	identity := authctx.From(r.Context())
	data, audit, err := h.svc.SubmitDiffReviewByTask(
		r.Context(), authctx.ScopeFrom(r.Context()), taskID, subtaskID, in, identity)
	h.record(r.Context(), identity, r, audit)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// SetHunkDecision 单 hunk 三态决策；只写决策表，不写审核记录、不写审计。
func (h *InteractionHandler) SetHunkDecision(w http.ResponseWriter, r *http.Request) error {
	var in contract.HunkDecisionInput
	if err := handler.DecodeJSON(w, r, &in, "decision"); err != nil {
		return err
	}
	reviewID, err := uuidParam(r, "reviewId")
	if err != nil {
		return err
	}
	hunkID := chi.URLParam(r, "hunkId")
	if hunkID == "" || len(hunkID) > contract.MaxHunkIDLen {
		return handler.NotFound("hunk 不存在")
	}
	data, _, err := h.svc.SetHunkDecision(r.Context(), authctx.ScopeFrom(r.Context()),
		reviewID, hunkID, in.Decision, authctx.From(r.Context()).UserID)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// GetPlan 读取计划确认；无产物时 data:null。
func (h *InteractionHandler) GetPlan(w http.ResponseWriter, r *http.Request) error {
	taskID, subtaskID, err := taskSubtaskParams(r)
	if err != nil {
		return err
	}
	data, err := h.svc.GetPlan(r.Context(), authctx.ScopeFrom(r.Context()), taskID, subtaskID)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// ConfirmPlan 确认计划门禁；写 plan_confirmed 证据 + 审计，不推进子任务状态。
func (h *InteractionHandler) ConfirmPlan(w http.ResponseWriter, r *http.Request) error {
	var in contract.PlanConfirmInput
	if err := handler.DecodeJSON(w, r, &in, "answers/stepIds"); err != nil {
		return err
	}
	planID, err := uuidParam(r, "planId")
	if err != nil {
		return err
	}
	identity := authctx.From(r.Context())
	data, audit, err := h.svc.ConfirmPlan(
		r.Context(), authctx.ScopeFrom(r.Context()), planID, in, identity)
	h.record(r.Context(), identity, r, audit)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// ListCheckpoints 读取检查点时间线；无产物返回空数组。
func (h *InteractionHandler) ListCheckpoints(w http.ResponseWriter, r *http.Request) error {
	taskID, subtaskID, err := taskSubtaskParams(r)
	if err != nil {
		return err
	}
	data, err := h.svc.ListCheckpoints(r.Context(), authctx.ScopeFrom(r.Context()), taskID, subtaskID)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Rollback 校验令牌与哈希；哈希不匹配返回 409 + rollback_blocked 审计，绝不强制覆盖。
func (h *InteractionHandler) Rollback(w http.ResponseWriter, r *http.Request) error {
	var in contract.RollbackInput
	if err := handler.DecodeJSON(w, r, &in, "rollbackToken/scope/targets/currentHashes"); err != nil {
		return err
	}
	checkpointID, err := uuidParam(r, "checkpointId")
	if err != nil {
		return err
	}
	identity := authctx.From(r.Context())
	data, audit, err := h.svc.Rollback(
		r.Context(), authctx.ScopeFrom(r.Context()), checkpointID, in, identity)
	h.record(r.Context(), identity, r, audit)
	if err != nil {
		var conflict *contract.InteractionConflictError
		if errors.As(err, &conflict) {
			return handler.Conflict(conflict.Error())
		}
		return err
	}
	return handler.OK(w, data)
}

// GetApproval 读取风险 → 介入强度徽标。
func (h *InteractionHandler) GetApproval(w http.ResponseWriter, r *http.Request) error {
	taskID, subtaskID, err := taskSubtaskParams(r)
	if err != nil {
		return err
	}
	data, err := h.svc.GetApproval(r.Context(), authctx.ScopeFrom(r.Context()), taskID, subtaskID)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// DecideApproval 记录审批结论；写 approval_decided 证据 + 审计，不是新权限点或状态边。
func (h *InteractionHandler) DecideApproval(w http.ResponseWriter, r *http.Request) error {
	var in contract.ApprovalDecideInput
	if err := handler.DecodeJSON(w, r, &in, "checkpointId/decision"); err != nil {
		return err
	}
	taskID, subtaskID, err := taskSubtaskParams(r)
	if err != nil {
		return err
	}
	identity := authctx.From(r.Context())
	data, audit, err := h.svc.DecideApproval(
		r.Context(), authctx.ScopeFrom(r.Context()), taskID, subtaskID, in, identity)
	h.record(r.Context(), identity, r, audit)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// taskSubtaskParams 读取并校验 /tasks/{id}/subtasks/{subtaskId} 两个路径参数。
func taskSubtaskParams(r *http.Request) (string, string, error) {
	taskID, err := uuidParam(r, "id")
	if err != nil {
		return "", "", err
	}
	subtaskID, err := uuidParam(r, "subtaskId")
	if err != nil {
		return "", "", err
	}
	return taskID, subtaskID, nil
}

// uuidParam 校验 UUID 形态：拼错的路径按资源不存在处理，不把脏值送到数据库触发 500。
func uuidParam(r *http.Request, name string) (string, error) {
	value := chi.URLParam(r, name)
	if !contract.IsValidUUID(value) {
		return "", handler.NotFound("资源不存在或已被移除")
	}
	return value, nil
}

// record 写审计；空 action 表示这一步不需要审计（单 hunk 决策、各读操作）。
// WithoutCancel 让客户端断开时仍能写完，Record 内部失败只记日志不回滚。
func (h *InteractionHandler) record(
	ctx context.Context, identity contract.Identity, r *http.Request, audit contract.InteractionAudit,
) {
	if audit.Action == "" || h.audit == nil {
		return
	}
	detail := audit.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	h.audit.Record(context.WithoutCancel(ctx), identity, audit.Action,
		audit.ObjectType, audit.ObjectID, detail, r.RemoteAddr)
}

// Package rdc · 人工审核中心的 HTTP 语义层。
// 职责：读取路径/查询参数、严格 DecodeJSON、调用审核业务层、写审核审计、统一响应。
// 输入：HTTP 请求（待审列表 / 详情 / 通过 / 驳回 / 提交确认）与上下文身份、scope。
// 输出：{code,data,message} JSON 外壳，全部成功 200。
// 边界：不写 SQL、不做字段业务校验（在 service）、不判权限（在路由中间件）；
//
//	审计只记日志不回滚，WithoutCancel 保证客户端断开也能写完。
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

// 审核审计 action，与 openapi 描述对应；objectType 复用审核记录语义。
const (
	reviewActionApproved      = "rdc.review.approved"
	reviewActionRejected      = "rdc.review.rejected"
	reviewActionCommitted     = "rdc.review.committed"
	reviewObjectSubtask       = "rdc_review"
)

// ReviewHandler 依赖审核业务层与审计服务。
type ReviewHandler struct {
	svc   *rdcsvc.ReviewService
	audit *auditsvc.Service
}

// NewReviewHandler 构造审核 HTTP 处理器。
func NewReviewHandler(svc *rdcsvc.ReviewService, audit *auditsvc.Service) *ReviewHandler {
	return &ReviewHandler{svc: svc, audit: audit}
}

// List 返回 scope 内待审任务分页列表；page/pageSize/reqKey 来自查询参数。
func (h *ReviewHandler) List(w http.ResponseWriter, r *http.Request) error {
	query := contract.ReviewTaskQuery{
		Page:   handler.ReadPage(r),
		ReqKey: r.URL.Query().Get("reqKey"),
	}
	data, err := h.svc.ListTasks(r.Context(), query, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 返回审核详情（任务 + 两子任务 + 审核历史）；非法 id 与越界统一 404。
func (h *ReviewHandler) Get(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.GetDetail(r.Context(), chi.URLParam(r, "id"), authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Approve 审核通过（reviewing → waiting_commit）。
func (h *ReviewHandler) Approve(w http.ResponseWriter, r *http.Request) error {
	return h.transition(w, r, contract.ReviewActionApprove, reviewActionApproved, "", "")
}

// Reject 审核驳回（reviewing → rejected），原因必填。
func (h *ReviewHandler) Reject(w http.ResponseWriter, r *http.Request) error {
	var in contract.ReviewRejectInput
	if err := handler.DecodeJSON(w, r, &in, "reason"); err != nil {
		return err
	}
	return h.transition(w, r, contract.ReviewActionReject, reviewActionRejected, in.Reason, "")
}

// Commit 人工提交确认（waiting_commit → committed），commit SHA 必填。
func (h *ReviewHandler) Commit(w http.ResponseWriter, r *http.Request) error {
	var in contract.ReviewCommitInput
	if err := handler.DecodeJSON(w, r, &in, "commitSha"); err != nil {
		return err
	}
	return h.transition(w, r, contract.ReviewActionCommit, reviewActionCommitted, "", in.CommitSha)
}

// transition 是三个审核动作的共同处理：调 service 改状态 + 写审核记录，再写审计。
func (h *ReviewHandler) transition(
	w http.ResponseWriter, r *http.Request, action, auditAction, reason, commitSha string,
) error {
	ctx := r.Context()
	identity := authctx.From(ctx)
	taskID := chi.URLParam(r, "id")
	subtaskID := chi.URLParam(r, "subtaskId")
	scope := authctx.ScopeFrom(ctx)

	var (
		subtask contract.DevSubtask
		record  contract.ReviewRecord
		err     error
	)
	switch action {
	case contract.ReviewActionApprove:
		subtask, record, err = h.svc.Approve(ctx, identity, scope, taskID, subtaskID)
	case contract.ReviewActionReject:
		subtask, record, err = h.svc.Reject(ctx, identity, scope, taskID, subtaskID, reason)
	case contract.ReviewActionCommit:
		subtask, record, err = h.svc.Commit(ctx, identity, scope, taskID, subtaskID, commitSha)
	default:
		return handler.BadRequest("未知的审核动作")
	}
	if err != nil {
		return err
	}

	detail := map[string]any{
		"taskId": taskID, "subtaskId": subtaskID, "kind": subtask.Kind,
		"action": action, "recordId": record.ID,
	}
	if reason != "" {
		detail["reason"] = reason
	}
	if commitSha != "" {
		detail["commitSha"] = commitSha
	}
	h.record(ctx, identity, r, auditAction, subtaskID, detail)
	return handler.OK(w, subtask)
}

// record 写审计；WithoutCancel 让客户端断开时仍能写完，Record 内部失败只记日志不回滚。
func (h *ReviewHandler) record(
	ctx context.Context,
	identity contract.Identity,
	r *http.Request,
	action, objectID string,
	detail map[string]any,
) {
	h.audit.Record(
		context.WithoutCancel(ctx), identity, action, reviewObjectSubtask, objectID,
		detail, r.RemoteAddr,
	)
}

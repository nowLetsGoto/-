// Package rdc · 人工审核中心的业务编排。
// 职责：校验审核请求字段、驱动子任务状态流转（复用 DevService 唯一写入口）、追加审核记录。
// 输入：handler 已严格解码的动作与原因 / commit SHA、调用者身份、scope、任务 / 子任务 id。
// 输出：contract.ReviewTaskList / ReviewDetail / DevSubtask；校验失败返回 ValidationError，
//
//	越界 / 不存在返回 NotFoundError。
//
// 边界：不碰 *http.Request / ResponseWriter、不写 SQL、不判权限、不写审计；
//
//	状态写入一律经 DevService.ApplyReviewAction（其底层是 writeStatus 条件更新），
//	本文件不直接改状态，杜绝第二条写入口。
package rdc

import (
	"context"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
)

// ReviewRepository 是审核业务层依赖的仓储契约；由 repository/rdc.ReviewRepository 实现。
type ReviewRepository interface {
	Append(ctx context.Context, taskID, subtaskID, action, reason, commitSha, reviewerUserID string,
		scope contract.Scope) (contract.ReviewRecord, error)
	ListTasks(ctx context.Context, query contract.ReviewTaskQuery, scope contract.Scope) (contract.ReviewTaskList, error)
	FindDetail(ctx context.Context, id string, scope contract.Scope) (contract.ReviewDetail, error)
}

// ReviewService 是人工审核中心的业务入口。
type ReviewService struct {
	reviews *reviewRepoAdapter
	dev     *DevService
}

// reviewRepoAdapter 把接口与具体仓储收在一起，便于纯逻辑测试注入替身。
type reviewRepoAdapter struct {
	repo ReviewRepository
}

// NewReviewService 构造审核业务层；审核仓储与研发服务由装配层注入。
func NewReviewService(repo ReviewRepository, dev *DevService) *ReviewService {
	return &ReviewService{reviews: &reviewRepoAdapter{repo: repo}, dev: dev}
}

// ListTasks 返回 scope 内待审任务分页列表。
func (s *ReviewService) ListTasks(
	ctx context.Context, query contract.ReviewTaskQuery, scope contract.Scope,
) (contract.ReviewTaskList, error) {
	return s.reviews.repo.ListTasks(ctx, query, scope)
}

// GetDetail 返回审核详情；越界任务由仓储转 404。
func (s *ReviewService) GetDetail(
	ctx context.Context, id string, scope contract.Scope,
) (contract.ReviewDetail, error) {
	return s.reviews.repo.FindDetail(ctx, id, scope)
}

// Approve 审核通过：reviewing → waiting_commit。写 approve 审核记录。
func (s *ReviewService) Approve(
	ctx context.Context, caller contract.Identity, scope contract.Scope, taskID, subtaskID string,
) (contract.DevSubtask, contract.ReviewRecord, error) {
	return s.apply(ctx, contract.ReviewActionApprove, caller, scope, taskID, subtaskID, "", "")
}

// Reject 审核驳回：reviewing → rejected。原因必填，写 reject 审核记录。
func (s *ReviewService) Reject(
	ctx context.Context, caller contract.Identity, scope contract.Scope, taskID, subtaskID, reason string,
) (contract.DevSubtask, contract.ReviewRecord, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > contract.MaxReviewReasonLen {
		return contract.DevSubtask{}, contract.ReviewRecord{},
			&contract.ValidationError{Message: "驳回原因必填且不超过 500 字"}
	}
	return s.apply(ctx, contract.ReviewActionReject, caller, scope, taskID, subtaskID, reason, "")
}

// Commit 提交确认：waiting_commit → committed。commit SHA 必填，写 commit 审核记录。
func (s *ReviewService) Commit(
	ctx context.Context, caller contract.Identity, scope contract.Scope, taskID, subtaskID, commitSha string,
) (contract.DevSubtask, contract.ReviewRecord, error) {
	commitSha = strings.TrimSpace(commitSha)
	if commitSha == "" || utf8.RuneCountInString(commitSha) > contract.MaxReviewCommitShaLen {
		return contract.DevSubtask{}, contract.ReviewRecord{},
			&contract.ValidationError{Message: "commit SHA 必填且不超过 64 字"}
	}
	return s.apply(ctx, contract.ReviewActionCommit, caller, scope, taskID, subtaskID, "", commitSha)
}

// apply 是三个审核动作的共同编排：先经 DevService 改状态（含状态机校验），
// 成功后追加审核记录。状态是真相来源，审核记录是留痕。
func (s *ReviewService) apply(
	ctx context.Context, action string, caller contract.Identity, scope contract.Scope,
	taskID, subtaskID, reason, commitSha string,
) (contract.DevSubtask, contract.ReviewRecord, error) {
	subtask, _, err := s.dev.ApplyReviewAction(ctx, action, taskID, subtaskID, scope)
	if err != nil {
		return contract.DevSubtask{}, contract.ReviewRecord{}, err
	}
	record, err := s.reviews.repo.Append(ctx, taskID, subtaskID, action, reason, commitSha,
		caller.UserID, scope)
	if err != nil {
		return contract.DevSubtask{}, contract.ReviewRecord{}, err
	}
	return subtask, record, nil
}

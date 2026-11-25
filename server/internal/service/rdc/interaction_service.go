// 职责：AI 人机交互四步（Diff 审查 / 计划确认 / 检查点回滚 / 分级审批）的业务编排。
// 输入：handler 已严格解码的请求、身份与 scope、注入的仓储与 AgentArtifactProvider。
// 输出：交互对象与交给 handler 落审计的 contract.InteractionAudit；业务拒绝用 ValidationError / 404 / 409。
// 边界：不碰 *http.Request / ResponseWriter、不写 SQL、不写真实文件、不判权限；
//
//	所有写操作只写决策 / 证据 / 门禁 / 检查点状态，绝不写研发子任务状态列，
//	绝不调用 DevService.Advance / Skip / Resubmit 或 ReviewService.apply（唯一状态写入口不被动过）。
package rdc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"dandelion/server/internal/contract"
)

// InteractionRepository 是交互业务层依赖的仓储契约，由 repository/rdc 实现。
type InteractionRepository interface {
	FindLatestReview(ctx context.Context, taskID, subtaskID string, scope contract.Scope) (*contract.DiffReview, error)
	FindReviewByID(ctx context.Context, reviewID string, scope contract.Scope) (*contract.DiffReview, error)
	UpsertDecision(ctx context.Context, reviewID, hunkID, decision, decidedBy string, scope contract.Scope) error
	SaveDecisionsTx(ctx context.Context, reviewID string, decisions []contract.HunkDecision, decidedBy string, scope contract.Scope) error
	FindPlan(ctx context.Context, taskID, subtaskID string, scope contract.Scope) (*contract.PlanConfirmation, error)
	FindPlanByID(ctx context.Context, planID string, scope contract.Scope) (*contract.PlanConfirmation, error)
	ConfirmPlanTx(ctx context.Context, planID string, in contract.PlanConfirmInput, confirmedBy string, scope contract.Scope) (contract.PlanConfirmation, error)
	ListCheckpoints(ctx context.Context, taskID, subtaskID string, scope contract.Scope) ([]contract.Checkpoint, error)
	FindCheckpoint(ctx context.Context, id string, scope contract.Scope) (*contract.Checkpoint, error)
	MarkRollbackTx(ctx context.Context, checkpointID, status, actorID string, scope contract.Scope) (contract.Checkpoint, error)
	AppendEvidence(ctx context.Context, taskID, subtaskID, action, reason, commitSha, reviewerUserID string,
		detail map[string]any, scope contract.Scope) (contract.ReviewRecord, error)
	FindEvidence(ctx context.Context, taskID, subtaskID, action string, scope contract.Scope) ([]contract.ReviewRecord, error)
}

// InteractionService 是交互层的唯一业务入口。
type InteractionService struct {
	repo     InteractionRepository
	provider AgentArtifactProvider
}

// NewInteractionService 构造交互业务层；生产注入 unavailableProvider，Group B 在此一处替换。
func NewInteractionService(repo InteractionRepository, provider AgentArtifactProvider) *InteractionService {
	return &InteractionService{repo: repo, provider: provider}
}

// GetDiffReview 读取 diff 审查；无真实产物时返回 nil 让 UI 显示真实空态，绝不造假 diff。
func (s *InteractionService) GetDiffReview(
	ctx context.Context, scope contract.Scope, taskID, subtaskID string,
) (*contract.DiffReview, error) {
	review, err := s.repo.FindLatestReview(ctx, taskID, subtaskID, scope)
	if err != nil || review != nil {
		return review, err
	}
	return nil, s.artifactUnavailable(ctx, taskID, subtaskID, scope)
}

// artifactUnavailable 探测执行器是否就绪：未就绪是 Group A 的常态，折叠成空态而不是错误。
func (s *InteractionService) artifactUnavailable(
	ctx context.Context, taskID, subtaskID string, scope contract.Scope,
) error {
	_, err := s.provider.LatestDiffReview(ctx, taskID, subtaskID, scope)
	if errors.Is(err, ErrArtifactUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	// 执行器真产出后必须先由 Group B 落库，交互层不构造未落库的临时审查对象。
	return &contract.ValidationError{Message: "Agent 产物尚未落库，请等待执行器完成写入"}
}

// SubmitDiffReview 按 reviewId 校验三态门禁后写决策与证据；保留给按 id 定位的内部调用。
func (s *InteractionService) SubmitDiffReview(
	ctx context.Context, scope contract.Scope, reviewID string,
	in contract.DiffReviewInput, actor contract.Identity,
) (*contract.DiffReview, contract.InteractionAudit, error) {
	review, err := s.findReviewByID(ctx, scope, reviewID)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	return s.submit(ctx, scope, review, in, actor)
}

// SubmitDiffReviewByTask 是契约路由（task/subtask）实际使用的提交入口：
// 先按关联键取该子任务最新审查，再复用同一套门禁、决策与证据逻辑。
// 越界 / 无产物一律按「审查不存在」404，不泄漏其他公司数据。
func (s *InteractionService) SubmitDiffReviewByTask(
	ctx context.Context, scope contract.Scope, taskID, subtaskID string,
	in contract.DiffReviewInput, actor contract.Identity,
) (*contract.DiffReview, contract.InteractionAudit, error) {
	review, err := s.repo.FindLatestReview(ctx, taskID, subtaskID, scope)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	if review == nil {
		return nil, contract.InteractionAudit{}, contract.NotFound("Diff 审查")
	}
	return s.submit(ctx, scope, review, in, actor)
}

// submit 是两种定位方式共用的唯一提交编排：三态门禁 → 决策事务 → 一条 diff_submitted 证据与审计。
func (s *InteractionService) submit(
	ctx context.Context, scope contract.Scope, review *contract.DiffReview,
	in contract.DiffReviewInput, actor contract.Identity,
) (*contract.DiffReview, contract.InteractionAudit, error) {
	reviewID := review.ID
	decisions, err := checkGate(review, in.Decisions)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	if err := s.repo.SaveDecisionsTx(ctx, reviewID, decisions, actor.UserID, scope); err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	detail := map[string]any{
		"taskId": review.TaskID, "subtaskId": review.SubtaskID, "reviewId": review.ID,
		"accepted": countDecisions(decisions, contract.DecisionAccepted),
		"rejected": countDecisions(decisions, contract.DecisionRejected),
	}
	if _, err := s.repo.AppendEvidence(ctx, review.TaskID, review.SubtaskID,
		contract.EvidenceDiffSubmitted, "", "", actor.UserID, detail, scope); err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	review.SubmitStatus = contract.SubmitSubmitted
	review.Decisions = decisions
	return review, contract.InteractionAudit{
		Action: "rdc.interaction.diff_submitted", ObjectType: "rdc_diff_review",
		ObjectID: review.ID, Detail: detail,
	}, nil
}

// findReviewByID 通过 repo 按 id 精确读取审查；不存在即 404（越界同样按不存在处理）。
func (s *InteractionService) findReviewByID(
	ctx context.Context, scope contract.Scope, reviewID string,
) (*contract.DiffReview, error) {
	review, err := s.repo.FindReviewByID(ctx, reviewID, scope)
	if err != nil {
		return nil, err
	}
	if review == nil {
		return nil, contract.NotFound("Diff 审查")
	}
	return review, nil
}

// SetHunkDecision 只 UPSERT 单 hunk 决策，不写审核记录，避免点击即留痕刷屏。
func (s *InteractionService) SetHunkDecision(
	ctx context.Context, scope contract.Scope, reviewID, hunkID, decision, actorID string,
) (*contract.DiffReview, contract.InteractionAudit, error) {
	if !contract.IsValidReviewDecision(decision) {
		return nil, contract.InteractionAudit{},
			&contract.ValidationError{Message: "hunk 决策只能是 pending / accepted / rejected"}
	}
	review, err := s.findReviewByID(ctx, scope, reviewID)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	if err := s.repo.UpsertDecision(ctx, reviewID, hunkID, decision, actorID, scope); err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	found := false
	for i := range review.Decisions {
		if review.Decisions[i].HunkID == hunkID {
			review.Decisions[i].Decision = decision
			review.Decisions[i].DecidedBy = actorID
			found = true
		}
	}
	if !found {
		review.Decisions = append(review.Decisions, contract.HunkDecision{
			ReviewID: reviewID, HunkID: hunkID, Decision: decision, DecidedBy: actorID,
		})
	}
	// 单 hunk 决策不写证据、不写审计：审计只在提交时写一条。
	return review, contract.InteractionAudit{}, nil
}

// GetPlan 读取计划确认；无真实产物时返回 nil（真实空态）。
func (s *InteractionService) GetPlan(
	ctx context.Context, scope contract.Scope, taskID, subtaskID string,
) (*contract.PlanConfirmation, error) {
	plan, err := s.repo.FindPlan(ctx, taskID, subtaskID, scope)
	if err != nil || plan != nil {
		return plan, err
	}
	_, _, probeErr := s.provider.LatestPlan(ctx, taskID, subtaskID, scope)
	if errors.Is(probeErr, ErrArtifactUnavailable) {
		return nil, nil
	}
	if probeErr != nil {
		return nil, probeErr
	}
	return nil, &contract.ValidationError{Message: "Agent 计划尚未落库，请等待执行器完成写入"}
}

// ConfirmPlan 校验门禁后确认计划；只表示意图对齐完成，不触发生成、不推进状态。
func (s *InteractionService) ConfirmPlan(
	ctx context.Context, scope contract.Scope, planID string,
	in contract.PlanConfirmInput, actor contract.Identity,
) (contract.PlanConfirmation, contract.InteractionAudit, error) {
	plan, err := s.repo.FindPlanByID(ctx, planID, scope)
	if err != nil {
		return contract.PlanConfirmation{}, contract.InteractionAudit{}, err
	}
	if plan == nil {
		return contract.PlanConfirmation{}, contract.InteractionAudit{}, contract.NotFound("计划确认")
	}
	if err := checkPlanGate(plan, in); err != nil {
		return contract.PlanConfirmation{}, contract.InteractionAudit{}, err
	}
	result, err := s.repo.ConfirmPlanTx(ctx, planID, in, actor.UserID, scope)
	if err != nil {
		return contract.PlanConfirmation{}, contract.InteractionAudit{}, err
	}
	detail := map[string]any{
		"taskId": result.TaskID, "subtaskId": result.SubtaskID,
		"planId": result.ID, "confirmedSteps": len(in.StepIDs),
	}
	return result, contract.InteractionAudit{
		Action: "rdc.interaction.plan_confirmed", ObjectType: "rdc_plan_confirmation",
		ObjectID: result.ID, Detail: detail,
	}, nil
}

// ListCheckpoints 返回检查点时间线；无产物时返回空数组而非假快照。
func (s *InteractionService) ListCheckpoints(
	ctx context.Context, scope contract.Scope, taskID, subtaskID string,
) ([]contract.Checkpoint, error) {
	items, err := s.repo.ListCheckpoints(ctx, taskID, subtaskID, scope)
	if err != nil || len(items) > 0 {
		return items, err
	}
	_, _, probeErr := s.provider.Checkpoints(ctx, taskID, subtaskID, scope)
	if errors.Is(probeErr, ErrArtifactUnavailable) {
		return []contract.Checkpoint{}, nil
	}
	if probeErr != nil {
		return nil, probeErr
	}
	return nil, &contract.ValidationError{Message: "Agent 检查点尚未落库，请等待执行器完成写入"}
}

// Rollback 校验令牌与逐文件哈希后只写证据 + 检查点状态，不回退子任务状态、不写真实文件。
func (s *InteractionService) Rollback(
	ctx context.Context, scope contract.Scope, checkpointID string, in contract.RollbackInput,
	actor contract.Identity,
) (*contract.Checkpoint, contract.InteractionAudit, error) {
	checkpoint, err := s.repo.FindCheckpoint(ctx, checkpointID, scope)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	if checkpoint == nil {
		return nil, contract.InteractionAudit{}, contract.NotFound("检查点")
	}
	if in.RollbackToken == "" || in.RollbackToken != checkpoint.RollbackToken {
		return nil, contract.InteractionAudit{}, &contract.ValidationError{Message: "回滚令牌无效"}
	}
	if checkpoint.Status != contract.CheckpointActive {
		return nil, contract.InteractionAudit{}, &contract.ValidationError{Message: "该检查点已回滚，不可重复回滚"}
	}
	if !contract.IsValidRollbackScope(in.Scope) {
		return nil, contract.InteractionAudit{}, &contract.ValidationError{Message: "回滚粒度只能是 hunk / file / checkpoint"}
	}

	// 先校验目标命中真实快照，杜绝「目标不在快照里 → 零文件校验 → 直接 200」的绕过。
	wanted, err := rollbackTargets(checkpoint, in)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	if err := s.validateHunkTargets(ctx, scope, checkpoint, in); err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	conflicts := verifyHashes(checkpoint, wanted, in.CurrentHashes)
	auditDetail := map[string]any{
		"taskId": checkpoint.TaskID, "subtaskId": checkpoint.SubtaskID,
		"checkpointId": checkpoint.ID,
	}
	if len(conflicts) > 0 {
		auditDetail["hashMismatch"] = conflicts
		blocked := contract.InteractionAudit{
			Action: "rdc.interaction.rollback_blocked", ObjectType: "rdc_checkpoint",
			ObjectID: checkpoint.ID, Detail: auditDetail,
		}
		return nil, blocked, &contract.InteractionConflictError{
			Message: "文件内容已变化，禁止回滚；请人工核对冲突文件（本系统不提供强制覆盖）",
			Files:   conflicts,
		}
	}

	status := contract.CheckpointPartial
	if in.Scope == contract.RollbackCheckpoint {
		status = contract.CheckpointRolledBack
	}
	result, err := s.repo.MarkRollbackTx(ctx, checkpointID, status, actor.UserID, scope)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	// Group B 才会调用 provider.ApplyRollback 写回真实文件；Group A 的 unavailableProvider
	// 必然失败，因此这里刻意不调用它——本版只做证据层。
	detail := map[string]any{
		"taskId": result.TaskID, "subtaskId": result.SubtaskID,
		"checkpointId": result.ID, "scope": in.Scope, "files": len(wanted),
	}
	return &result, contract.InteractionAudit{
		Action: "rdc.interaction.checkpoint_rolled_back", ObjectType: "rdc_checkpoint",
		ObjectID: result.ID, Detail: detail,
	}, nil
}

// GetApproval 由全部 hunk 风险取最大值算介入强度；无产物时返回 nil（真实空态）。
func (s *InteractionService) GetApproval(
	ctx context.Context, scope contract.Scope, taskID, subtaskID string,
) (*contract.ApprovalView, error) {
	review, err := s.GetDiffReview(ctx, scope, taskID, subtaskID)
	if err != nil || review == nil {
		return nil, err
	}
	return s.approvalOf(ctx, scope, review)
}

// DecideApproval 写 approval_decided 证据；是介入强度证据，不是新权限点或状态边。
func (s *InteractionService) DecideApproval(
	ctx context.Context, scope contract.Scope, taskID, subtaskID string,
	in contract.ApprovalDecideInput, actor contract.Identity,
) (*contract.ApprovalView, contract.InteractionAudit, error) {
	if !contract.IsValidApprovalDecision(in.Decision) {
		return nil, contract.InteractionAudit{},
			&contract.ValidationError{Message: "审批决定只能是 approve / reject"}
	}
	checkpoint, err := s.repo.FindCheckpoint(ctx, in.CheckpointID, scope)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	if checkpoint == nil || checkpoint.TaskID != taskID || checkpoint.SubtaskID != subtaskID {
		return nil, contract.InteractionAudit{}, contract.NotFound("检查点")
	}
	review, err := s.repo.FindLatestReview(ctx, taskID, subtaskID, scope)
	if err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	if review == nil {
		return nil, contract.InteractionAudit{}, contract.NotFound("Diff 审查")
	}
	detail := map[string]any{
		"taskId": taskID, "subtaskId": subtaskID, "checkpointId": in.CheckpointID,
		"decision": in.Decision, "overallRisk": overallRiskOf(review),
	}
	detail["intervention"] = contract.InterventionOf(detail["overallRisk"].(string))
	if _, err := s.repo.AppendEvidence(ctx, taskID, subtaskID,
		contract.EvidenceApprovalDecided, "", "", actor.UserID, detail, scope); err != nil {
		return nil, contract.InteractionAudit{}, err
	}
	return &contract.ApprovalView{
			OverallRisk: detail["overallRisk"].(string), Intervention: detail["intervention"].(string),
			Decided: true, Decision: in.Decision,
		}, contract.InteractionAudit{
			Action: "rdc.interaction.approval_decided", ObjectType: "rdc_checkpoint",
			ObjectID: in.CheckpointID, Detail: detail,
		}, nil
}

// approvalOf 计算整体风险、介入强度与是否已决定。
func (s *InteractionService) approvalOf(
	ctx context.Context, scope contract.Scope, review *contract.DiffReview,
) (*contract.ApprovalView, error) {
	risk := overallRiskOf(review)
	view := &contract.ApprovalView{OverallRisk: risk, Intervention: contract.InterventionOf(risk)}
	records, err := s.repo.FindEvidence(ctx, review.TaskID, review.SubtaskID,
		contract.EvidenceApprovalDecided, scope)
	if err != nil {
		return nil, err
	}
	if len(records) > 0 {
		view.Decided = true
		if decision, ok := records[0].Detail["decision"].(string); ok {
			view.Decision = decision
		}
	}
	return view, nil
}

// overallRiskOf 汇总所有 hunk 的 riskLabel 取最大值。
func overallRiskOf(review *contract.DiffReview) string {
	operations := make([]string, 0)
	for _, file := range review.Files {
		for _, hunk := range file.Hunks {
			operations = append(operations, hunk.RiskLabel)
		}
	}
	return contract.OverallRiskLevel(operations)
}

// checkGate 要求每个 hunk 都已表态且三态合法；返回按 hunkId 归一后的决策列表。
func checkGate(
	review *contract.DiffReview, submitted []contract.HunkDecision,
) ([]contract.HunkDecision, error) {
	byID := make(map[string]contract.HunkDecision, len(submitted))
	for _, decision := range submitted {
		if !contract.IsValidReviewDecision(decision.Decision) {
			return nil, &contract.ValidationError{Message: "hunk 决策值非法"}
		}
		byID[decision.HunkID] = decision
	}
	result := make([]contract.HunkDecision, 0)
	pending := 0
	for _, file := range review.Files {
		for _, hunk := range file.Hunks {
			decision, exists := byID[hunk.HunkID]
			if !exists || decision.Decision == contract.DecisionPending {
				pending++
				continue
			}
			decision.ReviewID = review.ID
			result = append(result, decision)
		}
	}
	if pending > 0 {
		return nil, &contract.ValidationError{Message: fmt.Sprintf("还有 %d 个 hunk 未处理，请全部接受或拒绝后再提交", pending)}
	}
	for hunkID := range byID {
		known := false
		for _, file := range review.Files {
			for _, hunk := range file.Hunks {
				if hunk.HunkID == hunkID {
					known = true
				}
			}
		}
		if !known {
			return nil, &contract.ValidationError{Message: "提交中包含不属于本次审查的 hunk"}
		}
	}
	return result, nil
}

// countDecisions 统计某种三态决策的数量，供证据 detail 使用。
func countDecisions(decisions []contract.HunkDecision, decision string) int {
	total := 0
	for _, item := range decisions {
		if item.Decision == decision {
			total++
		}
	}
	return total
}

// checkPlanGate 要求所有必答题已答、所有计划任务已确认，否则列出缺项。
func checkPlanGate(plan *contract.PlanConfirmation, in contract.PlanConfirmInput) error {
	answers := make(map[string]bool, len(in.Answers))
	for _, question := range in.Answers {
		answered := question.Answer != nil
		if text, ok := question.Answer.(string); ok {
			answered = strings.TrimSpace(text) != ""
		}
		if values, ok := question.Answer.([]any); ok {
			answered = len(values) > 0
		}
		answers[question.QuestionID] = answered
	}
	missing := make([]string, 0)
	for _, question := range plan.Questionnaire {
		if question.Required && !answers[question.QuestionID] && isAnswerEmpty(question.Answer) {
			missing = append(missing, question.QuestionID)
		}
	}
	steps := make(map[string]bool, len(in.StepIDs))
	for _, stepID := range in.StepIDs {
		steps[stepID] = true
	}
	for _, task := range plan.PlanTasks {
		if !task.Confirmed && !steps[task.StepID] {
			missing = append(missing, task.StepID)
		}
	}
	if len(missing) > 0 {
		return &contract.ValidationError{Message: "计划门禁未满足，缺项：" + strings.Join(missing, "、")}
	}
	return nil
}

// isAnswerEmpty 判断仓储里已有的答案是否空值（存量已答计划不应被重复要求）。
func isAnswerEmpty(answer any) bool {
	if answer == nil {
		return true
	}
	if text, ok := answer.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	if values, ok := answer.([]any); ok {
		return len(values) == 0
	}
	return false
}

// rollbackTargets 计算需要校验哈希的真实快照文件集合。
// 任何不属于 checkpoint.Files 的目标都会被拒绝（400），空集合也一律拒绝，
// 避免攻击者用伪造 filePath 跳过全部哈希校验后直接写回滚证据。
func rollbackTargets(
	checkpoint *contract.Checkpoint, in contract.RollbackInput,
) (map[string]bool, error) {
	snapshots := make(map[string]bool, len(checkpoint.Files))
	for _, file := range checkpoint.Files {
		snapshots[file.FilePath] = true
	}
	for _, target := range in.Targets {
		if target.FilePath == "" || !snapshots[target.FilePath] {
			return nil, &contract.ValidationError{Message: "回滚目标文件不属于该检查点快照"}
		}
	}
	if in.Scope == contract.RollbackCheckpoint {
		if len(checkpoint.Files) == 0 {
			return nil, &contract.ValidationError{Message: "检查点没有可回滚的文件快照"}
		}
		result := make(map[string]bool, len(snapshots))
		for filePath := range snapshots {
			result[filePath] = true
		}
		return result, nil
	}
	if len(in.Targets) == 0 {
		return nil, &contract.ValidationError{Message: "按文件 / hunk 回滚必须给出目标文件"}
	}
	result := make(map[string]bool, len(in.Targets))
	for _, target := range in.Targets {
		result[target.FilePath] = true
	}
	return result, nil
}

// validateHunkTargets 按 hunk 粒度回滚时校验 hunkId 必须真实属于该检查点的目标文件。
// 检查点快照本身不带 hunk 列表，因此只接受「关联的 diff 审查存在且 checkpointId 对齐」的情形；
// 无法建立映射时失败关闭，绝不凭请求里的 hunkId 猜文件。
func (s *InteractionService) validateHunkTargets(
	ctx context.Context, scope contract.Scope, checkpoint *contract.Checkpoint, in contract.RollbackInput,
) error {
	if in.Scope != contract.RollbackHunk {
		return nil
	}
	review, err := s.repo.FindLatestReview(ctx, checkpoint.TaskID, checkpoint.SubtaskID, contract.Scope{
		All: true,
	})
	if err != nil {
		return err
	}
	if review == nil || review.CheckpointID != checkpoint.ID {
		return &contract.ValidationError{Message: "未找到该检查点对应的 hunk 映射，无法按 hunk 回滚"}
	}
	for _, target := range in.Targets {
		if target.HunkID == "" {
			return &contract.ValidationError{Message: "按 hunk 回滚必须提供 hunkId"}
		}
		matched := false
		for _, file := range review.Files {
			if file.FilePath != target.FilePath {
				continue
			}
			for _, hunk := range file.Hunks {
				if hunk.HunkID == target.HunkID {
					matched = true
				}
			}
		}
		if !matched {
			return &contract.ValidationError{Message: "hunkId 不属于该回滚目标文件"}
		}
	}
	return nil
}

// verifyHashes 逐目标文件比对当前 hash 与 afterHash，返回冲突文件清单。
// 调用方必须先经 rollbackTargets 校验集合非空且全部命中真实快照。
func verifyHashes(
	checkpoint *contract.Checkpoint, wanted map[string]bool, currentHashes map[string]string,
) []string {
	conflicts := make([]string, 0)
	for _, file := range checkpoint.Files {
		if !wanted[file.FilePath] {
			continue
		}
		current, exists := currentHashes[file.FilePath]
		if !exists || current == "" || current != file.AfterHash {
			conflicts = append(conflicts, file.FilePath)
		}
	}
	return conflicts
}

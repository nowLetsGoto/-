// 职责：AI 交互层计划门禁与检查点回滚证据的持久化，含三个同事务写路径。
// 输入：service 已校验的 planId / checkpointId / 合并后的答案与步骤 / 回滚状态 / scope。
// 输出：contract.PlanConfirmation / Checkpoint；越界、令牌无效、非 active 一律 404 或 409。
// 边界：只写 gate_status / 检查点 status 与审核证据，绝不写研发子任务状态列；
//
//	计划确认不触发生成，回滚不写真实文件；所有读查询先 ScopeFilter 再叠条件。
package rdc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// FindPlan 返回 scope 内子任务的计划确认对象；不存在返回 nil（真实空态）。
func (r *InteractionRepository) FindPlan(
	ctx context.Context, taskID, subtaskID string, scope contract.Scope,
) (*contract.PlanConfirmation, error) {
	filter := repository.ScopeFilter(0, "t", scope).
		Add(`p.task_id::text = ?`, taskID).
		Add(`p.subtask_id::text = ?`, subtaskID)
	statement := `SELECT p.id::text, p.task_id::text, p.subtask_id::text, p.questionnaire::text, ` +
		`p.plan_tasks::text, p.gate_status, p.confirmed_by, coalesce(p.confirmed_at::text, '') ` +
		`FROM ` + r.planTable + ` p JOIN ` + r.taskTable + ` t ON t.id = p.task_id ` +
		`WHERE ` + filter.Cond() + ` ORDER BY p.created_at DESC, p.id DESC LIMIT 1`
	var plan contract.PlanConfirmation
	var questionnaireJSON, tasksJSON string
	err := r.db.QueryRowContext(ctx, statement, filter.Args()...).Scan(
		&plan.ID, &plan.TaskID, &plan.SubtaskID, &questionnaireJSON, &tasksJSON,
		&plan.GateStatus, &plan.ConfirmedBy, &plan.ConfirmedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("查询计划确认失败: %w", err)
	}
	if err := json.Unmarshal([]byte(questionnaireJSON), &plan.Questionnaire); err != nil {
		return nil, fmt.Errorf("解析问卷失败: %w", err)
	}
	if err := json.Unmarshal([]byte(tasksJSON), &plan.PlanTasks); err != nil {
		return nil, fmt.Errorf("解析计划任务失败: %w", err)
	}
	if plan.Questionnaire == nil {
		plan.Questionnaire = []contract.Question{}
	}
	if plan.PlanTasks == nil {
		plan.PlanTasks = []contract.PlanTask{}
	}
	return &plan, nil
}

// FindPlanByID 按计划 id 精确读取；越界 / 不存在返回 nil，供写路径做 404 判定。
func (r *InteractionRepository) FindPlanByID(
	ctx context.Context, planID string, scope contract.Scope,
) (*contract.PlanConfirmation, error) {
	filter := repository.ScopeFilter(0, "t", scope).Add(`p.id::text = ?`, planID)
	statement := `SELECT p.id::text, p.task_id::text, p.subtask_id::text, p.questionnaire::text, ` +
		`p.plan_tasks::text, p.gate_status, p.confirmed_by, coalesce(p.confirmed_at::text, '') ` +
		`FROM ` + r.planTable + ` p JOIN ` + r.taskTable + ` t ON t.id = p.task_id WHERE ` + filter.Cond()
	var plan contract.PlanConfirmation
	var questionnaireJSON, tasksJSON string
	err := r.db.QueryRowContext(ctx, statement, filter.Args()...).Scan(
		&plan.ID, &plan.TaskID, &plan.SubtaskID, &questionnaireJSON, &tasksJSON,
		&plan.GateStatus, &plan.ConfirmedBy, &plan.ConfirmedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("按 id 查询计划确认失败: %w", err)
	}
	if err := json.Unmarshal([]byte(questionnaireJSON), &plan.Questionnaire); err != nil {
		return nil, fmt.Errorf("解析问卷失败: %w", err)
	}
	if err := json.Unmarshal([]byte(tasksJSON), &plan.PlanTasks); err != nil {
		return nil, fmt.Errorf("解析计划任务失败: %w", err)
	}
	if plan.Questionnaire == nil {
		plan.Questionnaire = []contract.Question{}
	}
	if plan.PlanTasks == nil {
		plan.PlanTasks = []contract.PlanTask{}
	}
	return &plan, nil
}

// findPlanOn 在事务内读取并校验 plan 可见性，返回可合并的原始计划。
func (r *InteractionRepository) findPlanOn(
	ctx context.Context, q queryer, planID string, scope contract.Scope,
) (contract.PlanConfirmation, error) {
	filter := repository.ScopeFilter(0, "t", scope).Add(`p.id::text = ?`, planID)
	statement := `SELECT p.id::text, p.task_id::text, p.subtask_id::text, p.questionnaire::text, ` +
		`p.plan_tasks::text, p.gate_status, p.confirmed_by FROM ` + r.planTable + ` p ` +
		`JOIN ` + r.taskTable + ` t ON t.id = p.task_id WHERE ` + filter.Cond()
	var plan contract.PlanConfirmation
	var questionnaireJSON, tasksJSON string
	err := q.QueryRowContext(ctx, statement, filter.Args()...).Scan(
		&plan.ID, &plan.TaskID, &plan.SubtaskID, &questionnaireJSON, &tasksJSON,
		&plan.GateStatus, &plan.ConfirmedBy,
	)
	if err != nil {
		if isNoRows(err) {
			return contract.PlanConfirmation{}, contract.NotFound("计划确认")
		}
		return contract.PlanConfirmation{}, fmt.Errorf("校验计划确认可见性失败: %w", err)
	}
	if err := json.Unmarshal([]byte(questionnaireJSON), &plan.Questionnaire); err != nil {
		return contract.PlanConfirmation{}, fmt.Errorf("解析问卷失败: %w", err)
	}
	if err := json.Unmarshal([]byte(tasksJSON), &plan.PlanTasks); err != nil {
		return contract.PlanConfirmation{}, fmt.Errorf("解析计划任务失败: %w", err)
	}
	return plan, nil
}

// ConfirmPlanTx 同事务写：合并答案与步骤 → gate_status=confirmed → 追加 plan_confirmed 证据。
// 门禁是否满足由 service.checkPlanGate 判定，仓储只负责忠实持久化合并后的结果。
func (r *InteractionRepository) ConfirmPlanTx(
	ctx context.Context, planID string, in contract.PlanConfirmInput,
	confirmedBy string, scope contract.Scope,
) (contract.PlanConfirmation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.PlanConfirmation{}, fmt.Errorf("开启计划确认事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	plan, err := r.findPlanOn(ctx, tx, planID, scope)
	if err != nil {
		return contract.PlanConfirmation{}, err
	}

	answers := make(map[string]any, len(in.Answers))
	for _, question := range in.Answers {
		answers[question.QuestionID] = question.Answer
	}
	for i := range plan.Questionnaire {
		if answer, exists := answers[plan.Questionnaire[i].QuestionID]; exists {
			plan.Questionnaire[i].Answer = answer
		}
	}
	steps := make(map[string]bool, len(in.StepIDs))
	for _, stepID := range in.StepIDs {
		steps[stepID] = true
	}
	for i := range plan.PlanTasks {
		if steps[plan.PlanTasks[i].StepID] {
			plan.PlanTasks[i].Confirmed = true
		}
	}
	questionnaireJSON, err := json.Marshal(plan.Questionnaire)
	if err != nil {
		return contract.PlanConfirmation{}, fmt.Errorf("序列化问卷失败: %w", err)
	}
	tasksJSON, err := json.Marshal(plan.PlanTasks)
	if err != nil {
		return contract.PlanConfirmation{}, fmt.Errorf("序列化计划任务失败: %w", err)
	}
	statement := `UPDATE ` + r.planTable + ` SET questionnaire = $1::jsonb, plan_tasks = $2::jsonb, ` +
		`gate_status = 'confirmed', confirmed_by = $3, confirmed_at = now(), updated_at = now() ` +
		`WHERE id::text = $4`
	if _, err := tx.ExecContext(ctx, statement, string(questionnaireJSON), string(tasksJSON), confirmedBy, planID); err != nil {
		return contract.PlanConfirmation{}, fmt.Errorf("更新计划门禁失败: %w", err)
	}
	if _, err := appendEvidence(ctx, tx, r.recordTable, r.taskTable, plan.TaskID, plan.SubtaskID,
		contract.EvidencePlanConfirmed, "", "", confirmedBy, map[string]any{
			"taskId": plan.TaskID, "subtaskId": plan.SubtaskID, "planId": plan.ID,
			"confirmedSteps": len(in.StepIDs),
		}, scope); err != nil {
		return contract.PlanConfirmation{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.PlanConfirmation{}, fmt.Errorf("提交计划确认事务失败: %w", err)
	}
	plan.GateStatus = contract.GateConfirmed
	plan.ConfirmedBy = confirmedBy
	return plan, nil
}

// ListCheckpoints 返回子任务检查点时间线（时间倒序）；无数据时返回空数组。
func (r *InteractionRepository) ListCheckpoints(
	ctx context.Context, taskID, subtaskID string, scope contract.Scope,
) ([]contract.Checkpoint, error) {
	filter := repository.ScopeFilter(0, "t", scope).
		Add(`c.task_id::text = ?`, taskID).
		Add(`c.subtask_id::text = ?`, subtaskID)
	statement := `SELECT c.id::text, c.task_id::text, c.subtask_id::text, c.ai_action, c.files::text, ` +
		`c.rollback_token, c.status, c.created_by, c.created_at FROM ` + r.checkpointTable + ` c ` +
		`JOIN ` + r.taskTable + ` t ON t.id = c.task_id WHERE ` + filter.Cond() +
		` ORDER BY c.created_at DESC, c.id DESC`
	rows, err := r.db.QueryContext(ctx, statement, filter.Args()...)
	if err != nil {
		return nil, fmt.Errorf("查询检查点失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]contract.Checkpoint, 0)
	for rows.Next() {
		item, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历检查点失败: %w", err)
	}
	return result, nil
}

// FindCheckpoint 按 id 返回 scope 内检查点；令牌与状态由 service 校验。
func (r *InteractionRepository) FindCheckpoint(
	ctx context.Context, id string, scope contract.Scope,
) (*contract.Checkpoint, error) {
	filter := repository.ScopeFilter(0, "t", scope).Add(`c.id::text = ?`, id)
	statement := `SELECT c.id::text, c.task_id::text, c.subtask_id::text, c.ai_action, c.files::text, ` +
		`c.rollback_token, c.status, c.created_by, c.created_at FROM ` + r.checkpointTable + ` c ` +
		`JOIN ` + r.taskTable + ` t ON t.id = c.task_id WHERE ` + filter.Cond()
	item, err := scanCheckpoint(r.db.QueryRowContext(ctx, statement, filter.Args()...))
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// rowScanner 是 *sql.Row 与 *sql.Rows 的公共扫描能力，避免列表与单条各写一份映射。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanCheckpoint 统一检查点的行映射，列表与单条查询共用。
func scanCheckpoint(row rowScanner) (contract.Checkpoint, error) {
	var item contract.Checkpoint
	var filesJSON string
	var createdAt time.Time
	if err := row.Scan(
		&item.ID, &item.TaskID, &item.SubtaskID, &item.AIAction, &filesJSON,
		&item.RollbackToken, &item.Status, &item.CreatedBy, &createdAt,
	); err != nil {
		if isNoRows(err) {
			return contract.Checkpoint{}, err
		}
		return contract.Checkpoint{}, fmt.Errorf("映射检查点失败: %w", err)
	}
	if err := json.Unmarshal([]byte(filesJSON), &item.Files); err != nil {
		return contract.Checkpoint{}, fmt.Errorf("解析文件快照失败: %w", err)
	}
	if item.Files == nil {
		item.Files = []contract.FileSnapshot{}
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return item, nil
}

// MarkRollbackTx 同事务写：更新检查点状态 + 追加 checkpoint_rolled_back 证据。
// 只改检查点状态，绝不回退子任务状态；不碰真实文件（写文件属 Group B）。
func (r *InteractionRepository) MarkRollbackTx(
	ctx context.Context, checkpointID, status, actorID string, scope contract.Scope,
) (contract.Checkpoint, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Checkpoint{}, fmt.Errorf("开启回滚事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	filter := repository.ScopeFilter(0, "t", scope).Add(`c.id::text = ?`, checkpointID)
	statement := `SELECT c.id::text, c.task_id::text, c.subtask_id::text, c.ai_action, c.files::text, ` +
		`c.rollback_token, c.status, c.created_by, c.created_at FROM ` + r.checkpointTable + ` c ` +
		`JOIN ` + r.taskTable + ` t ON t.id = c.task_id WHERE ` + filter.Cond()
	checkpoint, err := scanCheckpoint(tx.QueryRowContext(ctx, statement, filter.Args()...))
	if err != nil {
		if isNoRows(err) {
			return contract.Checkpoint{}, contract.NotFound("检查点")
		}
		return contract.Checkpoint{}, err
	}
	// 条件更新是并发下的权威守卫：service 的 check-then-act 只能挡顺序请求，
	// 两个事务同时读到 active 时，只有第一个 UPDATE 能命中；后者 0 行直接判冲突并回滚事务，
	// 不追加任何证据，保证同一 rollback_token 只能成功一次。
	var updatedID string
	err = tx.QueryRowContext(ctx, `UPDATE `+r.checkpointTable+
		` SET status = $1 WHERE id::text = $2 AND status = 'active' RETURNING id::text`,
		status, checkpointID).Scan(&updatedID)
	if err != nil {
		if isNoRows(err) {
			return contract.Checkpoint{}, &contract.InteractionConflictError{
				Message: "检查点已被其他回滚请求占用或已回滚，禁止重复回滚",
			}
		}
		return contract.Checkpoint{}, fmt.Errorf("更新检查点状态失败: %w", err)
	}
	if _, err := appendEvidence(ctx, tx, r.recordTable, r.taskTable, checkpoint.TaskID,
		checkpoint.SubtaskID, contract.EvidenceCheckpointRollback, "", "", actorID, map[string]any{
			"taskId": checkpoint.TaskID, "subtaskId": checkpoint.SubtaskID,
			"checkpointId": checkpoint.ID, "status": status,
		}, scope); err != nil {
		return contract.Checkpoint{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Checkpoint{}, fmt.Errorf("提交回滚事务失败: %w", err)
	}
	checkpoint.Status = status
	return checkpoint, nil
}

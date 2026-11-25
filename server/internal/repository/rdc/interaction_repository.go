// 职责：AI 交互层 Diff 审查与 hunk 决策的唯一 SQL 出入口，负责审核表证据追加。
// 输入：service 已校验的 ids / 三态决策 / action / detail JSON，以及由身份推导的 scope。
// 输出：contract.DiffReview / HunkDecision / ReviewRecord；越界与不存在统一转 404。
// 边界：固定绑定 team 分区；所有查询先 ScopeFilter 再叠条件；不写子任务状态、不判业务规则、不写审计；
//
//	evidence 只追加到既有 rdc_review_record（同 task_id + subtask_id + detail），不建第二套审核历史。
package rdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// queryer 是 *sql.DB 与 *sql.Tx 的公共子集：仓储的读写只在两者上运行。
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// InteractionRepository 固定绑定 team 分区的交互证据仓储。
type InteractionRepository struct {
	db              *sql.DB
	diffTable       string
	decisionTable   string
	planTable       string
	checkpointTable string
	recordTable     string
	taskTable       string
}

// NewInteractionRepository 构造交互仓储；所有表名只来自 PartitionTeam.Table。
func NewInteractionRepository(db *sql.DB) *InteractionRepository {
	partition := repository.PartitionTeam
	return &InteractionRepository{
		db:              db,
		diffTable:       partition.Table("rdc_diff_review"),
		decisionTable:   partition.Table("rdc_hunk_decision"),
		planTable:       partition.Table("rdc_plan_confirmation"),
		checkpointTable: partition.Table("rdc_checkpoint"),
		recordTable:     partition.Table("rdc_review_record"),
		taskTable:       partition.Table("rdc_dev_task"),
	}
}

// FindLatestReview 返回 scope 内子任务最近一次 diff 审查及其决策；不存在返回 nil。
func (r *InteractionRepository) FindLatestReview(
	ctx context.Context, taskID, subtaskID string, scope contract.Scope,
) (*contract.DiffReview, error) {
	// 先 ScopeFilter 再叠业务条件：跨公司任务在这里就查不到，更不能读到它的产物。
	filter := repository.ScopeFilter(0, "t", scope).
		Add(`d.task_id::text = ?`, taskID).
		Add(`d.subtask_id::text = ?`, subtaskID)
	statement := `SELECT d.id::text, d.task_id::text, d.subtask_id::text, coalesce(d.checkpoint_id::text, ''), ` +
		`d.source, d.submit_status, d.files::text, d.created_at FROM ` + r.diffTable + ` d ` +
		`JOIN ` + r.taskTable + ` t ON t.id = d.task_id WHERE ` + filter.Cond() +
		` ORDER BY d.created_at DESC, d.id DESC LIMIT 1`
	var review contract.DiffReview
	var filesJSON string
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, statement, filter.Args()...).Scan(
		&review.ID, &review.TaskID, &review.SubtaskID, &review.CheckpointID,
		&review.Source, &review.SubmitStatus, &filesJSON, &createdAt,
	)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("查询 Diff 审查失败: %w", err)
	}
	review.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	if err := json.Unmarshal([]byte(filesJSON), &review.Files); err != nil {
		return nil, fmt.Errorf("解析 Diff 产物失败: %w", err)
	}
	review.Files = nonNilDiffs(review.Files)

	decisions, err := r.listDecisions(ctx, r.db, review.ID, scope)
	if err != nil {
		return nil, err
	}
	review.Decisions = decisions
	return &review, nil
}

// FindReviewByID 按审查 id 精确读取；越界 / 不存在返回 nil，供写路径做 404 判定。
func (r *InteractionRepository) FindReviewByID(
	ctx context.Context, reviewID string, scope contract.Scope,
) (*contract.DiffReview, error) {
	filter := repository.ScopeFilter(0, "t", scope).Add(`d.id::text = ?`, reviewID)
	statement := `SELECT d.id::text, d.task_id::text, d.subtask_id::text, coalesce(d.checkpoint_id::text, ''), ` +
		`d.source, d.submit_status, d.files::text, d.created_at FROM ` + r.diffTable + ` d ` +
		`JOIN ` + r.taskTable + ` t ON t.id = d.task_id WHERE ` + filter.Cond()
	var review contract.DiffReview
	var filesJSON string
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, statement, filter.Args()...).Scan(
		&review.ID, &review.TaskID, &review.SubtaskID, &review.CheckpointID,
		&review.Source, &review.SubmitStatus, &filesJSON, &createdAt,
	)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("按 id 查询 Diff 审查失败: %w", err)
	}
	review.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	if err := json.Unmarshal([]byte(filesJSON), &review.Files); err != nil {
		return nil, fmt.Errorf("解析 Diff 产物失败: %w", err)
	}
	review.Files = nonNilDiffs(review.Files)
	decisions, err := r.listDecisions(ctx, r.db, review.ID, scope)
	if err != nil {
		return nil, err
	}
	review.Decisions = decisions
	return &review, nil
}

// listDecisions 读取一次审查的全部 hunk 决策；越界审查由调用方先校验，这里再叠一层 scope。
func (r *InteractionRepository) listDecisions(
	ctx context.Context, q queryer, reviewID string, scope contract.Scope,
) ([]contract.HunkDecision, error) {
	filter := repository.ScopeFilter(0, "t", scope).Add(`d.id::text = ?`, reviewID)
	statement := `SELECT h.review_id::text, h.hunk_id, h.decision, h.decided_by, ` +
		`coalesce(h.decided_at::text, '') FROM ` + r.decisionTable + ` h ` +
		`JOIN ` + r.diffTable + ` d ON d.id = h.review_id ` +
		`JOIN ` + r.taskTable + ` t ON t.id = d.task_id WHERE ` + filter.Cond() +
		` ORDER BY h.hunk_id`
	rows, err := q.QueryContext(ctx, statement, filter.Args()...)
	if err != nil {
		return nil, fmt.Errorf("查询 hunk 决策失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]contract.HunkDecision, 0)
	for rows.Next() {
		var item contract.HunkDecision
		if err := rows.Scan(&item.ReviewID, &item.HunkID, &item.Decision, &item.DecidedBy, &item.DecidedAt); err != nil {
			return nil, fmt.Errorf("映射 hunk 决策失败: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 hunk 决策失败: %w", err)
	}
	return result, nil
}

// ensureScopedReview 在事务内确认审查属于可见范围；越界 / 不存在统一 404。
func (r *InteractionRepository) ensureScopedReview(
	ctx context.Context, q queryer, reviewID string, scope contract.Scope,
) error {
	filter := repository.ScopeFilter(0, "t", scope).Add(`d.id::text = ?`, reviewID)
	statement := `SELECT 1 FROM ` + r.diffTable + ` d ` +
		`JOIN ` + r.taskTable + ` t ON t.id = d.task_id WHERE ` + filter.Cond()
	var exists int
	if err := q.QueryRowContext(ctx, statement, filter.Args()...).Scan(&exists); err != nil {
		if isNoRows(err) {
			return contract.NotFound("Diff 审查")
		}
		return fmt.Errorf("校验 Diff 审查可见性失败: %w", err)
	}
	return nil
}

// upsertDecisionOn 在给定执行器上写一个 hunk 决策；唯一键 (review_id, hunk_id) 防重复刷屏。
func (r *InteractionRepository) upsertDecisionOn(
	ctx context.Context, q queryer, reviewID string, in contract.HunkDecision,
	decidedBy string, scope contract.Scope,
) error {
	statement := `INSERT INTO ` + r.decisionTable +
		` (review_id, hunk_id, decision, decided_by, decided_at, tenant_id)` +
		` VALUES ($1::uuid, $2, $3, $4, now(), NULLIF($5, '')::uuid)` +
		` ON CONFLICT (review_id, hunk_id) DO UPDATE SET ` +
		` decision = EXCLUDED.decision, decided_by = EXCLUDED.decided_by, decided_at = now()`
	_, err := q.ExecContext(ctx, statement,
		reviewID, in.HunkID, in.Decision, decidedBy, scope.TenantID)
	if err != nil {
		return fmt.Errorf("写入 hunk 决策失败: %w", err)
	}
	return nil
}

// UpsertDecision 单 hunk 决策：只写决策表，不写审核记录（提交时统一留一条证据）。
func (r *InteractionRepository) UpsertDecision(
	ctx context.Context, reviewID, hunkID, decision, decidedBy string, scope contract.Scope,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启 hunk 决策事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.ensureScopedReview(ctx, tx, reviewID, scope); err != nil {
		return err
	}
	if err := r.upsertDecisionOn(ctx, tx, reviewID, contract.HunkDecision{
		HunkID: hunkID, Decision: decision,
	}, decidedBy, scope); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 hunk 决策事务失败: %w", err)
	}
	return nil
}

// SaveDecisionsTx 在同一事务内写完全部决策并把审查提交态置为 submitted。
func (r *InteractionRepository) SaveDecisionsTx(
	ctx context.Context, reviewID string, decisions []contract.HunkDecision,
	decidedBy string, scope contract.Scope,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启审查提交事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.ensureScopedReview(ctx, tx, reviewID, scope); err != nil {
		return err
	}
	for _, decision := range decisions {
		if err := r.upsertDecisionOn(ctx, tx, reviewID, decision, decidedBy, scope); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+r.diffTable+
		` SET submit_status = 'submitted', updated_at = now() WHERE id::text = $1`, reviewID); err != nil {
		return fmt.Errorf("更新审查提交状态失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交审查事务失败: %w", err)
	}
	return nil
}

// AppendEvidence 追加一条交互证据到既有审核记录表（只追加，无更新 / 删除）。
func (r *InteractionRepository) AppendEvidence(
	ctx context.Context, taskID, subtaskID, action, reason, commitSha, reviewerUserID string,
	detail map[string]any, scope contract.Scope,
) (contract.ReviewRecord, error) {
	return appendEvidence(ctx, r.db, r.recordTable, r.taskTable,
		taskID, subtaskID, action, reason, commitSha, reviewerUserID, detail, scope)
}

// appendEvidence 支持在外部事务内追加证据，保证「状态 + 证据」同事务提交。
func appendEvidence(
	ctx context.Context, q queryer, recordTable, taskTable,
	taskID, subtaskID, action, reason, commitSha, reviewerUserID string,
	detail map[string]any, scope contract.Scope,
) (contract.ReviewRecord, error) {
	// 再次确认任务在 scope 内，杜绝给越界任务写证据。
	filter := repository.ScopeFilter(0, "t", scope).Add(`t.id::text = ?`, taskID)
	var exists int
	check := `SELECT 1 FROM ` + taskTable + ` t WHERE ` + filter.Cond()
	if err := q.QueryRowContext(ctx, check, filter.Args()...).Scan(&exists); err != nil {
		if isNoRows(err) {
			return contract.ReviewRecord{}, contract.NotFound(contract.ResourceDevTask)
		}
		return contract.ReviewRecord{}, fmt.Errorf("校验证据任务可见性失败: %w", err)
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		return contract.ReviewRecord{}, fmt.Errorf("序列化证据 detail 失败: %w", err)
	}
	statement := `INSERT INTO ` + recordTable +
		` (task_id, subtask_id, action, reason, commit_sha, reviewer_user_id, detail, tenant_id)` +
		` VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8)` +
		` RETURNING id::text, task_id::text, subtask_id::text, action, reason, commit_sha, reviewer_user_id, created_at`
	var record contract.ReviewRecord
	var createdAt time.Time
	if err := q.QueryRowContext(ctx, statement, taskID, subtaskID, action, reason, commitSha,
		reviewerUserID, string(payload), nullIfEmpty(scope.TenantID)).Scan(
		&record.ID, &record.TaskID, &record.SubtaskID, &record.Action,
		&record.Reason, &record.CommitSha, &record.ReviewerUserID, &createdAt,
	); err != nil {
		return contract.ReviewRecord{}, fmt.Errorf("追加交互证据失败: %w", err)
	}
	record.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	record.Detail = detail
	return record, nil
}

// FindEvidence 返回某子任务下某证据 action 的记录（时间倒序），供审批徽标判断是否已决定。
func (r *InteractionRepository) FindEvidence(
	ctx context.Context, taskID, subtaskID, action string, scope contract.Scope,
) ([]contract.ReviewRecord, error) {
	filter := repository.ScopeFilter(0, "t", scope).
		Add(`rec.task_id::text = ?`, taskID).
		Add(`rec.subtask_id::text = ?`, subtaskID).
		Add(`rec.action = ?`, action)
	statement := `SELECT rec.id::text, rec.action, coalesce(rec.detail::text, '{}'), rec.created_at ` +
		`FROM ` + r.recordTable + ` rec JOIN ` + r.taskTable + ` t ON t.id = rec.task_id ` +
		`WHERE ` + filter.Cond() + ` ORDER BY rec.created_at DESC, rec.id DESC LIMIT 1`
	var record contract.ReviewRecord
	var detailJSON string
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, statement, filter.Args()...).Scan(
		&record.ID, &record.Action, &detailJSON, &createdAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("查询交互证据失败: %w", err)
	}
	record.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	record.Detail = map[string]any{}
	if err := json.Unmarshal([]byte(detailJSON), &record.Detail); err != nil {
		return nil, fmt.Errorf("解析交互证据失败: %w", err)
	}
	return []contract.ReviewRecord{record}, nil
}

// nonNilDiffs 把 nil 切片收敛成 []，避免 JSON 序列化出 null 让前端 .length 抛错。
func nonNilDiffs(items []contract.FileDiff) []contract.FileDiff {
	if items == nil {
		return []contract.FileDiff{}
	}
	return items
}

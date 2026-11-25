// Package rdc · 人工审核中心的持久化。
// 职责：team 分区内审核记录的追加、待审任务列表与审核详情的只读查询。
// 输入：service 已校验的动作 / 原因 / commit SHA、任务 / 子任务 id、分页与 scope。
// 输出：contract.ReviewTaskList / ReviewDetail / ReviewRecord；越界统一转 404。
// 边界：固定绑定 team 分区；复用 DevRepository 的表名与 JOIN，不重复第二份 SQL 口径；
//
//	审核记录只追加，无更新 / 删除；所有读查询先 ScopeFilter 再叠业务条件；
//	状态写入不在本文件——那由 DevRepository.UpdateStatusIf 承担。
package rdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// ReviewRepository 固定绑定 team 分区的人工审核仓储。
// 持有 DevRepository 以复用其表名、JOIN 与行映射，避免第二份并列口径。
type ReviewRepository struct {
	db          *sql.DB
	dev         *DevRepository
	recordTable string
	taskStates  string
}

// NewReviewRepository 构造审核仓储；recordTable 由 PartitionTeam 生成，禁止手写拼接。
func NewReviewRepository(db *sql.DB, dev *DevRepository) *ReviewRepository {
	partition := repository.PartitionTeam
	return &ReviewRepository{
		db:          db,
		dev:         dev,
		recordTable: partition.Table("rdc_review_record"),
		// 待审口径：子任务处于 reviewing / waiting_commit / rejected 之一。
		taskStates: `'reviewing', 'waiting_commit', 'rejected'`,
	}
}

// Append 追加一条审核记录；返回带服务端 id / 时间的完整记录。
func (r *ReviewRepository) Append(
	ctx context.Context, taskID, subtaskID, action, reason, commitSha, reviewerUserID string,
	scope contract.Scope,
) (contract.ReviewRecord, error) {
	// scope 校验：任务必须在可见范围内，杜绝给越界任务写审核记录。
	gate := repository.ScopeFilter(0, "t", scope).Add(`t.id::text = ?`, taskID)
	var exists int
	check := `SELECT 1 FROM ` + r.dev.taskTable + ` t WHERE ` + gate.Cond()
	if err := r.db.QueryRowContext(ctx, check, gate.Args()...).Scan(&exists); err != nil {
		if isNoRows(err) {
			return contract.ReviewRecord{}, contract.NotFound(contract.ResourceDevTask)
		}
		return contract.ReviewRecord{}, fmt.Errorf("校验审核任务可见性失败: %w", err)
	}

	statement := `INSERT INTO ` + r.recordTable +
		` (task_id, subtask_id, action, reason, commit_sha, reviewer_user_id, tenant_id)` +
		` VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)` +
		` RETURNING id::text, task_id::text, subtask_id::text, action, reason, commit_sha, reviewer_user_id, created_at, detail::text`
	var record contract.ReviewRecord
	var createdAt time.Time
	var detailJSON string
	err := r.db.QueryRowContext(ctx, statement,
		taskID, subtaskID, action, reason, commitSha, reviewerUserID, nullIfEmpty(scope.TenantID),
	).Scan(
		&record.ID, &record.TaskID, &record.SubtaskID, &record.Action,
		&record.Reason, &record.CommitSha, &record.ReviewerUserID, &createdAt, &detailJSON,
	)
	if err != nil {
		return contract.ReviewRecord{}, fmt.Errorf("追加审核记录失败: %w", err)
	}
	record.Detail = map[string]any{}
	if err := json.Unmarshal([]byte(detailJSON), &record.Detail); err != nil {
		return contract.ReviewRecord{}, fmt.Errorf("解析审核记录 detail 失败: %w", err)
	}
	record.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return record, nil
}

// ListTasks 分页返回含待审子任务的任务（reviewing / waiting_commit / rejected）。
// 复用 DevRepository 的 JOIN 与列序；reviewableCount 统计处于待审口径的子任务数。
func (r *ReviewRepository) ListTasks(
	ctx context.Context, query contract.ReviewTaskQuery, scope contract.Scope,
) (contract.ReviewTaskList, error) {
	filter := repository.ScopeFilter(0, "t", scope).
		AddIf(query.ReqKey != "", `r.req_key = ?`, query.ReqKey).
		AddRaw(`(sf.status IN (` + r.taskStates + `) OR sb.status IN (` + r.taskStates + `))`)

	countSQL := `SELECT count(*) FROM ` + r.dev.taskTable + ` t` + r.dev.baseJoin + filter.Where()
	var total int
	if err := r.db.QueryRowContext(ctx, countSQL, filter.Args()...).Scan(&total); err != nil {
		return contract.ReviewTaskList{}, fmt.Errorf("统计待审任务数量失败: %w", err)
	}

	// 用与 DevRepository 相同的列序取行，再补算 reviewableCount（两条子任务各判一次）。
	statement := `SELECT ` + devTaskColumns + ` FROM ` + r.dev.taskTable + ` t` +
		r.dev.baseJoin + filter.Where() + ` ORDER BY t.created_at DESC` +
		fmt.Sprintf(` LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1))
	args := append(filter.Args(), query.Page.Limit(), query.Page.Offset())
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return contract.ReviewTaskList{}, fmt.Errorf("查询待审任务列表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	base, err := scanTasks(rows)
	if err != nil {
		return contract.ReviewTaskList{}, err
	}
	items := make([]contract.ReviewTask, 0, len(base))
	for _, item := range base {
		reviewable := 0
		if isReviewableStatus(item.FrontendStatus) {
			reviewable++
		}
		if isReviewableStatus(item.BackendStatus) {
			reviewable++
		}
		// overallStatus 不落列：scanTasks 拿到的两子任务状态在这里按统一口径聚合
		// （与 DevService 填充 DevTask.OverallStatus 用同一函数），避免空串与口径漂移。
		overall := contract.AggregateDevStatus([]string{item.FrontendStatus, item.BackendStatus})
		items = append(items, contract.ReviewTask{
			ID: item.ID, Title: item.Title, ReqKey: item.ReqKey,
			RequirementTitle: item.RequirementTitle, ProjectName: item.ProjectName,
			OverallStatus: overall, ReviewableCount: reviewable,
			FrontendStatus: item.FrontendStatus, BackendStatus: item.BackendStatus,
			CreatedAt: item.CreatedAt,
		})
	}
	return contract.ReviewTaskList{
		Items: items,
		Page: contract.Page{
			Page: query.Page.Page, PageSize: query.Page.PageSize, Total: total,
		},
	}, nil
}

// FindDetail 返回审核详情：任务、两条子任务与该任务的全部审核记录（时间正序）。
func (r *ReviewRepository) FindDetail(
	ctx context.Context, id string, scope contract.Scope,
) (contract.ReviewDetail, error) {
	detail, err := r.dev.FindByID(ctx, id, scope)
	if err != nil {
		return contract.ReviewDetail{}, err
	}
	// 与列表同源：仓储 FindByID 不含聚合，这里按统一口径算出整体状态。
	task := contract.ReviewTask{
		ID: detail.ID, Title: detail.Title, ReqKey: detail.ReqKey,
		RequirementTitle: detail.RequirementTitle, ProjectName: detail.ProjectName,
		OverallStatus:  contract.AggregateDevStatus([]string{detail.FrontendStatus, detail.BackendStatus}),
		FrontendStatus: detail.FrontendStatus, BackendStatus: detail.BackendStatus,
		CreatedAt: detail.CreatedAt,
	}
	for _, sub := range detail.Subtasks {
		if isReviewableStatus(sub.Status) {
			task.ReviewableCount++
		}
	}

	filter := repository.ScopeFilter(0, "t", scope).Add(`t.id::text = ?`, id)
	statement := `SELECT rec.id::text, rec.task_id::text, rec.subtask_id::text, rec.action, ` +
		`rec.reason, rec.commit_sha, rec.reviewer_user_id, rec.created_at, coalesce(rec.detail::text, '{}') ` +
		`FROM ` + r.recordTable + ` rec JOIN ` + r.dev.taskTable + ` t ON t.id = rec.task_id ` +
		`WHERE ` + filter.Cond() + ` ORDER BY rec.created_at, rec.id`
	rows, err := r.db.QueryContext(ctx, statement, filter.Args()...)
	if err != nil {
		return contract.ReviewDetail{}, fmt.Errorf("查询审核记录失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := make([]contract.ReviewRecord, 0)
	for rows.Next() {
		var rec contract.ReviewRecord
		var createdAt time.Time
		var detailJSON string
		if err := rows.Scan(
			&rec.ID, &rec.TaskID, &rec.SubtaskID, &rec.Action,
			&rec.Reason, &rec.CommitSha, &rec.ReviewerUserID, &createdAt, &detailJSON,
		); err != nil {
			return contract.ReviewDetail{}, fmt.Errorf("映射审核记录失败: %w", err)
		}
		rec.Detail = map[string]any{}
		if err := json.Unmarshal([]byte(detailJSON), &rec.Detail); err != nil {
			return contract.ReviewDetail{}, fmt.Errorf("解析审核记录 detail 失败: %w", err)
		}
		rec.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return contract.ReviewDetail{}, fmt.Errorf("遍历审核记录失败: %w", err)
	}

	return contract.ReviewDetail{Task: task, Subtasks: detail.Subtasks, Records: records}, nil
}

// isReviewableStatus 判断子任务状态是否属于待审口径。
func isReviewableStatus(status string) bool {
	return status == contract.DevStatusReviewing ||
		status == contract.DevStatusWaitingCommit ||
		status == contract.DevStatusRejected
}

// isNoRows 统一识别 pgx / database/sql 的空行错误。
func isNoRows(err error) bool {
	return err == sql.ErrNoRows || err == pgx.ErrNoRows
}

// nullIfEmpty 把空租户 id 收敛为 SQL NULL，避免往 UUID 列写空串。
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

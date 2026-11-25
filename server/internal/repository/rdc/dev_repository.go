// Package rdc · 研发任务编排层的持久化。
// 职责：team 分区内 rdc_dev_task / rdc_dev_subtask 的唯一 SQL 出入口：单事务建任务并成对插
//
//	子任务、聚合分页列表、详情、资料编辑、级联删除与条件状态更新。
//
// 输入：service 已校验的 DTO / id / 状态、分页与筛选条件、scope。
// 输出：contract.DevTaskDetail / DevSubtask / DevTaskList 与错误（越界统一转 404）。
// 边界：固定绑定 team 分区；不做字段校验、不判业务状态机、不判权限、不写审计；
//
//	所有读查询先 ScopeFilter(0,"t",scope) 再叠业务条件；编辑 SQL 的 SET 列表不含 status。
package rdc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// DevRepository 固定绑定 team 分区的研发任务仓储。
// 表名只来自 repository.PartitionTeam.Table，禁止手写字符串拼接。
type DevRepository struct {
	db            *sql.DB
	taskTable     string
	subtaskTable  string
	aggregateJoin string
	baseJoin      string
}

// devStatusOrdinalCase 是整体状态筛选的 SQL 序号映射，与 contract.DevStatusOrdinal 等价。
// skipped 没有 WHEN 分支 → CASE 返回 NULL → 被 MIN 忽略；两条都 skipped 时
// min_ord 为 NULL，筛选侧用 COALESCE(min_ord, 7) 收敛到 completed。
// rejected 映射为 -1（最小），MIN 一旦遇到驳回即选中它，与 Go 侧「驳回阻断优先」一致；
// 其 DevStatusOrdinal 同为 -1，故筛选 status=rejected 时 COALESCE 不会改写 -1。
// dev_repository_test.go 解析这段真实 SQL 并与 Go 聚合逐组合比对，防止两处漂移。
const devStatusOrdinalCase = `CASE status WHEN 'rejected' THEN -1 WHEN 'pending' THEN 0 WHEN 'ai_running' THEN 1` +
	` WHEN 'ai_completed' THEN 2 WHEN 'reviewing' THEN 3 WHEN 'waiting_commit' THEN 4` +
	` WHEN 'committed' THEN 5 WHEN 'validated' THEN 6 WHEN 'completed' THEN 7 END`

// devTaskColumns 是列表与详情共用的列序；overallStatus 不落列，由 service 聚合后填。
const devTaskColumns = `t.id::text, t.title, t.requirement_id::text, coalesce(r.req_key, ''),` +
	` coalesce(r.title, ''), coalesce(pj.name, ''), coalesce(sf.status, ''), coalesce(sb.status, ''),` +
	` t.creator_user_id, coalesce(nullif(p.name, ''), u.username, ''), t.created_at, t.updated_at`

// devSubtaskColumns 是子任务的统一列序，详情与条件更新后的回读共用。
const devSubtaskColumns = `s.id::text, s.kind, s.status, s.note, s.created_at, s.updated_at`

// 编辑类 SET 子句集中成常量：两处都不含 status，由测试直接断言，杜绝绕过状态机。
const (
	devTaskUpdateSet        = ` t SET title = $1, requirement_id = NULLIF($2, '')::uuid, updated_at = now()`
	devSubtaskNoteUpdateSet = ` s SET note = $1, updated_at = now()`
)

// NewDevRepository 构造唯一一个 team 分区的研发任务仓储。
func NewDevRepository(db *sql.DB) *DevRepository {
	partition := repository.PartitionTeam
	subtaskTable := partition.Table("rdc_dev_subtask")
	return &DevRepository{
		db:           db,
		taskTable:    partition.Table("rdc_dev_task"),
		subtaskTable: subtaskTable,
		aggregateJoin: ` LEFT JOIN (SELECT task_id, MIN(` + devStatusOrdinalCase + `) AS min_ord` +
			` FROM ` + subtaskTable + ` GROUP BY task_id) agg ON agg.task_id = t.id`,
		baseJoin: ` LEFT JOIN ` + partition.Table("rdc_requirements") + ` r ON r.id = t.requirement_id` +
			` LEFT JOIN ` + partition.Table("projects") + ` pj ON pj.id = r.project_id` +
			` LEFT JOIN public.users u ON u.id::text = t.creator_user_id` +
			` LEFT JOIN public.people p ON p.id = u.person_id` +
			` LEFT JOIN ` + subtaskTable + ` sf ON sf.task_id = t.id AND sf.kind = '` + contract.DevKindFrontend + `'` +
			` LEFT JOIN ` + subtaskTable + ` sb ON sb.task_id = t.id AND sb.kind = '` + contract.DevKindBackend + `'`,
	}
}

// devDB 让共享的行映射与辅助写入既能用 *sql.DB 也能用 *sql.Tx，避免两套重复 SQL。
type devDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// devScanner 让单行与多行查询共用同一份列映射。
type devScanner interface {
	Scan(dest ...any) error
}

// CreateWithSubtasks 在同一事务内插任务 → 成对插 pending 子任务 → JOIN 回读 → COMMIT。
// 任一子任务插入失败整笔回滚，不留"有任务没子任务"的半截数据。
func (r *DevRepository) CreateWithSubtasks(
	ctx context.Context,
	in contract.DevTaskInput,
	creatorUserID string,
	tenantID string,
) (contract.DevTaskDetail, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.DevTaskDetail{}, fmt.Errorf("开启研发任务创建事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insert := `INSERT INTO ` + r.taskTable +
		` (requirement_id, title, creator_user_id, tenant_id)` +
		` VALUES ($1::uuid, $2, $3, NULLIF($4, '')::uuid) RETURNING id::text`
	var id string
	if err := tx.QueryRowContext(ctx, insert, in.RequirementID, in.Title, creatorUserID, tenantID).Scan(&id); err != nil {
		return contract.DevTaskDetail{}, fmt.Errorf("写入研发任务失败: %w", err)
	}

	insertSubtask := `INSERT INTO ` + r.subtaskTable + ` (task_id, kind) VALUES ($1::uuid, $2)`
	for _, kind := range []string{contract.DevKindFrontend, contract.DevKindBackend} {
		if _, err := tx.ExecContext(ctx, insertSubtask, id, kind); err != nil {
			return contract.DevTaskDetail{}, fmt.Errorf("写入研发子任务失败 kind=%s: %w", kind, err)
		}
	}

	// 刚插入的行必然属于本事务，回读按 id 定位即可，无需再叠 scope。
	item, err := r.findTask(ctx, tx, `WHERE t.id = $1`, id)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.DevTaskDetail{}, fmt.Errorf("提交研发任务创建事务失败: %w", err)
	}
	return item, nil
}

// List 返回 scope 范围内按 reqKey / 整体状态过滤的任务分页。
// ScopeFilter 必须最先添加：后续业务条件以 AND 叠加，不能破坏租户边界。
func (r *DevRepository) List(
	ctx context.Context, query contract.DevTaskQuery, scope contract.Scope,
) (contract.DevTaskList, error) {
	filter := repository.ScopeFilter(0, "t", scope)
	filter.AddIf(query.ReqKey != "", `r.req_key = ?`, query.ReqKey)
	filter.AddIf(query.Status != "", `COALESCE(agg.min_ord, 7) = ?`, contract.DevStatusOrdinal(query.Status))

	var total int
	countSQL := `SELECT count(*) FROM ` + r.taskTable + ` t` + r.baseJoin + r.aggregateJoin + filter.Where()
	if err := r.db.QueryRowContext(ctx, countSQL, filter.Args()...).Scan(&total); err != nil {
		return contract.DevTaskList{}, fmt.Errorf("统计研发任务数量失败: %w", err)
	}

	args := append(filter.Args(), query.Page.Limit(), query.Page.Offset())
	statement := `SELECT ` + devTaskColumns + ` FROM ` + r.taskTable + ` t` + r.baseJoin +
		r.aggregateJoin + filter.Where() + ` ORDER BY t.created_at DESC` +
		fmt.Sprintf(` LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1))
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return contract.DevTaskList{}, fmt.Errorf("查询研发任务列表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items, err := scanTasks(rows)
	if err != nil {
		return contract.DevTaskList{}, err
	}
	return contract.DevTaskList{
		Items: items,
		Page: contract.Page{
			Page: query.Page.Page, PageSize: query.Page.PageSize, Total: total,
		},
	}, nil
}

// FindByID 按 id 查详情（含两条子任务）；越界与不存在同为 404。
func (r *DevRepository) FindByID(ctx context.Context, id string, scope contract.Scope) (contract.DevTaskDetail, error) {
	filter := repository.ScopeFilter(0, "t", scope).Add(`t.id::text = ?`, id)
	return r.findTask(ctx, r.db, filter.Where(), filter.Args()...)
}

// FindSubtask 按 (taskID, subtaskID) 查子任务；越界与不存在同为 404。
func (r *DevRepository) FindSubtask(
	ctx context.Context, taskID, subtaskID string, scope contract.Scope,
) (contract.DevSubtask, error) {
	filter := repository.ScopeFilter(0, "t", scope).
		Add(`t.id::text = ?`, taskID).
		Add(`s.id::text = ?`, subtaskID).
		AddRaw(`s.task_id = t.id`)
	statement := `SELECT ` + devSubtaskColumns + ` FROM ` + r.subtaskTable + ` s` +
		` JOIN ` + r.taskTable + ` t ON t.id = s.task_id` + filter.Where()
	subtask, err := scanSubtask(r.db.QueryRowContext(ctx, statement, filter.Args()...))
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return contract.DevSubtask{}, contract.NotFound(contract.ResourceDevSubtask)
	}
	if err != nil {
		return contract.DevSubtask{}, err
	}
	return subtask, nil
}

// UpdateTask 在单事务内编辑任务名 / 绑定需求 / 子任务备注，并回读完整详情。
// SET 列表只有 title / requirement_id / note / updated_at，绝不出现 status。
func (r *DevRepository) UpdateTask(
	ctx context.Context, id string, in contract.DevTaskUpdate, scope contract.Scope,
) (contract.DevTaskDetail, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.DevTaskDetail{}, fmt.Errorf("开启研发任务编辑事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// SET 先占用 $1/$2，ScopeFilter 的 base=2 让 WHERE 从 $3 开始，不会错位。
	filter := repository.ScopeFilter(2, "t", scope).Add(`t.id::text = ?`, id)
	statement := `UPDATE ` + r.taskTable + devTaskUpdateSet +
		` WHERE ` + filter.Cond() + ` RETURNING t.id::text`
	var updatedID string
	updateArgs := append([]any{in.Title, in.RequirementID}, filter.Args()...)
	if err := tx.QueryRowContext(ctx, statement, updateArgs...).Scan(&updatedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.DevTaskDetail{}, contract.NotFound(contract.ResourceDevTask)
		}
		return contract.DevTaskDetail{}, fmt.Errorf("更新研发任务失败: %w", err)
	}

	for _, note := range in.Subtasks {
		if _, err := r.updateSubtaskNote(ctx, tx, id, note.ID, note.Note, scope); err != nil {
			return contract.DevTaskDetail{}, err
		}
	}

	detailFilter := repository.ScopeFilter(0, "t", scope).Add(`t.id::text = ?`, id)
	detail, err := r.findTask(ctx, tx, detailFilter.Where(), detailFilter.Args()...)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.DevTaskDetail{}, fmt.Errorf("提交研发任务编辑事务失败: %w", err)
	}
	return detail, nil
}

// UpdateSubtaskNote 单独改一条子任务的备注；供独立调用与测试使用。
// 与 UpdateTask 内部共用 updateSubtaskNote，避免两处 SQL 漂移。
func (r *DevRepository) UpdateSubtaskNote(
	ctx context.Context, taskID, subtaskID, note string, scope contract.Scope,
) (bool, error) {
	return r.updateSubtaskNote(ctx, r.db, taskID, subtaskID, note, scope)
}

// updateSubtaskNote 只写 note / updated_at；通过 JOIN 任务表叠 scope，保证跨公司改不到。
func (r *DevRepository) updateSubtaskNote(
	ctx context.Context, q devDB, taskID, subtaskID, note string, scope contract.Scope,
) (bool, error) {
	filter := repository.ScopeFilter(1, "t", scope).
		AddRaw(`s.task_id = t.id`).
		Add(`t.id::text = ?`, taskID).
		Add(`s.id::text = ?`, subtaskID)
	statement := `UPDATE ` + r.subtaskTable + devSubtaskNoteUpdateSet +
		` FROM ` + r.taskTable + ` t WHERE ` + filter.Cond() + ` RETURNING s.id::text`
	var id string
	err := q.QueryRowContext(ctx, statement, append([]any{note}, filter.Args()...)...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("更新子任务备注失败: %w", err)
	}
	return true, nil
}

// UpdateStatusIf 以 `WHERE s.status = from` 做条件更新：并发 / 陈旧点击影响 0 行，
// 由 service 翻译成 400「状态已变化，请刷新后重试」，杜绝跳级。
// 仓储只执行调用方给定的 from/to，不判断状态机合法性（那是 service 的职责）。
func (r *DevRepository) UpdateStatusIf(
	ctx context.Context, taskID, subtaskID, from, to string, scope contract.Scope,
) (bool, error) {
	filter := repository.ScopeFilter(1, "t", scope).
		AddRaw(`s.task_id = t.id`).
		Add(`t.id::text = ?`, taskID).
		Add(`s.id::text = ?`, subtaskID).
		Add(`s.status = ?`, from)
	statement := `UPDATE ` + r.subtaskTable + ` s SET status = $1, updated_at = now()` +
		` FROM ` + r.taskTable + ` t WHERE ` + filter.Cond()
	result, err := r.db.ExecContext(ctx, statement, append([]any{to}, filter.Args()...)...)
	if err != nil {
		return false, fmt.Errorf("更新子任务状态失败: %w", err)
	}
	return statusUpdateAffected(result)
}

// statusUpdateAffected 把条件更新的影响行数收敛成布尔值：0 行即陈旧 / 并发点击。
func statusUpdateAffected(result sql.Result) (bool, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取状态更新结果失败: %w", err)
	}
	return affected > 0, nil
}

// Delete 按 scope + id 删除任务；子任务由外键 ON DELETE CASCADE 一并清理。
func (r *DevRepository) Delete(ctx context.Context, id string, scope contract.Scope) error {
	filter := repository.ScopeFilter(0, "t", scope).Add(`t.id::text = ?`, id)
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM `+r.taskTable+` t`+filter.Where(), filter.Args()...)
	if err != nil {
		return fmt.Errorf("删除研发任务失败: %w", err)
	}
	return repository.EnsureDeleted(result, contract.ResourceDevTask)
}

// findTask 读取一条任务并按契约补齐整体状态与两条子任务。
func (r *DevRepository) findTask(ctx context.Context, q devDB, where string, args ...any) (contract.DevTaskDetail, error) {
	statement := `SELECT ` + devTaskColumns + ` FROM ` + r.taskTable + ` t` + r.baseJoin + ` ` + where
	item, err := scanTask(q.QueryRowContext(ctx, statement, args...))
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return contract.DevTaskDetail{}, contract.NotFound(contract.ResourceDevTask)
	}
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	subtasks, err := r.loadSubtasks(ctx, q, item.ID)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	return contract.DevTaskDetail{DevTask: item, Subtasks: subtasks}, nil
}

// loadSubtasks 按 kind 升序回读两条子任务；新任务必然恰好两条。
func (r *DevRepository) loadSubtasks(ctx context.Context, q devDB, taskID string) ([]contract.DevSubtask, error) {
	statement := `SELECT ` + devSubtaskColumns + ` FROM ` + r.subtaskTable + ` s` +
		` WHERE s.task_id = $1::uuid ORDER BY s.kind`
	rows, err := q.QueryContext(ctx, statement, taskID)
	if err != nil {
		return nil, fmt.Errorf("查询研发子任务失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	subtasks := make([]contract.DevSubtask, 0, 2)
	for rows.Next() {
		subtask, err := scanSubtask(rows)
		if err != nil {
			return nil, err
		}
		subtasks = append(subtasks, subtask)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历研发子任务失败: %w", err)
	}
	return subtasks, nil
}

// scanTasks 把多行结果映射成任务列表；空结果返回非 nil 空切片。
func scanTasks(rows *sql.Rows) ([]contract.DevTask, error) {
	items := make([]contract.DevTask, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历研发任务列表失败: %w", err)
	}
	return items, nil
}

// scanTask 统一映射任务列；created_at / updated_at 统一转成 UTC 的 RFC3339 文本。
func scanTask(row devScanner) (contract.DevTask, error) {
	var item contract.DevTask
	var createdAt, updatedAt time.Time
	if err := row.Scan(&item.ID, &item.Title, &item.RequirementID, &item.ReqKey,
		&item.RequirementTitle, &item.ProjectName, &item.FrontendStatus, &item.BackendStatus,
		&item.CreatorUserID, &item.CreatorName, &createdAt, &updatedAt); err != nil {
		return contract.DevTask{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	item.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return item, nil
}

// scanSubtask 统一映射子任务列；最近推进时间取 updated_at。
func scanSubtask(row devScanner) (contract.DevSubtask, error) {
	var subtask contract.DevSubtask
	var createdAt, updatedAt time.Time
	if err := row.Scan(&subtask.ID, &subtask.Kind, &subtask.Status, &subtask.Note,
		&createdAt, &updatedAt); err != nil {
		return contract.DevSubtask{}, err
	}
	subtask.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	subtask.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return subtask, nil
}

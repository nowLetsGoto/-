// Package rdc 负责 team 分区的交付证据持久化。
// 职责：按需求聚合当前快照、查询历史，并在需求行锁事务内追加证据及归档。
// 输入：已鉴权 scope、需求 ID、分页条件和 service 组装的快照；输出：交付投影。
// 边界：只写 SQL/行映射，不判业务规则、不执行 Git、不写审计。
package rdc

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// DeliveryRepository 固定绑定 team 表。
type DeliveryRepository struct {
	db                            *sql.DB
	table, requirements, projects string
}

func NewDeliveryRepository(db *sql.DB) *DeliveryRepository {
	p := repository.PartitionTeam
	return &DeliveryRepository{db: db, table: p.Table("rdc_delivery_record"), requirements: p.Table("rdc_requirements"), projects: p.Table("projects")}
}

const deliveryCols = `coalesce(d.id::text,''), coalesce(d.seq,0), r.id::text, r.req_key, r.title, coalesce(pj.name,''), r.status,
 coalesce(d.test_result,'not_registered'), coalesce(d.test_summary,''), coalesce(d.test_report_ref,''), coalesce(d.test_registered_by,''), d.test_registered_at,
 coalesce(d.acceptance_status,'pending'), coalesce(d.acceptance_items,'[]'), coalesce(d.acceptance_note,''), coalesce(d.accepted_by,''), d.accepted_at,
 coalesce(d.archived,false), coalesce(d.archived_by,''), d.archived_at,
 coalesce(nullif(person.name,''),u.username,''), d.created_at`

func (r *DeliveryRepository) join() string {
	return ` LEFT JOIN ` + r.projects + ` pj ON pj.id=r.project_id LEFT JOIN public.users u ON u.id::text=d.creator_user_id LEFT JOIN public.people person ON person.id=u.person_id`
}
func scanDelivery(row interface{ Scan(...any) error }) (contract.DeliveryRecord, error) {
	var rec contract.DeliveryRecord
	var testAt, acceptedAt, archivedAt, createdAt sql.NullTime
	err := row.Scan(&rec.ID, &rec.Seq, &rec.RequirementID, &rec.ReqKey, &rec.RequirementTitle, &rec.ProjectName, &rec.RequirementStatus,
		&rec.TestResult, &rec.TestSummary, &rec.TestReportRef, &rec.TestRegisteredBy, &testAt,
		&rec.AcceptanceStatus, &rec.AcceptanceItems, &rec.AcceptanceNote, &rec.AcceptedBy, &acceptedAt,
		&rec.Archived, &rec.ArchivedBy, &archivedAt, &rec.CreatorName, &createdAt)
	if err != nil {
		return rec, err
	}
	if testAt.Valid {
		rec.TestRegisteredAt = testAt.Time.UTC().Format(time.RFC3339)
	}
	if acceptedAt.Valid {
		rec.AcceptedAt = acceptedAt.Time.UTC().Format(time.RFC3339)
	}
	if archivedAt.Valid {
		rec.ArchivedAt = archivedAt.Time.UTC().Format(time.RFC3339)
	}
	if createdAt.Valid {
		rec.CreatedAt = createdAt.Time.UTC().Format(time.RFC3339)
	}
	return rec, nil
}
func (r *DeliveryRepository) base() string {
	return `SELECT ` + deliveryCols + ` FROM ` + r.requirements + ` r LEFT JOIN ` + r.table + ` d ON d.requirement_id=r.id` + r.join()
}

// RequirementVisible 先按 scope 查需求，跨公司与不存在统一报 404。
func (r *DeliveryRepository) RequirementVisible(ctx context.Context, id string, scope contract.Scope) error {
	filter := repository.ScopeFilter(0, "r", scope).Add(`r.id::text=?`, id)
	var exists int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM `+r.requirements+` r WHERE `+filter.Cond(), filter.Args()...).Scan(&exists)
	if isNoRows(err) {
		return contract.NotFound(contract.ResourceRequirement)
	}
	return err
}

// FindDetail 返回当前证据以及最近 50 条历史；无证据也可展示需求并登记首条。
func (r *DeliveryRepository) FindDetail(ctx context.Context, id string, scope contract.Scope) (contract.DeliveryDetail, error) {
	filter := repository.ScopeFilter(0, "r", scope).Add(`r.id::text=?`, id)
	var detail contract.DeliveryDetail
	err := r.db.QueryRowContext(ctx, `SELECT r.id::text,r.req_key,r.title,r.description,r.acceptance_criteria,r.status,coalesce(pj.name,'') FROM `+r.requirements+` r LEFT JOIN `+r.projects+` pj ON pj.id=r.project_id WHERE `+filter.Cond(), filter.Args()...).Scan(&detail.RequirementID, &detail.ReqKey, &detail.Title, &detail.Description, &detail.AcceptanceCriteria, &detail.RequirementStatus, &detail.ProjectName)
	if isNoRows(err) {
		return detail, contract.NotFound(contract.ResourceRequirement)
	}
	if err != nil {
		return detail, err
	}
	detail.History = []contract.DeliveryRecord{}
	// LEFT JOIN 的首条空证据代表尚未登记；仍返回显式 pending 当前态。
	rows, err := r.db.QueryContext(ctx, r.base()+` WHERE `+filter.Cond()+` ORDER BY d.seq DESC NULLS LAST LIMIT 50`, filter.Args()...)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanDelivery(rows)
		if scanErr != nil {
			return detail, scanErr
		}
		if item.ID != "" {
			detail.History = append(detail.History, item)
		} else {
			detail.Current = item
		}
	}
	if err = rows.Err(); err != nil {
		return detail, err
	}
	if len(detail.History) > 0 {
		detail.Current = detail.History[0]
	}
	return detail, nil
}

// ListCurrent 在 scope 内先按需求选最大 seq，外层才按当前状态筛选。
func (r *DeliveryRepository) ListCurrent(ctx context.Context, q contract.DeliveryQuery, scope contract.Scope) (contract.DeliveryRecordList, error) {
	filter := repository.ScopeFilter(0, "r", scope)
	// SELECT * 的列名由内层显式别名命名；避免筛选旧快照。
	inner := `SELECT DISTINCT ON (r.id) ` + deliveryListCols + ` FROM ` + r.requirements + ` r LEFT JOIN ` + r.table + ` d ON d.requirement_id=r.id` + r.join() + filter.Where() + ` ORDER BY r.id,d.seq DESC NULLS LAST`
	args := append(filter.Args(), q.Keyword, q.AcceptanceStatus, q.Archived)
	// 使用下方占位符替换，前置 scope 参数始终排在业务参数之前。
	n := len(filter.Args())
	conditions := fmt.Sprintf(` WHERE ($%d='' OR current.req_key ILIKE '%%'||$%d||'%%' OR current.title ILIKE '%%'||$%d||'%%') AND ($%d='' OR current.acceptance_status=$%d) AND ($%d='' OR current.archived=($%d='true'))`, n+1, n+1, n+1, n+2, n+2, n+3, n+3)
	base := ` FROM (` + inner + `) current` + conditions
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*)`+base, args...).Scan(&total); err != nil {
		return contract.DeliveryRecordList{}, err
	}
	query := `SELECT ` + deliveryListOuterCols + base + fmt.Sprintf(` ORDER BY current.created_at DESC NULLS LAST,current.req_key LIMIT $%d OFFSET $%d`, n+4, n+5)
	rows, err := r.db.QueryContext(ctx, query, append(args, q.Page.Limit(), q.Page.Offset())...)
	if err != nil {
		return contract.DeliveryRecordList{}, err
	}
	defer rows.Close()
	items := []contract.DeliveryRecord{}
	for rows.Next() {
		item, scanErr := scanDelivery(rows)
		if scanErr != nil {
			return contract.DeliveryRecordList{}, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return contract.DeliveryRecordList{}, err
	}
	return contract.DeliveryRecordList{Items: items, Page: contract.Page{Page: q.Page.Page, PageSize: q.Page.PageSize, Total: total}}, nil
}

const deliveryListCols = `coalesce(d.id::text,'') AS id,coalesce(d.seq,0) AS seq,r.id::text AS requirement_id,r.req_key,r.title,coalesce(pj.name,'') AS project_name,r.status AS requirement_status,coalesce(d.test_result,'not_registered') AS test_result,coalesce(d.test_summary,'') AS test_summary,coalesce(d.test_report_ref,'') AS test_report_ref,coalesce(d.test_registered_by,'') AS test_registered_by,d.test_registered_at,coalesce(d.acceptance_status,'pending') AS acceptance_status,coalesce(d.acceptance_items,'[]') AS acceptance_items,coalesce(d.acceptance_note,'') AS acceptance_note,coalesce(d.accepted_by,'') AS accepted_by,d.accepted_at,coalesce(d.archived,false) AS archived,coalesce(d.archived_by,'') AS archived_by,d.archived_at,coalesce(nullif(person.name,''),u.username,'') AS creator_name,d.created_at`
const deliveryListOuterCols = `current.id,current.seq,current.requirement_id,current.req_key,current.title,current.project_name,current.requirement_status,current.test_result,current.test_summary,current.test_report_ref,current.test_registered_by,current.test_registered_at,current.acceptance_status,current.acceptance_items,current.acceptance_note,current.accepted_by,current.accepted_at,current.archived,current.archived_by,current.archived_at,current.creator_name,current.created_at`

// LockedDelivery 持有同一事务中的行锁及快照；业务规则由调用方回调判定。
type LockedDelivery struct {
	tx                                                          *sql.Tx
	repo                                                        *DeliveryRepository
	RequirementID, Status, Criteria, ReqKey, Title, ProjectName string
	Current                                                     contract.DeliveryRecord
}

// WithLockedRequirement 三类写操作共同先锁需求行，锁内读最新快照。
func (r *DeliveryRepository) WithLockedRequirement(ctx context.Context, scope contract.Scope, id string, fn func(*LockedDelivery) (contract.DeliveryRecord, error)) (contract.DeliveryRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.DeliveryRecord{}, err
	}
	defer tx.Rollback()
	filter := repository.ScopeFilter(0, "r", scope).Add(`r.id::text=?`, id)
	state := &LockedDelivery{tx: tx, repo: r, RequirementID: id}
	err = tx.QueryRowContext(ctx, `SELECT r.status,r.acceptance_criteria,r.req_key,r.title,coalesce(pj.name,'') FROM `+r.requirements+` r LEFT JOIN `+r.projects+` pj ON pj.id=r.project_id WHERE `+filter.Cond()+` FOR UPDATE OF r`, filter.Args()...).Scan(&state.Status, &state.Criteria, &state.ReqKey, &state.Title, &state.ProjectName)
	if isNoRows(err) {
		return contract.DeliveryRecord{}, contract.NotFound(contract.ResourceRequirement)
	}
	if err != nil {
		return contract.DeliveryRecord{}, err
	}
	state.Current, err = scanDelivery(tx.QueryRowContext(ctx, `SELECT `+deliveryCols+` FROM `+r.requirements+` r JOIN `+r.table+` d ON d.requirement_id=r.id`+r.join()+` WHERE r.id=$1::uuid ORDER BY d.seq DESC LIMIT 1`, id))
	if isNoRows(err) {
		state.Current = contract.DeliveryRecord{RequirementID: id, ReqKey: state.ReqKey, RequirementTitle: state.Title, ProjectName: state.ProjectName, RequirementStatus: state.Status, TestResult: contract.TestNotRegistered, AcceptanceStatus: contract.AcceptPending, AcceptanceItems: "[]"}
	} else if err != nil {
		return contract.DeliveryRecord{}, err
	}
	result, err := fn(state)
	if err != nil {
		return contract.DeliveryRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return contract.DeliveryRecord{}, fmt.Errorf("提交交付证据事务失败: %w", err)
	}
	return result, nil
}

// Append 仅在锁内新增完整快照，调用方负责覆盖变更字段。
func (s *LockedDelivery) Append(ctx context.Context, scope contract.Scope, actor string, row contract.DeliveryRecord) (contract.DeliveryRecord, error) {
	var created, timeTest, timeAccept, timeArchive sql.NullTime
	query := `INSERT INTO ` + s.repo.table + ` (requirement_id,tenant_id,test_result,test_summary,test_report_ref,test_registered_by,test_registered_at,acceptance_status,acceptance_items,acceptance_note,accepted_by,accepted_at,archived,archived_by,archived_at,creator_user_id) VALUES ($1::uuid,NULLIF($2,'')::uuid,$3,$4,$5,$6,NULLIF($7,'')::timestamptz,$8,$9,$10,$11,NULLIF($12,'')::timestamptz,$13,$14,NULLIF($15,'')::timestamptz,$16) RETURNING id::text,seq,created_at,test_registered_at,accepted_at,archived_at`
	err := s.tx.QueryRowContext(ctx, query, s.RequirementID, scope.TenantID, row.TestResult, row.TestSummary, row.TestReportRef, row.TestRegisteredBy, row.TestRegisteredAt, row.AcceptanceStatus, row.AcceptanceItems, row.AcceptanceNote, row.AcceptedBy, row.AcceptedAt, row.Archived, row.ArchivedBy, row.ArchivedAt, actor).Scan(&row.ID, &row.Seq, &created, &timeTest, &timeAccept, &timeArchive)
	if err != nil {
		return contract.DeliveryRecord{}, fmt.Errorf("追加交付快照失败: %w", err)
	}
	row.RequirementID = s.RequirementID
	row.ReqKey = s.ReqKey
	row.RequirementTitle = s.Title
	row.ProjectName = s.ProjectName
	row.RequirementStatus = s.Status
	row.CreatorName = actor
	if created.Valid {
		row.CreatedAt = created.Time.UTC().Format(time.RFC3339)
	}
	return row, nil
}

// MarkArchived 与 Append 处于同一事务；影响 0 行让整笔回滚。
func (s *LockedDelivery) MarkArchived(ctx context.Context, scope contract.Scope) error {
	filter := repository.ScopeFilter(0, "r", scope).Add(`r.id::text=?`, s.RequirementID).Add(`r.status <> ?`, contract.ReqStatusArchived)
	res, err := s.tx.ExecContext(ctx, `UPDATE `+s.repo.requirements+` r SET status='archived' WHERE `+filter.Cond(), filter.Args()...)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return &contract.ValidationError{Message: "需求状态已变化，请刷新后重试"}
	}
	return nil
}

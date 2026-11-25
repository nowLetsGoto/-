// Package rdc · 研发中心需求池的持久化。
// 职责：team 分区内 rdc_requirements 与 rdc_requirement_attachment 的唯一 SQL 出入口；
//
//	单事务原子分配编号序号 + 建需求 + 插附件，分页列表/详情投影项目名，按 scope 隔离读取。
//
// 输入：数据库连接、service 已校验的输入与附件行（id / relative_path 由 service 生成）、
//
//	调用者与归属信息、UTC 日期、查询条件与 scope。
//
// 输出：contract.Requirement / RequirementDetail / RequirementList 与错误（越界统一转 404）。
// 边界：固定绑定 team 分区；不做字段校验、不判权限、不写审计、不做文件 IO、不生成相对路径；
//
//	所有读查询必须先 ScopeFilter 再追加业务条件，非本公司一律 404。
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

// Repository 固定绑定 team 分区的需求仓储。
// 表名只来自 repository.PartitionTeam.Table，禁止手写字符串拼接。
type Repository struct {
	db               *sql.DB
	table            string // team.rdc_requirements
	seqTable         string // team.rdc_req_key_sequences
	attachmentsTable string // team.rdc_requirement_attachment
	projectJoin      string // LEFT JOIN team.projects ... 投影项目名
}

// NewRepository 构造唯一一个 team 分区的需求仓储。
func NewRepository(db *sql.DB) *Repository {
	partition := repository.PartitionTeam
	return &Repository{
		db:               db,
		table:            partition.Table("rdc_requirements"),
		seqTable:         partition.Table("rdc_req_key_sequences"),
		attachmentsTable: partition.Table("rdc_requirement_attachment"),
		projectJoin:      ` LEFT JOIN ` + partition.Table("projects") + ` pj ON pj.id = r.project_id`,
	}
}

// selectColumns 投影创建人姓名与所属项目名：
// people.name 优先、回落 users.username；项目名由 LEFT JOIN 得到，存量无归属需求为空串。
// coalesce 把可空列收敛成空串，避免扫描 NULL 进 string 报错。
const selectColumns = `r.id::text, r.req_key, r.title, r.description, r.status,
r.acceptance_criteria, coalesce(r.project_id::text, ''), coalesce(pj.name, ''),
r.creator_user_id, coalesce(nullif(p.name, ''), u.username, ''), r.created_at`

const creatorJoin = ` LEFT JOIN public.users u ON u.id::text = r.creator_user_id` +
	` LEFT JOIN public.people p ON p.id = u.person_id`

// baseSelect 返回统一的主查询前缀，列表与单条查询共用同一份列映射，避免两处漂移。
func (r *Repository) baseSelect() string {
	return `SELECT ` + selectColumns + ` FROM ` + r.table + ` r` + creatorJoin + r.projectJoin
}

// CreateWithAttachments 在**同一事务**内完成：原子分配序号 → 插需求（含 project_id）
// → 批量插附件行 → JOIN 回读完整需求 → COMMIT。
//
// 附件的 id 与 relative_path 由 service 在事务前生成并经 paths 算出，本函数只做 SQL；
// 事务失败时需求与附件行一起回滚，不留半截数据。文件系统无事务，搬文件由 service 在提交后完成。
func (r *Repository) CreateWithAttachments(
	ctx context.Context,
	in contract.RequirementInput,
	creatorUserID string,
	tenantID string,
	projectID string,
	day string,
	rows []contract.AttachmentRow,
) (contract.Requirement, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Requirement{}, fmt.Errorf("开启需求创建事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	seq, err := allocateSeq(ctx, tx, r.seqTable, day)
	if err != nil {
		return contract.Requirement{}, err
	}
	reqKey := contract.FormatReqKey(day, seq)

	// NULLIF 把超管的空公司标识转成 NULL；projectID 已由 service 校验必填且合法。
	query := `INSERT INTO ` + r.table + `
		(req_key, title, description, status, acceptance_criteria, project_id, creator_user_id, tenant_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, $7, NULLIF($8, '')::uuid)
		RETURNING id::text`
	var id string
	if err := tx.QueryRowContext(ctx, query,
		reqKey, in.Title, in.Description, in.Status, in.AcceptanceCriteria,
		projectID, creatorUserID, tenantID).Scan(&id); err != nil {
		return contract.Requirement{}, fmt.Errorf("写入需求失败: %w", err)
	}

	for _, row := range rows {
		insert := `INSERT INTO ` + r.attachmentsTable + `
			(id, requirement_id, file_name, content_type, file_size, relative_path)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)`
		if _, err := tx.ExecContext(ctx, insert,
			row.ID, id, row.FileName, row.ContentType, row.FileSize, row.RelativePath); err != nil {
			return contract.Requirement{}, fmt.Errorf("写入需求图片失败: %w", err)
		}
	}

	// 同一事务内回读完整行：req_key 刚分配、必然唯一，无需再叠 scope 条件。
	item, err := scanOne(ctx, tx, r.baseSelect()+` WHERE r.id = $1`, id)
	if err != nil {
		return contract.Requirement{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Requirement{}, fmt.Errorf("提交需求创建事务失败: %w", err)
	}
	return item, nil
}

// allocateSeq 用数据库级 UPSERT 原子地拿到当天的下一个序号。
// 严禁 SELECT count(*)+1 / max(seq)+1：并发下会撞 req_key UNIQUE 并把用户请求变成 500。
func allocateSeq(ctx context.Context, tx *sql.Tx, seqTable, day string) (int, error) {
	// AS s + s.next_seq 是必需的：PostgreSQL 在 DO UPDATE 里把目标列与 EXCLUDED 列
	// 都纳入解析范围，裸写 next_seq 会报 "column reference next_seq is ambiguous"。
	query := `INSERT INTO ` + seqTable + ` AS s (day, next_seq) VALUES ($1::date, 1)
		ON CONFLICT (day) DO UPDATE SET next_seq = s.next_seq + 1
		RETURNING s.next_seq`
	var seq int
	if err := tx.QueryRowContext(ctx, query, day).Scan(&seq); err != nil {
		return 0, fmt.Errorf("分配需求编号序号失败: %w", err)
	}
	return seq, nil
}

// List 返回 scope 范围内按状态 / 项目 / 关键字过滤的需求分页与总数。
// 必须先 ScopeFilter（公司隔离 / 失败关闭）再追加业务条件；
// projectId 用 ::text 比较，避免非法 uuid 字符串触发数据库 500。
func (r *Repository) List(ctx context.Context, query contract.RequirementQuery, scope contract.Scope) (contract.RequirementList, error) {
	filter := repository.ScopeFilter(0, "r", scope)
	filter.AddIf(query.Status != "", `r.status = ?`, query.Status)
	filter.AddIf(query.ProjectID != "", `r.project_id::text = ?`, query.ProjectID)
	// 关键字匹配编号或标题：两个占位符必须绑同一个模式串，用 AddMulti 一次追加，
	// 不能拆成两次 Add（那样会变成 AND），也不能把 OR 留在括号外（会破坏 AND 优先级导致跨租户）。
	if query.Keyword != "" {
		pattern := "%" + query.Keyword + "%"
		filter.AddMulti(`(r.req_key ILIKE ? OR r.title ILIKE ?)`, pattern, pattern)
	}

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM `+r.table+` r`+filter.Where(), filter.Args()...).Scan(&total); err != nil {
		return contract.RequirementList{}, fmt.Errorf("统计需求数量失败: %w", err)
	}

	args := append(filter.Args(), query.Page.Limit(), query.Page.Offset())
	statement := r.baseSelect() + filter.Where() +
		fmt.Sprintf(` ORDER BY r.created_at DESC LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1))
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return contract.RequirementList{}, fmt.Errorf("查询需求列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Requirement, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return contract.RequirementList{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return contract.RequirementList{}, fmt.Errorf("遍历需求列表失败: %w", err)
	}
	return contract.RequirementList{
		Items: items,
		Page: contract.Page{
			Page: query.Page.Page, PageSize: query.Page.PageSize, Total: total,
		},
	}, nil
}

// ExistsVisibleByID 判断一条需求是否存在且在当前 scope 内可见。
// 供研发任务新建 / 换绑时校验归属；跨公司、不存在的 id 都返回 false，不泄漏存在性细节。
func (r *Repository) ExistsVisibleByID(ctx context.Context, id string, scope contract.Scope) bool {
	filter := repository.ScopeFilter(0, "r", scope).Add(`r.id::text = ?`, id)
	var exists int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM `+r.table+` r`+filter.Where()+` LIMIT 1`, filter.Args()...).Scan(&exists)
	return err == nil
}

// FindByReqKey 按需求编号查详情（含附件）；必须先 ScopeFilter 再追加编号条件。
// 非本公司的编号与不存在的编号同为 404，不泄漏资源存在性。
func (r *Repository) FindByReqKey(ctx context.Context, reqKey string, scope contract.Scope) (contract.RequirementDetail, error) {
	filter := repository.ScopeFilter(0, "r", scope).Add(`r.req_key = ?`, reqKey)
	item, err := scanOne(ctx, r.db, r.baseSelect()+filter.Where(), filter.Args()...)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return contract.RequirementDetail{}, contract.NotFound(contract.ResourceRequirement)
	}
	if err != nil {
		return contract.RequirementDetail{}, err
	}
	attachments, err := r.ListAttachments(ctx, item.ID)
	if err != nil {
		return contract.RequirementDetail{}, err
	}
	return contract.RequirementDetail{Requirement: item, Attachments: attachments}, nil
}

// ListAttachments 按需求 id 返回图片列表（按创建时间升序），无图片时返回非 nil 空切片。
// 需求 id 来自已通过 scope 校验的详情查询，因此这里不必再叠租户条件。
func (r *Repository) ListAttachments(ctx context.Context, requirementID string) ([]contract.RequirementAttachment, error) {
	query := `SELECT a.id::text, a.file_name, a.content_type, a.file_size, a.created_at
FROM ` + r.attachmentsTable + ` a
WHERE a.requirement_id = $1::uuid ORDER BY a.created_at, a.id`
	rows, err := r.db.QueryContext(ctx, query, requirementID)
	if err != nil {
		return nil, fmt.Errorf("查询需求图片失败: %w", err)
	}
	defer rows.Close()

	attachments := make([]contract.RequirementAttachment, 0)
	for rows.Next() {
		attachment, err := scanAttachment(rows, false)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历需求图片失败: %w", err)
	}
	return attachments, nil
}

// FindAttachmentByID 按附件 id 读取（JOIN 需求后按 scope 过滤），供受保护的图片访问接口使用。
// 跨租户 / 不存在的附件统一转 404；返回的 RelativePath 只给 service 经 SafeResolve 解析，不出 DTO。
func (r *Repository) FindAttachmentByID(ctx context.Context, id string, scope contract.Scope) (contract.RequirementAttachment, error) {
	filter := repository.ScopeFilter(0, "r", scope).Add(`a.id::text = ?`, id)
	query := `SELECT a.id::text, a.file_name, a.content_type, a.file_size, a.created_at, a.relative_path
FROM ` + r.attachmentsTable + ` a
JOIN ` + r.table + ` r ON r.id = a.requirement_id` + filter.Where()
	attachment, err := scanAttachment(r.db.QueryRowContext(ctx, query, filter.Args()...), true)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return contract.RequirementAttachment{}, contract.NotFound(contract.ResourceRequirementAttachment)
	}
	if err != nil {
		return contract.RequirementAttachment{}, err
	}
	return attachment, nil
}

// DeleteByID 按 id 删除需求（附件行由外键级联删除），仅供搬文件失败时补偿使用。
// 只删本请求刚建的那一条；删不到也算补偿完成，绝不误伤他人数据。
func (r *Repository) DeleteByID(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM `+r.table+` WHERE id::text = $1`, id)
	if err != nil {
		return fmt.Errorf("补偿删除需求失败: %w", err)
	}
	if err := repository.EnsureDeleted(result, contract.ResourceRequirement); err != nil &&
		!errors.Is(err, contract.ErrNotFound) {
		return err
	}
	return nil
}

// rowQuerier 让单行查询既能走 *sql.DB（列表/详情）也能走 *sql.Tx（创建回读）。
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// scanOne 执行一次单行查询并按统一列序映射成需求。
func scanOne(ctx context.Context, q rowQuerier, statement string, args ...any) (contract.Requirement, error) {
	return scan(q.QueryRowContext(ctx, statement, args...))
}

// scanner 让单行与多行查询共用同一份列映射。
type scanner interface {
	Scan(dest ...any) error
}

// scan 把一行映射成需求；created_at 统一转成 UTC 的 RFC3339 文本。
func scan(row scanner) (contract.Requirement, error) {
	var item contract.Requirement
	var createdAt time.Time
	if err := row.Scan(&item.ID, &item.ReqKey, &item.Title, &item.Description, &item.Status,
		&item.AcceptanceCriteria, &item.ProjectID, &item.ProjectName,
		&item.CreatorUserID, &item.CreatorName, &createdAt); err != nil {
		return contract.Requirement{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return item, nil
}

// scanAttachment 映射附件行；withPath 为真时一并读出内部存储路径（图片访问用），否则只读对外字段。
func scanAttachment(row scanner, withPath bool) (contract.RequirementAttachment, error) {
	var attachment contract.RequirementAttachment
	var createdAt time.Time
	var err error
	if withPath {
		err = row.Scan(&attachment.ID, &attachment.FileName, &attachment.ContentType,
			&attachment.FileSize, &createdAt, &attachment.RelativePath)
	} else {
		err = row.Scan(&attachment.ID, &attachment.FileName, &attachment.ContentType,
			&attachment.FileSize, &createdAt)
	}
	if err != nil {
		return contract.RequirementAttachment{}, err
	}
	attachment.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return attachment, nil
}

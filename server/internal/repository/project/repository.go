// Package project · 项目的持久化。
// 职责：读写 projects 表，以及「项目 ↔ 人员 / 团队」关联表的读取。
// 输入：连接池；输出：contract.Project / 关联 ID 列表。
// 边界：不做 key 与 URL 校验（在 service/project）、不做权限判断、不写人员/团队表。
package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
)

// Repository 是 projects 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

const columns = `id::text, key, name, description, repo_url, created_at`

// List 返回指定页的项目与总数；PageSize=0 表示全量（下拉选项、守卫回退用）。
func (r *Repository) List(ctx context.Context, page contract.PageQuery) ([]contract.Project, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM projects`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计项目数量失败: %w", err)
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+columns+` FROM projects ORDER BY created_at ASC LIMIT $1 OFFSET $2`,
		page.Limit(), page.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("查询项目列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Project, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历项目列表失败: %w", err)
	}
	return items, total, nil
}

// FindByKey 按 key 查询；无记录统一转 contract.ErrProjectInvalid，供错误中间件转 400。
func (r *Repository) FindByKey(ctx context.Context, key string) (contract.Project, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM projects WHERE key = $1`, key)
	item, err := scan(row)
	if err != nil {
		return contract.Project{}, translate(err)
	}
	return item, nil
}

// Create 插入并回填创建时间；key 冲突由唯一索引拒绝，service 负责给出中文提示。
func (r *Repository) Create(ctx context.Context, in contract.ProjectInput) (contract.Project, error) {
	const query = `INSERT INTO projects (key, name, description, repo_url)
	VALUES ($1, $2, $3, $4)
	RETURNING ` + columns

	row := r.db.QueryRowContext(ctx, query, in.Key, in.Name, in.Description, in.RepoURL)
	item, err := scan(row)
	if err != nil {
		return contract.Project{}, err
	}
	return item, nil
}

// Update 按 key 改名称 / 描述 / 库地址；key 是 URL 段，不允许改。
func (r *Repository) Update(ctx context.Context, key string, in contract.ProjectInput) (contract.Project, error) {
	const query = `UPDATE projects SET name = $2, description = $3, repo_url = $4
	WHERE key = $1
	RETURNING ` + columns

	row := r.db.QueryRowContext(ctx, query, key, in.Name, in.Description, in.RepoURL)
	item, err := scan(row)
	if err != nil {
		return contract.Project{}, translate(err)
	}
	return item, nil
}

// ListPersons 返回该项目关联的人员（带名字），详情页直接渲染，不必再拉一次人员列表。
func (r *Repository) ListPersons(ctx context.Context, projectID string) ([]contract.RelationRef, error) {
	const query = `SELECT p.id::text, p.name
FROM project_members pm
JOIN people p ON p.id = pm.person_id
WHERE pm.project_id = $1
ORDER BY p.name`
	return r.listRefs(ctx, query, projectID)
}

// ListTeams 返回该项目关联的团队（带名字）。
func (r *Repository) ListTeams(ctx context.Context, projectID string) ([]contract.RelationRef, error) {
	const query = `SELECT t.id::text, t.name
FROM team_projects tp
JOIN teams t ON t.id = tp.team_id
WHERE tp.project_id = $1
ORDER BY t.name`
	return r.listRefs(ctx, query, projectID)
}

func (r *Repository) listRefs(ctx context.Context, query string, projectID string) ([]contract.RelationRef, error) {
	rows, err := r.db.QueryContext(ctx, query, projectID)
	if err != nil {
		return nil, fmt.Errorf("查询项目关联失败: %w", err)
	}
	defer rows.Close()

	refs := make([]contract.RelationRef, 0)
	for rows.Next() {
		var ref contract.RelationRef
		if err := rows.Scan(&ref.ID, &ref.Name); err != nil {
			return nil, fmt.Errorf("读取项目关联失败: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历项目关联失败: %w", err)
	}
	return refs, nil
}

// translate 把「无记录」统一成 contract.ErrProjectInvalid（400 而非 404：
// 项目 key 通常来自 URL 或表单，属于参数问题）。
func translate(err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return contract.ErrProjectInvalid
	}
	return err
}

// scanner 让单行与多行查询共用同一份扫描逻辑。
type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (contract.Project, error) {
	var item contract.Project
	var createdAt time.Time
	if err := row.Scan(&item.ID, &item.Key, &item.Name, &item.Description, &item.RepoURL, &createdAt); err != nil {
		return contract.Project{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return item, nil
}

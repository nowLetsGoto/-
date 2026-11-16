// Package people · 人员的持久化。
// 职责：读写 people 表，以及「人员 ↔ 团队」「人员 ↔ 项目」两张关联表（以 person 为主体）。
// 输入：连接池；输出：contract.Person（关联带 id 与名字，前端不必再拉对方列表）。
// 边界：不做字段校验（在 service/people）、不做权限判断；
//       团队侧对同一张 team_members 的写入在 repository/teams 里，各自只动自己那一侧。
package people

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
)

// Repository 是 people 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

const columns = `id::text, name, coalesce(username, ''), email, phone, title, created_at`

// 关联查询统一从"人员侧"出发，JOIN 出名字；两张表结构对称，抽成常量避免抄错列顺序。
const (
	teamLinksSQL = `SELECT tm.person_id::text, t.id::text, t.name
FROM team_members tm
JOIN teams t ON t.id = tm.team_id`
	projectLinksSQL = `SELECT pm.person_id::text, p.id::text, p.name
FROM project_members pm
JOIN projects p ON p.id = pm.project_id`
)

// List 返回全部人员，并一次性补齐关联的团队与项目（三查，不做 N+1）。
func (r *Repository) List(ctx context.Context) ([]contract.Person, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+columns+` FROM people ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询人员列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Person, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历人员列表失败: %w", err)
	}

	teams, err := r.grouped(ctx, teamLinksSQL)
	if err != nil {
		return nil, err
	}
	projects, err := r.grouped(ctx, projectLinksSQL)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Teams = fill(teams[items[i].ID])
		items[i].Projects = fill(projects[items[i].ID])
	}
	return items, nil
}

// FindByID 按 id 查人员（含关联）；无记录统一转 contract.ErrNotFound。
func (r *Repository) FindByID(ctx context.Context, id string) (contract.Person, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM people WHERE id = $1`, id)
	person, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Person{}, contract.ErrNotFound
		}
		return contract.Person{}, err
	}
	teams, projects, err := r.relations(ctx, person.ID)
	if err != nil {
		return contract.Person{}, err
	}
	person.Teams = teams
	person.Projects = projects
	return person, nil
}

// FindByUsername 按登录名查人员；登录账号没有对应人员时返回 contract.ErrNotFound。
func (r *Repository) FindByUsername(ctx context.Context, username string) (contract.Person, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM people WHERE username = $1`, username)
	person, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Person{}, contract.ErrNotFound
		}
		return contract.Person{}, err
	}
	teams, projects, err := r.relations(ctx, person.ID)
	if err != nil {
		return contract.Person{}, err
	}
	person.Teams = teams
	person.Projects = projects
	return person, nil
}

// ListProjectKeys 返回该人员关联的项目 key（按项目创建顺序），供「默认项目」使用。
func (r *Repository) ListProjectKeys(ctx context.Context, personID string) ([]string, error) {
	const query = `SELECT p.key
FROM project_members pm
JOIN projects p ON p.id = pm.project_id
WHERE pm.person_id = $1
ORDER BY p.created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, personID)
	if err != nil {
		return nil, fmt.Errorf("查询人员关联项目失败: %w", err)
	}
	defer rows.Close()

	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("读取人员关联项目失败: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历人员关联项目失败: %w", err)
	}
	return keys, nil
}

// Create 新建人员并写入关联；关联与主记录在同一个事务里，避免出现半截数据。
func (r *Repository) Create(ctx context.Context, in contract.PersonInput) (contract.Person, error) {
	return r.save(ctx, "", in)
}

// Update 全量覆盖人员字段与关联（前端每次提交完整的多选结果）。
func (r *Repository) Update(ctx context.Context, id string, in contract.PersonInput) (contract.Person, error) {
	return r.save(ctx, id, in)
}

func (r *Repository) save(ctx context.Context, id string, in contract.PersonInput) (contract.Person, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Person{}, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var row *sql.Row
	if id == "" {
		// NULLIF 把空登录名落成 NULL：username 上有唯一索引，多个空串会互相冲突。
		row = tx.QueryRowContext(ctx,
			`INSERT INTO people (name, username, email, phone, title)
			 VALUES ($1, NULLIF($2, ''), $3, $4, $5)
			 RETURNING `+columns,
			in.Name, in.Username, in.Email, in.Phone, in.Title)
	} else {
		row = tx.QueryRowContext(ctx,
			`UPDATE people
			 SET name = $2, username = NULLIF($3, ''), email = $4, phone = $5, title = $6
			 WHERE id = $1
			 RETURNING `+columns,
			id, in.Name, in.Username, in.Email, in.Phone, in.Title)
	}

	person, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Person{}, contract.ErrNotFound
		}
		return contract.Person{}, err
	}
	if err := replaceLinks(ctx, tx, person.ID, in.TeamIDs, in.ProjectIDs); err != nil {
		return contract.Person{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Person{}, fmt.Errorf("提交人员变更失败: %w", err)
	}

	// 关联要带名字返回，所以提交后按 id 重查一次（只在写路径发生）。
	return r.FindByID(ctx, person.ID)
}

// replaceLinks 先清后写，语义是「以本次提交为准」。
func replaceLinks(ctx context.Context, tx *sql.Tx, personID string, teamIDs, projectIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE person_id = $1`, personID); err != nil {
		return fmt.Errorf("清理人员团队关联失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE person_id = $1`, personID); err != nil {
		return fmt.Errorf("清理人员项目关联失败: %w", err)
	}
	for _, teamID := range teamIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO team_members (team_id, person_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			teamID, personID); err != nil {
			return fmt.Errorf("写入人员团队关联失败: %w", err)
		}
	}
	for _, projectID := range projectIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_members (project_id, person_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			projectID, personID); err != nil {
			return fmt.Errorf("写入人员项目关联失败: %w", err)
		}
	}
	return nil
}

// relations 查单个人员的两组关联。
func (r *Repository) relations(ctx context.Context, personID string) (teams []contract.RelationRef, projects []contract.RelationRef, err error) {
	teamMap, err := r.grouped(ctx, teamLinksSQL+` WHERE tm.person_id = $1`, personID)
	if err != nil {
		return nil, nil, err
	}
	projectMap, err := r.grouped(ctx, projectLinksSQL+` WHERE pm.person_id = $1`, personID)
	if err != nil {
		return nil, nil, err
	}
	return fill(teamMap[personID]), fill(projectMap[personID]), nil
}

// grouped 把「属主 ID → 关联(id,name) 列表」聚成 map；带可选参数便于复用同一段扫描。
func (r *Repository) grouped(ctx context.Context, query string, args ...any) (map[string][]contract.RelationRef, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询人员关联失败: %w", err)
	}
	defer rows.Close()

	grouped := make(map[string][]contract.RelationRef)
	for rows.Next() {
		var owner string
		var ref contract.RelationRef
		if err := rows.Scan(&owner, &ref.ID, &ref.Name); err != nil {
			return nil, fmt.Errorf("读取人员关联失败: %w", err)
		}
		grouped[owner] = append(grouped[owner], ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历人员关联失败: %w", err)
	}
	return grouped, nil
}

// fill 保证 JSON 里是 [] 而不是 null。
func fill(refs []contract.RelationRef) []contract.RelationRef {
	if refs == nil {
		return []contract.RelationRef{}
	}
	return refs
}

// scanner 让单行与多行查询共用同一份扫描逻辑。
type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (contract.Person, error) {
	var item contract.Person
	var createdAt time.Time
	if err := row.Scan(&item.ID, &item.Name, &item.Username, &item.Email, &item.Phone, &item.Title, &createdAt); err != nil {
		return contract.Person{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	item.Teams = []contract.RelationRef{}
	item.Projects = []contract.RelationRef{}
	return item, nil
}

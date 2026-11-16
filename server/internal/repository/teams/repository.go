// Package teams · 团队的持久化。
// 职责：读写 teams 表，以及「团队 ↔ 人员」「团队 ↔ 项目」两张关联表（以 team 为主体）。
// 输入：连接池；输出：contract.Team（关联带 id 与名字，前端不必再拉对方列表）。
// 边界：不做字段校验（在 service/teams）、不做权限判断；
//       人员侧对同一张 team_members 的写入在 repository/people 里，各自只动自己那一侧。
package teams

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
)

// Repository 是 teams 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

const columns = `id::text, name, description, created_at`

// 关联查询统一从"团队侧"出发，JOIN 出名字。
const (
	personLinksSQL = `SELECT tm.team_id::text, p.id::text, p.name
FROM team_members tm
JOIN people p ON p.id = tm.person_id`
	projectLinksSQL = `SELECT tp.team_id::text, p.id::text, p.name
FROM team_projects tp
JOIN projects p ON p.id = tp.project_id`
)

// List 返回全部团队，并一次性补齐关联的人员与项目（三查，不做 N+1）。
func (r *Repository) List(ctx context.Context) ([]contract.Team, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+columns+` FROM teams ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询团队列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Team, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历团队列表失败: %w", err)
	}

	persons, err := r.grouped(ctx, personLinksSQL)
	if err != nil {
		return nil, err
	}
	projects, err := r.grouped(ctx, projectLinksSQL)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Persons = fill(persons[items[i].ID])
		items[i].Projects = fill(projects[items[i].ID])
	}
	return items, nil
}

// FindByID 按 id 查团队（含关联）；无记录统一转 contract.ErrNotFound。
func (r *Repository) FindByID(ctx context.Context, id string) (contract.Team, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM teams WHERE id = $1`, id)
	team, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Team{}, contract.ErrNotFound
		}
		return contract.Team{}, err
	}
	persons, projects, err := r.relations(ctx, team.ID)
	if err != nil {
		return contract.Team{}, err
	}
	team.Persons = persons
	team.Projects = projects
	return team, nil
}

// Create 新建团队并写入关联；关联与主记录在同一个事务里。
func (r *Repository) Create(ctx context.Context, in contract.TeamInput) (contract.Team, error) {
	return r.save(ctx, "", in)
}

// Update 全量覆盖团队字段与关联（前端每次提交完整的多选结果）。
func (r *Repository) Update(ctx context.Context, id string, in contract.TeamInput) (contract.Team, error) {
	return r.save(ctx, id, in)
}

func (r *Repository) save(ctx context.Context, id string, in contract.TeamInput) (contract.Team, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Team{}, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var row *sql.Row
	if id == "" {
		row = tx.QueryRowContext(ctx,
			`INSERT INTO teams (name, description) VALUES ($1, $2) RETURNING `+columns,
			in.Name, in.Description)
	} else {
		row = tx.QueryRowContext(ctx,
			`UPDATE teams SET name = $2, description = $3 WHERE id = $1 RETURNING `+columns,
			id, in.Name, in.Description)
	}

	team, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Team{}, contract.ErrNotFound
		}
		return contract.Team{}, err
	}
	if err := replaceLinks(ctx, tx, team.ID, in.PersonIDs, in.ProjectIDs); err != nil {
		return contract.Team{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Team{}, fmt.Errorf("提交团队变更失败: %w", err)
	}

	// 关联要带名字返回，所以提交后按 id 重查一次（只在写路径发生）。
	return r.FindByID(ctx, team.ID)
}

// replaceLinks 先清后写，语义是「以本次提交为准」。
func replaceLinks(ctx context.Context, tx *sql.Tx, teamID string, personIDs, projectIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = $1`, teamID); err != nil {
		return fmt.Errorf("清理团队成员关联失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_projects WHERE team_id = $1`, teamID); err != nil {
		return fmt.Errorf("清理团队项目关联失败: %w", err)
	}
	for _, personID := range personIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO team_members (team_id, person_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			teamID, personID); err != nil {
			return fmt.Errorf("写入团队成员关联失败: %w", err)
		}
	}
	for _, projectID := range projectIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO team_projects (team_id, project_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			teamID, projectID); err != nil {
			return fmt.Errorf("写入团队项目关联失败: %w", err)
		}
	}
	return nil
}

// relations 查单个团队的两组关联。
func (r *Repository) relations(ctx context.Context, teamID string) (persons []contract.RelationRef, projects []contract.RelationRef, err error) {
	personMap, err := r.grouped(ctx, personLinksSQL+` WHERE tm.team_id = $1`, teamID)
	if err != nil {
		return nil, nil, err
	}
	projectMap, err := r.grouped(ctx, projectLinksSQL+` WHERE tp.team_id = $1`, teamID)
	if err != nil {
		return nil, nil, err
	}
	return fill(personMap[teamID]), fill(projectMap[teamID]), nil
}

// grouped 把「属主 ID → 关联(id,name) 列表」聚成 map；带可选参数便于复用同一段扫描。
func (r *Repository) grouped(ctx context.Context, query string, args ...any) (map[string][]contract.RelationRef, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询团队关联失败: %w", err)
	}
	defer rows.Close()

	grouped := make(map[string][]contract.RelationRef)
	for rows.Next() {
		var owner string
		var ref contract.RelationRef
		if err := rows.Scan(&owner, &ref.ID, &ref.Name); err != nil {
			return nil, fmt.Errorf("读取团队关联失败: %w", err)
		}
		grouped[owner] = append(grouped[owner], ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历团队关联失败: %w", err)
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

func scan(row scanner) (contract.Team, error) {
	var item contract.Team
	var createdAt time.Time
	if err := row.Scan(&item.ID, &item.Name, &item.Description, &createdAt); err != nil {
		return contract.Team{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	item.Persons = []contract.RelationRef{}
	item.Projects = []contract.RelationRef{}
	return item, nil
}

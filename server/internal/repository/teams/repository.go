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
	"dandelion/server/internal/repository"
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
// 项目关联只存在于团队分区，因此显式写 team.projects（分表后不再有 public.projects）。
const (
	personLinksSQL = `SELECT tm.team_id::text, p.id::text, p.name
FROM team_members tm
JOIN people p ON p.id = tm.person_id`
	projectLinksSQL = `SELECT tp.team_id::text, p.id::text, p.name
FROM team_projects tp
JOIN team.projects p ON p.id = tp.project_id`
)

// AdminTeamIDs 返回 team_admin 账号在 team_admins 中关联的全部团队 id。
// 只做单列读取与行映射；"该账号是不是 team_admin"由 authz.Service 判断，这里不重复判定。
func (r *Repository) AdminTeamIDs(ctx context.Context, userID string) ([]string, error) {
	return r.teamIDs(ctx, `SELECT team_id::text FROM team_admins WHERE user_id = $1 ORDER BY team_id`, userID)
}

// MemberTeamIDs 返回人员在 team_members 中关联的全部团队 id。
// 参数是 person_id 而不是 user_id：成员关系的真相在人员档案上，
// 账号到人员的映射由 authz.Service.PersonID 统一负责（显式绑定优先，兼容登录名回退）。
func (r *Repository) MemberTeamIDs(ctx context.Context, personID string) ([]string, error) {
	return r.teamIDs(ctx, `SELECT team_id::text FROM team_members WHERE person_id = $1 ORDER BY team_id`, personID)
}

// teamIDs 把"单列 id 查询"收敛成一份扫描逻辑。
// 排序让 scope 稳定可断言；空结果返回空切片而非 nil，保证 JSON 里是 []。
func (r *Repository) teamIDs(ctx context.Context, query, arg string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("查询账号关联团队失败: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取关联团队 id 失败: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历关联团队失败: %w", err)
	}
	return ids, nil
}

// List 返回指定页的团队，并补齐该页的关联（共三查，不做 N+1）；同时返回总数供分页。
// scope 是可见性边界：公司账号只看本公司的团队，超管（All）看全部。
func (r *Repository) List(ctx context.Context, page contract.PageQuery, scope contract.Scope) ([]contract.Team, int, error) {
	filter := repository.ScopeFilter(0, "t", scope)

	var total int
	countSQL := `SELECT count(*) FROM teams t` + filter.Where()
	if err := r.db.QueryRowContext(ctx, countSQL, filter.Args()...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计团队数量失败: %w", err)
	}

	args := append(filter.Args(), page.Limit(), page.Offset())
	query := `SELECT id::text, name, description, created_at FROM teams t` + filter.Where() +
		fmt.Sprintf(` ORDER BY t.created_at ASC LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询团队列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Team, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历团队列表失败: %w", err)
	}

	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	persons, err := r.grouped(ctx, personLinksSQL+` WHERE tm.team_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, 0, err
	}
	projects, err := r.grouped(ctx, projectLinksSQL+` WHERE tp.team_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		items[i].Persons = fill(persons[items[i].ID])
		items[i].Projects = fill(projects[items[i].ID])
	}
	return items, total, nil
}

// FindByID 按 id 查团队（含关联）；无记录统一转 contract.ErrNotFound。
// scope 限定可见范围：查本公司之外的团队与「不存在」同口径。
func (r *Repository) FindByID(ctx context.Context, id string, scope contract.Scope) (contract.Team, error) {
	filter := repository.ScopeFilter(0, "t", scope).Add(`t.id = ?`, id)
	row := r.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM teams t`+filter.Where(), filter.Args()...)
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

// Create 新建团队并写入关联；归属公司从 scope 取。
func (r *Repository) Create(ctx context.Context, in contract.TeamInput, scope contract.Scope) (contract.Team, error) {
	return r.save(ctx, "", in, scope)
}

// Update 全量覆盖团队字段与关联；scope 限定只能改本公司的团队。
func (r *Repository) Update(ctx context.Context, id string, in contract.TeamInput, scope contract.Scope) (contract.Team, error) {
	return r.save(ctx, id, in, scope)
}

func (r *Repository) save(ctx context.Context, id string, in contract.TeamInput, scope contract.Scope) (contract.Team, error) {
	if scope.TenantID == "" {
		return contract.Team{}, contract.ErrForbidden
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Team{}, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var row *sql.Row
	if id == "" {
		// tenant_id 从 scope 取，请求体不接受该字段。
		row = tx.QueryRowContext(ctx,
			`INSERT INTO teams (name, description, tenant_id) VALUES ($1, $2, $3) RETURNING `+columns,
			in.Name, in.Description, scope.TenantID)
	} else {
		// SET 占 $1..$2，filter 从 base=2 起算：$3=tenant_id、$4=id。
		filter := repository.ScopeFilter(2, "teams", scope).Add(`teams.id = ?`, id)
		row = tx.QueryRowContext(ctx,
			`UPDATE teams SET name = $1, description = $2 WHERE `+filter.Cond()+` RETURNING `+columns,
			in.Name, in.Description, scope.TenantID, id)
	}

	team, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Team{}, contract.ErrNotFound
		}
		return contract.Team{}, err
	}
	if err := replaceLinks(ctx, tx, team.ID, in.PersonIDs, in.ProjectIDs, scope); err != nil {
		return contract.Team{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Team{}, fmt.Errorf("提交团队变更失败: %w", err)
	}

	// 关联要带名字返回，所以提交后按 id 重查一次（只在写路径发生）。
	return r.FindByID(ctx, team.ID, scope)
}

// replaceLinks 先清后写，语义是「以本次提交为准」。
// 关联对象必须同属一个租户：人员按 tenant_id、项目按 tenant_id 过滤；
// team_admin 账号（user_id）本身不带 tenant 列，其归属由上层账号管理保证（P2）。
func replaceLinks(ctx context.Context, tx *sql.Tx, teamID string, personIDs, projectIDs []string, scope contract.Scope) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = $1`, teamID); err != nil {
		return fmt.Errorf("清理团队成员关联失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_projects WHERE team_id = $1`, teamID); err != nil {
		return fmt.Errorf("清理团队项目关联失败: %w", err)
	}
	for _, personID := range personIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO team_members (team_id, person_id)
			 SELECT $1::uuid, id FROM people WHERE id = $2::uuid AND tenant_id = $3::uuid
			 ON CONFLICT DO NOTHING`,
			teamID, personID, scope.TenantID); err != nil {
			return fmt.Errorf("写入团队成员关联失败: %w", err)
		}
	}
	for _, projectID := range projectIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO team_projects (team_id, project_id)
			 SELECT $1::uuid, id FROM team.projects WHERE id = $2::uuid AND tenant_id = $3::uuid
			 ON CONFLICT DO NOTHING`,
			teamID, projectID, scope.TenantID); err != nil {
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

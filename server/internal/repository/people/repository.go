// Package people · 人员的持久化。
// 职责：读写 people 表，以及「人员 ↔ 团队」「人员 ↔ 项目」两张关联表（以 person 为主体）。
// 输入：连接池；输出：contract.Person（关联带 id 与名字，前端不必再拉对方列表）。
// 边界：不做字段校验（在 service/people）、不做权限判断；
//
//	团队侧对同一张 team_members 的写入在 repository/teams 里，各自只动自己那一侧。
package people

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

// Repository 是 people 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// selectColumns 带 LEFT JOIN 取角色名：列表要显示"什么角色"，一次查完不做 N+1。
const selectColumns = `p.id::text, p.name, coalesce(p.username, ''), p.email, p.phone, p.title,
coalesce(p.role_key, ''), coalesce(r.name, ''), p.created_at`

const fromClause = ` FROM people p LEFT JOIN roles r ON r.key = p.role_key`

// 关联查询统一从"人员侧"出发，JOIN 出名字；两张表结构对称，抽成常量避免抄错列顺序。
// 项目关联只存在于团队分区，因此显式写 team.projects（分表后不再有 public.projects）。
const (
	teamLinksSQL = `SELECT tm.person_id::text, t.id::text, t.name
FROM team_members tm
JOIN teams t ON t.id = tm.team_id`
	projectLinksSQL = `SELECT pm.person_id::text, p.id::text, p.name
FROM project_members pm
JOIN team.projects p ON p.id = pm.project_id`
)

// List 返回指定页的人员，并补齐该页的关联（共三查，不做 N+1）；同时返回总数供分页。
// scope 是数据可见性的唯一来源：公司的列表只能看到本公司人员，超管（All）看全部。
// 条件拼装顺序固定：租户 → 业务筛选，配合 (tenant_id, created_at) 索引。
func (r *Repository) List(ctx context.Context, page contract.PageQuery, scope contract.Scope) ([]contract.Person, int, error) {
	filter := repository.ScopeFilter(0, "p", scope)
	// 团队级边界：team_admin / member 只能看关联团队的人员；平台管理员 TeamIDs 为空不受限。
	repository.ApplyTeam(filter, `p.id IN (SELECT tm.person_id FROM team_members tm WHERE tm.team_id = ANY(?))`, scope)

	var total int
	countSQL := `SELECT count(*) FROM people p` + filter.Where()
	if err := r.db.QueryRowContext(ctx, countSQL, filter.Args()...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计人员数量失败: %w", err)
	}

	args := append(filter.Args(), page.Limit(), page.Offset())
	query := `SELECT ` + selectColumns + fromClause + filter.Where() +
		fmt.Sprintf(` ORDER BY p.created_at ASC LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询人员列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Person, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历人员列表失败: %w", err)
	}

	// 只查这一页涉及的关联，避免翻到第 1 页却扫了全表的关系行。
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	teams, err := r.grouped(ctx, teamLinksSQL+` WHERE tm.person_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, 0, err
	}
	projects, err := r.grouped(ctx, projectLinksSQL+` WHERE pm.person_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		items[i].Teams = fill(teams[items[i].ID])
		items[i].Projects = fill(projects[items[i].ID])
	}
	return items, total, nil
}

// FindByID 按 id 查人员（含关联）；无记录统一转 contract.ErrNotFound。
// scope 必须是调用者的可见范围：查不到本公司之外的人，与「不存在」同一口径，
// 避免借 id 探测其它公司是否有人。
func (r *Repository) FindByID(ctx context.Context, id string, scope contract.Scope) (contract.Person, error) {
	filter := repository.ScopeFilter(0, "p", scope).Add(`p.id = ?`, id)
	repository.ApplyTeam(filter, `p.id IN (SELECT tm.person_id FROM team_members tm WHERE tm.team_id = ANY(?))`, scope)
	row := r.db.QueryRowContext(ctx, `SELECT `+selectColumns+fromClause+filter.Where(), filter.Args()...)
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
// 身份解析路径（authz.PersonID）会显式传 All：登录时还没算出 scope，
// 且「账号名对应谁」是身份事实，不受租户视图约束——查到后再由业务接口按 scope 过滤。
func (r *Repository) FindByUsername(ctx context.Context, username string, scope contract.Scope) (contract.Person, error) {
	filter := repository.ScopeFilter(0, "p", scope).Add(`p.username = ?`, username)
	row := r.db.QueryRowContext(ctx, `SELECT `+selectColumns+fromClause+filter.Where(), filter.Args()...)
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
// 同样是团队分区：单人空间没有成员关系。
func (r *Repository) ListProjectKeys(ctx context.Context, personID string) ([]string, error) {
	const query = `SELECT p.key
FROM project_members pm
JOIN team.projects p ON p.id = pm.project_id
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

// Create 新建人员并写入关联；归属公司从 scope 取，不接受请求指定。
func (r *Repository) Create(ctx context.Context, in contract.PersonInput, scope contract.Scope) (contract.Person, error) {
	return r.save(ctx, "", in, scope)
}

// Update 全量覆盖人员字段与关联（前端每次提交完整的多选结果）。
// scope 同时限定「只能改本公司的人」：WHERE 带 tenant_id，越界更新命中 0 行转 404。
func (r *Repository) Update(ctx context.Context, id string, in contract.PersonInput, scope contract.Scope) (contract.Person, error) {
	return r.save(ctx, id, in, scope)
}

func (r *Repository) save(ctx context.Context, id string, in contract.PersonInput, scope contract.Scope) (contract.Person, error) {
	// 写入必须有明确归属：无公司（超管平台视角或漏注 scope）都不允许建出「无主」人员，
	// 否则它不属于任何租户、谁都看不到。业务数据的创建归公司账号，超管不直接建（裁决 3）。
	if scope.TenantID == "" {
		return contract.Person{}, contract.ErrForbidden
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Person{}, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var row *sql.Row
	if id == "" {
		// NULLIF 把空登录名落成 NULL：username 上有唯一索引，多个空串会互相冲突。
		// tenant_id 从 scope 取——请求体里没有这个字段，调用方无法伪造归属。
		row = tx.QueryRowContext(ctx,
			`INSERT INTO people (name, username, email, phone, title, role_key, tenant_id)
			 VALUES ($1, NULLIF($2, ''), $3, $4, $5, NULLIF($6, ''), $7)
			 RETURNING id::text`,
			in.Name, in.Username, in.Email, in.Phone, in.Title, in.RoleKey, scope.TenantID)
	} else {
		// UPDATE 同样带 tenant_id 条件：只能改本公司的人。
		// SET 占 $1..$6，filter 从 base=6 起算：$7=tenant_id、$8=id，与参数顺序一一对应。
		filter := repository.ScopeFilter(6, "people", scope).Add(`people.id = ?`, id)
		row = tx.QueryRowContext(ctx,
			`UPDATE people
			 SET name = $1, username = NULLIF($2, ''), email = $3, phone = $4, title = $5,
			     role_key = NULLIF($6, '')
			 WHERE `+filter.Cond()+` RETURNING id::text`,
			in.Name, in.Username, in.Email, in.Phone, in.Title, in.RoleKey, scope.TenantID, id)
	}

	var personID string
	if err := row.Scan(&personID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Person{}, contract.ErrNotFound
		}
		return contract.Person{}, err
	}
	// 关联只能指向本公司对象：INSERT ... SELECT 带 tenant_id 条件，
	// 跨租户的 team_id / project_id 会命中 0 行，被 ON CONFLICT DO NOTHING 静默丢弃。
	// 与其报错泄漏「那个 id 存在但不属于你」，不如直接不建立关系。
	if err := replaceLinks(ctx, tx, personID, in.TeamIDs, in.ProjectIDs, scope.TenantID); err != nil {
		return contract.Person{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Person{}, fmt.Errorf("提交人员变更失败: %w", err)
	}

	// 关联与角色名都要带出来，提交后按 id 重查一次（只在写路径发生）。
	return r.FindByID(ctx, personID, scope)
}

// replaceLinks 先清后写，语义是「以本次提交为准」。
// tenantID 非空时，关联对象必须同属该公司——跨租户关联会被 SELECT 的 WHERE 挡掉。
func replaceLinks(ctx context.Context, tx *sql.Tx, personID string, teamIDs, projectIDs []string, tenantID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE person_id = $1`, personID); err != nil {
		return fmt.Errorf("清理人员团队关联失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE person_id = $1`, personID); err != nil {
		return fmt.Errorf("清理人员项目关联失败: %w", err)
	}
	for _, teamID := range teamIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO team_members (team_id, person_id)
			 SELECT id, $2::uuid FROM teams WHERE id = $1::uuid AND tenant_id = $3::uuid
			 ON CONFLICT DO NOTHING`,
			teamID, personID, tenantID); err != nil {
			return fmt.Errorf("写入人员团队关联失败: %w", err)
		}
	}
	for _, projectID := range projectIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_members (project_id, person_id)
			 SELECT id, $2::uuid FROM team.projects WHERE id = $1::uuid AND tenant_id = $3::uuid
			 ON CONFLICT DO NOTHING`,
			projectID, personID, tenantID); err != nil {
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
	if err := row.Scan(&item.ID, &item.Name, &item.Username, &item.Email, &item.Phone, &item.Title,
		&item.RoleKey, &item.RoleName, &createdAt); err != nil {
		return contract.Person{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	item.Teams = []contract.RelationRef{}
	item.Projects = []contract.RelationRef{}
	return item, nil
}

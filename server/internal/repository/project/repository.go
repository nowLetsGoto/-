// Package project · 项目的持久化。
// 职责：读写分区内的 projects 表，以及「项目 ↔ 人员 / 团队」关联表的读取。
// 输入：连接池；输出：contract.Project / 关联 ID 列表。
// 边界：不做 key 与 URL 校验（在 service/project）、不做权限判断、不写人员/团队表；
// 协作关系（成员 / 团队）只存在于团队分区，单人分区不读写这两张表。
package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// Repository 是一个分区内 projects 表的唯一 SQL 出入口；分区在构造时固定。
type Repository struct {
	db    *sql.DB
	table string // 全限定表名，如 team.projects；只能来自 repository.Partition.Table
	// ownerScoped 为真表示该项目表按创建者隔离（单人分区）：
	// 所有读写都必须带上 owner_user_id 条件，一个人看不到另一个人的项目。
	// 这是两套同构表在行为上的唯一差异，因此用一个布尔量表达，而不是两套 SQL。
	ownerScoped bool
}

// NewPartitioned 为 team 与 solo 各建一个访问器，交给上层按会话模式取用。
func NewPartitioned(db *sql.DB) repository.Partitioned[*Repository] {
	return repository.NewPartitioned(
		newRepository(db, repository.PartitionTeam, false),
		newRepository(db, repository.PartitionSolo, true),
	)
}

func newRepository(db *sql.DB, schema repository.Schema, ownerScoped bool) *Repository {
	return &Repository{db: db, table: schema.Table("projects"), ownerScoped: ownerScoped}
}

// selectColumns 带 LEFT JOIN 取角色名：列表要显示"项目角色"，一次查完不做 N+1。
// 角色关联按 roles.id：key 不再是外键目标，允许公司内重名。
// solo.projects 也加了 role_id 列（恒为 NULL，单人模式不需要项目角色），
// 保留同构列是为了两套表共用同一份查询 SQL。
const selectColumns = `p.id::text, p.key, p.name, p.description, p.repo_url,
coalesce(p.icon_url, ''), coalesce(r.key, ''), coalesce(r.name, ''), coalesce(p.tenant_id::text, ''), p.created_at`

const fromClause = ` LEFT JOIN roles r ON r.id = p.role_id`

// List 返回指定页的项目与总数；page.PageSize=0 表示全量（下拉选项、守卫回退用）。
// ownerUserID 非空时只返回该账号创建的项目，用于单人视图的项目管理。
// scope 限定租户边界：单人分区靠 owner 隔离（tenant_id 恒空），团队分区按 tenant_id 过滤。
func (r *Repository) List(ctx context.Context, page contract.PageQuery, ownerUserID string, scope contract.Scope) ([]contract.Project, int, error) {
	filter := r.ownerFilter(0, ownerUserID)
	// 只有团队分区的租户隔离：solo 分区的 tenant_id 恒为空，加条件反而查不到。
	if !r.ownerScoped {
		repository.ApplyTenant(filter, "p", scope)
		// 团队管理员只看关联团队的项目；平台管理员与成员不受此限（见 ApplyTeam 注释）。
		repository.ApplyTeam(filter, `p.id IN (SELECT tp.project_id FROM team_projects tp WHERE tp.team_id = ANY(?))`, scope)
	}

	var total int
	countSQL := `SELECT count(*) FROM ` + r.table + ` p` + filter.Where()
	if err := r.db.QueryRowContext(ctx, countSQL, filter.Args()...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计项目数量失败: %w", err)
	}

	args := append(filter.Args(), page.Limit(), page.Offset())
	query := `SELECT ` + selectColumns + ` FROM ` + r.table + ` p` + fromClause + filter.Where() +
		fmt.Sprintf(` ORDER BY p.created_at ASC LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1))

	rows, err := r.db.QueryContext(ctx, query, args...)
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

// ExistsVisibleByID 判断 id 对应的项目是否存在且属于当前 scope 可见范围。
// 供研发中心需求池校验归属项目：项目 id 是用户选的业务字段，必须后端复核租户归属，
// 不能只信前端下拉。ScopeFilter 负责租户边界（超管 All 放行、无 scope 失败关闭）。
// 查询异常按「不可见」处理并记日志——失败关闭，绝不把数据库故障误判成校验通过。
func (r *Repository) ExistsVisibleByID(ctx context.Context, id string, scope contract.Scope) bool {
	if id == "" {
		return false
	}
	filter := repository.ScopeFilter(0, "p", scope).Add(`p.id::text = ?`, id)
	query := `SELECT EXISTS(SELECT 1 FROM ` + r.table + ` p` + filter.Where() + `)`
	var exists bool
	if err := r.db.QueryRowContext(ctx, query, filter.Args()...).Scan(&exists); err != nil {
		log.Printf("校验项目归属失败 id=%s err=%v", id, err)
		return false
	}
	return exists
}

// FindByKey 按 key 查询；ownerUserID 非空时限定在该账号的空间内。
// 无记录统一转 contract.ErrProjectInvalid，供错误中间件转 400。
func (r *Repository) FindByKey(ctx context.Context, key, ownerUserID string, scope contract.Scope) (contract.Project, error) {
	filter := r.ownerFilter(0, ownerUserID)
	if !r.ownerScoped {
		repository.ApplyTenant(filter, "p", scope)
		repository.ApplyTeam(filter, `p.id IN (SELECT tp.project_id FROM team_projects tp WHERE tp.team_id = ANY(?))`, scope)
	}
	filter.Add(`p.key = ?`, key)
	query := `SELECT ` + selectColumns + ` FROM ` + r.table + ` p` + fromClause + filter.Where()

	row := r.db.QueryRowContext(ctx, query, filter.Args()...)
	item, err := scan(row)
	if err != nil {
		return contract.Project{}, noRowsToInvalidProject(err)
	}
	return item, nil
}

// Create 插入并回填创建时间；key 冲突由唯一索引拒绝，service 负责给出中文提示。
// ownerUserID 写入 owner_user_id：单人分区靠它隔离不同账号的项目空间。
// 团队分区把 scope.TenantID 写入 tenant_id——归属从 scope 取，请求体不接受该字段。
func (r *Repository) Create(ctx context.Context, in contract.ProjectInput, ownerUserID string, scope contract.Scope) (contract.Project, error) {
	tenantID := ""
	if !r.ownerScoped {
		if scope.All || scope.TenantID == "" {
			return contract.Project{}, contract.ErrForbidden
		}
		tenantID = scope.TenantID
	}
	// role_id 由 $5（角色标识）在 SQL 内解析；$7 是归属公司，$8 是同一个公司的 uuid 形态。
	// 为什么拆两个参数：同一占位符同时用于写入 tenant_id 列（uuid）和子查询比较时，
	// PG 的类型推断会打架（报 "column tenant_id is of type uuid but expression is of type text"）。
	//
	// 两处都用 NULLIF(...)::uuid 而不是在 Go 侧传 nil：
	// 单人分区的 tenantID 是空串，直接传 '' 会被 uuid 转换拒绝（invalid input syntax for type uuid: ""）。
	// NULLIF 把空串先变 NULL，再转 uuid 才安全。
	query := `INSERT INTO ` + r.table + ` (key, name, description, repo_url, icon_url, role_id, owner_user_id, tenant_id)
	VALUES ($1, $2, $3, $4, $5,
	        (SELECT ro.id FROM roles ro
	         WHERE ro.key = $6 AND (ro.tenant_id IS NULL OR ro.tenant_id = NULLIF($9, '')::uuid)),
	        $7, NULLIF($8, '')::uuid)
	RETURNING key`

	var created string
	err := r.db.QueryRowContext(ctx, query,
		in.Key, in.Name, in.Description, in.RepoURL, in.IconURL, in.RoleKey, ownerUserID, tenantID, tenantID).Scan(&created)
	if err != nil {
		return contract.Project{}, err
	}
	return r.FindByKey(ctx, created, ownerUserID, scope)
}

// Update 按 key 改名称 / 描述 / 库地址；key 是 URL 段，不允许改。
// ownerUserID 非空时只有创建者本人能改，别人的项目会命中 0 行并转成参数错误。
// 团队分区叠加 tenant_id 条件：只能改本公司的项目。
func (r *Repository) Update(ctx context.Context, key, ownerUserID string, in contract.ProjectInput, scope contract.Scope) (contract.Project, error) {
	// SET 先占满 $1..$5，因此条件必须从 $6 起算（base=5）。
	// 表必须起别名 p：条件由 ownerFilter 统一生成，列引用一律带 p. 前缀。
	filter := r.ownerFilter(5, ownerUserID)
	if !r.ownerScoped {
		repository.ApplyTenant(filter, "p", scope)
		repository.ApplyTeam(filter, `p.id IN (SELECT tp.project_id FROM team_projects tp WHERE tp.team_id = ANY(?))`, scope)
	}
	filter.Add(`p.key = ?`, key)
	query := `UPDATE ` + r.table + ` p
	SET name = $1, description = $2, repo_url = $3, icon_url = $4,
	    role_id = (SELECT ro.id FROM roles ro
	               WHERE ro.key = $5 AND (ro.tenant_id IS NULL OR ro.tenant_id = p.tenant_id))
	WHERE ` + filter.Cond() + ` RETURNING p.key`

	args := append([]any{in.Name, in.Description, in.RepoURL, in.IconURL, in.RoleKey}, filter.Args()...)

	var updated string
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&updated); err != nil {
		return contract.Project{}, noRowsToInvalidProject(err)
	}
	return r.FindByKey(ctx, updated, ownerUserID, scope)
}

// ListPersons 返回该项目关联的人员（带名字），详情页直接渲染，不必再拉一次人员列表。
// 协作关系只存在于团队分区，因此这里只会被团队分区实例调用。
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

// Usage 统计项目被哪些数据引用，供删除前判断"能不能删"。
type Usage struct {
	// Builds 是应用包数量。包是**不可恢复的资产**（对应磁盘文件），
	// 因此有包就拒绝删除，绝不级联。
	Builds int
	// Members 是参与该项目的人员数（team 分区的 project_members）。
	Members int
	// Teams 是关联该项目数（team 分区的 team_projects）。
	Teams int
}

// Empty 表示没有任何引用，可以直接删除。
func (u Usage) Empty() bool {
	return u.Builds == 0 && u.Members == 0 && u.Teams == 0
}

// CountUsage 按项目 key 统计引用数；一次往返算清三项。
//
// 单人分区的两张关联表是 team 专属（solo 没有成员与团队关系），
// 但仍统一查 team 库表：solo 项目的 id 不会出现在那些表里，结果自然是 0。
// 这样两套分区共用一份实现，不必为 solo 单独写一个"只数包"的分支。
func (r *Repository) CountUsage(ctx context.Context, key string, ownerUserID string, scope contract.Scope) (Usage, error) {
	filter := r.ownerFilter(0, ownerUserID)
	if !r.ownerScoped {
		repository.ApplyTenant(filter, "p", scope)
		repository.ApplyTeam(filter, `p.id IN (SELECT tp.project_id FROM team_projects tp WHERE tp.team_id = ANY(?))`, scope)
	}
	filter.Add(`p.key = ?`, key)

	// 三个标量子查询都按"本项目 id"关联，因此不会数到别的项目。
	query := `SELECT
  (SELECT count(*) FROM ` + r.buildsTable() + ` b WHERE b.project_id = p.id),
  (SELECT count(*) FROM project_members pm WHERE pm.project_id = p.id),
  (SELECT count(*) FROM team_projects tp WHERE tp.project_id = p.id)
FROM ` + r.table + ` p` + filter.Where()

	var usage Usage
	if err := r.db.QueryRowContext(ctx, query, filter.Args()...).
		Scan(&usage.Builds, &usage.Members, &usage.Teams); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return Usage{}, contract.ErrProjectInvalid
		}
		return Usage{}, fmt.Errorf("统计项目引用失败: %w", err)
	}
	return usage, nil
}

// buildsTable 返回与当前分区对应的包表名。
// team.projects 的包在 team.builds，solo.projects 的在 solo.builds——
// 表名必须跟着分区走，否则单人项目会去数团队分区的包（永远是 0，导致误删）。
func (r *Repository) buildsTable() string {
	if r.ownerScoped {
		return "solo.builds"
	}
	return "team.builds"
}

// Delete 按 key 删除项目；scope / owner 限定只能删自己看得到的那个。
//
// 调用方必须先确认 CountUsage.Empty()：包不可恢复，成员与团队关系代表"有人正在用"。
func (r *Repository) Delete(ctx context.Context, key, ownerUserID string, scope contract.Scope) error {
	filter := r.ownerFilter(0, ownerUserID)
	if !r.ownerScoped {
		repository.ApplyTenant(filter, "p", scope)
		repository.ApplyTeam(filter, `p.id IN (SELECT tp.project_id FROM team_projects tp WHERE tp.team_id = ANY(?))`, scope)
	}
	filter.Add(`p.key = ?`, key)

	result, err := r.db.ExecContext(ctx, `DELETE FROM `+r.table+` p`+filter.Where(), filter.Args()...)
	if err != nil {
		return fmt.Errorf("删除项目失败: %w", err)
	}
	// 这里的 0 行要翻译成 ErrProjectInvalid（400）而不是 404：
	// 项目 key 来自 URL / 表单，属于参数问题，与既有 FindByKey 的口径保持一致。
	if err := repository.EnsureDeleted(result, contract.ResourceProject); err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return contract.ErrProjectInvalid
		}
		return err
	}
	return nil
}

// ownerFilter 生成「只属于该账号」的条件。
// 团队分区与未指定账号时返回空条件，等价于全量；
// base 是 WHERE 之前已占用的参数个数（SELECT 传 0，UPDATE 传 SET 的参数数）。
func (r *Repository) ownerFilter(base int, ownerUserID string) *repository.Filter {
	filter := repository.NewFilter(base)
	if r.ownerScoped && ownerUserID != "" {
		filter.Add(`p.owner_user_id = ?`, ownerUserID)
	}
	return filter
}

// noRowsToInvalidProject 把「查不到项目」翻译成参数错误（400 而非 404）。
//
// 名字说明一切：这里只处理"无记录"，不碰约束冲突，也不生成 404——
// 项目 key 通常来自 URL 或表单，属于调用方参数不对，不是资源被删了。
func noRowsToInvalidProject(err error) error {
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
	if err := row.Scan(&item.ID, &item.Key, &item.Name, &item.Description, &item.RepoURL,
		&item.IconURL, &item.RoleKey, &item.RoleName, &item.TenantID, &createdAt); err != nil {
		return contract.Project{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return item, nil
}

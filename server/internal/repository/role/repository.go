// Package role · 角色与权限点的持久化。
// 职责：读写 roles 与 role_permissions，提供"角色 → 权限集合"的组装。
// 输入：连接池；输出：contract.Role（权限已展开）。
// 边界：不校验权限点是否存在（在 service/role）；不判断谁有权改角色（在路由层）。
package role

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

// Repository 是 roles 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// roleColumns 带 tenant_id，用来判断"内置（跨公司）"还是"某公司自定义"。
// columns 用 r. 前缀：所有查询都要 JOIN tenants 取公司名，
// 因为超管会跨公司看到同名角色，界面上必须能区分"这是哪家公司的 developer"。
const columns = `r.id::text, r.key, r.name, r.description, r.is_builtin,
coalesce(r.tenant_id::text, ''), coalesce(t.name, ''), r.created_at`

// fromClause 与 columns 配套：角色归属公司可能为空（历史遗留的全局行），用 LEFT JOIN 保留。
const fromClause = ` FROM roles r LEFT JOIN tenants t ON t.id = r.tenant_id`

// visibleFilter 生成角色可见性条件：平台内置（tenant_id 为空）对所有公司可见，
// 公司只能看到自己的自定义角色。超管（All）不加条件，看全部。
//
// 注意不能直接用 ApplyTenant：它对 tenant_id 为 NULL 的行会过滤掉，
// 而内置角色恰恰是靠 NULL 表示"平台级、所有公司共享"。
func visibleFilter(base int, scope contract.Scope) *repository.Filter {
	filter := repository.NewFilter(base)
	if scope.All {
		return filter
	}
	if scope.TenantID == "" {
		// 失败关闭：没有公司归属的身份不该看到任何角色。
		return filter.AddRaw(`1 = 0`)
	}
	// 角色不再有"全局共享"那一份：内置角色也是每家公司自己的（见 service/role 的说明），
	// 所以可见范围就是一个等值条件，不再需要 "OR tenant_id IS NULL"。
	return filter.Add(`r.tenant_id = ?`, scope.TenantID)
}

// List 返回可见角色，并一次性补齐权限点（两查，不做 N+1）。
func (r *Repository) List(ctx context.Context, scope contract.Scope) ([]contract.Role, error) {
	filter := visibleFilter(0, scope)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+columns+fromClause+filter.Where()+
			` ORDER BY t.name NULLS FIRST, r.is_builtin DESC, r.created_at ASC`, filter.Args()...)
	if err != nil {
		return nil, fmt.Errorf("查询角色列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Role, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历角色列表失败: %w", err)
	}

	permissions, err := r.groupedPermissions(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Permissions = fill(permissions[items[i].ID])
	}
	return items, nil
}

// FindByKey 按 key 查角色；无记录转 contract.NotFound(contract.ResourceRole)。
// scope 限定可见范围：跨公司的自定义角色与「不存在」同口径，避免用 key 探测。
func (r *Repository) FindByKey(ctx context.Context, key string, scope contract.Scope) (contract.Role, error) {
	filter := visibleFilter(0, scope).Add(`r.key = ?`, key)
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+fromClause+filter.Where(), filter.Args()...)
	item, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Role{}, contract.NotFound(contract.ResourceRole)
		}
		return contract.Role{}, err
	}
	item.Permissions, err = r.listPermissions(ctx, item.ID)
	if err != nil {
		return contract.Role{}, err
	}
	return item, nil
}

// FindByKeyAny 按 key 查角色，允许平台内置（tenant_id 为空）与指定公司的自定义角色。
//
// 参数 tenantID 为空表示「平台视角」（超管），此时只匹配内置角色——
// 超管没有公司归属，不该解析出某家公司的自定义角色。
//
// 为什么保留这个"不走 Scope"的入口：权限判定（authz.resolveRoleKey → 角色权限）
// 必须先解析出「这个人挂着什么角色」，而那一步发生在 scope 推导过程中，
// 不能反过来依赖 scope（会形成循环）。所以这里直接收 tenantID 而不是 Scope。
//
// 为什么必须带 tenantID：key 现在是「公司内唯一」，两家公司可以各有一个 devops。
// 不带公司过滤会随机命中一家——意味着甲公司的成员可能被套上乙公司 devops 的权限。
func (r *Repository) FindByKeyAny(ctx context.Context, key, tenantID string) (contract.Role, error) {
	// 有公司归属时：只在本公司范围内解析（内置角色也属于本公司那一份）。
	// 无公司归属（超管）时：无法定位到某家公司，按"任意公司"取一份——
	// 超管本身不走角色判定（permissionsOf 第一步就短路），这里只服务于兜底场景。
	query := `SELECT ` + columns + fromClause + ` WHERE r.key = $1`
	args := []any{key}
	if tenantID != "" {
		query += ` AND r.tenant_id = $2`
		args = append(args, tenantID)
	}
	query += ` ORDER BY r.created_at LIMIT 1`

	row := r.db.QueryRowContext(ctx, query, args...)
	item, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Role{}, contract.NotFound(contract.ResourceRole)
		}
		return contract.Role{}, err
	}
	item.Permissions, err = r.listPermissions(ctx, item.ID)
	if err != nil {
		return contract.Role{}, err
	}
	return item, nil
}

// Create 新建自定义角色，归属公司从 scope 取（请求体不接受 tenantId）。
func (r *Repository) Create(ctx context.Context, in contract.RoleInput, scope contract.Scope) (contract.Role, error) {
	return r.save(ctx, "", in, false, scope.TenantID)
}

// Update 覆盖名称 / 描述 / 权限；key 创建后不可改。
//
// scope 限定只能改本公司的角色——**包括本公司的内置角色**。
// 内置角色每家公司各持一份独立副本，所以"公司管理员改自己公司的 developer"
// 是完全正当的操作，不会影响其他公司。这也是把内置角色做成独立副本的意义所在。
func (r *Repository) Update(ctx context.Context, key string, in contract.RoleInput, scope contract.Scope) (contract.Role, error) {
	filter := visibleFilter(0, scope).Add(`r.key = ?`, key)
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+fromClause+filter.Where(), filter.Args()...)
	existing, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Role{}, contract.NotFound(contract.ResourceRole)
		}
		return contract.Role{}, err
	}
	return r.save(ctx, existing.ID, in, existing.IsBuiltin, existing.TenantID)
}

func (r *Repository) save(ctx context.Context, id string, in contract.RoleInput, isBuiltin bool, tenantID string) (contract.Role, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Role{}, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var roleID string
	if id == "" {
		// tenant_id 从 scope 取：自定义角色归属本公司，请求体无法伪造归属。
		row := tx.QueryRowContext(ctx,
			`INSERT INTO roles (key, name, description, is_builtin, tenant_id) VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid) RETURNING id::text`,
			in.Key, in.Name, in.Description, isBuiltin, tenantID)
		if err := row.Scan(&roleID); err != nil {
			return contract.Role{}, fmt.Errorf("创建角色失败: %w", err)
		}
	} else {
		roleID = id
		if _, err := tx.ExecContext(ctx,
			`UPDATE roles SET name = $2, description = $3 WHERE id = $1`,
			roleID, in.Name, in.Description); err != nil {
			return contract.Role{}, fmt.Errorf("更新角色失败: %w", err)
		}
	}

	// 权限也是"全量覆盖"：先清后写，避免留下本次未勾选的旧权限。
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return contract.Role{}, fmt.Errorf("清理角色权限失败: %w", err)
	}
	for _, permission := range in.Permissions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO role_permissions (role_id, permission) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			roleID, permission); err != nil {
			return contract.Role{}, fmt.Errorf("写入角色权限失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return contract.Role{}, fmt.Errorf("提交角色变更失败: %w", err)
	}
	return r.findByID(ctx, roleID)
}

// Usage 统计角色被多少人员与项目在用，供删除前判断"能不能删"。
type Usage struct {
	Persons  int
	Projects int
}

// Empty 表示没有任何引用，可以直接删除。
func (u Usage) Empty() bool { return u.Persons == 0 && u.Projects == 0 }

// CountUsage 统计角色的引用数。
//
// 两张表分别按 role_id 关联（改名后不再按 key 关联，见 schema.sql 的说明）；
// 单人分区的项目恒无角色，所以只数 team.projects 就够。
func (r *Repository) CountUsage(ctx context.Context, roleID string) (Usage, error) {
	const query = `SELECT
  (SELECT count(*) FROM people        WHERE role_id = $1),
  (SELECT count(*) FROM team.projects WHERE role_id = $1)`

	var usage Usage
	if err := r.db.QueryRowContext(ctx, query, roleID).Scan(&usage.Persons, &usage.Projects); err != nil {
		return Usage{}, fmt.Errorf("统计角色引用失败: %w", err)
	}
	return usage, nil
}

// Delete 删除角色；scope 限定只能删本公司自定义角色（内置角色 tenant_id 为空，必然落空）。
//
// role_permissions 由外键 ON DELETE CASCADE 自动清掉，不需要单独删。
// people.role_id / projects.role_id 是 SET NULL，但调用方**必须先用 CountUsage 拦掉**
// 有引用的角色：SET NULL 会把那些人/项目静默降权（权限从有变无），
// 这种"删角色顺便改变他人权限"的副作用不能在界面上不可见地发生。
func (r *Repository) Delete(ctx context.Context, key string, scope contract.Scope) error {
	filter := visibleFilter(0, scope).Add(`r.key = ?`, key)
	// 双保险：内置角色永远不可删（即使超管 scope.All 让 visibleFilter 放行）。
	filter.AddRaw(`r.is_builtin = false`)

	// DELETE 不能用带 JOIN 的 fromClause，改用子查询定位要删的行。
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM roles WHERE id IN (SELECT r.id`+fromClause+filter.Where()+`)`, filter.Args()...)
	if err != nil {
		return fmt.Errorf("删除角色失败: %w", err)
	}
	return repository.EnsureDeleted(result, contract.ResourceRole)
}

// BuiltinDefinition 是播种内置角色所需的最小信息。
// 定义在仓储侧而不是复用 service 的 builtinRole：仓储只关心"要写哪几行"，
// 不该反向 import 业务层的类型（依赖方向是 service → repository）。
type BuiltinDefinition struct {
	Key         string
	Name        string
	Description string
	Permissions []string
}

// ListTenantIDs 返回全部公司 id，供"给每个公司补齐内置角色"使用。
func (r *Repository) ListTenantIDs(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id::text FROM tenants ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("查询公司列表失败: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取公司 id 失败: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CreateBuiltinIfMissing 给某家公司补一个内置角色：该公司还没有这个 key 时插入并写默认权限。
// 返回 true 表示本次新建，false 表示已存在（已存在的角色不会被默认值覆盖）。
//
// 幂等靠唯一索引 (coalesce(tenant_id,零), key) 保证：
// 并发或有重复调用时只有一条能插进去，另一条命中冲突而不报错。
// 这里用 ON CONFLICT DO NOTHING 而不是 INSERT ... WHERE NOT EXISTS：
// 后者在并发下仍可能两个事务都通过检查、然后一方在提交时报唯一冲突；
// ON CONFLICT 把"检查 + 插入"交给数据库原子完成。
func (r *Repository) CreateBuiltinIfMissing(ctx context.Context, tenantID string, definition BuiltinDefinition) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var roleID string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO roles (key, name, description, is_builtin, tenant_id)
		 VALUES ($1, $2, $3, true, $4::uuid)
		 ON CONFLICT (COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), key) DO NOTHING
		 RETURNING id::text`,
		definition.Key, definition.Name, definition.Description, tenantID).Scan(&roleID)
	if errors.Is(err, sql.ErrNoRows) {
		// 已存在，保持用户改过的权限不动
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("播种内置角色失败: %w", err)
	}

	for _, permission := range definition.Permissions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO role_permissions (role_id, permission) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			roleID, permission); err != nil {
			return false, fmt.Errorf("写入内置角色权限失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("提交内置角色失败: %w", err)
	}
	return true, nil
}

// DeleteLegacySuperAdminRole 删除历史遗留的 super-admin 角色，返回删除行数。
//
// 为什么删：它是"身份被误当成角色"的产物——权限判定第一步就按 users.is_super_admin 短路，
// 从不读这个角色的权限；它唯一的作用是在角色列表里制造"挂上它就是超管"的误解。
// 挂过它的人由 people.role_id 的 ON DELETE SET NULL 自动变成"未分配"，
// 这些人本来也不是超管（真超管靠账号标记，不需要角色），清空是正确结果。
func (r *Repository) DeleteLegacySuperAdminRole(ctx context.Context) error {
	// 常量就地定义而不是放回 contract：它只服务于这一次数据迁移，
	// 从此不该再有任何代码引用它。放在契约层会让人以为"这是个合法角色 key"。
	const legacySuperAdminRoleKey = "super-admin"

	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM roles WHERE key = $1`, legacySuperAdminRoleKey); err != nil {
		return fmt.Errorf("删除遗留的超管角色失败: %w", err)
	}
	return nil
}

// AdoptGlobalBuiltins 把「全局内置角色」（tenant_id 为空）下发给每家公司，然后删掉全局那份。
//
// 旧模型：内置角色是一份全局行，tenant_id 为 NULL，所有公司共享。
// 新模型：每家公司各有一份独立副本，改它只影响本公司。
//
// **迁移顺序至关重要，写反会丢数据**：
// people.role_id 与 projects.role_id 都是 ON DELETE SET NULL，若先删全局行，
// 所有挂过该角色的人/项目会被静默置空（权限从有变无），且没有撤销手段。
// 因此必须 strict 按下面的顺序：
//  1. 给每家公司插入副本；
//  2. 把仍指向全局行的引用**改指**到「同公司、同 key」的副本；
//  3. 确认没有残留引用后，才删全局行。
//
// 迁移必须保留用户已改过的权限：副本的权限从全局行的 role_permissions 原样拷过去，
// 而不是用代码里的默认值；若某公司已有同 key 角色（之前自建过）则跳过，绝不用默认值覆盖。
func (r *Repository) AdoptGlobalBuiltins(ctx context.Context, definitions []BuiltinDefinition) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启内置角色迁移事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, definition := range definitions {
		// 1) 给每家公司插入副本（已存在则跳过）。权限稍后从全局行复制。
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO roles (key, name, description, is_builtin, tenant_id)
			SELECT g.key, g.name, g.description, true, t.id
			FROM roles g CROSS JOIN tenants t
			WHERE g.key = $1 AND g.tenant_id IS NULL
			ON CONFLICT (COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), key) DO NOTHING`,
			definition.Key); err != nil {
			return fmt.Errorf("下发内置角色失败（%s）: %w", definition.Key, err)
		}

		// 2) 复制权限：把全局行的权限原样给每家公司副本，只补副本缺的、不删已有的
		//    （保留管理员此前对副本做过的调整）。
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO role_permissions (role_id, permission)
			SELECT local.id, rp.permission
			FROM roles g
			JOIN role_permissions rp ON rp.role_id = g.id
			JOIN roles local ON local.key = g.key AND local.tenant_id IS NOT NULL
			WHERE g.key = $1 AND g.tenant_id IS NULL
			ON CONFLICT DO NOTHING`, definition.Key); err != nil {
			return fmt.Errorf("复制内置角色权限失败（%s）: %w", definition.Key, err)
		}
	}

	// 3) 把引用改指到同公司同 key 的副本。这一步必须在删全局行之前，
	//    否则外键的 ON DELETE SET NULL 会把所有人/项目的角色清空。
	if _, err := tx.ExecContext(ctx, `
		UPDATE people p SET role_id = local.id
		FROM roles g, roles local
		WHERE g.tenant_id IS NULL
		  AND p.role_id = g.id
		  AND local.key = g.key
		  AND local.tenant_id = p.tenant_id`); err != nil {
		return fmt.Errorf("迁移人员角色引用失败: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE team.projects pr SET role_id = local.id
		FROM roles g, roles local
		WHERE g.tenant_id IS NULL
		  AND pr.role_id = g.id
		  AND local.key = g.key
		  AND local.tenant_id = pr.tenant_id`); err != nil {
		return fmt.Errorf("迁移项目角色引用失败（team）: %w", err)
	}

	// 4) 兜底核对：还有引用指向全局行就中止，宁可迁移失败也不要静默清空角色。
	var remaining int
	if err := tx.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM people WHERE role_id IN (SELECT id FROM roles WHERE tenant_id IS NULL))
		     + (SELECT count(*) FROM team.projects WHERE role_id IN (SELECT id FROM roles WHERE tenant_id IS NULL))`).
		Scan(&remaining); err != nil {
		return fmt.Errorf("核对角色引用失败: %w", err)
	}
	if remaining > 0 {
		// 回滚整个事务：这些引用无法安全迁移（例如人员没有公司归属），
		// 交给运维排查，比删掉全局行让它们变成"无角色"要好。
		return fmt.Errorf("仍有 %d 处引用指向全局角色，已中止迁移以免清空角色", remaining)
	}

	// 5) 全局那份已无人引用，删掉；role_permissions 由外键 CASCADE 清理。
	if _, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE tenant_id IS NULL`); err != nil {
		return fmt.Errorf("清理全局内置角色失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交内置角色迁移失败: %w", err)
	}
	return nil
}

// PurgeDeprecatedPermissions 清掉所有角色里残留的管理类权限点。
// 这些点已从可勾选目录移除、改由 account_level 直接下发，
// 留在角色里会让"这个权限从哪来"出现两个口径。
func (r *Repository) PurgeDeprecatedPermissions(ctx context.Context, permissions []string) error {
	if len(permissions) == 0 {
		return nil
	}
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM role_permissions WHERE permission = ANY($1::text[])`, permissions); err != nil {
		return fmt.Errorf("清理过期权限点失败: %w", err)
	}
	return nil
}

func (r *Repository) findByID(ctx context.Context, id string) (contract.Role, error) {
	// 必须配 fromClause：columns 带 r. 别名且含 coalesce(t.name, '')，
	// 少了 JOIN tenants 会直接 SQL 报错——这是 save 末尾回读角色的唯一出口。
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+fromClause+` WHERE r.id = $1`, id)
	item, err := scan(row)
	if err != nil {
		return contract.Role{}, err
	}
	item.Permissions, err = r.listPermissions(ctx, item.ID)
	if err != nil {
		return contract.Role{}, err
	}
	return item, nil
}

func (r *Repository) listPermissions(ctx context.Context, roleID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT permission FROM role_permissions WHERE role_id = $1 ORDER BY permission`, roleID)
	if err != nil {
		return nil, fmt.Errorf("查询角色权限失败: %w", err)
	}
	defer rows.Close()

	permissions := make([]string, 0)
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, fmt.Errorf("读取角色权限失败: %w", err)
		}
		permissions = append(permissions, permission)
	}
	return permissions, rows.Err()
}

// groupedPermissions 聚成「角色 id → 权限列表」，列表页一次查完。
func (r *Repository) groupedPermissions(ctx context.Context) (map[string][]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT role_id::text, permission FROM role_permissions`)
	if err != nil {
		return nil, fmt.Errorf("查询角色权限失败: %w", err)
	}
	defer rows.Close()

	grouped := make(map[string][]string)
	for rows.Next() {
		var roleID, permission string
		if err := rows.Scan(&roleID, &permission); err != nil {
			return nil, fmt.Errorf("读取角色权限失败: %w", err)
		}
		grouped[roleID] = append(grouped[roleID], permission)
	}
	return grouped, rows.Err()
}

// fill 保证 JSON 里是 [] 而不是 null。
func fill(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (contract.Role, error) {
	var item contract.Role
	var createdAt time.Time
	if err := row.Scan(&item.ID, &item.Key, &item.Name, &item.Description, &item.IsBuiltin,
		&item.TenantID, &item.TenantName, &createdAt); err != nil {
		return contract.Role{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	item.Permissions = []string{}
	return item, nil
}

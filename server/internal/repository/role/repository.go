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
const columns = `id::text, key, name, description, is_builtin, coalesce(tenant_id::text, ''), created_at`

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
	return filter.Add(`(tenant_id IS NULL OR tenant_id = ?)`, scope.TenantID)
}

// List 返回可见角色，并一次性补齐权限点（两查，不做 N+1）。
func (r *Repository) List(ctx context.Context, scope contract.Scope) ([]contract.Role, error) {
	filter := visibleFilter(0, scope)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+columns+` FROM roles`+filter.Where()+
			` ORDER BY is_builtin DESC, created_at ASC`, filter.Args()...)
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

// FindByKey 按 key 查角色；无记录转 contract.ErrNotFound。
// scope 限定可见范围：跨公司的自定义角色与「不存在」同口径，避免用 key 探测。
func (r *Repository) FindByKey(ctx context.Context, key string, scope contract.Scope) (contract.Role, error) {
	filter := visibleFilter(0, scope).Add(`key = ?`, key)
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM roles`+filter.Where(), filter.Args()...)
	item, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Role{}, contract.ErrNotFound
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
	// 有公司归属时：内置角色 + 本公司自定义角色。
	// 无公司归属（超管）时：只认内置角色。
	query := `SELECT ` + columns + ` FROM roles WHERE key = $1 AND (tenant_id IS NULL)`
	args := []any{key}
	if tenantID != "" {
		query = `SELECT ` + columns + ` FROM roles
WHERE key = $1 AND (tenant_id IS NULL OR tenant_id = $2)`
		args = append(args, tenantID)
	}

	row := r.db.QueryRowContext(ctx, query, args...)
	item, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Role{}, contract.ErrNotFound
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
// scope 限定只能改本公司自定义角色：内置角色（tenant_id 为空）与别家公司的角色都会查不到。
func (r *Repository) Update(ctx context.Context, key string, in contract.RoleInput, scope contract.Scope) (contract.Role, error) {
	filter := visibleFilter(0, scope).Add(`key = ?`, key)
	// 内置角色只有超管能改：普通人即便看得到它，也不能改平台级定义。
	if !scope.All {
		filter.AddRaw(`tenant_id IS NOT NULL`)
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM roles`+filter.Where(), filter.Args()...)
	existing, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Role{}, contract.ErrNotFound
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

// ClearBuiltinTenant 把内置角色的 tenant_id 归零（NULL），表示"平台级、所有公司共享"。
// P1 的存量回填曾把角色一律归到默认公司；不修正的话，其他公司将看不到任何内置角色，
// 连 developer / tester 都选不到。幂等，只动 is_builtin=true 的行。
func (r *Repository) ClearBuiltinTenant(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE roles SET tenant_id = NULL WHERE is_builtin = true AND tenant_id IS NOT NULL`); err != nil {
		return fmt.Errorf("修正内置角色归属失败: %w", err)
	}
	return nil
}

// DeletePermissionsExceptRole 从除 exceptKey 之外的所有角色里删除指定权限点。
// 用于清理「已从可勾选目录移除、但存量角色还留着」的管理类权限，幂等。
// 用 NOT IN 子查询而不是先查再逐个删：一条语句完成，不会出现中间态。
func (r *Repository) DeletePermissionsExceptRole(ctx context.Context, exceptKey string, permissions []string) error {
	if len(permissions) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM role_permissions
		 WHERE permission = ANY($1::text[])
		   AND role_id NOT IN (SELECT id FROM roles WHERE key = $2)`,
		permissions, exceptKey)
	if err != nil {
		return fmt.Errorf("清理过期权限点失败: %w", err)
	}
	return nil
}

// CreateBuiltinIfMissing 播种内置角色：只在「平台内置角色里还没有这个 key」时插入并写默认权限。
// 返回 true 表示本次新建，false 表示已存在（已存在的角色不会被默认值覆盖）。
//
// 不用 ON CONFLICT：key 的唯一性现在是「(coalesce(tenant_id,零), key)」这个表达式索引，
// ON CONFLICT 要把整个表达式照抄一遍，可读性差且容易与索引定义走偏。
// 播种只在启动时执行一次，不是热路径，用 INSERT ... WHERE NOT EXISTS 更直白，
// 语义也精确对应「内置角色（tenant_id 为空）里没有这个 key」。
func (r *Repository) CreateBuiltinIfMissing(ctx context.Context, key, name, description string, permissions []string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var roleID string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO roles (key, name, description, is_builtin)
		 SELECT $1, $2, $3, true
		 WHERE NOT EXISTS (SELECT 1 FROM roles WHERE key = $1 AND tenant_id IS NULL)
		 RETURNING id::text`,
		key, name, description).Scan(&roleID)
	if errors.Is(err, sql.ErrNoRows) {
		// 已存在，保持用户改过的权限不动
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("播种内置角色失败: %w", err)
	}

	for _, permission := range permissions {
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

func (r *Repository) findByID(ctx context.Context, id string) (contract.Role, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM roles WHERE id = $1`, id)
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
	if err := row.Scan(&item.ID, &item.Key, &item.Name, &item.Description, &item.IsBuiltin, &item.TenantID, &createdAt); err != nil {
		return contract.Role{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	item.Permissions = []string{}
	return item, nil
}

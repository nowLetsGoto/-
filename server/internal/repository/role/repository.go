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
)

// Repository 是 roles 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

const columns = `id::text, key, name, description, is_builtin, created_at`

// List 返回全部角色，并一次性补齐权限点（两查，不做 N+1）。
func (r *Repository) List(ctx context.Context) ([]contract.Role, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+columns+` FROM roles ORDER BY is_builtin DESC, created_at ASC`)
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
func (r *Repository) FindByKey(ctx context.Context, key string) (contract.Role, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM roles WHERE key = $1`, key)
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

// Create 新建自定义角色。
func (r *Repository) Create(ctx context.Context, in contract.RoleInput) (contract.Role, error) {
	return r.save(ctx, "", in, false)
}

// Update 覆盖名称 / 描述 / 权限；key 创建后不可改。
func (r *Repository) Update(ctx context.Context, key string, in contract.RoleInput) (contract.Role, error) {
	existing, err := r.FindByKey(ctx, key)
	if err != nil {
		return contract.Role{}, err
	}
	return r.save(ctx, existing.ID, in, existing.IsBuiltin)
}

func (r *Repository) save(ctx context.Context, id string, in contract.RoleInput, isBuiltin bool) (contract.Role, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Role{}, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var roleID string
	if id == "" {
		row := tx.QueryRowContext(ctx,
			`INSERT INTO roles (key, name, description, is_builtin) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			in.Key, in.Name, in.Description, isBuiltin)
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

// CreateBuiltinIfMissing 播种内置角色：只在 key 不存在时插入并写默认权限。
// 返回 true 表示本次新建，false 表示已存在（已存在的角色不会被默认值覆盖）。
func (r *Repository) CreateBuiltinIfMissing(ctx context.Context, key, name, description string, permissions []string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var roleID string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO roles (key, name, description, is_builtin) VALUES ($1, $2, $3, true)
		 ON CONFLICT (key) DO NOTHING
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
	if err := row.Scan(&item.ID, &item.Key, &item.Name, &item.Description, &item.IsBuiltin, &createdAt); err != nil {
		return contract.Role{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	item.Permissions = []string{}
	return item, nil
}

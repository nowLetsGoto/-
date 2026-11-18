// Package tenant · 公司的持久化。
// 职责：读取 tenants 表给身份投影提供公司名；分页列出公司；
//
//	原子创建「公司 + 其平台管理员账号」——这两件事必须同一事务，
//	否则会留下「有公司没管理员」的孤儿，谁都进不去那家公司。
// 输入：连接池、公司 DTO、已派生的密码哈希。
// 输出：公司记录 / 平台管理员账号 id。
// 边界：不做业务判断（层级归属由 service 决定）、不碰密码学（哈希由 service 传入）；
//	平台设置副本属于 P4，本阶段不复制。
package tenant

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

// Repository 是 tenants 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// NameByID 按公司 id 取名字。
// 空 id（超管没有公司）与已删除的公司都返回空串：/me 不必为"没有公司"分流两套逻辑，
// 前端拿到空串就显示"平台"或隐藏公司标签，与"查不到"是同一种展示。
func (r *Repository) NameByID(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	var name string
	err := r.db.QueryRowContext(ctx, `SELECT name FROM tenants WHERE id = $1`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("查询公司名称失败: %w", err)
	}
	return name, nil
}

// tenantColumns 是列表与详情共用的列；管理员账号名通过子查询取（每公司至多一个 active 平台管理员）。
const tenantColumns = `t.id::text, t.name, t.code, t.status,
coalesce((SELECT u.username FROM users u
  WHERE u.tenant_id = t.id AND u.account_level = 'platform_admin' AND u.status = 'active'
  ORDER BY u.created_at LIMIT 1), ''), t.created_at`

// List 分页返回公司，按创建顺序；只给超管的平台视角使用。
func (r *Repository) List(ctx context.Context, page contract.PageQuery) ([]contract.Tenant, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM tenants`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计公司数量失败: %w", err)
	}

	filter := repository.NewFilter(0)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+tenantColumns+` FROM tenants t`+filter.Where()+
			fmt.Sprintf(` ORDER BY t.created_at ASC LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1)),
		page.Limit(), page.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("查询公司列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Tenant, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历公司列表失败: %w", err)
	}
	return items, total, nil
}

// CreateWithAdmin 在同一事务里创建公司与它的唯一平台管理员。
// adminHash 是已派生的密码哈希；本层不接触明文。
// 公司标识冲突（name/code 唯一）或管理员账号名冲突都会让整个事务回滚。
func (r *Repository) CreateWithAdmin(ctx context.Context, in contract.TenantInput, adminHash string) (contract.Tenant, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Tenant{}, fmt.Errorf("开启公司创建事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var tenantID string
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO tenants (name, code) VALUES ($1, $2) RETURNING id::text`,
		in.Name, in.Code).Scan(&tenantID); err != nil {
		return contract.Tenant{}, err
	}

	// 平台管理员：层级固定 platform_admin、归属新公司；is_super_admin 必须为 false——
	// 它是「公司管理员」，不是平台超管，两个概念不能混。
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, mode, account_level, tenant_id, is_super_admin)
		 VALUES ($1, $2, $3, $4, $5, false)`,
		in.AdminUsername, adminHash, contract.ModeTeam, contract.AccountLevelPlatformAdmin, tenantID); err != nil {
		return contract.Tenant{}, err
	}

	if err := tx.Commit(); err != nil {
		return contract.Tenant{}, fmt.Errorf("提交公司创建失败: %w", err)
	}

	// 提交后按 id 重查：管理员账号名由子查询带出，RETURNING 拿不到。
	row := r.db.QueryRowContext(ctx, `SELECT `+tenantColumns+` FROM tenants t WHERE t.id = $1`, tenantID)
	item, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Tenant{}, contract.ErrNotFound
		}
		return contract.Tenant{}, fmt.Errorf("读取新建公司失败: %w", err)
	}
	return item, nil
}

type scanner interface{ Scan(dest ...any) error }

func scan(row scanner) (contract.Tenant, error) {
	var item contract.Tenant
	var createdAt time.Time
	if err := row.Scan(&item.ID, &item.Name, &item.Code, &item.Status, &item.AdminUsername, &createdAt); err != nil {
		return contract.Tenant{}, err
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return item, nil
}

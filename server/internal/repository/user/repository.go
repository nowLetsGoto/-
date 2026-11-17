// Package user · 账号的持久化。
// 职责：读写 users 表，提供鉴权所需的密码哈希与令牌版本。
// 输入：连接池；输出：contract.User（对外）与内部 record（含密码，仅供 service 校验）。
// 边界：**不在任何对外结构里暴露 password_hash**；不做密码策略校验（在 service/user）；
//
//	不判断超管权限（在 middleware/login.RequireSuperAdmin）。
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
)

// Record 是仓储内部行：对外 DTO 之外还带密码哈希与令牌版本。
type Record struct {
	User         contract.User
	PasswordHash string
	TokenVersion int
}

// Repository 是 users 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// columns 带 LEFT JOIN 取人员姓名：列表页要显示"绑定的人是谁"，一次查完不做 N+1。
const columns = `u.id::text, u.username, u.password_hash, coalesce(u.person_id::text, ''),
u.is_super_admin, u.status, u.token_version, u.last_login_at, u.last_login_ip,
u.created_at, coalesce(p.name, '')`

const fromClause = ` FROM users u LEFT JOIN people p ON p.id = u.person_id`

// FindByUsername 按登录名查账号；无记录转 contract.ErrNotFound（调用方自行决定是否暴露）。
func (r *Repository) FindByUsername(ctx context.Context, username string) (Record, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+fromClause+` WHERE u.username = $1`, username)
	return scanRecord(row)
}

// FindByID 按 id 查账号。
func (r *Repository) FindByID(ctx context.Context, id string) (Record, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+fromClause+` WHERE u.id = $1`, id)
	return scanRecord(row)
}

// List 返回指定页的账号与总数；keyword 同时匹配登录名与绑定人员姓名。
func (r *Repository) List(ctx context.Context, page contract.PageQuery, keyword string) ([]contract.User, int, error) {
	where, args := keywordClause(keyword)

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM users u LEFT JOIN people p ON p.id = u.person_id`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计账号数量失败: %w", err)
	}

	// 分页参数追加在关键字参数之后，占位符顺序与 args 保持一致。
	args = append(args, page.Limit(), page.Offset())
	query := `SELECT ` + columns + fromClause + where +
		fmt.Sprintf(` ORDER BY u.is_super_admin DESC, u.created_at ASC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询账号列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.User, 0)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, record.User)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历账号列表失败: %w", err)
	}
	return items, total, nil
}

// Create 新建账号；用户名冲突由唯一索引拒绝，service 翻译成可读提示。
// 插入后按 id 重查一次：columns 里有 JOIN people 取姓名，RETURNING 拿不到。
func (r *Repository) Create(ctx context.Context, username, passwordHash, personID string, isSuperAdmin bool) (Record, error) {
	row := r.db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, person_id, is_super_admin)
		 VALUES ($1, $2, NULLIF($3, '')::uuid, $4)
		 RETURNING id::text`,
		username, passwordHash, personID, isSuperAdmin)

	var id string
	if err := row.Scan(&id); err != nil {
		return Record{}, fmt.Errorf("创建账号失败: %w", err)
	}
	return r.FindByID(ctx, id)
}

// Update 改超管标记与绑定人员（登录名不可改）。
func (r *Repository) Update(ctx context.Context, id, personID string, isSuperAdmin bool) (Record, error) {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE users SET person_id = NULLIF($2, '')::uuid, is_super_admin = $3 WHERE id = $1`,
		id, personID, isSuperAdmin); err != nil {
		return Record{}, fmt.Errorf("更新账号失败: %w", err)
	}
	return r.FindByID(ctx, id)
}

// SetStatus 启用/禁用账号；禁用时同时让令牌版本自增，做到"立即下线"。
func (r *Repository) SetStatus(ctx context.Context, id, status string) error {
	const query = `UPDATE users
SET status = $2,
    token_version = CASE WHEN $2 = 'disabled' THEN token_version + 1 ELSE token_version END
WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id, status)
	if err != nil {
		return fmt.Errorf("更新账号状态失败: %w", err)
	}
	return requireAffected(result)
}

// SetPassword 覆盖密码哈希，并让旧令牌失效（改密码后其它设备应重新登录）。
func (r *Repository) SetPassword(ctx context.Context, id, passwordHash string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = $2, token_version = token_version + 1 WHERE id = $1`,
		id, passwordHash)
	if err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
	}
	return requireAffected(result)
}

// Delete 物理删除账号。
// users 没有被任何外键引用（projects/builds 的 owner_user_id 是 TEXT，不是 FK），
// 因此删除不会级联破坏历史数据；历史上"谁建的包"仍留在审计日志里。
func (r *Repository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("删除账号失败: %w", err)
	}
	return requireAffected(result)
}

// BumpTokenVersion 强制下线：旧令牌里的版本号会与库里不一致而失效。
func (r *Repository) BumpTokenVersion(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE users SET token_version = token_version + 1 WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("强制下线失败: %w", err)
	}
	return requireAffected(result)
}

// TouchLogin 记录最近登录时间与来源 IP；失败不影响登录本身（旁路信息）。
func (r *Repository) TouchLogin(ctx context.Context, id, ip string) {
	_, _ = r.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = now(), last_login_ip = $2 WHERE id = $1`, id, ip)
}

// Count 返回账号总数，用于「首次启动才写种子管理员」的判断。
func (r *Repository) Count(ctx context.Context) (int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计账号数量失败: %w", err)
	}
	return total, nil
}

// CountSuperAdmins 返回启用中的超管数量，"不能没有超管"的护栏要用。
func (r *Repository) CountSuperAdmins(ctx context.Context) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE is_super_admin AND status = 'active'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("统计超级管理员失败: %w", err)
	}
	return count, nil
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取变更行数失败: %w", err)
	}
	if affected == 0 {
		return contract.ErrNotFound
	}
	return nil
}

// keywordClause 拼关键字条件：登录名或人员姓名模糊匹配。
func keywordClause(keyword string) (string, []any) {
	trimmed := strings.TrimSpace(keyword)
	if trimmed == "" {
		return "", nil
	}
	pattern := "%" + trimmed + "%"
	return ` WHERE (u.username ILIKE $1 OR p.name ILIKE $1)`, []any{pattern}
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(row scanner) (Record, error) {
	var record Record
	var lastLogin sql.NullTime
	var createdAt time.Time

	err := row.Scan(
		&record.User.ID, &record.User.Username, &record.PasswordHash, &record.User.PersonID,
		&record.User.IsSuperAdmin, &record.User.Status, &record.TokenVersion,
		&lastLogin, &record.User.LastLoginIP, &createdAt, &record.User.PersonName,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return Record{}, contract.ErrNotFound
		}
		return Record{}, fmt.Errorf("读取账号失败: %w", err)
	}

	record.User.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	if lastLogin.Valid {
		record.User.LastLoginAt = lastLogin.Time.UTC().Format(time.RFC3339)
	}
	return record, nil
}

// Package audit · 审计日志的持久化。
// 职责：写入一条审计记录、按条件分页查询。
// 输入：连接池、Entry（写入）/ contract.AuditQuery（查询）。
// 输出：写入无返回；查询返回 contract.AuditLog 列表与总数。
// 边界：**只追加，不提供 UPDATE / DELETE**——审计日志被篡改就失去意义。
package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dandelion/server/internal/contract"
)

// Entry 是一次审计写入的入参（比 DTO 少了自增 id 与时间）。
type Entry struct {
	ActorID    string
	ActorName  string
	Action     string
	ObjectType string
	ObjectID   string
	Result     string
	DetailJSON string
	IP         string
}

// Repository 是 audit_logs 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Create 追加一条审计记录。
func (r *Repository) Create(ctx context.Context, entry Entry) error {
	const query = `INSERT INTO audit_logs (actor_id, actor_name, action, object_type, object_id, result, detail, ip)
VALUES (NULLIF($1, '')::uuid, $2, $3, $4, $5, $6, $7::jsonb, $8)`

	if entry.DetailJSON == "" {
		entry.DetailJSON = "{}"
	}
	if _, err := r.db.ExecContext(ctx, query,
		entry.ActorID, entry.ActorName, entry.Action, entry.ObjectType,
		entry.ObjectID, entry.Result, entry.DetailJSON, entry.IP); err != nil {
		return fmt.Errorf("写入审计日志失败: %w", err)
	}
	return nil
}

const columns = `id, created_at, coalesce(actor_id::text, ''), actor_name,
action, object_type, object_id, result, detail::text, ip`

// List 按条件分页查询；filters 全部为空时等于"查全部"。
func (r *Repository) List(ctx context.Context, page contract.PageQuery, filters contract.AuditQuery) ([]contract.AuditLog, int, error) {
	where, args := buildWhere(filters)

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计审计日志失败: %w", err)
	}

	args = append(args, page.Limit(), page.Offset())
	query := `SELECT ` + columns + ` FROM audit_logs` + where +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询审计日志失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.AuditLog, 0)
	for rows.Next() {
		var item contract.AuditLog
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &createdAt, &item.ActorID, &item.ActorName,
			&item.Action, &item.ObjectType, &item.ObjectID, &item.Result, &item.Detail, &item.IP); err != nil {
			return nil, 0, fmt.Errorf("读取审计日志失败: %w", err)
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历审计日志失败: %w", err)
	}
	return items, total, nil
}

// buildWhere 拼筛选条件；占位符从 $1 递增，调用方负责把后续分页参数接着编号。
func buildWhere(filters contract.AuditQuery) (string, []any) {
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 3)

	if keyword := strings.TrimSpace(filters.Action); keyword != "" {
		args = append(args, "%"+keyword+"%")
		conditions = append(conditions, fmt.Sprintf("action ILIKE $%d", len(args)))
	}
	if actor := strings.TrimSpace(filters.ActorName); actor != "" {
		args = append(args, "%"+actor+"%")
		conditions = append(conditions, fmt.Sprintf("actor_name ILIKE $%d", len(args)))
	}
	if result := strings.TrimSpace(filters.Result); result != "" {
		args = append(args, result)
		conditions = append(conditions, fmt.Sprintf("result = $%d", len(args)))
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

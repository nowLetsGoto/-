// Package audit · 审计日志的持久化。
// 职责：写入一条审计记录、按条件分页查询。
// 输入：连接池、Entry（写入）/ contract.AuditQuery（查询）。
// 输出：写入无返回；查询返回 contract.AuditLog 列表与总数。
// 边界：**只追加，不提供 UPDATE / DELETE**——审计日志被篡改就失去意义；
// 单人 / 团队各写各的分区表，跨模式互不可见（单人操作不会出现在团队审计里）。
package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
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

// Repository 是一个分区内 audit_logs 表的唯一 SQL 出入口；分区在构造时固定。
type Repository struct {
	db     *sql.DB
	schema repository.Schema // 分区即模式：读出的每条日志都用它回填 Mode
	table  string            // 全限定表名，如 team.audit_logs；只能来自 repository.Partition.Table
}

// NewPartitioned 为 team 与 solo 各建一个访问器，交给上层按会话模式取用。
func NewPartitioned(db *sql.DB) repository.Partitioned[*Repository] {
	return repository.NewPartitioned(
		newRepository(db, repository.PartitionTeam),
		newRepository(db, repository.PartitionSolo),
	)
}

func newRepository(db *sql.DB, schema repository.Schema) *Repository {
	return &Repository{db: db, schema: schema, table: schema.Table("audit_logs")}
}

// Create 追加一条审计记录。
func (r *Repository) Create(ctx context.Context, entry Entry) error {
	query := `INSERT INTO ` + r.table + ` (actor_id, actor_name, action, object_type, object_id, result, detail, ip)
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

// List 按条件分页查询；filters 全部为空时等于"查本分区全部"。
func (r *Repository) List(ctx context.Context, page contract.PageQuery, filters contract.AuditQuery) ([]contract.AuditLog, int, error) {
	filter := buildFilter(filters)

	var total int
	countSQL := `SELECT count(*) FROM ` + r.table + filter.Where()
	if err := r.db.QueryRowContext(ctx, countSQL, filter.Args()...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计审计日志失败: %w", err)
	}

	args := append(filter.Args(), page.Limit(), page.Offset())
	query := `SELECT ` + columns + ` FROM ` + r.table + filter.Where() +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询审计日志失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.AuditLog, 0)
	// 模式在循环外算一次：同一分区的所有行必然同模式（分区即模式）。
	mode := string(r.schema.Mode())
	for rows.Next() {
		var item contract.AuditLog
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &createdAt, &item.ActorID, &item.ActorName,
			&item.Action, &item.ObjectType, &item.ObjectID, &item.Result, &item.Detail, &item.IP); err != nil {
			return nil, 0, fmt.Errorf("读取审计日志失败: %w", err)
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		item.Mode = mode
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历审计日志失败: %w", err)
	}
	return items, total, nil
}

// buildFilter 把查询条件翻译成 WHERE 片段；跳过的空条件不会占用参数位。
func buildFilter(filters contract.AuditQuery) *repository.Filter {
	filter := repository.NewFilter(0)
	if keyword := strings.TrimSpace(filters.Action); keyword != "" {
		filter.Add(`action ILIKE ?`, "%"+keyword+"%")
	}
	if actor := strings.TrimSpace(filters.ActorName); actor != "" {
		filter.Add(`actor_name ILIKE ?`, "%"+actor+"%")
	}
	if result := strings.TrimSpace(filters.Result); result != "" {
		filter.Add(`result = ?`, result)
	}
	return filter
}

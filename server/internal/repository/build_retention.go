// build_retention_repository.go · 淘汰所需的读写。
//
// 职责：按租户读出参与淘汰的候选快照，以及把淘汰结果落回数据库。
// 输入：租户 id、待标记的包 id 列表。
// 输出：候选快照；标记后无返回。
// 边界：不判断该淘汰谁（那是 service 的纯函数职责），只做 SQL；
//
//	不删任何行——淘汰只让对象离开 R2，数据库记录必须完整保留。
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RetentionRow 是一行参与淘汰决策的快照。
//
// 字段刻意最小化：只带决策需要的"身份 / 占用 / 时间 / 是否固定"。
// 应用名、版本号等业务字段不参与判断，带出来只会让 SQL 与模型都变胖。
type RetentionRow struct {
	ID             string
	TenantID       string
	R2Key          string
	R2Bytes        int64
	PinKind        string
	CreatedAt      time.Time
	LastAccessedAt *time.Time
}

// ListRetentionCandidates 读出一个租户下所有仍在 R2 的包，供淘汰算法决策。
//
// 只取 r2_key 非空且 r2_bytes > 0 的行：没有对象名的包不在桶里，
// 字节为 0 的包已被淘汰或尚未同步，两者都不占 R2 预算，不参与竞争。
func (r *BuildRepository) ListRetentionCandidates(ctx context.Context, tenantID string) ([]RetentionRow, error) {
	query := `SELECT b.id, p.tenant_id, b.r2_key, b.r2_bytes, b.pin_kind, b.created_at, b.last_accessed_at
FROM ` + r.table + ` b
JOIN ` + r.project + ` p ON p.id = b.project_id
WHERE p.tenant_id = $1 AND b.r2_key <> '' AND b.r2_bytes > 0
ORDER BY b.created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询淘汰候选失败: %w", err)
	}
	defer rows.Close()

	var out []RetentionRow
	for rows.Next() {
		var row RetentionRow
		var accessed sql.NullTime
		if err := rows.Scan(&row.ID, &row.TenantID, &row.R2Key, &row.R2Bytes,
			&row.PinKind, &row.CreatedAt, &accessed); err != nil {
			return nil, fmt.Errorf("读取淘汰候选失败: %w", err)
		}
		if accessed.Valid {
			t := accessed.Time
			row.LastAccessedAt = &t
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// MarkEvicted 把包标记为"对象已离开 R2"。
//
// 清空 r2_key 是这个标记的核心：下载路径以 r2_key 是否存在来决定走 CDN 还是本机，
// 置空后就不会再签发一个已经失效的直链。同时把 r2_bytes 归零，
// 让容量统计立刻反映"空间已释放"。
//
// 不删行：这是淘汰与人工删除的根本区别——历史版本、下载次数、分享记录都保留，
// 用户仍能看到"曾经有过这个版本"，只是文件不再占用对象存储。
func (r *BuildRepository) MarkEvicted(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	query := `UPDATE ` + r.table + `
SET r2_key = '', r2_bytes = 0, r2_state = 'evicted', r2_synced_at = NULL
WHERE id = ANY($1)`
	if _, err := r.db.ExecContext(ctx, query, ids); err != nil {
		return fmt.Errorf("标记淘汰失败: %w", err)
	}
	return nil
}

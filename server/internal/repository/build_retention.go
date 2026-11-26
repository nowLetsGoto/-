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

	"dandelion/server/internal/contract"
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
// 判据是 r2_state = 'synced' 且占用大于 0：已淘汰的包（r2_key 仍保留供重推）
// 与尚未同步的包都不占桶空间，不参与竞争。用状态而非 key 是否为空来筛，
// 是因为淘汰后 key 故意保留，按 key 判会把已回收的包重新算进候选，
// 每轮巡检都重复处理同一批对象。
func (r *BuildRepository) ListRetentionCandidates(ctx context.Context, tenantID string) ([]RetentionRow, error) {
	query := `SELECT b.id, p.tenant_id, b.r2_key, b.r2_bytes, b.pin_kind, b.created_at, b.last_accessed_at
FROM ` + r.table + ` b
JOIN ` + r.project + ` p ON p.id = b.project_id
WHERE p.tenant_id = $1 AND b.r2_state = $2 AND b.r2_bytes > 0
ORDER BY b.created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, tenantID, contract.R2StateSynced)
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

// MarkEvicted 把包标记为「对象已离开 R2」。
//
// 保留 r2_key 而不清空：它是「同步到 R2」重推时的目标对象名。
// 若清空，重推就得重新生成 key，而新 key 很可能丢掉
// {租户}/{项目}/direct/ 这段归属前缀，孤儿对象将再也无法判断属于谁。
//
// 因此「对象在不在」由 r2_state 判定，而不是看 r2_key 是否为空——
// 下载路径必须同步改成读 r2_state，否则会拿旧 key 去签发一个已删除对象的直链。
//
// r2_bytes 归零、r2_synced_at 清空，让容量统计立刻反映空间已释放。
// 不删行：这是淘汰与人工删除的根本区别——历史版本、下载次数、分享记录都保留。
func (r *BuildRepository) MarkEvicted(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	query := `UPDATE ` + r.table + `
SET r2_bytes = 0, r2_state = $2, r2_synced_at = NULL
WHERE id = ANY($1)`
	if _, err := r.db.ExecContext(ctx, query, ids, contract.R2StateEvicted); err != nil {
		return fmt.Errorf("标记淘汰失败: %w", err)
	}
	return nil
}

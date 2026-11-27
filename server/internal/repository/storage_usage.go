// storage_usage.go · 存储用量的统计查询。
//
// 职责：汇总单个分区内的 R2 占用、包数与同步状态，供管理端展示用量。
// 输入：租户 id。
// 输出：总量 + 按项目/环境/平台的分组明细。
// 边界：只读，不改任何数据；不做淘汰判断（那是 service.PlanRetention 的职责）；
//
//	只管一个分区——跨分区合并由 service 完成，仓储不该知道另一个分区叫什么。
//
// 为什么按租户而不是全局统计：配额口径是"每个租户 9GB"，
// 展示必须与判定口径一致，否则管理员看到的余量与系统行为对不上。
package repository

import (
	"context"
	"database/sql"
	"fmt"

	"dandelion/server/internal/contract"
)

// UsageGroup 是一行分组用量。
//
// 分组键是项目 + 环境 + 平台：这是运营看数据的最小粒度——
// "哪个项目的哪个环境占得多"是唯一能指导清理决策的问题。
type UsageGroup struct {
	ProjectKey  string
	Environment string
	Platform    string
	BuildCount  int
	R2Bytes     int64
}

// StorageUsage 是一个分区内的用量快照。
type StorageUsage struct {
	TotalBytes   int64 // R2 实际占用
	BuildCount   int   // 记录总数（含已淘汰、不在 R2 的）
	R2BuildCount int   // 仍占用 R2 的包数，即参与配额竞争的数量
	PendingCount int   // 尚未同步到 R2
	FailedCount  int   // 同步失败，需人工关注

	// LocalBytes 是本机已归档包的总大小，只统计 local_state='present' 的记录。
	//
	// 不能简单累加 file_size：直传包的记录里有大小，但字节只在 R2、
	// 本机根本没落文件（实测 13 条记录合计 1478MB，磁盘只有约 1024MB）。
	// 按状态过滤才与磁盘实际占用对得上。
	LocalBytes   int64
	LocalBuilds  int // 本机已归档的包数
	LocalMissing int // 本机还没有副本的包数（等待拉回或拉回失败）

	Groups []UsageGroup
}

// StorageUsageOf 汇总一个分区内某个租户的存储用量。
//
// 用一次查询取五个标量、一次查询取分组，避免在 Go 里遍历全表：
// 租户的包可能上千条，但聚合结果只有几行。
func (r *BuildRepository) StorageUsageOf(ctx context.Context, tenantID string) (StorageUsage, error) {
	// 表名来自 Partition 常量，非用户输入，拼接安全；不能用 const 是因为 r.table 是运行时值。
	// 状态比较用参数绑定，避免把枚举值散落成裸字符串——改一处忘另一处是这类统计最常见的错。
	totals := `
SELECT COALESCE(SUM(b.r2_bytes), 0),
       COUNT(*),
       COUNT(*) FILTER (WHERE b.r2_bytes > 0),
       COUNT(*) FILTER (WHERE b.r2_state = $2),
       COUNT(*) FILTER (WHERE b.r2_state = $3),
       COALESCE(SUM(b.file_size) FILTER (WHERE b.local_state = $4), 0),
       COUNT(*) FILTER (WHERE b.local_state = $4),
       COUNT(*) FILTER (WHERE b.local_state <> $4)
FROM ` + r.table + ` b
JOIN ` + r.project + ` p ON p.id = b.project_id
WHERE p.tenant_id = $1`

	var usage StorageUsage
	err := r.db.QueryRowContext(ctx, totals, tenantID,
		contract.R2StatePending, contract.R2StateFailed, contract.LocalPresent).Scan(
		&usage.TotalBytes, &usage.BuildCount, &usage.R2BuildCount,
		&usage.PendingCount, &usage.FailedCount,
		&usage.LocalBytes, &usage.LocalBuilds, &usage.LocalMissing)
	if err != nil && err != sql.ErrNoRows {
		return StorageUsage{}, fmt.Errorf("统计存储用量失败: %w", err)
	}

	groups, err := r.storageGroupsOf(ctx, tenantID)
	if err != nil {
		return StorageUsage{}, err
	}
	usage.Groups = groups
	return usage, nil
}

// storageGroupsOf 按"项目 / 环境 / 平台"分组统计占用。
//
// 只列仍有 R2 占用的组：已被淘汰的包不再消耗空间，列出来只会让
// 管理员误以为还要清理。
func (r *BuildRepository) storageGroupsOf(ctx context.Context, tenantID string) ([]UsageGroup, error) {
	query := `
SELECT p.key, b.environment, b.platform, COUNT(*), SUM(b.r2_bytes)
FROM ` + r.table + ` b
JOIN ` + r.project + ` p ON p.id = b.project_id
WHERE p.tenant_id = $1 AND b.r2_bytes > 0
GROUP BY p.key, b.environment, b.platform
ORDER BY SUM(b.r2_bytes) DESC`

	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("统计分组用量失败: %w", err)
	}
	defer rows.Close()

	var groups []UsageGroup
	for rows.Next() {
		var g UsageGroup
		if err := rows.Scan(&g.ProjectKey, &g.Environment, &g.Platform, &g.BuildCount, &g.R2Bytes); err != nil {
			return nil, fmt.Errorf("读取分组用量失败: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

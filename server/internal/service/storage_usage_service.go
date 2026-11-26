// storage_usage_service.go · 存储用量的对外视图。
//
// 职责：合并两个分区的用量、换算配额余量与预计可上传数量，产出前端可直接渲染的 DTO。
// 输入：租户 id。
// 输出：contract.StorageUsage。
// 边界：只读；不触发淘汰、不改数据。
//
// 为什么需要这一层而不让 handler 直接查仓储：
// 配额换算（9GB 余额、可再传几个）属于业务口径，
// 若散落在 handler 里，将来改配额就得改接口层，而它本该对配额无感。
package service

import (
	"context"
	"sort"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// StorageUsage 汇总一个租户在两个分区里的存储用量。
//
// 必须合并两个分区：同一家公司在 team 与 solo 都可能有项目，
// 只统计一边会让展示的占用小于实际，与"每租户 9GB"的判定口径矛盾。
func (s *BuildService) StorageUsage(ctx context.Context, mode contract.SessionMode, tenantID string) (contract.StorageUsage, error) {
	usage := contract.StorageUsage{
		QuotaBytes:  retentionMaxBytes,
		MaxVersions: retentionMaxVersions,
		Groups:      []contract.StorageUsageGroup{},
	}
	if tenantID == "" {
		return usage, nil
	}

	// 两个分区各查一次再合并：仓储只管自己那张表，跨分区是 service 的职责。
	teamUsage, err := s.repos.For(contract.ModeTeam).StorageUsageOf(ctx, tenantID)
	if err != nil {
		return contract.StorageUsage{}, err
	}
	soloUsage, err := s.repos.For(contract.ModeSolo).StorageUsageOf(ctx, tenantID)
	if err != nil {
		return contract.StorageUsage{}, err
	}

	usage.UsedBytes = teamUsage.TotalBytes + soloUsage.TotalBytes
	usage.BuildCount = teamUsage.BuildCount + soloUsage.BuildCount
	usage.R2BuildCount = teamUsage.R2BuildCount + soloUsage.R2BuildCount
	usage.PendingCount = teamUsage.PendingCount + soloUsage.PendingCount
	usage.FailedCount = teamUsage.FailedCount + soloUsage.FailedCount

	// 两组各自有序，合并后必须重排：否则前端看到的"最占空间"可能落在列表中间。
	usage.Groups = mergeUsageGroups(teamUsage.Groups, soloUsage.Groups)

	// 剩余额度与可上传数量：让管理员不必自己算，直接看到"还能传几个"。
	usage.FreeBytes = usage.QuotaBytes - usage.UsedBytes
	if usage.FreeBytes < 0 {
		usage.FreeBytes = 0
	}
	usage.RemainingVersions = usage.MaxVersions - usage.R2BuildCount
	if usage.RemainingVersions < 0 {
		usage.RemainingVersions = 0
	}
	if usage.R2BuildCount > 0 && usage.UsedBytes > 0 {
		// 用现有包的平均体积估算还能传几个：比"剩余字节 / 某个固定值"更贴近实际。
		avg := usage.UsedBytes / int64(usage.R2BuildCount)
		if avg > 0 {
			usage.EstimatedUploads = int(usage.FreeBytes / avg)
		}
	}
	return usage, nil
}

// usageKey 是分组聚合的键。用独立类型而非直接拿 DTO 当 map key，
// 是因为 DTO 含 R2Bytes/BuildCount 等会变动的字段，拿它做键会把"同组不同值"拆成两行。
type usageKey struct {
	projectKey  string
	environment string
	platform    string
}

// mergeUsageGroups 合并两个分区的分组，并按占用从大到小重排。
//
// 两个分区的项目 key 可能重名（团队与单人各有一个 "mall"），
// 合并时按"项目+环境+平台"再聚合一次，避免同一行数据出现两次。
func mergeUsageGroups(team, solo []repository.UsageGroup) []contract.StorageUsageGroup {
	type agg struct {
		bytes int64
		count int
	}
	buckets := make(map[usageKey]*agg, len(team)+len(solo))
	add := func(group repository.UsageGroup) {
		key := usageKey{group.ProjectKey, group.Environment, group.Platform}
		if buckets[key] == nil {
			buckets[key] = &agg{}
		}
		buckets[key].bytes += group.R2Bytes
		buckets[key].count += group.BuildCount
	}

	for _, g := range team {
		add(g)
	}
	for _, g := range solo {
		add(g)
	}

	out := make([]contract.StorageUsageGroup, 0, len(buckets))
	for key, value := range buckets {
		out = append(out, contract.StorageUsageGroup{
			ProjectKey:  key.projectKey,
			Environment: key.environment,
			Platform:    key.platform,
			BuildCount:  value.count,
			R2Bytes:     value.bytes,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].R2Bytes != out[j].R2Bytes {
			return out[i].R2Bytes > out[j].R2Bytes
		}
		// 占用相同时按项目名排序，保证多次请求的展示顺序稳定。
		return out[i].ProjectKey < out[j].ProjectKey
	})
	return out
}

// ai_model_call_repository.go · AI 模型调用记录的唯一 SQL 出入口。
// 职责：追加一条调用事实、按时间区间聚合统计（含 FILTER 口径）、统计滑动窗口内的出站次数、删除过期记录。
// 输入：service 已归一好的 contract.AIModelCallRecord、已投影到单一公司的 scope、时间区间与分组维度。
// 输出：聚合后的计数 / 指针型 token 与耗时（NULL → nil），以及错误。
// 边界：固定绑定 team 分区；不做业务判定（不判断"该不该拦"、不比对思考档位）、
// 不取当前时间（窗口起止由调用方算好传入）、不格式化文案、不碰 Key。
package rdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// AIModelCallRepository 固定绑定 team 分区；调用记录是 team 专属事实，solo 无此表。
type AIModelCallRepository struct {
	db    *sql.DB
	table string
}

// NewAIModelCallRepository 构造调用记录仓储；表名只从 PartitionTeam 生成，不接受外部拼名。
func NewAIModelCallRepository(db *sql.DB) *AIModelCallRepository {
	return &AIModelCallRepository{db: db, table: repository.PartitionTeam.Table("rdc_ai_model_call")}
}

// Insert 追加一条调用事实。model_id 为空表示本地拒绝（限流 / 无匹配），落 NULL 而不造假 UUID。
// token 为 nil 时落 NULL：统计靠 tokens_reported 区分"未上报"与"确实是 0"。
func (r *AIModelCallRepository) Insert(ctx context.Context, record contract.AIModelCallRecord) error {
	statement := `INSERT INTO ` + r.table +
		` (tenant_id, model_id, model_name, model_identifier, protocol, scene, success, error_category,` +
		` status_code, prompt_tokens, completion_tokens, tokens_reported, latency_ms, used_upstream,` +
		` used_default, used_fallback, requested_capabilities, requested_min_context, requested_effort)` +
		` VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,` +
		` $15, $16, $17::jsonb, $18, $19)`
	_, err := r.db.ExecContext(ctx, statement,
		record.TenantID, record.ModelID, record.ModelName, record.ModelIdentifier, record.Protocol,
		record.Scene, record.Success, record.ErrorCategory, record.StatusCode,
		record.PromptTokens, record.CompletionTokens, record.TokensReported, record.LatencyMS,
		record.UsedUpstream, record.UsedDefault, record.UsedFallback,
		string(requestedCapabilitiesJSON(record.RequestedCapabilities)),
		record.RequestedMinContext, record.RequestedEffort,
	)
	if err != nil {
		return fmt.Errorf("写入 AI 模型调用记录失败: %w", err)
	}
	return nil
}

// requestedCapabilitiesJSON 只做序列化；nil 与空切片都落 '[]'，不留 SQL NULL。
func requestedCapabilitiesJSON(capabilities []string) []byte {
	if capabilities == nil {
		capabilities = []string{}
	}
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		return []byte("[]")
	}
	return encoded
}

// CountInWindow 返回 [now-windowMinutes, now) 内"已出站"的调用次数，供软限流判定。
// 为什么数 used_upstream 而不是 success：限流防的是对上游的实际压力。
//   - 上游回 429/5xx 的请求确实打出去了，算压力，要计入；
//   - 平台在出站前就拒掉的请求（即本函数拦下的那种）没出去，不算压力。
//
// 用 used_upstream 让"被拒不计入"成为数据口径的自然结果，而不是额外维护的特例判断；
// 它同时天然杜绝自激：reject 落库的记录 used_upstream=false，不会让下一分钟窗口越算越满。
// 正因为口径已由这一列表达，这里绝不能再叠 error_category != 'rate_limited'——那会造第二份语义副本。
func (r *AIModelCallRepository) CountInWindow(
	ctx context.Context, scope contract.Scope, since time.Time,
) (int, error) {
	// includeLocal=true 让 aiCallFilter 不再追加 used_upstream 条件，这里显式补上——
	// 窗口计数只有一个口径，不因调用方传参而变体。
	filter := aiCallFilter(0, scope, true).
		AddRaw(`c.used_upstream = true`).
		Add(`c.created_at >= ?`, since)
	var total int
	statement := `SELECT count(*) FROM ` + r.table + ` c` + filter.Where()
	if err := r.db.QueryRowContext(ctx, statement, filter.Args()...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计窗口内 AI 调用次数失败: %w", err)
	}
	return total, nil
}

// aiCallFilter 是隔离条件的唯一构造点：AI 调用记录是租户私有事实，
// 即便调用方误传 All，也绝不允许跨公司读写（失败关闭）。
// includeLocal 把"未出站"的记录挡在统计口径之外：默认只看真正打到上游的调用，
// 否则限流拒绝会自己把自己统计成一堆失败，把"上游成功率"变成噪声。
func aiCallFilter(base int, scope contract.Scope, includeLocal bool) *repository.Filter {
	filter := repository.ScopeFilter(base, "c", scope)
	if !includeLocal {
		filter.AddRaw(`c.used_upstream = true`)
	}
	return filter
}

// Aggregate 按 [query.Start, query.End) 聚合调用统计；分组维度可组合，未请求的维度返回空串。
//
// 口径全部写在一处（SQL 的 FILTER 子句），不在 Go 侧二次计算——这是"统计口径只有一份"的落点：
//   - 成功 / 失败：COUNT(*) FILTER (WHERE success) / (WHERE NOT success)
//   - 本地拒绝：COUNT(*) FILTER (WHERE NOT used_upstream)，默认口径下该列恒为 0
//   - 平均耗时：AVG(latency_ms) FILTER (WHERE success AND used_upstream)
//     失败请求的耗时取决于失败在多早发生（DNS 10ms vs 超时 30s），混在一起求平均没有业务含义
//   - token：SUM(...) FILTER (WHERE tokens_reported)，未上报返回 NULL 而不是 0
func (r *AIModelCallRepository) Aggregate(
	ctx context.Context, query contract.AIModelCallQuery, scope contract.Scope,
) (contract.AIModelCallStats, error) {
	stats, err := r.aggregateTotals(ctx, aiRangeFilter(scope, query))
	if err != nil {
		return contract.AIModelCallStats{}, err
	}
	// 失败分布是独立一次查询：它是"按 error_category 分组"的结果，
	// 与单行总量在 SQL 形状上不同（一个聚合到一行，一个多行），
	// 硬合并只能靠窗口函数或子查询，可读性远差于两次查询。
	categories, err := r.aggregateFailureCategories(ctx, aiFailureFilter(scope, query))
	if err != nil {
		return contract.AIModelCallStats{}, err
	}
	stats.FailureCategories = categories
	if len(query.GroupBy) == 0 {
		return stats, nil
	}
	groups, err := r.aggregateGroups(ctx, aiRangeFilter(scope, query), query)
	if err != nil {
		return contract.AIModelCallStats{}, err
	}
	stats.Groups = groups
	return stats, nil
}

// aiRangeFilter 是"区间 + 隔离 + 是否含本地拒绝"的公共条件构造器。
// 每次调用都新建一个 Filter：Filter 是可变构建器，复用同一个实例会让后加的失败/分组条件
// 串到别的查询里——这类"条件泄漏"不会编译报错，只会让统计数字悄悄变错。
func aiRangeFilter(scope contract.Scope, query contract.AIModelCallQuery) *repository.Filter {
	return aiCallFilter(0, scope, query.IncludeLocal).
		Add(`c.created_at >= ?`, query.Start).
		Add(`c.created_at < ?`, query.End)
}

// aiFailureFilter 在区间条件上再限定"失败且类别非空"，只供失败分布使用。
//
// 为什么排除空串类别：成功行的 error_category 就是空串。若历史脏数据里出现
// "success 与 category 不一致"，不排除就会在失败分布里冒出一个无名字的桶，
// 比漏统计更不可解释。
func aiFailureFilter(scope contract.Scope, query contract.AIModelCallQuery) *repository.Filter {
	return aiRangeFilter(scope, query).
		AddRaw(`NOT c.success`).
		AddRaw(`c.error_category <> ''`)
}

// aggregateFailureCategories 按 error_category 统计失败条数，供前端"失败分布"列表。
// 排序按条数倒序、同数按类别名升序：让最主要的失败原因稳定在第一行，
// 同一份数据每次刷新顺序一致（不依赖 PG 的物理行序）。
func (r *AIModelCallRepository) aggregateFailureCategories(
	ctx context.Context, filter *repository.Filter,
) ([]contract.AICallCategoryCount, error) {
	statement := `SELECT c.error_category, count(*) FROM ` + r.table + ` c` + filter.Where() +
		` GROUP BY c.error_category ORDER BY count(*) DESC, c.error_category ASC`
	rows, err := r.db.QueryContext(ctx, statement, filter.Args()...)
	if err != nil {
		return nil, fmt.Errorf("聚合 AI 调用失败分类失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	categories := make([]contract.AICallCategoryCount, 0)
	for rows.Next() {
		var item contract.AICallCategoryCount
		if err := rows.Scan(&item.Category, &item.Count); err != nil {
			return nil, fmt.Errorf("读取 AI 调用失败分类失败: %w", err)
		}
		categories = append(categories, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return categories, nil
}

// 统计口径的唯一定义处：每条度量是一个命名常量，总量与分组各取所需，
// 但同一个 FILTER 表达式全仓只在这里出现一次（改口径只改一处）。
//
//   - 成功 / 失败：COUNT(*) FILTER (WHERE success) / (WHERE NOT success)
//   - 本地拒绝：FILTER (WHERE NOT used_upstream)，默认口径下恒为 0
//   - 平均耗时：AVG(latency_ms) FILTER (WHERE success AND used_upstream)
//     失败请求的耗时取决于失败在多早发生（DNS 10ms vs 超时 30s），混在一起求平均没有业务含义
//   - token：SUM(...) FILTER (WHERE tokens_reported)，未上报返回 NULL 而不是 0
const (
	aiMetricTotalCalls       = `count(*)`
	aiMetricSuccessCalls     = `count(*) FILTER (WHERE c.success)`
	aiMetricFailedCalls      = `count(*) FILTER (WHERE NOT c.success)`
	aiMetricLocalRejected    = `count(*) FILTER (WHERE NOT c.used_upstream)`
	aiMetricPromptTokens     = `sum(c.prompt_tokens) FILTER (WHERE c.tokens_reported)`
	aiMetricCompletionTokens = `sum(c.completion_tokens) FILTER (WHERE c.tokens_reported)`
	aiMetricTokensUnreported = `count(*) FILTER (WHERE NOT c.tokens_reported)`
	// AVG(bigint) 在 PG 里返回 numeric（而非 bigint），pgx 会把它当字符串扫进 *int64 直接报
	// "converting driver.Value type string ... to a int64"。必须显式 round + ::bigint 收成整数毫秒；
	// 用 round 而不是截断，让 0.5ms 进到 1 而不是被抹平（耗时是给人看的大致量级，取整方向无所谓对错，
	// 但四舍五入与人在别处看到的数字更一致）。
	aiMetricAvgLatency = `round(avg(c.latency_ms) FILTER (WHERE c.success AND c.used_upstream))::bigint`
)

// aiCallTotalsColumns 是总量查询的列序列，顺序即扫描顺序，两者必须同步改。
const aiCallTotalsColumns = aiMetricTotalCalls + ` AS total_calls, ` +
	aiMetricSuccessCalls + ` AS success_calls, ` +
	aiMetricFailedCalls + ` AS failed_calls, ` +
	aiMetricLocalRejected + ` AS local_rejected, ` +
	aiMetricPromptTokens + ` AS prompt_tokens, ` +
	aiMetricCompletionTokens + ` AS completion_tokens, ` +
	aiMetricTokensUnreported + ` AS tokens_unreported, ` +
	aiMetricAvgLatency + ` AS avg_success_latency`

// aiCallGroupColumns 是分组查询的列序列：去掉只对总量有意义的两个计数列，
// 其余表达式与总量逐字复用（不是另写一份近似 SQL）。
const aiCallGroupColumns = aiMetricTotalCalls + ` AS total_calls, ` +
	aiMetricSuccessCalls + ` AS success_calls, ` +
	aiMetricFailedCalls + ` AS failed_calls, ` +
	aiMetricPromptTokens + ` AS prompt_tokens, ` +
	aiMetricCompletionTokens + ` AS completion_tokens, ` +
	aiMetricAvgLatency + ` AS avg_success_latency`

// aggregateTotals 取总体一行；无记录时 COUNT 为 0、SUM/AVG 为 NULL（指针保持 nil）。
func (r *AIModelCallRepository) aggregateTotals(
	ctx context.Context, filter *repository.Filter,
) (contract.AIModelCallStats, error) {
	statement := `SELECT ` + aiCallTotalsColumns + ` FROM ` + r.table + ` c` + filter.Where()
	var stats contract.AIModelCallStats
	err := r.db.QueryRowContext(ctx, statement, filter.Args()...).Scan(
		&stats.TotalCalls, &stats.SuccessCalls, &stats.FailedCalls, &stats.LocalRejected,
		&stats.PromptTokens, &stats.CompletionTokens, &stats.TokensUnreported, &stats.AvgSuccessLatencyMS,
	)
	if err != nil {
		return contract.AIModelCallStats{}, fmt.Errorf("聚合 AI 调用统计失败: %w", err)
	}
	return stats, nil
}

// aiGroupColumns 把被请求的分组维度翻成 SELECT 与 GROUP BY 的列表达式。
// 顺序固定为 model → protocol → scene → day，保证列顺序与下游读取顺序一致。
func aiGroupColumns(groupBy []string) (selects, keys []string) {
	for _, dimension := range groupBy {
		switch dimension {
		case contract.AIUsageGroupModel:
			selects = append(selects, `coalesce(c.model_id::text, '')`, `c.model_name`)
			keys = append(keys, `coalesce(c.model_id::text, '')`, `c.model_name`)
		case contract.AIUsageGroupProtocol:
			selects = append(selects, `c.protocol`)
			keys = append(keys, `c.protocol`)
		case contract.AIUsageGroupScene:
			selects = append(selects, `c.scene`)
			keys = append(keys, `c.scene`)
		case contract.AIUsageGroupDay:
			// created_at 统一按 UTC 取日：跨时区的"日"必须有一个共同基准，否则同一天会被切碎。
			day := `to_char(c.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')`
			selects = append(selects, day)
			keys = append(keys, day)
		}
	}
	return selects, keys
}

// aggregateGroups 按请求维度分组；每组都带完整的度量列（与总量同一份 aiCallMetrics）。
func (r *AIModelCallRepository) aggregateGroups(
	ctx context.Context, filter *repository.Filter, query contract.AIModelCallQuery,
) ([]contract.AICallGroup, error) {
	selects, keys := aiGroupColumns(query.GroupBy)
	if len(selects) == 0 {
		return []contract.AICallGroup{}, nil
	}
	order := ` ORDER BY total_calls DESC`
	if len(keys) > 0 {
		order += `, ` + strings.Join(keys, ", ")
	}
	statement := `SELECT ` + strings.Join(selects, ", ") + `, ` + aiCallGroupColumns +
		` FROM ` + r.table + ` c` + filter.Where() +
		` GROUP BY ` + strings.Join(keys, ", ") + order
	rows, err := r.db.QueryContext(ctx, statement, filter.Args()...)
	if err != nil {
		return nil, fmt.Errorf("分组聚合 AI 调用统计失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	groups := make([]contract.AICallGroup, 0)
	for rows.Next() {
		group, scanErr := scanAICallGroup(rows, query)
		if scanErr != nil {
			return nil, scanErr
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

// scanAICallGroup 按请求维度顺序吸收分组列，未请求的维度在结构体里保持空串。
func scanAICallGroup(scanner aiScanner, query contract.AIModelCallQuery) (contract.AICallGroup, error) {
	var group contract.AICallGroup
	dest := make([]any, 0, 6)
	if query.HasGroup(contract.AIUsageGroupModel) {
		dest = append(dest, &group.ModelID, &group.ModelName)
	}
	if query.HasGroup(contract.AIUsageGroupProtocol) {
		dest = append(dest, &group.Protocol)
	}
	if query.HasGroup(contract.AIUsageGroupScene) {
		dest = append(dest, &group.Scene)
	}
	if query.HasGroup(contract.AIUsageGroupDay) {
		dest = append(dest, &group.Day)
	}
	// 顺序必须与 aiCallGroupColumns 一致：total, success, failed, prompt, completion, avg_latency。
	dest = append(dest, &group.TotalCalls, &group.SuccessCalls, &group.FailedCalls,
		&group.PromptTokens, &group.CompletionTokens, &group.AvgSuccessLatencyMS,
	)
	if err := scanner.Scan(dest...); err != nil {
		return contract.AICallGroup{}, fmt.Errorf("读取 AI 调用分组失败: %w", err)
	}
	return group, nil
}

// DeleteExpired 删除 created_at < before 的记录，返回删除条数。
// 本设计只提供方法与单测，不接任何定时器（见设计 §9.1 条目 2），因此未接调度前不会误删。
func (r *AIModelCallRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM `+r.table+` WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("清理过期 AI 调用记录失败: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取清理条数失败: %w", err)
	}
	return deleted, nil
}

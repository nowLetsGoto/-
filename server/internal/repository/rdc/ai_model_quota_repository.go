// ai_model_quota_repository.go · AI 模型软限流阈值的唯一 SQL 出入口。
// 职责：按公司读一行阈值配置、整行覆盖写入（不存在则插入）。
// 输入：已投影到单一公司的 scope、service 校验过的 contract.AIQuotaInput。
// 输出：contract.AIQuotaConfig（含 extra_config 原文与 updated_at）或错误。
// 边界：固定绑定 team 分区；不补默认值（由 contract.AIQuotaDefaults 统一补）、
// 不判断是否超阈值、不取当前时间做成窗口判断、不写审计。
package rdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// AIModelQuotaRepository 固定绑定 team 分区；阈值是每公司一行的配置，solo 无此表。
type AIModelQuotaRepository struct {
	db    *sql.DB
	table string
}

// NewAIModelQuotaRepository 构造阈值仓储；表名只从 PartitionTeam 生成。
func NewAIModelQuotaRepository(db *sql.DB) *AIModelQuotaRepository {
	return &AIModelQuotaRepository{db: db, table: repository.PartitionTeam.Table("rdc_ai_model_quota")}
}

// aiQuotaColumns 是读取列序：租户、四项阈值、扩展配置原文、更新时间。
const aiQuotaColumns = ` q.enabled, q.max_calls, q.window_minutes, q.action,` +
	` coalesce(q.extra_config::text, '{}'), q.updated_at`

// Find 读取本公司的阈值行；无行时原样返回 sql.ErrNoRows。
// 为什么不在仓储补默认值：默认值是契约（AIQuotaDefaults），补默认属于读路径的业务口径，
// 由 service 统一做一次即可；仓储补一份会让"默认值"出现两个来源。
func (r *AIModelQuotaRepository) Find(ctx context.Context, scope contract.Scope) (contract.AIQuotaConfig, error) {
	filter := aiQuotaFilter(0, scope)
	var cfg contract.AIQuotaConfig
	var extraText string
	var updatedAt time.Time
	statement := `SELECT ` + aiQuotaColumns + ` FROM ` + r.table + ` q` + filter.Where()
	err := r.db.QueryRowContext(ctx, statement, filter.Args()...).Scan(
		&cfg.Enabled, &cfg.MaxCalls, &cfg.WindowMinutes, &cfg.Action, &extraText, &updatedAt,
	)
	if err != nil {
		return contract.AIQuotaConfig{}, err
	}
	cfg.ExtraConfig = json.RawMessage(extraText)
	cfg.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return cfg, nil
}

// Upsert 整行覆盖：有行则更新四项 + extra_config + updated_at，无行则插入。
// 整行覆盖而非部分更新是有意的：阈值只有四项，部分更新会引入
// "只改了 maxCalls" 的读取-合并-写入竞态，整行覆盖让语义无歧义。
// extra_config 只在入参非空时覆盖，其余情况保留原值——它承载的是当前接口不认识的扩展键，
// 客户端传空不该把它们清掉（那是隐性数据丢失）。
func (r *AIModelQuotaRepository) Upsert(
	ctx context.Context, scope contract.Scope, input contract.AIQuotaInput, extra json.RawMessage,
) (contract.AIQuotaConfig, error) {
	if !contract.IsValidUUID(scope.TenantID) || scope.All {
		return contract.AIQuotaConfig{}, &contract.ValidationError{Message: "目标公司标识不合法"}
	}
	var extraArg any
	if len(extra) > 0 && string(extra) != "null" {
		extraArg = string(extra)
	}
	statement := `INSERT INTO ` + r.table +
		` (tenant_id, enabled, max_calls, window_minutes, action, extra_config)` +
		` VALUES ($1::uuid, $2, $3, $4, $5, coalesce($6::jsonb, '{}'::jsonb))` +
		` ON CONFLICT (tenant_id) DO UPDATE SET` +
		` enabled = EXCLUDED.enabled, max_calls = EXCLUDED.max_calls,` +
		` window_minutes = EXCLUDED.window_minutes, action = EXCLUDED.action,` +
		` extra_config = CASE WHEN $6::jsonb IS NULL THEN ` + r.table + `.extra_config` +
		` ELSE EXCLUDED.extra_config END,` +
		` updated_at = now()`
	_, err := r.db.ExecContext(ctx, statement,
		scope.TenantID, input.Enabled, input.MaxCalls, input.WindowMinutes, input.Action, extraArg)
	if err != nil {
		return contract.AIQuotaConfig{}, fmt.Errorf("写入 AI 模型调用阈值失败: %w", err)
	}
	return r.Find(ctx, scope)
}

// aiQuotaFilter 是隔离条件的唯一构造点；阈值是租户私有配置，All 范围也绝不放行（失败关闭）。
func aiQuotaFilter(base int, scope contract.Scope) *repository.Filter {
	filter := repository.ScopeFilter(base, "q", scope)
	if filter.Cond() == "" {
		filter.AddRaw(`1 = 0`)
	}
	return filter
}

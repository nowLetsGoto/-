// ai_model_stats_service.go · 「AI模型」统计查询与软限流阈值的业务层。
// 职责：把仓储的聚合结果补成可直接上屏的统计响应（附阈值快照与超限状态），以及阈值的读/整行写。
// 输入：已投影到单一公司的 scope、handler 解析出的查询条件与已校验的 AIQuotaInput。
// 输出：contract.AIModelCallStats / AIQuotaConfig，或可预期业务错误。
// 边界：不碰 *http.Request、不写 SQL、不判定"该不该拦某次调用"（判定在 chat_quota.go）、不取审计。
package rdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"dandelion/server/internal/contract"
)

// ModelStatsService 依赖两个只读/低频写的仓储：调用记录（聚合）与阈值（读写）。
// 依赖声明为接口而非具体类型，理由与 ModelChatService 相同：统计口径可脱离数据库单测。
type ModelStatsService struct {
	calls  AIModelCallRepository
	quotas AIQuotaRepository
}

// NewModelStatsService 构造统计服务；两个仓储都由 main 注入同一 *sql.DB。
func NewModelStatsService(calls AIModelCallRepository, quotas AIQuotaRepository) *ModelStatsService {
	return &ModelStatsService{calls: calls, quotas: quotas}
}

// ResolveScope 复用配置服务的超管代配投影口径：统计与阈值是租户私有数据，
// scope.All 时必须先落成单一公司，否则仓储的失败关闭会让查询变成空集而不是"看全部"。
func (s *ModelStatsService) ResolveScope(scope contract.Scope, tenantID string) (contract.Scope, error) {
	return resolveModelScope(scope, tenantID)
}

// Usage 查询 [Start, End) 区间的调用统计，并附上当前阈值快照与超限状态。
// 超管代配的 ?tenantId= 由 handler 先经 ResolveScope 投影，这里只接受单一公司 scope。
func (s *ModelStatsService) Usage(
	ctx context.Context, scope contract.Scope, query contract.AIModelCallQuery, now time.Time,
) (contract.AIModelCallStats, error) {
	normalized, err := contract.NormalizeAIModelCallQuery(query, now)
	if err != nil {
		return contract.AIModelCallStats{}, err
	}
	stats, err := s.calls.Aggregate(ctx, normalized, scope)
	if err != nil {
		return contract.AIModelCallStats{}, err
	}
	// 无数据时仓储返回 nil 切片；统一成空数组，避免前端拿到 null 后还要判空。
	if stats.Groups == nil {
		stats.Groups = []contract.AICallGroup{}
	}
	if stats.FailureCategories == nil {
		stats.FailureCategories = []contract.AICallCategoryCount{}
	}
	quota, err := s.GetQuota(ctx, scope)
	if err != nil {
		return contract.AIModelCallStats{}, err
	}
	stats.Quota = quota
	stats.QuotaState, err = s.quotaState(ctx, scope, quota, now)
	if err != nil {
		return contract.AIModelCallStats{}, err
	}
	return stats, nil
}

// quotaState 只供界面提示条使用：enabled 且窗口内已出站次数达上限 → exceeded。
// 它不参与任何放行决策——决策永远走 decideQuota，避免"展示态"与"真实判定"两套规则。
func (s *ModelStatsService) quotaState(
	ctx context.Context, scope contract.Scope, quota contract.AIQuotaConfig, now time.Time,
) (string, error) {
	if !quota.Enabled {
		return contract.AIQuotaStateOK, nil
	}
	since := now.Add(-time.Duration(quota.WindowMinutes) * time.Minute)
	used, err := s.calls.CountInWindow(ctx, scope, since)
	if err != nil {
		return "", err
	}
	if used >= quota.MaxCalls {
		return contract.AIQuotaStateExceeded, nil
	}
	return contract.AIQuotaStateOK, nil
}

// GetQuota 读本公司阈值；从未配置过时返回契约默认值（默认关闭），绝不返回零值。
// 返回值里的 UpdatedAt 为空串即表示"当前展示的是默认值"，前端据此提示"尚未配置"。
func (s *ModelStatsService) GetQuota(ctx context.Context, scope contract.Scope) (contract.AIQuotaConfig, error) {
	cfg, err := s.quotas.Find(ctx, scope)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.AIQuotaDefaults(), nil
	}
	if err != nil {
		return contract.AIQuotaConfig{}, err
	}
	// 历史脏行（如非法 action）在读路径宽容补齐，不让统计页因配置问题 500。
	return contract.NormalizeAIQuota(cfg), nil
}

// UpdateQuota 整行覆盖写阈值，并保留 extra_config 里的既有扩展键（retentionDays 等）。
// 保留而非清空是有意的：本次接口不认识那些键，客户端传空不该把它们当成"删除指令"。
func (s *ModelStatsService) UpdateQuota(
	ctx context.Context, scope contract.Scope, input contract.AIQuotaInput,
) (contract.AIQuotaConfig, error) {
	validated, err := contract.ValidateAIQuotaInput(input)
	if err != nil {
		return contract.AIQuotaConfig{}, err
	}
	current, findErr := s.quotas.Find(ctx, scope)
	extra := current.ExtraConfig
	if findErr != nil && !errors.Is(findErr, sql.ErrNoRows) {
		return contract.AIQuotaConfig{}, findErr
	}
	return s.quotas.Upsert(ctx, scope, validated, json.RawMessage(extra))
}

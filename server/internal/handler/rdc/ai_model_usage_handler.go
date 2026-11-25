// ai_model_usage_handler.go · 「AI模型」统计与阈值的 HTTP 语义层。
// 职责：解析查询参数 / 严格白名单解码请求体、调用统计服务、阈值写操作记一条审计、统一响应外壳。
// 输入：HTTP 请求与上下文中的身份 / scope；输出：{code,data,message} JSON（阈值写返回 200）。
// 边界：不写 SQL、不做统计口径计算（在 SQL 的 FILTER）、不判定是否超限（在 chat_quota.go）、
// 请求体白名单**不含** tenantId —— 超管代配一律走 ?tenantId=，避免 body/query 两处来源打架。
package rdc

import (
	"context"
	"net/http"
	"strings"
	"time"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	rdcsvc "dandelion/server/internal/service/rdc"
)

// aiQuotaActionUpdated 是阈值更新唯一的审计动作名；与 P0 的 7 类模型动作并列。
const aiQuotaActionUpdated = "rdc.ai_model.quota_updated"

// aiStatsService 是 handler 依赖的最小业务接口，便于注入 fake 做严格字段（含审计）测试。
type aiStatsService interface {
	Usage(ctx context.Context, scope contract.Scope, query contract.AIModelCallQuery, now time.Time) (
		contract.AIModelCallStats, error)
	GetQuota(ctx context.Context, scope contract.Scope) (contract.AIQuotaConfig, error)
	UpdateQuota(ctx context.Context, scope contract.Scope, input contract.AIQuotaInput) (
		contract.AIQuotaConfig, error)
	ResolveScope(scope contract.Scope, tenantID string) (contract.Scope, error)
}

// auditRecorder 是审计依赖的最小端口（设计 §3.7 类图里的 `auditor`）。
// 声明成接口而不是直接依赖具体服务，是为了让"审计 detail 不含 Key、含 changedFields"
// 这类断言能在不连数据库的前提下被测试锁死——审计内容本身是契约的一部分。
type auditRecorder interface {
	Record(ctx context.Context, actor contract.Identity, action, objectType, objectID string,
		detail map[string]any, ip string)
}

// AIModelUsageHandler 依赖统计服务与审计端口；scope 投影复用配置服务的同一口径。
type AIModelUsageHandler struct {
	svc   aiStatsService
	audit auditRecorder
}

// NewAIModelUsageHandler 构造统计与阈值处理器；audit 允许为 nil（禁用审计的部署形态）。
func NewAIModelUsageHandler(svc aiStatsService, audit auditRecorder) *AIModelUsageHandler {
	return &AIModelUsageHandler{svc: svc, audit: audit}
}

// GetUsage：时间区间半开 `[start,end)`，缺省最近 7 天；groupBy 逗号分隔可组合。
// includeLocal=true 时统计里才出现"本地拒绝"这一类，默认不含（默认口径只看真正出站的调用）。
func (h *AIModelUsageHandler) GetUsage(w http.ResponseWriter, r *http.Request) error {
	scope, err := h.svc.ResolveScope(authctx.ScopeFrom(r.Context()), r.URL.Query().Get("tenantId"))
	if err != nil {
		return aiHandlerError(err)
	}
	query, err := decodeUsageQuery(r)
	if err != nil {
		return err
	}
	stats, err := h.svc.Usage(r.Context(), scope, query, time.Now())
	if err != nil {
		return aiHandlerError(err)
	}
	return handler.OK(w, stats)
}

// GetQuota 读阈值；从未配置过时返回默认值（enabled=false），前端据此提示"尚未配置"。
func (h *AIModelUsageHandler) GetQuota(w http.ResponseWriter, r *http.Request) error {
	scope, err := h.svc.ResolveScope(authctx.ScopeFrom(r.Context()), r.URL.Query().Get("tenantId"))
	if err != nil {
		return aiHandlerError(err)
	}
	cfg, err := h.svc.GetQuota(r.Context(), scope)
	if err != nil {
		return aiHandlerError(err)
	}
	return handler.OK(w, cfg)
}

// UpdateQuota 整行覆盖写阈值；成功分支记一条审计，detail 含变更字段与前后值（绝不含 Key）。
func (h *AIModelUsageHandler) UpdateQuota(w http.ResponseWriter, r *http.Request) error {
	var in contract.AIQuotaInput
	if err := handler.DecodeJSON(w, r, &in, "enabled/maxCalls/windowMinutes/action"); err != nil {
		return err
	}
	ctx := r.Context()
	scope := authctx.ScopeFrom(ctx)
	tenantID := r.URL.Query().Get("tenantId")
	resolved, err := h.svc.ResolveScope(scope, tenantID)
	if err != nil {
		return aiHandlerError(err)
	}
	before, readErr := h.svc.GetQuota(ctx, resolved)
	if readErr != nil {
		return aiHandlerError(readErr)
	}
	cfg, err := h.svc.UpdateQuota(ctx, resolved, in)
	if err != nil {
		return aiHandlerError(err)
	}
	h.record(ctx, authctx.From(ctx), r, scope, tenantID, before, cfg)
	return handler.OK(w, cfg)
}

// decodeUsageQuery 解析统计查询参数；时间缺失交给 contract 补默认区间，非法格式当场 400。
func decodeUsageQuery(r *http.Request) (contract.AIModelCallQuery, error) {
	params := r.URL.Query()
	query := contract.AIModelCallQuery{}
	start, err := parseOptionalTime(params.Get("start"), "start")
	if err != nil {
		return contract.AIModelCallQuery{}, err
	}
	end, err := parseOptionalTime(params.Get("end"), "end")
	if err != nil {
		return contract.AIModelCallQuery{}, err
	}
	query.Start, query.End = start, end
	query.GroupBy = splitGroupBy(params.Get("groupBy"))
	includeLocal, err := parseStrictBool(params.Get("includeLocal"), "includeLocal")
	if err != nil {
		return contract.AIModelCallQuery{}, err
	}
	query.IncludeLocal = includeLocal
	return query, nil
}

// parseOptionalTime 解析可选时间参数：空串 = 未传（返回零值，由 contract 补默认）；
// 非空则必须是 RFC3339。为什么不接受 "2026-01-01" 这种简写：那会被当成本地时区的某个瞬间，
// 而统计口径是 UTC 半开区间，让用户以为查了一整天却在边界上少几个小时是最难自查的错误。
func parseOptionalTime(raw, field string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, handler.BadRequest(field + " 必须是 RFC3339 时间")
	}
	return parsed, nil
}

// splitGroupBy 把逗号分隔的分组维度拆成切片，顺带丢掉空白项（"model,," 不该产生空维度）。
// 维度合法性由 contract.NormalizeAIModelCallQuery 统一裁决，这里不重复白名单。
func splitGroupBy(raw string) []string {
	if raw == "" {
		return nil
	}
	dimensions := make([]string, 0, len(contract.AIUsageGroupBy))
	for _, item := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			dimensions = append(dimensions, trimmed)
		}
	}
	return dimensions
}

// parseStrictBool 只认 "true" / "false"（空串视为未传）。
// 为什么不用 strconv.ParseBool：它会额外接受 1/0/t/f/T/F/TRUE，于是 includeLocal=1 会静默生效，
// 而同一个客户端传 includeLocal=2 又报错——同一类输入一半成功一半失败，比全都不接受更难排查。
func parseStrictBool(raw, field string) (bool, error) {
	switch raw {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, handler.BadRequest(field + " 只能是 true 或 false")
	}
}

// record 写阈值变更审计；WithoutCancel 让客户端断开后仍能写完，审计失败只记日志不回滚。
func (h *AIModelUsageHandler) record(
	ctx context.Context, identity contract.Identity, r *http.Request,
	scope contract.Scope, tenantID string, before, after contract.AIQuotaConfig,
) {
	if h.audit == nil {
		return
	}
	detail := map[string]any{
		"changedFields": aiQuotaChangedFields(before, after),
		"before":        aiQuotaAuditValues(before),
		"after":         aiQuotaAuditValues(after),
	}
	if scope.All {
		detail["targetTenantID"] = tenantID
		detail["delegated"] = true
	}
	h.audit.Record(context.WithoutCancel(ctx), identity, aiQuotaActionUpdated, aiObjectType,
		afterActionID(tenantID, scope), detail, handler.ClientIP(r))
}

// afterActionID 是审计对象 id：阈值没有行主键，用"目标公司"作对象标识最可追溯。
func afterActionID(tenantID string, scope contract.Scope) string {
	if scope.All {
		return tenantID
	}
	return scope.TenantID
}

// aiQuotaChangedFields 只比较四项可见阈值，避免把 extra_config 原文写进审计。
func aiQuotaChangedFields(before, after contract.AIQuotaConfig) []string {
	type field struct {
		name  string
		left  any
		right any
	}
	fields := []field{
		{"enabled", before.Enabled, after.Enabled},
		{"maxCalls", before.MaxCalls, after.MaxCalls},
		{"windowMinutes", before.WindowMinutes, after.WindowMinutes},
		{"action", before.Action, after.Action},
	}
	changed := make([]string, 0, len(fields))
	for _, item := range fields {
		if item.left != item.right {
			changed = append(changed, item.name)
		}
	}
	return changed
}

// aiQuotaAuditValues 是审计里的阈值快照；只含四项配置，不含任何 Key / 扩展原文。
func aiQuotaAuditValues(cfg contract.AIQuotaConfig) map[string]any {
	return map[string]any{
		"enabled": cfg.Enabled, "maxCalls": cfg.MaxCalls,
		"windowMinutes": cfg.WindowMinutes, "action": cfg.Action,
	}
}

// 编译期断言：统计服务必须满足本 handler 的依赖口径（含超管代配投影方法）。
var _ aiStatsService = (*rdcsvc.ModelStatsService)(nil)

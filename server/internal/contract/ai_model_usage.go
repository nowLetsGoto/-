// ai_model_usage.go · 研发中心「AI模型」P1 的统计与软限流契约。
// 职责：定义调用统计的查询条件 / 响应 DTO、阈值配置的读写 DTO 与默认值、分组维度与保留期常量，
// 以及这些 DTO 上的纯校验/规范化函数。
// 输入：handler 解析出的查询参数与请求体；仓储返回的聚合结果。
// 输出：跨层共用的 contract 类型与纯函数；不返回任何 Key、密文或上游原文。
// 边界：不访问 DB / HTTP / 环境变量，不做聚合计算（聚合在 SQL 的 Aggregate 一处），
// 不做限流判定（判定在 service/rdc/chat_quota.go 的 decideQuota）。
package contract

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

const (
	// AIRetentionDaysDefault 是调用记录的默认保留天数；清理任务未接调度前不会误删。
	AIRetentionDaysDefault = 90
	AIRetentionDaysMin     = 7
	AIRetentionDaysMax     = 365

	// AIUsageDefaultRangeDays 是统计的默认时间跨度（end = 当前整点）。
	AIUsageDefaultRangeDays = 7

	AIQuotaDefaultMaxCalls      = 1000
	AIQuotaDefaultWindowMinutes = 60

	AIQuotaActionWarn   = "warn"
	AIQuotaActionReject = "reject"

	// AIQuotaStateOK / Exceeded 是响应里的阈值状态；两者都只是"展示态"，判定仍以 decideQuota 为准。
	AIQuotaStateOK       = "ok"
	AIQuotaStateExceeded = "exceeded"

	// AIUsageGroupModel / Protocol / Scene / Day 是可组合的分组维度，与 x-rdc-ai-usage.groupBy 同序。
	AIUsageGroupModel    = "model"
	AIUsageGroupProtocol = "protocol"
	AIUsageGroupScene    = "scene"
	AIUsageGroupDay      = "day"
)

// AIUsageGroupBy 是分组维度的白名单；顺序即前端展示顺序。
var AIUsageGroupBy = []string{AIUsageGroupModel, AIUsageGroupProtocol, AIUsageGroupScene, AIUsageGroupDay}

// AIQuotaActions 是超阈值动作的白名单，与 x-rdc-ai-quota.actions 同序。
var AIQuotaActions = []string{AIQuotaActionWarn, AIQuotaActionReject}

// AIQuotaConfig 是阈值配置的读取投影；无配置行时由 AIQuotaDefaults() 补齐，绝不返回零值。
// ExtraConfig 原样保留未知键：加新配置项不改 DDL，只在解析侧加字段。
// UpdatedAt 为空串表示"从未配置过，当前展示的是默认值"——不是"刚刚更新"。
type AIQuotaConfig struct {
	Enabled       bool            `json:"enabled"`
	MaxCalls      int             `json:"maxCalls"`
	WindowMinutes int             `json:"windowMinutes"`
	Action        string          `json:"action"`
	ExtraConfig   json.RawMessage `json:"-"`
	UpdatedAt     string          `json:"updatedAt"`
}

// AIQuotaInput 是阈值的写请求体（整行覆盖）；无 tenantId（超管用 ?tenantId=）。
type AIQuotaInput struct {
	Enabled       bool   `json:"enabled"`
	MaxCalls      int    `json:"maxCalls"`
	WindowMinutes int    `json:"windowMinutes"`
	Action        string `json:"action"`
}

// AIQuotaDefaults 是默认阈值的唯一来源：默认关闭，避免升级后突然拒绝调用。
func AIQuotaDefaults() AIQuotaConfig {
	return AIQuotaConfig{
		Enabled:       false,
		MaxCalls:      AIQuotaDefaultMaxCalls,
		WindowMinutes: AIQuotaDefaultWindowMinutes,
		Action:        AIQuotaActionWarn,
	}
}

// NormalizeAIQuota 把读取到（或缺失）的配置补齐为可用值：
// 零值字段回落到默认值，非法 action 回落到 warn——读路径不因历史脏数据报错。
func NormalizeAIQuota(cfg AIQuotaConfig) AIQuotaConfig {
	defaults := AIQuotaDefaults()
	if cfg.MaxCalls <= 0 {
		cfg.MaxCalls = defaults.MaxCalls
	}
	if cfg.WindowMinutes <= 0 {
		cfg.WindowMinutes = defaults.WindowMinutes
	}
	if !slices.Contains(AIQuotaActions, cfg.Action) {
		cfg.Action = defaults.Action
	}
	return cfg
}

// ValidateAIQuotaInput 校验写请求：四个字段都必须显式给出，越界一律 400。
// 为什么要求整行覆盖而非部分更新：阈值只有四项，部分更新会让"我只改了 maxCalls"变成
// 一个隐式的读取-合并-写入竞态，整行覆盖让语义无歧义。
func ValidateAIQuotaInput(in AIQuotaInput) (AIQuotaInput, error) {
	if in.MaxCalls < 1 || in.MaxCalls > 1000000 {
		return AIQuotaInput{}, &ValidationError{Message: "每窗口最大调用次数必须在 1–1000000 之间"}
	}
	if in.WindowMinutes < 1 || in.WindowMinutes > 10080 {
		return AIQuotaInput{}, &ValidationError{Message: "窗口长度必须在 1–10080 分钟之间"}
	}
	if !slices.Contains(AIQuotaActions, in.Action) {
		return AIQuotaInput{}, &ValidationError{Message: "超限动作只能是 warn 或 reject"}
	}
	return in, nil
}

// AIRetentionDays 从阈值配置的 extra_config 解析保留天数：
// 缺失 / 非法一律回落到默认 90 天（读路径宽容），合法区间 [7, 365]。
func AIRetentionDays(extra json.RawMessage) int {
	if len(extra) == 0 || string(extra) == "null" {
		return AIRetentionDaysDefault
	}
	var fields struct {
		RetentionDays int `json:"retentionDays"`
	}
	if err := json.Unmarshal(extra, &fields); err != nil {
		return AIRetentionDaysDefault
	}
	if fields.RetentionDays < AIRetentionDaysMin || fields.RetentionDays > AIRetentionDaysMax {
		return AIRetentionDaysDefault
	}
	return fields.RetentionDays
}

// AIModelCallQuery 是统计查询条件；时间范围为半开区间 [Start, End)。
// 半开区间让"按天连续求和"不会把边界那一毫秒重复计入——时间序列统计的通行约定。
type AIModelCallQuery struct {
	Start        time.Time
	End          time.Time
	GroupBy      []string
	IncludeLocal bool
	TenantID     string
}

// HasGroup 判断某个分组维度是否被请求。
func (q AIModelCallQuery) HasGroup(dimension string) bool {
	return slices.Contains(q.GroupBy, dimension)
}

// NormalizeAIModelCallQuery 补默认区间并校验各维度。
// End 缺省取当前整点，Start 缺省取 End - 7 天；显式给出时必须 Start < End。
func NormalizeAIModelCallQuery(q AIModelCallQuery, now time.Time) (AIModelCallQuery, error) {
	if q.End.IsZero() {
		q.End = now.Truncate(time.Hour)
	}
	if q.Start.IsZero() {
		q.Start = q.End.AddDate(0, 0, -AIUsageDefaultRangeDays)
	}
	if !q.Start.Before(q.End) {
		return AIModelCallQuery{}, &ValidationError{Message: "开始时间必须早于结束时间"}
	}
	for _, dimension := range q.GroupBy {
		if !slices.Contains(AIUsageGroupBy, dimension) {
			return AIModelCallQuery{}, &ValidationError{
				Message: fmt.Sprintf("分组维度只能是 model / protocol / scene / day，不支持 %s", dimension),
			}
		}
	}
	return q, nil
}

// AICallGroup 是一条分组明细。未被请求的维度留空串（不填占位值，避免前端误当真实数据）。
type AICallGroup struct {
	ModelID             string `json:"modelId"`
	ModelName           string `json:"modelName"`
	Protocol            string `json:"protocol"`
	Scene               string `json:"scene"`
	Day                 string `json:"day"`
	TotalCalls          int    `json:"totalCalls"`
	SuccessCalls        int    `json:"successCalls"`
	FailedCalls         int    `json:"failedCalls"`
	PromptTokens        *int64 `json:"promptTokens"`
	CompletionTokens    *int64 `json:"completionTokens"`
	AvgSuccessLatencyMS *int64 `json:"avgSuccessLatencyMs"`
}

// AICallCategoryCount 是失败类别的计数项；只统计 success=false 的记录。
type AICallCategoryCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// AIModelCallStats 是统计响应：总量 + 失败分布 + 分组明细 + 阈值快照。
// 口径见设计 §4.4：平均耗时只算「成功且已出站」，token 只对上报了 usage 的记录求和。
// token 与耗时用指针：nil 表示"没有可用数据"，与 0 严格区分。
type AIModelCallStats struct {
	TotalCalls          int                   `json:"totalCalls"`
	SuccessCalls        int                   `json:"successCalls"`
	FailedCalls         int                   `json:"failedCalls"`
	LocalRejected       int                   `json:"localRejected"`
	PromptTokens        *int64                `json:"promptTokens"`
	CompletionTokens    *int64                `json:"completionTokens"`
	TokensUnreported    int                   `json:"tokensUnreported"`
	AvgSuccessLatencyMS *int64                `json:"avgSuccessLatencyMs"`
	FailureCategories   []AICallCategoryCount `json:"failureCategories"`
	Groups              []AICallGroup         `json:"groups"`
	Quota               AIQuotaConfig         `json:"quota"`
	QuotaState          string                `json:"quotaState"`
}

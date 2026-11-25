// ai_model.go · 研发中心「AI模型」契约镜像。
// 职责：集中定义 AI 模型模块的枚举、DTO、extra_config 白名单校验/规范化、请求参数映射与 scnet 模板。
// 输入：api/openapi.yaml 的 x-rdc-ai-* 扩展（由 ai_model_test.go 逐项锁死）与调用方传入的配置值。
// 输出：跨层共用的 contract 类型与纯函数；任何输出 DTO 均不含完整 Key，只有 apiKeyMask。
// 边界：不含业务逻辑编排、不访问 DB / HTTP / 环境变量；Key 的加解密只在 internal/modelkey。
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

// 模型枚举与边界：与 openapi 的 AIProvider / AIProtocol / AIReasoningEffort / AICapability 同序。
const (
	AIProviderSCNet = "scnet"

	// 三种上游报文协议。差异只收在各 protocol_xxx.go 的 build/parse 两处，
	// 任何一处 if protocol == ... 都是返工信号。
	AIProtocolOpenAI          = "openai"
	AIProtocolAnthropic       = "anthropic"
	AIProtocolOpenAIResponses = "openai_responses"

	AIReasoningNone = "none"
	AIReasoningLow  = "low"
	AIReasoningHigh = "high"
	AIReasoningMax  = "max"

	AIDefaultReasoning     = AIReasoningHigh
	AIMinContextTokens     = 1024
	AIMaxContextTokens     = 393216
	AIDefaultContextTokens = 8192

	AIDefaultTemperature    = 1.0
	AIDefaultTopP           = 1.0
	AIExtraTimeoutMinMS     = 1000
	AIExtraTimeoutMaxMS     = 120000
	AIExtraTimeoutDefaultMS = 30000

	// AIMaxTemperatureDefault 是非 anthropic 协议的 temperature 上限；
	// anthropic 只接 0–1，见 AIModelMaxTemperature。
	AIMaxTemperatureDefault   = 2.0
	AIMaxTemperatureAnthropic = 1.0

	// ModelCallHardTimeout 是 http.Client.Timeout 的硬上限，per-call 超时不得越过它。
	ModelCallHardTimeout = 120 * time.Second

	MaxAINameLen      = 100
	MaxAIModelIDLen   = 100
	MaxAIBaseURLLen   = 500
	MaxAICapabilities = 16
)

var AIProviders = []string{AIProviderSCNet}
var AIProtocols = []string{AIProtocolOpenAI, AIProtocolAnthropic, AIProtocolOpenAIResponses}
var AIReasoningEfforts = []string{AIReasoningNone, AIReasoningLow, AIReasoningHigh, AIReasoningMax}
var AICapabilities = []string{"文本生成", "深度思考", "视觉理解"}
var AIExtraConfigKeys = []string{"temperature", "topP", "reasoningEffort", "timeoutMs"}

// AIChatCategory 是统一调用接口的七类错误分类，与 x-rdc-ai-test-categories 同序。
type AIChatCategory string

const (
	AIChatConfigMissing AIChatCategory = "config_missing"
	AIChatAuthFailed    AIChatCategory = "auth_failed"
	AIChatBadParameter  AIChatCategory = "parameter_error"
	AIChatRateLimited   AIChatCategory = "rate_limited"
	AIChatNetwork       AIChatCategory = "network_error"
	AIChatBadResponse   AIChatCategory = "response_error"
	// AIChatCapabilityMismatch：有启用模型但无一条满足声明的能力/上下文/思考档位。
	// 与 config_missing 的区别：一个是"根本没配"，一个是"配了但不够用"，处置完全不同。
	AIChatCapabilityMismatch AIChatCategory = "capability_mismatch"
)

var AIChatCategories = []string{
	string(AIChatConfigMissing), string(AIChatAuthFailed), string(AIChatBadParameter),
	string(AIChatRateLimited), string(AIChatNetwork), string(AIChatBadResponse),
	string(AIChatCapabilityMismatch),
}

// ErrAITenantRequired 表示超管未指定目标公司：列表显示空态，写操作由 handler 翻译成 400。
var ErrAITenantRequired = errors.New("请先选择目标公司")

// AIModel 是模型配置的对外投影。**没有完整 Key 字段**，唯一可见的 Key 相关信息是掩码。
type AIModel struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Provider        string          `json:"provider"`
	Protocol        string          `json:"protocol"`
	BaseURL         string          `json:"baseUrl"`
	ModelID         string          `json:"modelId"`
	APIKeyMask      string          `json:"apiKeyMask"`
	IsDefault       bool            `json:"isDefault"`
	ThinkingEnabled bool            `json:"thinkingEnabled"`
	ContextTokens   int             `json:"contextTokens"`
	ExtraConfig     json.RawMessage `json:"extraConfig"`
	Capabilities    []string        `json:"capabilities"`
	Enabled         bool            `json:"enabled"`
	CreatedAt       string          `json:"createdAt"`
	UpdatedAt       string          `json:"updatedAt"`
}

// AIModelRecord 是仓储与 service 之间的内部行投影。
// Ciphertext / Nonce 用 json:"-" 彻底挡在响应之外，明文 Key 从不进入本结构。
type AIModelRecord struct {
	AIModel
	Ciphertext string `json:"-"`
	Nonce      string `json:"-"`
}

// AIModelList 是分页响应体，items 与分页元信息平铺。
type AIModelList struct {
	Items []AIModel `json:"items"`
	Page
}

// AIModelInput 是新建 / 编辑请求体：无 tenantId / isDefault / enabled / apiKeyMask。
// APIKey 仅入站，留空表示"不更新"（新建时留空表示不配置 Key）。
type AIModelInput struct {
	Name            string          `json:"name"`
	Provider        string          `json:"provider"`
	Protocol        string          `json:"protocol"`
	BaseURL         string          `json:"baseUrl"`
	ModelID         string          `json:"modelId"`
	APIKey          string          `json:"apiKey,omitempty"`
	ThinkingEnabled bool            `json:"thinkingEnabled"`
	ContextTokens   int             `json:"contextTokens"`
	ExtraConfig     json.RawMessage `json:"extraConfig"`
	Capabilities    []string        `json:"capabilities"`
}

// AIModelStatusInput 只用于启用 / 停用，不接受其它字段。
type AIModelStatusInput struct {
	Enabled bool `json:"enabled"`
}

// AIModelQuery 是列表筛选条件；Enabled 为 nil 表示不筛，Protocol 为空表示不筛，
// TenantID 仅超管查询参数。
type AIModelQuery struct {
	Page     PageQuery
	Enabled  *bool
	Protocol string
	TenantID string
}

// AIModelTestResult 是实测结果：摘要均经脱敏，无 Key、无密文、无上游原始错误体。
type AIModelTestResult struct {
	OK               bool           `json:"ok"`
	Category         AIChatCategory `json:"category"`
	Message          string         `json:"message"`
	StatusCode       int            `json:"statusCode"`
	LatencyMS        int64          `json:"latencyMs"`
	ModelID          string         `json:"modelId"`
	UsedFallback     bool           `json:"usedFallback"`
	RequestSummary   string         `json:"requestSummary"`
	ResponsePreview  string         `json:"responsePreview"`
	PromptTokens     int            `json:"promptTokens"`
	CompletionTokens int            `json:"completionTokens"`
}

// AIModelCallRecord 是一次调用的事实行，仓储层唯一的写入形状。
// ModelID 允许为空：本地拒绝（限流 / 无匹配）时没有目标模型，硬塞 UUID 会造假。
// PromptTokens / CompletionTokens 用指针：上游未上报 usage 时为 nil（写库落 NULL），
// 与"真的用了 0 token"严格区分——把不知道当零会让容量规划系统性偏低。
// 结构体刻意不含任何 Key 字段：调用记录是长期保留的统计素材，不给明文留位置。
type AIModelCallRecord struct {
	ID                    string   `json:"id,omitempty"`
	TenantID              string   `json:"tenantId"`
	ModelID               string   `json:"modelId,omitempty"`
	ModelName             string   `json:"modelName"`
	ModelIdentifier       string   `json:"modelIdentifier"`
	Protocol              string   `json:"protocol"`
	Scene                 string   `json:"scene"`
	Success               bool     `json:"success"`
	ErrorCategory         string   `json:"errorCategory,omitempty"`
	StatusCode            int      `json:"statusCode"`
	PromptTokens          *int64   `json:"promptTokens"`
	CompletionTokens      *int64   `json:"completionTokens"`
	TokensReported        bool     `json:"tokensReported"`
	LatencyMS             int64    `json:"latencyMs"`
	UsedUpstream          bool     `json:"usedUpstream"`
	UsedDefault           bool     `json:"usedDefault"`
	UsedFallback          bool     `json:"usedFallback"`
	RequestedCapabilities []string `json:"requestedCapabilities"`
	RequestedMinContext   int      `json:"requestedMinContext"`
	RequestedEffort       string   `json:"requestedEffort"`
	CreatedAt             string   `json:"createdAt"`
}

// AIExtraConfig 是扩展配置的规范化结果，字段与 openapi 的 x-rdc-ai-extra-config 一一对应。
type AIExtraConfig struct {
	Temperature     float64 `json:"temperature"`
	TopP            float64 `json:"topP"`
	ReasoningEffort string  `json:"reasoningEffort"`
	TimeoutMS       int     `json:"timeoutMs"`
}

// AIRequestParams 是调用 scnet 时唯一的参数注入来源。
type AIRequestParams struct {
	ReasoningEffort string
	MaxTokens       int
	Temperature     float64
	TopP            float64
	TimeoutMS       int
}

// SCTemplate 是"可选模板"的契约镜像：用户点按钮填入，永不自动插库、不伪造数据。
const (
	SCTemplateName     = "DeepSeek-V4.1-Flash-1017"
	SCTemplateProvider = AIProviderSCNet
	SCTemplateProtocol = AIProtocolOpenAI
	SCTemplateBaseURL  = "https://api.scnet.cn/api/llm/v1"
	SCTemplateModelID  = "DeepSeek-V4.1-Flash-1017"
	SCTemplateContext  = AIDefaultContextTokens
)

var SCTemplateCapabilities = []string{"文本生成", "深度思考", "视觉理解"}

// SCTemplate 返回一份可直接填入表单的模板；调用方仍需用户自行填写 Key 并确认保存。
func SCTemplate() AIModelInput {
	return AIModelInput{
		Name:            SCTemplateName,
		Provider:        SCTemplateProvider,
		Protocol:        SCTemplateProtocol,
		BaseURL:         SCTemplateBaseURL,
		ModelID:         SCTemplateModelID,
		ThinkingEnabled: true,
		ContextTokens:   SCTemplateContext,
		ExtraConfig:     NormalizeAIExtraConfig(nil),
		Capabilities:    append([]string(nil), SCTemplateCapabilities...),
	}
}

func defaultAIExtraConfig() AIExtraConfig {
	return AIExtraConfig{
		Temperature:     AIDefaultTemperature,
		TopP:            AIDefaultTopP,
		ReasoningEffort: AIDefaultReasoning,
		TimeoutMS:       AIExtraTimeoutDefaultMS,
	}
}

// invalidExtra 构造可上屏的扩展配置错误；不回显原始值，避免把敏感内容带进响应。
func invalidExtra(key string) error {
	return &ValidationError{Message: fmt.Sprintf("扩展配置 %s 取值不合法", key)}
}

// parseAIExtraConfig 解析扩展配置。strict=true 写入时拒绝未知键与越界值；
// strict=false 读取时忽略未知键并把越界值补为默认，保证历史数据向后兼容。
func parseAIExtraConfig(raw json.RawMessage, strict bool) (AIExtraConfig, error) {
	cfg := defaultAIExtraConfig()
	if len(raw) == 0 || string(raw) == "null" {
		return cfg, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		if strict {
			return AIExtraConfig{}, invalidExtra("extraConfig")
		}
		return cfg, nil
	}
	for key, value := range fields {
		switch key {
		case "temperature":
			var number float64
			if err := json.Unmarshal(value, &number); err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 2 {
				if strict {
					return AIExtraConfig{}, invalidExtra(key)
				}
				continue
			}
			cfg.Temperature = number
		case "topP":
			var number float64
			if err := json.Unmarshal(value, &number); err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 || number > 1 {
				if strict {
					return AIExtraConfig{}, invalidExtra(key)
				}
				continue
			}
			cfg.TopP = number
		case "reasoningEffort":
			var effort string
			if err := json.Unmarshal(value, &effort); err != nil {
				if strict {
					return AIExtraConfig{}, invalidExtra(key)
				}
				continue
			}
			valid := false
			for _, item := range AIReasoningEfforts {
				if effort == item {
					valid = true
					break
				}
			}
			if !valid {
				if strict {
					return AIExtraConfig{}, invalidExtra(key)
				}
				continue
			}
			cfg.ReasoningEffort = effort
		case "timeoutMs":
			var timeout int
			if err := json.Unmarshal(value, &timeout); err != nil || timeout < AIExtraTimeoutMinMS || timeout > AIExtraTimeoutMaxMS {
				if strict {
					return AIExtraConfig{}, invalidExtra(key)
				}
				continue
			}
			cfg.TimeoutMS = timeout
		default:
			if strict {
				return AIExtraConfig{}, invalidExtra(key)
			}
		}
	}
	return cfg, nil
}

// ValidateAIExtraConfig 用于写入：未知键与越界值一律 400，缺省键补默认值后规范化回写。
// protocol 参与校验只为一件事：温度上限按协议收窄（anthropic 只接 0–1）。
// 为什么在这里卡而不是等上游拒绝：配置期 400 用户当场就知道填错了，调用期失败是生产事故。
func ValidateAIExtraConfig(raw json.RawMessage, protocol string) (json.RawMessage, error) {
	cfg, err := parseAIExtraConfig(raw, true)
	if err != nil {
		return nil, err
	}
	if limit := AIModelMaxTemperature(protocol); cfg.Temperature > limit {
		return nil, &ValidationError{Message: fmt.Sprintf("%s 协议的温度取值范围为 0–%g", protocol, limit)}
	}
	return json.Marshal(cfg)
}

// NormalizeAIExtraConfig 用于读取：忽略未知键、补默认值，不因历史数据报错。
func NormalizeAIExtraConfig(raw json.RawMessage) json.RawMessage {
	cfg, _ := parseAIExtraConfig(raw, false)
	normalized, _ := json.Marshal(cfg)
	return normalized
}

// AIModelRequestParams 把模型配置折算成唯一的调用参数注入口径。
// 关思考 → reasoning_effort=none；开思考 → 取 extra_config.reasoningEffort，默认 high。
func AIModelRequestParams(model AIModel) AIRequestParams {
	cfg, _ := parseAIExtraConfig(model.ExtraConfig, false)
	effort := AIReasoningNone
	if model.ThinkingEnabled {
		effort = cfg.ReasoningEffort
	}
	tokens := model.ContextTokens
	if tokens < AIMinContextTokens || tokens > AIMaxContextTokens {
		tokens = AIDefaultContextTokens
	}
	return AIRequestParams{
		ReasoningEffort: effort,
		MaxTokens:       tokens,
		Temperature:     cfg.Temperature,
		TopP:            cfg.TopP,
		TimeoutMS:       cfg.TimeoutMS,
	}
}

// NormalizeAIBaseURL 校验 API 基址：http(s) 绝对地址、无 userinfo / query / fragment，去掉结尾斜杠。
func NormalizeAIBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > MaxAIBaseURLLen {
		return "", &ValidationError{Message: "API 地址不能为空且不得超过 500 字"}
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", &ValidationError{Message: "API 地址必须是无 query 的 http(s) 绝对地址"}
	}
	return strings.TrimRight(trimmed, "/"), nil
}

// AIChatMessage 返回七类错误的统一中文提示，不含上游原文、Key 或密文。
func AIChatMessage(category AIChatCategory) string {
	switch category {
	case AIChatConfigMissing:
		return "该公司尚未配置可用模型，请前往 AI 模型页配置"
	case AIChatAuthFailed:
		return "密钥无效或已失效，请更新 Key"
	case AIChatBadParameter:
		return "模型或参数不被支持，请检查模型 ID 与思考 / 上下文配置"
	case AIChatRateLimited:
		return "供应商繁忙或限流，请稍后重试"
	case AIChatNetwork:
		return "网络不可达或调用超时，请稍后重试"
	case AIChatBadResponse:
		return "供应商响应异常"
	case AIChatCapabilityMismatch:
		return "没有满足条件的模型，请调整能力 / 上下文 / 思考强度要求，或补充启用模型"
	default:
		return "模型调用失败，请稍后重试"
	}
}

// IsValidAIChatCategory 判断错误分类是否在七类目录里。
func IsValidAIChatCategory(category string) bool {
	for _, item := range AIChatCategories {
		if item == category {
			return true
		}
	}
	return false
}

// AIReasoningRank 是思考档位的唯一序函数（none<low<high<max）；未知值返回 -1。
// 为什么集中一处：调度匹配、表单提示、协议折算三处都依赖这个序，散落必然失配。
func AIReasoningRank(effort string) int {
	switch effort {
	case AIReasoningNone:
		return 0
	case AIReasoningLow:
		return 1
	case AIReasoningHigh:
		return 2
	case AIReasoningMax:
		return 3
	default:
		return -1
	}
}

// AIModelOfferedEffort 返回模型"可提供"的思考档位上限：
// thinking_enabled=false → none（无论 extra_config 写了什么，开关关掉就是没有思考能力）；
// true 则取 extra_config.reasoningEffort（配置与开关矛盾时以 extra_config 为准，沿用 P0 口径）。
func AIModelOfferedEffort(model AIModel) string {
	return AIModelRequestParams(model).ReasoningEffort
}

// AIModelMaxTemperature 按协议返回 temperature 上限：anthropic 为 1，其余 2。
// 为什么"校验按协议走"：配置期 400 远优于调用期被上游拒（用户能立刻看到哪一项填错）。
func AIModelMaxTemperature(protocol string) float64 {
	if protocol == AIProtocolAnthropic {
		return AIMaxTemperatureAnthropic
	}
	return AIMaxTemperatureDefault
}

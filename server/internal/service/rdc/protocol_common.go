// protocol_common.go · 三协议共用的报文层能力：消息归一、usage 归一、结果装配、思考档位折算。
// 职责：把「协议无关」的公共动作收敛成一份实现，供 protocol_openai / _anthropic / _responses 复用。
// 输入：ChatMessage 列表、各协议已解析出的 content / reasoning / usage 原始值、出站事实 upstreamCall。
// 输出：统一的 ChatResult 与 thinkingPlan；所有对外文本经 modelkey.Redact。
// 边界：本文件**不允许**出现任何单一协议特有的字段名或请求头字面量（例如该协议的密钥头、
// 版本头、输出上限字段名、思考强度字段名）；它们只能写在对应 protocol_*.go 里。
// 新增协议时应"只加一个文件"，若必须改本文件，说明抽象漏了 —— 先补函数再改，不要复制粘贴。
package rdc

import (
	"net/http"
	"strings"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/modelkey"
)

// thinkingPlan 把平台四档统一折算成各协议参数；返回的是"待写入请求体的字段值"，
// 数值口径与 PRD §5.2 折算表一致，且三协议共享同一张表 —— 改档位只改这里。
//
// 为什么必须是一张表而不是三处 switch：三协议对"思考强度"不是一个维度
// （档位串 / token 预算 / 三档枚举），平台四档才是统一词汇。翻译一旦散落，
// 调整某一档就要改三处，且极易漏改其中一处造成静默不一致。
type thinkingPlan struct {
	OpenAIEffort    string // 四档原值透传；none 表示关闭思考
	AnthropicBudget int    // token 预算；0 = 请求体里不传该字段（该协议用"不传"表达关闭）
	ResponsesEffort string // 三档枚举；空 = 请求体里不传该字段
}

// thinkingPlans 是档位折算的唯一来源；键取自 contract 常量，不写第二套档位字面量。
var thinkingPlans = map[string]thinkingPlan{
	contract.AIReasoningNone: {OpenAIEffort: contract.AIReasoningNone, AnthropicBudget: 0, ResponsesEffort: ""},
	contract.AIReasoningLow:  {OpenAIEffort: contract.AIReasoningLow, AnthropicBudget: 1024, ResponsesEffort: "low"},
	contract.AIReasoningHigh: {OpenAIEffort: contract.AIReasoningHigh, AnthropicBudget: 4096, ResponsesEffort: "medium"},
	contract.AIReasoningMax:  {OpenAIEffort: contract.AIReasoningMax, AnthropicBudget: 8192, ResponsesEffort: "high"},
}

// planThinking 查表；未知档位按 none 处理，不回显原值、不 panic。
// 配置期已被 contract.ValidateAIExtraConfig 拦过，这里只是兜底：历史脏数据不该让调用 500。
func planThinking(effort string) thinkingPlan {
	if plan, ok := thinkingPlans[effort]; ok {
		return plan
	}
	return thinkingPlans[contract.AIReasoningNone]
}

// inputOutputUsage 是两个以 input/output 命名 usage 的协议共用的形状。
// 放在共用层是因为"字段名完全相同"，而不是把两种报文结构强行统一 ——
// 各协议的顶层响应结构仍各自持有，见 protocol_anthropic.go / protocol_responses.go。
//
// 字段用 *int 而不是 int：nil 表示**上游没给这个字段**，非 nil 且值为 0 表示
// **上游确实报了 0**（如缓存命中）。这两件事在统计口径上完全不同 ——
// 前者落 NULL（不知道），后者落 0（真实用量）；用 int 会把两者压成同一个 0。
type inputOutputUsage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

// tokens 返回可空的两个 token 数；usage 整体缺失时调用方持有的是 nil 指针，
// 这里对 nil 接收者返回双 nil —— 不 panic、也不伪造一个全零结构（伪造即等于"上报了 0"）。
func (u *inputOutputUsage) tokens() (*int, *int) {
	if u == nil {
		return nil, nil
	}
	return u.InputTokens, u.OutputTokens
}

// tokensFrom 把两个"可空 token 数"折成 usageFrom 的三参形式，是三协议唯一的折算点。
//
// 为什么必须用指针判断而不是"值是否为 0"：指针 nil 精确表达"字段没出现"，
// 非 nil 的 0 表达"字段出现了且值是 0"。若只用值判断，上游真报 0 会被误当未上报，
// 反向漏报同样会把真实用量丢掉。
//
// 口径选择：**任一字段缺失即视为未上报**。只报一半（如只有 prompt 没有 completion）时，
// 另一半无从推断，把未知当 0 会让容量规划系统性偏低，因此保守地整体按"未上报"处理，
// 落 NULL 而不是落一个半真半假的数。
func tokensFrom(promptTokens, completionTokens *int) (int, int, bool) {
	if promptTokens == nil || completionTokens == nil {
		return usageFrom(0, 0, false)
	}
	return usageFrom(*promptTokens, *completionTokens, true)
}

// chatReply 是协议解析器从上游响应里提取出的全部业务内容。
// 提取（差异）与装配（共性）靠这个结构体分界：解析器只填它，ChatResult 一律由
// buildChatResult 生成，禁止解析器自己拼 ChatResult（否则 Redact 会有三份）。
type chatReply struct {
	Content          string
	Reasoning        string
	PromptTokens     int
	CompletionTokens int
	TokensReported   bool
}

// joinText 把多段文本用换行拼成一段；空切片返回空串（不是 "\n"）。
// 协议把多段内容合成一个字符串时统一走这里，避免各处 strings.Join 的细节差异。
func joinText(parts []string) string {
	return strings.Join(parts, "\n")
}

// splitSystem 把 system 消息合并成一段文本并从消息列表里摘掉，供"顶层 system 字段"的协议使用。
// 多条 system 用换行拼接：协议只接受一个 system 字符串，直接丢弃会静默改变提示词语义。
func splitSystem(messages []ChatMessage) (string, []ChatMessage) {
	systemParts := make([]string, 0, 1)
	rest := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" {
			systemParts = append(systemParts, message.Content)
			continue
		}
		rest = append(rest, message)
	}
	return joinText(systemParts), rest
}

// withDefaultProbe 在消息为空时补一句最短探针，供实测（Test）与显式传空消息的调用方使用。
// 空 messages 上游必然 400，补探针既能让"测试连接"验证真实链路，也不会伪造业务内容。
func withDefaultProbe(messages []ChatMessage) []ChatMessage {
	if len(messages) > 0 {
		return messages
	}
	return []ChatMessage{{Role: "user", Content: "请回复简短的 OK。"}}
}

// usageFrom 是「缺数据不当零」的唯一落点：reported=false（上游没返回 usage）时
// 把两个数抹成 0 并保持 false，由 chat_record.go 据此把 token 列写 NULL。
// 为什么不靠"两个数都是 0"来判断没上报：上游真会返回 0 token（缓存命中），
// 那种情况下 0 是真实数据；只有解析处知道 usage 字段到底出现过没有。
func usageFrom(promptTokens, completionTokens int, reported bool) (int, int, bool) {
	if !reported {
		return 0, 0, false
	}
	return promptTokens, completionTokens, true
}

// protocolSpec 是"某协议的报文形态"的唯一描述：只有两个纯函数，
// 不含超时、不含 HTTP 出站、不含重试、不含限流与统计。
// 新增协议 = 新增一个 protocol_xxx.go 实现本接口 + 下面 switch 加一个 case，别处一律不动。
type protocolSpec interface {
	build(record contract.AIModelRecord, params contract.AIRequestParams, messages []ChatMessage, plainKey string) (
		endpoint string, header http.Header, body []byte, err error)
	parse(raw []byte, call upstreamCall, record contract.AIModelRecord, plainKey string) (ChatResult, error)
}

// protocolSpecFor 按协议值返回报文描述；未知值返回 nil，由 chatOnce 翻成 parameter_error。
// 为什么用 switch 而不用注册表 + 反射：三个分支是编译期可知的常量，
// 注册表只增加间接层与初始化顺序风险，换不来可扩展性（新增协议本来就要改代码）。
func protocolSpecFor(protocol string) protocolSpec {
	switch protocol {
	case contract.AIProtocolOpenAI:
		return openaiSpec{}
	case contract.AIProtocolAnthropic:
		return anthropicSpec{}
	case contract.AIProtocolOpenAIResponses:
		return responsesSpec{}
	default:
		return nil
	}
}

// chatEndpoint 由 base_url 拼出协议端点：去掉用户可能多写的结尾斜杠再接后缀。
// 放在共用层是因为"拼 URL"无协议语义，后缀字面量由各协议自己给。
func chatEndpoint(baseURL, suffix string) string {
	return strings.TrimRight(baseURL, "/") + suffix
}

// buildChatResult 是三协议唯一的 ChatResult 构造点：统一脱敏、统一耗时与标记口径。
// 把 modelkey.Redact 收在这里，"任何对外文本都必须脱敏"成为结构性事实，
// 而不是靠三个协议各自记得调用。
func buildChatResult(
	record contract.AIModelRecord, call upstreamCall, plainKey string, reply chatReply,
) ChatResult {
	return ChatResult{
		Content:          modelkey.Redact(plainKey, reply.Content),
		ReasoningContent: modelkey.Redact(plainKey, reply.Reasoning),
		ModelID:          record.ModelID,
		LatencyMS:        call.latencyMS,
		PromptTokens:     reply.PromptTokens,
		CompletionTokens: reply.CompletionTokens,
		TokensReported:   reply.TokensReported,
	}
}

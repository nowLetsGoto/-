// protocol_openai.go · 仅 openai 协议的报文差异：端点后缀、Bearer 认证、messages[]、思考强度字段。
// 职责：实现 protocolSpec 的 build / parse 两个纯函数（本协议的差异全部在此）。
// 输入：模型记录、已折算的请求参数、归一后的消息、明文 Key（仅用于拼认证头）。
// 输出：endpoint + header + body；chatReply（content / reasoning / usage）。
// 边界：不发 HTTP 请求、不加超时、不做重试、不判断限流与统计、不写日志、不落库。
package rdc

import (
	"encoding/json"
	"net/http"

	"dandelion/server/internal/contract"
)

// openaiSpec 是 openai（`{base_url}/chat/completions`）的协议描述值。
// 之所以是"值"而不是"类型 + 方法"：三协议各只有两个纯函数，加一层无行为包装就是冗余。
type openaiSpec struct{}

// openaiRequest 是 OpenAI 兼容的最小请求体；参数由 contract.AIModelRequestParams 唯一决定。
// system 消息原样留在 messages 里 —— 该协议用 role=system 表达系统提示，无需拆成顶层字段。
type openaiRequest struct {
	Model           string        `json:"model"`
	Messages        []ChatMessage `json:"messages"`
	Temperature     float64       `json:"temperature"`
	TopP            float64       `json:"top_p"`
	MaxTokens       int           `json:"max_tokens"`
	ReasoningEffort string        `json:"reasoning_effort"`
}

// openaiResponse 只声明本协议用到的字段（choices[0].message 与 usage）。
// usage 用指针：nil 表示上游压根没返回 usage（不少兼容实现如此），
// 与"返回了 usage 但值是 0"必须区分，否则 0 会被当作真实用量写进统计。
type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     *int `json:"prompt_tokens"`
		CompletionTokens *int `json:"completion_tokens"`
	} `json:"usage"`
}

// build 组装 openai 请求：端点 = base_url + /chat/completions，认证 = Authorization: Bearer。
func (openaiSpec) build(
	record contract.AIModelRecord, params contract.AIRequestParams, messages []ChatMessage, plainKey string,
) (string, http.Header, []byte, error) {
	payload, err := json.Marshal(openaiRequest{
		Model: record.ModelID, Messages: messages, Temperature: params.Temperature,
		TopP: params.TopP, MaxTokens: params.MaxTokens,
		ReasoningEffort: planThinking(params.ReasoningEffort).OpenAIEffort,
	})
	if err != nil {
		// 入参全是已校验过的标量与字符串，序列化失败只可能是编程错误。
		return "", nil, nil, newChatError(contract.AIChatBadParameter, 0)
	}
	return chatEndpoint(record.BaseURL, "/chat/completions"), bearerHeader(plainKey), payload, nil
}

// parse 提取 choices[0].message 的正文与思考内容；两者皆空按 response_error。
func (openaiSpec) parse(raw []byte, call upstreamCall, record contract.AIModelRecord, plainKey string) (ChatResult, error) {
	var decoded openaiResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ChatResult{}, newChatError(contract.AIChatBadResponse, call.statusCode)
	}
	if len(decoded.Choices) == 0 {
		return ChatResult{}, newChatError(contract.AIChatBadResponse, call.statusCode)
	}
	message := decoded.Choices[0].Message
	if message.Content == "" && message.ReasoningContent == "" {
		return ChatResult{}, newChatError(contract.AIChatBadResponse, call.statusCode)
	}
	// usage 缺失与「usage 真为 0」用指针可区分，故必须按字段是否存在判断，
	// 不能拿 choices 的存在性代替 —— 上游给了正文但没给 usage 是常见的兼容实现行为。
	promptTokens, completionTokens, reported := tokensFrom(usagePointers(decoded.Usage))
	return buildChatResult(record, call, plainKey, chatReply{
		Content: message.Content, Reasoning: message.ReasoningContent,
		PromptTokens: promptTokens, CompletionTokens: completionTokens, TokensReported: reported,
	}), nil
}

// usagePointers 把 openai 的匿名 usage 结构摊成两个可空 token 数；结构整体缺失时双 nil。
// 与 anthropic / responses 的 (*inputOutputUsage).tokens() 是各自协议的取值适配，
// 差异只在"字段名"，折算本身仍收敛在 tokensFrom 一处。
func usagePointers(usage *struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
}) (*int, *int) {
	if usage == nil {
		return nil, nil
	}
	return usage.PromptTokens, usage.CompletionTokens
}

// bearerHeader 返回 `Authorization: Bearer <key>`；openai 与 openai_responses 共用同一形态。
// 它是"协议认证形态"而非"通用工具"，所以放在协议层而不是 protocol_common.go。
func bearerHeader(plainKey string) http.Header {
	header := make(http.Header, 1)
	header.Set("Authorization", "Bearer "+plainKey)
	return header
}

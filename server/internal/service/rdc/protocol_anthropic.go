// protocol_anthropic.go · 仅 anthropic 协议的报文差异：/messages、x-api-key、顶层 system、content[]。
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

// anthropicVersion 是该协议必需的版本头；后端写死，不暴露给用户填。
// 上游按版本号切换报文语义，交给用户填只会让"配置错误"变成"调用期 400"。
const anthropicVersion = "2023-06-01"

// anthropicSpec 是 anthropic（`{base_url}/messages`）的协议描述值。
type anthropicSpec struct{}

// anthropicRequest 的差异集中在三处：system 是顶层字符串（不在 messages 里）、
// max_tokens 必填、思考强度用 thinking.budget_tokens 表达（关闭时不传该字段）。
type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []ChatMessage      `json:"messages"`
	Temperature float64            `json:"temperature"`
	TopP        float64            `json:"top_p"`
	MaxTokens   int                `json:"max_tokens"`
	Thinking    *anthropicThinking `json:"thinking,omitempty"`
}

// anthropicThinking 用 token 预算表达思考强度；budget=0 表示关闭，此时整个字段不出现。
type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// anthropicResponse 的 content 是"内容块数组"，正文与思考靠 type 区分而不是靠字段名。
type anthropicResponse struct {
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"content"`
	Usage inputOutputUsage `json:"usage"`
}

// build 组装 anthropic 请求：system 提到顶层、messages 只剩 user/assistant。
func (anthropicSpec) build(
	record contract.AIModelRecord, params contract.AIRequestParams, messages []ChatMessage, plainKey string,
) (string, http.Header, []byte, error) {
	system, conversation := splitSystem(messages)
	request := anthropicRequest{
		Model: record.ModelID, System: system, Messages: conversation,
		Temperature: params.Temperature, TopP: params.TopP, MaxTokens: params.MaxTokens,
	}
	// budget=0 时保持 nil（omitempty 让字段消失）：该协议不接受 budget_tokens=0，
	// 传 0 会被判参数错误，而"关思考"的正确表达方式就是不发这个字段。
	if plan := planThinking(params.ReasoningEffort); plan.AnthropicBudget > 0 {
		request.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: plan.AnthropicBudget}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", nil, nil, newChatError(contract.AIChatBadParameter, 0)
	}
	return chatEndpoint(record.BaseURL, "/messages"), anthropicHeader(plainKey), payload, nil
}

// parse 按 type 分流内容块：text 拼正文、thinking 拼思考；两类都为空按 response_error。
// 未知 type 不报错而是忽略 —— 上游会加新块类型（如 tool_use），忽略比整单失败更安全。
func (anthropicSpec) parse(raw []byte, call upstreamCall, record contract.AIModelRecord, plainKey string) (ChatResult, error) {
	var decoded anthropicResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ChatResult{}, newChatError(contract.AIChatBadResponse, call.statusCode)
	}
	texts := make([]string, 0, len(decoded.Content))
	thinkings := make([]string, 0, len(decoded.Content))
	for _, block := range decoded.Content {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "thinking":
			thinkings = append(thinkings, block.Thinking)
		}
	}
	content, reasoning := joinText(texts), joinText(thinkings)
	if content == "" && reasoning == "" {
		return ChatResult{}, newChatError(contract.AIChatBadResponse, call.statusCode)
	}
	promptTokens, completionTokens, reported := tokensFrom(decoded.Usage.tokens())
	return buildChatResult(record, call, plainKey, chatReply{
		Content: content, Reasoning: reasoning,
		PromptTokens: promptTokens, CompletionTokens: completionTokens, TokensReported: reported,
	}), nil
}

// anthropicHeader 是该协议专属认证形态：x-api-key + 版本头，且**不带** Authorization。
// 同时带两种认证头会被上游按"多凭据"拒绝，所以这里刻意不调用 bearerHeader。
func anthropicHeader(plainKey string) http.Header {
	header := make(http.Header, 2)
	header.Set("x-api-key", plainKey)
	header.Set("anthropic-version", anthropicVersion)
	return header
}

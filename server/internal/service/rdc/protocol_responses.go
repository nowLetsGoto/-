// protocol_responses.go · 仅 openai_responses 协议的报文差异：/responses、input[]、output[]。
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

// responsesSpec 是 openai_responses（`{base_url}/responses`）的协议描述值。
type responsesSpec struct{}

// responsesRequest 的三处差异：对话内容走 input（**锁定消息数组**，不用字符串形态）、
// 输出上限叫 max_output_tokens、思考强度走 reasoning.effort（三档枚举，空则不传）。
type responsesRequest struct {
	Model           string              `json:"model"`
	Input           []ChatMessage       `json:"input"`
	Temperature     float64             `json:"temperature"`
	TopP            float64             `json:"top_p"`
	MaxOutputTokens int                 `json:"max_output_tokens"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
}

// responsesReasoning 只有三档（low/medium/high），由 planThinking 折算，空表示关闭。
type responsesReasoning struct {
	Effort string `json:"effort"`
}

// responsesResponse 的 output 是"条目数组"：正文在 type=="message" 的 content[].text 里，
// 思考摘要在 type=="reasoning" 的 summary[].text 里；output_text 是上游给的单串便捷字段。
type responsesResponse struct {
	OutputText string `json:"output_text"`
	Output     []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Summary []struct {
			Text string `json:"text"`
		} `json:"summary"`
	} `json:"output"`
	Usage inputOutputUsage `json:"usage"`
}

// build 组装 responses 请求：input 用消息数组形态（可保多轮与角色，与 messages 一一对应）。
func (responsesSpec) build(
	record contract.AIModelRecord, params contract.AIRequestParams, messages []ChatMessage, plainKey string,
) (string, http.Header, []byte, error) {
	request := responsesRequest{
		Model: record.ModelID, Input: messages,
		Temperature: params.Temperature, TopP: params.TopP, MaxOutputTokens: params.MaxTokens,
	}
	// 空串表示该档位在本协议没有对应值（none），保持 nil 让 reasoning 字段整体消失。
	if effort := planThinking(params.ReasoningEffort).ResponsesEffort; effort != "" {
		request.Reasoning = &responsesReasoning{Effort: effort}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", nil, nil, newChatError(contract.AIChatBadParameter, 0)
	}
	return chatEndpoint(record.BaseURL, "/responses"), bearerHeader(plainKey), payload, nil
}

// parse 优先取 output_text；为空则回落到 output[] 里按 type 分流拼接。
// 两条路径都为空即 response_error（上游结构变了或响应被截断）。
func (responsesSpec) parse(raw []byte, call upstreamCall, record contract.AIModelRecord, plainKey string) (ChatResult, error) {
	var decoded responsesResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ChatResult{}, newChatError(contract.AIChatBadResponse, call.statusCode)
	}
	content, reasoning := decoded.textContent(), decoded.thinkingContent()
	if content == "" && reasoning == "" {
		return ChatResult{}, newChatError(contract.AIChatBadResponse, call.statusCode)
	}
	promptTokens, completionTokens, reported := tokensFrom(decoded.Usage.tokens())
	return buildChatResult(record, call, plainKey, chatReply{
		Content: content, Reasoning: reasoning,
		PromptTokens: promptTokens, CompletionTokens: completionTokens, TokensReported: reported,
	}), nil
}

// textContent 先取 output_text，再回落到 message 条目的 content[].text。
// 为什么保留两条路径：output_text 是便捷聚合字段，部分兼容实现不返回它，
// 而 output[] 是结构化的权威来源；两者都空才算"上游没给正文"。
func (r responsesResponse) textContent() string {
	if r.OutputText != "" {
		return r.OutputText
	}
	texts := make([]string, 0, len(r.Output))
	for _, item := range r.Output {
		if item.Type != "message" {
			continue
		}
		for _, block := range item.Content {
			texts = append(texts, block.Text)
		}
	}
	return joinText(texts)
}

// thinkingContent 只取 type=="reasoning" 条目的 summary[].text 拼接；无则空串。
func (r responsesResponse) thinkingContent() string {
	parts := make([]string, 0, len(r.Output))
	for _, item := range r.Output {
		if item.Type != "reasoning" {
			continue
		}
		for _, summary := range item.Summary {
			parts = append(parts, summary.Text)
		}
	}
	return joinText(parts)
}

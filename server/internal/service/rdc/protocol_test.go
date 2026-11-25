// protocol_test.go · 三协议报文层的契约测试（本地 mock，不发起真实上游调用）。
// 职责：钉死每种协议的端点后缀、认证头、请求体关键字段、响应提取与空响应判定，以及折算单表。
// 输入：httptest.Server 冒充上游；ChatMessage / 请求参数由测试就地构造。
// 输出：全部断言通过；断言只关心"报文形态"，不关心业务编排（那属于 model_chat_test.go）。
// 边界：不访问任何真实域名、不读环境变量、不使用真实 Key；测试 Key 是显式的假串。
package rdc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dandelion/server/internal/contract"
)

// protocolCapture 记录 mock 上游收到的一次请求，供逐字段断言。
type protocolCapture struct {
	Path          string
	Authorization string
	APIKey        string
	Version       string
	Body          map[string]any
}

// chatOnceFixture 起一个 mock 上游并按协议构造模型记录；reply 是上游的响应体。
func chatOnceFixture(t *testing.T, protocol, reply string) (contract.AIModelRecord, *protocolCapture) {
	t.Helper()
	captured := &protocolCapture{Body: map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		captured.Authorization = r.Header.Get("Authorization")
		captured.APIKey = r.Header.Get("x-api-key")
		captured.Version = r.Header.Get("anthropic-version")
		_ = json.NewDecoder(r.Body).Decode(&captured.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)

	record := contract.AIModelRecord{
		AIModel: contract.AIModel{
			ID: "id", Name: "测试模型", Provider: contract.AIProviderSCNet, Protocol: protocol,
			BaseURL: server.URL + "/", ModelID: "model-x", IsDefault: true,
			ThinkingEnabled: true, ContextTokens: 8192, Enabled: true,
			ExtraConfig: json.RawMessage(`{"reasoningEffort":"high","timeoutMs":3000}`),
		},
	}
	return record, captured
}

// runChatOnce 以测试 Key 直接调用唯一出口，跳过 Codec（Key 明文在 chatOnce 入参即已就绪）。
// 只保留一个返回值：本文件断言的是报文形态，出站账本由 chat_transport 的语义单独覆盖。
func runChatOnce(t *testing.T, record contract.AIModelRecord, messages []ChatMessage) (ChatResult, error) {
	t.Helper()
	client := &http.Client{Timeout: contract.ModelCallHardTimeout}
	result, _, err := chatOnce(context.Background(), client, record, aiTestPlainKey, messages)
	return result, err
}

// conversation 是一组带 system 的多轮消息，三协议都需据此断言 system 的不同承载方式。
func conversation() []ChatMessage {
	return []ChatMessage{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "在的"},
	}
}

func TestOpenAIProtocolBuildAndParse(t *testing.T) {
	record, captured := chatOnceFixture(t, contract.AIProtocolOpenAI,
		`{"choices":[{"message":{"content":"OK","reasoning_content":"想一想"}}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	result, err := runChatOnce(t, record, conversation())
	if err != nil {
		t.Fatalf("应调用成功: %v", err)
	}
	if captured.Path != "/chat/completions" {
		t.Fatalf("端点后缀应为 /chat/completions，实际 %s", captured.Path)
	}
	if captured.Authorization != "Bearer "+aiTestPlainKey || captured.APIKey != "" {
		t.Fatalf("认证头应为 Bearer，实际 %q / %q", captured.Authorization, captured.APIKey)
	}
	if captured.Body["max_tokens"] != float64(8192) || captured.Body["reasoning_effort"] != "high" {
		t.Fatalf("请求体参数错误: %v", captured.Body)
	}
	// system 消息留在 messages 里（该协议的表达方式），不应被提到顶层。
	messages, ok := captured.Body["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages 应保留 3 条（含 system），实际 %v", captured.Body["messages"])
	}
	if result.Content != "OK" || result.ReasoningContent != "想一想" {
		t.Fatalf("响应提取错误: %+v", result)
	}
	if !result.TokensReported || result.PromptTokens != 3 || result.CompletionTokens != 1 {
		t.Fatalf("usage 提取错误: %+v", result)
	}
}

func TestAnthropicProtocolBuildAndParse(t *testing.T) {
	record, captured := chatOnceFixture(t, contract.AIProtocolAnthropic,
		`{"content":[{"type":"thinking","thinking":"先想"},{"type":"text","text":"OK"}],`+
			`"usage":{"input_tokens":5,"output_tokens":2}}`)
	result, err := runChatOnce(t, record, conversation())
	if err != nil {
		t.Fatalf("应调用成功: %v", err)
	}
	if captured.Path != "/messages" {
		t.Fatalf("端点后缀应为 /messages，实际 %s", captured.Path)
	}
	if captured.APIKey != aiTestPlainKey || captured.Version == "" {
		t.Fatalf("认证头应为 x-api-key + 版本头，实际 %q / %q", captured.APIKey, captured.Version)
	}
	if captured.Authorization != "" {
		t.Fatalf("该协议不得同时带 Authorization，实际 %q", captured.Authorization)
	}
	if captured.Body["system"] != "你是助手" || captured.Body["max_tokens"] != float64(8192) {
		t.Fatalf("顶层 system / max_tokens 错误: %v", captured.Body)
	}
	messages, ok := captured.Body["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages 应只剩 2 条（system 已提到顶层），实际 %v", captured.Body["messages"])
	}
	thinking, ok := captured.Body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(4096) {
		t.Fatalf("high 档应折算为 4096 预算，实际 %v", captured.Body["thinking"])
	}
	if result.Content != "OK" || result.ReasoningContent != "先想" {
		t.Fatalf("内容块提取错误: %+v", result)
	}
	if !result.TokensReported || result.PromptTokens != 5 || result.CompletionTokens != 2 {
		t.Fatalf("usage 提取错误: %+v", result)
	}
}

func TestResponsesProtocolBuildAndParse(t *testing.T) {
	record, captured := chatOnceFixture(t, contract.AIProtocolOpenAIResponses,
		`{"output_text":"OK","output":[{"type":"reasoning","summary":[{"text":"先想"}]}],`+
			`"usage":{"input_tokens":4,"output_tokens":1}}`)
	result, err := runChatOnce(t, record, conversation())
	if err != nil {
		t.Fatalf("应调用成功: %v", err)
	}
	if captured.Path != "/responses" {
		t.Fatalf("端点后缀应为 /responses，实际 %s", captured.Path)
	}
	if captured.Authorization != "Bearer "+aiTestPlainKey {
		t.Fatalf("认证头应为 Bearer，实际 %q", captured.Authorization)
	}
	if captured.Body["max_output_tokens"] != float64(8192) {
		t.Fatalf("输出上限字段应为 max_output_tokens，实际 %v", captured.Body)
	}
	if _, hasMaxTokens := captured.Body["max_tokens"]; hasMaxTokens {
		t.Fatal("该协议不应出现 max_tokens 字段")
	}
	inputs, ok := captured.Body["input"].([]any)
	if !ok || len(inputs) != 3 {
		t.Fatalf("input 必须是消息数组（含 system 共 3 条），实际 %v", captured.Body["input"])
	}
	reasoning, ok := captured.Body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" {
		t.Fatalf("high 档应折算为 medium，实际 %v", captured.Body["reasoning"])
	}
	if result.Content != "OK" || result.ReasoningContent != "先想" {
		t.Fatalf("output 提取错误: %+v", result)
	}
}

func TestResponsesFallsBackToOutputArray(t *testing.T) {
	record, _ := chatOnceFixture(t, contract.AIProtocolOpenAIResponses,
		`{"output":[{"type":"message","content":[{"type":"output_text","text":"A"},{"type":"output_text","text":"B"}]}]}`)
	result, err := runChatOnce(t, record, conversation())
	if err != nil {
		t.Fatalf("应调用成功: %v", err)
	}
	if result.Content != "A\nB" {
		t.Fatalf("output_text 缺失时应回落到 output[] 拼接，实际 %q", result.Content)
	}
}

func TestThinkingNoneOmitsProtocolSpecificFields(t *testing.T) {
	// 关思考：anthropic 不传 thinking，responses 不传 reasoning；openai 仍传四档原值 none。
	for _, protocol := range contract.AIProtocols {
		t.Run(protocol, func(t *testing.T) {
			record, captured := chatOnceFixture(t, protocol,
				`{"choices":[{"message":{"content":"OK"}}],"output_text":"OK",`+
					`"content":[{"type":"text","text":"OK"}]}`)
			record.ThinkingEnabled = false
			if _, err := runChatOnce(t, record, conversation()); err != nil {
				t.Fatalf("应调用成功: %v", err)
			}
			switch protocol {
			case contract.AIProtocolAnthropic:
				if _, exists := captured.Body["thinking"]; exists {
					t.Fatalf("关思考不得传 thinking 字段: %v", captured.Body)
				}
			case contract.AIProtocolOpenAIResponses:
				if _, exists := captured.Body["reasoning"]; exists {
					t.Fatalf("关思考不得传 reasoning 字段: %v", captured.Body)
				}
			case contract.AIProtocolOpenAI:
				if captured.Body["reasoning_effort"] != contract.AIReasoningNone {
					t.Fatalf("关思考应传 none，实际 %v", captured.Body["reasoning_effort"])
				}
			}
		})
	}
}

func TestPlanThinkingIsSingleTable(t *testing.T) {
	// 四档 × 三协议的折算值必须与设计 §3.5 折算表逐格一致；这是"只改一处"的护栏。
	want := map[string]thinkingPlan{
		contract.AIReasoningNone: {OpenAIEffort: "none", AnthropicBudget: 0, ResponsesEffort: ""},
		contract.AIReasoningLow:  {OpenAIEffort: "low", AnthropicBudget: 1024, ResponsesEffort: "low"},
		contract.AIReasoningHigh: {OpenAIEffort: "high", AnthropicBudget: 4096, ResponsesEffort: "medium"},
		contract.AIReasoningMax:  {OpenAIEffort: "max", AnthropicBudget: 8192, ResponsesEffort: "high"},
	}
	if len(thinkingPlans) != len(want) {
		t.Fatalf("折算表应只有 %d 行，实际 %d 行", len(want), len(thinkingPlans))
	}
	for effort, expected := range want {
		if got := planThinking(effort); got != expected {
			t.Fatalf("%s 档折算错误：期望 %+v，实际 %+v", effort, expected, got)
		}
	}
	if got := planThinking("ultra"); got != want[contract.AIReasoningNone] {
		t.Fatalf("未知档位应按 none 兜底，实际 %+v", got)
	}
}

func TestEmptyResponseIsResponseError(t *testing.T) {
	// 三协议对"上游给了 2xx 但没有可用正文"必须统一判 response_error，不允许各自发明类别。
	cases := map[string]string{
		contract.AIProtocolOpenAI:          `{"choices":[]}`,
		contract.AIProtocolAnthropic:       `{"content":[{"type":"tool_use"}]}`,
		contract.AIProtocolOpenAIResponses: `{"output":[]}`,
	}
	for protocol, reply := range cases {
		t.Run(protocol, func(t *testing.T) {
			record, _ := chatOnceFixture(t, protocol, reply)
			_, err := runChatOnce(t, record, conversation())
			chatErr, ok := err.(*ChatError)
			if !ok || chatErr.Category != contract.AIChatBadResponse {
				t.Fatalf("空响应应归 response_error，实际 %v", err)
			}
		})
	}
}

func TestUnknownProtocolIsParameterError(t *testing.T) {
	record, _ := chatOnceFixture(t, "grpc", `{}`)
	_, err := runChatOnce(t, record, conversation())
	chatErr, ok := err.(*ChatError)
	if !ok || chatErr.Category != contract.AIChatBadParameter {
		t.Fatalf("未知协议应归 parameter_error，实际 %v", err)
	}
	// 错误文案不得回显脏协议值，否则等于把内部数据带到界面。
	if chatErr.Error() == "grpc" {
		t.Fatal("错误文案不得回显协议原值")
	}
}

func TestSplitSystemJoinsMultipleSystemMessages(t *testing.T) {
	system, rest := splitSystem([]ChatMessage{
		{Role: "system", Content: "A"}, {Role: "user", Content: "U"}, {Role: "system", Content: "B"},
	})
	if system != "A\nB" {
		t.Fatalf("多条 system 应换行拼接，实际 %q", system)
	}
	if len(rest) != 1 || rest[0].Role != "user" {
		t.Fatalf("非 system 消息应原序保留，实际 %+v", rest)
	}
}

func TestWithDefaultProbeOnlyWhenEmpty(t *testing.T) {
	original := []ChatMessage{{Role: "user", Content: "hi"}}
	if got := withDefaultProbe(original); len(got) != 1 || got[0].Content != "hi" {
		t.Fatalf("非空消息不得被改写，实际 %+v", got)
	}
	if got := withDefaultProbe(nil); len(got) != 1 || got[0].Role != "user" {
		t.Fatalf("空消息应补一条 user 探针，实际 %+v", got)
	}
}

// usageReply 返回某协议"带 usage"的完整响应体，用于两个方向的 token 口径断言。
// zero=true 时显式报 0（缓存命中是真实场景），否则报非零值。
func usageReply(protocol string, zero bool) string {
	prompt, completion := `11`, `7`
	if zero {
		prompt, completion = `0`, `0`
	}
	switch protocol {
	case contract.AIProtocolOpenAI:
		return `{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":` +
			prompt + `,"completion_tokens":` + completion + `}}`
	case contract.AIProtocolAnthropic:
		return `{"content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":` +
			prompt + `,"output_tokens":` + completion + `}}`
	default:
		return `{"output_text":"OK","usage":{"input_tokens":` +
			prompt + `,"output_tokens":` + completion + `}}`
	}
}

func TestUsageReportedWhenUpstreamReturnsTokens(t *testing.T) {
	// 上报了非零 usage：TokensReported 必须为 true 且值原样透出。
	for _, protocol := range contract.AIProtocols {
		t.Run(protocol, func(t *testing.T) {
			record, _ := chatOnceFixture(t, protocol, usageReply(protocol, false))
			result, err := runChatOnce(t, record, conversation())
			if err != nil {
				t.Fatalf("应成功: %v", err)
			}
			if !result.TokensReported || result.PromptTokens != 11 || result.CompletionTokens != 7 {
				t.Fatalf("%s: 上报了 usage 必须原样透出，实际 reported=%v prompt=%d completion=%d",
					protocol, result.TokensReported, result.PromptTokens, result.CompletionTokens)
			}
		})
	}
}

func TestUsageZeroIsStillReported(t *testing.T) {
	// 这次修复的关键反向用例：上游**真实上报 0**（缓存命中）时，
	// TokensReported 必须仍是 true、值为 0 —— 证明"缺失"与"真实的 0"被区分开了。
	// 若把两者混为一谈，这里的 true 会退化成 false，统计里就会把已知的 0 显示成「未上报」。
	for _, protocol := range contract.AIProtocols {
		t.Run(protocol, func(t *testing.T) {
			record, _ := chatOnceFixture(t, protocol, usageReply(protocol, true))
			result, err := runChatOnce(t, record, conversation())
			if err != nil {
				t.Fatalf("应成功: %v", err)
			}
			if !result.TokensReported {
				t.Fatalf("%s: 上游真实上报 0 时 TokensReported 必须为 true，实际 false", protocol)
			}
			if result.PromptTokens != 0 || result.CompletionTokens != 0 {
				t.Fatalf("%s: 真实 0 应原样透出，实际 prompt=%d completion=%d",
					protocol, result.PromptTokens, result.CompletionTokens)
			}
		})
	}
}

func TestUsageHalfReportedIsUnreported(t *testing.T) {
	// 只报一半（只有 prompt_tokens）：另一half 无从推断，按"未上报"整体处理。
	// 这条锁死 tokensFrom 的口径选择，防止后人改成"有哪个用哪个"。
	reply := `{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":11}}`
	record, _ := chatOnceFixture(t, contract.AIProtocolOpenAI, reply)
	result, err := runChatOnce(t, record, conversation())
	if err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if result.TokensReported {
		t.Fatal("只报一个 token 字段时必须按未上报处理，不得落半真半假的数")
	}
	if result.PromptTokens != 0 || result.CompletionTokens != 0 {
		t.Fatalf("未上报时两个数都应抹成 0，实际 prompt=%d completion=%d",
			result.PromptTokens, result.CompletionTokens)
	}
}

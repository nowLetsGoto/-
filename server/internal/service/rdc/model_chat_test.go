// model_chat_test.go · 统一调用实现的行为测试（mock HTTP，不发起真实扣费调用）。
// 职责：钉死参数注入、六类错误分类、超时、实测摘要脱敏，以及 P1 的限流拒绝 / 选模 / 落统计三条编排语义。
// 输入：httptest.Server 冒充上游、内存 fake 仓储、本地测试主密钥与测试 Key 字面量。
// 输出：全部断言通过；返回体与错误对象均不含完整 Key。
// 边界：不访问 api.scnet.cn、不读环境变量、不写任何真实凭据；每次调用都是本地 mock。
package rdc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/modelkey"
)

const aiTestPlainKey = "sk-test-not-a-real-key-1234"

type capturedCall struct {
	Authorization string
	Body          map[string]any
}

// chatFixture 起 mock 上游并装配完整编排依赖；respond 决定上游怎么回。
func chatFixture(t *testing.T, respond func(http.ResponseWriter, *http.Request)) (*fakeAIRepo, *ModelChatService, *capturedCall, string) {
	t.Helper()
	captured := &capturedCall{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Authorization = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&captured.Body)
		respond(w, r)
	}))
	t.Cleanup(server.Close)

	repo := newFakeAIRepo()
	repo.items["id"] = testModelRecord(t, server.URL)
	repo.defaultRow = repo.items["id"]
	codec, err := modelkey.New([]byte(aiTestMasterKey))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: contract.ModelCallHardTimeout}
	return repo, NewModelChatService(repo, &fakeCallRepo{}, noQuota(), codec, client), captured, aiTestPlainKey
}

// testModelRecord 造一条已加密 Key 的模型记录（base_url 指向 mock 上游）。
func testModelRecord(t *testing.T, baseURL string) contract.AIModelRecord {
	t.Helper()
	codec, err := modelkey.New([]byte(aiTestMasterKey))
	if err != nil {
		t.Fatal(err)
	}
	cipher, nonce, err := codec.Seal(aiTestPlainKey)
	if err != nil {
		t.Fatal(err)
	}
	return contract.AIModelRecord{
		AIModel: contract.AIModel{
			ID: "id", Name: "测试模型", Provider: contract.AIProviderSCNet, Protocol: contract.AIProtocolOpenAI,
			BaseURL: baseURL, ModelID: "model-x", APIKeyMask: modelkey.Mask(aiTestPlainKey),
			IsDefault: true, ContextTokens: 4096, ThinkingEnabled: true,
			ExtraConfig:  json.RawMessage(`{"reasoningEffort":"max","timeoutMs":3000}`),
			Capabilities: []string{}, Enabled: true,
		},
		Ciphertext: cipher, Nonce: nonce,
	}
}

func okResponse(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
}

// countingServer 起一个只数请求数、固定回 ok 的上游，用于断言"是否真的出站"。
func countingServer(t *testing.T) (*int32, *httptest.Server) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		okResponse(w, r)
	}))
	t.Cleanup(server.Close)
	return &hits, server
}

func TestChatInjectsParamsAndUsesFallback(t *testing.T) {
	repo, svc, captured, plain := chatFixture(t, okResponse)
	repo.fallback = true
	result, err := svc.Chat(context.Background(), ChatRequest{TenantID: superScope, Scene: "qa", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("调用应成功: %v", err)
	}
	if !result.UsedFallback || !result.UsedDefault || result.Content != "OK" || result.LatencyMS < 0 {
		t.Fatalf("结果标记错误: %+v", result)
	}
	if captured.Authorization != "Bearer "+plain {
		t.Fatal("请求头必须带 Bearer Key")
	}
	if captured.Body["reasoning_effort"] != "max" || captured.Body["max_tokens"] != float64(4096) {
		t.Fatalf("参数注入错误: %v", captured.Body)
	}
	if captured.Body["temperature"] != float64(1) || captured.Body["top_p"] != float64(1) {
		t.Fatalf("默认扩展参数注入错误: %v", captured.Body)
	}
}

func TestChatThinkingDisabledInjectsNone(t *testing.T) {
	// 直接用同一参数映射验证关思考语义（不依赖 httptest 的间接赋值）。
	model := contract.AIModel{ThinkingEnabled: false, ContextTokens: 2048}
	params := contract.AIModelRequestParams(model)
	if params.ReasoningEffort != contract.AIReasoningNone || params.MaxTokens != 2048 {
		t.Fatalf("关思考映射应为 none / 2048，实际 %+v", params)
	}
}

func TestErrorCategories(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   contract.AIChatCategory
	}{
		{"鉴权失败", http.StatusUnauthorized, `{"error":"bad"}`, contract.AIChatAuthFailed},
		{"参数错误", http.StatusBadRequest, `{"error":"bad"}`, contract.AIChatBadParameter},
		{"不可接受", http.StatusUnprocessableEntity, `{"error":"bad"}`, contract.AIChatBadParameter},
		{"限流", http.StatusTooManyRequests, `{"error":"bad"}`, contract.AIChatRateLimited},
		{"上游故障", http.StatusServiceUnavailable, `{"error":"bad"}`, contract.AIChatRateLimited},
		{"响应异常", http.StatusOK, `not-json`, contract.AIChatBadResponse},
		{"空响应", http.StatusOK, `{"choices":[]}`, contract.AIChatBadResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc, _, plain := chatFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := svc.Chat(context.Background(), ChatRequest{TenantID: superScope})
			chatErr, ok := err.(*ChatError)
			if !ok || chatErr.Category != tc.want {
				t.Fatalf("分类应为 %s，实际 %v", tc.want, err)
			}
			if strings.Contains(chatErr.Error(), plain) || strings.Contains(chatErr.Error(), "bad") {
				t.Fatalf("错误文案不得含 Key 或上游原文: %s", chatErr.Error())
			}
		})
	}
}

func TestCancelIsNetworkError(t *testing.T) {
	_, svc, _, _ := chatFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		okResponse(w, nil)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.Chat(ctx, ChatRequest{TenantID: superScope})
	chatErr, ok := err.(*ChatError)
	if !ok || chatErr.Category != contract.AIChatNetwork {
		t.Fatalf("context 取消应归 network_error，实际 %v", err)
	}
}

func TestConfigMissingWithoutKey(t *testing.T) {
	repo := newFakeAIRepo()
	codec, _ := modelkey.New([]byte(aiTestMasterKey))
	svc := NewModelChatService(repo, &fakeCallRepo{}, noQuota(), codec, nil)
	_, err := svc.Chat(context.Background(), ChatRequest{TenantID: superScope})
	chatErr, ok := err.(*ChatError)
	if !ok || chatErr.Category != contract.AIChatConfigMissing {
		t.Fatalf("未配置模型应归 config_missing，实际 %v", err)
	}
}

func TestTestResultNeverContainsKey(t *testing.T) {
	_, svc, _, plain := chatFixture(t, okResponse)
	result, err := svc.Test(context.Background(), contract.Scope{TenantID: superScope}, "id", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.StatusCode != http.StatusOK {
		t.Fatalf("实测应成功: %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), plain) {
		t.Fatal("实测结果 JSON 不得包含 Key")
	}
}

// ===== P1 编排语义：限流 / 选模 / 落统计 =====

func TestChatRejectSendsNoUpstreamRequestButRecordsRow(t *testing.T) {
	// C7：reject 命中时上游必须收到 0 个请求，且库里落 1 条 used_upstream=false 的 rate_limited 记录。
	hits, server := countingServer(t)
	repo := newFakeAIRepo()
	repo.defaultRow = testModelRecord(t, server.URL)
	codec, _ := modelkey.New([]byte(aiTestMasterKey))
	calls := &fakeCallRepo{usedCount: 10}
	quotas := &fakeQuotaRepo{cfg: contract.AIQuotaConfig{
		Enabled: true, MaxCalls: 10, WindowMinutes: 60, Action: contract.AIQuotaActionReject,
	}}
	svc := NewModelChatService(repo, calls, quotas, codec, nil)

	_, err := svc.Chat(context.Background(), ChatRequest{TenantID: superScope, Scene: "qa"})
	var chatErr *ChatError
	if !errors.As(err, &chatErr) || chatErr.Category != contract.AIChatRateLimited {
		t.Fatalf("超限且 reject 必须返回 rate_limited，实际 %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("限流拒绝时上游必须收到 0 个请求，实际 %d", got)
	}
	if len(calls.inserted) != 1 {
		t.Fatalf("拒绝也必须落 1 条记录，实际 %d 条", len(calls.inserted))
	}
	row := calls.lastInserted()
	if row.UsedUpstream || row.Success || row.ErrorCategory != string(contract.AIChatRateLimited) {
		t.Fatalf("被拒记录口径错误: %+v", row)
	}
	if row.ModelID != "" {
		t.Fatalf("无匹配模型时 model_id 必须留空（落 NULL），实际 %s", row.ModelID)
	}
}

func TestChatWarnAllowsAndStillCallsUpstream(t *testing.T) {
	// C6：warn 动作在超限时照常调用，只是留一条告警日志。
	hits, server := countingServer(t)
	repo := newFakeAIRepo()
	repo.defaultRow = testModelRecord(t, server.URL)
	codec, _ := modelkey.New([]byte(aiTestMasterKey))
	calls := &fakeCallRepo{usedCount: 99}
	quotas := &fakeQuotaRepo{cfg: contract.AIQuotaConfig{
		Enabled: true, MaxCalls: 10, WindowMinutes: 60, Action: contract.AIQuotaActionWarn,
	}}
	svc := NewModelChatService(repo, calls, quotas, codec, nil)

	if _, err := svc.Chat(context.Background(), ChatRequest{TenantID: superScope}); err != nil {
		t.Fatalf("warn 动作必须放行: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("warn 动作必须真实出站 1 次，实际 %d", got)
	}
	if row := calls.lastInserted(); !row.UsedUpstream || !row.Success {
		t.Fatalf("成功记录口径错误: %+v", row)
	}
}

func TestChatQuotaReadFailureFailsOpen(t *testing.T) {
	// 读阈值失败 → 放行（fail-open），并正常出站。
	hits, server := countingServer(t)
	repo := newFakeAIRepo()
	repo.defaultRow = testModelRecord(t, server.URL)
	codec, _ := modelkey.New([]byte(aiTestMasterKey))
	quotas := &fakeQuotaRepo{findErr: errors.New("db down")}
	svc := NewModelChatService(repo, &fakeCallRepo{}, quotas, codec, nil)

	if _, err := svc.Chat(context.Background(), ChatRequest{TenantID: superScope}); err != nil {
		t.Fatalf("阈值读取失败必须放行: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("fail-open 后必须出站 1 次，实际 %d", got)
	}
}

func TestChatRecordFailureDoesNotBreakResult(t *testing.T) {
	// C2：写记录失败只记日志，成功结果不能被翻成失败。
	_, server := countingServer(t)
	repo := newFakeAIRepo()
	repo.defaultRow = testModelRecord(t, server.URL)
	codec, _ := modelkey.New([]byte(aiTestMasterKey))
	calls := &fakeCallRepo{insertErr: errors.New("insert failed")}
	svc := NewModelChatService(repo, calls, noQuota(), codec, nil)

	result, err := svc.Chat(context.Background(), ChatRequest{TenantID: superScope})
	if err != nil {
		t.Fatalf("写记录失败不得影响调用结果: %v", err)
	}
	if result.Content != "OK" {
		t.Fatalf("结果内容应保留，实际 %q", result.Content)
	}
}

func TestChatDeclaredNeedsRouteToSelection(t *testing.T) {
	// 声明需求后必须走 ListEnabled + selectModel：候选只有 8K 模型时，≥16K 请求必须 capability_mismatch。
	hits, server := countingServer(t)
	small := testModelRecord(t, server.URL)
	small.ContextTokens = 8192
	small.Capabilities = []string{"视觉理解"}
	repo := newFakeAIRepo()
	repo.defaultRow = small
	repo.enabledItems = []contract.AIModelRecord{small}
	codec, _ := modelkey.New([]byte(aiTestMasterKey))
	calls := &fakeCallRepo{}
	svc := NewModelChatService(repo, calls, noQuota(), codec, nil)

	_, err := svc.Chat(context.Background(), ChatRequest{
		TenantID: superScope, Capabilities: []string{"视觉理解"}, MinContextTokens: 16384,
	})
	var chatErr *ChatError
	if !errors.As(err, &chatErr) || chatErr.Category != contract.AIChatCapabilityMismatch {
		t.Fatalf("无匹配必须 capability_mismatch，实际 %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("无匹配时不得出站，实际 %d 次", got)
	}
	if row := calls.lastInserted(); row.UsedUpstream ||
		row.ErrorCategory != string(contract.AIChatCapabilityMismatch) {
		t.Fatalf("无匹配记录口径错误: %+v", row)
	}
}

func TestChatIllegalNeedsNeitherCallNorRecord(t *testing.T) {
	// B9：非法入参是调用方 bug，不算"一次调用"，所以既不落库也不出站。
	hits, server := countingServer(t)
	repo := newFakeAIRepo()
	repo.defaultRow = testModelRecord(t, server.URL)
	codec, _ := modelkey.New([]byte(aiTestMasterKey))
	calls := &fakeCallRepo{}
	svc := NewModelChatService(repo, calls, noQuota(), codec, nil)

	_, err := svc.Chat(context.Background(), ChatRequest{TenantID: superScope, ReasoningEffort: "ultra"})
	var chatErr *ChatError
	if !errors.As(err, &chatErr) || chatErr.Category != contract.AIChatBadParameter {
		t.Fatalf("非法档位必须 parameter_error，实际 %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 0 || len(calls.inserted) != 0 {
		t.Fatalf("非法入参不得出站也不得落库: hits=%d rows=%d", got, len(calls.inserted))
	}
}

func TestChatRecordsTokensAndRequirementSnapshot(t *testing.T) {
	// C1/C3：成功记录必须带 token、耗时与需求快照，且不含任何 Key 信息。
	_, server := countingServer(t)
	repo := newFakeAIRepo()
	record := testModelRecord(t, server.URL)
	record.Capabilities = []string{"视觉理解"}
	repo.defaultRow = record
	repo.enabledItems = []contract.AIModelRecord{record}
	codec, _ := modelkey.New([]byte(aiTestMasterKey))
	calls := &fakeCallRepo{}
	svc := NewModelChatService(repo, calls, noQuota(), codec, nil)

	_, err := svc.Chat(context.Background(), ChatRequest{
		TenantID: superScope, Scene: "interaction",
		Capabilities: []string{"视觉理解"}, MinContextTokens: 4096, ReasoningEffort: contract.AIReasoningLow,
	})
	if err != nil {
		t.Fatal(err)
	}
	row := calls.lastInserted()
	if !row.Success || !row.UsedUpstream || !row.TokensReported {
		t.Fatalf("成功记录标记错误: %+v", row)
	}
	if row.PromptTokens == nil || *row.PromptTokens != 3 ||
		row.CompletionTokens == nil || *row.CompletionTokens != 1 {
		t.Fatalf("token 必须落实际值: %+v", row)
	}
	if row.Scene != "interaction" || row.RequestedMinContext != 4096 ||
		row.RequestedEffort != contract.AIReasoningLow ||
		!slicesEqualString(row.RequestedCapabilities, []string{"视觉理解"}) {
		t.Fatalf("需求快照丢失: %+v", row)
	}
	encoded, _ := json.Marshal(row)
	if strings.Contains(string(encoded), aiTestPlainKey) {
		t.Fatal("调用记录不得含 Key")
	}
}

// slicesEqualString 就地比较两个字符串切片，避免为测试再引比较工具包。
func slicesEqualString(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

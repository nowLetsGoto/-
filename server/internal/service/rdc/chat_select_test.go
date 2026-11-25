// chat_select_test.go · 选模纯函数的规则测试（无 DB、无时钟、无 HTTP）。
// 职责：按 PRD §3.2 的 fixture A/B/C/D 钉死硬筛顺序、`>=` 语义、AND 语义、默认不豁免、稳定排序与非法入参。
// 输入：就地构造的 contract.AIModelRecord 切片与 ChatNeeds。
// 输出：全部断言通过；证明调度规则可脱离数据库单测。
// 边界：不连数据库、不读环境变量、不发起任何网络调用。
package rdc

import (
	"errors"
	"slices"
	"testing"

	"dandelion/server/internal/contract"
)

// fixtureModel 造一条模型记录；effort 为空时视为关闭思考（只能提供 none）。
// createdAt 用可比较的 RFC3339 串，让"创建早的胜出"可断言。
func fixtureModel(id string, contextTokens int, effort string, createdAt string, capabilities ...string) contract.AIModelRecord {
	record := contract.AIModelRecord{
		AIModel: contract.AIModel{
			ID: id, Name: "模型" + id, Protocol: contract.AIProtocolOpenAI, ModelID: "id-" + id,
			ThinkingEnabled: effort != "", ContextTokens: contextTokens,
			ExtraConfig:  []byte(`{"reasoningEffort":"` + effort + `"}`),
			Capabilities: capabilities, Enabled: true, CreatedAt: createdAt,
		},
	}
	if !record.ThinkingEnabled {
		record.ExtraConfig = []byte(`{}`)
	}
	return record
}

// prdFixtures 是 PRD §3.2 的四模型 fixture，顺序刻意打散（C 在最前），
// 以证明"结果不依赖入参顺序"——排序由 Step 6 决定，不是"谁先来谁赢"。
func prdFixtures() []contract.AIModelRecord {
	return []contract.AIModelRecord{
		fixtureModel("C", 131072, contract.AIReasoningLow, "2026-01-03T00:00:00Z", "文本生成"),
		fixtureModel("A", 8192, "", "2026-01-01T00:00:00Z", "文本生成"),
		fixtureModel("B", 32768, contract.AIReasoningHigh, "2026-01-02T00:00:00Z", "文本生成", "视觉理解", "深度思考"),
		fixtureModel("D", 32768, contract.AIReasoningHigh, "2026-01-04T00:00:00Z", "文本生成", "深度思考"),
	}
}

// selectWith 是本文件唯一的调用封装：把原始请求字段归一后交给 selectModel，
// 与生产路径（Chat → normalizeNeeds → selectModel）逐段一致，避免测试另造一套入参。
func selectWith(t *testing.T, candidates []contract.AIModelRecord, in ChatRequest) (contract.AIModelRecord, error) {
	t.Helper()
	needs, err := normalizeNeeds(in)
	if err != nil {
		return contract.AIModelRecord{}, err
	}
	return selectModel(candidates, needs)
}

func TestSelectModelVisualHighContextOnlyMatchesB(t *testing.T) {
	// 视觉 + ≥16K + high：A（8K/无思考）上下文与档位都不足；C（128K/low）档位不足；
	// D（32K/high/深思考）能力不足。只有 B 同时满足三项 —— 这是设计的核心断言。
	record, err := selectWith(t, prdFixtures(), ChatRequest{
		Capabilities: []string{"视觉理解"}, MinContextTokens: 16384, ReasoningEffort: contract.AIReasoningHigh,
	})
	if err != nil {
		t.Fatalf("应命中 B，实际报错: %v", err)
	}
	if record.ID != "B" {
		t.Fatalf("应只命中 B，实际 %s", record.ID)
	}
}

func TestSelectModelExcludesCapableButTooSmall(t *testing.T) {
	// PRD B2 第二组：能力满足但上下文不足的模型必须被**排除**，而不是因为"能力吻合度最高"胜出。
	// 这里给 A 打上视觉标签，让它能力全中但只有 8K —— 若筛选顺序写错（先能力），A 会胜出。
	candidates := []contract.AIModelRecord{
		fixtureModel("A", 8192, contract.AIReasoningHigh, "2026-01-01T00:00:00Z", "视觉理解"),
		fixtureModel("B", 32768, contract.AIReasoningHigh, "2026-01-02T00:00:00Z", "视觉理解"),
	}
	record, err := selectWith(t, candidates, ChatRequest{Capabilities: []string{"视觉理解"}, MinContextTokens: 16384})
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "B" {
		t.Fatalf("上下文不足的 A 必须被排除，实际命中 %s", record.ID)
	}
}

func TestSelectModelContextUsesGreaterOrEqual(t *testing.T) {
	// PRD B6：模型 8192、请求 8192 必须命中（含等于）；请求 8193 必须被排除。
	model := []contract.AIModelRecord{fixtureModel("A", 8192, "", "2026-01-01T00:00:00Z")}
	if record, err := selectWith(t, model, ChatRequest{MinContextTokens: 8192}); err != nil || record.ID != "A" {
		t.Fatalf("恰好等于时必须命中: %v err=%v", record.ID, err)
	}
	_, err := selectWith(t, model, ChatRequest{MinContextTokens: 8193})
	var chatErr *ChatError
	if !errors.As(err, &chatErr) || chatErr.Category != contract.AIChatCapabilityMismatch {
		t.Fatalf("超出容量必须 capability_mismatch，实际 %v", err)
	}
}

func TestSelectModelEffortUsesRankOrder(t *testing.T) {
	// PRD B5：档位序 none < low < high < max。可提供 high 的模型能接 low/high，不能接 max。
	high := []contract.AIModelRecord{fixtureModel("H", 32768, contract.AIReasoningHigh, "2026-01-01T00:00:00Z")}
	for _, effort := range []string{contract.AIReasoningLow, contract.AIReasoningHigh, contract.AIReasoningNone} {
		if _, err := selectWith(t, high, ChatRequest{ReasoningEffort: effort}); err != nil {
			t.Fatalf("请求 %s 应命中可提供 high 的模型: %v", effort, err)
		}
	}
	if _, err := selectWith(t, high, ChatRequest{ReasoningEffort: contract.AIReasoningMax}); err == nil {
		t.Fatal("请求 max 不得命中只能提供 high 的模型")
	}
}

func TestSelectModelThinkingDisabledCannotSatisfyEffort(t *testing.T) {
	// PRD B5 后半：thinking_enabled=false 的模型只能提供 none，任何档位请求都必须失配。
	off := []contract.AIModelRecord{fixtureModel("Off", 131072, "", "2026-01-01T00:00:00Z")}
	for _, effort := range []string{contract.AIReasoningLow, contract.AIReasoningHigh, contract.AIReasoningMax} {
		if _, err := selectWith(t, off, ChatRequest{ReasoningEffort: effort}); err == nil {
			t.Fatalf("关思考模型不得满足 %s 档位请求", effort)
		}
	}
	if _, err := selectWith(t, off, ChatRequest{ReasoningEffort: contract.AIReasoningNone}); err != nil {
		t.Fatalf("请求 none 等价于不约束，应命中: %v", err)
	}
}

func TestSelectModelCapabilitiesAreAndSemantics(t *testing.T) {
	// PRD B4：请求每一项都必须具备；超集模型不受多余标签影响。
	partial := []contract.AIModelRecord{fixtureModel("P", 32768, "", "2026-01-01T00:00:00Z", "视觉理解")}
	if _, err := selectWith(t, partial, ChatRequest{Capabilities: []string{"视觉理解", "深度思考"}}); err == nil {
		t.Fatal("缺一项能力必须失配（AND 语义）")
	}
	superset := []contract.AIModelRecord{
		fixtureModel("S", 32768, "", "2026-01-01T00:00:00Z", "文本生成", "视觉理解", "深度思考"),
	}
	if record, err := selectWith(t, superset, ChatRequest{Capabilities: []string{"视觉理解"}}); err != nil || record.ID != "S" {
		t.Fatalf("超集模型必须可命中: %v err=%v", record.ID, err)
	}
}

func TestSelectModelDefaultDoesNotBypassHardFilters(t *testing.T) {
	// PRD B7：默认标记不构成越过硬约束的理由 —— 默认模型 8K 时，≥16K 的请求必须落到 B。
	defaultSmall := fixtureModel("A", 8192, "", "2026-01-01T00:00:00Z", "视觉理解")
	defaultSmall.IsDefault = true
	candidates := []contract.AIModelRecord{
		defaultSmall,
		fixtureModel("B", 32768, "", "2026-01-02T00:00:00Z", "视觉理解"),
	}
	record, err := selectWith(t, candidates, ChatRequest{Capabilities: []string{"视觉理解"}, MinContextTokens: 16384})
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "B" {
		t.Fatalf("默认模型不得豁免上下文约束，实际命中 %s", record.ID)
	}
}

func TestSelectModelStableOrderingPrefersDefault(t *testing.T) {
	// PRD B8 前半：都满足条件时默认者胜出（即便更晚创建）。
	early := fixtureModel("Early", 32768, "", "2026-01-01T00:00:00Z")
	lateDefault := fixtureModel("LateDefault", 32768, "", "2026-09-01T00:00:00Z")
	lateDefault.IsDefault = true
	record, err := selectWith(t, []contract.AIModelRecord{early, lateDefault}, ChatRequest{MinContextTokens: 16384})
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "LateDefault" {
		t.Fatalf("默认者必须胜出，实际 %s", record.ID)
	}
}

func TestSelectModelStableOrderingPrefersEarlierCreated(t *testing.T) {
	// PRD B8 后半：都非默认时创建早的胜出；打乱入参顺序仍取同一个 —— 结果可复现、不随机。
	candidates := []contract.AIModelRecord{
		fixtureModel("Later", 32768, "", "2026-05-01T00:00:00Z"),
		fixtureModel("Earlier", 32768, "", "2026-01-01T00:00:00Z"),
	}
	for _, order := range [][]contract.AIModelRecord{candidates, {candidates[1], candidates[0]}} {
		record, err := selectWith(t, order, ChatRequest{MinContextTokens: 16384})
		if err != nil {
			t.Fatal(err)
		}
		if record.ID != "Earlier" {
			t.Fatalf("创建早者必须胜出且与入参顺序无关，实际 %s", record.ID)
		}
	}
}

func TestSelectModelZeroNeedsMatchesP0Semantics(t *testing.T) {
	// B1：三字段全零值 == 未声明，必须与 P0 的 `is_default DESC, created_at ASC` 选出同一条。
	// 这里用同一 fixture 做对照：P0 的 SQL 取"默认优先、其次最早创建"，纯函数必须给同一答案。
	candidates := prdFixtures()
	candidates[2].IsDefault = true // B 设为默认
	needs, err := normalizeNeeds(ChatRequest{TenantID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if needs.Declared() {
		t.Fatal("三字段全零值必须视为未声明")
	}
	record, err := selectModel(candidates, needs)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "B" {
		t.Fatalf("零值请求必须命中默认模型 B，实际 %s", record.ID)
	}
}

func TestSelectModelZeroNeedsWithoutDefaultPicksEarliest(t *testing.T) {
	// 零值 + 无默认：与 P0 SQL 的 created_at ASC 同序，A（2026-01-01）胜出。
	needs, _ := normalizeNeeds(ChatRequest{})
	record, err := selectModel(prdFixtures(), needs)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "A" {
		t.Fatalf("无默认时必须取最早创建者 A，实际 %s", record.ID)
	}
}

func TestSelectModelMismatchMessageListsDeclaredDimensions(t *testing.T) {
	// B3：文案只列调用方**声明了的**维度，并给出公司实际最大值，便于自助排障。
	_, err := selectWith(t, prdFixtures(), ChatRequest{Capabilities: []string{"视觉理解"}, MinContextTokens: 204800})
	var chatErr *ChatError
	if !errors.As(err, &chatErr) || chatErr.Category != contract.AIChatCapabilityMismatch {
		t.Fatalf("必须 capability_mismatch，实际 %v", err)
	}
	for _, want := range []string{"204800", "131072", "视觉理解"} {
		if !containsText(chatErr.Message, want) {
			t.Fatalf("文案必须含 %q，实际: %s", want, chatErr.Message)
		}
	}
}

func TestSelectModelEmptyCandidateSetIsMismatch(t *testing.T) {
	// 一个模型都没配：也是"配了但不够用"的一种，绝不 panic、绝不返回零值记录。
	_, err := selectWith(t, nil, ChatRequest{Capabilities: []string{"视觉理解"}})
	var chatErr *ChatError
	if !errors.As(err, &chatErr) || chatErr.Category != contract.AIChatCapabilityMismatch {
		t.Fatalf("空候选集必须 capability_mismatch，实际 %v", err)
	}
}

func TestNormalizeNeedsRejectsIllegalInput(t *testing.T) {
	// B9：三条非法入参都必须是 parameter_error（调用方 bug，不落库、不出站）。
	for _, tc := range []struct {
		name string
		in   ChatRequest
	}{
		{"白名单外能力", ChatRequest{Capabilities: []string{"视频生成"}}},
		{"非四档档位", ChatRequest{ReasoningEffort: "ultra"}},
		{"负数上下文", ChatRequest{MinContextTokens: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeNeeds(tc.in)
			var chatErr *ChatError
			if !errors.As(err, &chatErr) || chatErr.Category != contract.AIChatBadParameter {
				t.Fatalf("必须 parameter_error，实际 %v", err)
			}
		})
	}
}

func TestNormalizeCapabilitiesTrimsDedupesAndKeepsOrder(t *testing.T) {
	// 能力是集合语义：去空白、去空串、去重，但保持首次出现顺序（错误文案与日志可复现）。
	got := normalizeCapabilities([]string{" 视觉理解 ", "", "深度思考", "视觉理解", "   "})
	want := []string{"视觉理解", "深度思考"}
	if !slices.Equal(got, want) {
		t.Fatalf("归一结果应为 %v，实际 %v", want, got)
	}
}

// containsText 是 strings.Contains 的可读别名：本文件断言的是"文案含某片段"，
// 用独立函数名让测试意图在断言行就能读懂。
func containsText(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

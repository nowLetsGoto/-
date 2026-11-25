// ai_model_test.go · AI 模型契约一致性测试。
// 职责：把 openapi 的 x-rdc-ai-* 与 Go 枚举、extra_config 白名单、模板常量逐项锁死。
// 输入：api/openapi.yaml 与 contract/ai_model.go 的常量、DTO、纯函数。
// 输出：任一漂移即 t.Fatal；响应 DTO 不得出现完整 Key 字段。
// 边界：只读契约文件与纯函数，不访问 DB / 网络 / 环境变量。
package contract

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestAIEnumsMatchContract(t *testing.T) {
	spec := loadSpec(t)
	for _, tc := range []struct {
		name string
		yaml []string
		want []string
	}{
		{"providers", spec.XRDCAIProviders, AIProviders},
		{"protocols", spec.XRDCAIProtocols, AIProtocols},
		{"capabilities", spec.XRDCAICapabilities, AICapabilities},
		{"reasoning-efforts", spec.XRDCAIReasoningEfforts, AIReasoningEfforts},
		{"test-categories", spec.XRDCAITestCategories, AIChatCategories},
	} {
		if !reflect.DeepEqual(tc.yaml, tc.want) {
			t.Fatalf("x-rdc-ai-%s=%v 与 Go=%v 不一致", tc.name, tc.yaml, tc.want)
		}
	}
	got := spec.XRDCAIContextRange
	if got.Min != AIMinContextTokens || got.Max != AIMaxContextTokens || got.Default != AIDefaultContextTokens {
		t.Fatalf("上下文区间不一致: 契约=%+v Go=%d/%d/%d", got, AIMinContextTokens, AIMaxContextTokens, AIDefaultContextTokens)
	}
}

func TestAIExtraConfigMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	keys := make([]string, 0, len(spec.XRDCAIExtraConfig))
	for key := range spec.XRDCAIExtraConfig {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := append([]string(nil), AIExtraConfigKeys...)
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("x-rdc-ai-extra-config 键=%v 与白名单=%v 不一致", keys, want)
	}
	checks := []struct {
		key  string
		min  float64
		max  float64
		def  any
		excl bool
	}{
		{"temperature", 0, 2, 1, false},
		{"topP", 0, 1, 1, true},
		{"reasoningEffort", 0, 0, "high", false},
		{"timeoutMs", float64(AIExtraTimeoutMinMS), float64(AIExtraTimeoutMaxMS), AIExtraTimeoutDefaultMS, false},
	}
	for _, check := range checks {
		field := spec.XRDCAIExtraConfig[check.key]
		if field.Default != check.def {
			t.Fatalf("%s 默认值不一致: 契约=%v Go=%v", check.key, field.Default, check.def)
		}
		if check.key == "reasoningEffort" {
			if !reflect.DeepEqual(field.Enum, AIReasoningEfforts) {
				t.Fatalf("reasoningEffort 枚举不一致: %v", field.Enum)
			}
			continue
		}
		if field.Minimum == nil || *field.Minimum != check.min || field.Maximum == nil || *field.Maximum != check.max {
			t.Fatalf("%s 取值区间不一致: %+v", check.key, field)
		}
		if field.ExclusiveMin != check.excl {
			t.Fatalf("%s exclusiveMin 不一致: %v", check.key, field.ExclusiveMin)
		}
	}
}

func TestAIExtraConfigValidation(t *testing.T) {
	valid := json.RawMessage(`{"temperature":0.5,"topP":0.8,"reasoningEffort":"low","timeoutMs":5000}`)
	normalized, err := ValidateAIExtraConfig(valid, AIProtocolOpenAI)
	if err != nil {
		t.Fatalf("合法扩展配置应通过: %v", err)
	}
	if string(normalized) != `{"temperature":0.5,"topP":0.8,"reasoningEffort":"low","timeoutMs":5000}` {
		t.Fatalf("规范化结果不符合字段顺序/默认值: %s", normalized)
	}
	for _, invalid := range []string{
		`{"unknown":1}`, `{"temperature":2.1}`, `{"topP":0}`, `{"reasoningEffort":"ultra"}`, `{"timeoutMs":999}`,
	} {
		if _, err := ValidateAIExtraConfig(json.RawMessage(invalid), AIProtocolOpenAI); err == nil {
			t.Fatalf("非法扩展配置 %s 必须被拒", invalid)
		}
	}
	// 读取时未知键忽略、越界补默认，保证历史行不会因为新增字段而报错。
	legacy := NormalizeAIExtraConfig(json.RawMessage(`{"unknown":1,"temperature":9,"timeoutMs":1000}`))
	if !strings.Contains(string(legacy), `"temperature":1`) || !strings.Contains(string(legacy), `"timeoutMs":1000`) {
		t.Fatalf("读取规范化失败: %s", legacy)
	}
}

func TestAIModelRequestParamsMapping(t *testing.T) {
	model := AIModel{
		ContextTokens:   4096,
		ThinkingEnabled: false,
		ExtraConfig:     json.RawMessage(`{"reasoningEffort":"max"}`),
	}
	params := AIModelRequestParams(model)
	if params.ReasoningEffort != AIReasoningNone || params.MaxTokens != 4096 || params.TimeoutMS != AIExtraTimeoutDefaultMS {
		t.Fatalf("关思考映射错误: %+v", params)
	}
	model.ThinkingEnabled = true
	params = AIModelRequestParams(model)
	if params.ReasoningEffort != AIReasoningMax {
		t.Fatalf("开思考应取 extra_config.reasoningEffort，实际 %s", params.ReasoningEffort)
	}
}

func TestSCTemplateMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	template := spec.XRDCAITemplate
	goTemplate := SCTemplate()
	if template.Name != SCTemplateName || template.Provider != SCTemplateProvider || template.Protocol != SCTemplateProtocol ||
		template.BaseURL != SCTemplateBaseURL || template.ModelID != SCTemplateModelID ||
		template.ContextTokens != SCTemplateContext || !template.ThinkingEnabled ||
		!reflect.DeepEqual(template.Capabilities, SCTemplateCapabilities) {
		t.Fatalf("scnet 模板与契约不一致: 契约=%+v Go=%+v", template, goTemplate)
	}
	if goTemplate.APIKey != "" {
		t.Fatal("模板绝不携带 Key")
	}
}

func TestAIDTOWhitelist(t *testing.T) {
	spec := loadSpec(t)
	for _, forbidden := range []string{"apiKey", "apiKeyCiphertext", "apiKeyNonce", "tenantId"} {
		if _, ok := spec.Components.Schemas["AIModel"].Properties[forbidden]; ok {
			t.Fatalf("AIModel 响应 schema 不得含 %s", forbidden)
		}
	}
	for _, dto := range []any{AIModel{}, AIModelTestResult{}, AIModelRecord{}} {
		typed := reflect.TypeOf(dto)
		for i := 0; i < typed.NumField(); i++ {
			tag := typed.Field(i).Tag.Get("json")
			if tag == "apiKey" || tag == "apiKeyCiphertext" || tag == "apiKeyNonce" {
				t.Fatalf("%s 不得出现完整 Key 字段 %s", typed.Name(), tag)
			}
		}
	}
	input := reflect.TypeOf(AIModelInput{})
	allowed := map[string]bool{
		"name": true, "provider": true, "protocol": true, "baseUrl": true, "modelId": true,
		"apiKey": true, "thinkingEnabled": true, "contextTokens": true, "extraConfig": true, "capabilities": true,
	}
	for i := 0; i < input.NumField(); i++ {
		tag := strings.Split(input.Field(i).Tag.Get("json"), ",")[0]
		if !allowed[tag] {
			t.Fatalf("AIModelInput 出现非法入站字段 %s", tag)
		}
	}
}

func TestAIPermissionsAndMenu(t *testing.T) {
	if !IsValidPermission(PermissionRDCModelRead) || !IsValidPermission(PermissionRDCModelWrite) {
		t.Fatal("AI 模型读写点必须在可勾选目录里")
	}
	found := false
	for _, menu := range Menus {
		if menu.Key != "/rdc/models" {
			continue
		}
		found = true
		if menu.ViewPermission != PermissionRDCModelRead || menu.AutoGrantBuiltin {
			t.Fatalf("AI 模型菜单门槛错误: %+v", menu)
		}
		if len(menu.Actions) != 1 || menu.Actions[0].Key != PermissionRDCModelWrite {
			t.Fatalf("AI 模型菜单必须只挂写点: %+v", menu.Actions)
		}
	}
	if !found {
		t.Fatal("缺少 /rdc/models 菜单")
	}
}

func TestAIProtocolEnumHasThreeValues(t *testing.T) {
	spec := loadSpec(t)
	if len(AIProtocols) != 3 {
		t.Fatalf("AIProtocols 应为 3 值，实际 %d: %v", len(AIProtocols), AIProtocols)
	}
	// 三处同值：Go 常量 / x-rdc-ai-protocols / components.schemas.AIProtocol.enum。
	// 任何一处漂移都是"新增协议只改两处"的返工信号，所以这里有意识地断言三次。
	if !reflect.DeepEqual(spec.Components.Schemas["AIProtocol"].Enum, AIProtocols) {
		t.Fatalf("AIProtocol enum=%v 与 Go=%v 不一致", spec.Components.Schemas["AIProtocol"].Enum, AIProtocols)
	}
	if !reflect.DeepEqual(spec.XRDCAIProtocols, AIProtocols) {
		t.Fatalf("x-rdc-ai-protocols=%v 与 Go=%v 不一致", spec.XRDCAIProtocols, AIProtocols)
	}
	for _, want := range []string{AIProtocolOpenAI, AIProtocolAnthropic, AIProtocolOpenAIResponses} {
		if !slices.Contains(AIProtocols, want) {
			t.Fatalf("AIProtocols 缺少 %s", want)
		}
	}
}

func TestAIReasoningRankOrder(t *testing.T) {
	// 四个合法档位 + 一个非法值：档位序是调度匹配与协议折算的共同前提，错一位就全错。
	for _, tc := range []struct {
		effort string
		rank   int
	}{
		{AIReasoningNone, 0},
		{AIReasoningLow, 1},
		{AIReasoningHigh, 2},
		{AIReasoningMax, 3},
		{"", -1},
		{"ultra", -1},
	} {
		if got := AIReasoningRank(tc.effort); got != tc.rank {
			t.Fatalf("AIReasoningRank(%q)=%d，期望 %d", tc.effort, got, tc.rank)
		}
	}
	// 单调性：白名单顺序与序号必须一一对应，防止有人只改常量不改 switch。
	for index, effort := range AIReasoningEfforts {
		if AIReasoningRank(effort) != index {
			t.Fatalf("AIReasoningEfforts[%d]=%s 的序号不是 %d", index, effort, index)
		}
	}
}

func TestAIChatCategoriesAreSeven(t *testing.T) {
	// 6 → 7：capability_mismatch 是 P1 唯一新增类别。
	if len(AIChatCategories) != 7 {
		t.Fatalf("AIChatCategories 应为 7 类，实际 %d: %v", len(AIChatCategories), AIChatCategories)
	}
	if !IsValidAIChatCategory(string(AIChatCapabilityMismatch)) {
		t.Fatal("capability_mismatch 必须被视为合法类别")
	}
	message := AIChatMessage(AIChatCapabilityMismatch)
	// 文案要能指导处置：说清"没有满足条件的模型"且给出三个可调维度，而不是笼统的"调用失败"。
	for _, keyword := range []string{"没有满足条件的模型", "能力", "上下文", "思考"} {
		if !strings.Contains(message, keyword) {
			t.Fatalf("capability_mismatch 文案缺少关键词 %q: %s", keyword, message)
		}
	}
}

func TestAIModelOfferedEffortAndTemperature(t *testing.T) {
	// 关思考 = 只能提供 none（无论 extra_config 写了什么）。
	closed := AIModel{ThinkingEnabled: false, ExtraConfig: json.RawMessage(`{"reasoningEffort":"max"}`)}
	if got := AIModelOfferedEffort(closed); got != AIReasoningNone {
		t.Fatalf("关思考的模型可提供档位应为 none，实际 %s", got)
	}
	open := AIModel{ThinkingEnabled: true, ExtraConfig: json.RawMessage(`{"reasoningEffort":"max"}`)}
	if got := AIModelOfferedEffort(open); got != AIReasoningMax {
		t.Fatalf("开思考的模型应取 extra_config 档位，实际 %s", got)
	}
	// 温度上限按协议走：anthropic 只接 0–1，其余 0–2。
	for protocol, want := range map[string]float64{
		AIProtocolOpenAI:          AIMaxTemperatureDefault,
		AIProtocolOpenAIResponses: AIMaxTemperatureDefault,
		AIProtocolAnthropic:       AIMaxTemperatureAnthropic,
	} {
		if got := AIModelMaxTemperature(protocol); got != want {
			t.Fatalf("%s 温度上限=%g，期望 %g", protocol, got, want)
		}
	}
	// 配置期就要拒：anthropic 填 1.5 属于调用期必被上游 400 的错，此处提前拦下。
	if _, err := ValidateAIExtraConfig(json.RawMessage(`{"temperature":1.5}`), AIProtocolAnthropic); err == nil {
		t.Fatal("anthropic 协议 temperature=1.5 必须被拒")
	}
	if _, err := ValidateAIExtraConfig(json.RawMessage(`{"temperature":1.5}`), AIProtocolOpenAI); err != nil {
		t.Fatalf("openai 协议 temperature=1.5 合法，不应报错: %v", err)
	}
}

func TestAIModelCallRecordCarriesNoKey(t *testing.T) {
	// 调用记录是长期保留的统计素材，结构里连"看起来像 Key"的字段都不许有。
	for _, forbidden := range []string{"APIKey", "Ciphertext", "Nonce", "Key", "BaseURL", "PlainKey"} {
		if _, ok := reflect.TypeOf(AIModelCallRecord{}).FieldByName(forbidden); ok {
			t.Fatalf("AIModelCallRecord 不得含字段 %s", forbidden)
		}
	}
	// token 必须是 *int64：nil（未上报）与 0（真的用了 0）在类型层面就不同。
	for _, name := range []string{"PromptTokens", "CompletionTokens"} {
		field, ok := reflect.TypeOf(AIModelCallRecord{}).FieldByName(name)
		if !ok {
			t.Fatalf("AIModelCallRecord 缺少 %s", name)
		}
		if field.Type.Kind() != reflect.Ptr || field.Type.Elem().Kind() != reflect.Int64 {
			t.Fatalf("%s 必须是 *int64，实际 %s", name, field.Type)
		}
	}
}

func TestAIQuotaDefaultsMatchContract(t *testing.T) {
	spec := loadSpec(t)
	defaults := AIQuotaDefaults()
	// 默认关闭是硬要求：P1 上线不得改变任何存量的调用行为。
	if defaults.Enabled {
		t.Fatal("阈值默认必须关闭")
	}
	if !reflect.DeepEqual(spec.XRDCAIQuota.Actions, AIQuotaActions) {
		t.Fatalf("x-rdc-ai-quota.actions=%v 与 Go=%v 不一致", spec.XRDCAIQuota.Actions, AIQuotaActions)
	}
	if !reflect.DeepEqual(spec.XRDCAIUsage.GroupBy, AIUsageGroupBy) {
		t.Fatalf("x-rdc-ai-usage.groupBy=%v 与 Go=%v 不一致", spec.XRDCAIUsage.GroupBy, AIUsageGroupBy)
	}
	yamlDefaults := spec.XRDCAIQuota.Defaults
	if yamlDefaults.Enabled != defaults.Enabled || yamlDefaults.MaxCalls != defaults.MaxCalls ||
		yamlDefaults.WindowMinutes != defaults.WindowMinutes || yamlDefaults.Action != defaults.Action {
		t.Fatalf("阈值默认值不一致: 契约=%+v Go=%+v", yamlDefaults, defaults)
	}
	if spec.XRDCAIQuota.RetentionDaysDefault != AIRetentionDaysDefault ||
		spec.XRDCAIQuota.RetentionDaysRange.Min != AIRetentionDaysMin ||
		spec.XRDCAIQuota.RetentionDaysRange.Max != AIRetentionDaysMax {
		t.Fatalf("保留期契约不一致: %+v", spec.XRDCAIQuota)
	}
	if spec.XRDCAIUsage.DefaultRangeDays != AIUsageDefaultRangeDays {
		t.Fatalf("默认统计区间=%d，期望 %d", spec.XRDCAIUsage.DefaultRangeDays, AIUsageDefaultRangeDays)
	}
}

func TestProtocolTemperatureContract(t *testing.T) {
	spec := loadSpec(t)
	// x-rdc-ai-protocol-temperature 必须覆盖全部协议，否则前端提示会漏一个。
	if len(spec.XRDCProtocolTemp) != len(AIProtocols) {
		t.Fatalf("x-rdc-ai-protocol-temperature 覆盖 %d 个协议，期望 %d", len(spec.XRDCProtocolTemp), len(AIProtocols))
	}
	for _, protocol := range AIProtocols {
		entry, defined := spec.XRDCProtocolTemp[protocol]
		if !defined || entry.Maximum == nil || entry.Minimum == nil {
			t.Fatalf("协议 %s 缺少温度区间定义", protocol)
		}
		if *entry.Maximum != AIModelMaxTemperature(protocol) {
			t.Fatalf("协议 %s 契约上限=%g Go=%g", protocol, *entry.Maximum, AIModelMaxTemperature(protocol))
		}
		if *entry.Minimum != 0 {
			t.Fatalf("协议 %s 温度下限应为 0，实际 %g", protocol, *entry.Minimum)
		}
	}
}

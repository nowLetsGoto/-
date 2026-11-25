// 契约一致性测试：把 api/openapi.yaml 与 Go 常量逐项比对，任一漂移即失败。
// 职责：锁死环境/平台/元数据来源枚举、API 路径、页面路由、常量上限、平台元数据、请求体字段。
// 输入：仓库根 api/openapi.yaml（唯一 SSOT），以及本包与 config 包的 Go 常量。
// 输出：全部一致时通过；不一致时 t.Fatal 并指出具体差异。
// 边界：yaml.v3 只在本测试使用，不进运行时依赖；演示方法是把 yaml 里 pre 改成 pre2，本测试必须失败。
package contract

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"dandelion/server/internal/config"
)

type openAPISpec struct {
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths      map[string]any `yaml:"paths"`
	XConstants struct {
		MaxUploadMB          int64 `yaml:"maxUploadMB"`
		StageTokenTTLSeconds int   `yaml:"stageTokenTTLSeconds"`
		ParseTimeoutSeconds  int   `yaml:"parseTimeoutSeconds"`
		ParseReadLimitMB     int   `yaml:"parseReadLimitMB"`
		AuthTokenTTLSeconds  int   `yaml:"authTokenTTLSeconds"`
		RDCGitTimeoutSeconds int   `yaml:"rdcGitTimeoutSeconds"`
		RDCGitMaxCommits     int   `yaml:"rdcGitMaxCommits"`
		RDCMaxImageMB        int   `yaml:"rdcMaxImageMB"`
		RDCMaxImages         int   `yaml:"rdcMaxImages"`
		Limits               struct {
			AppName        int `yaml:"appName"`
			PackageID      int `yaml:"packageId"`
			Version        int `yaml:"version"`
			BuildNumber    int `yaml:"buildNumber"`
			Changelog      int `yaml:"changelog"`
			Username       int `yaml:"username"`
			Password       int `yaml:"password"`
			WebhookURL     int `yaml:"webhookUrl"`
			WebhookSecret  int `yaml:"webhookSecret"`
			ReqTitle       int `yaml:"reqTitle"`
			ReqDescription int `yaml:"reqDescription"`
			ReqAcceptance  int `yaml:"reqAcceptance"`
		} `yaml:"limits"`
	} `yaml:"x-constants"`
	XWebhookFields     map[string]string `yaml:"x-webhook-fields"`
	XRDCImageExts      map[string]string `yaml:"x-rdc-image-exts"`
	XRDCDevStatuses    []string          `yaml:"x-rdc-dev-statuses"`
	XRDCDevTransitions map[string]string `yaml:"x-rdc-dev-transitions"`
	XRDCReviewActions  map[string]struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
	} `yaml:"x-rdc-review-actions"`
	XRDCInteractionReviewDecisions  []string          `yaml:"x-rdc-interaction-review-decisions"`
	XRDCInteractionFileStatus       []string          `yaml:"x-rdc-interaction-file-status"`
	XRDCInteractionSource           []string          `yaml:"x-rdc-interaction-source"`
	XRDCInteractionSubmitStatus     []string          `yaml:"x-rdc-interaction-submit-status"`
	XRDCInteractionQuestionType     []string          `yaml:"x-rdc-interaction-question-type"`
	XRDCInteractionGateStatus       []string          `yaml:"x-rdc-interaction-gate-status"`
	XRDCInteractionCheckpointStatus []string          `yaml:"x-rdc-interaction-checkpoint-status"`
	XRDCInteractionRiskLevel        []string          `yaml:"x-rdc-interaction-risk-level"`
	XRDCInteractionIntervention     []string          `yaml:"x-rdc-interaction-intervention"`
	XRDCInteractionOperationType    []string          `yaml:"x-rdc-interaction-operation-type"`
	XRDCInteractionEvidenceActions  []string          `yaml:"x-rdc-interaction-evidence-actions"`
	XRDCInteractionRiskMatrix       map[string]string `yaml:"x-rdc-interaction-risk-matrix"`
	XRDCInteractionInterventions    map[string]string `yaml:"x-rdc-interaction-interventions"`
	XRDCAIProviders                 []string          `yaml:"x-rdc-ai-providers"`
	XRDCAIProtocols                 []string          `yaml:"x-rdc-ai-protocols"`
	XRDCAICapabilities              []string          `yaml:"x-rdc-ai-capabilities"`
	XRDCAIReasoningEfforts          []string          `yaml:"x-rdc-ai-reasoning-efforts"`
	XRDCAIContextRange              struct {
		Min     int `yaml:"min"`
		Max     int `yaml:"max"`
		Default int `yaml:"default"`
	} `yaml:"x-rdc-ai-context-range"`
	XRDCAIExtraConfig map[string]struct {
		Type         string   `yaml:"type"`
		Minimum      *float64 `yaml:"minimum"`
		ExclusiveMin bool     `yaml:"exclusiveMin"`
		Maximum      *float64 `yaml:"maximum"`
		Default      any      `yaml:"default"`
		Enum         []string `yaml:"enum"`
	} `yaml:"x-rdc-ai-extra-config"`
	XRDCAITestCategories []string `yaml:"x-rdc-ai-test-categories"`
	XRDCProtocolTemp     map[string]struct {
		Minimum *float64 `yaml:"minimum"`
		Maximum *float64 `yaml:"maximum"`
	} `yaml:"x-rdc-ai-protocol-temperature"`
	XRDCAIQuota struct {
		Defaults struct {
			Enabled       bool   `yaml:"enabled"`
			MaxCalls      int    `yaml:"maxCalls"`
			WindowMinutes int    `yaml:"windowMinutes"`
			Action        string `yaml:"action"`
		} `yaml:"defaults"`
		Actions       []string `yaml:"actions"`
		MaxCallsRange struct {
			Min int `yaml:"min"`
			Max int `yaml:"max"`
		} `yaml:"maxCallsRange"`
		WindowMinutesRange struct {
			Min int `yaml:"min"`
			Max int `yaml:"max"`
		} `yaml:"windowMinutesRange"`
		RetentionDaysDefault int `yaml:"retentionDaysDefault"`
		RetentionDaysRange   struct {
			Min int `yaml:"min"`
			Max int `yaml:"max"`
		} `yaml:"retentionDaysRange"`
	} `yaml:"x-rdc-ai-quota"`
	XRDCAIUsage struct {
		GroupBy          []string `yaml:"groupBy"`
		DefaultRangeDays int      `yaml:"defaultRangeDays"`
	} `yaml:"x-rdc-ai-usage"`
	XRDCAITemplate struct {
		Name            string   `yaml:"name"`
		Provider        string   `yaml:"provider"`
		Protocol        string   `yaml:"protocol"`
		BaseURL         string   `yaml:"baseUrl"`
		ModelID         string   `yaml:"modelId"`
		ContextTokens   int      `yaml:"contextTokens"`
		ThinkingEnabled bool     `yaml:"thinkingEnabled"`
		Capabilities    []string `yaml:"capabilities"`
	} `yaml:"x-rdc-ai-template"`
	XEnvironments map[string]string `yaml:"x-environments"`
	XPlatforms    map[string]struct {
		Label       string `yaml:"label"`
		Ext         string `yaml:"ext"`
		ContentType string `yaml:"contentType"`
	} `yaml:"x-platforms"`
	XPageRoutes struct {
		DownloadPage string `yaml:"downloadPage"`
	} `yaml:"x-page-routes"`
	Components struct {
		Schemas map[string]struct {
			Enum       []string       `yaml:"enum"`
			Properties map[string]any `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadSpec(t *testing.T) openAPISpec {
	t.Helper()
	// 本测试文件在 server/internal/contract/ 下，向上三级到仓库根。
	path := filepath.Join("..", "..", "..", "api", "openapi.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取契约文件失败 %s: %v", path, err)
	}
	var spec openAPISpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("解析契约文件失败: %v", err)
	}
	return spec
}

func TestEnvironmentEnumMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	want := spec.Components.Schemas["Environment"].Enum
	if !reflect.DeepEqual(want, Environments) {
		t.Fatalf("Environment.enum=%v 与 Go Environments=%v 不一致", want, Environments)
	}
	var labels []string
	for key := range spec.XEnvironments {
		labels = append(labels, key)
	}
	sort.Strings(labels)
	sorted := append([]string(nil), Environments...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(labels, sorted) {
		t.Fatalf("x-environments 键=%v 与枚举=%v 不一致", labels, sorted)
	}
}

func TestPlatformEnumMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	want := spec.Components.Schemas["Platform"].Enum
	if !reflect.DeepEqual(want, Platforms) {
		t.Fatalf("Platform.enum=%v 与 Go Platforms=%v 不一致", want, Platforms)
	}
	for platform, info := range spec.XPlatforms {
		got, ok := PlatformInfos[platform]
		if !ok {
			t.Fatalf("x-platforms 出现未知平台 %s", platform)
		}
		if got.Ext != info.Ext || got.ContentType != info.ContentType || got.Label != info.Label {
			t.Fatalf("平台 %s 元数据不一致: Go=%+v 契约=%+v", platform, got, info)
		}
	}
	if len(spec.XPlatforms) != len(PlatformInfos) {
		t.Fatalf("x-platforms 数量与 Go PlatformInfos 不一致")
	}
}

// TestMetaSourceEnumMatchesContract 锁死 metaSource 只有两值，且与 Go 常量集合完全一致。
// 第一版由服务端派生（parsed / fallback）：yaml 多一个或少一个值都会 FAIL。
func TestMetaSourceEnumMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	enum := spec.Components.Schemas["MetaSource"].Enum
	if len(enum) != 2 {
		t.Fatalf("第一版 MetaSource 只允许 parsed / fallback 两值，实际 %v", enum)
	}
	sortedEnum := append([]string(nil), enum...)
	sort.Strings(sortedEnum)
	sortedGo := append([]string(nil), MetaSources...)
	sort.Strings(sortedGo)
	if !reflect.DeepEqual(sortedEnum, sortedGo) {
		t.Fatalf("MetaSource.enum=%v 与 Go MetaSources=%v 不一致", enum, MetaSources)
	}
}

// TestAccountLevelEnumMatchesContract 锁死四级身份的取值与顺序：
// 层级大小直接决定接口门槛，yaml 与 Go 一旦漂移就会出现"前端显示平台管理员、后端却当普通成员"的越权/误拒。
func TestAccountLevelEnumMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	enum := spec.Components.Schemas["AccountLevel"].Enum
	if !reflect.DeepEqual(enum, AccountLevels) {
		t.Fatalf("AccountLevel.enum=%v 与 Go AccountLevels=%v 不一致", enum, AccountLevels)
	}
	if AccountLevelRank(AccountLevelSuperAdmin) <= AccountLevelRank(AccountLevelPlatformAdmin) ||
		AccountLevelRank(AccountLevelPlatformAdmin) <= AccountLevelRank(AccountLevelTeamAdmin) ||
		AccountLevelRank(AccountLevelTeamAdmin) <= AccountLevelRank(AccountLevelMember) {
		t.Fatal("层级顺序必须是 super_admin > platform_admin > team_admin > member")
	}
	if AccountLevelRank("unknown") != 0 || AccountLevelAtLeast(AccountLevelMember, "unknown") {
		t.Fatal("未知层级必须排最低，未知门槛必须失败关闭")
	}
}

func TestAPIRoutesMatchContract(t *testing.T) {
	spec := loadSpec(t)
	if len(spec.Servers) == 0 {
		t.Fatal("契约缺少 servers.url")
	}
	prefix := spec.Servers[0].URL
	if prefix != APIPrefix {
		t.Fatalf("servers.url=%s 与 Go APIPrefix=%s 不一致", prefix, APIPrefix)
	}

	got := make(map[string]bool)
	for key := range spec.Paths {
		if key == RouteHealth {
			got[key] = true
			continue
		}
		got[prefix+key] = true
	}
	if len(got) != len(APIRoutes) {
		t.Fatalf("契约路径数=%d 与 Go APIRoutes 数=%d 不一致: %v / %v", len(got), len(APIRoutes), got, APIRoutes)
	}
	for _, route := range APIRoutes {
		if !got[route] {
			t.Fatalf("契约缺少 Go 路由 %s", route)
		}
	}
	if spec.XPageRoutes.DownloadPage != RouteDownloadPage {
		t.Fatalf("下载页路由不一致: 契约=%s Go=%s", spec.XPageRoutes.DownloadPage, RouteDownloadPage)
	}
}

func TestDeliveryEnumsMatchContract(t *testing.T) {
	spec := loadSpec(t)
	for _, tc := range []struct {
		name     string
		expected []string
	}{
		{"TestResult", TestResults}, {"AcceptanceStatus", AcceptanceStatuses}, {"GitStatus", GitStatuses},
	} {
		if got := spec.Components.Schemas[tc.name].Enum; !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("%s enum=%v Go=%v", tc.name, got, tc.expected)
		}
	}
	if spec.XConstants.RDCGitTimeoutSeconds != RDCDefaultGitTimeoutSeconds || spec.XConstants.RDCGitMaxCommits != RDCGitMaxCommits {
		t.Fatal("Git 配置常量与契约不一致")
	}
}

func TestLimitsMatchContract(t *testing.T) {
	spec := loadSpec(t)
	if spec.XConstants.MaxUploadMB != config.DefaultMaxUploadMB {
		t.Fatalf("maxUploadMB 契约=%d 与 config 默认值=%d 不一致", spec.XConstants.MaxUploadMB, config.DefaultMaxUploadMB)
	}
	if spec.XConstants.StageTokenTTLSeconds != StageTokenTTLSeconds ||
		spec.XConstants.ParseTimeoutSeconds != ParseTimeoutSeconds ||
		spec.XConstants.ParseReadLimitMB != ParseReadLimitMB {
		t.Fatal("stage/parser 常量与契约 x-constants 不一致")
	}
	if spec.XConstants.AuthTokenTTLSeconds != AuthTokenTTLSeconds {
		t.Fatalf("authTokenTTLSeconds 契约=%d 与 Go=%d 不一致", spec.XConstants.AuthTokenTTLSeconds, AuthTokenTTLSeconds)
	}
	limits := spec.XConstants.Limits
	if limits.AppName != MaxAppNameLen || limits.PackageID != MaxPackageIDLen ||
		limits.Version != MaxVersionLen || limits.BuildNumber != MaxBuildNumberLen ||
		limits.Changelog != MaxChangelogLen {
		t.Fatal("字段长度上限与契约 x-constants.limits 不一致")
	}
	if limits.Username != MaxUsernameLen || limits.Password != MaxPasswordLen {
		t.Fatalf("登录字段上限与契约不一致: 契约=%d/%d Go=%d/%d",
			limits.Username, limits.Password, MaxUsernameLen, MaxPasswordLen)
	}
	if limits.WebhookURL != MaxWebhookURLLen || limits.WebhookSecret != MaxWebhookSecretLen {
		t.Fatalf("Webhook 上限与契约不一致: 契约=%d/%d Go=%d/%d",
			limits.WebhookURL, limits.WebhookSecret, MaxWebhookURLLen, MaxWebhookSecretLen)
	}
	if limits.ReqTitle != MaxReqTitleLen || limits.ReqDescription != MaxReqDescLen ||
		limits.ReqAcceptance != MaxReqAcceptanceLen {
		t.Fatalf("需求字段上限与契约不一致: 契约=%d/%d/%d Go=%d/%d/%d",
			limits.ReqTitle, limits.ReqDescription, limits.ReqAcceptance,
			MaxReqTitleLen, MaxReqDescLen, MaxReqAcceptanceLen)
	}
}

// TestWebhookFieldsMatchContract 锁死可推送字段：yaml 键集合 = Go 标签表，枚举顺序 = WebhookFields。
func TestWebhookFieldsMatchContract(t *testing.T) {
	spec := loadSpec(t)

	if len(spec.XWebhookFields) != len(WebhookFieldLabels) {
		t.Fatalf("x-webhook-fields 共 %d 项，与 Go 标签表 %d 项不一致", len(spec.XWebhookFields), len(WebhookFieldLabels))
	}
	for key, label := range spec.XWebhookFields {
		got, ok := WebhookFieldLabels[key]
		if !ok {
			t.Fatalf("契约出现 Go 未定义的字段 %s", key)
		}
		if got != label {
			t.Fatalf("字段 %s 标签不一致: Go=%s 契约=%s", key, got, label)
		}
	}

	if enum := spec.Components.Schemas["WebhookField"].Enum; !reflect.DeepEqual(enum, WebhookFields) {
		t.Fatalf("WebhookField.enum=%v 与 Go WebhookFields=%v 不一致", enum, WebhookFields)
	}

	// 默认勾选必须是合法字段，否则新装实例的设置页会渲染出未知项。
	for _, field := range DefaultWebhookFields {
		if !IsValidWebhookField(field) {
			t.Fatalf("默认勾选字段 %s 不在允许集合内", field)
		}
	}
}

// TestCommitRequestHasExactlyNineFields 断言提交契约恰好 9 个字段（项目 + token + 7 个表单值）：
// 多出 file / metaSource / touchedFields 中任何一个都应让本测试失败。
func TestCommitRequestHasExactlyNineFields(t *testing.T) {
	spec := loadSpec(t)
	commit := spec.Components.Schemas["CommitBuildRequest"].Properties
	want := []string{
		"project", "stageToken", "environment", "platform",
		"appName", "packageId", "version", "buildNumber", "changelog",
	}
	if len(commit) != len(want) {
		t.Fatalf("CommitBuildRequest 必须恰好 %d 个字段，实际 %d 个: %v", len(want), len(commit), sortedKeys(commit))
	}
	for _, field := range want {
		if _, exists := commit[field]; !exists {
			t.Fatalf("CommitBuildRequest 缺少字段 %s，实际字段: %v", field, sortedKeys(commit))
		}
	}
}

// TestBuildHasThreeURLsAndCount 断言 Build 带 3 个地址字段与下载次数，供前端零拼接消费。
func TestBuildHasThreeURLsAndCount(t *testing.T) {
	spec := loadSpec(t)
	build := spec.Components.Schemas["Build"].Properties
	for _, field := range []string{"downloadPagePath", "downloadPageUrl", "downloadFileUrl", "downloadCount"} {
		if _, exists := build[field]; !exists {
			t.Fatalf("Build 缺少字段 %s", field)
		}
	}
}

// TestRequirementStatusEnumMatchesContract 把需求状态枚举与契约逐项锁死。
// 顺序也有意义：前端筛选项按同一顺序镜像，漂移会让用户看到的顺序与契约不一致。
func TestRequirementStatusEnumMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	enum := spec.Components.Schemas["RequirementStatus"].Enum
	if !reflect.DeepEqual(enum, RequirementStatuses) {
		t.Fatalf("RequirementStatus.enum=%v 与 Go RequirementStatuses=%v 不一致", enum, RequirementStatuses)
	}
	for _, status := range RequirementStatuses {
		if !IsValidRequirementStatus(status) {
			t.Fatalf("枚举状态 %q 应通过 IsValidRequirementStatus", status)
		}
	}
	for _, invalid := range []string{"", "PENDING_REVIEW", "reviewing", "done"} {
		if IsValidRequirementStatus(invalid) {
			t.Fatalf("非法状态 %q 不应通过校验", invalid)
		}
	}
}

// TestFormatReqKeyFollowsWidthConstant 钉住编号格式与宽度常量的一致性。
// 把 ReqSeqWidth 从 3 改成 6 时，本测试与契约正则一起改，格式不会散落到各层。
func TestFormatReqKeyFollowsWidthConstant(t *testing.T) {
	key := FormatReqKey("20250614", 1)
	if key != "REQ-20250614-001" {
		t.Fatalf("FormatReqKey 应为 REQ-20250614-001，实际 %q", key)
	}
	if !IsValidReqKey(key) {
		t.Fatalf("FormatReqKey 产出的 %q 应通过 IsValidReqKey", key)
	}
	for _, invalid := range []string{"", "REQ-20250614-1", "REQ-20250614-0001", "REQ-2025061-001", "REQ-20250614-00a"} {
		if IsValidReqKey(invalid) {
			t.Fatalf("非法需求编号 %q 不应通过校验", invalid)
		}
	}
}

// TestRDCImageConstraintsMatchContract 把图片白名单/上限与契约逐项锁死：
// 任一处漂移（后端改了上限、契约漏改、扩展名归一规则变了）都会让本测试失败。
func TestRDCImageConstraintsMatchContract(t *testing.T) {
	spec := loadSpec(t)
	if int64(spec.XConstants.RDCMaxImageMB)<<20 != MaxRDCImageBytes {
		t.Fatalf("单张图片上限不一致: 契约=%dMB Go=%d", spec.XConstants.RDCMaxImageMB, MaxRDCImageBytes)
	}
	if spec.XConstants.RDCMaxImages != MaxRDCImages {
		t.Fatalf("图片数量上限不一致: 契约=%d Go=%d", spec.XConstants.RDCMaxImages, MaxRDCImages)
	}
	if len(spec.XRDCImageExts) != len(RDCImageExts) {
		t.Fatalf("x-rdc-image-exts 共 %d 项，与 Go RDCImageExts %d 项不一致",
			len(spec.XRDCImageExts), len(RDCImageExts))
	}
	for ext, mime := range spec.XRDCImageExts {
		if RDCImageExts[ext] != mime {
			t.Fatalf("扩展名 %s 的 MIME 不一致: 契约=%s Go=%s", ext, mime, RDCImageExts[ext])
		}
	}
	if ext, ok := RDCImageExtOf("image/jpeg"); !ok || ext != "jpg" {
		t.Fatalf("image/jpeg 应归一为 jpg，实际 %q ok=%v", ext, ok)
	}
	for _, contentType := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		if !IsRDCImageContentType(contentType) {
			t.Fatalf("白名单 MIME %q 应通过校验", contentType)
		}
	}
	for _, contentType := range []string{"", "application/pdf", "text/plain", "image/svg+xml"} {
		if IsRDCImageContentType(contentType) {
			t.Fatalf("非图片 MIME %q 不应通过校验", contentType)
		}
	}
}

// TestDevStateMachineMatchesContract 把研发子任务的状态枚举与合法前进边逐项锁死：
// 顺序、值、边集合任一漂移都会让这里失败，杜绝"第二份状态机"悄然出现。
func TestDevStateMachineMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	if !reflect.DeepEqual(spec.XRDCDevStatuses, DevSubtaskStatuses) {
		t.Fatalf("x-rdc-dev-statuses=%v 与 Go DevSubtaskStatuses=%v 不一致",
			spec.XRDCDevStatuses, DevSubtaskStatuses)
	}
	if len(spec.XRDCDevStatuses) != 10 {
		t.Fatalf("研发子任务必须恰好 10 个状态（8 进度 + rejected + skipped），实际 %d", len(spec.XRDCDevStatuses))
	}
	if !reflect.DeepEqual(spec.XRDCDevTransitions, DevAdvance) {
		t.Fatalf("x-rdc-dev-transitions=%v 与 Go DevAdvance=%v 不一致",
			spec.XRDCDevTransitions, DevAdvance)
	}
	if len(DevAdvance) != 7 {
		t.Fatalf("合法前进边必须恰好 7 条，实际 %d", len(DevAdvance))
	}

	// 审核动作边：x-rdc-review-actions 与 Go devReviewActions 逐项一致，且 rejected 不在前进链。
	if len(spec.XRDCReviewActions) != 4 {
		t.Fatalf("x-rdc-review-actions 必须恰好 4 条（approve/reject/commit/resubmit），实际 %d", len(spec.XRDCReviewActions))
	}
	for action, edge := range spec.XRDCReviewActions {
		to, ok := ReviewActionTarget(action, edge.From)
		if !ok || to != edge.To {
			t.Fatalf("审核动作 %s: ReviewActionTarget(%q) 应为 %q ok=true，实际 %q ok=%v",
				action, edge.From, edge.To, to, ok)
		}
		if !IsValidReviewAction(action) {
			t.Fatalf("审核动作 %s 应在合法目录里", action)
		}
		// 源态不同、动作名伪造都必须被拒。
		if _, bad := ReviewActionTarget(action, "unknown"); bad {
			t.Fatalf("审核动作 %s 不允许从 unknown 触发", action)
		}
	}
	if _, ok := DevAdvance[DevStatusRejected]; ok {
		t.Fatal("rejected 是阻断态，不得出现在前进链 DevAdvance 里")
	}

	// 每条合法边都能推进一态；终态与未知态无任何出边。
	for from, to := range DevAdvance {
		got, ok := DevNextStatus(from)
		if !ok || got != to {
			t.Fatalf("DevNextStatus(%q) 应为 %q ok=true，实际 %q ok=%v", from, to, got, ok)
		}
	}
	for _, from := range []string{DevStatusCompleted, DevStatusSkipped, "", "unknown"} {
		if to, ok := DevNextStatus(from); ok {
			t.Fatalf("终态/非法态 %q 不可推进，实际得到 %q", from, to)
		}
	}

	// 序 0..6 可标跳过；completed / skipped 不可；序号口径与 SQL CASE 一致。
	for ordinal, status := range DevSubtaskStatuses[:8] {
		if DevStatusOrdinal(status) != ordinal {
			t.Fatalf("状态 %q 的序号应为 %d，实际 %d", status, ordinal, DevStatusOrdinal(status))
		}
		if ordinal <= 6 && !CanSkipDevStatus(status) {
			t.Fatalf("未完成态 %q 应允许标记跳过", status)
		}
		if ordinal == 7 && CanSkipDevStatus(status) {
			t.Fatalf("已完成态 %q 不允许再标跳过", status)
		}
	}
	if DevStatusOrdinal(DevStatusSkipped) != -1 || DevStatusOrdinal("unknown") != -1 {
		t.Fatal("skipped 与未知状态的序号必须为 -1（SQL MIN 口径）")
	}
	if DevStatusOrdinal(DevStatusRejected) != -1 {
		t.Fatal("rejected 是阻断态，不占进度序号（ordinal=-1），不得影响 SQL MIN 口径")
	}
	if CanSkipDevStatus(DevStatusRejected) {
		t.Fatal("rejected 阻断态不允许直接标跳过，须先经研发中心重新研发")
	}
	if !IsDevTerminal(DevStatusCompleted) || !IsDevTerminal(DevStatusSkipped) || IsDevTerminal(DevStatusPending) {
		t.Fatal("仅 completed / skipped 是终态")
	}
	if IsDevTerminal(DevStatusRejected) {
		t.Fatal("rejected 不是终态：可经 resubmit 回到 ai_completed")
	}
}

// TestAggregateDevStatusGolden 钉死整体状态：
// skipped 排除、两条都排除视为 completed、其余取序号最小的瓶颈态。
func TestAggregateDevStatusGolden(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"两条待开发", []string{DevStatusPending, DevStatusPending}, DevStatusPending},
		{"取瓶颈", []string{DevStatusCommitted, DevStatusReviewing}, DevStatusReviewing},
		{"前端跳过", []string{DevStatusSkipped, DevStatusValidated}, DevStatusValidated},
		{"后端跳过", []string{DevStatusAIRunning, DevStatusSkipped}, DevStatusAIRunning},
		{"两条都跳过", []string{DevStatusSkipped, DevStatusSkipped}, DevStatusCompleted},
		{"完成加跳过", []string{DevStatusCompleted, DevStatusSkipped}, DevStatusCompleted},
		{"一条完成一条在途", []string{DevStatusCompleted, DevStatusCommitted}, DevStatusCommitted},
		{"未知态不参与", []string{"unknown", DevStatusWaitingCommit}, DevStatusWaitingCommit},
		{"驳回阻断优先", []string{DevStatusRejected, DevStatusCompleted}, DevStatusRejected},
		{"两条都驳回", []string{DevStatusRejected, DevStatusRejected}, DevStatusRejected},
		{"驳回覆盖在途", []string{DevStatusPending, DevStatusRejected}, DevStatusRejected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := AggregateDevStatus(test.statuses); got != test.want {
				t.Fatalf("AggregateDevStatus(%v)=%q，期望 %q", test.statuses, got, test.want)
			}
		})
	}
}

// TestDevUpdateDTOHasNoStatus 是编辑安全底线的结构性断言：
// DTO 顶层与嵌套都不能出现 status 字段，否则任何一层绕过都可能写状态。
func TestDevUpdateDTOHasNoStatus(t *testing.T) {
	spec := loadSpec(t)
	update := spec.Components.Schemas["DevTaskUpdate"]
	if _, exists := update.Properties["status"]; exists {
		t.Fatal("openapi 的 DevTaskUpdate 不得含 status 字段")
	}
	nested := spec.Components.Schemas["DevSubtaskNoteUpdate"]
	if _, exists := nested.Properties["status"]; exists {
		t.Fatal("openapi 的 DevSubtaskNoteUpdate 不得含 status 字段")
	}
	typed := reflect.TypeOf(DevTaskUpdate{})
	for i := 0; i < typed.NumField(); i++ {
		if typed.Field(i).Tag.Get("json") == "status" {
			t.Fatal("Go 的 DevTaskUpdate 不得含 status 字段")
		}
	}
	nestedType := reflect.TypeOf(DevSubtaskNoteUpdate{})
	for i := 0; i < nestedType.NumField(); i++ {
		if nestedType.Field(i).Tag.Get("json") == "status" {
			t.Fatal("Go 的 DevSubtaskNoteUpdate 不得含 status 字段")
		}
	}
}

// TestRDCInteractionEnumsMatchContract 逐项锁死 12 组交互枚举，顺序也有意义：
// 前端标签表按同一顺序镜像，任一处漂移都会让界面与后端判定脱节。
func TestRDCInteractionEnumsMatchContract(t *testing.T) {
	spec := loadSpec(t)
	for _, tc := range []struct {
		name string
		yaml []string
		want []string
	}{
		{"review-decisions", spec.XRDCInteractionReviewDecisions, ReviewDecisions},
		{"file-status", spec.XRDCInteractionFileStatus, InteractionFileStatuses},
		{"source", spec.XRDCInteractionSource, InteractionSources},
		{"submit-status", spec.XRDCInteractionSubmitStatus, SubmitStatuses},
		{"question-type", spec.XRDCInteractionQuestionType, QuestionTypes},
		{"gate-status", spec.XRDCInteractionGateStatus, GateStatuses},
		{"checkpoint-status", spec.XRDCInteractionCheckpointStatus, CheckpointStatuses},
		{"risk-level", spec.XRDCInteractionRiskLevel, RiskLevels},
		{"intervention", spec.XRDCInteractionIntervention, Interventions},
		{"operation-type", spec.XRDCInteractionOperationType, OperationTypes},
		{"evidence-actions", spec.XRDCInteractionEvidenceActions, InteractionEvidenceActions},
	} {
		if !reflect.DeepEqual(tc.yaml, tc.want) {
			t.Fatalf("x-rdc-interaction-%s=%v 与 Go=%v 不一致", tc.name, tc.yaml, tc.want)
		}
	}
	// 证据 action 绝不混入状态动作目录，否则会打开第二条状态写入路径。
	for _, action := range InteractionEvidenceActions {
		if IsValidReviewAction(action) {
			t.Fatalf("证据动作 %s 不得出现在状态动作目录 devReviewActions 中", action)
		}
		if !IsInteractionEvidenceAction(action) {
			t.Fatalf("证据动作 %s 应被 IsInteractionEvidenceAction 识别", action)
		}
	}
	if IsInteractionEvidenceAction(ReviewActionApprove) {
		t.Fatal("approve 是状态动作，不是交互证据动作")
	}
}

// TestRDCInteractionRiskMatrixMatchesContract 把风险矩阵、介入映射与兜底规则逐项钉死。
func TestRDCInteractionRiskMatrixMatchesContract(t *testing.T) {
	spec := loadSpec(t)
	if len(spec.XRDCInteractionRiskMatrix) != len(OperationRisk) {
		t.Fatalf("风险矩阵项数不一致: 契约=%d Go=%d", len(spec.XRDCInteractionRiskMatrix), len(OperationRisk))
	}
	for operation, risk := range spec.XRDCInteractionRiskMatrix {
		if got, ok := OperationRisk[operation]; !ok || got != risk {
			t.Fatalf("操作 %s 风险不一致: 契约=%s Go=%s", operation, risk, got)
		}
	}
	if len(spec.XRDCInteractionInterventions) != len(RiskIntervention) {
		t.Fatalf("介入映射项数不一致: 契约=%d Go=%d", len(spec.XRDCInteractionInterventions), len(RiskIntervention))
	}
	for risk, intervention := range spec.XRDCInteractionInterventions {
		if got, ok := RiskIntervention[risk]; !ok || got != intervention {
			t.Fatalf("风险 %s 介入强度不一致: 契约=%s Go=%s", risk, intervention, got)
		}
	}
	for _, unknown := range []string{"", "other", "unknown_operation"} {
		if got := RiskOfOperation(unknown); got != DefaultRisk {
			t.Fatalf("未知操作 %q 应兜底 %s，实际 %s", unknown, DefaultRisk, got)
		}
	}
	if InterventionOf("unknown") != InterventionSample {
		t.Fatal("未知风险的介入强度应兜底抽查 sample")
	}

	// 整体风险取最大值（木桶效应）：只要含一个 high，整体即 high。
	for _, tc := range []struct {
		operations []string
		want       string
	}{
		{nil, RiskMedium},
		{[]string{"format", "doc"}, RiskLow},
		{[]string{"format", "code_edit"}, RiskMedium},
		{[]string{"format", "delete_file", "code_edit"}, RiskHigh},
		{[]string{"unknown"}, RiskMedium},
	} {
		if got := OverallRiskLevel(tc.operations); got != tc.want {
			t.Fatalf("OverallRiskLevel(%v)=%s，期望 %s", tc.operations, got, tc.want)
		}
	}
}

// TestRDCInteractionDTOWhitelist 钉死交互请求 DTO 的可写字段：
// tenantId / source / 操作人 / 子任务状态都不能由请求体注入。
func TestRDCInteractionDTOWhitelist(t *testing.T) {
	spec := loadSpec(t)
	for _, tc := range []struct {
		schema string
		fields []string
	}{
		{"DiffReviewInput", []string{"decisions"}},
		{"HunkDecisionInput", []string{"decision"}},
		{"PlanConfirmInput", []string{"answers", "stepIds"}},
		{"RollbackInput", []string{"rollbackToken", "scope", "targets", "currentHashes"}},
		{"ApprovalDecideInput", []string{"checkpointId", "decision"}},
	} {
		properties := spec.Components.Schemas[tc.schema].Properties
		if len(properties) != len(tc.fields) {
			t.Fatalf("%s 字段数应为 %d，实际 %d: %v", tc.schema, len(tc.fields), len(properties), sortedKeys(properties))
		}
		for _, field := range tc.fields {
			if _, ok := properties[field]; !ok {
				t.Fatalf("%s 缺少字段 %s", tc.schema, field)
			}
		}
	}
	// Go DTO 同样不能出现状态字段，避免任何一层绕过路径参数写状态。
	for _, dto := range []any{DiffReviewInput{}, PlanConfirmInput{}, RollbackInput{}, ApprovalDecideInput{}} {
		typed := reflect.TypeOf(dto)
		for i := 0; i < typed.NumField(); i++ {
			if typed.Field(i).Tag.Get("json") == "status" {
				t.Fatalf("%s 不得含 status 字段", typed.Name())
			}
		}
	}
}

// sortedKeys 让断言失败信息稳定可读。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

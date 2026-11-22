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
	XWebhookFields map[string]string `yaml:"x-webhook-fields"`
	XRDCImageExts  map[string]string `yaml:"x-rdc-image-exts"`
	XEnvironments  map[string]string `yaml:"x-environments"`
	XPlatforms     map[string]struct {
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

// sortedKeys 让断言失败信息稳定可读。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

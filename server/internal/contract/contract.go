// Package contract 定义跨层 DTO 与契约常量的 Go 镜像。
// 职责：前后端共读 api/openapi.yaml；本文件把枚举、路由、长度上限镜像为 Go 常量供运行时使用。
// 输入：无外部输入（值由契约一致性测试 contract_test.go 逐项比对 yaml 锁死）。
// 输出：Build / StageResult / CommitInput 等 DTO、环境与平台常量、路由常量与工具函数。
// 边界：不含业务逻辑、不访问 DB / 文件系统、不生成 URL（三个 URL 由 service.buildURLs 唯一生成）。
package contract

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
)

// 跨层错误标记：service 不依赖 handler，用 sentinel 表达可预期的业务拒绝，
// 由 handler.WriteError 统一翻译成对应的 HTTP 状态码与中文文案。
var (
	ErrNotFound          = errors.New("包不存在或已被移除")
	ErrStageInvalid      = errors.New("暂存文件已失效，请重新选择文件")
	ErrPlatformMismatch  = errors.New("所选平台与文件类型不一致")
)

// 环境枚举：全仓 Go 侧唯一定义处，与 api/openapi.yaml 的 Environment.enum 顺序一致。
const (
	EnvTest   = "test"
	EnvHotfix = "hotfix"
	EnvPre    = "pre"
	EnvPrd    = "prd"
)

// 平台枚举：与 api/openapi.yaml 的 Platform.enum 顺序一致。
const (
	PlatformAndroid = "android"
	PlatformIOS     = "ios"
)

// 元数据来源枚举：由服务端派生（任一字段发生空值兜底 → fallback，否则 → parsed），
// 不出现在 CommitBuildRequest 中，前端无法伪造。
const (
	MetaParsed   = "parsed"
	MetaFallback = "fallback"
)

// 路由常量：servers.url + paths 键组成完整路径；契约测试比对同一份列表。
const (
	APIPrefix        = "/api/v1"
	RouteStage       = APIPrefix + "/builds/stage"
	RouteBuilds      = APIPrefix + "/builds"
	RouteBuildByID   = APIPrefix + "/builds/{id}"
	RouteBuildFile   = APIPrefix + "/builds/{id}/file"
	RouteHealth      = "/healthz"
	RouteDownloadPage = "/d/{id}"
)

// 契约级上限，与 x-constants 对齐。
const (
	MaxAppNameLen      = 100
	MaxPackageIDLen    = 100
	MaxVersionLen      = 50
	MaxBuildNumberLen  = 50
	MaxChangelogLen    = 2000
	StageTokenTTLSeconds = 3600
	ParseTimeoutSeconds  = 5
	ParseReadLimitMB     = 5
)

// Environments / Platforms / MetaSources / APIRoutes 供契约测试与页面渲染使用。
var Environments = []string{EnvTest, EnvHotfix, EnvPre, EnvPrd}
var Platforms = []string{PlatformAndroid, PlatformIOS}
var MetaSources = []string{MetaParsed, MetaFallback}
var APIRoutes = []string{RouteStage, RouteBuilds, RouteBuildByID, RouteBuildFile, RouteHealth}

// PlatformInfo 是平台的产品元数据，扩展名与下载 Content-Type 的唯一来源。
type PlatformInfo struct {
	Label       string
	Ext         string
	ContentType string
}

// PlatformInfos 与 api/openapi.yaml 的 x-platforms 逐项对应，由契约测试锁死。
var PlatformInfos = map[string]PlatformInfo{
	PlatformAndroid: {Label: "Android", Ext: "apk", ContentType: "application/vnd.android.package-archive"},
	PlatformIOS:     {Label: "iOS", Ext: "ipa", ContentType: "application/octet-stream"},
}

// Build 是完整包 DTO；三个 URL 字段由 service.buildURLs 填充，repository 映射时留空。
type Build struct {
	ID               string `json:"id"`
	Environment      string `json:"environment"`
	Platform         string `json:"platform"`
	AppName          string `json:"appName"`
	PackageID        string `json:"packageId"`
	Version          string `json:"version"`
	BuildNumber      string `json:"buildNumber"`
	Changelog        string `json:"changelog"`
	OriginalFileName string `json:"originalFileName"`
	FileSize         int64  `json:"fileSize"`
	FileExt          string `json:"fileExt"`
	RelativePath     string `json:"relativePath"`
	MetaSource       string `json:"metaSource"`
	DownloadCount    int64  `json:"downloadCount"`
	DownloadPagePath string `json:"downloadPagePath"`
	DownloadPageURL  string `json:"downloadPageUrl"`
	DownloadFileURL  string `json:"downloadFileUrl"`
	CreatedAt        string `json:"createdAt"`
}

// ParsedMeta 是自动解析的 4 个规范字段。
type ParsedMeta struct {
	AppName     string `json:"appName"`
	PackageID   string `json:"packageId"`
	Version     string `json:"version"`
	BuildNumber string `json:"buildNumber"`
}

// StageResult 是暂存结果；解析失败只体现在 Warnings，绝不作为错误返回。
type StageResult struct {
	StageToken string     `json:"stageToken"`
	Parsed     ParsedMeta `json:"parsed"`
	Warnings   []string   `json:"warnings"`
}

// CommitInput 是提交发布请求体，**恰好 8 个字段**：token + 表单最终值。
// 不含文件、不含解析结果、不含 touched 标记，也不含 metaSource（来源由服务端派生）。
type CommitInput struct {
	StageToken  string `json:"stageToken"`
	Environment string `json:"environment"`
	Platform    string `json:"platform"`
	AppName     string `json:"appName"`
	PackageID   string `json:"packageId"`
	Version     string `json:"version"`
	BuildNumber string `json:"buildNumber"`
	Changelog   string `json:"changelog"`
}

// ListQuery 是列表查询条件，Platform 为空表示不筛。
type ListQuery struct {
	Environment string
	Platform    string
}

// ListResult 是列表响应体；空结果 Items 为空数组而不是 nil，避免前端拿到 null。
type ListResult struct {
	Items       []Build `json:"items"`
	Total       int     `json:"total"`
	Environment string  `json:"environment"`
	Platform    string  `json:"platform"`
}

// HealthStatus 是健康检查结果。
type HealthStatus struct {
	Status           string `json:"status"`
	Uptime           int64  `json:"uptime"`
	DbOK             bool   `json:"dbOk"`
	StorageWritable  bool   `json:"storageWritable"`
}

// FileRef 是下载所需的已校验文件引用。
type FileRef struct {
	AbsPath     string
	ContentType string
	FileName    string
	Size        int64
}

// IsValidEnvironment / IsValidPlatform 是唯一的枚举校验入口。
func IsValidEnvironment(value string) bool {
	for _, item := range Environments {
		if item == value {
			return true
		}
	}
	return false
}

func IsValidPlatform(value string) bool {
	for _, item := range Platforms {
		if item == value {
			return true
		}
	}
	return false
}

// PlatformOfExt 由文件扩展名反推平台提示，供 stage 解析使用（不用于最终落盘）。
func PlatformOfExt(ext string) (string, bool) {
	for _, platform := range Platforms {
		if PlatformInfos[platform].Ext == ext {
			return platform, true
		}
	}
	return "", false
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsValidUUID 校验标准 UUID 形式：stage token 与包 id 共用。
// token 校验避免用它拼出任意路径；id 校验避免把非法字符串绑定到 PG 的 uuid 列触发 500。
func IsValidUUID(value string) bool {
	return uuidPattern.MatchString(value)
}

// NewStageToken 用标准库生成不可枚举的 UUID v4，零第三方依赖。
func NewStageToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成暂存标识失败: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

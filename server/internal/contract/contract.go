// Package contract 定义跨层 DTO 与契约常量的 Go 镜像。
// 职责：前后端共读 api/openapi.yaml；本文件把枚举、路由、长度上限镜像为 Go 常量供运行时使用。
// 输入：无外部输入（值由契约一致性测试 contract_test.go 逐项比对 yaml 锁死）。
// 输出：Build / StageResult / CommitInput / LoginInput / LoginResult 等 DTO、环境与平台常量、路由常量与工具函数。
// 边界：不含业务逻辑、不访问 DB / 文件系统、不生成 URL（三个 URL 由 service.buildURLs 唯一生成）。
package contract

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// 跨层错误标记：service 不依赖 handler，用 sentinel 表达可预期的业务拒绝，
// 由 handler.WriteError 统一翻译成对应的 HTTP 状态码与中文文案。
var (
	ErrNotFound         = errors.New("包不存在或已被移除")
	ErrStageInvalid     = errors.New("暂存文件已失效，请重新选择文件")
	ErrPlatformMismatch = errors.New("所选平台与文件类型不一致")
	// ErrInvalidCredentials 登录凭据错误 → 400：与全站「业务参数不合法」同一口径，不占用 401。
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	// ErrUnauthorized 未携带令牌或令牌失效 → 401：前端据此清空会话并回到登录页。
	ErrUnauthorized = errors.New("登录状态已失效，请重新登录")
	// ErrAPIKeyInvalid 外部接口的 Key 缺失 / 未配置 / 不匹配 → 401。
	ErrAPIKeyInvalid = errors.New("API Key 无效，请在系统设置中查看后重试")
	// ErrForbidden 已登录但无权访问该功能 → 403（前端隐藏菜单不是安全边界，接口必须自己拒绝）。
	ErrForbidden = errors.New("无权访问该功能")
	// ErrProjectInvalid 请求里的项目 key 不存在 → 400：属于参数错误，不是资源缺失。
	ErrProjectInvalid = errors.New("项目不存在，请刷新项目列表后重试")
)

// ValidationError 是业务层对请求内容的拒绝（如 Webhook 地址非法），
// 由 handler.WriteError 翻译成 400，避免每加一条校验规则就往 contract 里塞一个 sentinel。
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

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
	// PlatformHarmony 鸿蒙：只支持上传与分发，暂不解析包内元数据。
	PlatformHarmony = "harmony"
)

// 元数据来源枚举：由服务端派生（任一字段发生空值兜底 → fallback，否则 → parsed），
// 不出现在 CommitBuildRequest 中，前端无法伪造。
const (
	MetaParsed   = "parsed"
	MetaFallback = "fallback"
)

// 路由常量：servers.url + paths 键组成完整路径；契约测试比对同一份列表。
const (
	APIPrefix              = "/api/v1"
	RouteLogin             = APIPrefix + "/auth/login"
	RouteProjects          = APIPrefix + "/projects"
	RouteProjectByKey      = APIPrefix + "/projects/{key}"
	RoutePeople            = APIPrefix + "/people"
	RoutePersonByID        = APIPrefix + "/people/{id}"
	RouteTeams             = APIPrefix + "/teams"
	RouteTeamByID          = APIPrefix + "/teams/{id}"
	RouteMe                = APIPrefix + "/me"
	RouteAdminUsers        = APIPrefix + "/admin/users"
	RouteAdminUserByID     = APIPrefix + "/admin/users/{id}"
	RouteAdminUserPassword = APIPrefix + "/admin/users/{id}/reset-password"
	RouteAdminUserStatus   = APIPrefix + "/admin/users/{id}/status"
	RouteAdminAuditLogs    = APIPrefix + "/admin/audit-logs"
	RouteSettings          = APIPrefix + "/settings"
	RouteWebhookTest       = APIPrefix + "/settings/webhook/test"
	RouteOpenBuilds        = APIPrefix + "/open/builds"
	RouteStage             = APIPrefix + "/builds/stage"
	RouteBuilds            = APIPrefix + "/builds"
	RouteBuildByID         = APIPrefix + "/builds/{id}"
	RouteBuildFile         = APIPrefix + "/builds/{id}/file"
	RouteHealth            = "/healthz"
	RouteDownloadPage      = "/d/{id}"
)

// 契约级上限，与 x-constants 对齐。
const (
	MaxAppNameLen        = 100
	MaxPackageIDLen      = 100
	MaxVersionLen        = 50
	MaxBuildNumberLen    = 50
	MaxChangelogLen      = 2000
	MaxUsernameLen       = 64
	MaxPasswordLen       = 128
	MaxWebhookURLLen     = 500
	MaxWebhookSecretLen  = 200
	MaxProjectKeyLen     = 32
	MaxProjectNameLen    = 60
	MaxProjectDescLen    = 200
	MaxRepoURLLen        = 300
	MaxPersonNameLen     = 40
	MaxUsernameLen2      = 64
	MaxEmailLen          = 120
	MaxPhoneLen          = 40
	MaxTitleLen          = 60
	MaxTeamNameLen       = 60
	MaxTeamDescLen       = 200
	MaxRelationCount     = 200
	StageTokenTTLSeconds = 3600
	ParseTimeoutSeconds  = 5
	ParseReadLimitMB     = 5
	// AuthTokenTTLSeconds 登录令牌有效期（12 小时）；与 x-constants.authTokenTTLSeconds 一致。
	AuthTokenTTLSeconds = 43200
)

// Environments / Platforms / MetaSources / APIRoutes 供契约测试与页面渲染使用。
var Environments = []string{EnvTest, EnvHotfix, EnvPre, EnvPrd}
var Platforms = []string{PlatformAndroid, PlatformIOS, PlatformHarmony}
var MetaSources = []string{MetaParsed, MetaFallback}
var APIRoutes = []string{
	RouteLogin, RouteMe,
	RouteAdminUsers, RouteAdminUserByID, RouteAdminUserPassword, RouteAdminUserStatus,
	RouteAdminAuditLogs,
	RouteProjects, RouteProjectByKey,
	RoutePeople, RoutePersonByID, RouteTeams, RouteTeamByID,
	RouteSettings, RouteWebhookTest, RouteOpenBuilds,
	RouteStage, RouteBuilds, RouteBuildByID, RouteBuildFile, RouteHealth,
}

// Webhook 可推送字段的键：与 api/openapi.yaml 的 x-webhook-fields 键集合一致。
const (
	FieldID               = "id"
	FieldAppName          = "appName"
	FieldPackageID        = "packageId"
	FieldVersion          = "version"
	FieldBuildNumber      = "buildNumber"
	FieldEnvironment      = "environment"
	FieldPlatform         = "platform"
	FieldChangelog        = "changelog"
	FieldFileSize         = "fileSize"
	FieldOriginalFileName = "originalFileName"
	FieldDownloadPageURL  = "downloadPageUrl"
	FieldDownloadFileURL  = "downloadFileUrl"
	FieldDownloadCount    = "downloadCount"
	FieldCreatedAt        = "createdAt"
)

// WebhookFields 是设置页复选框的展示顺序，同时充当字段合法性的唯一校验集合。
var WebhookFields = []string{
	FieldID, FieldAppName, FieldPackageID, FieldVersion, FieldBuildNumber,
	FieldEnvironment, FieldPlatform, FieldChangelog, FieldFileSize,
	FieldOriginalFileName, FieldDownloadPageURL, FieldDownloadFileURL,
	FieldDownloadCount, FieldCreatedAt,
}

// WebhookFieldLabels 是字段中文名，镜像 x-webhook-fields，由契约测试逐项锁死。
var WebhookFieldLabels = map[string]string{
	FieldID:               "包 ID",
	FieldAppName:          "应用名",
	FieldPackageID:        "包名 / Bundle ID",
	FieldVersion:          "版本号",
	FieldBuildNumber:      "构建号",
	FieldEnvironment:      "环境",
	FieldPlatform:         "平台",
	FieldChangelog:        "更新说明",
	FieldFileSize:         "文件大小",
	FieldOriginalFileName: "原始文件名",
	FieldDownloadPageURL:  "下载页地址",
	FieldDownloadFileURL:  "下载文件地址",
	FieldDownloadCount:    "下载次数",
	FieldCreatedAt:        "上传时间",
}

// DefaultWebhookFields 是新装实例的默认勾选：覆盖交接所需信息，不暴露内部存储路径。
var DefaultWebhookFields = []string{
	FieldAppName, FieldVersion, FieldBuildNumber, FieldEnvironment,
	FieldPlatform, FieldDownloadPageURL,
}

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
	PlatformHarmony: {Label: "鸿蒙", Ext: "hap", ContentType: "application/octet-stream"},
}

// LoginInput 是登录请求体，**恰好 2 个字段**；不接受任何额外字段。
type LoginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResult 是登录成功后的无状态令牌；服务端不存会话，只校验签名与到期时间。
type LoginResult struct {
	Token     string `json:"token"`
	Username  string `json:"username"`
	ExpiresAt string `json:"expiresAt"`
}

// WebhookConfig 是 Webhook 的完整配置；字段集合见 WebhookFields。
type WebhookConfig struct {
	Enabled bool     `json:"enabled"`
	URL     string   `json:"url"`
	Secret  string   `json:"secret"`
	Fields  []string `json:"fields"`
}

// WebhookFieldOption 是设置页渲染复选框用的字段目录项。
type WebhookFieldOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Settings 是系统设置页的全部数据；APIKey 只在此接口返回。
type Settings struct {
	Webhook         WebhookConfig        `json:"webhook"`
	APIKey          string               `json:"apiKey"`
	AvailableFields []WebhookFieldOption `json:"availableFields"`
}

// WebhookTestResult 是测试投递结果；目标不可达也返回 200，用 Delivered 表达。
type WebhookTestResult struct {
	Delivered  bool   `json:"delivered"`
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
}

// IsValidWebhookField 判断字段键是否在允许集合内。
func IsValidWebhookField(field string) bool {
	_, ok := WebhookFieldLabels[field]
	return ok
}

// Project 是业务数据的一级归属：应用包、应用、热更新都挂在项目下。
// Key 同时是 URL 段（/p/{key}/...），创建后不可修改。
type Project struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoURL     string `json:"repoUrl"`
	CreatedAt   string `json:"createdAt"`
}

// ProjectInput 是新建 / 编辑项目请求，恰好 4 个字段。
type ProjectInput struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoURL     string `json:"repoUrl"`
}

// ProjectList 是项目列表响应体：items 与分页元信息平铺；空结果为 items: [] 而不是 nil。
type ProjectList struct {
	Items []Project `json:"items"`
	Page
}

// ProjectDetail 是项目详情：项目本身 + 关联的人员与团队（带名字，详情页不必再拉两份列表）。
type ProjectDetail struct {
	Project
	Persons []RelationRef `json:"persons"`
	Teams   []RelationRef `json:"teams"`
}

// RelationRef 是关联的轻量投影：只带 id 与名字。
// 列表接口直接带回它，前端就不必为了"把 ID 显示成名字"再拉一遍对方列表。
type RelationRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// 列表分页的默认值与上限。PageSize=0 是显式的「不分页」，
// 只给「要一次拿全量」的场景用（下拉选项、聚合统计），列表页一律传具体页大小。
const (
	DefaultPageSize = 10
	MaxPageSize     = 200
)

// PageQuery 是列表的分页参数（已归一化，非法的 page 会被压到 1）。
type PageQuery struct {
	Page     int
	PageSize int
}

// Limit 返回 SQL 的 LIMIT：不分页时给一个足够大的上限，避免为 0 时查出 0 行。
func (q PageQuery) Limit() int {
	if q.PageSize <= 0 {
		return 1 << 30
	}
	return q.PageSize
}

// Offset 返回 SQL 的 OFFSET：不分页时恒为 0。
func (q PageQuery) Offset() int {
	if q.PageSize <= 0 || q.Page <= 1 {
		return 0
	}
	return (q.Page - 1) * q.PageSize
}

// Page 是分页响应的元信息，与 items 平铺返回，前端据此渲染分页条。
type Page struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
	Total    int `json:"total"`
}

// Person 是人员；Teams / Projects 是该人员关联的团队与项目（多选）。
type Person struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Username  string        `json:"username"`
	Email     string        `json:"email"`
	Phone     string        `json:"phone"`
	Title     string        `json:"title"`
	Teams     []RelationRef `json:"teams"`
	Projects  []RelationRef `json:"projects"`
	CreatedAt string        `json:"createdAt"`
}

// PersonInput 是新增 / 编辑人员请求；写入仍用 ID 数组。
type PersonInput struct {
	Name       string   `json:"name"`
	Username   string   `json:"username"`
	Email      string   `json:"email"`
	Phone      string   `json:"phone"`
	Title      string   `json:"title"`
	TeamIDs    []string `json:"teamIds"`
	ProjectIDs []string `json:"projectIds"`
}

// PersonList 是人员列表响应体：items 与分页元信息平铺。
type PersonList struct {
	Items []Person `json:"items"`
	Page
}

// Team 是团队；Persons / Projects 是该团队关联的人员与项目（多选）。
type Team struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Persons     []RelationRef `json:"persons"`
	Projects    []RelationRef `json:"projects"`
	CreatedAt   string        `json:"createdAt"`
}

// TeamInput 是新增 / 编辑团队请求；写入仍用 ID 数组。
type TeamInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	PersonIDs   []string `json:"personIds"`
	ProjectIDs  []string `json:"projectIds"`
}

// TeamList 是团队列表响应体：items 与分页元信息平铺。
type TeamList struct {
	Items []Team `json:"items"`
	Page
}

// Me 是「当前登录人」的档案投影：没有对应人员记录时 PersonID 为空、ProjectKeys 为空。
// 前端据此把应用包/应用的默认项目设为「我关联的第一个项目」。
type Me struct {
	Username     string   `json:"username"`
	PersonID     string   `json:"personId"`
	Name         string   `json:"name"`
	ProjectKeys  []string `json:"projectKeys"`
	IsSuperAdmin bool     `json:"isSuperAdmin"`
}

// Identity 是鉴权中间件解析出的「当前调用者」，只含判定所需的最小字段。
// 放在 contract 里是因为 service/login 产出它、middleware/login 消费它，两边不该互相 import。
type Identity struct {
	UserID       string
	Username     string
	IsSuperAdmin bool
}

// User 是账号视图：**不含任何密码字段**。
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PersonID     string `json:"personId"`
	PersonName   string `json:"personName"`
	IsSuperAdmin bool   `json:"isSuperAdmin"`
	Status       string `json:"status"`
	LastLoginAt  string `json:"lastLoginAt"`
	LastLoginIP  string `json:"lastLoginIp"`
	CreatedAt    string `json:"createdAt"`
}

// UserInput 新建账号：账号名 + 初始密码 + 是否超管（+ 可选绑定人员）。
type UserInput struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	PersonID     string `json:"personId"`
	IsSuperAdmin bool   `json:"isSuperAdmin"`
}

// UserUpdate 编辑账号：账号名不可改，只允许改超管标记与绑定人员。
type UserUpdate struct {
	PersonID     string `json:"personId"`
	IsSuperAdmin bool   `json:"isSuperAdmin"`
}

// UserStatusInput 只用于启用 / 禁用。
type UserStatusInput struct {
	Status string `json:"status"`
}

// PasswordReset 承载一次性临时密码；明文只在该响应里出现一次。
type PasswordReset struct {
	Password string `json:"password"`
}

// AuditLog 是一条审计记录；detail 是 JSON 文本（写入方决定结构），前端格式化展示。
type AuditLog struct {
	ID         int64  `json:"id"`
	CreatedAt  string `json:"createdAt"`
	ActorID    string `json:"actorId"`
	ActorName  string `json:"actorName"`
	Action     string `json:"action"`
	ObjectType string `json:"objectType"`
	ObjectID   string `json:"objectId"`
	Result     string `json:"result"`
	Detail     string `json:"detail"`
	IP         string `json:"ip"`
}

// AuditQuery 是审计日志的筛选条件，字段为空表示不筛。
type AuditQuery struct {
	Action    string
	ActorName string
	Result    string
}

// AuditLogList 是审计列表响应体：items 与分页元信息平铺。
type AuditLogList struct {
	Items []AuditLog `json:"items"`
	Page
}

// UserList 是账号列表响应体：items 与分页元信息平铺。
type UserList struct {
	Items []User `json:"items"`
	Page
}

var projectKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// IsValidProjectKey 校验项目 key：小写字母 / 数字 / 短横线，2-32 位，且以字母数字开头。
func IsValidProjectKey(key string) bool {
	return projectKeyPattern.MatchString(key)
}

// NormalizeHTTPURL 去掉首尾空白并校验是否为 http(s) 绝对地址，返回规范化结果。
// 项目的 GitLab 地址与 Webhook 地址共用这一份判断，避免两处各写一遍。
func NormalizeHTTPURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errInvalidHTTPURL
	}
	return trimmed, nil
}

var errInvalidHTTPURL = errors.New("必须是 http:// 或 https:// 开头的绝对地址")

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

// CommitInput 是提交发布请求体，**恰好 9 个字段**：项目 + token + 表单最终值。
// 不含文件、不含解析结果、不含 touched 标记，也不含 metaSource（来源由服务端派生）。
type CommitInput struct {
	Project     string `json:"project"`
	StageToken  string `json:"stageToken"`
	Environment string `json:"environment"`
	Platform    string `json:"platform"`
	AppName     string `json:"appName"`
	PackageID   string `json:"packageId"`
	Version     string `json:"version"`
	BuildNumber string `json:"buildNumber"`
	Changelog   string `json:"changelog"`
}

// ListQuery 是列表查询条件；Platform 为空表示不筛，Project 是项目 key。
type ListQuery struct {
	Project     string
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
	Status          string `json:"status"`
	Uptime          int64  `json:"uptime"`
	DbOK            bool   `json:"dbOk"`
	StorageWritable bool   `json:"storageWritable"`
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

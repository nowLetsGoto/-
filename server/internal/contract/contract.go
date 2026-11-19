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
	"unicode/utf8"
)

// 跨层错误标记：service 不依赖 handler，用 sentinel 表达可预期的业务拒绝，
// 由 handler.WriteError 统一翻译成对应的 HTTP 状态码与中文文案。
var (
	// ErrNotFound 是「资源缺失」的兜底 sentinel：只在调用方没有更具体的资源名时使用。
	//
	// 为什么文案是通用的而不是"包不存在"：这个错误会被人员/团队/角色/公司等
	// 多种仓储返回，绑定某一种资源的说法会让其他场景的用户找错方向。
	// 需要精确文案时请用 NotFound(resource)——把资源名留给调用方，句式由本包统一生成。
	ErrNotFound         = errors.New("该资源不存在或已被移除")
	ErrStageInvalid     = errors.New("暂存文件已失效，请重新选择文件")
	ErrPlatformMismatch = errors.New("所选平台与文件类型不一致")
	// ErrInvalidCredentials 登录凭据错误 → 400：与全站「业务参数不合法」同一口径，不占用 401。
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	// ErrTooManyAttempts 登录失败次数超限，临时锁定 → 429。
	//
	// 为什么可以明确告知（不像凭据错误那样含糊）：不存在的账号同样会被计数并锁定，
	// 所以「被锁定」不能用来判断账号是否存在。反过来，含糊其辞会让真实用户
	// 以为密码记错了而反复重试，把 15 分钟锁死得更彻底——这是安全与可用性的正确取舍。
	ErrTooManyAttempts = errors.New("登录尝试过于频繁，请稍后再试")
	// ErrTenantDisabled 账号所属公司已被停用 → 403。
	//
	// 这条在密码校验**之后**才判定，因此能走到这里的调用方本来就是该公司的人，
	// 不存在账号存在性泄露；说清原因反而能让对方知道"去找公司管理员"而不是反复试密码。
	ErrTenantDisabled = errors.New("所属公司已停用，请联系平台管理员")
	// ErrUnauthorized 未携带令牌或令牌失效 → 401：前端据此清空会话并回到登录页。
	ErrUnauthorized = errors.New("登录状态已失效，请重新登录")
	// ErrAPIKeyInvalid 外部接口的 Key 缺失 / 未配置 / 不匹配 → 401。
	ErrAPIKeyInvalid = errors.New("API Key 无效，请在系统设置中查看后重试")
	// ErrAPIKeyUnbound 平台兜底 Key（不属于任何公司）调用外部上传 → 403。
	//
	// 与 ErrAPIKeyInvalid 分开：Key 本身是有效的，只是**没有公司归属**，
	// 因此不能往任何公司的项目投包。文案要让 CI 日志能直接指向修法，
	// 而不是让人反复核对 Key 是否抄错。
	ErrAPIKeyUnbound = errors.New("平台级 API Key 不能用于上传：请使用该公司的 API Key（平台集成 → 外部 API）")
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

// 资源名：NotFound 的唯一变量。
//
// 集中成常量而不是各层手写字符串，理由与枚举常量相同：
// 一处拼错（"人员"写成"成员"）会让全站同类提示不一致，而且编译器发现不了。
const (
	ResourceBuild  = "应用包"
	ResourceProject = "项目"
	ResourcePerson = "人员"
	ResourceTeam   = "团队"
	ResourceUser   = "账号"
	ResourceRole   = "角色"
	ResourceTenant = "公司"
	ResourceFile   = "文件"
)

// NotFoundError 是「指定资源不存在」的错误，由 handler.WriteError 翻译成 404。
//
// 调用方只声明"缺的是什么"，文案句式由本类型统一决定——这把「错误语义」
// 和「上屏措辞」分开：业务层不需要知道中文怎么说，展示层不需要知道资源从哪来。
type NotFoundError struct {
	Resource string
}

// Error 返回统一句式："{资源}不存在或已被移除"。
//
// 刻意不提供"自带整句文案"的构造函数：一旦允许各处传完整句子，句式就会漂移，
// 这正是本次要修的问题。所有资源缺失都走同一句式，只有资源名不同。
func (e *NotFoundError) Error() string {
	return e.Resource + "不存在或已被移除"
}

// Is 让资源化的 NotFoundError 与通用 sentinel 等价。
//
// 这是整套改造的关键：现有大量 `errors.Is(err, contract.ErrNotFound)` 判断
// （中间件、service 的兜底分支）无需改动，仍能把带资源名的错误识别为"资源缺失"。
// 一处定义同时满足两件事——文案精确、判定统一。
func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// NotFound 构造一个资源缺失错误。调用点只需给出 ResourceXxx 常量，不拼文案。
func NotFound(resource string) error { return &NotFoundError{Resource: resource} }

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

// AccountLevel 是管理身份层级：决定「能管谁、管多大范围」，与业务角色完全正交。
// 取值与 api/openapi.yaml 的 AccountLevel.enum、users.account_level 的 CHECK 约束一一对应。
// 顺序从高到低；新增层级必须同时改三处并在此注释里说明，避免各层各自解释大小关系。
const (
	AccountLevelSuperAdmin    = "super_admin"
	AccountLevelPlatformAdmin = "platform_admin"
	AccountLevelTeamAdmin     = "team_admin"
	AccountLevelMember        = "member"
)

// AccountLevels 是四级枚举的有序列表，供契约一致性测试逐项比对 openapi.yaml。
var AccountLevels = []string{
	AccountLevelSuperAdmin, AccountLevelPlatformAdmin, AccountLevelTeamAdmin, AccountLevelMember,
}

// AccountLevelRank 返回层级的数值序：super_admin=4 → member=1，未知/空值为 0。
// 未知值给最低序（而不是默认最高）是刻意的安全选择：库里出现脏值时，
// 它连 member 门槛都过不了，不会因为「没写层级」意外获得管理权限。
func AccountLevelRank(level string) int {
	switch level {
	case AccountLevelSuperAdmin:
		return 4
	case AccountLevelPlatformAdmin:
		return 3
	case AccountLevelTeamAdmin:
		return 2
	case AccountLevelMember:
		return 1
	default:
		return 0
	}
}

// IsAccountLevel 判断字符串是否四级枚举之一，用于拒绝请求里的脏层级值。
func IsAccountLevel(level string) bool {
	return AccountLevelRank(level) > 0
}

// AccountLevelAtLeast 判断 level 是否不低于 min。
// min 不是四级枚举之一时一律返回 false——门槛拼错应当把接口关上，而不是对所有人放行。
func AccountLevelAtLeast(level, min string) bool {
	want := AccountLevelRank(min)
	if want == 0 {
		return false
	}
	return AccountLevelRank(level) >= want
}

// EffectiveAccountLevel 把库里的 account_level 与过渡期的 is_super_admin 布尔折算成唯一有效层级。
// 保留布尔是过渡安排：旧账号（含种子管理员）的 account_level 仍是 member，
// 若不看布尔，加列这一步就会把现有超管挡在门外。布尔只会「抬高」到 super_admin，
// 不会反过来压低一个显式设置的层级。
// 空值保持为空（而不是补成 member）：零值身份代表"还没鉴权"，必须过不了任何层级门槛。
func EffectiveAccountLevel(level string, superAdmin bool) string {
	if superAdmin {
		return AccountLevelSuperAdmin
	}
	return level
}

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
	RouteMePassword        = APIPrefix + "/me/password"
	RoutePermissions       = APIPrefix + "/permissions"
	// RouteMenus 暴露平台级菜单目录，供角色授权页渲染菜单树。
	// 只读：菜单结构由代码定义，任何公司都不能增删改（见 Menus 的说明）。
	RouteMenus = APIPrefix + "/menus"
	RouteRoles             = APIPrefix + "/roles"
	// RouteRoleByID 是单个角色的读写入口。
	// 用 id 而不是 key：key 只保证公司内唯一，超管视角下会歧义（见 service/role.Get）。
	RouteRoleByID = APIPrefix + "/roles/{id}"
	RouteAdminUsers        = APIPrefix + "/admin/users"
	RouteAdminUserByID     = APIPrefix + "/admin/users/{id}"
	RouteAdminUserPassword = APIPrefix + "/admin/users/{id}/reset-password"
	RouteAdminUserStatus   = APIPrefix + "/admin/users/{id}/status"
	RouteAdminAuditLogs    = APIPrefix + "/admin/audit-logs"
	// RoutePlatformTenants 是超管的公司管理入口（列表 + 新建，同一路径不同方法）。
	RoutePlatformTenants = APIPrefix + "/platform/tenants"
	// RoutePlatformTenantByID 是单家公司的操作入口（删除）。
	RoutePlatformTenantByID = APIPrefix + "/platform/tenants/{id}"
	// RoutePlatformTenantStatus 启停公司；与 ByID 分开是因为它是"改状态"而非"改资料"。
	RoutePlatformTenantStatus = APIPrefix + "/platform/tenants/{id}/status"
	RouteSettings          = APIPrefix + "/settings"
	RouteWebhookTest       = APIPrefix + "/settings/webhook/test"
	RouteAPIKeyRotate      = APIPrefix + "/settings/api-key/rotate"
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
	RouteLogin, RouteMe, RouteMePassword,
	RouteAdminUsers, RouteAdminUserByID, RouteAdminUserPassword, RouteAdminUserStatus,
	RouteAdminAuditLogs,
	RoutePlatformTenants, RoutePlatformTenantByID, RoutePlatformTenantStatus,
	RouteProjects, RouteProjectByKey,
	RoutePeople, RoutePersonByID, RouteTeams, RouteTeamByID,
	RoutePermissions, RouteMenus, RouteRoles, RouteRoleByID,
	RouteSettings, RouteWebhookTest, RouteAPIKeyRotate, RouteOpenBuilds,
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
// SessionMode 是登录时选定的数据视图：
//   - team：平台视图，能看到全部项目数据与管理菜单；
//   - solo：个人视图，只能看到自己上传的包与自己产生的审计记录。
//
// 模式随令牌签发，客户端改 localStorage 不会改变服务端行为。
type SessionMode string

const (
	ModeSolo SessionMode = "solo"
	ModeTeam SessionMode = "team"
)

// IsValid 防止令牌载荷里出现未知模式。
func (m SessionMode) IsValid() bool {
	return m == ModeSolo || m == ModeTeam
}

type LoginInput struct {
	Username string      `json:"username"`
	Password string      `json:"password"`
	Mode     SessionMode `json:"mode"`
}

// NormalizeMode 把空值按默认单人模式处理，非法值明确拒绝。
func NormalizeMode(mode SessionMode) (SessionMode, error) {
	if mode == "" {
		return ModeSolo, nil
	}
	if !mode.IsValid() {
		return "", &ValidationError{Message: "登录模式只能是单人模式或团队模式"}
	}
	return mode, nil
}

// NormalizeAccountMode 归一化账号自带的模式，**缺省为团队模式**。
// 仅供「种子账号」这类没有登录上下文、必须显式指定模式的内部调用使用；
// 超管在页面上建账号时模式取自他自己的登录模式，不经过这里——
// 登录时已经选定模式，账号的归属随之确定，不该再由建账号的人二次挑选。
func NormalizeAccountMode(mode SessionMode) (SessionMode, error) {
	if mode == "" {
		return ModeTeam, nil
	}
	if !mode.IsValid() {
		return "", &ValidationError{Message: "账号模式只能是单人模式或团队模式"}
	}
	return mode, nil
}

// LoginResult 是登录成功后的无状态令牌；服务端不存会话，只校验签名与到期时间。
// AccountLevel / TenantID 由账号表随令牌一并返回，供前端首屏立即知道身份层级；
// 真正的完整身份（含 teamIds）仍以 /me 为准，客户端传来的这两个字段永远不被信任。
type LoginResult struct {
	Token     string      `json:"token"`
	Username  string      `json:"username"`
	ExpiresAt string      `json:"expiresAt"`
	Mode      SessionMode `json:"mode"`
	// AccountLevel 是折算后的有效层级（旧 is_super_admin 布尔在此已折算为 super_admin）。
	AccountLevel string `json:"accountLevel"`
	// TenantID 是账号归属公司；超管为空串。
	TenantID string `json:"tenantId"`
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

// Settings 是系统设置页的全部数据。
//
// 这里**不再返回 API Key 明文**：库里只存 SHA-256 摘要，明文只在「轮换」的那一次响应里出现。
// 设置页需要的只是「有没有配过」，所以用 APIKeyConfigured 这个布尔表达。
type Settings struct {
	Webhook         WebhookConfig        `json:"webhook"`
	APIKeyConfigured bool                `json:"apiKeyConfigured"`
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
	RoleKey     string `json:"roleKey"`
	RoleName    string `json:"roleName"`
	CreatedAt   string `json:"createdAt"`
	// TenantID 是项目归属公司；solo 分区的项目恒为空。
	// 包继承项目的归属，Webhook 投递据此选择「这家公司」的配置。
	TenantID string `json:"tenantId"`
}

// ProjectInput 是新建 / 编辑项目请求，恰好 4 个字段。
type ProjectInput struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoURL     string `json:"repoUrl"`
	RoleKey     string `json:"roleKey"`
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
	RoleKey   string        `json:"roleKey"`
	RoleName  string        `json:"roleName"`
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
	RoleKey    string   `json:"roleKey"`
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
	Username string `json:"username"`
	PersonID string `json:"personId"`
	Name     string `json:"name"`
	// RoleKey / RoleName 是人员在团队里的角色，供账号卡展示「当前角色」。
	// 未分配角色时两者都是空串，由前端统一显示成「普通用户」——
	// 「没有角色」与「角色名叫普通用户」在展示上是同一件事，不必在后端造一个假角色。
	RoleKey      string   `json:"roleKey"`
	RoleName     string   `json:"roleName"`
	ProjectKeys  []string `json:"projectKeys"`
	IsSuperAdmin bool     `json:"isSuperAdmin"`
	// 租户身份三件套，全部由服务端从账号推导，前端只做菜单投影，不参与判定。
	// TenantID / TeamIDs 为空表示超管（跨全部公司）或未绑定任何团队；
	// AccountLevel 是折算后的有效层级，与 isSuperAdmin 在过渡期同时存在。
	TenantID     string   `json:"tenantId"`
	TenantName   string   `json:"tenantName"`
	AccountLevel string   `json:"accountLevel"`
	TeamIDs      []string `json:"teamIds"`
	// Permissions 是"全局视角"（不含项目）的权限集合，供前端裁剪菜单与按钮。
	// 项目内的权限可能被项目角色覆盖，那部分由各接口自己判定。
	Permissions []string `json:"permissions"`
	// MenuKeys 是当前用户角色被授权的菜单，供前端做"减法"收窄侧栏。
	//
	// 它不是判定依据，只是前端的投影输入：真正的边界始终是后端守卫。
	// 超管拿到的是全部菜单（不受角色配置影响）。
	// 未分配角色时是空数组——前端据此只保留"无需角色"的基础菜单。
	MenuKeys []string `json:"menuKeys"`
}

// Permission 是一个权限点。目录定义在代码里（Permissions），数据库只存"角色拥有哪些"，
// 这样新增权限点不需要改表结构，也不会出现"代码里有、库里没有"的漂移。
type Permission struct {
	Key   string `json:"key"`
	Group string `json:"group"`
	Label string `json:"label"`
}

// Permissions 是**业务角色可勾选**的权限点，按业务模块分组；前端按 Group 渲染勾选矩阵。
//
// 这里只放"业务能力"（能看什么、能传什么），**不放管理能力**：
//   - directory.read 保留：看通讯录是业务需要（提交包要选项目、看团队）；
//   - directory.write 移出：改人员/团队是管理动作，由 account_level（team_admin 及以上）直接赋予；
//   - settings.read/write 移出：平台集成配置属于公司管理，由 platform_admin 直接赋予；
//   - admin.access、角色管理移出：这些是身份层级本身，不该"勾一下就能给谁"。
//
// 这样从源头杜绝「给某人勾了 settings.write 就能改全公司配置」这类越权，
// 即使攻击者伪造权限点，IsValidPermission 也会拒绝。
// Permissions 是全部可勾选权限点。
//
// 这里同时包含业务权限与管理权限。管理权限曾经刻意不开放给角色（由 account_level 固有），
// 现在产品改为「普通账号完全由角色配置驱动」，所以必须让角色能配出管理能力——
// 否则勾选矩阵无论怎么点都给不出"能进角色管理"这类能力。
//
// 唯一例外是超管与平台管理员身份：它们仍由 account_level 控制（见 managementPermissions），
// 不依赖这里的勾选。
var Permissions = []Permission{
	{Key: "build.read", Group: "应用包", Label: "查看应用包与下载"},
	{Key: "build.write", Group: "应用包", Label: "上传与发布应用包"},
	{Key: "build.delete", Group: "应用包", Label: "删除应用包"},
	{Key: "project.read", Group: "项目", Label: "查看项目"},
	{Key: "project.write", Group: "项目", Label: "新建与编辑项目"},
	{Key: "project.delete", Group: "项目", Label: "删除项目"},
	{Key: "directory.read", Group: "基础资料", Label: "查看人员与团队"},
	{Key: PermissionDirectoryWrite, Group: "基础资料", Label: "新增与编辑人员与团队"},
	{Key: PermissionDirectoryDelete, Group: "基础资料", Label: "删除人员与团队"},
	{Key: PermissionRoleRead, Group: "角色管理", Label: "查看角色定义"},
	{Key: PermissionRoleWrite, Group: "角色管理", Label: "新建与编辑角色"},
	{Key: PermissionRoleDelete, Group: "角色管理", Label: "删除自定义角色"},
	{Key: PermissionSettingsRead, Group: "平台集成", Label: "查看集成配置"},
	{Key: PermissionSettingsWrite, Group: "平台集成", Label: "修改集成配置与轮换 Key"},
	{Key: PermissionAuditRead, Group: "审计日志", Label: "查看本公司审计日志"},
	// account.read 只给"看账号列表"，不含任何写操作。
	// 账号的新建/编辑/删除是平台治理动作，由 account_level 固有赋予（见 managementPermissions），
	// 不走角色配置——否则任意角色都能给自己或别人开账号、改归属，等同于绕过公司边界。
	{Key: PermissionAccountRead, Group: "账号管理", Label: "查看账号列表"},
}

// 管理类权限点的 key。
//
// 这些 key 现在既可写入角色（见 Permissions），也作为 platform_admin 的固有权限
// （见 service/authz.managementPermissions）。两个来源在 permissionsOf 里合并：
// 平台管理员哪怕角色没勾也照样有；普通账号则完全靠角色勾选。
const (
	PermissionDirectoryWrite  = "directory.write"
	PermissionDirectoryDelete = "directory.delete"
	PermissionSettingsRead    = "settings.read"
	PermissionSettingsWrite   = "settings.write"
	PermissionAdminAccess     = "admin.access"
	// PermissionRoleRead/Write 控制角色定义的查看与编辑；Delete 控制删除。
	// 拆成三个是为了让"只看不改"这种配置成立（审计员场景）。
	PermissionRoleRead  = "role.read"
	PermissionRoleWrite = "role.write"
	PermissionRoleDelete = "role.delete"
	// PermissionAuditRead 控制本公司审计日志的查看。
	PermissionAuditRead = "audit.read"
	// PermissionAccountRead 控制账号列表的**查看**。
	//
	// 与 directory.write 分开的理由：过去一个 directory.write 同时表示
	// 「能改人员/团队」「能看账号」「能改账号」三件事，导致想给某个角色
	// "只读账号列表"就必须同时给出管人管账号的权力。拆开后各管一件事。
	PermissionAccountRead = "account.read"
)

// IsValidPermission 判断权限点是否在**可勾选**目录里，写入角色时用它挡住拼错/伪造的值。
// 管理类权限点现在也在目录里（普通账号靠角色配置获得管理能力），但仍需通过本函数校验。
func IsValidPermission(key string) bool {
	for _, permission := range Permissions {
		if permission.Key == key {
			return true
		}
	}
	return false
}

// 注意：这里**没有** super-admin / platform-admin 这类"角色 key"。
//
// 超级管理员与平台管理员是**账号身份**，不是角色：
//   - 超级管理员：users.is_super_admin 标记，全平台固定；权限判定第一步就短路为全量，
//     从不查角色（见 service/authz.permissionsOf）。
//   - 平台管理员：users.account_level = 'platform_admin'，由超管在「公司管理」建公司时连带创建。
//
// 历史上曾有一个 key=super-admin 的内置角色，它从未被权限判定读取过，
// 只会让角色列表出现一个"看起来像超管"的选项而引人误解，已删除。
// 内置角色现在只有 project-owner / developer / tester / guest 四个业务角色。

// Role 是角色：key 用于代码引用，name 给人看，permissions 是权限点集合。
type Role struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	// IsBuiltin 表示"本公司预置的角色"：可以改权限，但不能删除。
	// 内置角色每家公司各持一份独立副本（见 service/role 的说明），不是全局共享。
	IsBuiltin bool `json:"isBuiltin"`
	// TenantID / TenantName 是角色归属的公司。
	// 公司账号只会看到自己公司的角色（TenantName 与自己的公司名相同，可忽略）；
	// 超管跨公司看全部，同名角色会出现多份，靠 TenantName 区分是哪家公司的。
	TenantID    string   `json:"tenantId"`
	TenantName  string   `json:"tenantName"`
	Permissions []string `json:"permissions"`
	// MenuKeys 是该角色被授权可见的菜单，按公司隔离存储。
	MenuKeys  []string `json:"menuKeys"`
	CreatedAt string   `json:"createdAt"`
}

// ── 菜单资源 ──────────────────────────────────────────────────────────────
//
// 设计要点（三者缺一不可）：
//
//  1. **菜单目录是平台级的、只读的**。它描述"系统有哪些页面"，属于产品结构，
//     不该让每家公司自己增删——否则甲公司加了个菜单，乙公司的前端根本没有对应路由。
//     目录的唯一来源是本文件的 Menus，前端 navigation.ts 与它逐条对齐。
//
//  2. **授权是每公司独立的**。菜单挂到角色上（role_menus），角色本身按 tenant_id 隔离，
//     所以甲公司的 developer 授权了哪些菜单，与乙公司毫无关系。
//
//  3. **授权只做减法**。菜单最终可见 = 原有身份/权限判据 ∩ 角色授权的菜单。
//     给普通成员勾上"账号管理"也不会放行——后端仍需 team_admin。
//     这保证菜单配置只能收窄视野，绝不能变成提权入口。
//
// 超管不受菜单授权约束（permissionsOf 第一步就短路为全量），永远看到全部菜单。

// MenuAction 是菜单下的一个可勾选操作权限，例如「应用包」下的上传、删除。
//
// 它取代了过去"权限点矩阵与菜单树分成两大块"的设计：
// 操作权限现在挂在它生效的菜单节点上，配菜单时顺手配操作，二者不会脱节。
type MenuAction struct {
	Key   string `json:"key"`   // 权限点 key，如 build.write
	Label string `json:"label"` // 上屏名称
}

// Menu 是菜单目录中的一项，按 group 分组展示成授权树。
type Menu struct {
	Key   string `json:"key"`   // 页面路径，如 /overview、/directory/roles；同时是授权存储的 key
	Label string `json:"label"` // 上屏名称
	Group string `json:"group"` // 一级分组名，用于前端渲染树
	// ViewPermission 是进入本菜单所需的"查看权限"。
	//
	// 勾选菜单即自动授予它，用户**不单独勾选**查看权限（见 service/role.normalize）。
	// 这条规则解决了最容易踩的坑：过去用户在菜单树里勾了"角色管理"，
	// 却没在另一块权限矩阵里勾 role.read，结果菜单死活不显示。
	ViewPermission string `json:"viewPermission"`
	// Actions 是本菜单下的操作权限（写/删等），在菜单节点内勾选。
	Actions []MenuAction `json:"actions"`
}

// Menus 是全部可授权菜单，顺序即前端展示顺序。
//
// 为什么用路径当 key：前端路由表里路径本来就唯一且稳定，
// 再发明一套 menu-id 就得维护"id ↔ path"映射，多一处必然漂移的地方。
//
// 每张菜单自带查看权限与操作权限，这个映射是**唯一来源**：
// 保存角色时由它推导最终 permissions（见 service/role.derivePermissions），
// 前端也只渲染这份树，不再有独立的"权限点矩阵"。
var Menus = []Menu{
	{Key: "/overview", Label: "概览", Group: "总览", ViewPermission: "build.read"},
	{
		Key: "/packages", Label: "应用包", Group: "分发", ViewPermission: "build.read",
		Actions: []MenuAction{
			{Key: "build.write", Label: "上传与发布应用包"},
			{Key: "build.delete", Label: "删除应用包"},
		},
	},
	{Key: "/apps", Label: "应用", Group: "分发", ViewPermission: "build.read"},
	{
		Key: "/directory/people", Label: "人员管理", Group: "基础资料", ViewPermission: "directory.read",
		Actions: []MenuAction{
			{Key: PermissionDirectoryWrite, Label: "新增与编辑人员"},
			{Key: PermissionDirectoryDelete, Label: "删除人员"},
		},
	},
	{
		Key: "/directory/teams", Label: "团队管理", Group: "基础资料", ViewPermission: "directory.read",
		Actions: []MenuAction{
			{Key: PermissionDirectoryWrite, Label: "新增与编辑团队"},
			{Key: PermissionDirectoryDelete, Label: "删除团队"},
		},
	},
	{
		Key: "/directory/projects", Label: "项目管理", Group: "基础资料", ViewPermission: "project.read",
		Actions: []MenuAction{
			{Key: "project.write", Label: "新建与编辑项目"},
			{Key: "project.delete", Label: "删除项目"},
		},
	},
	// 账号管理：查看权限独立成 account.read，可由角色配置。
	// 新增/编辑等写操作不在菜单里体现——它们只属于平台管理员，不受角色配置影响。
	{Key: "/admin/accounts", Label: "账号管理", Group: "基础资料", ViewPermission: PermissionAccountRead},
	{
		Key: "/directory/roles", Label: "角色管理", Group: "公司设置", ViewPermission: PermissionRoleRead,
		Actions: []MenuAction{
			{Key: PermissionRoleWrite, Label: "新建与编辑角色"},
			{Key: PermissionRoleDelete, Label: "删除自定义角色"},
		},
	},
	{Key: "/admin/audit-logs", Label: "审计日志", Group: "公司设置", ViewPermission: PermissionAuditRead},
	{
		Key: "/settings", Label: "平台集成", Group: "公司设置", ViewPermission: PermissionSettingsRead,
		Actions: []MenuAction{{Key: PermissionSettingsWrite, Label: "修改配置与轮换 Key"}},
	},
	// 刻意不含「平台管理 / 公司管理」（/admin/tenants）：它是超管专属，
	// 而超管始终全量、不靠角色授权；列进来只会误导（勾了也仍被 RequireSuperAdmin 拦）。
}

// AllMenus 返回对外输出的菜单目录，保证每项 actions 是非 nil 的空数组。
//
// Go 的 nil slice 序列化成 JSON null，前端 `menu.actions.length` 会直接抛错。
// 契约层负责把"没有操作权限"表达成 []，不让每个调用方各自补。
func AllMenus() []Menu {
	result := make([]Menu, len(Menus))
	copy(result, Menus)
	for i := range result {
		if result[i].Actions == nil {
			result[i].Actions = []MenuAction{}
		}
	}
	return result
}

// AllMenuKeys 返回完整菜单 key 列表。
//
// 唯一用途是"默认全量授权"：超管的 /me、新建角色的默认值都从这里取。
// 放在契约层是因为有两个 service 包都需要它，各自实现一份必然漂移。
func AllMenuKeys() []string {
	keys := make([]string, 0, len(Menus))
	for _, menu := range Menus {
		keys = append(keys, menu.Key)
	}
	return keys
}

// ViewPermissions 返回所有"查看类"权限点的集合（每个菜单的 ViewPermission 去重）。
//
// normalize 用它做一件事：把用户传来/历史存下的查看权限全部丢掉，
// 再按勾选的菜单重新授予。这样"有菜单才有查看权"是硬规则，
// 无法通过直接塞 role.read 之类的方式绕过菜单配置拿到能力。
func ViewPermissions() map[string]struct{} {
	result := make(map[string]struct{})
	for _, menu := range Menus {
		if menu.ViewPermission != "" {
			result[menu.ViewPermission] = struct{}{}
		}
	}
	return result
}

// IsValidMenu 判断一个 key 是否是合法的可授权菜单，写入角色时用它挡住拼错/伪造的值。
func IsValidMenu(key string) bool {
	for _, menu := range Menus {
		if menu.Key == key {
			return true
		}
	}
	return false
}

// RoleInput 是新建 / 编辑角色请求。
type RoleInput struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	// MenuKeys 是本次授权的菜单列表，全量覆盖（与 permissions 同语义）。
	MenuKeys []string `json:"menuKeys"`
}

// RoleList 是角色列表响应体（角色数量少，不分页）。
type RoleList struct {
	Items []Role `json:"items"`
}

// Scope 是一次请求能看到的数据边界，由身份层级推导，绝不信任请求参数。
// 三个字段的语义互斥且完备：
//   - All=true          → 超管，跨全部公司（仅平台管理接口可达，业务接口仍受 tenant 约束）
//   - TenantID 非空      → 公司边界；TeamIDs 为空表示本公司全部团队
//   - TeamIDs 非空       → 团队边界；范围是所有关联团队的并集
//
// 这是 P0 的唯一产出物：本阶段只负责把它推导出来存进 context，
// 仓储按 scope 过滤是 P1 的事，本轮不改变任何仓储签名。
// Scope 是一次请求能访问的数据边界，由身份层级推导，绝不信任请求参数。
// 三个字段的语义互斥且完备：
//   - All=true          → 超管，跨全部公司（仅平台管理接口可达，业务接口仍受 tenant 约束）
//   - TenantID 非空      → 公司边界；TeamIDs 为空表示本公司全部团队
//   - TeamIDs 非空       → 团队边界；范围是所有关联团队的并集
//
// TeamScoped 区分「TeamIDs 为空 = 平台管理员看全公司」与「member 没有团队所以什么都看不到」：
// 前者为 false（不加团队条件），后者为 true（即使 TeamIDs 为空也要加 1=0 失败关闭）。
type Scope struct {
	All        bool
	TenantID   string
	TeamIDs    []string
	TeamScoped bool
	// SelfUserID 是调用者自己的账号 id，只服务于 TeamScoped 身份（团队负责人）。
	//
	// 为什么需要它：团队负责人的团队归属在 team_admins，而账号列表按
	// 「绑定人员的 team_members」过滤——不加这一项，他会在账号管理页里看不到自己。
	// 它不是"额外放权"，只是一个自我可见的例外；能操作自己的写入仍由
	// service/user.ensureInScope 明确放行。
	SelfUserID string
}

// Identity 是鉴权中间件解析出的「当前调用者」，只含判定所需的最小字段。
// 放在 contract 里是因为 service/login 产出它、middleware/login 消费它，两边不该互相 import。
type Identity struct {
	UserID   string
	Username string
	// AccountLevel 是库里的原始层级；判定时一律走 Level()，它负责过渡期布尔折算。
	AccountLevel string
	// TenantID 是账号归属公司；超管为空。
	TenantID     string
	IsSuperAdmin bool
	Mode         SessionMode
}

// Level 返回该身份的有效管理层级。
// 唯一读取入口：新代码不要再直接比较 IsSuperAdmin 或裸读 AccountLevel，
// 否则过渡期布尔与新枚举会各自得出不同结论。
func (i Identity) Level() string {
	return EffectiveAccountLevel(i.AccountLevel, i.IsSuperAdmin)
}

// User 是账号视图：**不含任何密码字段**。
// User 是账号管理视图里的一条记录。
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	// Mode 是账号归属的模式：创建它的人用的是哪个登录模式，它就属于哪个模式。
	// 登录时已经选定模式，所以这里只做展示（列表按模式筛选、删除按模式匹配），不接受修改。
	Mode         SessionMode `json:"mode"`
	PersonID     string      `json:"personId"`
	PersonName   string      `json:"personName"`
	// RoleKey / RoleName 是账号对应人员的角色，随账号一起返回，省得账号列表为显示角色名再拉人员列表。
	// 角色的真相在 people.role_key：账号只是"以哪个人的身份登录"，它自己没有角色。
	RoleKey      string `json:"roleKey"`
	RoleName     string `json:"roleName"`
	IsSuperAdmin bool   `json:"isSuperAdmin"`
	Status       string `json:"status"`
	LastLoginAt  string `json:"lastLoginAt"`
	LastLoginIP  string `json:"lastLoginIp"`
	CreatedAt    string `json:"createdAt"`
	// AccountLevel 是管理身份层级，与 RoleKey（业务角色）正交。
	AccountLevel string `json:"accountLevel"`
	// TenantID 是账号归属公司；超管为空。TeamIDs 只对 team_admin 有意义，是它可管的团队。
	TenantID string   `json:"tenantId"`
	TeamIDs  []string `json:"teamIds"`
}

// UserInput 新建账号：账号名 + 初始密码 + 可选绑定人员 + 管理身份层级。
// **没有 mode 字段**：账号归属由创建者的登录模式决定，页面上的模式是只读展示。
//
// roleKey 写的是**绑定人员**的角色（people.role_key），不是账号自己的列：
// users 表永远没有角色字段，这样「账号页改角色」与「人员页改角色」写的是同一处。
// 传空表示沿用该人员现有角色；传非空则由 service 校验调用者有没有资格分配这个角色
// （super-admin 角色只有超管能分配），不合格直接 403/400。
//
// accountLevel 只能传比调用者低的层级，具体规则由 service/user 判定——
// 请求里传 super_admin 不会让任何人变成超管，那是越权尝试。
// teamIds 仅在 accountLevel=team_admin 时有意义，且必须落在调用者可见的团队内。
type UserInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	PersonID string `json:"personId"`
	// RoleKey 是随人员一起写入的角色标识；为空时沿用人员现有角色。
	RoleKey      string   `json:"roleKey"`
	IsSuperAdmin bool     `json:"isSuperAdmin"`
	AccountLevel string   `json:"accountLevel"`
	TeamIDs      []string `json:"teamIds"`
}

// TenantInput 新建公司：公司名 + 公司标识 + 平台管理员的初始账号与密码。
// 公司的平台管理员随公司在同一事务里创建，初始密码只在响应里返回一次。
type TenantInput struct {
	Name            string `json:"name"`
	Code            string `json:"code"`
	AdminUsername   string `json:"adminUsername"`
	AdminPassword   string `json:"adminPassword"`
}

// Tenant 是账号管理视角的一家公司（含其平台管理员账号名）。
type Tenant struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Code          string `json:"code"`
	Status        string `json:"status"`
	AdminUsername string `json:"adminUsername"`
	CreatedAt     string `json:"createdAt"`
}

// TenantResult 是新建公司的结果：公司信息 + 一次性明文密码。
type TenantResult struct {
	Tenant   Tenant `json:"tenant"`
	Password string `json:"password"`
}

// TenantList 是分页的公司列表。
type TenantList struct {
	Items []Tenant `json:"items"`
	Page
}

// UserUpdate 编辑账号：账号名与模式都不可改，只允许改绑定人员、角色与超管标记。
// RoleKey 写的是绑定人员的角色（people.role_key），不是账号上的字段——
// 这样"改账号的角色"与"改人员的角色"永远是同一件事，不会出现两处角色互相矛盾。
type UserUpdate struct {
	PersonID     string `json:"personId"`
	RoleKey      string `json:"roleKey"`
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

// PasswordChange 是「改自己的密码」的请求体：必须带旧密码，
// 因为带着有效令牌不等于本人操作（手机被人拿起就能改密码是不合格的）。
type PasswordChange struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// ApiKeyResult 承载轮换后的新 Key；明文只在这一个响应里出现。
type ApiKeyResult struct {
	ApiKey string `json:"apiKey"`
}

// AuditLog 是一条审计记录；detail 是 JSON 文本（写入方决定结构），前端格式化展示。
// Mode 标出这条日志产生于哪个模式的分区——单人、团队各写各的表，所以它属于记录本身，
// 而不是筛选条件（跨模式互不可见，查谁的表就是谁的模式）。
type AuditLog struct {
	ID         int64  `json:"id"`
	CreatedAt  string `json:"createdAt"`
	Mode       string `json:"mode"`
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
	// OwnerUserID 是上传者账号 id；system 表示由外部 API 或历史数据产生。
	OwnerUserID string `json:"ownerUserId"`
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
// 不带 owner 条件：单人隔离由「项目属于谁」在分区与项目表边界上完成，
// 列表只需按项目 + 环境（+平台）查，不必再叠一层 owner 过滤。
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

// MinAccountPasswordLen / MaxAccountPasswordLen 是账号密码长度的契约上限，账号创建与公司创建共用。
// 注意与上面的 MaxPasswordLen（128，webhook 等其它字段上限）区分。
const (
	MinAccountPasswordLen = 8
	MaxAccountPasswordLen = 64
)

// ValidatePassword 校验账号密码长度；返回 ValidationError 可直接上屏。
// 放在 contract 是因为账号管理与公司管理都要用它，规则必须只有一份。
func ValidatePassword(password string) error {
	if n := utf8.RuneCountInString(password); n < MinAccountPasswordLen || n > MaxAccountPasswordLen {
		return &ValidationError{Message: "密码长度需在 8-64 位之间"}
	}
	return nil
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

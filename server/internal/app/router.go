// Package app 负责路由装配与静态托管。
// 职责：注册登录接口与 /api/v1 业务接口、按「公开 / 需登录」分组、托管 embed 静态资源、
//
//	实现 SPA fallback、挂载错误中间件。
//
// 输入：已构造好的 handler 与前端静态 FS（由 main.go 注入）。
// 输出：可直接交给 http.Server 的 http.Handler。
// 边界：不做业务判断、不做角色权限（只有「已登录 / 未登录」两种）；
//
//	中间件顺序为「API 路由 → /healthz → 静态 → SPA fallback → 错误中间件（最外层，最后挂载）」。
package app

import (
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	audithandler "dandelion/server/internal/handler/audit"
	"dandelion/server/internal/handler/login"
	"dandelion/server/internal/handler/openapi"
	peoplehandler "dandelion/server/internal/handler/people"
	projecthandler "dandelion/server/internal/handler/project"
	rolehandler "dandelion/server/internal/handler/role"
	settingshandler "dandelion/server/internal/handler/settings"
	teamshandler "dandelion/server/internal/handler/teams"
	tenanthandler "dandelion/server/internal/handler/tenant"
	userhandler "dandelion/server/internal/handler/user"
	"dandelion/server/internal/middleware"
	"dandelion/server/internal/middleware/apikey"
	authzmw "dandelion/server/internal/middleware/authz"
	loginmw "dandelion/server/internal/middleware/login"
	authzsvc "dandelion/server/internal/service/authz"
)

// Deps 是装配层注入的全部依赖。
type Deps struct {
	Builds *handler.BuildHandler
	// Open 是外部系统的一步式上传；Auth 是登录接口，Verify 是它的令牌校验函数。
	Open     *openapi.Handler
	Login    *login.Handler
	Projects *projecthandler.Handler
	People   *peoplehandler.Handler
	Users    *userhandler.Handler
	Audit    *audithandler.Handler
	Roles    *rolehandler.Handler
	// Authz 提供权限判定，路由层用它给每个接口挂上所需权限点
	Authz    *authzsvc.Service
	Teams    *teamshandler.Handler
	Tenants  *tenanthandler.Handler
	Settings *settingshandler.Handler
	Verify   loginmw.Verifier
	// APIKey 每次请求查一次库反查归属公司（空串=平台兜底 Key），改配置无需重启。
	APIKey   apikey.KeyResolver
	Health   *handler.HealthHandler
	StaticFS fs.FS
}

// allowedMethods 是「路由 pattern → 允许方法」的静态映射。
// chi 的默认 405 处理器会自动回填 Allow 头；一旦自定义 MethodNotAllowed 就必须自己补，
// 否则响应缺少 Allow，与契约 §6.1 的「405 + Allow + JSON 外壳」不一致。
var allowedMethods = map[string]string{
	contract.RouteLogin:               "POST",
	contract.RouteMe:                  "GET",
	contract.RouteMePassword:          "PUT",
	contract.RouteProjects:            "GET, POST",
	contract.RouteProjectByKey:        "GET, PUT",
	contract.RoutePeople:              "GET, POST",
	contract.RoutePersonByID:          "GET, PUT",
	contract.RouteTeams:               "GET, POST",
	contract.RouteTeamByID:            "GET, PUT",
	contract.RouteOpenBuilds:          "POST",
	contract.RouteOpenUploadTicket:    "POST",
	contract.RouteOpenStageFromR2:     "POST",
	contract.RouteSettings:            "GET, PUT",
	contract.RouteStorageUsage:        "GET",
	contract.RouteWebhookTest:         "POST",
	contract.RouteBuilds:              "GET, POST",
	contract.RouteStage:               "POST",
	contract.RouteUploadTicket:        "POST",
	contract.RouteStageFromR2:         "POST",
	contract.RouteUploadAbort:         "POST",
	contract.RouteBuildByID:           "GET, PUT, DELETE",
	contract.RouteBuildVersions:       "GET",
	contract.RouteBuildFile:           "GET",
	contract.RouteBuildSyncToR2:       "POST",
	contract.RouteBuildLocalPull:      "POST",
	contract.RouteBuildSyncProgress:   "GET",
	contract.RouteSharedBuild:         "GET",
	contract.RouteSharedBuildVersions: "GET",
	contract.RouteSharedBuildFile:     "GET",
	contract.RouteSharedDownloadCount: "POST",
}

// allowedMethodsFor 返回该请求应回填的 Allow 值。
// 优先用 chi 已匹配的 pattern；方法不匹配时 chi 不会设置 pattern，退回按 URL 路径推导。
func allowedMethodsFor(req *http.Request) string {
	if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern != "" {
		if allow, ok := allowedMethods[pattern]; ok {
			return allow
		}
	}
	path := strings.TrimSuffix(req.URL.Path, "/")
	switch {
	case path == contract.RouteLogin, path == contract.RouteOpenBuilds, path == contract.RouteWebhookTest:
		return "POST"
	// /me/password 必须排在 /me 之前判定：下面那条是精确相等，顺序其实无关，
	// 但放这里能让人一眼看出 /me 下有两个方法不同的接口。
	case path == contract.RouteMePassword:
		return "PUT"
	case path == contract.RouteMe:
		return "GET"
	case strings.HasPrefix(path, contract.RouteAdminUsers):
		return "GET, POST, PUT, DELETE"
	case path == contract.RouteProjects, path == contract.RoutePeople, path == contract.RouteTeams, path == contract.RouteRoles:
		return "GET, POST"
	case path == contract.RoutePermissions, path == contract.RouteMenus:
		return "GET"
	case strings.HasPrefix(path, contract.RouteProjects+"/"), strings.HasPrefix(path, contract.RoutePeople+"/"):
		return "GET, PUT"
	case strings.HasPrefix(path, contract.RouteTeams+"/"), strings.HasPrefix(path, contract.RouteRoles+"/"):
		return "GET, PUT"
	case path == contract.RouteSettings:
		return "GET, PUT"
	case path == contract.RouteStorageUsage:
		return "GET"
	case path == contract.RouteBuilds:
		return "GET, POST"
	case path == contract.RouteOpenBuilds, path == contract.RouteOpenUploadTicket,
		path == contract.RouteOpenStageFromR2:
		return "POST"
	case path == contract.RouteStage, path == contract.RouteUploadTicket,
		path == contract.RouteStageFromR2, path == contract.RouteUploadAbort:
		return "POST"
	case strings.HasSuffix(path, "/sync-to-r2"), strings.HasSuffix(path, "/local-pull"),
		strings.HasSuffix(path, "/settings/r2"), strings.HasSuffix(path, "/settings/r2/test"):
		// 必须排在下面的通用前缀判断之前：重推是写操作，
		// 落到默认的 GET 会让 405 响应给出错误的 Allow 头。
		return "POST"
	case strings.HasSuffix(path, "/sync-progress"):
		return "GET"
	case strings.HasSuffix(path, "/download-count"):
		return "POST"
	case strings.HasPrefix(path, contract.RouteBuilds+"/"):
		if strings.Count(strings.TrimPrefix(path, contract.RouteBuilds+"/"), "/") == 0 {
			return "GET, PUT, DELETE"
		}
		return "GET"
	default:
		return "GET"
	}
}

// NewRouter 组装完整 HTTP 处理链。
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Route(contract.APIPrefix, func(api chi.Router) {
		api.Post("/auth/login", middleware.Wrap(d.Login.Login))

		// 分享无需登录；详情、同应用版本与文件都必须逐次校验来源包的分享凭证。
		api.Get("/public/builds/{id}", middleware.Wrap(d.Builds.SharedGet))
		api.Get("/public/builds/{id}/versions", middleware.Wrap(d.Builds.SharedVersions))
		api.Get("/public/builds/{id}/file", middleware.Wrap(d.Builds.SharedDownloadFile))
		api.Post("/public/builds/{id}/download-count", middleware.Wrap(d.Builds.SharedDownloadCount))

		// 外部系统上传：X-API-Key 鉴权，与管理端登录态互不影响。
		// 一步式 multipart 保留给小包；大包走取票据 → 直传 R2 → 提交元信息三步。
		open := api.With(apikey.Require(d.APIKey))
		open.Post("/open/builds", middleware.Wrap(d.Open.Upload))
		open.Post("/open/builds/upload-ticket", middleware.Wrap(d.Open.UploadTicket))
		open.Post("/open/builds/stage-from-r2", middleware.Wrap(d.Open.StageFromR2))

		// 管理动作（上传 / 发布 / 列表 / 系统设置）需要登录；分组只加中间件，不改路由前缀。
		api.Group(func(private chi.Router) {
			private.Use(loginmw.RequireAuth(d.Verify))
			// 身份解析完立刻推导 scope 存进 ctx，后面的 handler 只读不算。
			// 挂在 RequireAuth 之后：没有可信身份时不做任何范围推导。
			private.Use(loginmw.InjectScope(d.Authz))
			private.Get("/builds/{id}", middleware.Wrap(d.Builds.Get))
			// 编辑与删除按包的真实项目检查权限，不信任 query 中的 project。
			private.Put("/builds/{id}", middleware.Wrap(d.Builds.Update))
			private.Delete("/builds/{id}", middleware.Wrap(d.Builds.Delete))
			private.Get("/builds/{id}/versions", middleware.Wrap(d.Builds.Versions))
			private.Get("/builds/{id}/file", middleware.Wrap(d.Builds.DownloadFile))
			// 重推已淘汰的包：POST 因为会真实上传对象并改状态，不可被缓存或预取。
			private.Post("/builds/{id}/sync-to-r2", middleware.Wrap(d.Builds.SyncToR2))
			// 反向操作：把 R2 对象重新拉回本机，仅用于失败后主动重试。
			private.Post("/builds/{id}/local-pull", middleware.Wrap(d.Builds.RetryLocalPull))
			// 查询重推进度：GET 只读，供包卡片画进度条与成功/失败状态。
			private.Get("/builds/{id}/sync-progress", middleware.Wrap(d.Builds.SyncProgress))
			// 发布/暂存：提交时项目在请求体里，由 handler 内部判定 build.write
			private.Post("/builds/stage", middleware.Wrap(d.Builds.Stage))
			// 大包直传：先取票据让浏览器直接 PUT 到 R2，避开 Tunnel 的 100MB 请求体上限。
			private.Post("/builds/upload-ticket", middleware.Wrap(d.Builds.UploadTicket))
			private.Post("/builds/stage-from-r2", middleware.Wrap(d.Builds.StageFromR2))
			private.Post("/builds/upload-abort", middleware.Wrap(d.Builds.UploadAbort))
			private.Post("/builds", middleware.Wrap(d.Builds.Commit))
			private.With(authzmw.RequireProjectPermission(d.Authz, "build.read")).Get("/builds", middleware.Wrap(d.Builds.List))
			// 存储用量：租户一律取自登录态，前端无法通过参数查别人的用量。
			private.Get("/storage/usage", middleware.Wrap(d.Builds.StorageUsage))

			private.Get("/me", middleware.Wrap(d.People.Me))
			// 改自己的密码只要求登录态：任何账号都该能换自己的密码，与是否超管无关。
			private.Put("/me/password", middleware.Wrap(d.Users.ChangePassword))
			// 项目读取是单人模式的基础设施：没有项目列表就无法进入自己的应用包页。
			// 菜单目录对所有登录用户开放：前端需要它把角色授权的菜单投影到侧栏。
			// 目录本身是平台级静态数据，不含任何公司信息，无需 scope 过滤。
			private.Get("/menus", middleware.Wrap(d.Roles.Menus))
			private.With(authzmw.RequirePermission(d.Authz, "project.read")).Get("/projects", middleware.Wrap(d.Projects.List))
			private.With(authzmw.RequirePermission(d.Authz, "project.read")).Get("/projects/{key}", middleware.Wrap(d.Projects.Get))
			// 项目写操作对两种模式都开放：单人模式自建项目是上传包的前提，
			// 不需要管理员先建好；数据落在各自的分区，越权由分区与 owner 过滤兜住。
			private.With(authzmw.RequirePermission(d.Authz, "project.write")).Post("/projects", middleware.Wrap(d.Projects.Create))
			private.With(authzmw.RequirePermission(d.Authz, "project.write")).Put("/projects/{key}", middleware.Wrap(d.Projects.Update))
			private.With(authzmw.RequirePermission(d.Authz, "project.delete")).Delete("/projects/{key}", middleware.Wrap(d.Projects.Delete))
			private.Group(func(team chi.Router) {
				team.Use(authzmw.RequireTeamMode)
				team.With(authzmw.RequirePermission(d.Authz, "directory.read")).Get("/people", middleware.Wrap(d.People.List))
				team.With(authzmw.RequirePermission(d.Authz, "directory.write")).Post("/people", middleware.Wrap(d.People.Create))
				team.With(authzmw.RequirePermission(d.Authz, "directory.read")).Get("/people/{id}", middleware.Wrap(d.People.Get))
				team.With(authzmw.RequirePermission(d.Authz, "directory.write")).Put("/people/{id}", middleware.Wrap(d.People.Update))
				team.With(authzmw.RequirePermission(d.Authz, contract.PermissionDirectoryDelete)).Delete("/people/{id}", middleware.Wrap(d.People.Delete))
				team.With(authzmw.RequirePermission(d.Authz, "directory.read")).Get("/teams", middleware.Wrap(d.Teams.List))
				team.With(authzmw.RequirePermission(d.Authz, "directory.write")).Post("/teams", middleware.Wrap(d.Teams.Create))
				team.With(authzmw.RequirePermission(d.Authz, "directory.read")).Get("/teams/{id}", middleware.Wrap(d.Teams.Get))
				team.With(authzmw.RequirePermission(d.Authz, "directory.write")).Put("/teams/{id}", middleware.Wrap(d.Teams.Update))
				team.With(authzmw.RequirePermission(d.Authz, contract.PermissionDirectoryDelete)).Delete("/teams/{id}", middleware.Wrap(d.Teams.Delete))
				// 角色管理不在这里：它只对超级管理员开放，见下面的 admin 专区。
			})
		})

		// 账号管理拆成两组：**看**与**改**是两个不同量级的权力。
		//
		//   看：account.read，可由角色配置——普通账号配了这个就能看本公司账号列表。
		//   改：RequireMinLevel(platform_admin)，**不受角色配置影响**。
		//
		// 为什么写操作不能用权限点：新建账号要定归属、改账号要动角色与状态，
		// 一旦能靠角色勾选获得，任意角色都能给自己开号、改别人的团队归属，
		// 等于绕过公司边界。这类"平台治理动作"只给身份，不给配置。
		api.Group(func(accounts chi.Router) {
			accounts.Use(loginmw.RequireAuth(d.Verify))
			accounts.Use(loginmw.InjectScope(d.Authz))
			accounts.Use(authzmw.RequireTeamMode)

			// 只读：角色可配。
			accounts.With(authzmw.RequirePermission(d.Authz, contract.PermissionAccountRead)).
				Get("/admin/users", middleware.Wrap(d.Users.List))
			accounts.With(authzmw.RequirePermission(d.Authz, contract.PermissionAccountRead)).
				Get("/admin/users/{id}", middleware.Wrap(d.Users.Get))

			// 写操作：平台管理员及以上，不经过权限点。
			write := accounts.With(loginmw.RequireMinLevel(contract.AccountLevelPlatformAdmin))
			write.Post("/admin/users", middleware.Wrap(d.Users.Create))
			write.Put("/admin/users/{id}", middleware.Wrap(d.Users.Update))
			write.Delete("/admin/users/{id}", middleware.Wrap(d.Users.Delete))
			write.Post("/admin/users/{id}/reset-password", middleware.Wrap(d.Users.ResetPassword))
			write.Post("/admin/users/{id}/status", middleware.Wrap(d.Users.SetStatus))
		})

		// 超级管理员专区：RequireAuth 之后再叠一层 RequireSuperAdmin，
		// 前端隐藏菜单只是体验，这里的 403 才是真正的边界。
		api.Group(func(admin chi.Router) {
			admin.Use(loginmw.RequireAuth(d.Verify))
			admin.Use(loginmw.RequireSuperAdmin)
			// 公司管理：建公司与它的平台管理员、列出公司。仅超管（admin 组本身已经是超管门槛）。
			admin.Get("/platform/tenants", middleware.Wrap(d.Tenants.List))
			admin.Post("/platform/tenants", middleware.Wrap(d.Tenants.Create))
			// 启停与删除公司：比"建"更危险（影响面是整家公司），因此同样只在超管专区。
			admin.Post("/platform/tenants/{id}/status", middleware.Wrap(d.Tenants.SetStatus))
			admin.Delete("/platform/tenants/{id}", middleware.Wrap(d.Tenants.Delete))
		})

		// 审计日志：由权限点 audit.read 控制，限本公司；超管（All）看全部平台审计。
		//
		// 为什么必须单独成组而不是留在超管专区：
		//   - 需要 InjectScope 才知道"查哪家公司"——超管专区没有 scope 注入；
		//   - 下放给平台管理员后，他能查本公司谁改了什么，这才是审计的实际用途。
		//
		// 为什么必须叠 RequireTeamMode：solo 分区的审计**不带租户过滤**
		// （个人工作台没有公司概念，见 repository/audit.List 的说明）。
		// 若允许在单人模式查询，一个平台管理员用 solo 登录就能看到全平台单人用户的审计记录——
		// 那是一条跨公司泄漏。审计属于管理能力，只在团队视图存在。
		api.Group(func(audit chi.Router) {
			audit.Use(loginmw.RequireAuth(d.Verify))
			audit.Use(loginmw.InjectScope(d.Authz))
			audit.Use(authzmw.RequireTeamMode)
			audit.Use(authzmw.RequirePermission(d.Authz, contract.PermissionAuditRead))
			audit.Get("/admin/audit-logs", middleware.Wrap(d.Audit.List))
		})

		// 平台集成配置：由权限点控制，限本公司（超管落到平台兜底行）。
		// 读与写分开：settings.read 看配置，settings.write 改配置/轮换 Key。
		// 必须单独成组：需要 InjectScope 定位"哪家公司的那一行"。
		api.Group(func(settings chi.Router) {
			settings.Use(loginmw.RequireAuth(d.Verify))
			settings.Use(loginmw.InjectScope(d.Authz))
			settings.With(authzmw.RequirePermission(d.Authz, contract.PermissionSettingsRead)).
				Get("/settings", middleware.Wrap(d.Settings.Get))
			settings.With(authzmw.RequirePermission(d.Authz, contract.PermissionSettingsWrite)).
				Put("/settings", middleware.Wrap(d.Settings.Update))
			settings.With(authzmw.RequirePermission(d.Authz, contract.PermissionSettingsWrite)).
				Post("/settings/webhook/test", middleware.Wrap(d.Settings.Test))
			settings.With(authzmw.RequirePermission(d.Authz, contract.PermissionSettingsWrite)).
				Post("/settings/api-key/rotate", middleware.Wrap(d.Settings.RotateAPIKey))

			// R2 对象存储配置：与 Webhook 同为平台集成的一部分，权限点一致。
			// 测试接口挂写权限而非读权限——它会真实发起网络请求，
			// 不该让只有查看权的账号反复触发。
			settings.With(authzmw.RequirePermission(d.Authz, contract.PermissionSettingsRead)).
				Get("/settings/r2", middleware.Wrap(d.Settings.GetR2))
			settings.With(authzmw.RequirePermission(d.Authz, contract.PermissionSettingsWrite)).
				Put("/settings/r2", middleware.Wrap(d.Settings.UpdateR2))
			settings.With(authzmw.RequirePermission(d.Authz, contract.PermissionSettingsWrite)).
				Post("/settings/r2/test", middleware.Wrap(d.Settings.TestR2))
		})

		// 角色管理：三个权限点分别守读、写、删。
		//
		// 产品决策：普通账号凭角色勾选就能管理角色定义，不再限 platform_admin。
		// 可见范围仍由 scope 决定：公司账号看「内置副本 + 本公司自定义」，
		// 超管（All）看全部。改内置角色只影响本公司的副本。
		api.Group(func(roles chi.Router) {
			roles.Use(loginmw.RequireAuth(d.Verify))
			roles.Use(loginmw.InjectScope(d.Authz))
			roles.Use(authzmw.RequireTeamMode)
			// 权限目录本身只读且无公司信息，任何登录用户都能取（前端角色表单要用）。
			roles.Get("/permissions", middleware.Wrap(d.Roles.Permissions))
			roles.With(authzmw.RequirePermission(d.Authz, contract.PermissionRoleRead)).
				Get("/roles", middleware.Wrap(d.Roles.List))
			roles.With(authzmw.RequirePermission(d.Authz, contract.PermissionRoleRead)).
				Get("/roles/{id}", middleware.Wrap(d.Roles.Get))
			roles.With(authzmw.RequirePermission(d.Authz, contract.PermissionRoleWrite)).
				Post("/roles", middleware.Wrap(d.Roles.Create))
			roles.With(authzmw.RequirePermission(d.Authz, contract.PermissionRoleWrite)).
				Put("/roles/{id}", middleware.Wrap(d.Roles.Update))
			roles.With(authzmw.RequirePermission(d.Authz, contract.PermissionRoleDelete)).
				Delete("/roles/{id}", middleware.Wrap(d.Roles.Delete))
		})
	})
	r.Get(contract.RouteHealth, middleware.Wrap(d.Health.Health))

	// 方法不匹配统一回 405 + Allow + JSON 外壳。
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Allow", allowedMethodsFor(req))
		handler.WriteError(w, req, handler.MethodNotAllowed("请求方法不支持"))
	})
	// 未命中任何路由（含 /、/upload、/d/{id}）交给 SPA fallback：
	// 不用 "/*" 通配，避免它抢走「路径存在但方法不允许」的请求、把 405 变成 404。
	r.NotFound(spaHandler(d.StaticFS))

	// 错误中间件最后挂载：作为最外层包装，兜住全部 handler 的 panic。
	return middleware.ErrorHandler(r)
}

// spaHandler 命中静态文件就直出，否则把 GET 一律回 index.html。
// /d/{id} 直接打开或刷新能正常显示，靠的就是这里；容器阶段 Nginx 的 try_files 承担同样职责。
func spaHandler(static fs.FS) http.HandlerFunc {
	indexHTML, err := fs.ReadFile(static, "index.html")
	if err != nil {
		log.Printf("读取内嵌 index.html 失败，SPA fallback 将返回提示页: %v", err)
		indexHTML = []byte("<!doctype html><html lang=\"zh-CN\"><body>前端产物缺失，请执行 make build</body></html>")
	}
	fileServer := http.FileServer(http.FS(static))

	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, contract.APIPrefix) || r.URL.Path == contract.RouteHealth {
			handler.WriteError(w, r, handler.NotFound("接口不存在"))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET")
			handler.WriteError(w, r, handler.MethodNotAllowed("请求方法不支持"))
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "." {
			if info, statErr := fs.Stat(static, name); statErr == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(indexHTML)
		}
	}
}

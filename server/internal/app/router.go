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
	rdchandler "dandelion/server/internal/handler/rdc"
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
	// RDC 是研发中心需求池的入口，只在 team 专区注册（solo 会话被中间件挡在门外）。
	RDC    *rdchandler.Handler
	Verify loginmw.Verifier
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
	contract.RouteSettings:            "GET, PUT",
	contract.RouteWebhookTest:         "POST",
	contract.RouteBuilds:              "GET, POST",
	contract.RouteStage:               "POST",
	contract.RouteBuildByID:           "GET",
	contract.RouteBuildVersions:       "GET",
	contract.RouteBuildFile:           "GET",
	contract.RouteRDCRequirements:     "GET, POST",
	contract.RouteRDCRequirementByKey: "GET",
	// 需求图片访问是受保护的只读接口，走与需求详情相同的读权限点。
	contract.RouteRDCRequirementAttachment: "GET",
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
	case path == contract.RouteBuilds:
		return "GET, POST"
	case path == contract.RouteStage:
		return "POST"
	case path == contract.RouteRDCRequirements:
		return "GET, POST"
	case strings.HasPrefix(path, strings.TrimSuffix(contract.RouteRDCRequirementAttachment, "{id}")):
		return "GET"
	case strings.HasPrefix(path, contract.RouteRDCRequirements+"/"):
		// /rdc/requirements/{reqKey} 只支持 GET；带斜杠的子路径一律按详情处理。
		return "GET"
	case strings.HasPrefix(path, contract.RouteBuilds+"/"):
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

		// 下载链路必须公开：扫码的人没有账号，点开链接就要能看详情、能下载。
		api.Get("/builds/{id}", middleware.Wrap(d.Builds.Get))
		api.Get("/builds/{id}/versions", middleware.Wrap(d.Builds.Versions))
		api.Get("/builds/{id}/file", middleware.Wrap(d.Builds.DownloadFile))

		// 外部系统上传：X-API-Key 鉴权，与管理端登录态互不影响。
		api.With(apikey.Require(d.APIKey)).Post("/open/builds", middleware.Wrap(d.Open.Upload))

		// 管理动作（上传 / 发布 / 列表 / 系统设置）需要登录；分组只加中间件，不改路由前缀。
		api.Group(func(private chi.Router) {
			private.Use(loginmw.RequireAuth(d.Verify))
			// 身份解析完立刻推导 scope 存进 ctx，后面的 handler 只读不算。
			// 挂在 RequireAuth 之后：没有可信身份时不做任何范围推导。
			private.Use(loginmw.InjectScope(d.Authz))
			// 发布/暂存：提交时项目在请求体里，由 handler 内部判定 build.write
			private.Post("/builds/stage", middleware.Wrap(d.Builds.Stage))
			private.Post("/builds", middleware.Wrap(d.Builds.Commit))
			private.With(authzmw.RequireProjectPermission(d.Authz, "build.read")).Get("/builds", middleware.Wrap(d.Builds.List))

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

		// 研发中心需求池（仅 team）：RequireTeamMode 是真正的模式边界，
		// 读接口守 rdc.requirement.read、写接口守独立的 rdc.requirement.write。
		// 数据范围由 InjectScope 写入上下文，仓储只查 team 分区并按 scope 过滤。
		api.Group(func(rdc chi.Router) {
			rdc.Use(loginmw.RequireAuth(d.Verify))
			rdc.Use(loginmw.InjectScope(d.Authz))
			rdc.Use(authzmw.RequireTeamMode)
			rdc.With(authzmw.RequirePermission(d.Authz, contract.PermissionRDCRequirementRead)).
				Get("/rdc/requirements", middleware.Wrap(d.RDC.List))
			rdc.With(authzmw.RequirePermission(d.Authz, contract.PermissionRDCRequirementRead)).
				Get("/rdc/requirements/{reqKey}", middleware.Wrap(d.RDC.Get))
			// 图片读取仍守读权限点，不是公开接口；越界与不存在一律 404（service + 仓储兜底）。
			rdc.With(authzmw.RequirePermission(d.Authz, contract.PermissionRDCRequirementRead)).
				Get("/rdc/requirement-attachments/{id}", middleware.Wrap(d.RDC.Attachment))
			rdc.With(authzmw.RequirePermission(d.Authz, contract.PermissionRDCRequirementWrite)).
				Post("/rdc/requirements", middleware.Wrap(d.RDC.Create))
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(indexHTML)
		}
	}
}

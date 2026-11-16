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
	Settings *settingshandler.Handler
	Verify   loginmw.Verifier
	// APIKey 每次请求读一次系统设置里的 Key，改配置无需重启。
	APIKey   apikey.KeyProvider
	Health   *handler.HealthHandler
	StaticFS fs.FS
}

// allowedMethods 是「路由 pattern → 允许方法」的静态映射。
// chi 的默认 405 处理器会自动回填 Allow 头；一旦自定义 MethodNotAllowed 就必须自己补，
// 否则响应缺少 Allow，与契约 §6.1 的「405 + Allow + JSON 外壳」不一致。
var allowedMethods = map[string]string{
	contract.RouteLogin:        "POST",
	contract.RouteMe:           "GET",
	contract.RouteProjects:     "GET, POST",
	contract.RouteProjectByKey: "GET, PUT",
	contract.RoutePeople:       "GET, POST",
	contract.RoutePersonByID:   "GET, PUT",
	contract.RouteTeams:        "GET, POST",
	contract.RouteTeamByID:     "GET, PUT",
	contract.RouteOpenBuilds:   "POST",
	contract.RouteSettings:     "GET, PUT",
	contract.RouteWebhookTest:  "POST",
	contract.RouteBuilds:       "GET, POST",
	contract.RouteStage:        "POST",
	contract.RouteBuildByID:    "GET",
	contract.RouteBuildFile:    "GET",
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
	case path == contract.RouteMe:
		return "GET"
	case strings.HasPrefix(path, contract.RouteAdminUsers):
		return "GET, POST, PUT"
	case path == contract.RouteProjects, path == contract.RoutePeople, path == contract.RouteTeams, path == contract.RouteRoles:
		return "GET, POST"
	case path == contract.RoutePermissions:
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
		api.Get("/builds/{id}/file", middleware.Wrap(d.Builds.DownloadFile))

		// 外部系统上传：X-API-Key 鉴权，与管理端登录态互不影响。
		api.With(apikey.Require(d.APIKey)).Post("/open/builds", middleware.Wrap(d.Open.Upload))

		// 管理动作（上传 / 发布 / 列表 / 系统设置）需要登录；分组只加中间件，不改路由前缀。
		api.Group(func(private chi.Router) {
			private.Use(loginmw.RequireAuth(d.Verify))
			// 发布/暂存：提交时项目在请求体里，由 handler 内部判定 build.write
			private.Post("/builds/stage", middleware.Wrap(d.Builds.Stage))
			private.Post("/builds", middleware.Wrap(d.Builds.Commit))
			private.With(authzmw.RequirePermission(d.Authz, "build.read")).Get("/builds", middleware.Wrap(d.Builds.List))

			private.Get("/me", middleware.Wrap(d.People.Me))
			private.With(authzmw.RequirePermission(d.Authz, "project.read")).Get("/projects", middleware.Wrap(d.Projects.List))
			private.With(authzmw.RequirePermission(d.Authz, "project.write")).Post("/projects", middleware.Wrap(d.Projects.Create))
			private.With(authzmw.RequirePermission(d.Authz, "project.read")).Get("/projects/{key}", middleware.Wrap(d.Projects.Get))
			private.With(authzmw.RequirePermission(d.Authz, "project.write")).Put("/projects/{key}", middleware.Wrap(d.Projects.Update))
			private.With(authzmw.RequirePermission(d.Authz, "directory.read")).Get("/people", middleware.Wrap(d.People.List))
			private.With(authzmw.RequirePermission(d.Authz, "directory.write")).Post("/people", middleware.Wrap(d.People.Create))
			private.With(authzmw.RequirePermission(d.Authz, "directory.read")).Get("/people/{id}", middleware.Wrap(d.People.Get))
			private.With(authzmw.RequirePermission(d.Authz, "directory.write")).Put("/people/{id}", middleware.Wrap(d.People.Update))
			private.With(authzmw.RequirePermission(d.Authz, "directory.read")).Get("/teams", middleware.Wrap(d.Teams.List))
			private.With(authzmw.RequirePermission(d.Authz, "directory.write")).Post("/teams", middleware.Wrap(d.Teams.Create))
			private.With(authzmw.RequirePermission(d.Authz, "directory.read")).Get("/teams/{id}", middleware.Wrap(d.Teams.Get))
			private.With(authzmw.RequirePermission(d.Authz, "directory.write")).Put("/teams/{id}", middleware.Wrap(d.Teams.Update))
			private.Get("/permissions", middleware.Wrap(d.Roles.Permissions))
			private.With(authzmw.RequirePermission(d.Authz, "role.read")).Get("/roles", middleware.Wrap(d.Roles.List))
			private.With(authzmw.RequirePermission(d.Authz, "role.read")).Get("/roles/{key}", middleware.Wrap(d.Roles.Get))
		})

		// 超级管理员专区：RequireAuth 之后再叠一层 RequireSuperAdmin，
		// 前端隐藏菜单只是体验，这里的 403 才是真正的边界。
		api.Group(func(admin chi.Router) {
			admin.Use(loginmw.RequireAuth(d.Verify))
			admin.Use(loginmw.RequireSuperAdmin)
			admin.Get("/admin/users", middleware.Wrap(d.Users.List))
			admin.Post("/admin/users", middleware.Wrap(d.Users.Create))
			admin.Put("/admin/users/{id}", middleware.Wrap(d.Users.Update))
			admin.Post("/admin/users/{id}/reset-password", middleware.Wrap(d.Users.ResetPassword))
			admin.Post("/admin/users/{id}/status", middleware.Wrap(d.Users.SetStatus))
			admin.Get("/admin/audit-logs", middleware.Wrap(d.Audit.List))
			// 角色的写操作属于"改权限"，只对超管开放
			admin.Post("/roles", middleware.Wrap(d.Roles.Create))
			admin.Put("/roles/{key}", middleware.Wrap(d.Roles.Update))

			// 平台设置与密钥：路径仍是 /settings，但只对超管开放。
			admin.Get("/settings", middleware.Wrap(d.Settings.Get))
			admin.Put("/settings", middleware.Wrap(d.Settings.Update))
			admin.Post("/settings/webhook/test", middleware.Wrap(d.Settings.Test))
			admin.Post("/settings/api-key/rotate", middleware.Wrap(d.Settings.RotateAPIKey))
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

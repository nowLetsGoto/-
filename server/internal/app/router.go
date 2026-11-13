// Package app 负责路由装配与静态托管。
// 职责：注册 /api/v1 五个接口与独立 /healthz、托管 embed 静态资源、实现 SPA fallback、挂载错误中间件。
// 输入：已构造好的 handler 与前端静态 FS（由 main.go 注入）。
// 输出：可直接交给 http.Server 的 http.Handler。
// 边界：不做业务判断、不注册任何鉴权 / 板块 / 推送 / 解析预览路由；
//       中间件顺序为「API 路由 → /healthz → 静态 → SPA fallback → 错误中间件（最外层，最后挂载）」。
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
	"dandelion/server/internal/middleware"
)

// Deps 是装配层注入的全部依赖。
type Deps struct {
	Builds   *handler.BuildHandler
	Health   *handler.HealthHandler
	StaticFS fs.FS
}

// NewRouter 组装完整 HTTP 处理链。
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Route(contract.APIPrefix, func(api chi.Router) {
		api.Post("/builds/stage", middleware.Wrap(d.Builds.Stage))
		api.Post("/builds", middleware.Wrap(d.Builds.Commit))
		api.Get("/builds", middleware.Wrap(d.Builds.List))
		api.Get("/builds/{id}", middleware.Wrap(d.Builds.Get))
		api.Get("/builds/{id}/file", middleware.Wrap(d.Builds.DownloadFile))
	})
	r.Get(contract.RouteHealth, middleware.Wrap(d.Health.Health))

	// 方法不匹配与未知路径都统一走错误格式化；未知 API 绝不回 index.html。
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		handler.WriteError(w, req, handler.MethodNotAllowed("请求方法不支持"))
	})
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		handler.WriteError(w, req, handler.NotFound("接口不存在"))
	})
	r.HandleFunc("/*", spaHandler(d.StaticFS))

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

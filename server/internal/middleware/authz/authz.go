// Package authz · 权限中间件。
// 职责：在路由层要求调用者具备某个权限点，不满足即 403。
// 输入：authz 服务、权限点字符串；输出：可挂到 chi 的中间件。
// 边界：**必须挂在 RequireAuth 之后**（依赖上下文里的身份）；
//       项目上下文取自 `?project=`，没有则按平台级判定——
//       "项目在请求体里"的接口（如提交发布）请在 handler 里用 authz.Service.Check。
package authz

import (
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	authzsvc "dandelion/server/internal/service/authz"
)

// RequirePermission 要求调用者具备 permission；项目上下文从查询参数 project 读取。
func RequirePermission(svc *authzsvc.Service, permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := authctx.From(r.Context())
			permissions, err := svc.Permissions(r.Context(), identity, r.URL.Query().Get("project"))
			if err != nil {
				handler.WriteError(w, r, err)
				return
			}
			if _, ok := permissions[permission]; !ok {
				handler.WriteError(w, r, contract.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

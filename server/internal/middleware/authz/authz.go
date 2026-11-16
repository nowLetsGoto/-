// Package authz · 权限中间件。
// 职责：在路由层要求调用者具备某个权限点，不满足即 403。
// 输入：authz 服务、权限点字符串；输出：可挂到 chi 的中间件。
// 边界：**必须挂在 RequireAuth 之后**（依赖上下文里的身份）。
// 平台权限刻意忽略 `?project=`：否则普通账号能借一个高权限项目绕过平台边界。
// 只有项目内接口用 RequireProjectPermission；"项目在请求体里"提交发布请在 handler 里用 authz.Service.Check。
package authz

import (
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	authzsvc "dandelion/server/internal/service/authz"
)

// RequirePermission 只判定平台权限，刻意忽略客户端的 project 参数。
// 否则普通账号能给 /people?project=高权限项目 借用项目角色，绕过平台权限。
// 真正的项目内查询必须显式挂 RequireProjectPermission。
func RequirePermission(svc *authzsvc.Service, permission string) func(http.Handler) http.Handler {
	return require(svc, permission, false)
}

// RequireProjectPermission 只供项目内接口使用；查询参数 project 是该接口的实际业务上下文。
func RequireProjectPermission(svc *authzsvc.Service, permission string) func(http.Handler) http.Handler {
	return require(svc, permission, true)
}

func require(svc *authzsvc.Service, permission string, projectScoped bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := authctx.From(r.Context())
			projectKey := ""
			if projectScoped {
				projectKey = r.URL.Query().Get("project")
			}
			permissions, err := svc.Permissions(r.Context(), identity, projectKey)
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

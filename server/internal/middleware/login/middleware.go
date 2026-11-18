// Package login · 鉴权中间件。
// 职责：校验 Bearer 令牌、把调用者身份与数据范围写进请求上下文、提供管理层级守门器。
// 输入：Verifier（由 service/login 提供）、authz 服务（用于 ScopeOf）；输出：包装后的 http.Handler。
// 边界：不解析业务、不写库；令牌无效统一 401，身份层级不够统一 403。
package login

import (
	"context"
	"net/http"
	"strings"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	authzsvc "dandelion/server/internal/service/authz"
)

// Verifier 校验令牌：成功返回调用者身份。带上 ctx 是因为校验要读账号状态与令牌版本。
type Verifier func(ctx context.Context, token string) (contract.Identity, bool)

// RequireAuth 返回校验 Bearer 令牌的中间件；通过后把身份写进上下文再放行。
func RequireAuth(verify Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := verify(r.Context(), bearerToken(r))
			if !ok {
				handler.WriteError(w, r, contract.ErrUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(authctx.With(r.Context(), identity)))
		})
	}
}

// RequireMinLevel 要求账号身份层级不低于 min，**必须挂在 RequireAuth 之后**。
// 层级顺序：super_admin > platform_admin > team_admin > member。
// 它取代散落各处的 RequireSuperAdmin：同一个中间件传不同参数即可表达不同门槛。
// min 非法时 comparison 恒为 false（见 contract.AccountLevelAtLeast），避免拼错门槛把接口敞开。
func RequireMinLevel(min string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !contract.AccountLevelAtLeast(authctx.From(r.Context()).Level(), min) {
				handler.WriteError(w, r, contract.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireSuperAdmin 是 RequireMinLevel("super_admin") 的别名，仅为存量路由过渡保留。
// 新代码禁止直接使用：管理接口应声明它真正需要的最低层级。
func RequireSuperAdmin(next http.Handler) http.Handler {
	return RequireMinLevel(contract.AccountLevelSuperAdmin)(next)
}

// InjectScope 把 ScopeOf 推导出的数据范围放进 context，
// 挂在 RequireAuth 之后、所有业务 handler 之前；handler 从 ctx 取，不自己算、更不接受请求参数。
// 推导失败直接终止请求：拿不到可信范围时宁可报错，也不能让后续查询退化成全量。
func InjectScope(svc *authzsvc.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, err := svc.ScopeOf(r.Context(), authctx.From(r.Context()))
			if err != nil {
				handler.WriteError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(authctx.WithScope(r.Context(), scope)))
		})
	}
}

// bearerToken 从 Authorization 头取令牌，容忍大小写与前后的空格。
func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return ""
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// Package login · 鉴权中间件。
// 职责：校验 Bearer 令牌、把调用者身份写进请求上下文、提供「仅超级管理员」的守门器。
// 输入：Verifier（由 service/login 提供）；输出：包装后的 http.Handler。
// 边界：不解析业务、不写库；令牌无效统一 401，身份够不到统一 403。
package login

import (
	"context"
	"net/http"
	"strings"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
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

// RequireSuperAdmin 只放行超级管理员，**必须挂在 RequireAuth 之后**。
// 前端把菜单藏起来只是体验，真正的边界在这里：非超管一律 403。
func RequireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authctx.From(r.Context()).IsSuperAdmin {
			handler.WriteError(w, r, contract.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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

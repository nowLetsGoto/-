// Package login · 登录令牌校验中间件。
// 职责：从 Authorization: Bearer <token> 取令牌，交给注入的校验函数；失败即 401 并中断。
// 输入：校验函数（由 service/login 的 Service.Verify 提供）；输出：可挂到 chi.Group 的中间件。
// 边界：不解析令牌、不查库、不做角色权限判断；「未携带」与「已失效」统一 401，避免泄露差异。
package login

import (
	"net/http"
	"strings"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
)

// Verifier 校验令牌，成功时返回用户名。用函数而非具体类型，便于测试替换。
type Verifier func(token string) (username string, ok bool)

// RequireAuth 返回校验 Bearer 令牌的中间件；校验通过后原样放行。
func RequireAuth(verify Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := verify(bearerToken(r)); !ok {
				handler.WriteError(w, r, contract.ErrUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken 取出 Bearer 之后的令牌；缺失或格式不符时返回空串（Verifier 会判为无效）。
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	// 先比长度再切片，避免短请求头触发越界；EqualFold 兼容 bearer / BEARER。
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(value[len(prefix):])
}

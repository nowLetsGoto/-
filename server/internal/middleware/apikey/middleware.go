// Package apikey · 外部接口的 API Key 校验与归属反查。
// 职责：从 X-API-Key 取 Key，反查它属于哪家公司（平台兜底 Key 为空串），把结果写进 context。
// 输入：KeyResolver（由 service/settings 提供）；输出：可挂到 chi 路由上的中间件。
// 边界：不生成 / 不轮换 Key、不区分调用方身份、不做业务授权。
//
// 归属只能来自 Key 本身：请求体里的任何公司标识都不可信，
// 否则任何人都能伪造一个 tenant 把包塞进别家公司。
package apikey

import (
	"context"
	"net/http"
	"strings"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
)

const headerName = "X-API-Key"

// KeyResolver 用调用方提供的 Key 反查归属公司；返回空串表示平台兜底 Key。
// 找不到（或 Key 无效）返回 contract.ErrAPIKeyInvalid。
// 每次请求都查一次库，改配置后无需重启。
type KeyResolver func(ctx context.Context, provided string) (string, error)

// tenantKey 是存放归属公司的 context key，仅本包与 openapi handler 之间约定。
type tenantKey struct{}

// Require 返回校验 API Key 的中间件；校验通过后把归属公司写进 context。
func Require(resolve KeyResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := strings.TrimSpace(r.Header.Get(headerName))
			// Key 未配置或无效：宁可全拒，也不要放行一个没有凭据的写接口。
			if provided == "" {
				handler.WriteError(w, r, contract.ErrAPIKeyInvalid)
				return
			}
			tenantID, err := resolve(r.Context(), provided)
			if err != nil {
				handler.WriteError(w, r, contract.ErrAPIKeyInvalid)
				return
			}
			// 空串是合法结果（平台兜底 Key），表示"按项目归属推导公司"。
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantKey{}, tenantID)))
		})
	}
}

// TenantFrom 取出中间件写入的归属公司。
// 返回 ("", false)：调用链上没有 API Key 中间件（不该发生，调用方应视为未授权）；
// 返回 ("", true)：合法的平台兜底 Key。两者必须区分，否则会把"没走鉴权"当成"兜底 Key"。
func TenantFrom(ctx context.Context) (string, bool) {
	tenantID, ok := ctx.Value(tenantKey{}).(string)
	return tenantID, ok
}

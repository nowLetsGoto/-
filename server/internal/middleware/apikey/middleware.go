// Package apikey · 外部接口的 API Key 校验。
// 职责：从 X-API-Key 取 Key，与系统设置里的 Key 定长比较；不匹配即 401。
// 输入：KeyProvider（由 service/settings 提供）；输出：可挂到 chi 路由上的中间件。
// 边界：不生成 / 不轮换 Key、不区分调用方身份；Key 为空（未配置）时一律拒绝。
package apikey

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
)

const headerName = "X-API-Key"

// KeyProvider 返回当前有效 Key；每次请求都读一次，改配置后无需重启。
type KeyProvider func(ctx context.Context) (string, error)

// Require 返回校验 API Key 的中间件。
func Require(provider KeyProvider) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			expected, err := provider(r.Context())
			// 读配置失败或 Key 未配置：宁可全拒，也不要放行一个没有凭据的写接口。
			if err != nil || expected == "" {
				handler.WriteError(w, r, contract.ErrAPIKeyInvalid)
				return
			}
			if !match(r.Header.Get(headerName), expected) {
				handler.WriteError(w, r, contract.ErrAPIKeyInvalid)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// match 用定长比较，避免通过响应耗时逐字符试探 Key。
func match(provided string, expected string) bool {
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(provided)), []byte(expected)) == 1
}

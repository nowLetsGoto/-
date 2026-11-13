// Package middleware 提供错误处理中间件。
// 职责：把返回 error 的 handler 适配成标准 http.HandlerFunc（由 WriteError 统一格式化）；
//       作为最外层中间件恢复 panic；不参与任何业务判断。
// 输入：业务 handler 或下一层 http.Handler。
// 输出：可被 chi 注册的 http.HandlerFunc / http.Handler。
// 边界：错误格式化与 tmp 清理都在 handler.WriteError 一处完成，本包不重复实现。
package middleware

import (
	"fmt"
	"log"
	"net/http"

	"dandelion/server/internal/handler"
)

// Handler 是允许返回 error 的业务处理函数；路由层因此零 try/catch。
type Handler func(http.ResponseWriter, *http.Request) error

// Wrap 把业务 handler 适配为标准 http.HandlerFunc，普通错误交给统一出口。
func Wrap(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			handler.WriteError(w, r, err)
		}
	}
}

// ErrorHandler 在 NewRouter 组装完路由/静态/fallback 之后最后挂载，
// 是 panic 的最终兜底；chi 要求 Use 早于路由注册，故用外层包装实现「最后挂载」语义。
func ErrorHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("[panic] method=%s path=%s value=%v", r.Method, r.URL.Path, recovered)
				handler.WriteError(w, r, fmt.Errorf("未捕获异常: %v", recovered))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

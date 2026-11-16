// clientip.go · 取请求来源 IP。
// 职责：优先用反向代理写入的 X-Forwarded-For 首段，退化到 RemoteAddr 的主机部分。
// 输入：*http.Request；输出：IP 字符串（取不到返回空串）。
// 边界：只做提取，不信任性校验——IP 仅用于审计与「最近登录」展示，不作为鉴权依据。
package handler

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP 返回调用方 IP：优先反向代理的 X-Forwarded-For 第一段。
func ClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

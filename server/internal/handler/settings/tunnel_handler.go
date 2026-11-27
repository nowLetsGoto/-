// tunnel_handler.go · 公网隧道状态的 HTTP 语义层。
// 职责：读取隧道状态、触发一次深度检查、重启隧道服务。
// 输入：HTTP 请求；输出：统一 JSON 响应。
// 边界：不修改隧道配置、不代做登录；重启只调用 launchctl kickstart。
//
// 与 settings handler 同组：隧道属于「平台集成」的一部分，
// 复用同一套登录与权限中间件，不另开一个接口组。
package settings

import (
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/handler"
)

// GetTunnel 返回隧道的只读状态（快路径，不跑 cloudflared 命令）。
func (h *Handler) GetTunnel(w http.ResponseWriter, r *http.Request) error {
	status, err := h.tunnel.Status(r.Context())
	if err != nil {
		return err
	}
	return handler.OK(w, status)
}

// CheckTunnel 做一次深度检查，读取真实连接器数（约 1-2 秒）。
//
// 挂读权限即可：它只查询，不改变任何状态。
func (h *Handler) CheckTunnel(w http.ResponseWriter, r *http.Request) error {
	status, err := h.tunnel.Check(r.Context())
	if err != nil {
		return err
	}
	return handler.OK(w, status)
}

// RestartTunnel 重启隧道服务。
//
// 只做 kickstart：配置不在这里改，重启是唯一安全的运维动作。
// 记录审计——重启会让公网短暂中断，是用户可见的操作。
func (h *Handler) RestartTunnel(w http.ResponseWriter, r *http.Request) error {
	if err := h.tunnel.Restart(r.Context()); err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "tunnel.restarted", "tunnel", "dandelion",
		map[string]any{}, handler.ClientIP(r))

	status, err := h.tunnel.Status(r.Context())
	if err != nil {
		return err
	}
	return handler.OK(w, status)
}

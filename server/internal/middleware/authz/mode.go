// Package authz · 会话模式中间件。
// 职责：按登录令牌里的模式限制接口；单人模式只允许业务数据与管理员的审计/平台设置。
// 输入：authctx 中的身份；输出：允许继续或 403。
// 边界：前端菜单只是投影，这里是真正的边界；模式来自签名令牌，不能通过 localStorage 伪造。
package authz

import (
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
)

// RequireTeamMode 限制为团队模式：人员/团队/项目配置/角色/账号管理等平台能力都属于团队视图。
func RequireTeamMode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authctx.From(r.Context()).Mode != contract.ModeTeam {
			handler.WriteError(w, r, contract.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// auth_handler.go · POST /auth/login。
// 职责：解码登录请求、做必填与长度校验、调用 AuthService、返回令牌。
// 输入：application/json {username, password}；输出：{token, username, expiresAt} 包在统一响应外壳里。
// 边界：不做密码比对（在 service）、不写会话表、不下发 Cookie；
//       本接口自身不校验令牌，未登录 / 过期统一由 middleware.RequireAuth 处理。
package handler

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/service"
)

// AuthHandler 只依赖鉴权服务，由 main.go 注入。
type AuthHandler struct {
	svc *service.AuthService
}

func NewAuthHandler(svc *service.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

// Login 校验凭据并签发令牌；凭据错误返回 400，文案不区分「用户不存在」与「密码错误」。
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) error {
	var in contract.LoginInput
	if err := DecodeJSON(w, r, &in, "username/password"); err != nil {
		return err
	}

	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || in.Password == "" {
		return BadRequest("请输入用户名和密码")
	}
	if utf8.RuneCountInString(in.Username) > contract.MaxUsernameLen ||
		utf8.RuneCountInString(in.Password) > contract.MaxPasswordLen {
		return BadRequest("用户名或密码过长")
	}
	// 只记录用户名用于排查，密码永不进日志。
	AttachLogFields(r, "username="+in.Username)

	result, err := h.svc.Login(in.Username, in.Password)
	if err != nil {
		return err
	}
	return OK(w, result)
}

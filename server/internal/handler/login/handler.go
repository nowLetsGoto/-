// Package login · POST /auth/login 的 HTTP 语义。
// 职责：解码登录请求、做必填与长度校验、调用业务层、返回令牌。
// 输入：application/json {username, password, mode?}；输出：{token, username, expiresAt, mode} 包在统一响应外壳里。
// 边界：不做密码比对（在 service/login）、不写会话表、不下发 Cookie；
//
//	本接口自身不校验令牌，未登录 / 过期统一由 middleware/login 处理。
package login

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	loginsvc "dandelion/server/internal/service/login"
)

// Handler 只依赖登录业务层，由 main.go 注入。
type Handler struct {
	svc *loginsvc.Service
}

func NewHandler(service *loginsvc.Service) *Handler {
	return &Handler{svc: service}
}

// Login 校验凭据并签发令牌；凭据错误返回 400，文案不区分「用户不存在」与「密码错误」。
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) error {
	var in contract.LoginInput
	if err := handler.DecodeJSON(w, r, &in, "username/password/mode"); err != nil {
		return err
	}

	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || in.Password == "" {
		return handler.BadRequest("请输入用户名和密码")
	}
	if utf8.RuneCountInString(in.Username) > contract.MaxUsernameLen ||
		utf8.RuneCountInString(in.Password) > contract.MaxPasswordLen {
		return handler.BadRequest("用户名或密码过长")
	}
	// 只记录用户名用于排查，密码永不进日志。
	handler.AttachLogFields(r, "username="+in.Username)

	result, err := h.svc.Authenticate(r.Context(), in.Username, in.Password, handler.ClientIP(r), in.Mode)
	if err != nil {
		return err
	}
	return handler.OK(w, result)
}

// Package audit · 审计日志接口（仅超级管理员）。
// 职责：按动作 / 操作者 / 结果筛选并分页返回审计记录。
// 输入：HTTP 查询参数；输出：统一 JSON 响应。
// 边界：**只读**——不提供写入、修改、删除接口；写入发生在各业务 handler 内部。
package audit

import (
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
)

// Handler 只依赖审计业务层。
type Handler struct {
	svc *auditsvc.Service
}

func NewHandler(svc *auditsvc.Service) *Handler {
	return &Handler{svc: svc}
}

// List 分页查询审计日志：action / actor / result 三个条件都可省略。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	query := contract.AuditQuery{
		Action:    r.URL.Query().Get("action"),
		ActorName: r.URL.Query().Get("actor"),
		Result:    r.URL.Query().Get("result"),
	}
	// 单人管理员只能追溯自己的操作；团队模式保留跨账号审计能力。
	if authctx.From(r.Context()).Mode == contract.ModeSolo {
		query.ActorName = authctx.From(r.Context()).Username
	}
	data, err := h.svc.List(r.Context(), handler.ReadPage(r), query)
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

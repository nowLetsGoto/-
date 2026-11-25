// Package rdc 负责需求交付的 HTTP 语义。
// 职责：读取分页与需求 ID、严格解码人工输入、成功后记录审计。
// 输入：交付中心 HTTP 请求及身份/scope；输出：统一 JSON 外壳。
// 边界：不写 SQL、不判验收业务规则、Git 刷新不写审计。
package rdc

import (
	"context"
	"net/http"
	"strings"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	rdcsvc "dandelion/server/internal/service/rdc"
	"github.com/go-chi/chi/v5"
)

type DeliveryHandler struct {
	svc   *rdcsvc.DeliveryService
	audit *auditsvc.Service
}

func NewDeliveryHandler(svc *rdcsvc.DeliveryService, audit *auditsvc.Service) *DeliveryHandler {
	return &DeliveryHandler{svc: svc, audit: audit}
}
func deliveryID(r *http.Request) (string, error) {
	id := chi.URLParam(r, "requirementId")
	if !contract.IsValidUUID(id) {
		return "", handler.BadRequest("需求标识无效")
	}
	return id, nil
}
func (h *DeliveryHandler) List(w http.ResponseWriter, r *http.Request) error {
	archived := r.URL.Query().Get("archived")
	if archived != "" && archived != "true" && archived != "false" {
		return handler.BadRequest("归档筛选无效")
	}
	acceptance := r.URL.Query().Get("acceptanceStatus")
	if acceptance != "" && acceptance != contract.AcceptPending && acceptance != contract.AcceptPassed && acceptance != contract.AcceptFailed {
		return handler.BadRequest("验收筛选无效")
	}
	q := contract.DeliveryQuery{Page: handler.ReadPage(r), Keyword: strings.TrimSpace(r.URL.Query().Get("keyword")), AcceptanceStatus: acceptance, Archived: archived}
	result, err := h.svc.List(r.Context(), q, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, result)
}
func (h *DeliveryHandler) Detail(w http.ResponseWriter, r *http.Request) error {
	id, err := deliveryID(r)
	if err != nil {
		return err
	}
	result, err := h.svc.Detail(r.Context(), id, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, result)
}
func (h *DeliveryHandler) RefreshGit(w http.ResponseWriter, r *http.Request) error {
	id, err := deliveryID(r)
	if err != nil {
		return err
	}
	result, err := h.svc.RefreshGit(r.Context(), id, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, result)
}
func (h *DeliveryHandler) RegisterTest(w http.ResponseWriter, r *http.Request) error {
	id, err := deliveryID(r)
	if err != nil {
		return err
	}
	var in contract.TestRegisterInput
	if err = handler.DecodeJSON(w, r, &in, "result/summary/reportRef"); err != nil {
		return err
	}
	row, err := h.svc.RegisterTest(r.Context(), id, authctx.ScopeFrom(r.Context()), authctx.From(r.Context()), in)
	if err != nil {
		return err
	}
	h.record(r, "rdc.delivery.test_registered", id, map[string]any{"recordId": row.ID, "seq": row.Seq, "result": in.Result})
	return handler.OK(w, row)
}
func (h *DeliveryHandler) Accept(w http.ResponseWriter, r *http.Request) error {
	id, err := deliveryID(r)
	if err != nil {
		return err
	}
	var in contract.DeliveryAcceptInput
	if err = handler.DecodeJSON(w, r, &in, "result/note/items"); err != nil {
		return err
	}
	row, err := h.svc.Accept(r.Context(), id, authctx.ScopeFrom(r.Context()), authctx.From(r.Context()), in)
	if err != nil {
		return err
	}
	h.record(r, "rdc.delivery.accepted", id, map[string]any{"recordId": row.ID, "seq": row.Seq, "result": in.Result, "itemCount": len(in.Items)})
	return handler.OK(w, row)
}
func (h *DeliveryHandler) Archive(w http.ResponseWriter, r *http.Request) error {
	id, err := deliveryID(r)
	if err != nil {
		return err
	}
	row, err := h.svc.Archive(r.Context(), id, authctx.ScopeFrom(r.Context()), authctx.From(r.Context()))
	if err != nil {
		return err
	}
	h.record(r, "rdc.delivery.archived", id, map[string]any{"recordId": row.ID, "seq": row.Seq})
	return handler.OK(w, row)
}
func (h *DeliveryHandler) record(r *http.Request, action, id string, detail map[string]any) {
	h.audit.Record(context.WithoutCancel(r.Context()), authctx.From(r.Context()), action, "rdc_delivery_record", id, detail, handler.ClientIP(r))
}

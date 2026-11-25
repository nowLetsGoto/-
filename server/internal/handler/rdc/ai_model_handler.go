// ai_model_handler.go · AI 模型配置的 HTTP 语义层。
// 职责：取分页/筛选/tenantId、严格 DecodeJSON、调用业务层、成功分支写 7 类审计、统一响应外壳。
// 输入：HTTP 请求与上下文中的身份 / scope；输出：{code,data,message} JSON（新建 201，其余 200）。
// 边界：不写 SQL、不校验业务字段（在 service）、不判权限（在路由中间件）；
//
//	请求体白名单不含 tenantId / isDefault / enabled / apiKeyMask，实测只走同一 ModelChatService。
package rdc

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	rdcsvc "dandelion/server/internal/service/rdc"
)

// 7 类审计 action，detail 只记非敏感事实，Key 仅记是否更新。
const (
	aiActionCreated    = "rdc.ai_model.created"
	aiActionUpdated    = "rdc.ai_model.updated"
	aiActionDefaultSet = "rdc.ai_model.default_set"
	aiActionEnabled    = "rdc.ai_model.enabled"
	aiActionDisabled   = "rdc.ai_model.disabled"
	aiActionDeleted    = "rdc.ai_model.deleted"
	aiActionTested     = "rdc.ai_model.tested"
	aiObjectType       = "rdc_ai_model"
)

// 实测探针固定为一句短提示，用户可在界面看到将消耗少量 token 的二次确认。
var aiProbeMessages = []rdcsvc.ChatMessage{{Role: "user", Content: "请回复简短的 OK，用于验证模型配置。"}}

// aiModelService 是 handler 依赖的最小业务接口；便于用 fake 做严格字段与审计测试。
type aiModelService interface {
	List(ctx context.Context, scope contract.Scope, tenantID string, query contract.AIModelQuery) (contract.AIModelList, error)
	Get(ctx context.Context, id string, scope contract.Scope, tenantID string) (contract.AIModel, error)
	Create(ctx context.Context, scope contract.Scope, tenantID string, in contract.AIModelInput) (contract.AIModel, error)
	Update(ctx context.Context, id string, scope contract.Scope, tenantID string, in contract.AIModelInput) (contract.AIModel, error)
	SetDefault(ctx context.Context, id string, scope contract.Scope, tenantID string) (contract.AIModel, error)
	SetEnabled(ctx context.Context, id string, enabled bool, scope contract.Scope, tenantID string) (contract.AIModel, error)
	SoftDelete(ctx context.Context, id string, scope contract.Scope, tenantID string) error
	// ResolveScope 供实测端点复用同一超管代配投影，避免把 All 范围漏给仓储。
	ResolveScope(scope contract.Scope, tenantID string) (contract.Scope, error)
}

// aiChatService 只暴露实测；内部 Chat 由其它 Go 模块注入，不走 HTTP。
type aiChatService interface {
	Test(ctx context.Context, scope contract.Scope, modelID string, probe []rdcsvc.ChatMessage) (contract.AIModelTestResult, error)
}

// AIModelHandler 依赖配置服务、统一调用服务与审计服务。
type AIModelHandler struct {
	svc   aiModelService
	chat  aiChatService
	audit *auditsvc.Service
}

// NewAIModelHandler 构造 HTTP 处理器。
func NewAIModelHandler(svc aiModelService, chat aiChatService, audit *auditsvc.Service) *AIModelHandler {
	return &AIModelHandler{svc: svc, chat: chat, audit: audit}
}

// List：分页 + 启用态 + 协议 + 超管 tenantId；超管未选公司由 service 返回空页。
func (h *AIModelHandler) List(w http.ResponseWriter, r *http.Request) error {
	query := contract.AIModelQuery{Page: handler.ReadPage(r), TenantID: r.URL.Query().Get("tenantId")}
	if raw := r.URL.Query().Get("enabled"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return handler.BadRequest("启用态筛选只能是 true 或 false")
		}
		query.Enabled = &value
	}
	// 协议筛选：非法值当场 400，而不是默默返回空列表让人以为"没配模型"。
	if raw := strings.TrimSpace(r.URL.Query().Get("protocol")); raw != "" {
		if !slices.Contains(contract.AIProtocols, raw) {
			return handler.BadRequest("协议筛选只能是 openai / anthropic / openai_responses")
		}
		query.Protocol = raw
	}
	data, err := h.svc.List(r.Context(), authctx.ScopeFrom(r.Context()), query.TenantID, query)
	if err != nil {
		return aiHandlerError(err)
	}
	return handler.OK(w, data)
}

// Get 按 id 读取详情，越界 404。
func (h *AIModelHandler) Get(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"), authctx.ScopeFrom(r.Context()), r.URL.Query().Get("tenantId"))
	if err != nil {
		return aiHandlerError(err)
	}
	return handler.OK(w, data)
}

// Create 新建模型；apiKey 明文只在请求体里出现一次，响应只有掩码。
func (h *AIModelHandler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.AIModelInput
	if err := decodeAIModelInput(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	identity := authctx.From(ctx)
	tenantID := r.URL.Query().Get("tenantId")
	model, err := h.svc.Create(ctx, authctx.ScopeFrom(ctx), tenantID, in)
	if err != nil {
		return aiHandlerError(err)
	}
	h.record(ctx, identity, r, aiActionCreated, model.ID, map[string]any{
		"modelId": model.ID, "name": model.Name, "provider": model.Provider, "baseUrl": model.BaseURL,
		"apiKeyUpdated": in.APIKey != "",
	}, authctx.ScopeFrom(ctx), tenantID)
	return handler.OKWithStatus(w, http.StatusCreated, model)
}

// Update 编辑资料；请求体留空 apiKey 即不更新三列。审计先读旧值算变更字段。
func (h *AIModelHandler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.AIModelInput
	if err := decodeAIModelInput(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	identity := authctx.From(ctx)
	scope := authctx.ScopeFrom(ctx)
	id, tenantID := chi.URLParam(r, "id"), r.URL.Query().Get("tenantId")
	before, err := h.svc.Get(ctx, id, scope, tenantID)
	if err != nil {
		return aiHandlerError(err)
	}
	model, err := h.svc.Update(ctx, id, scope, tenantID, in)
	if err != nil {
		return aiHandlerError(err)
	}
	h.record(ctx, identity, r, aiActionUpdated, id, map[string]any{
		"modelId": id, "name": model.Name, "fieldsChanged": aiChangedFields(before, model),
		"apiKeyUpdated": in.APIKey != "",
	}, scope, tenantID)
	return handler.OK(w, model)
}

// SetDefault 事务互斥切换默认；影响面是"下一次调用会用这家公司的新默认模型"。
func (h *AIModelHandler) SetDefault(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	identity := authctx.From(ctx)
	scope := authctx.ScopeFrom(ctx)
	id, tenantID := chi.URLParam(r, "id"), r.URL.Query().Get("tenantId")
	before, err := h.svc.Get(ctx, id, scope, tenantID)
	if err != nil {
		return aiHandlerError(err)
	}
	model, err := h.svc.SetDefault(ctx, id, scope, tenantID)
	if err != nil {
		return aiHandlerError(err)
	}
	h.record(ctx, identity, r, aiActionDefaultSet, id, map[string]any{
		"modelId": id, "previousDefaultId": aiPreviousDefault(before),
	}, scope, tenantID)
	return handler.OK(w, model)
}

// SetEnabled 启停；审计 action 区分 enabled / disabled，不记录 Key。
func (h *AIModelHandler) SetEnabled(w http.ResponseWriter, r *http.Request) error {
	var in contract.AIModelStatusInput
	if err := handler.DecodeJSON(w, r, &in, "enabled"); err != nil {
		return err
	}
	ctx := r.Context()
	identity := authctx.From(ctx)
	scope := authctx.ScopeFrom(ctx)
	id, tenantID := chi.URLParam(r, "id"), r.URL.Query().Get("tenantId")
	model, err := h.svc.SetEnabled(ctx, id, in.Enabled, scope, tenantID)
	if err != nil {
		return aiHandlerError(err)
	}
	action := aiActionDisabled
	if in.Enabled {
		action = aiActionEnabled
	}
	h.record(ctx, identity, r, action, id, map[string]any{"modelId": id, "enabled": in.Enabled}, scope, tenantID)
	return handler.OK(w, model)
}

// Delete 软删（enabled=false）；先读一次拿到名称供审计，失败只记日志不回滚。
func (h *AIModelHandler) Delete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	identity := authctx.From(ctx)
	scope := authctx.ScopeFrom(ctx)
	id, tenantID := chi.URLParam(r, "id"), r.URL.Query().Get("tenantId")
	before, err := h.svc.Get(ctx, id, scope, tenantID)
	if err != nil {
		return aiHandlerError(err)
	}
	if err := h.svc.SoftDelete(ctx, id, scope, tenantID); err != nil {
		return aiHandlerError(err)
	}
	h.record(ctx, identity, r, aiActionDeleted, id, map[string]any{
		"modelId": id, "name": before.Name,
	}, scope, tenantID)
	return handler.OK(w, nil)
}

// Test 用该模型已存 Key 调用一次；失败也返回 200 + 分类结果，不暴露上游原文。
func (h *AIModelHandler) Test(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	identity := authctx.From(ctx)
	scope := authctx.ScopeFrom(ctx)
	id, tenantID := chi.URLParam(r, "id"), r.URL.Query().Get("tenantId")
	// 实测是写操作：超管必须先把 ?tenantId= 投影到单一公司，否则仓储的失败关闭会误报 404。
	resolved, err := h.svc.ResolveScope(scope, tenantID)
	if err != nil {
		return aiHandlerError(err)
	}
	result, err := h.chat.Test(ctx, resolved, id, aiProbeMessages)
	if err != nil {
		return aiHandlerError(err)
	}
	h.record(ctx, identity, r, aiActionTested, id, map[string]any{
		"modelId": id, "category": string(result.Category), "statusCode": result.StatusCode,
		"latencyMs": result.LatencyMS,
	}, scope, tenantID)
	return handler.OK(w, result)
}

// decodeAIModelInput 严格白名单：tenantId / isDefault / enabled / apiKeyMask 入 body 一律 400。
func decodeAIModelInput(w http.ResponseWriter, r *http.Request, target *contract.AIModelInput) error {
	const allowed = "name/provider/protocol/baseUrl/modelId/apiKey/thinkingEnabled/contextTokens/extraConfig/capabilities"
	return handler.DecodeJSON(w, r, target, allowed)
}

// aiHandlerError 把"超管未选公司"翻译成 400；其余交给统一 WriteError（校验 400 / 越界 404）。
func aiHandlerError(err error) error {
	if errors.Is(err, contract.ErrAITenantRequired) {
		return handler.BadRequest(contract.ErrAITenantRequired.Error())
	}
	return err
}

// record 写审计；WithoutCancel 让客户端断开后仍能写完，Record 内部失败只记日志不回滚。
func (h *AIModelHandler) record(
	ctx context.Context, identity contract.Identity, r *http.Request,
	action, objectID string, detail map[string]any, scope contract.Scope, tenantID string,
) {
	if h.audit == nil {
		return
	}
	if scope.All {
		detail["targetTenantID"] = tenantID
		detail["delegated"] = true
	}
	h.audit.Record(context.WithoutCancel(ctx), identity, action, aiObjectType, objectID,
		detail, handler.ClientIP(r))
}

// aiChangedFields 只比较可见的资料字段，避免把 Key 或密文写进审计 detail。
func aiChangedFields(before, after contract.AIModel) []string {
	type field struct {
		name  string
		left  any
		right any
	}
	fields := []field{
		{"name", before.Name, after.Name},
		{"provider", before.Provider, after.Provider},
		{"protocol", before.Protocol, after.Protocol},
		{"baseUrl", before.BaseURL, after.BaseURL},
		{"modelId", before.ModelID, after.ModelID},
		{"thinkingEnabled", before.ThinkingEnabled, after.ThinkingEnabled},
		{"contextTokens", before.ContextTokens, after.ContextTokens},
		{"extraConfig", string(before.ExtraConfig), string(after.ExtraConfig)},
		{"capabilities", before.Capabilities, after.Capabilities},
	}
	changed := make([]string, 0, len(fields))
	for _, item := range fields {
		if !reflect.DeepEqual(item.left, item.right) {
			changed = append(changed, item.name)
		}
	}
	return changed
}

// aiPreviousDefault 返回设默认前的默认 id；没有默认时用空串表示，不回显任何 Key 信息。
func aiPreviousDefault(model contract.AIModel) string {
	if model.IsDefault {
		return model.ID
	}
	return ""
}

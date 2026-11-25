// interaction_handler_test.go · 交互层 HTTP 语义的安全底线测试。
// 职责：钉死请求体白名单、路径 id 校验、越界 404、回滚冲突 409 与 5 类审计 action。
// 输入：httptest 请求、内存仓储替身、unavailableProvider 与审计记录替身。
// 输出：断言状态码 / 审计 action / 审计 detail 关键字段。
// 边界：不连数据库、不起真实服务、不测路由中间件（由 app 层装配保证）。
package rdc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	rdcsvc "dandelion/server/internal/service/rdc"
)

const (
	handlerTaskID     = "11111111-1111-4111-8111-111111111111"
	handlerSubtaskID  = "22222222-2222-4222-8222-222222222222"
	handlerReviewID   = "33333333-3333-4333-8333-333333333333"
	handlerPlanID     = "44444444-4444-4444-8444-444444444444"
	handlerCheckpoint = "55555555-5555-4555-8555-555555555555"
)

// handlerRepo 为 handler 单测提供最小可读可写的交互仓储替身。
type handlerRepo struct {
	review     *contract.DiffReview
	plan       *contract.PlanConfirmation
	checkpoint *contract.Checkpoint
	evidence   map[string]any
}

func (r *handlerRepo) FindLatestReview(
	_ context.Context, _, _ string, _ contract.Scope,
) (*contract.DiffReview, error) {
	return r.review, nil
}
func (r *handlerRepo) FindReviewByID(
	_ context.Context, _ string, _ contract.Scope,
) (*contract.DiffReview, error) {
	return r.review, nil
}
func (r *handlerRepo) UpsertDecision(
	_ context.Context, _, _, _, _ string, _ contract.Scope,
) error {
	return nil
}
func (r *handlerRepo) SaveDecisionsTx(
	_ context.Context, _ string, _ []contract.HunkDecision, _ string, _ contract.Scope,
) error {
	return nil
}
func (r *handlerRepo) FindPlan(
	_ context.Context, _, _ string, _ contract.Scope,
) (*contract.PlanConfirmation, error) {
	return r.plan, nil
}
func (r *handlerRepo) FindPlanByID(
	_ context.Context, _ string, _ contract.Scope,
) (*contract.PlanConfirmation, error) {
	return r.plan, nil
}
func (r *handlerRepo) ConfirmPlanTx(
	_ context.Context, _ string, _ contract.PlanConfirmInput, _ string, _ contract.Scope,
) (contract.PlanConfirmation, error) {
	return *r.plan, nil
}
func (r *handlerRepo) ListCheckpoints(
	_ context.Context, _, _ string, _ contract.Scope,
) ([]contract.Checkpoint, error) {
	return []contract.Checkpoint{}, nil
}
func (r *handlerRepo) FindCheckpoint(
	_ context.Context, _ string, _ contract.Scope,
) (*contract.Checkpoint, error) {
	return r.checkpoint, nil
}
func (r *handlerRepo) MarkRollbackTx(
	_ context.Context, _, status, _ string, _ contract.Scope,
) (contract.Checkpoint, error) {
	result := *r.checkpoint
	result.Status = status
	return result, nil
}
func (r *handlerRepo) AppendEvidence(
	_ context.Context, _, _, action, _, _, _ string, detail map[string]any, _ contract.Scope,
) (contract.ReviewRecord, error) {
	r.evidence = detail
	return contract.ReviewRecord{Action: action}, nil
}
func (r *handlerRepo) FindEvidence(
	_ context.Context, _, _, _ string, _ contract.Scope,
) ([]contract.ReviewRecord, error) {
	return nil, nil
}

// auditSpy 记录 handler 写出的审计 action 与 detail。
type auditSpy struct {
	actions []string
	details []map[string]any
}

func (s *auditSpy) Record(
	_ context.Context, _ contract.Identity, action, _, _ string, detail map[string]any, _ string,
) {
	s.actions = append(s.actions, action)
	s.details = append(s.details, detail)
}

// handlerContext 注入 team scope 与操作人，模拟中间件已完成的身份解析。
func handlerContext(r *http.Request, params map[string]string) *http.Request {
	routeCtx := chi.NewRouteContext()
	for key, value := range params {
		routeCtx.URLParams.Add(key, value)
	}
	base := context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx)
	ctx := authctx.With(base, contract.Identity{
		UserID: "user-1", Username: "tester", Mode: contract.ModeTeam, TenantID: "tenant-1",
	})
	ctx = authctx.WithScope(ctx, contract.Scope{TenantID: "tenant-1"})
	return r.WithContext(ctx)
}

// newHandlerFixture 构造处理器与测试替身。
func newHandlerFixture() (*InteractionHandler, *handlerRepo, *auditSpy) {
	repo := &handlerRepo{
		review: &contract.DiffReview{
			ID: handlerReviewID, TaskID: handlerTaskID, SubtaskID: handlerSubtaskID,
			Source: contract.ArtifactSourceAgent, SubmitStatus: contract.SubmitPending,
			Files: []contract.FileDiff{{FilePath: "a.go", Hunks: []contract.Hunk{{HunkID: "h1"}}}},
		},
		plan: &contract.PlanConfirmation{ID: handlerPlanID, TaskID: handlerTaskID, SubtaskID: handlerSubtaskID},
		checkpoint: &contract.Checkpoint{
			ID: handlerCheckpoint, TaskID: handlerTaskID, SubtaskID: handlerSubtaskID,
			RollbackToken: "token-1", Status: contract.CheckpointActive,
			Files: []contract.FileSnapshot{{FilePath: "a.go", AfterHash: "after"}},
		},
	}
	spy := &auditSpy{}
	return NewInteractionHandler(
		rdcsvc.NewInteractionService(repo, rdcsvc.NewUnavailableProvider()), spy), repo, spy
}

// TestInteractionDecodeWhitelist 钉死所有写端点拒绝未知字段（含越权字段 tenantId / source / status）。
func TestInteractionDecodeWhitelist(t *testing.T) {
	handler, _, spy := newHandlerFixture()
	cases := []struct {
		name string
		body string
		call func(*http.Request) error
	}{
		{"submit", `{"decisions":[],"tenantId":"evil"}`, func(r *http.Request) error { return handler.SubmitDiffReview(httptest.NewRecorder(), r) }},
		{"decision", `{"decision":"accepted","status":"completed"}`, func(r *http.Request) error { return handler.SetHunkDecision(httptest.NewRecorder(), r) }},
		{"plan", `{"answers":[],"stepIds":[],"source":"manual"}`, func(r *http.Request) error { return handler.ConfirmPlan(httptest.NewRecorder(), r) }},
		{"rollback", `{"rollbackToken":"t","scope":"checkpoint","targets":[],"currentHashes":{},"status":"completed"}`, func(r *http.Request) error { return handler.Rollback(httptest.NewRecorder(), r) }},
		{"approval", `{"checkpointId":"` + handlerCheckpoint + `","decision":"approve","tenantId":"evil"}`, func(r *http.Request) error { return handler.DecideApproval(httptest.NewRecorder(), r) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/rdc/interactions", strings.NewReader(tc.body))
			err := tc.call(handlerContext(r, nil))
			if err == nil || !strings.Contains(err.Error(), "未知字段") {
				t.Fatalf("未知字段应 400，实际 %v", err)
			}
		})
	}
	if len(spy.actions) != 0 {
		t.Fatalf("解码失败不得写审计，实际 %v", spy.actions)
	}
}

// TestInteractionInvalidPathIDIs404 钉死非法 UUID 按 404 处理，不把脏值送进 SQL。
func TestInteractionInvalidPathIDIs404(t *testing.T) {
	handler, _, _ := newHandlerFixture()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/rdc/interactions/tasks/not-a-uuid/subtasks/x/diff-review", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "not-a-uuid")
	routeCtx.URLParams.Add("subtaskId", handlerSubtaskID)
	chiContext := context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx)
	err := handler.GetDiffReview(httptest.NewRecorder(), r.WithContext(chiContext))
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("非法路径 id 应 404，实际 %v", err)
	}
}

// TestInteractionAuditActions 钉死 4 类成功操作各写一条审计且 detail 含关联键。
func TestInteractionAuditActions(t *testing.T) {
	handler, _, spy := newHandlerFixture()

	submit := httptest.NewRequest(http.MethodPost, "/submit",
		strings.NewReader(`{"decisions":[{"reviewId":"`+handlerReviewID+`","hunkId":"h1","decision":"accepted","decidedBy":"","decidedAt":""}]}`))
	if err := handler.SubmitDiffReview(httptest.NewRecorder(), handlerContext(submit, map[string]string{"id": handlerTaskID, "subtaskId": handlerSubtaskID})); err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	plan := httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(`{"answers":[],"stepIds":[]}`))
	if err := handler.ConfirmPlan(httptest.NewRecorder(), handlerContext(plan, map[string]string{"planId": handlerPlanID})); err != nil {
		t.Fatalf("确认计划失败: %v", err)
	}

	rollback := httptest.NewRequest(http.MethodPost, "/rollback",
		strings.NewReader(`{"rollbackToken":"token-1","scope":"checkpoint","targets":[],"currentHashes":{"a.go":"after"}}`))
	if err := handler.Rollback(httptest.NewRecorder(), handlerContext(rollback, map[string]string{"checkpointId": handlerCheckpoint})); err != nil {
		t.Fatalf("回滚失败: %v", err)
	}

	approval := httptest.NewRequest(http.MethodPost, "/approval/decide",
		strings.NewReader(`{"checkpointId":"`+handlerCheckpoint+`","decision":"approve"}`))
	if err := handler.DecideApproval(httptest.NewRecorder(), handlerContext(approval, map[string]string{"id": handlerTaskID, "subtaskId": handlerSubtaskID})); err != nil {
		t.Fatalf("审批失败: %v", err)
	}

	want := []string{
		"rdc.interaction.diff_submitted", "rdc.interaction.plan_confirmed",
		"rdc.interaction.checkpoint_rolled_back", "rdc.interaction.approval_decided",
	}
	if len(spy.actions) != len(want) {
		t.Fatalf("审计数量应为 %d，实际 %v", len(want), spy.actions)
	}
	for i, action := range want {
		if spy.actions[i] != action {
			t.Fatalf("第 %d 条审计应为 %s，实际 %s", i, action, spy.actions[i])
		}
		detail := spy.details[i]
		if detail["taskId"] != handlerTaskID || detail["subtaskId"] != handlerSubtaskID {
			t.Fatalf("审计 %s 缺少关联键: %v", action, detail)
		}
	}
}

// TestRollbackConflictIs409AndAudited 钉死哈希不匹配返回 409 + rollback_blocked 审计，且无强制覆盖。
func TestRollbackConflictIs409AndAudited(t *testing.T) {
	h, _, spy := newHandlerFixture()
	rollback := httptest.NewRequest(http.MethodPost, "/rollback",
		strings.NewReader(`{"rollbackToken":"token-1","scope":"checkpoint","targets":[],"currentHashes":{"a.go":"changed"}}`))
	w := httptest.NewRecorder()
	err := h.Rollback(w, handlerContext(rollback, map[string]string{"checkpointId": handlerCheckpoint}))
	var appErr *handler.AppError
	if err == nil || !errors.As(err, &appErr) || appErr.Status != http.StatusConflict {
		t.Fatalf("哈希冲突应 409，实际 %v", err)
	}
	if len(spy.actions) != 1 || spy.actions[0] != "rdc.interaction.rollback_blocked" {
		t.Fatalf("冲突必须写 rollback_blocked 审计，实际 %v", spy.actions)
	}
	mismatch, ok := spy.details[0]["hashMismatch"].([]string)
	if !ok || len(mismatch) != 1 || mismatch[0] != "a.go" {
		t.Fatalf("审计应列出冲突文件，实际 %v", spy.details[0])
	}
}

// TestInteractionOKEnvelope 钉死读端点空态的 {code,data:null,message} 外壳。
func TestInteractionOKEnvelope(t *testing.T) {
	handler, _, _ := newHandlerFixture()
	handler.svc = rdcsvc.NewInteractionService(&handlerRepo{}, rdcsvc.NewUnavailableProvider())
	r := httptest.NewRequest(http.MethodGet, "/diff-review", nil)
	w := httptest.NewRecorder()
	if err := handler.GetDiffReview(w, handlerContext(r, map[string]string{"id": handlerTaskID, "subtaskId": handlerSubtaskID})); err != nil {
		t.Fatalf("空态读取不应报错: %v", err)
	}
	var envelope struct {
		Code    int             `json:"code"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if envelope.Code != 0 || string(envelope.Data) != "null" {
		t.Fatalf("provider 未就绪应返回 data:null，实际 %s", w.Body.String())
	}
}

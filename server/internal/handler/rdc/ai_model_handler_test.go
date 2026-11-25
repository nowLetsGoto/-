// ai_model_handler_test.go · AI 模型 HTTP 语义测试。
// 职责：钉死严格请求白名单、solo 403、超管未选公司 400、响应不含 Key 与实测结果直出。
// 输入：httptest 请求、内存 fake 服务、chi 路由与团队模式中间件。
// 输出：状态码 / JSON 外壳符合契约；不连数据库、不访问 scnet、不含真实凭据。
// 边界：权限点守卫由 router 的 RequirePermission 负责，这里只验证 handler 自身的输入边界。
package rdc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/middleware"
	authzmw "dandelion/server/internal/middleware/authz"
	rdcsvc "dandelion/server/internal/service/rdc"
)

const aiHandlerTestKey = "sk-test-not-a-real-key-1234"

type fakeModelService struct {
	createdInput contract.AIModelInput
	createErr    error
}

func (f *fakeModelService) List(context.Context, contract.Scope, string, contract.AIModelQuery) (contract.AIModelList, error) {
	return contract.AIModelList{Items: []contract.AIModel{}}, nil
}
func (f *fakeModelService) Get(context.Context, string, contract.Scope, string) (contract.AIModel, error) {
	return contract.AIModel{}, nil
}
func (f *fakeModelService) Create(_ context.Context, _ contract.Scope, _ string, in contract.AIModelInput) (contract.AIModel, error) {
	f.createdInput = in
	if f.createErr != nil {
		return contract.AIModel{}, f.createErr
	}
	return contract.AIModel{ID: "id", Name: in.Name, APIKeyMask: "sk-****1234"}, nil
}
func (f *fakeModelService) Update(context.Context, string, contract.Scope, string, contract.AIModelInput) (contract.AIModel, error) {
	return contract.AIModel{}, nil
}
func (f *fakeModelService) SetDefault(context.Context, string, contract.Scope, string) (contract.AIModel, error) {
	return contract.AIModel{}, nil
}
func (f *fakeModelService) SetEnabled(context.Context, string, bool, contract.Scope, string) (contract.AIModel, error) {
	return contract.AIModel{}, nil
}
func (f *fakeModelService) SoftDelete(context.Context, string, contract.Scope, string) error {
	return nil
}
func (f *fakeModelService) ResolveScope(scope contract.Scope, tenantID string) (contract.Scope, error) {
	if !scope.All {
		return scope, nil
	}
	if tenantID == "" {
		return scope, contract.ErrAITenantRequired
	}
	return contract.Scope{TenantID: tenantID}, nil
}

type fakeChatService struct {
	result    contract.AIModelTestResult
	gotScope  contract.Scope
	gotTenant string
}

func (f *fakeChatService) Test(_ context.Context, scope contract.Scope, _ string, _ []rdcsvc.ChatMessage) (contract.AIModelTestResult, error) {
	f.gotScope = scope
	f.gotTenant = scope.TenantID
	return f.result, nil
}

func aiHandlerRouter(mode contract.SessionMode, all bool, svc *fakeModelService, chat *fakeChatService) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := authctx.With(r.Context(), contract.Identity{Mode: mode, Username: "tester"})
			ctx = authctx.WithScope(ctx, contract.Scope{TenantID: "11111111-1111-1111-1111-111111111111", All: all})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	router.Use(authzmw.RequireTeamMode)
	handler := NewAIModelHandler(svc, chat, nil)
	router.Group(func(group chi.Router) {
		group.Post("/rdc/models", middleware.Wrap(handler.Create))
		group.Post("/rdc/models/{id}/test", middleware.Wrap(handler.Test))
	})
	return router
}

func aiRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestCreateRejectsForbiddenFields(t *testing.T) {
	for _, field := range []string{`,"tenantId":"x"`, `,"isDefault":true`, `,"enabled":true`, `,"apiKeyMask":"x"`} {
		svc := &fakeModelService{}
		router := aiHandlerRouter(contract.ModeTeam, false, svc, &fakeChatService{})
		body := `{"name":"m","provider":"scnet","protocol":"openai","baseUrl":"https://x.cn/v1","modelId":"m1","contextTokens":8192` + field + `}`
		response := aiRequest(router, http.MethodPost, "/rdc/models", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("未知字段 %s 必须 400，实际 %d", field, response.Code)
		}
	}
}

func TestCreateSoloForbidden(t *testing.T) {
	router := aiHandlerRouter(contract.ModeSolo, false, &fakeModelService{}, &fakeChatService{})
	response := aiRequest(router, http.MethodPost, "/rdc/models", `{}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("solo 必须 403，实际 %d", response.Code)
	}
}

func TestSuperAdminWithoutTenantReturns400(t *testing.T) {
	svc := &fakeModelService{createErr: contract.ErrAITenantRequired}
	router := aiHandlerRouter(contract.ModeTeam, true, svc, &fakeChatService{})
	response := aiRequest(router, http.MethodPost, "/rdc/models", `{"name":"m"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("超管未选公司必须 400，实际 %d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateResponseHasNoPlainKey(t *testing.T) {
	svc := &fakeModelService{}
	router := aiHandlerRouter(contract.ModeTeam, false, svc, &fakeChatService{})
	body := `{"name":"m","provider":"scnet","protocol":"openai","baseUrl":"https://x.cn/v1","modelId":"m1","apiKey":"` + aiHandlerTestKey + `","contextTokens":8192,"thinkingEnabled":false,"extraConfig":{},"capabilities":[]}`
	response := aiRequest(router, http.MethodPost, "/rdc/models", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("新建应 201，实际 %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), aiHandlerTestKey) {
		t.Fatal("响应不得包含 Key 明文")
	}
	if svc.createdInput.APIKey != aiHandlerTestKey {
		t.Fatal("service 必须收到明文 Key 才能加密入库")
	}
	var envelope struct {
		Code int     `json:"code"`
		Data AIModel `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Code != 0 || envelope.Data.APIKeyMask != "sk-****1234" {
		t.Fatalf("响应外壳/掩码错误: %s err=%v", response.Body.String(), err)
	}
}

type AIModel = contract.AIModel

func TestTestEndpointReturnsRedactedResult(t *testing.T) {
	chat := &fakeChatService{result: contract.AIModelTestResult{
		OK: false, Category: contract.AIChatAuthFailed, Message: contract.AIChatMessage(contract.AIChatAuthFailed),
		StatusCode: 401, RequestSummary: "model=model-x", ResponsePreview: "已隐藏",
	}}
	router := aiHandlerRouter(contract.ModeTeam, false, &fakeModelService{}, chat)
	response := aiRequest(router, http.MethodPost, "/rdc/models/id/test", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("实测失败也应 200，实际 %d", response.Code)
	}
	if strings.Contains(response.Body.String(), aiHandlerTestKey) {
		t.Fatal("实测响应不得含 Key")
	}
	if !strings.Contains(response.Body.String(), "auth_failed") {
		t.Fatalf("实测响应应含错误分类: %s", response.Body.String())
	}
}

func TestTestEndpointDelegatesTenantScope(t *testing.T) {
	const target = "22222222-2222-2222-2222-222222222222"
	chat := &fakeChatService{result: contract.AIModelTestResult{OK: true, Category: "ok"}}
	router := aiHandlerRouter(contract.ModeTeam, true, &fakeModelService{}, chat)

	response := aiRequest(router, http.MethodPost, "/rdc/models/id/test?tenantId="+target, `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("超管带 tenantId 实测应 200，实际 %d %s", response.Code, response.Body.String())
	}
	if chat.gotScope.All || chat.gotTenant != target {
		t.Fatalf("实测必须收到投影后的单一公司 scope: %+v", chat.gotScope)
	}

	before := chat.gotTenant
	response = aiRequest(router, http.MethodPost, "/rdc/models/id/test", `{}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("超管未指定公司实测应 400，实际 %d %s", response.Code, response.Body.String())
	}
	if chat.gotTenant != before {
		t.Fatal("400 分支不得继续调用实测服务")
	}
}

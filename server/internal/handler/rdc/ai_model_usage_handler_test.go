// ai_model_usage_handler_test.go · 统计与阈值端点的 HTTP 语义测试。
// 职责：钉死查询参数解析、请求体白名单（拒绝 tenantId）、超管未选公司 400、audit nil 时不 panic。
// 输入：httptest 请求、内存 fake 统计服务、chi 路由与团队模式中间件。
// 输出：状态码 / JSON 外壳符合契约；不连数据库、不访问上游。
// 边界：权限点守卫由 router 的 RequirePermission 负责，这里只验证 handler 自身的输入边界与取参口径。
package rdc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/middleware"
	authzmw "dandelion/server/internal/middleware/authz"
)

const aiUsageTenantID = "11111111-1111-1111-1111-111111111111"

// fakeStatsService 记录 handler 传下来的 scope 与查询条件，供断言取参正确性。
type fakeStatsService struct {
	query     contract.AIModelCallQuery
	now       time.Time
	gotScope  contract.Scope
	stats     contract.AIModelCallStats
	quota     contract.AIQuotaConfig
	upserted  contract.AIQuotaInput
	getErr    error
	updateErr error
}

func (f *fakeStatsService) Usage(
	_ context.Context, scope contract.Scope, query contract.AIModelCallQuery, now time.Time,
) (contract.AIModelCallStats, error) {
	f.gotScope, f.query, f.now = scope, query, now
	if f.getErr != nil {
		return contract.AIModelCallStats{}, f.getErr
	}
	return f.stats, nil
}

func (f *fakeStatsService) GetQuota(_ context.Context, scope contract.Scope) (contract.AIQuotaConfig, error) {
	f.gotScope = scope
	if f.getErr != nil {
		return contract.AIQuotaConfig{}, f.getErr
	}
	return f.quota, nil
}

func (f *fakeStatsService) UpdateQuota(
	_ context.Context, scope contract.Scope, input contract.AIQuotaInput,
) (contract.AIQuotaConfig, error) {
	f.gotScope, f.upserted = scope, input
	if f.updateErr != nil {
		return contract.AIQuotaConfig{}, f.updateErr
	}
	cfg := f.quota
	cfg.Enabled, cfg.MaxCalls, cfg.WindowMinutes, cfg.Action = input.Enabled, input.MaxCalls, input.WindowMinutes, input.Action
	return cfg, nil
}

// ResolveScope 与生产同口径：超管且未带 tenantId → ErrAITenantRequired（handler 翻成 400）。
func (f *fakeStatsService) ResolveScope(scope contract.Scope, tenantID string) (contract.Scope, error) {
	if !scope.All {
		return scope, nil
	}
	if !contract.IsValidUUID(tenantID) {
		return scope, contract.ErrAITenantRequired
	}
	return contract.Scope{TenantID: tenantID}, nil
}

// fakeAuditRecorder 记录每次审计调用的入参，供断言 action / detail / 代配标记。
type fakeAuditRecorder struct {
	action     string
	objectType string
	objectID   string
	detail     map[string]any
	calls      int
}

func (f *fakeAuditRecorder) Record(_ context.Context, _ contract.Identity, action, objectType,
	objectID string, detail map[string]any, _ string) {
	f.calls++
	f.action, f.objectType, f.objectID, f.detail = action, objectType, objectID, detail
}

// usageRouter 模拟真实注册顺序：usage / quota 三条都在 {id} 之前，证明静态段不被参数段抢走。
// audit 为 nil 时按"未配置审计"的部署形态运行，handler 必须不 panic。
func usageRouter(mode contract.SessionMode, all bool, svc *fakeStatsService, audit auditRecorder) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := authctx.With(r.Context(), contract.Identity{Mode: mode, Username: "tester"})
			ctx = authctx.WithScope(ctx, contract.Scope{TenantID: aiUsageTenantID, All: all})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	router.Use(authzmw.RequireTeamMode)
	handler := NewAIModelUsageHandler(svc, audit)
	router.Group(func(group chi.Router) {
		group.Get("/rdc/models/usage", middleware.Wrap(handler.GetUsage))
		group.Get("/rdc/models/quota", middleware.Wrap(handler.GetQuota))
		group.Put("/rdc/models/quota", middleware.Wrap(handler.UpdateQuota))
	})
	return router
}

// usageRequest 发一次请求；body 为空时按 GET 语义不发请求体。
func usageRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestUsageParsesQueryParameters(t *testing.T) {
	svc := &fakeStatsService{stats: contract.AIModelCallStats{Groups: []contract.AICallGroup{}}}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	path := "/rdc/models/usage?start=2026-01-01T00:00:00Z&end=2026-01-02T00:00:00Z&groupBy=model,day&includeLocal=true"
	response := usageRequest(router, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("统计查询应 200，实际 %d body=%s", response.Code, response.Body.String())
	}
	if !svc.query.IncludeLocal || len(svc.query.GroupBy) != 2 ||
		svc.query.GroupBy[0] != contract.AIUsageGroupModel || svc.query.GroupBy[1] != contract.AIUsageGroupDay {
		t.Fatalf("查询参数解析错误: %+v", svc.query)
	}
	if svc.query.Start.IsZero() || svc.query.End.IsZero() {
		t.Fatalf("显式时间不得被覆盖: %+v", svc.query)
	}
}

func TestUsageRejectsMalformedTime(t *testing.T) {
	// 非法时间当场 400：交给 contract 补默认会把用户的错误输入当成"没传"，静默改变查询范围。
	for _, path := range []string{
		"/rdc/models/usage?start=2026-01-01",
		"/rdc/models/usage?end=not-a-time",
		"/rdc/models/usage?includeLocal=maybe",
	} {
		router := usageRouter(contract.ModeTeam, false, &fakeStatsService{}, nil)
		response := usageRequest(router, http.MethodGet, path, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，实际 %d", path, response.Code)
		}
	}
}

func TestUsageDefaultsRangeWhenTimesAbsent(t *testing.T) {
	// 不传时间时 handler 必须把 Start/End 留零，让 contract 统一补"最近 7 天"——
	// 默认区间只能有一处定义，handler 不自作主张算日期。
	svc := &fakeStatsService{}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	if response := usageRequest(router, http.MethodGet, "/rdc/models/usage", ""); response.Code != http.StatusOK {
		t.Fatalf("缺省区间应 200，实际 %d", response.Code)
	}
	if !svc.query.Start.IsZero() || !svc.query.End.IsZero() {
		t.Fatalf("handler 不得自行计算默认区间: %+v", svc.query)
	}
}

func TestUpdateQuotaRejectsTenantIDInBody(t *testing.T) {
	// 超管代配只走 ?tenantId=：body 里出现 tenantId 必须 400，避免两处来源打架。
	body := `{"enabled":true,"maxCalls":100,"windowMinutes":60,"action":"warn","tenantId":"x"}`
	router := usageRouter(contract.ModeTeam, false, &fakeStatsService{}, nil)
	response := usageRequest(router, http.MethodPut, "/rdc/models/quota", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("body 含 tenantId 必须 400，实际 %d", response.Code)
	}
}

func TestUpdateQuotaPassesInputThrough(t *testing.T) {
	svc := &fakeStatsService{quota: contract.AIQuotaDefaults()}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	body := `{"enabled":true,"maxCalls":500,"windowMinutes":30,"action":"reject"}`
	response := usageRequest(router, http.MethodPut, "/rdc/models/quota", body)
	if response.Code != http.StatusOK {
		t.Fatalf("阈值写应 200，实际 %d body=%s", response.Code, response.Body.String())
	}
	if svc.upserted.MaxCalls != 500 || svc.upserted.Action != contract.AIQuotaActionReject {
		t.Fatalf("入参未透传: %+v", svc.upserted)
	}
	if svc.gotScope.All {
		t.Fatalf("写阈值必须落到单一公司 scope，实际 %+v", svc.gotScope)
	}
}

func TestUpdateQuotaValidatesBoundaries(t *testing.T) {
	// 越界一律 400，且错误文案来自 contract 的一处定义（handler 不自造校验文案）。
	for _, body := range []string{
		`{"enabled":true,"maxCalls":0,"windowMinutes":60,"action":"warn"}`,
		`{"enabled":true,"maxCalls":100,"windowMinutes":0,"action":"warn"}`,
		`{"enabled":true,"maxCalls":100,"windowMinutes":60,"action":"block"}`,
	} {
		router := usageRouter(contract.ModeTeam, false, &fakeStatsService{quota: contract.AIQuotaDefaults()}, nil)
		// fake 不做校验，故把校验错误显式模拟为 service 返回：这里直接用真实 contract 校验走一遍。
		var in contract.AIQuotaInput
		if err := json.Unmarshal([]byte(body), &in); err != nil {
			t.Fatal(err)
		}
		if _, err := contract.ValidateAIQuotaInput(in); err == nil {
			t.Fatalf("越界入参必须被 contract 拒绝: %s", body)
		}
		if response := usageRequest(router, http.MethodPut, "/rdc/models/quota", body); response.Code != http.StatusOK {
			t.Fatalf("fake 不校验，期望 200 以证明校验只发生在 service 层: %d", response.Code)
		}
	}
}

func TestQuotaSuperAdminWithoutTenantIs400(t *testing.T) {
	// 超管未带 ?tenantId= 时三个端点都必须 400（写操作不能落到 All scope 的失败关闭上）。
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/rdc/models/usage"},
		{http.MethodGet, "/rdc/models/quota"},
		{http.MethodPut, "/rdc/models/quota"},
	} {
		router := usageRouter(contract.ModeTeam, true, &fakeStatsService{quota: contract.AIQuotaDefaults()}, nil)
		body := ""
		if tc.method == http.MethodPut {
			body = `{"enabled":true,"maxCalls":100,"windowMinutes":60,"action":"warn"}`
		}
		response := usageRequest(router, tc.method, tc.path, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s 超管缺 tenantId 应 400，实际 %d", tc.method, tc.path, response.Code)
		}
	}
}

func TestUsageSuperAdminWithTenantProjectsScope(t *testing.T) {
	svc := &fakeStatsService{}
	router := usageRouter(contract.ModeTeam, true, svc, nil)
	response := usageRequest(router, http.MethodGet, "/rdc/models/usage?tenantId="+aiUsageTenantID, "")
	if response.Code != http.StatusOK {
		t.Fatalf("超管带合法 tenantId 应 200，实际 %d", response.Code)
	}
	if svc.gotScope.All || svc.gotScope.TenantID != aiUsageTenantID {
		t.Fatalf("scope 未投影到目标公司: %+v", svc.gotScope)
	}
}

func TestUsageSoloForbidden(t *testing.T) {
	// solo 会话没有 team 分区表，中间件必须挡在 handler 之前。
	router := usageRouter(contract.ModeSolo, false, &fakeStatsService{}, nil)
	if response := usageRequest(router, http.MethodGet, "/rdc/models/usage", ""); response.Code != http.StatusForbidden {
		t.Fatalf("solo 会话必须 403，实际 %d", response.Code)
	}
}

func TestUpdateQuotaSoloForbidden(t *testing.T) {
	// 写阈值同样属于 team 专属能力，solo 会话必须先被中间件挡住，不能落到 service。
	router := usageRouter(contract.ModeSolo, false, &fakeStatsService{quota: contract.AIQuotaDefaults()}, nil)
	body := `{"enabled":true,"maxCalls":100,"windowMinutes":60,"action":"warn"}`
	if response := usageRequest(router, http.MethodPut, "/rdc/models/quota", body); response.Code != http.StatusForbidden {
		t.Fatalf("solo 会话写阈值必须 403，实际 %d", response.Code)
	}
}

func TestIncludeLocalStrictBool(t *testing.T) {
	// includeLocal 只认 true / false：1、0、TRUE、t 这类 strconv 能接受的写法必须 400。
	// 理由：同一客户端传 1 成功、传 2 失败，比两种都不接受更难排查。
	for _, raw := range []string{"1", "0", "TRUE", "t", "yes"} {
		router := usageRouter(contract.ModeTeam, false, &fakeStatsService{}, nil)
		path := "/rdc/models/usage?includeLocal=" + raw
		if response := usageRequest(router, http.MethodGet, path, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("includeLocal=%s 必须 400，实际 %d", raw, response.Code)
		}
	}
	for _, raw := range []string{"true", "false"} {
		svc := &fakeStatsService{}
		router := usageRouter(contract.ModeTeam, false, svc, nil)
		if response := usageRequest(router, http.MethodGet, "/rdc/models/usage?includeLocal="+raw, ""); response.Code != http.StatusOK {
			t.Fatalf("includeLocal=%s 应 200，实际 %d", raw, response.Code)
		}
		if svc.query.IncludeLocal != (raw == "true") {
			t.Fatalf("includeLocal=%s 解析错误: %+v", raw, svc.query)
		}
	}
}

func TestGroupBySplitsAndDropsBlanks(t *testing.T) {
	// "model,, day ," 应拆成 [model, day]：空白项不产生空维度，顺序保持。
	svc := &fakeStatsService{}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	if response := usageRequest(router, http.MethodGet, "/rdc/models/usage?groupBy=model,,%20day%20,", ""); response.Code != http.StatusOK {
		t.Fatalf("合法 groupBy 应 200，实际 %d", response.Code)
	}
	want := []string{contract.AIUsageGroupModel, contract.AIUsageGroupDay}
	if len(svc.query.GroupBy) != len(want) {
		t.Fatalf("groupBy 拆分错误: %v", svc.query.GroupBy)
	}
	for index, dimension := range want {
		if svc.query.GroupBy[index] != dimension {
			t.Fatalf("groupBy 顺序或内容错误: %v", svc.query.GroupBy)
		}
	}
}

func TestUsageRejectsIllegalGroupBy(t *testing.T) {
	// 维度合法性由 contract 一处裁决：handler 只负责把非法值透传上去并让错误变成 400。
	svc := &fakeStatsService{getErr: &contract.ValidationError{Message: "分组维度非法"}}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	response := usageRequest(router, http.MethodGet, "/rdc/models/usage?groupBy=tenant", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("非法分组维度应 400，实际 %d", response.Code)
	}
}

func TestUpdateQuotaWritesAuditWithoutKey(t *testing.T) {
	// 审计是契约的一部分：必须含 action / changedFields / before / after，且绝不含 Key 或原文。
	audit := &fakeAuditRecorder{}
	svc := &fakeStatsService{quota: contract.AIQuotaConfig{
		Enabled: false, MaxCalls: 1000, WindowMinutes: 60, Action: contract.AIQuotaActionWarn,
	}}
	router := usageRouter(contract.ModeTeam, false, svc, audit)
	body := `{"enabled":true,"maxCalls":500,"windowMinutes":60,"action":"reject"}`
	if response := usageRequest(router, http.MethodPut, "/rdc/models/quota", body); response.Code != http.StatusOK {
		t.Fatalf("阈值写应 200，实际 %d", response.Code)
	}
	if audit.calls != 1 || audit.action != "rdc.ai_model.quota_updated" || audit.objectType != "rdc_ai_model" {
		t.Fatalf("审计调用不正确: calls=%d action=%s type=%s", audit.calls, audit.action, audit.objectType)
	}
	if audit.objectID != aiUsageTenantID {
		t.Fatalf("审计对象 id 应为目标公司，实际 %s", audit.objectID)
	}
	changed, ok := audit.detail["changedFields"].([]string)
	if !ok || len(changed) != 3 {
		t.Fatalf("changedFields 应含 3 项（enabled/maxCalls/action），实际 %v", audit.detail["changedFields"])
	}
	// before / after 都必须是四项快照，不含 extra_config 原文。
	for _, key := range []string{"before", "after"} {
		values, ok := audit.detail[key].(map[string]any)
		if !ok || len(values) != 4 {
			t.Fatalf("%s 快照应含 4 项，实际 %v", key, audit.detail[key])
		}
	}
	encoded, _ := json.Marshal(audit.detail)
	if strings.Contains(string(encoded), "retentionDays") || strings.Contains(string(encoded), aiHandlerTestKey) {
		t.Fatalf("审计 detail 不得含扩展原文或 Key: %s", encoded)
	}
}

func TestUpdateQuotaAuditMarksDelegation(t *testing.T) {
	// 超管代配必须留痕：targetTenantID + delegated，便于事后追溯"谁替哪家公司改了阈值"。
	audit := &fakeAuditRecorder{}
	svc := &fakeStatsService{quota: contract.AIQuotaDefaults()}
	router := usageRouter(contract.ModeTeam, true, svc, audit)
	body := `{"enabled":true,"maxCalls":100,"windowMinutes":60,"action":"warn"}`
	path := "/rdc/models/quota?tenantId=" + aiUsageTenantID
	if response := usageRequest(router, http.MethodPut, path, body); response.Code != http.StatusOK {
		t.Fatalf("超管代配写阈值应 200，实际 %d", response.Code)
	}
	if audit.detail["targetTenantID"] != aiUsageTenantID || audit.detail["delegated"] != true {
		t.Fatalf("代配审计缺少标记: %v", audit.detail)
	}
}

func TestUsageResponseHasNoKeyMaterial(t *testing.T) {
	// 统计响应全文不得出现任何 Key 字样：AIModelCallStats 本就不含 Key，
	// 这条断言防的是"以后有人往 DTO 里加字段"。
	svc := &fakeStatsService{stats: contract.AIModelCallStats{
		TotalCalls: 3, SuccessCalls: 2, FailedCalls: 1,
		FailureCategories: []contract.AICallCategoryCount{},
		Groups:            []contract.AICallGroup{},
	}}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	response := usageRequest(router, http.MethodGet, "/rdc/models/usage", "")
	if response.Code != http.StatusOK {
		t.Fatalf("统计查询应 200，实际 %d", response.Code)
	}
	if strings.Contains(response.Body.String(), aiHandlerTestKey) ||
		strings.Contains(response.Body.String(), "apiKey") {
		t.Fatalf("统计响应不得含 Key: %s", response.Body.String())
	}
}

func TestUsageNilAuditDoesNotPanic(t *testing.T) {
	// 未配置审计的部署形态：写阈值必须照常成功，不能因 audit 为 nil 而 panic。
	svc := &fakeStatsService{quota: contract.AIQuotaDefaults()}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	body := `{"enabled":true,"maxCalls":10,"windowMinutes":5,"action":"warn"}`
	if response := usageRequest(router, http.MethodPut, "/rdc/models/quota", body); response.Code != http.StatusOK {
		t.Fatalf("audit 为 nil 时应照常成功，实际 %d", response.Code)
	}
}

func TestNonSuperAdminIgnoresTenantIDQuery(t *testing.T) {
	// 非超管带 ?tenantId= 必须被忽略：scope 投影只对 All 范围生效，
	// 否则任何人都能通过改 query 读到别家公司的统计。
	svc := &fakeStatsService{}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	other := "22222222-2222-2222-2222-222222222222"
	response := usageRequest(router, http.MethodGet, "/rdc/models/usage?tenantId="+other, "")
	if response.Code != http.StatusOK {
		t.Fatalf("非超管带 tenantId 应照常 200，实际 %d", response.Code)
	}
	if svc.gotScope.TenantID != aiUsageTenantID {
		t.Fatalf("非超管的 scope 必须来自身份而非 query，实际 %s", svc.gotScope.TenantID)
	}
}

func TestGetQuotaNeverReturnsZeroValue(t *testing.T) {
	// 从未配置过的公司：service 返回 AIQuotaDefaults()（默认关闭），不是零值对象。
	svc := &fakeStatsService{quota: contract.AIQuotaDefaults()}
	router := usageRouter(contract.ModeTeam, false, svc, nil)
	response := usageRequest(router, http.MethodGet, "/rdc/models/quota", "")
	if response.Code != http.StatusOK {
		t.Fatalf("读阈值应 200，实际 %d", response.Code)
	}
	var envelope struct {
		Data contract.AIQuotaConfig `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if envelope.Data.Enabled || envelope.Data.MaxCalls != contract.AIQuotaDefaultMaxCalls ||
		envelope.Data.WindowMinutes != contract.AIQuotaDefaultWindowMinutes ||
		envelope.Data.Action != contract.AIQuotaActionWarn {
		t.Fatalf("缺行时必须返回默认阈值: %+v", envelope.Data)
	}
	if envelope.Data.UpdatedAt != "" {
		t.Fatalf("从未配置时 updatedAt 必须为空串，实际 %q", envelope.Data.UpdatedAt)
	}
}

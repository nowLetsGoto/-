// ai_model_qa_e2e_test.go · QA 独立验证：第 5 菜单「AI模型」真实 HTTP + 真实 PostgreSQL 攻击面测试。
// 职责：以 fresh eyes 验证 Key 加密/掩码/不回显、租户隔离与超管代配、读写权限、统一 Chat 出口、
//
//	错误分类与脱敏、extra_config 校验、思考/上下文注入、默认联动与软删。不是重跑实现方的内存替身单测。
//
// 输入：QA_AIMODEL_DB_URL 指向的专用空库；本地 httptest 冒充上游；固定假 Key（不含任何真实凭据）。
// 输出：HTTP 状态 / JSON 外壳 / DB 行 / mock 请求体的断言；所有 scnet 流量只落到本地 mock。
// 边界：只在专用库建表与清表；不碰真实 scnet、不改工作区源码、不 git add/commit；未设 DB_URL 则跳过。
package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	rdchandler "dandelion/server/internal/handler/rdc"
	"dandelion/server/internal/middleware"
	authzmw "dandelion/server/internal/middleware/authz"
	loginmw "dandelion/server/internal/middleware/login"
	"dandelion/server/internal/modelkey"
	"dandelion/server/internal/repository"
	auditrepo "dandelion/server/internal/repository/audit"
	peoplerepo "dandelion/server/internal/repository/people"
	projectrepo "dandelion/server/internal/repository/project"
	rdcrepo "dandelion/server/internal/repository/rdc"
	rolerepo "dandelion/server/internal/repository/role"
	teamsrepo "dandelion/server/internal/repository/teams"
	userrepo "dandelion/server/internal/repository/user"
	auditsvc "dandelion/server/internal/service/audit"
	authzsvc "dandelion/server/internal/service/authz"
	rdcsvc "dandelion/server/internal/service/rdc"
)

const (
	qamTenantA = "c0ffee11-1111-4111-8111-111111111111"
	qamTenantB = "c0ffee22-2222-4222-8222-222222222222"
	qamRoleRW  = "qam-model-rw"
	qamRoleRO  = "qam-model-ro"
	// 测试假 Key：只在本地测试与 mock 中出现，不是任何真实凭据。
	qamFakeKey  = "sk-qa-fake-abcdefghWXYZ"
	qamFakeKey2 = "sk-qa-fake-zzzz12345678"
	// 掩码规则：保留最后一个 '-' 及其前缀 + **** + 末 4 位。
	qamMask       = "sk-qa-fake-****WXYZ"
	qamTokenSuper = "qam-super"
	qamTokenRW    = "qam-rw-a"
	qamTokenRO    = "qam-ro-a"
	qamTokenNo    = "qam-none"
	qamTokenRWB   = "qam-rw-b"
	qamTokenSolo  = "qam-solo-super"
)

var qamMasterKey = bytes.Repeat([]byte{0x5a}, 32)

// qamEnv 持有专用库、真实业务链与只指向本机的 mock 上游。
type qamEnv struct {
	db       *sql.DB
	router   http.Handler
	chat     *rdcsvc.ModelChatService
	repo     *rdcrepo.AIModelRepository
	codec    *modelkey.Codec
	mock     *qamMock
	mockBase string
}

type qamMockCall struct {
	Path string
	Auth string
	Body map[string]any
	Raw  string
}

// qamMock 是本地 scnet 替身；scenario 由 URL 首段决定，绝不访问外网。
type qamMock struct {
	mu    sync.Mutex
	calls []qamMockCall
}

func (m *qamMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	call := qamMockCall{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Raw: string(raw)}
	_ = json.Unmarshal(raw, &call.Body)
	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()

	scenario := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
	w.Header().Set("Content-Type", "application/json")
	switch scenario {
	case "auth401":
		w.WriteHeader(http.StatusUnauthorized)
		// 故意回显假 Key，验证上游错误体绝不被透传/记录。
		_, _ = fmt.Fprintf(w, `{"error":{"message":"invalid key %s"}}`, qamFakeKey)
	case "param400", "param422":
		if scenario == "param400" {
			w.WriteHeader(http.StatusBadRequest)
		} else {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
		_, _ = fmt.Fprintf(w, `{"error":{"message":"unsupported %s"}}`, qamFakeKey)
	case "rate429":
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
	case "rate503":
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
	case "badjson":
		_, _ = io.WriteString(w, `not-json`)
	case "empty":
		_, _ = io.WriteString(w, `{"choices":[]}`)
	case "emptycontent":
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":""}}]}`)
	case "echo":
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":"echo %s done"}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`, qamFakeKey)
	case "slow":
		time.Sleep(1500 * time.Millisecond)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"late"}}]}`)
	default:
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"QA OK","reasoning_content":"QA think"}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
	}
}

func (m *qamMock) last() qamMockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return qamMockCall{}
	}
	return m.calls[len(m.calls)-1]
}

func (m *qamMock) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// qamEnvSetup 连专用库、装幂等 schema、种两家公司与读写角色，并搭真实中间件链。
func qamEnvSetup(t *testing.T) *qamEnv {
	t.Helper()
	dsn := os.Getenv("QA_AIMODEL_DB_URL")
	if dsn == "" {
		t.Skip("未设置 QA_AIMODEL_DB_URL，跳过真实数据库攻击面测试")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("连接专用库失败: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping 专用库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := repository.EnsureSchema(ctx, db); err != nil {
		t.Fatalf("幂等建 schema 失败: %v", err)
	}
	qamSeed(t, db)

	mock := &qamMock{}
	upstream := httptest.NewServer(mock)
	t.Cleanup(upstream.Close)

	codec, err := modelkey.New(qamMasterKey)
	if err != nil {
		t.Fatalf("构造测试 codec 失败: %v", err)
	}
	repo := rdcrepo.NewAIModelRepository(db)
	svc := rdcsvc.NewModelService(repo, codec)
	chat := rdcsvc.NewModelChatService(
		repo, rdcrepo.NewAIModelCallRepository(db), rdcrepo.NewAIModelQuotaRepository(db),
		codec, &http.Client{Timeout: contract.ModelCallHardTimeout})
	audit := auditsvc.NewService(auditrepo.NewPartitioned(db))
	modelHandler := rdchandler.NewAIModelHandler(svc, chat, audit)
	stats := rdcsvc.NewModelStatsService(rdcrepo.NewAIModelCallRepository(db), rdcrepo.NewAIModelQuotaRepository(db))
	usageHandler := rdchandler.NewAIModelUsageHandler(stats, audit)

	authz := authzsvc.NewService(
		userrepo.NewRepository(db), peoplerepo.NewRepository(db),
		repository.Partitioned[*projectrepo.Repository]{}, rolerepo.NewRepository(db), teamsrepo.NewRepository(db),
	)
	verify := func(_ context.Context, token string) (contract.Identity, bool) {
		switch token {
		case qamTokenSuper:
			return contract.Identity{UserID: "90909090-9090-4090-8090-909090909090", Username: "qam-super", Mode: contract.ModeTeam, IsSuperAdmin: true}, true
		case qamTokenSolo:
			return contract.Identity{UserID: "90909090-9090-4090-8090-909090909090", Username: "qam-super", Mode: contract.ModeSolo, IsSuperAdmin: true}, true
		case qamTokenRW:
			return contract.Identity{UserID: "f0ffee11-1111-4111-8111-111111111111", Username: "qam-rw-a", Mode: contract.ModeTeam,
				TenantID: qamTenantA, AccountLevel: contract.AccountLevelPlatformAdmin}, true
		case qamTokenRO:
			return contract.Identity{UserID: "f0ffee22-2222-4222-8222-222222222222", Username: "qam-ro-a", Mode: contract.ModeTeam,
				TenantID: qamTenantA, AccountLevel: contract.AccountLevelTeamAdmin}, true
		case qamTokenRWB:
			return contract.Identity{UserID: "f0ffee33-3333-4333-8333-333333333333", Username: "qam-rw-b", Mode: contract.ModeTeam,
				TenantID: qamTenantB, AccountLevel: contract.AccountLevelPlatformAdmin}, true
		case qamTokenNo:
			return contract.Identity{UserID: "f0ffee44-4444-4444-8444-444444444444", Username: "qam-none", Mode: contract.ModeTeam,
				TenantID: qamTenantA, AccountLevel: contract.AccountLevelMember}, true
		}
		return contract.Identity{}, false
	}

	// 路由守卫逐条镜像 app/router.go 的第 5 菜单分组，用的是真实中间件与真实 handler。
	r := chi.NewRouter()
	r.Route(contract.APIPrefix, func(api chi.Router) {
		api.Group(func(models chi.Router) {
			models.Use(loginmw.RequireAuth(verify))
			models.Use(loginmw.InjectScope(authz))
			models.Use(authzmw.RequireTeamMode)
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelRead)).
				Get("/rdc/models", middleware.Wrap(modelHandler.List))
			// 静态段先注册，镜像生产路由顺序（usage / quota 在 {id} 之前）。
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelRead)).
				Get("/rdc/models/usage", middleware.Wrap(usageHandler.GetUsage))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelRead)).
				Get("/rdc/models/quota", middleware.Wrap(usageHandler.GetQuota))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelWrite)).
				Put("/rdc/models/quota", middleware.Wrap(usageHandler.UpdateQuota))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelRead)).
				Get("/rdc/models/{id}", middleware.Wrap(modelHandler.Get))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelWrite)).
				Post("/rdc/models", middleware.Wrap(modelHandler.Create))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelWrite)).
				Put("/rdc/models/{id}", middleware.Wrap(modelHandler.Update))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelWrite)).
				Post("/rdc/models/{id}/default", middleware.Wrap(modelHandler.SetDefault))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelWrite)).
				Post("/rdc/models/{id}/status", middleware.Wrap(modelHandler.SetEnabled))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelWrite)).
				Delete("/rdc/models/{id}", middleware.Wrap(modelHandler.Delete))
			models.With(authzmw.RequirePermission(authz, contract.PermissionRDCModelWrite)).
				Post("/rdc/models/{id}/test", middleware.Wrap(modelHandler.Test))
		})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Allow", allowedMethodsFor(req))
		handler.WriteError(w, req, handler.MethodNotAllowed("请求方法不支持"))
	})
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		handler.WriteError(w, req, handler.NotFound("接口不存在"))
	})

	env := &qamEnv{db: db, router: r, chat: chat, repo: repo, mock: mock, codec: codec}
	env.mockBase = upstream.URL
	return env
}

func qamSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	statements := []struct {
		sql  string
		args []any
	}{
		{`TRUNCATE team.rdc_ai_model, team.audit_logs, public.role_permissions, public.roles,
			public.users, public.people, public.tenants CASCADE`, nil},
		{`INSERT INTO public.tenants(id,name,code) VALUES
			($1,'QAM-A','QAM-A'),($2,'QAM-B','QAM-B')`, []any{qamTenantA, qamTenantB}},
		{`INSERT INTO public.roles(id,key,name,tenant_id) VALUES
			('d0ffee11-1111-4111-8111-111111111111',$3,'qa rw',$1),
			('d0ffee22-2222-4222-8222-222222222222',$4,'qa ro',$1),
			('d0ffee33-3333-4333-8333-333333333333','qam-model-rw-b','qa rw b',$2)`,
			[]any{qamTenantA, qamTenantB, qamRoleRW, qamRoleRO}},
		{`INSERT INTO public.role_permissions(role_id,permission) VALUES
			('d0ffee11-1111-4111-8111-111111111111','rdc.model.read'),
			('d0ffee11-1111-4111-8111-111111111111','rdc.model.write'),
			('d0ffee33-3333-4333-8333-333333333333','rdc.model.read'),
			('d0ffee33-3333-4333-8333-333333333333','rdc.model.write'),
			('d0ffee22-2222-4222-8222-222222222222','rdc.model.read')`, nil},
		{`INSERT INTO public.people(id,name,username,role_id,tenant_id) VALUES
			('e0ffee11-1111-4111-8111-111111111111','qa rw a','qam-rw-a','d0ffee11-1111-4111-8111-111111111111',$1),
			('e0ffee22-2222-4222-8222-222222222222','qa ro a','qam-ro-a','d0ffee22-2222-4222-8222-222222222222',$1),
			('e0ffee33-3333-4333-8333-333333333333','qa rw b','qam-rw-b','d0ffee33-3333-4333-8333-333333333333',$2)`,
			[]any{qamTenantA, qamTenantB}},
		{`INSERT INTO public.users(id,username,password_hash,mode,person_id,account_level,tenant_id) VALUES
			('f0ffee11-1111-4111-8111-111111111111','qam-rw-a','x','team','e0ffee11-1111-4111-8111-111111111111','platform_admin',$1),
			('f0ffee22-2222-4222-8222-222222222222','qam-ro-a','x','team','e0ffee22-2222-4222-8222-222222222222','team_admin',$1),
			('f0ffee33-3333-4333-8333-333333333333','qam-rw-b','x','team','e0ffee33-3333-4333-8333-333333333333','platform_admin',$2)`,
			[]any{qamTenantA, qamTenantB}},
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("种子数据失败: %v sql=%s", err, stmt.sql)
		}
	}
}

// qamRequest 发一个真实 HTTP 请求，返回状态、解析后的外壳与原文。
func qamRequest(t *testing.T, env *qamEnv, token, method, path, body string) (int, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(method, contract.APIPrefix+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	var envelope map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
	return rec.Code, envelope, rec.Body.String()
}

// qamModelInput 构造一份合法的新建/编辑请求体。
func qamModelInput(name, baseURL, apiKey, extra string, thinking bool, contextTokens int) string {
	if extra == "" {
		extra = `{}`
	}
	payload := map[string]any{
		"name": name, "provider": "scnet", "protocol": "openai", "baseUrl": baseURL,
		"modelId": "DeepSeek-QA", "thinkingEnabled": thinking, "contextTokens": contextTokens,
		"extraConfig": json.RawMessage(extra), "capabilities": []string{"文本生成"},
	}
	if apiKey != "" {
		payload["apiKey"] = apiKey
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func qamCreateModel(t *testing.T, env *qamEnv, token, tenantParam, name, baseURL, key, extra string, thinking bool, tokens int) string {
	t.Helper()
	path := "/rdc/models" + tenantParam
	status, envelope, raw := qamRequest(t, env, token, http.MethodPost, path,
		qamModelInput(name, baseURL, key, extra, thinking, tokens))
	if status != http.StatusCreated {
		t.Fatalf("新建模型失败: status=%d body=%s", status, raw)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("新建响应结构错误: %s", raw)
	}
	return fmt.Sprint(data["id"])
}

func qamEnvelopeData(t *testing.T, envelope map[string]any, raw string) map[string]any {
	t.Helper()
	if envelope["code"] != float64(0) {
		t.Fatalf("响应 code 非 0: %s", raw)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应 data 不是对象: %s", raw)
	}
	return data
}

func qamDBString(t *testing.T, env *qamEnv, query string, args ...any) string {
	t.Helper()
	var out string
	if err := env.db.QueryRowContext(context.Background(), query, args...).Scan(&out); err != nil {
		t.Fatalf("查库失败: %v sql=%s", err, query)
	}
	return out
}

// ---------- 1. Key 加密、掩码、不回显、留空不更新 ----------

func TestQAModel_KeyEncryptionMaskAndNoEcho(t *testing.T) {
	env := qamEnvSetup(t)
	id := qamCreateModel(t, env, qamTokenRW, "", "qa-key-model", "https://mock.local/v1", qamFakeKey, `{}`, false, 8192)

	var cipherB64, nonceB64, mask string
	if err := env.db.QueryRowContext(context.Background(),
		`SELECT api_key_ciphertext, api_key_nonce, api_key_mask FROM team.rdc_ai_model WHERE id::text=$1`, id).
		Scan(&cipherB64, &nonceB64, &mask); err != nil {
		t.Fatalf("读取密文失败: %v", err)
	}
	if cipherB64 == "" || nonceB64 == "" {
		t.Fatal("Key 必须加密入库，密文/nonce 不可为空")
	}
	if strings.Contains(cipherB64, qamFakeKey) || strings.Contains(nonceB64, qamFakeKey) {
		t.Fatal("数据库列不得出现 Key 明文")
	}
	decodedNonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil || len(decodedNonce) != 12 {
		t.Fatalf("nonce 必须是 base64 的 12 字节: len=%d err=%v", len(decodedNonce), err)
	}
	plain, err := env.codec.Open(cipherB64, nonceB64)
	if err != nil || plain != qamFakeKey {
		t.Fatalf("密文无法正确解密: err=%v", err)
	}
	if mask != qamMask {
		t.Fatalf("掩码错误: got=%q want=%q", mask, qamMask)
	}

	// 列表 / 详情 / 编辑回显都只有掩码，全文找不到完整 Key，也没有任何完整 Key JSON 字段。
	for _, path := range []string{"/rdc/models", "/rdc/models/" + id} {
		status, envelope, raw := qamRequest(t, env, qamTokenRW, http.MethodGet, path, "")
		if status != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, status, raw)
		}
		if strings.Contains(raw, qamFakeKey) || strings.Contains(raw, cipherB64) || strings.Contains(raw, nonceB64) {
			t.Fatalf("%s 响应泄漏 Key/密文: %s", path, raw)
		}
		if regexp.MustCompile(`"apiKey"\s*:`).MatchString(raw) {
			t.Fatalf("%s 响应含完整 Key 字段: %s", path, raw)
		}
		qamEnvelopeData(t, envelope, raw)
	}

	// 编辑 Key 留空 / 显式空串：三列保持不变；填新 Key 才轮换。
	for _, patch := range []string{
		qamModelInput("qa-key-model", "https://mock.local/v1", "", `{}`, false, 8192),
		strings.Replace(qamModelInput("qa-key-model", "https://mock.local/v1", qamFakeKey2, `{}`, false, 8192),
			fmt.Sprintf(`"apiKey":%q`, qamFakeKey2), `"apiKey":""`, 1),
	} {
		status, envelope, raw := qamRequest(t, env, qamTokenRW, http.MethodPut, "/rdc/models/"+id, patch)
		if status != http.StatusOK {
			t.Fatalf("留空更新失败: status=%d body=%s", status, raw)
		}
		data := qamEnvelopeData(t, envelope, raw)
		if data["apiKeyMask"] != qamMask {
			t.Fatalf("留空更新后掩码被改写: %v", data["apiKeyMask"])
		}
		var after string
		_ = env.db.QueryRowContext(context.Background(),
			`SELECT api_key_ciphertext FROM team.rdc_ai_model WHERE id::text=$1`, id).Scan(&after)
		if after != cipherB64 {
			t.Fatal("留空更新改写了密文")
		}
	}

	status, envelope, raw := qamRequest(t, env, qamTokenRW, http.MethodPut, "/rdc/models/"+id,
		qamModelInput("qa-key-model", "https://mock.local/v1", qamFakeKey2, `{}`, false, 8192))
	if status != http.StatusOK {
		t.Fatalf("换 Key 失败: %d %s", status, raw)
	}
	data := qamEnvelopeData(t, envelope, raw)
	if data["apiKeyMask"] == qamMask || strings.Contains(raw, qamFakeKey2) {
		t.Fatalf("换 Key 后掩码/响应错误: %s", raw)
	}

	// 审计 detail 只记录布尔事实，不记 Key 值。
	details := qamDBString(t, env,
		`SELECT coalesce(string_agg(detail::text,' | '),'') FROM team.audit_logs WHERE object_id::text=$1`, id)
	if strings.Contains(details, qamFakeKey) || strings.Contains(details, qamFakeKey2) || strings.Contains(details, cipherB64) {
		t.Fatalf("审计 detail 泄漏 Key/密文: %s", details)
	}
	if !strings.Contains(details, "apiKeyUpdated") {
		t.Fatalf("审计 detail 缺少 apiKeyUpdated: %s", details)
	}
}

// ---------- 2. 权限、隔离、超管代配 ----------

func TestQAModel_PermissionsTenantIsolationAndDelegation(t *testing.T) {
	env := qamEnvSetup(t)
	idA := qamCreateModel(t, env, qamTokenRW, "", "qa-A", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192)
	idB := qamCreateModel(t, env, qamTokenRWB, "", "qa-B", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192)

	writes := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/rdc/models", qamModelInput("x", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192)},
		{http.MethodPut, "/rdc/models/" + idA, qamModelInput("x", env.mockBase+"/ok", "", `{}`, false, 8192)},
		{http.MethodPost, "/rdc/models/" + idA + "/default", `{}`},
		{http.MethodPost, "/rdc/models/" + idA + "/status", `{"enabled":false}`},
		{http.MethodDelete, "/rdc/models/" + idA, ``},
		{http.MethodPost, "/rdc/models/" + idA + "/test", `{}`},
	}
	reads := []struct{ method, path string }{
		{http.MethodGet, "/rdc/models"},
		{http.MethodGet, "/rdc/models/" + idA},
	}

	// solo 超管 8 操作全 403；无权限账号 8 操作全 403。
	for _, token := range []string{qamTokenSolo, qamTokenNo} {
		for _, op := range reads {
			status, _, _ := qamRequest(t, env, token, op.method, op.path, "")
			if status != http.StatusForbidden {
				t.Fatalf("token=%s %s %s 期望 403，实际 %d", token, op.method, op.path, status)
			}
		}
		for _, op := range writes {
			status, _, _ := qamRequest(t, env, token, op.method, op.path, op.body)
			if status != http.StatusForbidden {
				t.Fatalf("token=%s %s %s 期望 403，实际 %d", token, op.method, op.path, status)
			}
		}
	}

	// 只读角色：2 读 200、6 写 403。
	for _, op := range reads {
		status, _, _ := qamRequest(t, env, qamTokenRO, op.method, op.path, "")
		if status != http.StatusOK {
			t.Fatalf("只读账号 %s 期望 200，实际 %d", op.path, status)
		}
	}
	for _, op := range writes {
		status, _, _ := qamRequest(t, env, qamTokenRO, op.method, op.path, op.body)
		if status != http.StatusForbidden {
			t.Fatalf("只读账号写 %s 期望 403，实际 %d", op.path, status)
		}
	}

	// 跨公司：A 用 B 的 id 读写/实测/设默认/启停/软删一律 404。
	for _, op := range writes[1:] {
		status, _, _ := qamRequest(t, env, qamTokenRW, op.method, strings.Replace(op.path, idA, idB, 1), op.body)
		if status != http.StatusNotFound {
			t.Fatalf("跨公司 %s %s 期望 404，实际 %d", op.method, op.path, status)
		}
	}
	status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodGet, "/rdc/models/"+idB, "")
	if status != http.StatusNotFound {
		t.Fatalf("跨公司详情期望 404，实际 %d %s", status, raw)
	}

	// 超管代配：不指定 tenantId 列表空、写 400；指定后命中目标公司。
	status, envelope, raw := qamRequest(t, env, qamTokenSuper, http.MethodGet, "/rdc/models", "")
	data := qamEnvelopeData(t, envelope, raw)
	if status != http.StatusOK || len(data["items"].([]any)) != 0 {
		t.Fatalf("超管未指定公司列表必须为空: %d %s", status, raw)
	}
	for _, op := range writes {
		status, _, _ := qamRequest(t, env, qamTokenSuper, op.method, op.path, op.body)
		if status != http.StatusBadRequest {
			t.Fatalf("超管未指定公司写 %s 期望 400，实际 %d", op.path, status)
		}
	}
	for _, tc := range []struct{ tenant, want string }{{qamTenantA, idA}, {qamTenantB, idB}} {
		status, envelope, raw = qamRequest(t, env, qamTokenSuper, http.MethodGet,
			"/rdc/models?tenantId="+tc.tenant, "")
		data = qamEnvelopeData(t, envelope, raw)
		items := data["items"].([]any)
		if status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["id"] != tc.want {
			t.Fatalf("超管代配列表错误: tenant=%s %d %s", tc.tenant, status, raw)
		}
	}
	// 实测是写操作，超管代配同样必须先投影 tenantId：带参数应命中目标公司并返回脱敏结果。
	status, envelope, raw = qamRequest(t, env, qamTokenSuper, http.MethodPost,
		"/rdc/models/"+idA+"/test?tenantId="+qamTenantA, `{}`)
	if status != http.StatusOK {
		t.Fatalf("超管代配实测应命中目标公司返回 200，实际 %d %s", status, raw)
	}
	qamEnvelopeData(t, envelope, raw)

	// 非超管带 tenantId 被忽略：仍操作自己公司，看不到 B 的模型。
	status, envelope, raw = qamRequest(t, env, qamTokenRW, http.MethodGet, "/rdc/models?tenantId="+qamTenantB, "")
	data = qamEnvelopeData(t, envelope, raw)
	items := data["items"].([]any)
	if status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["id"] != idA {
		t.Fatalf("非超管 tenantId 未被忽略: %d %s", status, raw)
	}

	// 代配写操作审计必须带 targetTenantID / delegated。
	qamRequest(t, env, qamTokenSuper, http.MethodPost, "/rdc/models?tenantId="+qamTenantA,
		qamModelInput("qa-delegate", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192))
	detail := qamDBString(t, env, `SELECT detail::text FROM team.audit_logs WHERE action='rdc.ai_model.created' ORDER BY id DESC LIMIT 1`)
	if !strings.Contains(detail, qamTenantA) || !strings.Contains(detail, "delegated") {
		t.Fatalf("代配审计缺少 targetTenantID/delegated: %s", detail)
	}

	// 405 + Allow 与生产路由同口径。
	req := httptest.NewRequest(http.MethodPatch, contract.APIPrefix+"/rdc/models", nil)
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "POST") {
		t.Fatalf("405/Allow 错误: %d allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}

// ---------- 3. 统一 Chat：出口、参数注入、错误分类、脱敏 ----------

func TestQAModel_ChatInjectionErrorsAndRedaction(t *testing.T) {
	env := qamEnvSetup(t)
	chatID := qamCreateModel(t, env, qamTokenRW, "", "qa-chat", env.mockBase+"/ok", qamFakeKey,
		`{"temperature":0,"topP":0.2,"reasoningEffort":"max","timeoutMs":1000}`, false, 1024)

	// ChatRequest 的结构边界：只允许「选择条件」类字段，绝不允许 Key / base_url / 凭据。
	// 用白名单而非固定三字段，是因为 P1 加了三个可选需求字段（Capabilities / MinContextTokens /
	// ReasoningEffort）—— 它们只是"要什么样的模型"，不含任何凭据。安全边界靠"禁凭据字段名"保证，
	// 不靠"禁新增字段"，否则每加一个正当字段都要改断言，久而久之会被顺手放宽。
	allowed := map[string]bool{
		"TenantID": true, "Scene": true, "Messages": true,
		"Capabilities": true, "MinContextTokens": true, "ReasoningEffort": true,
	}
	forbiddenWords := []string{"Key", "URL", "Ciphertext", "Nonce", "Token", "Secret", "Password"}
	requestType := reflect.TypeOf(rdcsvc.ChatRequest{})
	for i := 0; i < requestType.NumField(); i++ {
		name := requestType.Field(i).Name
		if !allowed[name] {
			t.Fatalf("ChatRequest 出现白名单外字段: %s", name)
		}
		for _, word := range forbiddenWords {
			// MinContextTokens 是显式例外：它是"最小上下文窗口"这个需求条件，不是凭据。
			if name != "MinContextTokens" && strings.Contains(name, word) {
				t.Fatalf("ChatRequest 不得出现凭据类字段: %s", name)
			}
		}
	}
	if requestType.NumField() != len(allowed) {
		t.Fatalf("ChatRequest 字段数应为 %d，实际 %d", len(allowed), requestType.NumField())
	}

	result, err := env.chat.Chat(context.Background(), rdcsvc.ChatRequest{
		TenantID: qamTenantA, Scene: "qa", Messages: []rdcsvc.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if result.Content != "QA OK" || result.ReasoningContent != "QA think" || result.PromptTokens != 11 || result.CompletionTokens != 7 {
		t.Fatalf("Chat 结果错误: %+v", result)
	}
	call := env.mock.last()
	if !strings.HasSuffix(call.Path, "/ok/chat/completions") || call.Auth != "Bearer "+qamFakeKey {
		t.Fatalf("mock 收到错误的上游请求: path=%s auth=%s", call.Path, call.Auth)
	}
	for key, want := range map[string]float64{
		"temperature": 0, "top_p": 0.2, "max_tokens": 1024,
	} {
		if got, ok := call.Body[key].(float64); !ok || got != want {
			t.Fatalf("请求体 %s=%v 期望 %v: %s", key, call.Body[key], want, call.Raw)
		}
	}
	if call.Body["reasoning_effort"] != "none" {
		t.Fatalf("关思考必须注入 none: %s", call.Raw)
	}
	if strings.Contains(call.Raw, qamFakeKey) {
		t.Fatal("Key 只允许出现在 Authorization 头，不得进请求体")
	}

	// 开思考 + 大上下文 + 高 effort：映射按配置生效。
	status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodPut, "/rdc/models/"+chatID,
		qamModelInput("qa-chat", env.mockBase+"/ok", "", `{"reasoningEffort":"high","timeoutMs":5000}`, true, 393216))
	if status != http.StatusOK {
		t.Fatalf("改思考配置失败: %d %s", status, raw)
	}
	if _, err := env.chat.Chat(context.Background(), rdcsvc.ChatRequest{TenantID: qamTenantA, Messages: []rdcsvc.ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("Chat(思考开启) 失败: %v", err)
	}
	call = env.mock.last()
	if call.Body["reasoning_effort"] != "high" || call.Body["max_tokens"] != float64(393216) {
		t.Fatalf("思考/上下文注入错误: %s", call.Raw)
	}

	// 六类错误：每类用独立 base_url 场景，Key 故意由 mock 回显在错误体里。
	cases := []struct {
		scenario string
		want     contract.AIChatCategory
	}{
		{"auth401", contract.AIChatAuthFailed},
		{"param400", contract.AIChatBadParameter},
		{"param422", contract.AIChatBadParameter},
		{"rate429", contract.AIChatRateLimited},
		{"rate503", contract.AIChatRateLimited},
		{"badjson", contract.AIChatBadResponse},
		{"empty", contract.AIChatBadResponse},
		{"emptycontent", contract.AIChatBadResponse},
		{"slow", contract.AIChatNetwork},
	}
	for _, tc := range cases {
		id := qamCreateModel(t, env, qamTokenRW, "", "qa-err-"+tc.scenario,
			env.mockBase+"/"+tc.scenario, qamFakeKey, `{"timeoutMs":1000}`, false, 8192)
		status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodPost,
			"/rdc/models/"+id+"/default", `{}`)
		if status != http.StatusOK {
			t.Fatalf("设默认失败 %s: %d %s", tc.scenario, status, raw)
		}
		_, err := env.chat.Chat(context.Background(), rdcsvc.ChatRequest{TenantID: qamTenantA})
		chatErr, ok := err.(*rdcsvc.ChatError)
		if !ok || chatErr.Category != tc.want {
			t.Fatalf("%s 期望分类 %s，实际 %v", tc.scenario, tc.want, err)
		}
		if strings.Contains(chatErr.Error(), qamFakeKey) || strings.Contains(fmt.Sprint(chatErr.Cause), qamFakeKey) {
			t.Fatalf("%s 错误信息泄漏 Key: %v", tc.scenario, chatErr)
		}
		// 实测端点与 Chat 共用实现：失败也是 200 + 分类结果，且响应无 Key。
		status, _, testRaw := qamRequest(t, env, qamTokenRW, http.MethodPost,
			"/rdc/models/"+id+"/test", `{}`)
		if status != http.StatusOK || !strings.Contains(testRaw, string(tc.want)) || strings.Contains(testRaw, qamFakeKey) {
			t.Fatalf("%s 实测结果错误: %d %s", tc.scenario, status, testRaw)
		}
	}

	// 成功响应里若上游回显 Key，Chat / Test 摘要都必须脱敏。
	echoID := qamCreateModel(t, env, qamTokenRW, "", "qa-echo", env.mockBase+"/echo", qamFakeKey, `{}`, false, 8192)
	status, _, raw = qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models/"+echoID+"/default", `{}`)
	if status != http.StatusOK {
		t.Fatalf("设默认失败: %d %s", status, raw)
	}
	res, err := env.chat.Chat(context.Background(), rdcsvc.ChatRequest{TenantID: qamTenantA})
	if err != nil || strings.Contains(res.Content, qamFakeKey) {
		t.Fatalf("Chat 内容未脱敏: %+v err=%v", res, err)
	}
	status, _, raw = qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models/"+echoID+"/test", `{}`)
	if status != http.StatusOK || strings.Contains(raw, qamFakeKey) {
		t.Fatalf("实测响应未脱敏: %d %s", status, raw)
	}
}

// ---------- 4. extra_config / context / 默认联动 ----------

func TestQAModel_ExtraConfigContextAndDefaultLifecycle(t *testing.T) {
	env := qamEnvSetup(t)
	bad := []struct{ name, extra string }{
		{"未知键", `{"foo":1}`},
		{"温度越界", `{"temperature":2.1}`},
		{"topP 为零", `{"topP":0}`},
		{"effort 非法", `{"reasoningEffort":"ultra"}`},
		{"timeout 过小", `{"timeoutMs":999}`},
		{"timeout 过大", `{"timeoutMs":120001}`},
	}
	for _, tc := range bad {
		status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models",
			qamModelInput("qa-bad", env.mockBase+"/ok", qamFakeKey, tc.extra, false, 8192))
		if status != http.StatusBadRequest {
			t.Fatalf("%s 期望 400，实际 %d %s", tc.name, status, raw)
		}
	}
	for _, tokens := range []int{1023, 393217} {
		status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models",
			qamModelInput("qa-bad", env.mockBase+"/ok", qamFakeKey, `{}`, false, tokens))
		if status != http.StatusBadRequest {
			t.Fatalf("contextTokens=%d 期望 400，实际 %d %s", tokens, status, raw)
		}
	}
	for _, tokens := range []int{1024, 393216} {
		status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models",
			qamModelInput("qa-bound", env.mockBase+"/ok", qamFakeKey, `{}`, false, tokens))
		if status != http.StatusCreated {
			t.Fatalf("contextTokens=%d 应为合法边界，实际 %d %s", tokens, status, raw)
		}
	}

	// 缺省补默认；读取时忽略历史未知键。
	id := qamCreateModel(t, env, qamTokenRW, "", "qa-defaults", env.mockBase+"/ok", qamFakeKey, `{}`, false, 0)
	_, envelope, raw := qamRequest(t, env, qamTokenRW, http.MethodGet, "/rdc/models/"+id, "")
	data := qamEnvelopeData(t, envelope, raw)
	extra := data["extraConfig"].(map[string]any)
	for key, want := range map[string]float64{"temperature": 1, "topP": 1, "timeoutMs": 30000} {
		if extra[key] != want {
			t.Fatalf("extraConfig 默认值错误: %s=%v want=%v (%s)", key, extra[key], want, raw)
		}
	}
	if extra["reasoningEffort"] != "high" || data["contextTokens"] != float64(8192) {
		t.Fatalf("默认值错误: %s", raw)
	}
	if _, err := env.db.ExecContext(context.Background(),
		`UPDATE team.rdc_ai_model SET extra_config='{"temperature":0.7,"legacy":true}'::jsonb WHERE id::text=$1`, id); err != nil {
		t.Fatalf("模拟历史数据失败: %v", err)
	}
	_, envelope, raw = qamRequest(t, env, qamTokenRW, http.MethodGet, "/rdc/models/"+id, "")
	data = qamEnvelopeData(t, envelope, raw)
	extra = data["extraConfig"].(map[string]any)
	if _, ok := extra["legacy"]; ok || extra["temperature"] != 0.7 || extra["reasoningEffort"] != "high" {
		t.Fatalf("详情读取规范化错误: %v", extra)
	}
	_, envelope, raw = qamRequest(t, env, qamTokenRW, http.MethodGet, "/rdc/models?pageSize=0", "")
	data = qamEnvelopeData(t, envelope, raw)
	for _, item := range data["items"].([]any) {
		row := item.(map[string]any)
		if row["id"] != id {
			continue
		}
		listExtra := row["extraConfig"].(map[string]any)
		if _, ok := listExtra["legacy"]; ok || listExtra["reasoningEffort"] != "high" {
			t.Fatalf("列表读取规范化错误: %v", listExtra)
		}
	}

	// 首个启用自动默认；切换默认事务互斥；并发切换后恰好一个默认。
	first := qamDBString(t, env, `SELECT id::text FROM team.rdc_ai_model WHERE tenant_id=$1 AND is_default`, qamTenantA)
	if first == "" {
		t.Fatal("首个启用模型必须自动成为默认")
	}
	m1 := qamCreateModel(t, env, qamTokenRW, "", "qa-m1", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192)
	m2 := qamCreateModel(t, env, qamTokenRW, "", "qa-m2", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192)
	var wg sync.WaitGroup
	for _, target := range []string{m1, m2, m1, m2} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models/"+id+"/default", `{}`)
		}(target)
	}
	wg.Wait()
	count := qamDBString(t, env, `SELECT count(*)::text FROM team.rdc_ai_model WHERE tenant_id=$1 AND is_default`, qamTenantA)
	if count != "1" {
		t.Fatalf("并发设默认后默认数量=%s，期望 1", count)
	}

	// 停用默认且另有启用 → 400；软删默认同样拦截。
	defaultID := qamDBString(t, env, `SELECT id::text FROM team.rdc_ai_model WHERE tenant_id=$1 AND is_default`, qamTenantA)
	status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models/"+defaultID+"/status", `{"enabled":false}`)
	if status != http.StatusBadRequest {
		t.Fatalf("停用默认且另有启用期望 400，实际 %d %s", status, raw)
	}
	status, _, raw = qamRequest(t, env, qamTokenRW, http.MethodDelete, "/rdc/models/"+defaultID, "")
	if status != http.StatusBadRequest {
		t.Fatalf("软删默认且另有启用期望 400，实际 %d %s", status, raw)
	}

	// 无默认但有启用 → Chat 走兜底并标记 UsedFallback。
	if _, err := env.db.ExecContext(context.Background(),
		`UPDATE team.rdc_ai_model SET is_default=false WHERE tenant_id=$1`, qamTenantA); err != nil {
		t.Fatalf("清空默认失败: %v", err)
	}
	res, err := env.chat.Chat(context.Background(), rdcsvc.ChatRequest{TenantID: qamTenantA})
	if err != nil || !res.UsedFallback {
		t.Fatalf("无默认应走兜底: usedFallback=%v err=%v", res.UsedFallback, err)
	}

	// 唯一启用模型可停用，is_default 被清为 false；软删不物理删行。
	fresh := qamCreateModel(t, env, qamTokenRWB, "", "qa-soft", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192)
	if _, err := env.db.ExecContext(context.Background(),
		`UPDATE team.rdc_ai_model SET is_default=true WHERE id::text=$1`, fresh); err != nil {
		t.Fatalf("设置唯一默认失败: %v", err)
	}
	status, _, raw = qamRequest(t, env, qamTokenRWB, http.MethodDelete, "/rdc/models/"+fresh, "")
	if status != http.StatusOK {
		t.Fatalf("软删唯一启用模型失败: %d %s", status, raw)
	}
	var enabled, isDefault bool
	if err := env.db.QueryRowContext(context.Background(),
		`SELECT enabled, is_default FROM team.rdc_ai_model WHERE id::text=$1`, fresh).Scan(&enabled, &isDefault); err != nil {
		t.Fatalf("软删后行不应消失: %v", err)
	}
	if enabled || isDefault {
		t.Fatalf("软删后 enabled/is_default 必须为 false: %v %v", enabled, isDefault)
	}

	// 无模型公司 → config_missing。
	_, err = env.chat.Chat(context.Background(), rdcsvc.ChatRequest{TenantID: qamTenantB})
	chatErr, ok := err.(*rdcsvc.ChatError)
	if !ok || chatErr.Category != contract.AIChatConfigMissing {
		t.Fatalf("无启用模型期望 config_missing，实际 %v", err)
	}
}

// ---------- 5. 请求体严格白名单 ----------

func TestQAModel_StrictBodyWhitelist(t *testing.T) {
	env := qamEnvSetup(t)
	base := qamModelInput("qa-strict", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192)
	for _, forbidden := range []string{`"tenantId":"` + qamTenantB + `"`, `"isDefault":true`, `"enabled":false`, `"apiKeyMask":"sk-****xxxx"`} {
		body := strings.TrimSuffix(base, "}") + "," + forbidden + "}"
		status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models", body)
		if status != http.StatusBadRequest {
			t.Fatalf("请求体含 %s 期望 400，实际 %d %s", forbidden, status, raw)
		}
	}
	id := qamCreateModel(t, env, qamTokenRW, "", "qa-strict-ok", env.mockBase+"/ok", qamFakeKey, `{}`, false, 8192)
	status, _, raw := qamRequest(t, env, qamTokenRW, http.MethodPost,
		"/rdc/models/"+id+"/status", `{"enabled":true,"tenantId":"`+qamTenantB+`"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("启停请求体含 tenantId 期望 400，实际 %d %s", status, raw)
	}
}

// ---------- 6. 启动 fail-fast 与唯一掩码 ----------

func TestQAModel_ConfigFailFast(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
	}{
		{"缺失", false, ""},
		{"非 base64", true, "not-base64!!"},
		{"解码非 32 字节", true, base64.StdEncoding.EncodeToString([]byte("short"))},
	}
	orig, had := os.LookupEnv("RDC_MODEL_ENCRYPTION_KEY")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("RDC_MODEL_ENCRYPTION_KEY", orig)
		} else {
			_ = os.Unsetenv("RDC_MODEL_ENCRYPTION_KEY")
		}
	})
	for _, tc := range cases {
		if tc.set {
			_ = os.Setenv("RDC_MODEL_ENCRYPTION_KEY", tc.val)
		} else {
			_ = os.Unsetenv("RDC_MODEL_ENCRYPTION_KEY")
		}
		if _, err := config.Load(); err == nil {
			t.Fatalf("%s 必须 fail-fast", tc.name)
		}
	}
	_ = os.Setenv("RDC_MODEL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(qamMasterKey))
	if _, err := config.Load(); err != nil {
		t.Fatalf("合法主密钥应通过: %v", err)
	}
}

// 日志探测：本模块业务路径不应向标准 log 写任何 Key 明文。
func TestQAModel_NoKeyInLogs(t *testing.T) {
	env := qamEnvSetup(t)
	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(original) })
	qamCreateModel(t, env, qamTokenRW, "", "qa-log", env.mockBase+"/echo", qamFakeKey, `{}`, false, 8192)
	qamRequest(t, env, qamTokenRW, http.MethodPost, "/rdc/models/"+qamDBString(t, env,
		`SELECT id::text FROM team.rdc_ai_model WHERE name='qa-log' LIMIT 1`)+"/test", `{}`)
	if strings.Contains(buf.String(), qamFakeKey) {
		t.Fatalf("日志出现 Key 明文: %s", buf.String())
	}
}

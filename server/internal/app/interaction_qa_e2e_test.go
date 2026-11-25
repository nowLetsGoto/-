// interaction_qa_e2e_test.go · QA 独立验证：AI 交互层真实 HTTP + 真实 PostgreSQL 攻击面测试。
// 职责：用独立数据库、真实中间件链、真实 handler/service/repository 验证零假数据、状态不变、
//
//	哈希禁止回滚、门禁/风险、隔离权限、审计双写与 405。不是重跑实现者的内存替身单测。
// 输入：QA_INTERACTION_DB_URL 指向的专用空库；固定 UUID 种子数据；httptest 请求。
// 输出：断言 HTTP 状态 / JSON 外壳 / 数据库行未变 / 审计与证据行。
// 边界：只在专用库里建表与清表；不碰工作区文件、不 git add/commit；QA_INTERACTION_DB_URL 未设置时跳过。
package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditrepo "dandelion/server/internal/repository/audit"
	"dandelion/server/internal/repository"
	projectrepo "dandelion/server/internal/repository/project"
	rdcrepo "dandelion/server/internal/repository/rdc"
	auditsvc "dandelion/server/internal/service/audit"
	authzsvc "dandelion/server/internal/service/authz"
	rdcsvc "dandelion/server/internal/service/rdc"
	loginmw "dandelion/server/internal/middleware/login"
	authzmw "dandelion/server/internal/middleware/authz"
	rdchandler "dandelion/server/internal/handler/rdc"
)

const (
	qaTenantA  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	qaTenantB  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	qaReqA     = "11111111-1111-4111-8111-111111111111"
	qaReqB     = "11111111-1111-4111-8111-222222222222"
	qaTaskA    = "22222222-2222-4222-8222-111111111111"
	qaTaskB    = "22222222-2222-4222-8222-222222222222"
	qaSubA     = "33333333-3333-4333-8333-111111111111"
	qaSubEmpty = "33333333-3333-4333-8333-222222222222"
	qaSubB     = "33333333-3333-4333-8333-333333333333"
	qaUserID   = "77777777-7777-4777-8777-777777777777"
	qaReviewA  = "44444444-4444-4444-8444-111111111111"
	qaReviewB  = "44444444-4444-4444-8444-222222222222"
	qaPlanA    = "55555555-5555-4555-8555-111111111111"
	qaPlanB    = "55555555-5555-4555-8555-222222222222"
	qaCpA      = "66666666-6666-4666-8666-111111111111"
	qaCpB      = "66666666-6666-4666-8666-222222222222"
	qaCpC      = "66666666-6666-4666-8666-333333333333"
	qaTokenA   = "qa-token-A"
	qaTokenB   = "qa-token-B"
	qaTokenC   = "qa-token-C"
)

const qaFilesJSON = `[
 {"filePath":"src/main.go","fileStatus":"modified","oldPath":"","hunks":[
  {"hunkId":"h1","oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"oldContent":["old"],"newContent":["new"],"contextBefore":[],"contextAfter":[],"riskLabel":"delete_file"},
  {"hunkId":"h2","oldStart":5,"oldLines":1,"newStart":5,"newLines":1,"oldContent":["x"],"newContent":["y"],"contextBefore":[],"contextAfter":[],"riskLabel":"format"}
 ]}
]`

const qaPlanQuestions = `[{"questionId":"q1","prompt":"为什么改？","type":"text","required":true,"options":[],"answer":null}]`
const qaPlanTasks = `[{"stepId":"s1","title":"第一步","description":"","confirmed":false,"estimatedRisk":"low"}]`

const qaCpFiles = `[
 {"filePath":"src/main.go","fileStatus":"modified","beforeContent":"old","afterContent":"new","beforeHash":"before-h","afterHash":"after-h"},
 {"filePath":"src/config.yaml","fileStatus":"modified","beforeContent":"k: 1","afterContent":"k: 2","beforeHash":"cfg-before","afterHash":"cfg-after"}
]`

// qaEnv 持有专用库、真实 HTTP handler 与审计服务。
type qaEnv struct {
	db     *sql.DB
	router http.Handler
}

// qaEnvSetup 连接专用库、装 schema、种两家公司的最小可见数据，并返回真实 HTTP 链。
func qaEnvSetup(t *testing.T) *qaEnv {
	t.Helper()
	dsn := os.Getenv("QA_INTERACTION_DB_URL")
	if dsn == "" {
		t.Skip("未设置 QA_INTERACTION_DB_URL，跳过真实数据库攻击面测试")
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
	qaSeed(t, db)

	auditService := auditsvc.NewService(auditrepo.NewPartitioned(db))
	repo := rdcrepo.NewInteractionRepository(db)
	svc := rdcsvc.NewInteractionService(repo, rdcsvc.NewUnavailableProvider())
	h := rdchandler.NewInteractionHandler(svc, auditService)
	// 权限服务用零依赖构造：超管短路全量权限，无角色 team 账号落默认基线，
	// solo 身份在 RequireTeamMode 就被挡住，三者都不需要查业务仓储。
	authz := authzsvc.NewService(nil, nil, repository.Partitioned[*projectrepo.Repository]{}, nil, nil)
	return &qaEnv{db: db, router: qaInteractionRouter(h, authz)}
}

// qaSeed 重置专用库里的交互相关表并写入两家公司的最小数据集。
func qaSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`TRUNCATE team.audit_logs, team.rdc_review_record, team.rdc_hunk_decision,
		 team.rdc_diff_review, team.rdc_plan_confirmation, team.rdc_checkpoint,
		 team.rdc_dev_subtask, team.rdc_dev_task, team.rdc_requirements, public.tenants CASCADE`,
		`INSERT INTO public.tenants(id,name,code) VALUES ($1,'QA-A','QA-A'),($2,'QA-B','QA-B')`,
		`INSERT INTO team.rdc_requirements(id,req_key,title,tenant_id) VALUES
		 ($1,'QA-REQ-A','QA A',$2),($3,'QA-REQ-B','QA B',$4)`,
		`INSERT INTO team.rdc_dev_task(id,requirement_id,title,tenant_id) VALUES
		 ($1,$2,'QA task A',$3),($4,$5,'QA task B',$6)`,
		`INSERT INTO team.rdc_dev_subtask(id,task_id,kind,status) VALUES
		 ($1,$2,'frontend','ai_completed'),($3,$2,'backend','pending'),($4,$5,'frontend','ai_completed')`,
		`INSERT INTO team.rdc_diff_review(id,task_id,subtask_id,source,submit_status,files,tenant_id) VALUES
		 ($1,$2,$3,'agent_executor','pending',$4,$5),($6,$7,$8,'agent_executor','pending',$4,$9)`,
		`INSERT INTO team.rdc_plan_confirmation(id,task_id,subtask_id,questionnaire,plan_tasks,gate_status,tenant_id) VALUES
		 ($1,$2,$3,$4,$5,'blocked',$6),($7,$8,$9,$4,$5,'blocked',$10)`,
		`INSERT INTO team.rdc_checkpoint(id,task_id,subtask_id,ai_action,files,rollback_token,status,tenant_id) VALUES
		 ($1,$2,$3,'qa-action-A',$4,$5,'active',$6),
		 ($7,$8,$9,'qa-action-B',$4,$10,'active',$11),
		 ($12,$2,$3,'qa-action-C',$4,$13,'active',$6)`,
	}
	args := [][]any{
		{},
		{qaTenantA, qaTenantB},
		{qaReqA, qaTenantA, qaReqB, qaTenantB},
		{qaTaskA, qaReqA, qaTenantA, qaTaskB, qaReqB, qaTenantB},
		{qaSubA, qaTaskA, qaSubEmpty, qaSubB, qaTaskB},
		{qaReviewA, qaTaskA, qaSubA, qaFilesJSON, qaTenantA, qaReviewB, qaTaskB, qaSubB, qaTenantB},
		{qaPlanA, qaTaskA, qaSubA, qaPlanQuestions, qaPlanTasks, qaTenantA, qaPlanB, qaTaskB, qaSubB, qaTenantB},
		{qaCpA, qaTaskA, qaSubA, qaCpFiles, qaTokenA, qaTenantA, qaCpB, qaTaskB, qaSubB, qaTokenB, qaTenantB, qaCpC, qaTokenC},
	}
	for i, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt, args[i]...); err != nil {
			t.Fatalf("种子 SQL #%d 失败: %v\n%s", i, err, stmt)
		}
	}
}

// qaInteractionRouter 复刻 router.go 的交互组中间件链与 405 处理；
// 唯一替换是把 InjectScope 换成由测试头注入 scope（scope 推导有独立单测，这里专注交互层边界）。
func qaInteractionRouter(h *rdchandler.InteractionHandler, authz *authzsvc.Service) http.Handler {
	r := chi.NewRouter()
	verify := func(_ context.Context, token string) (contract.Identity, bool) {
		switch token {
		case "qa-super":
			return contract.Identity{UserID: qaUserID, Username: "qa-super", Mode: contract.ModeTeam,
				TenantID: qaTenantA, IsSuperAdmin: true}, true
		case "qa-solo":
			return contract.Identity{UserID: "qa-solo", Username: "qa-solo", Mode: contract.ModeSolo,
				TenantID: qaTenantA, AccountLevel: contract.AccountLevelMember}, true
		case "qa-noperm":
			return contract.Identity{UserID: "", Username: "qa-noperm", Mode: contract.ModeTeam,
				TenantID: qaTenantA, AccountLevel: contract.AccountLevelMember}, true
		case "qa-solo-super":
			return contract.Identity{UserID: qaUserID, Username: "qa-solo-super", Mode: contract.ModeSolo,
				TenantID: qaTenantA, IsSuperAdmin: true}, true
		}
		return contract.Identity{}, false
	}
	r.Route(contract.APIPrefix, func(api chi.Router) {
		api.Group(func(interaction chi.Router) {
			interaction.Use(loginmw.RequireAuth(verify))
			interaction.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					raw := req.Header.Get("X-QA-Scope")
					scope := contract.Scope{All: true}
					switch {
					case raw == "none":
						scope = contract.Scope{}
					case raw != "" && raw != "all":
						scope = contract.Scope{TenantID: raw, TeamIDs: []string{}}
					}
					next.ServeHTTP(w, req.WithContext(authctx.WithScope(req.Context(), scope)))
				})
			})
			interaction.Use(authzmw.RequireTeamMode)
			interaction.Use(authzmw.RequirePermission(authz, contract.PermissionRDCReviewRead))

			interaction.Get("/rdc/interactions/tasks/{id}/subtasks/{subtaskId}/diff-review", middlewareWrap(h.GetDiffReview))
			interaction.Post("/rdc/interactions/tasks/{id}/subtasks/{subtaskId}/diff-review/submit", middlewareWrap(h.SubmitDiffReview))
			interaction.Put("/rdc/interactions/diff-reviews/{reviewId}/hunks/{hunkId}/decision", middlewareWrap(h.SetHunkDecision))
			interaction.Get("/rdc/interactions/tasks/{id}/subtasks/{subtaskId}/plan-confirmation", middlewareWrap(h.GetPlan))
			interaction.Post("/rdc/interactions/plan-confirmations/{planId}/confirm", middlewareWrap(h.ConfirmPlan))
			interaction.Get("/rdc/interactions/tasks/{id}/subtasks/{subtaskId}/checkpoints", middlewareWrap(h.ListCheckpoints))
			interaction.Post("/rdc/interactions/checkpoints/{checkpointId}/rollback", middlewareWrap(h.Rollback))
			interaction.Get("/rdc/interactions/tasks/{id}/subtasks/{subtaskId}/approval", middlewareWrap(h.GetApproval))
			interaction.Post("/rdc/interactions/tasks/{id}/subtasks/{subtaskId}/approval/decide", middlewareWrap(h.DecideApproval))
		})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Allow", allowedMethodsFor(req))
		handler.WriteError(w, req, handler.MethodNotAllowed("请求方法不支持"))
	})
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		handler.WriteError(w, req, handler.NotFound("接口不存在"))
	})
	return r
}

// middlewareWrap 把交互 handler 的 error 返回转成统一 HTTP 外壳（与 middleware.Wrap 同语义）。
func middlewareWrap(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			handler.WriteError(w, r, err)
		}
	}
}

// qaEnvelope 是统一响应外壳的最小投影。
type qaEnvelope struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

// qaDo 发一个带身份与 scope 的请求，断言状态码与外壳 code 一致，返回外壳与 Allow 头。
func qaDo(t *testing.T, env *qaEnv, method, path, token, scope, body string, want int) (qaEnvelope, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, contract.APIPrefix+path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if scope != "" {
		req.Header.Set("X-QA-Scope", scope)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	var envl qaEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envl); err != nil {
		t.Fatalf("%s %s 响应不是合法 JSON: %v, body=%s", method, path, err, w.Body.String())
	}
	// 统一外壳：成功 code=0，失败 code 等于 HTTP 状态；want=0 表示调用方自行判断。
	if w.Code != want {
		t.Fatalf("%s %s 期望 HTTP %d，实际 HTTP=%d code=%d msg=%s", method, path, want, w.Code, envl.Code, envl.Message)
	}
	if want > 0 {
		wantCode := want
		if want == http.StatusOK {
			wantCode = 0
		}
		if envl.Code != wantCode {
			t.Fatalf("%s %s 期望 code=%d，实际 code=%d msg=%s", method, path, wantCode, envl.Code, envl.Message)
		}
	}
	return envl, w.Header().Get("Allow")
}

func qaCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("计数失败 %s: %v", query, err)
	}
	return n
}

func qaString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatalf("取值失败 %s: %v", query, err)
	}
	return s
}

// TestQAZeroFakeDataAndNoArtifactIngest 核心 1：生产 provider 下所有读路径是真实空态，且没有人工灌假 diff 的写点。
func TestQAZeroFakeDataAndNoArtifactIngest(t *testing.T) {
	env := qaEnvSetup(t)
	base := "/rdc/interactions/tasks/" + qaTaskA + "/subtasks/" + qaSubEmpty
	for _, suffix := range []string{"/diff-review", "/plan-confirmation", "/checkpoints", "/approval"} {
		resp, _ := qaDo(t, env, http.MethodGet, base+suffix, "qa-super", "all", "", 200)
		data := strings.TrimSpace(string(resp.Data))
		switch suffix {
		case "/checkpoints":
			if data != "[]" {
				t.Fatalf("%s 无产物应返回空数组，实际 %s", suffix, data)
			}
		default:
			if data != "null" {
				t.Fatalf("%s 无产物应返回 data:null，实际 %s", suffix, data)
			}
		}
	}

	// 尝试直接灌假产物：不存在的产物写入端点不能返回成功，也不能改动已有 files 快照。
	fake := `{"files":[{"filePath":"evil.go","fileStatus":"added","hunks":[]}],"source":"manual","tenantId":"` + qaTenantB + `"}`
	for _, probe := range []struct {
		path string
		want int
	}{
		{"/rdc/interactions/tasks/" + qaTaskA + "/subtasks/" + qaSubEmpty + "/diff-review", 405},
		{"/rdc/interactions/diff-reviews/" + qaReviewA, 404},
		{"/rdc/interactions/artifacts", 404},
	} {
		_, _ = qaDo(t, env, http.MethodPost, probe.path, "qa-super", "all", fake, probe.want)
	}
	// 已注册写端点塞 files/source 越权字段必须 400，产物快照不能变。
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/tasks/"+qaTaskA+"/subtasks/"+qaSubA+"/diff-review/submit",
		"qa-super", "all", `{"decisions":[],"files":[{"filePath":"evil.go"}]}`, 400)
	qaDo(t, env, http.MethodPut,
		"/rdc/interactions/diff-reviews/"+qaReviewA+"/hunks/h1/decision",
		"qa-super", "all", `{"decision":"accepted","files":[{"filePath":"evil.go"}]}`, 400)
	got := qaString(t, env.db, `SELECT files::text FROM team.rdc_diff_review WHERE id=$1`, qaReviewA)
	if !strings.Contains(got, "src/main.go") || strings.Contains(got, "evil.go") {
		t.Fatalf("产物快照被越权字段污染: %s", got)
	}
	if n := qaCount(t, env.db, `SELECT count(*) FROM team.rdc_diff_review`); n != 2 {
		t.Fatalf("不得新增假审查记录，实际 count=%d", n)
	}
}

// TestQASubtaskStatusImmutableAcrossAllWrites 核心 2：完整 HTTP 写序列后子任务状态完全不变。
func TestQASubtaskStatusImmutableAcrossAllWrites(t *testing.T) {
	env := qaEnvSetup(t)
	before := qaString(t, env.db, `SELECT status FROM team.rdc_dev_subtask WHERE id=$1`, qaSubA)
	beforeOther := qaString(t, env.db, `SELECT status FROM team.rdc_dev_subtask WHERE id=$1`, qaSubEmpty)

	qaDo(t, env, http.MethodPut,
		"/rdc/interactions/diff-reviews/"+qaReviewA+"/hunks/h1/decision",
		"qa-super", "all", `{"decision":"accepted"}`, 200)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/tasks/"+qaTaskA+"/subtasks/"+qaSubA+"/diff-review/submit",
		"qa-super", "all", `{"decisions":[{"hunkId":"h1","decision":"accepted"},{"hunkId":"h2","decision":"rejected"}]}`, 200)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/plan-confirmations/"+qaPlanA+"/confirm",
		"qa-super", "all", `{"answers":[{"questionId":"q1","answer":"已确认"}],"stepIds":["s1"]}`, 200)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/tasks/"+qaTaskA+"/subtasks/"+qaSubA+"/approval/decide",
		"qa-super", "all", `{"checkpointId":"`+qaCpA+`","decision":"approve"}`, 200)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/checkpoints/"+qaCpA+"/rollback",
		"qa-super", "all", `{"rollbackToken":"`+qaTokenA+`","scope":"checkpoint","targets":[],`+
			`"currentHashes":{"src/main.go":"after-h","src/config.yaml":"cfg-after"}}`, 200)

	if after := qaString(t, env.db, `SELECT status FROM team.rdc_dev_subtask WHERE id=$1`, qaSubA); after != before {
		t.Fatalf("交互写序列改变了子任务状态: %s → %s", before, after)
	}
	if after := qaString(t, env.db, `SELECT status FROM team.rdc_dev_subtask WHERE id=$1`, qaSubEmpty); after != beforeOther {
		t.Fatalf("交互写序列改变了其他子任务状态: %s → %s", beforeOther, after)
	}
	gotActions := qaString(t, env.db,
		`SELECT string_agg(action, ',' ORDER BY action) FROM team.rdc_review_record WHERE subtask_id=$1`, qaSubA)
	for _, action := range []string{"diff_submitted", "plan_confirmed", "approval_decided", "checkpoint_rolled_back"} {
		if !strings.Contains(gotActions, action) {
			t.Fatalf("缺少证据 action %s，实际 %s", action, gotActions)
		}
	}
	if n := qaCount(t, env.db, `SELECT count(*) FROM team.audit_logs WHERE action LIKE 'rdc.interaction.%'`); n != 4 {
		t.Fatalf("4 类成功动作应各写一条审计，实际 %d", n)
	}
	var submitDetail string
	if err := env.db.QueryRowContext(context.Background(),
		`SELECT detail::text FROM team.rdc_review_record WHERE action='diff_submitted' AND subtask_id=$1`, qaSubA,
	).Scan(&submitDetail); err != nil {
		t.Fatalf("缺少 diff_submitted 证据: %v", err)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(submitDetail), &evidence); err != nil {
		t.Fatalf("证据 detail 不是合法 JSON: %v", err)
	}
	for _, key := range []string{"taskId", "subtaskId", "reviewId", "accepted", "rejected"} {
		if _, ok := evidence[key]; !ok {
			t.Fatalf("diff_submitted 证据 detail 缺 %s: %s", key, submitDetail)
		}
	}
	if evidence["taskId"] != qaTaskA || evidence["subtaskId"] != qaSubA || evidence["reviewId"] != qaReviewA ||
		evidence["accepted"] != float64(1) || evidence["rejected"] != float64(1) {
		t.Fatalf("diff_submitted 证据 detail 值不正确: %s", submitDetail)
	}
}

// TestQASubmitRouteIsReachable 独立钉死提交端点可达性：契约路径没有 reviewId，
// 但 handler 从 URL 读 reviewId，导致生产路由上该端点恒定 404。
func TestQASubmitRouteIsReachable(t *testing.T) {
	env := qaEnvSetup(t)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/tasks/"+qaTaskA+"/subtasks/"+qaSubA+"/diff-review/submit",
		"qa-super", "all", `{"decisions":[{"hunkId":"h1","decision":"accepted"},{"hunkId":"h2","decision":"rejected"}]}`, 200)
}

// TestQARollbackHashMismatchBlocked 核心 3a：token 正确但内容已变化 → 409 + 列冲突 + blocked 审计 + 零证据零状态写。
func TestQARollbackHashMismatchBlocked(t *testing.T) {
	env := qaEnvSetup(t)
	path := "/rdc/interactions/checkpoints/" + qaCpA + "/rollback"
	resp, _ := qaDo(t, env, http.MethodPost, path, "qa-super", "all",
		`{"rollbackToken":"`+qaTokenA+`","scope":"checkpoint","targets":[],`+
			`"currentHashes":{"src/main.go":"after-h","src/config.yaml":"已经被后续动作改掉"}}`, 409)
	if !strings.Contains(resp.Message, "src/config.yaml") || !strings.Contains(resp.Message, "禁止回滚") {
		t.Fatalf("409 文案必须列冲突文件并说明禁止回滚: %s", resp.Message)
	}
	if status := qaString(t, env.db, `SELECT status FROM team.rdc_checkpoint WHERE id=$1`, qaCpA); status != contract.CheckpointActive {
		t.Fatalf("禁止回滚不得改检查点状态，实际 %s", status)
	}
	if n := qaCount(t, env.db, `SELECT count(*) FROM team.rdc_review_record WHERE action='checkpoint_rolled_back' AND subtask_id=$1`, qaSubA); n != 0 {
		t.Fatalf("禁止回滚不得写回滚证据，实际 %d", n)
	}
	var auditDetail string
	if err := env.db.QueryRowContext(context.Background(),
		`SELECT detail::text FROM team.audit_logs WHERE action='rdc.interaction.rollback_blocked' AND object_id=$1`, qaCpA,
	).Scan(&auditDetail); err != nil {
		t.Fatalf("缺少 rollback_blocked 审计: %v", err)
	}
	if !strings.Contains(auditDetail, "src/config.yaml") || !strings.Contains(auditDetail, qaSubA) {
		t.Fatalf("blocked 审计 detail 缺冲突文件 / 关联键: %s", auditDetail)
	}
}

// TestQARollbackTokenAndScopeRejection 核心 3b：错误 token / 空 token / 非 active / 未知 filePath 绕过哈希都必须拒绝。
func TestQARollbackTokenAndScopeRejection(t *testing.T) {
	env := qaEnvSetup(t)
	cases := []struct {
		name, cpID, body string
	}{
		{"wrong token", qaCpA, `{"rollbackToken":"bad","scope":"checkpoint","currentHashes":{}}`},
		{"empty token", qaCpA, `{"rollbackToken":"","scope":"checkpoint","currentHashes":{}}`},
		{"not active", qaCpC, `{"rollbackToken":"` + qaTokenC + `","scope":"checkpoint","currentHashes":{}}`},
		// 目标文件不属于检查点快照：当前 targetFiles 会算出空集合，哈希校验被整段跳过。
		{"unknown target bypass", qaCpA, `{"rollbackToken":"` + qaTokenA + `","scope":"file",` +
			`"targets":[{"filePath":"src/not-in-snapshot.go"}],"currentHashes":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "not active" {
				if _, err := env.db.ExecContext(context.Background(),
					`UPDATE team.rdc_checkpoint SET status='rolled_back' WHERE id=$1`, tc.cpID); err != nil {
					t.Fatal(err)
				}
			}
			qaDo(t, env, http.MethodPost, "/rdc/interactions/checkpoints/"+tc.cpID+"/rollback",
				"qa-super", "all", tc.body, 400)
			if n := qaCount(t, env.db, `SELECT count(*) FROM team.rdc_review_record WHERE action='checkpoint_rolled_back' AND subtask_id=$1`, qaSubA); n != 0 {
				t.Fatalf("非法回滚不得写证据，实际 %d", n)
			}
		})
	}
}

// TestQARollbackMatchingPathTouchesOnlyCheckpoint 核心 3c：匹配路径只改检查点状态 + 证据，不回退状态、不写真实文件。
func TestQARollbackMatchingPathTouchesOnlyCheckpoint(t *testing.T) {
	env := qaEnvSetup(t)
	before := qaString(t, env.db, `SELECT status FROM team.rdc_dev_subtask WHERE id=$1`, qaSubA)
	qaDo(t, env, http.MethodPost, "/rdc/interactions/checkpoints/"+qaCpA+"/rollback",
		"qa-super", "all", `{"rollbackToken":"`+qaTokenA+`","scope":"file",`+
			`"targets":[{"filePath":"src/main.go"}],"currentHashes":{"src/main.go":"after-h"}}`, 200)
	if status := qaString(t, env.db, `SELECT status FROM team.rdc_checkpoint WHERE id=$1`, qaCpA); status != contract.CheckpointPartial {
		t.Fatalf("单文件回滚应为 partially_rolled_back，实际 %s", status)
	}
	if after := qaString(t, env.db, `SELECT status FROM team.rdc_dev_subtask WHERE id=$1`, qaSubA); after != before {
		t.Fatalf("回滚不得回退子任务状态: %s → %s", before, after)
	}
	if n := qaCount(t, env.db, `SELECT count(*) FROM team.rdc_review_record WHERE action='checkpoint_rolled_back' AND subtask_id=$1`, qaSubA); n != 1 {
		t.Fatalf("匹配路径应写一条回滚证据，实际 %d", n)
	}
	// 复用令牌必须被拒绝。
	qaDo(t, env, http.MethodPost, "/rdc/interactions/checkpoints/"+qaCpA+"/rollback",
		"qa-super", "all", `{"rollbackToken":"`+qaTokenA+`","scope":"checkpoint","currentHashes":{"src/main.go":"after-h"}}`, 400)
}

// TestQAConcurrentRollbackSameToken 核心 3d：同一 token 并发回滚应当只有一个成功（幂等令牌语义）。
func TestQAConcurrentRollbackSameToken(t *testing.T) {
	env := qaEnvSetup(t)
	const n = 8
	var wg sync.WaitGroup
	statuses := make([]int, n)
	barrier := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-barrier
			req := httptest.NewRequest(http.MethodPost,
				contract.APIPrefix+"/rdc/interactions/checkpoints/"+qaCpA+"/rollback",
				strings.NewReader(`{"rollbackToken":"`+qaTokenA+`","scope":"checkpoint",`+
					`"currentHashes":{"src/main.go":"after-h","src/config.yaml":"cfg-after"}}`))
			req.Header.Set("Authorization", "Bearer qa-super")
			req.Header.Set("X-QA-Scope", "all")
			w := httptest.NewRecorder()
			env.router.ServeHTTP(w, req)
			statuses[idx] = w.Code
		}(i)
	}
	close(barrier)
	wg.Wait()
	success := 0
	for _, code := range statuses {
		if code == 200 {
			success++
		} else if code != 400 && code != 409 {
			t.Fatalf("并发回滚出现非预期状态 %d: %v", code, statuses)
		}
	}
	if success != 1 {
		t.Fatalf("同一 rollback_token 并发回滚应恰好 1 次成功，实际 %d 次 (状态=%v)", success, statuses)
	}
	if n := qaCount(t, env.db, `SELECT count(*) FROM team.rdc_review_record WHERE action='checkpoint_rolled_back' AND subtask_id=$1`, qaSubA); n != 1 {
		t.Fatalf("并发回滚应只有一条证据，实际 %d", n)
	}
}

// TestQAGateAndRiskHTTP 重点 4：pending 门禁、计划门禁、整体风险与介入强度都经真实 HTTP 验证。
func TestQAGateAndRiskHTTP(t *testing.T) {
	env := qaEnvSetup(t)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/tasks/"+qaTaskA+"/subtasks/"+qaSubA+"/diff-review/submit",
		"qa-super", "all", `{"decisions":[{"hunkId":"h1","decision":"accepted"}]}`, 400)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/plan-confirmations/"+qaPlanA+"/confirm",
		"qa-super", "all", `{"answers":[],"stepIds":[]}`, 400)
	resp, _ := qaDo(t, env, http.MethodGet,
		"/rdc/interactions/tasks/"+qaTaskA+"/subtasks/"+qaSubA+"/approval",
		"qa-super", "all", "", 200)
	var view struct {
		OverallRisk  string `json:"overallRisk"`
		Intervention string `json:"intervention"`
	}
	if err := json.Unmarshal(resp.Data, &view); err != nil {
		t.Fatal(err)
	}
	if view.OverallRisk != "high" || view.Intervention != "strict" {
		t.Fatalf("含 delete_file 的审查应 high/strict，实际 %+v", view)
	}
}

// TestQATenantAndModeIsolationHTTP 重点 5：solo 403、无读权限 403、跨公司 404、9 路径 405 + Allow。
func TestQATenantAndModeIsolationHTTP(t *testing.T) {
	env := qaEnvSetup(t)
	paths := []struct {
		method, path, allow string
	}{
		{http.MethodGet, "/rdc/interactions/tasks/" + qaTaskA + "/subtasks/" + qaSubA + "/diff-review", "GET"},
		{http.MethodPost, "/rdc/interactions/tasks/" + qaTaskA + "/subtasks/" + qaSubA + "/diff-review/submit", "POST"},
		{http.MethodPut, "/rdc/interactions/diff-reviews/" + qaReviewA + "/hunks/h1/decision", "PUT"},
		{http.MethodGet, "/rdc/interactions/tasks/" + qaTaskA + "/subtasks/" + qaSubA + "/plan-confirmation", "GET"},
		{http.MethodPost, "/rdc/interactions/plan-confirmations/" + qaPlanA + "/confirm", "POST"},
		{http.MethodGet, "/rdc/interactions/tasks/" + qaTaskA + "/subtasks/" + qaSubA + "/checkpoints", "GET"},
		{http.MethodPost, "/rdc/interactions/checkpoints/" + qaCpA + "/rollback", "POST"},
		{http.MethodGet, "/rdc/interactions/tasks/" + qaTaskA + "/subtasks/" + qaSubA + "/approval", "GET"},
		{http.MethodPost, "/rdc/interactions/tasks/" + qaTaskA + "/subtasks/" + qaSubA + "/approval/decide", "POST"},
	}
	for _, p := range paths {
		qaDo(t, env, p.method, p.path, "qa-solo", "all", `{}`, 403)
		qaDo(t, env, p.method, p.path, "qa-solo-super", "all", `{}`, 403)
		qaDo(t, env, p.method, p.path, "qa-noperm", "all", `{}`, 403)
		wrong := http.MethodGet
		if p.method == http.MethodGet {
			wrong = http.MethodPost
		}
		_, allow := qaDo(t, env, wrong, p.path, "qa-super", "all", `{}`, 405)
		if allow != p.allow {
			t.Fatalf("%s %s 的 Allow 应为 %s，实际 %q", wrong, p.path, p.allow, allow)
		}
	}
	// 跨公司读路径：与「无产物」同口径返回真实空态，不泄漏 B 公司产物存在性。
	for _, suffix := range []string{"/diff-review", "/plan-confirmation", "/checkpoints", "/approval"} {
		resp, _ := qaDo(t, env, http.MethodGet,
			"/rdc/interactions/tasks/"+qaTaskB+"/subtasks/"+qaSubB+suffix, "qa-super", qaTenantA, "", 200)
		if strings.Contains(string(resp.Data), "src/main.go") || strings.Contains(string(resp.Data), "after-h") {
			t.Fatalf("跨公司读取泄漏了 B 公司数据: %s", resp.Data)
		}
	}
	// 跨公司写路径一律 404。
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/tasks/"+qaTaskB+"/subtasks/"+qaSubB+"/diff-review/submit",
		"qa-super", qaTenantA, `{"decisions":[]}`, 404)
	qaDo(t, env, http.MethodPut,
		"/rdc/interactions/diff-reviews/"+qaReviewB+"/hunks/h1/decision",
		"qa-super", qaTenantA, `{"decision":"accepted"}`, 404)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/plan-confirmations/"+qaPlanB+"/confirm",
		"qa-super", qaTenantA, `{"answers":[],"stepIds":[]}`, 404)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/checkpoints/"+qaCpB+"/rollback",
		"qa-super", qaTenantA, `{"rollbackToken":"`+qaTokenB+`","scope":"checkpoint"}`, 404)
}

// TestQANestedUnknownFieldRejected 嵌套对象里的越权字段也必须被严格解码拒绝。
func TestQANestedUnknownFieldRejected(t *testing.T) {
	env := qaEnvSetup(t)
	qaDo(t, env, http.MethodPost,
		"/rdc/interactions/tasks/"+qaTaskA+"/subtasks/"+qaSubA+"/diff-review/submit",
		"qa-super", "all",
		`{"decisions":[{"hunkId":"h1","decision":"accepted","tenantId":"evil","status":"completed"}]}`, 400)
}

// TestQAKnownIssuesAreDocumented 把静态红线再钉一遍：生产代码没有状态机调用、没有产物写入端点。
func TestQAKnownIssuesAreDocumented(t *testing.T) {
	for _, path := range []string{
		"../service/rdc/interaction_service.go",
		"../repository/rdc/interaction_repository.go",
		"../repository/rdc/interaction_repository_control.go",
		"../handler/rdc/interaction_handler.go",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range []string{
			"UPDATE rdc_dev_subtask", "ApplyReviewAction(", ".Advance(", ".Skip(", ".Resubmit(",
			"INSERT INTO team.rdc_diff_review", "ApplyRollback(",
		} {
			if strings.Contains(string(data), token) {
				t.Fatalf("生产源码 %s 出现红线 %q", path, token)
			}
		}
	}
}

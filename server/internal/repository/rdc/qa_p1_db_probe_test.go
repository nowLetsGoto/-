package rdc

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

func qaP1DB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("QA_AIMODEL_DB_URL")
	if dsn == "" {
		t.Skip("未设置 QA_AIMODEL_DB_URL")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := repository.EnsureSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

const qaTenant = "c0ffee11-1111-4111-8111-111111111111"

// qaP1Tenant 保证测试公司存在，并清空本公司历史记录，让每个用例从确定状态开始。
//
// 为什么必须自带 fixture：调用记录有 REFERENCES public.tenants(id) 外键，
// 空库直接 Insert 会被 23503 拦下；而"清空本公司"只作用于固定测试 uuid，
// 不会碰到别的公司数据（不影响同库其它用例）。
func qaP1Tenant(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO public.tenants (id, name, code) VALUES ($1::uuid, $2, $3)
		 ON CONFLICT (id) DO NOTHING`,
		qaTenant, "QA P1 探针公司", "qa-p1-probe"); err != nil {
		t.Fatalf("准备测试公司失败: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM team.rdc_ai_model_call WHERE tenant_id = $1::uuid`, qaTenant); err != nil {
		t.Fatalf("清理本公司调用记录失败: %v", err)
	}
}

// qaP1Record 造一条最小可写记录；token 由调用方通过 tokensReported/prompt/completion 决定。
// 传 nil 表示"上游未上报"，这正是 NOT NULL 约束修复前写不进去的那种行。
func qaP1Record(tokensReported bool, prompt, completion *int64) contract.AIModelCallRecord {
	return contract.AIModelCallRecord{
		TenantID: qaTenant, Scene: "qa",
		PromptTokens: prompt, CompletionTokens: completion, TokensReported: tokensReported,
	}
}

func int64Ptr(value int64) *int64 { return &value }

func TestQAP1AggregateFailureCategoriesGap(t *testing.T) {
	db := qaP1DB(t)
	qaP1Tenant(t, db)
	repo := NewAIModelCallRepository(db)
	ctx := context.Background()

	// 3 条失败（两类），1 条成功：用来同时验证 FailedCalls 与失败分布的分组。
	failures := []string{"auth_failed", "auth_failed", "rate_limited"}
	for _, category := range failures {
		record := qaP1Record(false, nil, nil)
		record.Success = false
		record.ErrorCategory = category
		if err := repo.Insert(ctx, record); err != nil {
			t.Fatalf("写入失败记录失败: %v", err)
		}
	}
	ok := qaP1Record(true, int64Ptr(11), int64Ptr(7))
	ok.Success = true
	ok.UsedUpstream = true
	if err := repo.Insert(ctx, ok); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	stats, err := repo.Aggregate(ctx, contract.AIModelCallQuery{
		Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncludeLocal: true,
	}, contract.Scope{TenantID: qaTenant})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("总调用=%d 成功=%d 失败=%d 失败分布=%+v",
		stats.TotalCalls, stats.SuccessCalls, stats.FailedCalls, stats.FailureCategories)

	if stats.FailedCalls != 3 {
		t.Fatalf("失败应 3 条，实际 %d", stats.FailedCalls)
	}
	if len(stats.FailureCategories) != 2 {
		t.Fatalf("失败分布应有 2 类（auth_failed / rate_limited），实际 %+v", stats.FailureCategories)
	}
	// 排序契约：条数倒序、同数按类别名升序 → auth_failed(2) 在前。
	if stats.FailureCategories[0].Category != "auth_failed" || stats.FailureCategories[0].Count != 2 {
		t.Fatalf("失败分布首项应为 auth_failed×2，实际 %+v", stats.FailureCategories[0])
	}
}

func TestQAP1CountInWindowExcludesLocalReject(t *testing.T) {
	db := qaP1DB(t)
	qaP1Tenant(t, db)
	repo := NewAIModelCallRepository(db)
	ctx := context.Background()

	// 3 条被拒（used_upstream=false，token 未上报 → NULL）+ 2 条已出站。
	for i := 0; i < 3; i++ {
		rejected := qaP1Record(false, nil, nil)
		rejected.Success = false
		rejected.ErrorCategory = string(contract.AIChatRateLimited)
		rejected.UsedUpstream = false
		if err := repo.Insert(ctx, rejected); err != nil {
			t.Fatalf("被拒记录写入失败: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		ok := qaP1Record(true, int64Ptr(3), int64Ptr(1))
		ok.Success = true
		ok.UsedUpstream = true
		if err := repo.Insert(ctx, ok); err != nil {
			t.Fatal(err)
		}
	}
	count, err := repo.CountInWindow(ctx, contract.Scope{TenantID: qaTenant}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("窗口内计数=%d（期望 2，被拒 3 条不应计入）", count)
	if count != 2 {
		t.Fatalf("窗口只数已出站：期望 2，实际 %d", count)
	}
}

// TestQAP1TokenNullVsZero 验证"未上报 → NULL"与"真实 0 → 0"在库里确实可区分，
// 且聚合口径只在有数据时求和。这是本次 DDL 修复的核心断言。
func TestQAP1TokenNullVsZero(t *testing.T) {
	db := qaP1DB(t)
	qaP1Tenant(t, db)
	repo := NewAIModelCallRepository(db)
	ctx := context.Background()

	// 一条未上报（token 应落 NULL）、一条真实 0（token 应落 0）。
	unreported := qaP1Record(false, nil, nil)
	unreported.Success = true
	unreported.UsedUpstream = true
	unreported.LatencyMS = 100
	if err := repo.Insert(ctx, unreported); err != nil {
		t.Fatalf("未上报记录必须能写入（NULL token）: %v", err)
	}
	zero := qaP1Record(true, int64Ptr(0), int64Ptr(0))
	zero.Success = true
	zero.UsedUpstream = true
	zero.LatencyMS = 300
	if err := repo.Insert(ctx, zero); err != nil {
		t.Fatal(err)
	}

	// 直接查库：未上报那行的两列必须是 SQL NULL，而不是 0。
	var promptNull, completionNull bool
	err := db.QueryRow(`
		SELECT prompt_tokens IS NULL, completion_tokens IS NULL
		FROM team.rdc_ai_model_call
		WHERE tenant_id = $1::uuid AND tokens_reported = false`, qaTenant).Scan(&promptNull, &completionNull)
	if err != nil {
		t.Fatal(err)
	}
	if !promptNull || !completionNull {
		t.Fatalf("未上报的 token 必须是 NULL，实际 promptIsNull=%v completionIsNull=%v", promptNull, completionNull)
	}

	now := time.Now().UTC()
	stats, err := repo.Aggregate(ctx, contract.AIModelCallQuery{
		Start: now.Add(-time.Hour), End: now.Add(time.Hour),
	}, contract.Scope{TenantID: qaTenant})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("promptTokens=%v completionTokens=%v tokensUnreported=%d avgLatency=%v",
		deref(stats.PromptTokens), deref(stats.CompletionTokens),
		stats.TokensUnreported, deref(stats.AvgSuccessLatencyMS))

	if stats.PromptTokens == nil || *stats.PromptTokens != 0 {
		t.Fatalf("只对 tokens_reported=true 求和，应为 0（真实 0），实际 %v", deref(stats.PromptTokens))
	}
	if stats.TokensUnreported != 1 {
		t.Fatalf("未上报条数应为 1，实际 %d", stats.TokensUnreported)
	}
	// 平均耗时只算 success AND used_upstream：两条都符合，均值 (100+300)/2 = 200。
	if stats.AvgSuccessLatencyMS == nil || *stats.AvgSuccessLatencyMS != 200 {
		t.Fatalf("平均耗时应为 200ms，实际 %v", deref(stats.AvgSuccessLatencyMS))
	}
}

// TestQAP1LocalRejectExcludedFromAverageLatency 确认"成功但本地拒绝"的记录
// 不参与平均耗时（它 latency=0，若被计入会把均值拉低，让人误以为上游很快）。
func TestQAP1LocalRejectExcludedFromAverageLatency(t *testing.T) {
	db := qaP1DB(t)
	qaP1Tenant(t, db)
	repo := NewAIModelCallRepository(db)
	ctx := context.Background()

	real := qaP1Record(true, int64Ptr(3), int64Ptr(1))
	real.Success = true
	real.UsedUpstream = true
	real.LatencyMS = 500
	if err := repo.Insert(ctx, real); err != nil {
		t.Fatal(err)
	}
	// 理论上不该出现"success 且未出站"，但历史脏数据可能存在；口径必须在数据异常时也正确。
	localOK := qaP1Record(false, nil, nil)
	localOK.Success = true
	localOK.UsedUpstream = false
	localOK.LatencyMS = 0
	if err := repo.Insert(ctx, localOK); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	stats, err := repo.Aggregate(ctx, contract.AIModelCallQuery{
		Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncludeLocal: true,
	}, contract.Scope{TenantID: qaTenant})
	if err != nil {
		t.Fatal(err)
	}
	if stats.AvgSuccessLatencyMS == nil || *stats.AvgSuccessLatencyMS != 500 {
		t.Fatalf("平均耗时只算已出站的成功记录，应为 500，实际 %v", deref(stats.AvgSuccessLatencyMS))
	}
}

// deref 是测试内的可读打印助手：nil 打印成 "nil" 而不是一串地址。
func deref(value *int64) any {
	if value == nil {
		return "nil"
	}
	return *value
}

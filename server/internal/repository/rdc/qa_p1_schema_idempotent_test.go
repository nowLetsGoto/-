package rdc

import (
	"context"
	"testing"

	"dandelion/server/internal/repository"
)

// TestQAP1SchemaIsIdempotent 连续执行三次 EnsureSchema，每次都不得报错。
//
// 这条专门守住「存量环境迁移」：CREATE TABLE IF NOT EXISTS 不会修改已存在表的列约束，
// 因此放开 NOT NULL 必须靠独立的 ALTER ... DROP NOT NULL；而 DROP NOT NULL 对
// 已经可空的列是幂等的，本测试用"连跑三次"把这个性质钉住。
func TestQAP1SchemaIsIdempotent(t *testing.T) {
	db := qaP1DB(t)
	for round := 1; round <= 3; round++ {
		if err := repository.EnsureSchema(context.Background(), db); err != nil {
			t.Fatalf("第 %d 次 EnsureSchema 失败: %v", round, err)
		}
	}
	// 迁移后列必须真的可空，且不再有 DEFAULT（否则 nil 会被兜成 0，语义又错回去）。
	var nullable bool
	var hasDefault bool
	err := db.QueryRow(`
		SELECT is_nullable = 'YES', column_default IS NOT NULL
		FROM information_schema.columns
		WHERE table_schema = 'team' AND table_name = 'rdc_ai_model_call'
		  AND column_name = 'prompt_tokens'`).Scan(&nullable, &hasDefault)
	if err != nil {
		t.Fatal(err)
	}
	if !nullable {
		t.Fatal("prompt_tokens 必须可空（NULL = 上游未上报）")
	}
	if hasDefault {
		t.Fatal("prompt_tokens 不应有 DEFAULT：否则未上报会被兜成 0，与「不知道 ≠ 零」冲突")
	}
}

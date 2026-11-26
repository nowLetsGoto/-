//go:build integration

// 职责：验证「淘汰保留 r2_key」与「下载按 r2_state 判断」这对语义是自洽的。
// 输入：本机 PostgreSQL 与临时构造的包记录。
// 输出：对淘汰后字段与候选筛选的断言。
// 边界：只操作临时创建的包，用后即删；不改动真实业务数据。
package repository

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
)

// TestMarkEvictedKeepsKeyForResync 验证淘汰保留 r2_key。
//
// 这是「同步到 R2」能工作的前提：重推需要知道推到哪个对象名。
// 若清空 key，重推就得重新生成，而新 key 很可能丢掉 {租户}/{项目}/ 归属前缀，
// 对象成孤儿后再也无法判断属于谁。
func TestMarkEvictedKeepsKeyForResync(t *testing.T) {
	db, skip := openTestDB(t)
	if skip != "" {
		t.Skip(skip)
	}
	ctx := context.Background()

	var projectID string
	if err := db.QueryRowContext(ctx,
		`SELECT id::text FROM team.projects LIMIT 1`).Scan(&projectID); err != nil {
		t.Skipf("跳过：没有可用于构造测试的项目 %v", err)
	}

	const testKey = "mirror-test/evict-retain.apk"
	var buildID string
	err := db.QueryRowContext(ctx, `
		INSERT INTO team.builds
			(project_id, environment, platform, app_name, original_file_name, file_size,
			 file_ext, relative_path, r2_key, r2_state, r2_bytes)
		VALUES ($1, 'test', 'android', 'mirror-probe', 'probe.apk', 1024,
		 'apk', 'test/android/mirror-probe.apk', $2, $3, 1024)
		RETURNING id::text`, projectID, testKey, contract.R2StateSynced).Scan(&buildID)
	if err != nil {
		t.Fatalf("构造测试包失败: %v", err)
	}
	defer db.ExecContext(ctx, `DELETE FROM team.builds WHERE id = $1`, buildID)

	repo := newBuildRepository(db, PartitionTeam, false)
	if err := repo.MarkEvicted(ctx, []string{buildID}); err != nil {
		t.Fatalf("标记淘汰失败: %v", err)
	}

	var gotKey, gotState string
	var gotBytes int64
	if err := db.QueryRowContext(ctx,
		`SELECT r2_key, r2_state, r2_bytes FROM team.builds WHERE id = $1`, buildID).
		Scan(&gotKey, &gotState, &gotBytes); err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	if gotKey != testKey {
		t.Fatalf("淘汰必须保留 r2_key 供重推复用，got=%q want=%q", gotKey, testKey)
	}
	if gotState != contract.R2StateEvicted {
		t.Fatalf("状态应为 evicted，got=%q", gotState)
	}
	if gotBytes != 0 {
		t.Fatalf("占用应归零以释放配额，got=%d", gotBytes)
	}
}

// TestEvictedBuildLeavesRetentionCandidates 验证已淘汰的包不再进候选。
//
// 淘汰后 r2_key 仍在，若候选筛选仍按「key 是否为空」判断，
// 每轮巡检都会重新处理同一批已回收的包，日志被刷满而实际无对象可删。
func TestEvictedBuildLeavesRetentionCandidates(t *testing.T) {
	db, skip := openTestDB(t)
	if skip != "" {
		t.Skip(skip)
	}
	ctx := context.Background()

	var projectID, tenantID string
	if err := db.QueryRowContext(ctx,
		`SELECT id::text, tenant_id::text FROM team.projects LIMIT 1`).
		Scan(&projectID, &tenantID); err != nil {
		t.Skipf("跳过：没有可用于构造测试的项目 %v", err)
	}

	insert := func(state string, bytes int64) string {
		var id string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO team.builds
				(project_id, environment, platform, app_name, original_file_name, file_size,
				 file_ext, relative_path, r2_key, r2_state, r2_bytes)
			VALUES ($1, 'test', 'android', 'mirror-probe', 'probe.apk', 1024,
			 'apk', 'test/android/mirror-probe.apk', 'mirror-test/candidate.apk', $2, $3)
			RETURNING id::text`, projectID, state, bytes).Scan(&id); err != nil {
			t.Fatalf("构造测试包失败: %v", err)
		}
		return id
	}
	liveID := insert(contract.R2StateSynced, 2048)
	evictedID := insert(contract.R2StateEvicted, 0)
	defer db.ExecContext(ctx, `DELETE FROM team.builds WHERE id = ANY($1)`,
		[]string{liveID, evictedID})

	repo := newBuildRepository(db, PartitionTeam, false)
	rows, err := repo.ListRetentionCandidates(ctx, tenantID)
	if err != nil {
		t.Fatalf("查询候选失败: %v", err)
	}

	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.ID] = true
	}
	if !seen[liveID] {
		t.Fatal("仍在 R2 的包应进入候选")
	}
	if seen[evictedID] {
		t.Fatal("已淘汰的包不应再进候选（key 仍保留，须按状态筛）")
	}
}

// openTestDB 打开测试库；返回非空字符串表示应跳过本次测试。
//
// 抽出来是因为三个用例都需要同一段「连不上就跳过」的样板，
// 复制三遍会让真正要断言的逻辑被淹没。
func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		return nil, "跳过：无法读取配置 " + err.Error()
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, "跳过：无法连接数据库 " + err.Error()
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, "跳过：数据库不可达 " + err.Error()
	}
	t.Cleanup(func() { db.Close() })
	return db, ""
}

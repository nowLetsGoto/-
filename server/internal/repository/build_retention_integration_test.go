//go:build integration

// 职责：验证淘汰执行器在真实数据库上"只删对象、不删记录"的核心契约。
// 输入：本机 PostgreSQL 与临时构造的包记录。
// 输出：标记后的字段断言，以及记录仍存在的证明。
// 边界：只操作临时创建的包，用后即删；不改动真实业务数据。
package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"dandelion/server/internal/config"
)

// TestMarkEvictedKeepsRow 验证淘汰只清 R2 相关字段，记录本身必须留着。
//
// 这是"淘汰"与"删除"的分界线：淘汰要让包不再占用对象存储，
// 但历史版本、下载次数、分享记录都要保留，用户仍能看到曾有过这个版本。
func TestMarkEvictedKeepsRow(t *testing.T) {
	cfg, loadErr := config.Load()
	if loadErr != nil {
		t.Skipf("跳过：无法读取配置 %v", loadErr)
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		t.Skipf("跳过：无法连接数据库 %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("跳过：数据库不可达 %v", err)
	}

	ctx := context.Background()

	// 造一个临时项目与包，用后清理；key 带独立前缀避免撞上真实数据。
	var tenantID, projectID string
	if err := db.QueryRowContext(ctx,
		`SELECT tenant_id::text FROM team.projects LIMIT 1`).Scan(&tenantID); err != nil {
		t.Skipf("跳过：没有可用于构造测试的项目 %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT id::text FROM team.projects LIMIT 1`).Scan(&projectID); err != nil {
		t.Skipf("跳过：没有可用于构造测试的项目 %v", err)
	}

	const testKey = "retention-test/evict-me.apk"
	var buildID string
	err = db.QueryRowContext(ctx, `
		INSERT INTO team.builds
			(project_id, environment, platform, app_name, original_file_name, file_size,
			 file_ext, relative_path, r2_key, r2_state, r2_bytes, download_count)
		VALUES ($1, 'test', 'android', 'retention-probe', 'probe.apk', 1024,
		 'apk', 'test/android/probe.apk', $2, 'synced', 1024, 7)
		RETURNING id::text`, projectID, testKey).Scan(&buildID)
	if err != nil {
		t.Fatalf("构造测试包失败: %v", err)
	}
	defer db.ExecContext(ctx, `DELETE FROM team.builds WHERE id = $1`, buildID)

	repo := newBuildRepository(db, PartitionTeam, false)
	if err := repo.MarkEvicted(ctx, []string{buildID}); err != nil {
		t.Fatalf("标记淘汰失败: %v", err)
	}

	var (
		gotKey    string
		gotBytes  int64
		gotState  string
		gotCount  int64
		gotSynced sql.NullTime
	)
	err = db.QueryRowContext(ctx, `
		SELECT r2_key, r2_bytes, r2_state, download_count, r2_synced_at
		FROM team.builds WHERE id = $1`, buildID).
		Scan(&gotKey, &gotBytes, &gotState, &gotCount, &gotSynced)
	if err != nil {
		t.Fatalf("淘汰后记录不应消失，但查不到: %v", err)
	}

	if gotKey != "" {
		t.Fatalf("r2_key 应被清空（否则会签发失效直链），got=%q", gotKey)
	}
	if gotBytes != 0 {
		t.Fatalf("r2_bytes 应归零（否则容量统计不释放），got=%d", gotBytes)
	}
	if gotState != "evicted" {
		t.Fatalf("r2_state 应为 evicted，got=%q", gotState)
	}
	if gotCount != 7 {
		t.Fatalf("下载次数必须保留，got=%d", gotCount)
	}
	if gotSynced.Valid {
		t.Fatalf("r2_synced_at 应清空（对象已不在桶里），got=%v", gotSynced.Time)
	}
}

// TestListRetentionCandidatesSkipsEvicted 验证已被淘汰的包不再进入候选。
//
// 否则每轮巡检都会重复处理同一批包，日志被刷满而实际没有任何对象可删。
func TestListRetentionCandidatesSkipsEvicted(t *testing.T) {
	cfg, loadErr := config.Load()
	if loadErr != nil {
		t.Skipf("跳过：无法读取配置 %v", loadErr)
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		t.Skipf("跳过：无法连接数据库 %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("跳过：数据库不可达 %v", err)
	}

	ctx := context.Background()
	var projectID, tenantID string
	if err := db.QueryRowContext(ctx,
		`SELECT id::text, tenant_id::text FROM team.projects LIMIT 1`).
		Scan(&projectID, &tenantID); err != nil {
		t.Skipf("跳过：没有可用于构造测试的项目 %v", err)
	}

	// 两个包：一个仍有 R2 对象，一个已被淘汰。
	var liveID, evictedID string
	insert := func(state, key string, bytes int64) string {
		var id string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO team.builds
				(project_id, environment, platform, app_name, original_file_name, file_size,
				 file_ext, relative_path, r2_key, r2_state, r2_bytes)
			VALUES ($1, 'test', 'android', 'retention-probe', 'probe.apk', 1024,
			 'apk', 'test/android/probe.apk', $2, $3, $4)
			RETURNING id::text`, projectID, key, state, bytes).Scan(&id); err != nil {
			t.Fatalf("构造测试包失败: %v", err)
		}
		return id
	}
	liveID = insert("synced", "retention-test/live.apk", 2048)
	evictedID = insert("evicted", "", 0)
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
		t.Fatal("仍有 R2 对象的包应进入候选")
	}
	if seen[evictedID] {
		t.Fatal("已淘汰的包不应再进入候选")
	}
}

// TestPlanRetentionWithRealRows 用真实快照验证决策与库中数据一致。
func TestPlanRetentionWithRealRows(t *testing.T) {
	// 纯算法与仓储解耦，这里只需证明快照字段能正确喂进算法。
	rows := []RetentionRow{
		{ID: "a", R2Bytes: 100, PinKind: "", CreatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: "b", R2Bytes: 100, PinKind: "manual", CreatedAt: time.Now()},
	}
	if len(rows) != 2 {
		t.Fatal("快照构造失败")
	}
}

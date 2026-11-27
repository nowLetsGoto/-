//go:build integration

// 职责：验证本机留底的状态机在真实数据库上的行为。
// 输入：本机 PostgreSQL 与临时构造的包记录。
// 输出：对抢占互斥、状态查询、状态推进的断言。
// 边界：只操作临时创建的包，用后即删；不触碰真实业务数据。
package repository

import (
	"context"
	"database/sql"
	"testing"

	"dandelion/server/internal/contract"
)

// TestClaimLocalPullIsExclusive 验证抢占是互斥的。
//
// 巡检与提交后的即时拉取可能同时盯上同一个包。若两者都判定「该我拉」，
// 同一个 R2 对象会被下载两份、并发写同一个临时文件，结果是文件损坏。
// 抢占靠一条带条件的 UPDATE 实现，第二次必须抢不到。
func TestClaimLocalPullIsExclusive(t *testing.T) {
	db, skip := openTestDB(t)
	if skip != "" {
		t.Skip(skip)
	}
	ctx := context.Background()
	id := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalAbsent)

	repo := newBuildRepository(db, PartitionTeam, false)

	first, err := repo.ClaimLocalPull(ctx, id)
	if err != nil {
		t.Fatalf("首次抢占失败: %v", err)
	}
	if !first {
		t.Fatal("首次抢占应成功")
	}

	second, err := repo.ClaimLocalPull(ctx, id)
	if err != nil {
		t.Fatalf("二次抢占失败: %v", err)
	}
	if second {
		t.Fatal("已处于 pulling 时应抢不到，否则会有两份并发下载")
	}
}

// TestListLocalPendingSkipsPullingAndPresent 验证候选筛选。
//
// 含「正在拉的 pulling」会导致同一对象被反复取出、并发下载；
// 含 present 会让巡检每轮重搬已经归档好的包，白耗带宽。
// 卡死已久的 pulling 不在排除之列，那是回收路径，见 TestClaimLocalPullReclaimsStale。
func TestListLocalPendingSkipsPullingAndPresent(t *testing.T) {
	db, skip := openTestDB(t)
	if skip != "" {
		t.Skip(skip)
	}
	ctx := context.Background()

	absentID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalAbsent)
	failedID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalFailed)
	pullingID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalPulling)
	presentID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalPresent)

	// 新拉回会在抢占时写时间戳；有时间戳且未超时的才是「正在拉」。
	// 时间戳为空的旧记录一律视为卡死，应该进入待拉回被回收。
	if _, err := db.ExecContext(ctx,
		`UPDATE team.builds SET local_pulling_at = now() WHERE id = $1`, pullingID); err != nil {
		t.Fatalf("设置拉取时间戳失败: %v", err)
	}

	var tenantID string
	db.QueryRowContext(ctx, `SELECT p.tenant_id::text FROM team.builds b
		JOIN team.projects p ON p.id = b.project_id WHERE b.id = $1`, absentID).Scan(&tenantID)

	repo := newBuildRepository(db, PartitionTeam, false)
	rows, err := repo.ListLocalPending(ctx, tenantID)
	if err != nil {
		t.Fatalf("查询待拉回失败: %v", err)
	}

	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.ID] = true
	}
	if !seen[absentID] {
		t.Error("absent 的包应进入待拉回")
	}
	if !seen[failedID] {
		t.Error("failed 的包应进入待拉回（等待重试）")
	}
	if seen[pullingID] {
		t.Error("pulling 的包不应进入待拉回（会并发下载同一对象）")
	}
	if seen[presentID] {
		t.Error("present 的包不应进入待拉回（已经归档好了）")
	}
}

// TestListLocalPendingReclaimsStalePulling 验证卡死的 pulling 会被巡检重新捞出。
//
// 筛选与抢占必须一致：这里捞不出来，ClaimLocalPull 允许重抢也毫无意义。
// NULL 时间戳同样要覆盖——字段是后加的，存量记录都是 NULL（实测卡过 18 小时）。
func TestListLocalPendingReclaimsStalePulling(t *testing.T) {
	db, skip := openTestDB(t)
	if skip != "" {
		t.Skip(skip)
	}
	ctx := context.Background()

	staleID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalPulling)
	nullID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalPulling)
	db.ExecContext(ctx,
		`UPDATE team.builds SET local_pulling_at = now() - interval '2 hours' WHERE id = $1`, staleID)
	db.ExecContext(ctx,
		`UPDATE team.builds SET local_pulling_at = NULL WHERE id = $1`, nullID)

	var tenantID string
	db.QueryRowContext(ctx, `SELECT p.tenant_id::text FROM team.builds b
		JOIN team.projects p ON p.id = b.project_id WHERE b.id = $1`, staleID).Scan(&tenantID)

	rows, err := newBuildRepository(db, PartitionTeam, false).ListLocalPending(ctx, tenantID)
	if err != nil {
		t.Fatalf("查询待拉回失败: %v", err)
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.ID] = true
	}
	if !seen[staleID] || !seen[nullID] {
		t.Errorf("卡死的 pulling 应进入待拉回，got stale=%v null=%v", seen[staleID], seen[nullID])
	}
}

// TestSetLocalState 验证状态推进只改 local_state，不碰 r2 侧字段。
func TestSetLocalState(t *testing.T) {
	db, skip := openTestDB(t)
	if skip != "" {
		t.Skip(skip)
	}
	ctx := context.Background()
	id := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalAbsent)

	repo := newBuildRepository(db, PartitionTeam, false)
	if err := repo.SetLocalState(ctx, id, contract.LocalPresent); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}

	var local, r2 string
	var bytes int64
	if err := db.QueryRowContext(ctx,
		`SELECT local_state, r2_state, r2_bytes FROM team.builds WHERE id = $1`, id).
		Scan(&local, &r2, &bytes); err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	if local != contract.LocalPresent {
		t.Fatalf("local_state 应更新为 present，got=%q", local)
	}
	if r2 != contract.R2StateSynced {
		t.Fatalf("local_state 的更新不该影响 r2_state，got=%q", r2)
	}
	if bytes != 1024 {
		t.Fatalf("local_state 的更新不该影响 r2_bytes，got=%d", bytes)
	}
}

// insertLocalProbe 造一个包用于留底测试，并在测试结束后删除。
func insertLocalProbe(t *testing.T, db *sql.DB, r2State, localState string) string {
	t.Helper()
	ctx := context.Background()
	var projectID string
	if err := db.QueryRowContext(ctx,
		`SELECT id::text FROM team.projects LIMIT 1`).Scan(&projectID); err != nil {
		t.Skipf("跳过：没有可用于构造测试的项目 %v", err)
	}

	var id string
	err := db.QueryRowContext(ctx, `
		INSERT INTO team.builds
			(project_id, environment, platform, app_name, original_file_name, file_size,
			 file_ext, relative_path, r2_key, r2_state, r2_bytes, local_state)
		VALUES ($1, 'test', 'android', 'local-probe', 'probe.apk', 1024,
		 'apk', 'test/android/local-probe.apk', 'mirror-test/local.apk', $2, 1024, $3)
		RETURNING id::text`, projectID, r2State, localState).Scan(&id)
	if err != nil {
		t.Fatalf("构造测试包失败: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(context.Background(), `DELETE FROM team.builds WHERE id = $1`, id)
	})
	return id
}

// TestClaimLocalPullReclaimsStale 验证卡死的 pulling 能被重新抢到。
//
// 这条与 ListLocalPending 的筛选必须一致：巡检把它捞出来，PullLocal 就该抢到它。
// 若 ClaimLocalPull 只写 local_state <> 'pulling'，被捞出来的卡死记录会因为
// 状态已是 pulling 而抢不到——表现为「巡检补拉数不为 0、状态却永远不动」。
// 实测这个不一致让 4 个包卡了 18 小时。
func TestClaimLocalPullReclaimsStale(t *testing.T) {
	db, skip := openTestDB(t)
	if skip != "" {
		t.Skip(skip)
	}
	ctx := context.Background()

	// 卡死：pulling 且时间戳很远（或没有时间戳，存量记录就是 NULL）。
	staleID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalPulling)
	db.ExecContext(ctx,
		`UPDATE team.builds SET local_pulling_at = now() - interval '2 hours' WHERE id = $1`, staleID)

	nullID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalPulling)
	db.ExecContext(ctx, `UPDATE team.builds SET local_pulling_at = NULL WHERE id = $1`, nullID)

	// 正在拉：刚写入时间戳，不能被抢。
	freshID := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalPulling)
	db.ExecContext(ctx, `UPDATE team.builds SET local_pulling_at = now() WHERE id = $1`, freshID)

	repo := newBuildRepository(db, PartitionTeam, false)

	for _, c := range []struct {
		name string
		id   string
		want bool
	}{
		{"超时的 pulling 应能重抢", staleID, true},
		{"无时间戳的 pulling 应能重抢（存量记录）", nullID, true},
		{"刚进 pulling 的不该被抢", freshID, false},
	} {
		got, err := repo.ClaimLocalPull(ctx, c.id)
		if err != nil {
			t.Fatalf("%s: 抢占出错 %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: got=%v want=%v", c.name, got, c.want)
		}
	}
}

// TestClaimLocalPullStampsTime 验证抢占写时间戳、收尾清时间戳。
func TestClaimLocalPullStampsTime(t *testing.T) {
	db, skip := openTestDB(t)
	if skip != "" {
		t.Skip(skip)
	}
	ctx := context.Background()
	id := insertLocalProbe(t, db, contract.R2StateSynced, contract.LocalAbsent)

	repo := newBuildRepository(db, PartitionTeam, false)
	if _, err := repo.ClaimLocalPull(ctx, id); err != nil {
		t.Fatalf("抢占失败: %v", err)
	}

	var stamped sql.NullTime
	db.QueryRowContext(ctx,
		`SELECT local_pulling_at FROM team.builds WHERE id = $1`, id).Scan(&stamped)
	if !stamped.Valid {
		t.Fatal("抢占必须写下 local_pulling_at，否则无法判断拉取是否已卡死")
	}

	if err := repo.SetLocalState(ctx, id, contract.LocalPresent); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}
	db.QueryRowContext(ctx,
		`SELECT local_pulling_at FROM team.builds WHERE id = $1`, id).Scan(&stamped)
	if stamped.Valid {
		t.Fatal("离开 pulling 后应清空 local_pulling_at")
	}
}

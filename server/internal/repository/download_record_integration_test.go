//go:build integration

// 职责：验证下载记录会同时更新计数与访问时间——淘汰算法的排序依据。
// 输入：本机 PostgreSQL 与临时构造的包记录。
// 输出：对 download_count 与 last_accessed_at 的断言。
// 边界：只操作临时创建的包，用后即删；不改动真实业务数据。
package repository

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"dandelion/server/internal/config"
)

// TestRecordDownloadTouchesAccessedAt 是这次修复的核心断言。
//
// 淘汰按"最久未被访问优先"排序，若 last_accessed_at 从未被写入，
// 排序会静默退化为纯按上传时间——不报错，只在配额紧张时删错包。
// 因此这两件事必须绑在同一条语句里，且要有测试盯住。
func TestRecordDownloadTouchesAccessedAt(t *testing.T) {
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
	var projectID string
	if err := db.QueryRowContext(ctx,
		`SELECT id::text FROM team.projects LIMIT 1`).Scan(&projectID); err != nil {
		t.Skipf("跳过：没有可用于构造测试的项目 %v", err)
	}

	// 造一个从未被下载过的包：last_accessed_at 初值为 NULL。
	var buildID string
	err = db.QueryRowContext(ctx, `
		INSERT INTO team.builds
			(project_id, environment, platform, app_name, original_file_name, file_size,
			 file_ext, relative_path, download_count)
		VALUES ($1, 'test', 'android', 'access-probe', 'probe.apk', 1024,
		 'apk', 'test/android/probe.apk', 0)
		RETURNING id::text`, projectID).Scan(&buildID)
	if err != nil {
		t.Fatalf("构造测试包失败: %v", err)
	}
	defer db.ExecContext(ctx, `DELETE FROM team.builds WHERE id = $1`, buildID)

	repo := newBuildRepository(db, PartitionTeam, false)

	count, err := repo.RecordDownload(ctx, buildID)
	if err != nil {
		t.Fatalf("记录下载失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("计数应为 1，got=%d", count)
	}

	var accessed sql.NullTime
	var gotCount int64
	if err := db.QueryRowContext(ctx,
		`SELECT last_accessed_at, download_count FROM team.builds WHERE id = $1`, buildID).
		Scan(&accessed, &gotCount); err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	if !accessed.Valid {
		t.Fatal("last_accessed_at 必须被写入，否则淘汰排序会退化为纯按上传时间")
	}
	if gotCount != 1 {
		t.Fatalf("download_count 应为 1，got=%d", gotCount)
	}

	// 再下载一次：计数递增，访问时间随之更新。
	if _, err := repo.RecordDownload(ctx, buildID); err != nil {
		t.Fatalf("第二次记录失败: %v", err)
	}
	var second sql.NullTime
	var secondCount int64
	db.QueryRowContext(ctx,
		`SELECT last_accessed_at, download_count FROM team.builds WHERE id = $1`, buildID).
		Scan(&second, &secondCount)

	if secondCount != 2 {
		t.Fatalf("二次下载后计数应为 2，got=%d", secondCount)
	}
	if !second.Valid || second.Time.Before(accessed.Time) {
		t.Fatal("访问时间不应回退")
	}
}

// TestRecordDownloadReportsMissingBuild 验证包不存在时返回 NotFound。
//
// 调用方靠这个信号判断"不在这个分区"，不能把跨分区探测的落空当成故障。
func TestRecordDownloadReportsMissingBuild(t *testing.T) {
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

	repo := newBuildRepository(db, PartitionTeam, false)
	_, err = repo.RecordDownload(context.Background(),
		"00000000-0000-4000-8000-000000000000")
	if err == nil {
		t.Fatal("不存在的包应返回错误")
	}
}

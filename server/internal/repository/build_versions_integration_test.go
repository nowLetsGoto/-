//go:build integration

// 职责：用 PostgreSQL 临时表验证公开版本列表的完整性、排序与数据边界。
// 输入：config.Load 读取的本地数据库配置；输出：跨环境与隔离规则的回归断言。
// 边界：仅在 integration 标签下运行；所有数据位于独立连接的临时表，不修改业务表。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
)

func TestPublicVersionsAcrossEnvironments(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// 临时表绑定数据库连接；固定单连接保证建表、写入、查询使用同一个临时 schema。
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, query := range []string{
		`CREATE TEMP TABLE download_version_projects (id TEXT PRIMARY KEY, key TEXT, icon_url TEXT)`,
		// 列必须与 buildColumns 完全对齐：rowToBuild 按固定顺序 Scan，
		// 少一列就会在读取时报 "column does not exist"，而不是静默缺字段。
		`CREATE TEMP TABLE download_version_builds (
			id TEXT PRIMARY KEY, project_id TEXT, environment TEXT, platform TEXT,
			app_name TEXT, package_id TEXT, version TEXT, build_number TEXT,
			changelog TEXT, original_file_name TEXT, file_size BIGINT, file_ext TEXT,
			relative_path TEXT, meta_source TEXT, download_count BIGINT,
			created_at TIMESTAMPTZ, owner_user_id TEXT,
			r2_key TEXT NOT NULL DEFAULT '', pin_kind TEXT NOT NULL DEFAULT '',
			r2_state TEXT NOT NULL DEFAULT 'pending', local_state TEXT NOT NULL DEFAULT 'absent',
			local_pulled_bytes BIGINT NOT NULL DEFAULT 0, local_error TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO download_version_projects VALUES ('project-a', 'app', ''), ('project-b', 'other', '')`,
		`INSERT INTO download_version_builds
			SELECT 'test-' || n, 'project-a', 'test', 'android', '应用', 'com.example.app',
				'1.' || n, n::text, '测试更新 ' || n, 'app.apk', 1024, 'apk',
				'internal/path', 'manual', 0,
				'2026-11-24T00:00:00Z'::timestamptz + n * INTERVAL '1 second', 'owner-a'
			FROM generate_series(1, 15) n`,
		`INSERT INTO download_version_builds
			SELECT env, 'project-a', env, 'android', '应用', 'com.example.app',
				'1.0', '1', '各环境更新说明', 'app.apk', 2048, 'apk',
				'internal/path', 'manual', 0, '2026-11-23T00:00:00Z', 'owner-a'
			FROM unnest(ARRAY['prd', 'pre', 'hotfix']) env`,
		`INSERT INTO download_version_builds
			SELECT 'ios-' || env, 'project-a', env, 'ios', '应用', 'com.example.app.ios',
				'1.0', '1', 'iOS 更新说明', 'app.ipa', 4096, 'ipa',
				'internal/ios', 'manual', 0, '2026-11-24T01:00:00Z', 'owner-a'
			FROM unnest(ARRAY['test', 'prd']) env`,
		// 这些包刻意与目标包仅相差一个边界，均不应出现在公开版本列表。
		`INSERT INTO download_version_builds
			SELECT 'foreign-' || boundary, project, 'prd', platform, name, package,
				'9.9', '999', '', 'app.apk', 1, 'apk', 'secret/path', 'manual', 0,
				'2026-11-25T00:00:00Z', owner
			FROM (VALUES
				('platform', 'project-a', 'ios', '应用', 'com.example.app', 'owner-a'),
				('project', 'project-b', 'android', '应用', 'com.example.app', 'owner-a'),
				('name', 'project-a', 'android', '其它应用', 'com.example.app', 'owner-a'),
				('package', 'project-a', 'android', '应用', 'com.example.other', 'owner-a'),
				('owner', 'project-a', 'android', '应用', 'com.example.app', 'owner-b')
			) AS excluded(boundary, project, platform, name, package, owner)`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}

	repo := &BuildRepository{
		db: db, table: "pg_temp.download_version_builds", project: "pg_temp.download_version_projects",
	}
	items, projectKey, err := repo.ListSiblingVersions(ctx, "test-15", false)
	if err != nil {
		t.Fatal(err)
	}
	if projectKey != "app" || len(items) != 17 {
		t.Fatalf("应返回同应用完整的 17 个其它版本，实际 project=%q count=%d", projectKey, len(items))
	}
	environments := make(map[string]bool)
	for i, item := range items {
		if item.ID == "test-15" || item.OwnerUserID != "owner-a" || item.Platform != "android" || item.AppName != "应用" || item.PackageID != "com.example.app" {
			t.Fatalf("列表包含当前包或越界包：%+v", item)
		}
		if item.Changelog == "" || item.FileSize == 0 {
			t.Fatalf("版本选择所需的更新说明或文件大小缺失：%s", item.ID)
		}
		environments[item.Environment] = true
		if i > 0 {
			previous, _ := time.Parse(time.RFC3339Nano, items[i-1].CreatedAt)
			current, _ := time.Parse(time.RFC3339Nano, item.CreatedAt)
			if current.After(previous) || (current.Equal(previous) && item.ID > items[i-1].ID) {
				t.Fatalf("版本未按发布时间及 id 倒序：%s / %s", items[i-1].ID, item.ID)
			}
		}
	}
	for _, environment := range []string{"test", "prd", "pre", "hotfix"} {
		if !environments[environment] {
			t.Errorf("较旧的 %s 环境被新测试包挤出列表", environment)
		}
	}
	allPlatforms, _, err := repo.ListSiblingVersions(ctx, "test-15", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(allPlatforms) != 20 {
		t.Fatalf("跨平台应包含 17 个 Android 和 3 个 iOS 包，实际 %d", len(allPlatforms))
	}
	iosFound := false
	for _, item := range allPlatforms {
		if item.OwnerUserID != "owner-a" || item.AppName != "应用" || item.ID == "foreign-project" || item.ID == "foreign-package" {
			t.Fatalf("跨平台查询越过项目、应用、上传者或同平台包名边界：%+v", item)
		}
		if item.ID == "ios-test" && item.PackageID == "com.example.app.ios" {
			iosFound = true
		}
	}
	if !iosFound {
		t.Fatal("跨平台查询未返回使用不同 Bundle ID 的 iOS 包")
	}
	fromIOS, _, err := repo.ListSiblingVersions(ctx, "ios-test", true)
	if err != nil {
		t.Fatal(err)
	}
	androidFound := false
	for _, item := range fromIOS {
		if item.ID == "test-15" {
			androidFound = true
		}
		if item.Platform == "ios" && item.PackageID != "com.example.app.ios" {
			t.Fatalf("iOS 版本混入不同 Bundle ID：%+v", item)
		}
	}
	if !androidFound {
		t.Fatal("iOS 入口未返回 Android 包，反向切换不可用")
	}
	if _, _, err := repo.ListSiblingVersions(ctx, "missing", true); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("不存在的公开包应返回 404，实际：%v", err)
	}
}

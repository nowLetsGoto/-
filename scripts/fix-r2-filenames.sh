#!/bin/bash
# 为已存在的 R2 对象补写 Content-Disposition，让 CDN 下载带回原始文件名。
#
# 背景：直传对象由浏览器 PUT 生成，早期版本没有写入下载名元数据，
# 302 到 CDN 后浏览器只能拿 URL 末段的 UUID 当文件名（如 1d828823-....apk）。
# 新代码在发布时自动补写；本脚本处理历史对象。
#
# 用法：cd ~/Desktop/dandelion && bash scripts/fix-r2-filenames.sh

set -euo pipefail
cd "$(dirname "$0")/.."

set -a
source .env
set +a

echo "=== 为 R2 对象补写下载文件名 ==="
echo

# 用生产代码的 SetContentDisposition，保证编码与运行时一致
cat > /tmp/fix_r2_names.go << 'GO'
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"dandelion/server/internal/storage"
)

func main() {
	cfg := storage.R2Config{
		AccountID:     os.Getenv("R2_ACCOUNT_ID"),
		AccessKeyID:   os.Getenv("R2_ACCESS_KEY_ID"),
		SecretKey:     os.Getenv("R2_SECRET_ACCESS_KEY"),
		Bucket:        os.Getenv("R2_BUCKET"),
		PublicBaseURL: os.Getenv("R2_PUBLIC_BASE_URL"),
	}
	r2, err := storage.NewR2(context.Background(), cfg)
	if err != nil {
		fmt.Println("R2 初始化失败:", err)
		os.Exit(1)
	}

	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Println("数据库连接失败:", err)
		os.Exit(1)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT r2_key, original_file_name, platform
		FROM team.builds
		WHERE r2_key IS NOT NULL AND r2_key <> ''
		ORDER BY created_at DESC`)
	if err != nil {
		fmt.Println("查询失败:", err)
		os.Exit(1)
	}
	type item struct{ key, name, platform string }
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.key, &it.name, &it.platform); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	ok, fail := 0, 0
	for _, it := range items {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		mime, err := storage.ContentType(it.platform)
		if err != nil {
			cancel()
			continue
		}
		if err := r2.SetContentDisposition(ctx, it.key, it.name, mime); err != nil {
			fmt.Printf("  ✗ %s → %s : %v\n", it.key, it.name, err)
			fail++
		} else {
			fmt.Printf("  ✓ %s → %s\n", it.key, it.name)
			ok++
		}
		cancel()
	}
	fmt.Printf("\n完成：成功 %d，失败 %d\n", ok, fail)
}
GO

cp /tmp/fix_r2_names.go server/cmd/fixnames_tmp.go
(cd server && go run ./cmd/fixnames_tmp.go)
rm -f server/cmd/fixnames_tmp.go

echo
echo "提示：CDN 已缓存的旧响应头需等缓存过期（或手动在 Cloudflare 清除缓存）才会更新。"
echo "新上传的包不受影响——发布时会自动补写。"
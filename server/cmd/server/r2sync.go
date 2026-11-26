// r2sync.go · 把本机已有的安装包补齐到 R2（幂等、可重复执行）。
//
// 为什么需要它：新包走浏览器直传时字节直接落到 R2，r2_key 会被回写；
// 但通过 multipart 上传的包、以及启用 R2 之前发布的历史包，磁盘上有文件、
// r2_key 却是空的。下载接口只在 r2_key 非空时才 302 跳转到 R2，
// 因此这些包会一直走本机流式——家宽上行就是瓶颈，下载非常慢。
//
// 用法（在运行服务的同一套环境变量下执行）：
//
//	dandelion r2sync --dry-run     # 只列出待同步清单
//	dandelion r2sync --limit 1     # 先同步最小的一个做验证
//	dandelion r2sync               # 全量补齐
//
// 边界：只读本机文件、只写 R2 与 builds.r2_key，不改动其它字段，也不删除任何文件。
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"dandelion/server/internal/config"
	"dandelion/server/internal/storage"
)

// syncCandidate 是一条待补齐记录。分区（team/solo）决定更新哪张表。
type syncCandidate struct {
	partition    string
	id           string
	relativePath string
	platform     string
	size         int64
	fileName     string
}

func runR2Sync(args []string) error {
	fs := flag.NewFlagSet("r2sync", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "只列出待同步对象，不上传、不写库")
	limit := fs.Int("limit", 0, "最多处理多少个包（按体积从小到大），0 表示不限")
	skipIfExists := fs.Bool("skip-existing", true, "R2 上已有同尺寸对象时跳过上传，只补写 r2_key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// 用项目统一的路径对象解析文件位置：实际文件在 storage/uploads/<relative_path>，
	// 硬编码 "/uploads" 前缀会在未来目录结构调整时静默失效。SafeResolve 还带越界校验。
	paths, err := storage.NewPaths(cfg.StorageRoot)
	if err != nil {
		return fmt.Errorf("初始化存储路径失败: %w", err)
	}
	r2, err := storage.NewR2(context.Background(), storage.R2Config{
		AccountID:     cfg.R2AccountID,
		AccessKeyID:   cfg.R2AccessKeyID,
		SecretKey:     cfg.R2SecretKey,
		Bucket:        cfg.R2Bucket,
		PublicBaseURL: cfg.R2PublicBaseURL,
	})
	if err != nil {
		return fmt.Errorf("初始化对象存储失败: %w", err)
	}
	if r2 == nil {
		return errors.New("R2 未配置（需要 R2_ACCOUNT_ID / R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY / R2_BUCKET）")
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("打开数据库连接失败: %w", err)
	}
	defer db.Close()
	// 探活用独立的短超时上下文；上传必须用不带截止时间的 ctx，
	// 否则 119MB 的包会在 30 秒时被 context deadline 掐断。
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 30*time.Second)
	if err := db.PingContext(pingCtx); err != nil {
		cancelPing()
		return fmt.Errorf("PostgreSQL 不可用: %w", err)
	}
	cancelPing()
	ctx := context.Background()

	items, err := collectUnsynced(ctx, db)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		log.Println("没有待同步的包：所有记录的 r2_key 都已写入")
		return nil
	}

	// 从小到大处理：小包先成功，能尽早验证链路是否通。
	if *limit > 0 && len(items) > *limit {
		items = items[:*limit]
	}

	var totalBytes int64
	for _, it := range items {
		totalBytes += it.size
	}
	log.Printf("待同步 %d 个包，合计 %.1f MB，桶=%s", len(items), float64(totalBytes)/1024/1024, cfg.R2Bucket)
	if *dryRun {
		for _, it := range items {
			log.Printf("  [dry-run] %s/%s %s (%.1f MB) → %s",
				it.partition, it.id, it.fileName, float64(it.size)/1024/1024, it.relativePath)
		}
		return nil
	}

	var okCount, skipCount, failCount int
	startAll := time.Now()
	for i, it := range items {
		prefix := fmt.Sprintf("[%d/%d]", i+1, len(items))
		localPath, pathErr := paths.SafeResolve(it.relativePath)
		if pathErr != nil {
			log.Printf("%s 跳过 %s：路径非法 %q: %v", prefix, it.fileName, it.relativePath, pathErr)
			failCount++
			continue
		}
		info, statErr := os.Stat(localPath)
		if statErr != nil {
			log.Printf("%s 跳过 %s：本机文件不存在 %s", prefix, it.fileName, localPath)
			failCount++
			continue
		}
		if it.size > 0 && info.Size() != it.size {
			log.Printf("%s 跳过 %s：记录体积 %d 与实际 %d 不符，先人工确认",
				prefix, it.fileName, it.size, info.Size())
			failCount++
			continue
		}

		key := r2.Key(it.relativePath)

		// 已存在同尺寸对象时不必重传：既省带宽，也让命令天然可重入。
		if *skipIfExists {
			if size, found, err := r2.StatObject(ctx, key); err == nil && found && size == info.Size() {
				if err := setR2Key(ctx, db, it.partition, it.id, key); err != nil {
					log.Printf("%s %s：对象已在 R2，但写 r2_key 失败: %v", prefix, it.fileName, err)
					failCount++
					continue
				}
				log.Printf("%s 已在 R2，补写 r2_key：%s", prefix, it.fileName)
				skipCount++
				continue
			}
		}

		contentType := "application/octet-stream"
		if ct, err := storage.ContentType(it.platform); err == nil {
			contentType = ct
		}

		f, openErr := os.Open(localPath)
		if openErr != nil {
			log.Printf("%s 打开失败 %s: %v", prefix, it.fileName, openErr)
			failCount++
			continue
		}
		start := time.Now()
		putErr := r2.Put(ctx, key, f, info.Size(), contentType, it.fileName)
		f.Close()
		if putErr != nil {
			log.Printf("%s 上传失败 %s: %v", prefix, it.fileName, putErr)
			failCount++
			continue
		}
		dur := time.Since(start)
		if err := setR2Key(ctx, db, it.partition, it.id, key); err != nil {
			log.Printf("%s %s 已上传但写 r2_key 失败: %v", prefix, it.fileName, err)
			failCount++
			continue
		}
		log.Printf("%s ✅ %s (%.1f MB) 用时 %v，%.2f MB/s",
			prefix, it.fileName, float64(info.Size())/1024/1024, dur.Round(time.Second),
			float64(info.Size())/1024/1024/dur.Seconds())
		okCount++
	}

	log.Printf("完成：成功 %d，已存在 %d，失败 %d，总耗时 %v",
		okCount, skipCount, failCount, time.Since(startAll).Round(time.Second))
	if failCount > 0 {
		return fmt.Errorf("%d 个包未能同步，请查看上方日志", failCount)
	}
	return nil
}

// collectUnsynced 扫描两个分区里 r2_key 为空的记录，按体积升序返回。
func collectUnsynced(ctx context.Context, db *sql.DB) ([]syncCandidate, error) {
	var items []syncCandidate
	for _, part := range []string{"team", "solo"} {
		var exists sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1)`, part+".builds").Scan(&exists); err != nil {
			return nil, fmt.Errorf("检查分区 %s 失败: %w", part, err)
		}
		if !exists.Valid {
			continue
		}
		q := fmt.Sprintf(`SELECT id, relative_path, platform, file_size, original_file_name
			FROM %s.builds
			WHERE r2_key IS NULL OR r2_key = ''
			ORDER BY file_size ASC`, part)
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("查询 %s.builds 失败: %w", part, err)
		}
		for rows.Next() {
			var it syncCandidate
			it.partition = part
			if err := rows.Scan(&it.id, &it.relativePath, &it.platform, &it.size, &it.fileName); err != nil {
				rows.Close()
				return nil, fmt.Errorf("读取 %s.builds 记录失败: %w", part, err)
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return items, nil
}

func setR2Key(ctx context.Context, db *sql.DB, partition, id, key string) error {
	q := fmt.Sprintf(`UPDATE %s.builds SET r2_key = $2 WHERE id = $1`, partition)
	if _, err := db.ExecContext(ctx, q, id, key); err != nil {
		return err
	}
	return nil
}

// Package repository 是全仓唯一的 SQL 出入口。
// 职责：builds 表的建表、写入、按环境(+平台)查询、按 id 查询、下载成功后原子自增计数。
// 输入：context 与 DTO / 查询条件；输出：contract.Build 或计数新值。
// 边界：全部参数绑定，禁止字符串拼 SQL；不碰文件系统、不做业务校验、不生成三个 URL 字段
//       （rowToBuild 把它们留空，由 service.buildURLs 统一填充，避免换域名要刷库）；
//       pgx.ErrNoRows / sql.ErrNoRows 统一映射为 contract.ErrNotFound。
package repository

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"dandelion/server/internal/contract"
)

// schemaDDL 内嵌建表 DDL：DDL 与仓储层同包，二进制自带，
// 启动时幂等执行、不依赖当前工作目录；容器里由 initdb 执行同一份文件作为双保险。
//
//go:embed schema.sql
var schemaDDL string

// buildColumns 是行映射的唯一列清单，顺序与 rowToBuild 的 Scan 参数严格对应。
const buildColumns = `id, environment, platform, app_name, package_id, version, build_number,
	changelog, original_file_name, file_size, file_ext, relative_path, meta_source,
	download_count, created_at`

// BuildRepository 持有 database/sql 连接池；pgx/v5/stdlib 注册了驱动名 "pgx"。
type BuildRepository struct {
	db *sql.DB
}

func NewBuildRepository(db *sql.DB) *BuildRepository {
	return &BuildRepository{db: db}
}

// DB 暴露连接池供健康检查 Ping 使用（不暴露 SQL 能力给其它层）。
func (r *BuildRepository) DB() *sql.DB {
	return r.db
}

// EnsureSchema 幂等执行内嵌 DDL，进程启动时调用一次。
func (r *BuildRepository) EnsureSchema(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("初始化 builds 表失败: %w", err)
	}
	return nil
}

// NextID 先向数据库申请一个 UUID：文件落盘必须使用与入库相同的 id，
// 所以不能等 INSERT 的 RETURNING；仍由 PG 的 gen_random_uuid() 生成，零第三方 UUID 库。
func (r *BuildRepository) NextID(ctx context.Context) (string, error) {
	var id string
	if err := r.db.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		return "", fmt.Errorf("申请包标识失败: %w", err)
	}
	return id, nil
}

// Insert 写入一行完整元数据，并回填数据库生成的 created_at 与 download_count。
func (r *BuildRepository) Insert(ctx context.Context, b *contract.Build) error {
	const query = `INSERT INTO builds (
		id, environment, platform, app_name, package_id, version, build_number,
		changelog, original_file_name, file_size, file_ext, relative_path,
		meta_source, download_count, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
	RETURNING created_at, download_count`

	var createdAt time.Time
	var count int64
	err := r.db.QueryRowContext(ctx, query,
		b.ID, b.Environment, b.Platform, b.AppName, b.PackageID, b.Version, b.BuildNumber,
		b.Changelog, b.OriginalFileName, b.FileSize, b.FileExt, b.RelativePath,
		b.MetaSource, 0,
	).Scan(&createdAt, &count)
	if err != nil {
		return fmt.Errorf("写入包元数据失败: %w", err)
	}
	b.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	b.DownloadCount = count
	return nil
}

// List 固定 WHERE environment=$1 [AND platform=$2] ORDER BY created_at DESC，
// 命中 idx_builds_env_platform_created；第一版不分页，空结果返回空切片而不是 404。
func (r *BuildRepository) List(ctx context.Context, q contract.ListQuery) ([]contract.Build, error) {
	var query strings.Builder
	query.WriteString(`SELECT ` + buildColumns + ` FROM builds WHERE environment = $1`)
	args := []any{q.Environment}
	if q.Platform != "" {
		query.WriteString(` AND platform = $2`)
		args = append(args, q.Platform)
	}
	query.WriteString(` ORDER BY created_at DESC`)

	rows, err := r.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("查询包列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Build, 0)
	for rows.Next() {
		build, err := rowToBuild(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, build)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历包列表失败: %w", err)
	}
	return items, nil
}

// FindByID 返回单个包；无记录统一转成 contract.ErrNotFound 供错误中间件转 404。
func (r *BuildRepository) FindByID(ctx context.Context, id string) (contract.Build, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+buildColumns+` FROM builds WHERE id = $1`, id)
	build, err := rowToBuild(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Build{}, contract.ErrNotFound
		}
		return contract.Build{}, fmt.Errorf("查询包详情失败: %w", err)
	}
	return build, nil
}

// IncrDownloadCount 单语句原子自增；仅在流成功完成后由 service 旁路调用。
func (r *BuildRepository) IncrDownloadCount(ctx context.Context, id string) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		`UPDATE builds SET download_count = download_count + 1 WHERE id = $1 RETURNING download_count`,
		id,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("下载计数失败: %w", err)
	}
	return count, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

// rowToBuild 完成 snake_case → camelCase 映射；三个 URL 字段刻意留空，由 service 填充。
func rowToBuild(row rowScanner) (contract.Build, error) {
	var b contract.Build
	var createdAt time.Time
	err := row.Scan(
		&b.ID, &b.Environment, &b.Platform, &b.AppName, &b.PackageID,
		&b.Version, &b.BuildNumber, &b.Changelog, &b.OriginalFileName,
		&b.FileSize, &b.FileExt, &b.RelativePath, &b.MetaSource,
		&b.DownloadCount, &createdAt,
	)
	if err != nil {
		return contract.Build{}, err
	}
	b.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	return b, nil
}

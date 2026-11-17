// Package repository 是全仓唯一的 SQL 出入口（应用包部分）。
// 职责：builds 表的写入、按环境(+平台)查询、按 id 查询、下载成功后原子自增计数。
// 输入：context 与 DTO / 查询条件；输出：contract.Build 或计数新值。
// 边界：全部参数绑定，禁止字符串拼值（表名只能来自 Partition 常量）；
// 不碰文件系统、不做业务校验、不生成三个 URL 字段——rowToBuild 把它们留空，
// 由 service.buildURLs 统一填充，避免换域名要刷库；
// pgx.ErrNoRows / sql.ErrNoRows 统一映射为 contract.ErrNotFound。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
)

// buildColumns 是行映射的唯一列清单，顺序与 rowToBuild 的 Scan 参数严格对应。
const buildColumns = `id, environment, platform, app_name, package_id, version, build_number,
	changelog, original_file_name, file_size, file_ext, relative_path, meta_source,
	download_count, created_at, owner_user_id`

// BuildRepository 是一个分区内的 builds 表访问器；分区在构造时固定，实例本身无状态。
type BuildRepository struct {
	db      *sql.DB
	table   string // 全限定表名，如 team.builds；只能来自 Partition.Table
	project string // 同分区的项目表，用于把项目 key 解析成 id
}

// NewBuildPartitioned 为 team 与 solo 各建一个访问器，交给上层按会话模式取用。
func NewBuildPartitioned(db *sql.DB) Partitioned[*BuildRepository] {
	return NewPartitioned(newBuildRepository(db, PartitionTeam), newBuildRepository(db, PartitionSolo))
}

func newBuildRepository(db *sql.DB, schema Partition) *BuildRepository {
	return &BuildRepository{
		db:      db,
		table:   schema.Table("builds"),
		project: schema.Table("projects"),
	}
}

// DB 暴露连接池供健康检查 Ping 使用（不暴露 SQL 能力给其它层）。
// 连接池与分区无关，因此只挂在默认（团队）实例上即可被装配层取得。
func (r *BuildRepository) DB() *sql.DB { return r.db }

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
// projectID 由 service 先按项目 key 解析好再传入，仓储层不关心 key。
func (r *BuildRepository) Insert(ctx context.Context, projectID string, b *contract.Build) error {
	query := `INSERT INTO ` + r.table + ` (
		id, project_id, environment, platform, app_name, package_id, version, build_number,
		changelog, original_file_name, file_size, file_ext, relative_path,
		meta_source, download_count, created_at, owner_user_id
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now(), $16)
	RETURNING created_at, download_count`

	var createdAt time.Time
	var count int64
	err := r.db.QueryRowContext(ctx, query,
		b.ID, projectID, b.Environment, b.Platform, b.AppName, b.PackageID, b.Version, b.BuildNumber,
		b.Changelog, b.OriginalFileName, b.FileSize, b.FileExt, b.RelativePath,
		b.MetaSource, 0, ownerUserID(b.OwnerUserID),
	).Scan(&createdAt, &count)
	if err != nil {
		return fmt.Errorf("写入包元数据失败: %w", err)
	}
	b.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	b.DownloadCount = count
	return nil
}

// List 固定 WHERE project_id=$1 AND environment=$2 [AND platform=$3] ORDER BY created_at DESC，
// 命中 idx_builds_project_env_platform_created；第一版不分页，空结果返回空切片而不是 404。
// 不再按 owner 过滤：单人隔离已由「项目属于谁」保证——
// solo 分区的项目按 owner 创建与解析，只有本人能往自己的项目上传，所以包天然属于本人。
func (r *BuildRepository) List(ctx context.Context, projectID string, q contract.ListQuery) ([]contract.Build, error) {
	var query strings.Builder
	query.WriteString(`SELECT ` + buildColumns + ` FROM ` + r.table + ` WHERE project_id = $1 AND environment = $2`)
	args := []any{projectID, q.Environment}
	if q.Platform != "" {
		query.WriteString(` AND platform = $3`)
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
	row := r.db.QueryRowContext(ctx, `SELECT `+buildColumns+` FROM `+r.table+` WHERE id = $1`, id)
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
// 包里没这一行时同样返回 contract.ErrNotFound：调用方据此判断"不在这个分区"，
// 不至于把跨分区探测的正常落空当成故障。
func (r *BuildRepository) IncrDownloadCount(ctx context.Context, id string) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		`UPDATE `+r.table+` SET download_count = download_count + 1 WHERE id = $1 RETURNING download_count`,
		id,
	).Scan(&count)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return 0, contract.ErrNotFound
		}
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
		&b.DownloadCount, &createdAt, &b.OwnerUserID,
	)
	if err != nil {
		return contract.Build{}, err
	}
	b.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	return b, nil
}

// ownerUserID 保证 owner 永远有值：system 代表不是某个登录人手动上传的（外部 API / 历史数据）。
func ownerUserID(owner string) string {
	if owner == "" {
		return "system"
	}
	return owner
}

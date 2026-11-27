// Package repository 是全仓唯一的 SQL 出入口（应用包部分）。
// 职责：builds 表的写入、按环境(+平台)查询、按 id 查询、下载成功后原子自增计数。
// 输入：context 与 DTO / 查询条件；输出：contract.Build 或计数新值。
// 边界：全部参数绑定，禁止字符串拼值（表名只能来自 Partition 常量）；
// 不碰文件系统、不做业务校验、不生成三个 URL 字段——rowToBuild 把它们留空，
// 由 service.buildURLs 统一填充，避免换域名要刷库；
// pgx.ErrNoRows / sql.ErrNoRows 统一映射为 contract.NotFound(contract.ResourceBuild)。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
)

// buildColumns 是行映射的唯一列清单，顺序与 rowToBuild 的 Scan 参数严格对应。
//
// 收录原则是「列表或详情要显示什么」：pin_kind / r2_state / local_state 决定行内
// 显示哪些标记与操作，因此必须在；r2_bytes、last_accessed_at、pinned_by 等
// 只参与内部决策，由治理专用查询按需读取，不进这条热路径。
const buildColumns = `id, environment, platform, app_name, package_id, version, build_number,
	changelog, original_file_name, file_size, file_ext, relative_path, meta_source,
	download_count, created_at, owner_user_id, r2_key, pin_kind, r2_state, local_state,
	local_pulled_bytes, local_error`

// columns 在固定列后追加两个派生列，都从同分区的 projects 表按 project_id 子查询：
//   - icon_url：公开下载页要显示，Build 表不冗余存，避免图标更新后还是旧值；
//   - tenant_id：R2 按公司隔离后，每条对象存储调用都要凭它选中该公司的客户端。
//
// tenant_id 不做冗余列而是每次 join：它是「项目归哪家公司」的函数，
// 项目若被调整归属（管理员操作），包记录不该停留在一个过期的副本上。
//
// 两个分区（team / solo）的 projects 表同构，可用同一份 SQL，
// 因此这里不需要按分区分支——见 schema.sql 中「保留同构列」的说明。
func (r *BuildRepository) columns() string {
	return buildColumns + `,
		(SELECT p.icon_url FROM ` + r.project + ` p WHERE p.id = project_id),
		coalesce((SELECT p.tenant_id::text FROM ` + r.project + ` p WHERE p.id = project_id), '')`
}

// BuildRepository 是一个分区内的 builds 表访问器；分区在构造时固定，实例本身无状态。
type BuildRepository struct {
	db      *sql.DB
	table   string // 全限定表名，如 team.builds；只能来自 Partition.Table
	project string // 同分区的项目表，用于把项目 key 解析成 id
	share   string // 同分区分享表，与新包在同一事务里创建
	// ownerScoped 为真表示该表按创建者隔离（单人分区）：列表必须带 owner_user_id 条件。
	// 与 project 仓储的 ownerScoped 同名同义——两处隔离用同一个词，读代码时不必重新推断。
	// 这一层校验是兜底：正常路径由 service 先 resolveProject（带 owner）再按项目查，
	// 但"先解析项目"只是调用点的约定，仓储自己带上 owner 条件才不依赖任何人记得。
	ownerScoped bool
}

// NewBuildPartitioned 为 team 与 solo 各建一个访问器，交给上层按会话模式取用。
func NewBuildPartitioned(db *sql.DB) Partitioned[*BuildRepository] {
	return NewPartitioned(newBuildRepository(db, PartitionTeam, false), newBuildRepository(db, PartitionSolo, true))
}

func newBuildRepository(db *sql.DB, schema Partition, ownerScoped bool) *BuildRepository {
	return &BuildRepository{
		db:          db,
		table:       schema.Table("builds"),
		project:     schema.Table("projects"),
		share:       schema.Table("build_shares"),
		ownerScoped: ownerScoped,
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启发布事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	query := `INSERT INTO ` + r.table + ` (
		id, project_id, environment, platform, app_name, package_id, version, build_number,
		changelog, original_file_name, file_size, file_ext, relative_path,
		meta_source, download_count, created_at, owner_user_id, local_state
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now(), $16, $17)
	RETURNING created_at, download_count`

	var createdAt time.Time
	var count int64
	err = tx.QueryRowContext(ctx, query,
		b.ID, projectID, b.Environment, b.Platform, b.AppName, b.PackageID, b.Version, b.BuildNumber,
		b.Changelog, b.OriginalFileName, b.FileSize, b.FileExt, b.RelativePath,
		b.MetaSource, 0, ownerUserID(b.OwnerUserID), b.LocalState,
	).Scan(&createdAt, &count)
	if err != nil {
		return fmt.Errorf("写入包元数据失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+r.share+` (build_id) VALUES ($1)`, b.ID); err != nil {
		return fmt.Errorf("创建包分享授权失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交发布事务失败: %w", err)
	}
	b.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	b.DownloadCount = count
	return nil
}

// List 固定 WHERE project_id=$1 [AND environment=$2] [AND platform=$3] [AND owner_user_id=$n]
// ORDER BY created_at DESC，命中 idx_builds_project_env_platform_created；第一版不分页，
// 空结果返回空切片而不是 404。
//
// ownerUserID 在单人分区是**必需**条件：项目本身已按 owner 隔离，这里再加一道，
// 使"忘了先 resolveProject"也不会读到别人的包——隔离从此是仓储的性质，不是调用点的纪律。
// 团队分区忽略该参数（团队数据本就共享）。
func (r *BuildRepository) List(ctx context.Context, projectID string, q contract.ListQuery, ownerUserID string) ([]contract.Build, error) {
	filter := NewFilter(0).
		Add(`project_id = ?`, projectID).
		AddIf(q.Environment != "", `environment = ?`, q.Environment).
		AddIf(q.Platform != "", `platform = ?`, q.Platform).
		AddIf(r.ownerScoped && ownerUserID != "", `owner_user_id = ?`, ownerUserID)

	query := `SELECT ` + r.columns() + ` FROM ` + r.table + filter.Where() + ` ORDER BY created_at DESC, id DESC`

	rows, err := r.db.QueryContext(ctx, query, filter.Args()...)
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

// FindByID 返回单个包；无记录统一转成 contract.NotFound(contract.ResourceBuild) 供错误中间件转 404。
func (r *BuildRepository) FindByID(ctx context.Context, id string) (contract.Build, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+r.columns()+` FROM `+r.table+` WHERE id = $1`, id)
	build, err := rowToBuild(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return contract.Build{}, contract.NotFound(contract.ResourceBuild)
		}
		return contract.Build{}, fmt.Errorf("查询包详情失败: %w", err)
	}
	return build, nil
}

// ListSiblingVersions 返回同项目、同应用、同上传者的其它包，按时间倒序。
// 默认按平台和 package_id 收敛；includePlatforms 允许不同平台使用不同包名。
// 调用方先按真实项目校验登录读取权限，或校验来源包的独立分享凭证；SQL 再限定项目 / 应用 / owner，
// 不依赖调用方传 projectID（Build DTO 本身不带这一列）。
func (r *BuildRepository) ListSiblingVersions(ctx context.Context, id string, includePlatforms bool) ([]contract.Build, string, error) {
	var projectKey string
	check := `SELECT (SELECT key FROM ` + r.project + ` WHERE id = b.project_id)
		FROM ` + r.table + ` b WHERE b.id = $1`
	if err := r.db.QueryRowContext(ctx, check, id).Scan(&projectKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return nil, "", contract.NotFound(contract.ResourceBuild)
		}
		return nil, "", fmt.Errorf("查询包所在分区失败: %w", err)
	}

	query := `SELECT ` + r.columns() + ` FROM ` + r.table + `
		WHERE id <> $1
		  AND project_id = (SELECT project_id FROM ` + r.table + ` WHERE id = $1)
		  AND app_name = (SELECT app_name FROM ` + r.table + ` WHERE id = $1)
		  AND (
		    (platform = (SELECT platform FROM ` + r.table + ` WHERE id = $1)
		      AND package_id = (SELECT package_id FROM ` + r.table + ` WHERE id = $1))
		    OR ($2 AND platform <> (SELECT platform FROM ` + r.table + ` WHERE id = $1))
		  )
		  AND owner_user_id = (SELECT owner_user_id FROM ` + r.table + ` WHERE id = $1)
		ORDER BY created_at DESC, id DESC`
	rows, err := r.db.QueryContext(ctx, query, id, includePlatforms)
	if err != nil {
		return nil, "", fmt.Errorf("查询同应用版本失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.Build, 0)
	for rows.Next() {
		build, err := rowToBuild(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, build)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("遍历同应用版本失败: %w", err)
	}
	return items, projectKey, nil
}

// MarkR2Synced 记录「对象已就位」这一事实：对象名、占用字节、同步状态一次写全。
//
// 为什么三个字段一起写而不拆成 SetR2Key + SetState：
// 它们是同一个事件（上传成功）的三个侧面，分开调用就有机会只写一半——
// 出现「key 有了但状态还是 pending」的记录，巡检会反复重传同一个对象。
//
// bytes 允许为 0（对象确实可能是空文件），空 key 则直接返回：
// 未镜像的包不该被误标为已同步。
func (r *BuildRepository) MarkR2Synced(ctx context.Context, id, key string, bytes int64) error {
	if key == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE `+r.table+`
SET r2_key = $2, r2_bytes = $3, r2_state = $4, r2_synced_at = now()
WHERE id = $1`,
		id, key, bytes, contract.R2StateSynced)
	if err != nil {
		return fmt.Errorf("记录 R2 同步结果失败: %w", err)
	}
	return nil
}

// RecordDownload 记录一次下载：计数自增，同时刷新访问时间。
//
// 两件事合在一条语句里，是因为它们描述的是同一个事实——"这个包刚被下载过"。
// 拆成两次调用会让它们有机会只改一处：淘汰算法依赖 last_accessed_at 判断
// "谁没人用"，一旦计数还在走而时间没更新，排序就悄悄退化成纯按上传时间，
// 而且不会报错，只会在配额紧张时删错包。
//
// 包里没这一行时返回 contract.NotFound：调用方据此判断"不在这个分区"，
// 不至于把跨分区探测的正常落空当成故障。
func (r *BuildRepository) RecordDownload(ctx context.Context, id string) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		`UPDATE `+r.table+`
SET download_count = download_count + 1, last_accessed_at = now()
WHERE id = $1
RETURNING download_count`,
		id,
	).Scan(&count)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return 0, contract.NotFound(contract.ResourceBuild)
		}
		return 0, fmt.Errorf("记录下载失败: %w", err)
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
		&b.DownloadCount, &createdAt, &b.OwnerUserID, &b.R2Key, &b.PinKind, &b.R2State, &b.LocalState,
		&b.LocalPulledBytes, &b.LocalError, &b.ProjectIconURL, &b.TenantID,
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

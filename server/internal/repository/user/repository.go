// Package user · 账号的持久化。
// 职责：读写 users 表，提供鉴权所需的密码哈希与令牌版本。
// 输入：连接池；输出：contract.User（对外）与内部 record（含密码，仅供 service 校验）。
// 边界：**不在任何对外结构里暴露 password_hash**；不做密码策略校验（在 service/user）；
//
//	不判断超管权限（在 middleware/login.RequireSuperAdmin）。
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// Record 是仓储内部行：对外 DTO 之外还带密码哈希、令牌版本与身份层级原始值。
// 层级与租户只放在 Record 里、不进 contract.User：它们是鉴权输入，不是账号管理接口的输出，
// 放进 DTO 就得同步改公开契约，徒增 P0 的范围。
type Record struct {
	User         contract.User
	PasswordHash string
	TokenVersion int
	AccountLevel string
	TenantID     string
}

// Repository 是 users 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// columns 带 LEFT JOIN 取人员姓名与角色名：列表页要显示"绑定的人是谁、什么角色"，一次查完不做 N+1。
const columns = `u.id::text, u.username, u.mode, u.password_hash, coalesce(u.person_id::text, ''),
u.is_super_admin, u.status, u.token_version, u.last_login_at, u.last_login_ip,
u.created_at, coalesce(p.name, ''), coalesce(r.key, ''), coalesce(r.name, ''),
coalesce(u.tenant_id::text, ''), u.account_level`

// fromClause 两次 LEFT JOIN 都是"可空"的：账号可以先建、之后再绑定人员；
// 人员也可以没有角色。两种情况都要让账号本身仍能被查出来。
// 角色按 roles.id 关联（people.role_id），key 可公司内重名，因此不能再按 key JOIN。
const fromClause = ` FROM users u
LEFT JOIN people p ON p.id = u.person_id
LEFT JOIN roles r ON r.id = p.role_id`

// FindByUsername 按登录名查账号；无记录转 contract.ErrNotFound（调用方自行决定是否暴露）。
func (r *Repository) FindByUsername(ctx context.Context, username string) (Record, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+fromClause+` WHERE u.username = $1`, username)
	return scanRecord(row)
}

// FindByID 按 id 查账号。
func (r *Repository) FindByID(ctx context.Context, id string) (Record, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+columns+fromClause+` WHERE u.id = $1`, id)
	return scanRecord(row)
}

// List 返回指定模式的账号页与总数；keyword 同时匹配登录名与绑定人员姓名。
// mode 是硬筛选：账号管理的视图跟着登录模式走，单人模式看不到团队账号，反之亦然。
// scope 叠加租户边界：公司账号只看本公司的账号，超管（All）看全部。
func (r *Repository) List(ctx context.Context, page contract.PageQuery, mode contract.SessionMode, keyword string, scope contract.Scope) ([]contract.User, int, error) {
	filter := accountFilter(mode, keyword)
	repository.ApplyTenant(filter, "u", scope)
	if !scope.All {
		// 非超管看不到超管账号：在列表里露出来等于把「平台最高权限账号」指给公司管理员看。
		// 只按 is_super_admin 判——超管是账号身份，与角色无关（角色体系里没有超管这个概念）。
		// 过滤放在 SQL 而不是内存里，分页的 total 才不会算错。
		filter.AddRaw(`u.is_super_admin = false`)
	}

	var total int
	countSQL := `SELECT count(*)` + fromClause + filter.Where()
	if err := r.db.QueryRowContext(ctx, countSQL, filter.Args()...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计账号数量失败: %w", err)
	}

	// 分页参数追加在筛选参数之后，占位符顺序与 args 一致。
	args := append(filter.Args(), page.Limit(), page.Offset())
	query := `SELECT ` + columns + fromClause + filter.Where() +
		fmt.Sprintf(` ORDER BY u.is_super_admin DESC, u.created_at ASC LIMIT %s OFFSET %s`,
			filter.Next(0), filter.Next(1))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询账号列表失败: %w", err)
	}
	defer rows.Close()

	items := make([]contract.User, 0)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, record.User)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历账号列表失败: %w", err)
	}
	return items, total, nil
}

// Create 新建账号；用户名冲突由唯一索引拒绝，service 翻译成可读提示。
// accountLevel / tenantID 由 service 按调用者层级推导后传入，仓储不自行判断。
// roleKey 非空时在同一事务里写绑定人员的角色（people.role_id），
// 「建号 + 绑定 + 定角色」要么全成、要么全不成，不会留下只有半个配置的账号。
// teamIDs 仅在 team_admin 时写入 team_admins——团队管理员与团队是多对多，关联与账号同事务。
func (r *Repository) Create(ctx context.Context, username, passwordHash string, mode contract.SessionMode, personID, roleKey string, isSuperAdmin bool, accountLevel, tenantID string, teamIDs []string) (Record, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("开启账号创建事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	level := accountLevel
	if level == "" {
		level = contract.AccountLevelMember
	}
	row := tx.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, mode, person_id, is_super_admin, account_level, tenant_id)
		 VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, NULLIF($7, '')::uuid)
		 RETURNING id::text`,
		username, passwordHash, mode, personID, isSuperAdmin, level, tenantID)

	var id string
	if err := row.Scan(&id); err != nil {
		return Record{}, fmt.Errorf("创建账号失败: %w", err)
	}
	if personID != "" && roleKey != "" {
		if _, err := tx.ExecContext(ctx, roleAssignmentSQL, personID, roleKey); err != nil {
			return Record{}, fmt.Errorf("写入人员角色失败: %w", err)
		}
	}
	if err := replaceAdminTeams(ctx, tx, id, teamIDs, tenantID); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("提交账号创建失败: %w", err)
	}
	return r.FindByID(ctx, id)
}

// roleAssignmentSQL 把角色标识解析成 roles.id 再写入人员档案。
// 角色按「平台内置（tenant_id 为空）+ 本公司自定义」可见，解析不到就是 NULL（无角色）——
// 与 FindByID 的 JOIN 同一条可见性规则，避免出现"账号列表显示 A、权限判定用 B"。
const roleAssignmentSQL = `UPDATE people SET role_id = (
    SELECT ro.id FROM roles ro
    WHERE ro.key = $2 AND (ro.tenant_id IS NULL OR ro.tenant_id = people.tenant_id)
  ) WHERE id = $1`

// PersonRoleKey 返回某个人当前的角色标识，没有绑定角色时返回空串。
// 账号侧只读它一个字段，用来校验「把这个账号绑到这个人身上是否会越权」。
func (r *Repository) PersonRoleKey(ctx context.Context, personID string) (string, error) {
	var key string
	err := r.db.QueryRowContext(ctx,
		`SELECT coalesce(r.key, '') FROM people p LEFT JOIN roles r ON r.id = p.role_id WHERE p.id = $1`,
		personID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", contract.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("查询人员角色失败: %w", err)
	}
	return key, nil
}

// replaceAdminTeams 写入团队管理员的关联团队；tenantID 非空时团队必须同属该公司。
// 非 team_admin 传空 teamIDs，等同一句空操作。
func replaceAdminTeams(ctx context.Context, tx *sql.Tx, userID string, teamIDs []string, tenantID string) error {
	for _, teamID := range teamIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO team_admins (team_id, user_id)
			 SELECT id, $2::uuid FROM teams WHERE id = $1::uuid AND tenant_id = $3::uuid
			 ON CONFLICT DO NOTHING`,
			teamID, userID, tenantID); err != nil {
			return fmt.Errorf("写入团队管理员关联失败: %w", err)
		}
	}
	return nil
}

// AdminTeamIDs 返回团队管理员账号关联的团队 id（有序、空结果返回空切片）。
// 与写入端 replaceAdminTeams 成对放在本仓储：team_admins 的读写都归账号侧，
// 团队仓储的 AdminTeamIDs 是给 scope 推导用的另一条只读路径，二者查的是同一张表。
func (r *Repository) AdminTeamIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT team_id::text FROM team_admins WHERE user_id = $1 ORDER BY team_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("查询管理员关联团队失败: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取管理员关联团队失败: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历管理员关联团队失败: %w", err)
	}
	return ids, nil
}

// Update 改绑定人员与超管标记（登录名与模式都不可改）。
//
// roleKey 不是 users 的列，而是绑定人员的角色：账号没有自己的角色，
// 它以某个人的身份登录，权限就来自那个人。两件事放在同一个事务里，
// 避免"人员换成了 A、角色却还是旧角色的"这种半截状态。
// personID 为空时没有可写角色的人，roleKey 一并忽略——角色必须挂在人身上。
func (r *Repository) Update(ctx context.Context, id, personID string, isSuperAdmin bool, roleKey string) (Record, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("开启账号更新事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET person_id = NULLIF($2, '')::uuid, is_super_admin = $3 WHERE id = $1`,
		id, personID, isSuperAdmin); err != nil {
		return Record{}, fmt.Errorf("更新账号失败: %w", err)
	}

	if personID != "" {
		// 查不到行不算错：personID 的合法性已由 users.person_id 的外键保证，
		// 这里 0 行只意味着该人员刚被别处删掉，账号侧的更新仍应成功。
		if _, err := tx.ExecContext(ctx, roleAssignmentSQL, personID, roleKey); err != nil {
			return Record{}, fmt.Errorf("更新账号角色失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("提交账号更新失败: %w", err)
	}
	return r.FindByID(ctx, id)
}

// SetStatus 启用/禁用账号；禁用时同时让令牌版本自增，做到"立即下线"。
func (r *Repository) SetStatus(ctx context.Context, id, status string) error {
	const query = `UPDATE users
SET status = $2,
    token_version = CASE WHEN $2 = 'disabled' THEN token_version + 1 ELSE token_version END
WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id, status)
	if err != nil {
		return fmt.Errorf("更新账号状态失败: %w", err)
	}
	return requireAffected(result)
}

// SetPassword 覆盖密码哈希，并让旧令牌失效（改密码后其它设备应重新登录）。
func (r *Repository) SetPassword(ctx context.Context, id, passwordHash string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = $2, token_version = token_version + 1 WHERE id = $1`,
		id, passwordHash)
	if err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
	}
	return requireAffected(result)
}

// Delete 按 id 与模式双条件物理删除账号：模式不符会命中 0 行并转成 ErrNotFound，
// 因此「跨模式删账号」在 SQL 层就被挡住，不依赖调用方是否记得先校验。
// users 没有被任何外键引用（projects/builds 的 owner_user_id 是 TEXT，不是 FK），
// 所以删除本身不会级联破坏历史数据；账号在单人分区留下的数据由 service 清理。
func (r *Repository) Delete(ctx context.Context, id string, mode contract.SessionMode) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1 AND mode = $2`, id, mode)
	if err != nil {
		return fmt.Errorf("删除账号失败: %w", err)
	}
	return requireAffected(result)
}

// BumpTokenVersion 强制下线：旧令牌里的版本号会与库里不一致而失效。
func (r *Repository) BumpTokenVersion(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE users SET token_version = token_version + 1 WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("强制下线失败: %w", err)
	}
	return requireAffected(result)
}

// TouchLogin 记录最近登录时间与来源 IP；失败不影响登录本身（旁路信息）。
func (r *Repository) TouchLogin(ctx context.Context, id, ip string) {
	_, _ = r.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = now(), last_login_ip = $2 WHERE id = $1`, id, ip)
}

// Count 返回账号总数，用于「首次启动才写种子管理员」的判断。
func (r *Repository) Count(ctx context.Context) (int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计账号数量失败: %w", err)
	}
	return total, nil
}

// CountSuperAdmins 返回启用中的超管数量，"不能没有超管"的护栏要用。
func (r *Repository) CountSuperAdmins(ctx context.Context) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE is_super_admin AND status = 'active'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("统计超级管理员失败: %w", err)
	}
	return count, nil
}

// DisableOrphanPlatformAdmins 禁用「platform_admin 但没有公司归属」的账号，返回处理行数。
//
// 这类账号是一个曾经的漏洞产物：账号接口允许超管建 platform_admin，而归属取 caller.TenantID
// （超管恒为空），于是建出能登录、带管理权限点、却因 scope 落到「无公司 → 1 = 0」
// 而任何数据都读不到的账号。写入路径已封（见 service/user 的 resolveCreateLevel）。
//
// 这里只**禁用**不删除：删除账号不可逆，且这类账号可能承载过真实操作历史；
// 禁用让它无法登录，同时保留记录可追溯。幂等，启动时执行一次。
//
// 不禁用超管：超管本来就没有公司，tenant_id 为空是正常的。
func (r *Repository) DisableOrphanPlatformAdmins(ctx context.Context) (int, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE users SET status = 'disabled'
		 WHERE account_level = 'platform_admin' AND tenant_id IS NULL AND status = 'active'`)
	if err != nil {
		return 0, fmt.Errorf("禁用孤儿平台管理员失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取处理行数失败: %w", err)
	}
	return int(affected), nil
}

// AlignSuperAdminLevel 把「is_super_admin 为真但 account_level 不是 super_admin」的存量账号
// 的层级对齐，返回修正行数。
//
// 为什么需要：is_super_admin 是旧模型的唯一身份位，P0 引入 account_level 后两者并存。
// 运行时靠 contract.EffectiveAccountLevel 折算，功能上不出错；但数据层不一致——
// 任何直接读 account_level 的地方（SQL 统计、导出的报表、以后的管理界面）都会把超管当成 member。
// 幂等，启动时执行一次。
func (r *Repository) AlignSuperAdminLevel(ctx context.Context) (int, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE users SET account_level = 'super_admin'
		 WHERE is_super_admin = true AND account_level <> 'super_admin'`)
	if err != nil {
		return 0, fmt.Errorf("对齐超管层级失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取对齐行数失败: %w", err)
	}
	return int(affected), nil
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取变更行数失败: %w", err)
	}
	if affected == 0 {
		return contract.ErrNotFound
	}
	return nil
}

// accountFilter 生成账号列表的固定条件：模式是必须的，关键字是可选的。
// 表必须起别名 u / p：fromClause 已把两张表 JOIN 在一起，列引用一律带前缀。
func accountFilter(mode contract.SessionMode, keyword string) *repository.Filter {
	filter := repository.NewFilter(0).Add(`u.mode = ?`, mode)
	trimmed := strings.TrimSpace(keyword)
	if trimmed == "" {
		return filter
	}
	// 同一个模式串要同时匹配两个列，用 AddMulti 一次加完：分两次 Add 会被 AND 连起来而查不到任何行。
	pattern := "%" + trimmed + "%"
	return filter.AddMulti(`(u.username ILIKE ? OR p.name ILIKE ?)`, pattern, pattern)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(row scanner) (Record, error) {
	var record Record
	var lastLogin sql.NullTime
	var createdAt time.Time

	err := row.Scan(
		&record.User.ID, &record.User.Username, &record.User.Mode, &record.PasswordHash, &record.User.PersonID,
		&record.User.IsSuperAdmin, &record.User.Status, &record.TokenVersion,
		&lastLogin, &record.User.LastLoginIP, &createdAt, &record.User.PersonName,
		&record.User.RoleKey, &record.User.RoleName, &record.TenantID, &record.AccountLevel,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return Record{}, contract.ErrNotFound
		}
		return Record{}, fmt.Errorf("读取账号失败: %w", err)
	}

	record.User.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	if lastLogin.Valid {
		record.User.LastLoginAt = lastLogin.Time.UTC().Format(time.RFC3339)
	}
	// 层级与租户同时投影到 DTO 和 Record：账号列表要显示身份，鉴权要读原始值。
	record.User.AccountLevel = record.AccountLevel
	record.User.TenantID = record.TenantID
	record.User.TeamIDs = []string{}
	return record, nil
}

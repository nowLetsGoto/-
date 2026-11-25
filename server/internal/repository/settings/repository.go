// Package settings · 平台集成配置的持久化（Webhook 与外部 API Key）。
// 职责：按公司读写 system_settings 的一行；只做 SQL 与行映射。
// 输入：连接池、公司 id（空串表示「平台兜底行」）。
// 输出：Config / Webhook 值。
// 边界：不做业务校验（在 service/settings）、不生成 Key、不投递 Webhook。
//
// 隔离模型：一家公司一行（tenant_id 唯一约束保证），tenant_id 为空的那一行是全平台共用的
// 兜底配置。存在兜底行的意义是存量兼容——改造前只有一行全局配置，那把 Key 直接迁成兜底行，
// 现有 CI 不用改任何东西就能继续用。
package settings

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Webhook 是 Webhook 部分的配置。
type Webhook struct {
	Provider string
	Enabled  bool
	URL      string
	Secret   string
	Fields   []string
}

// Config 是整行配置；APIKey 只读，不随 SaveWebhook 更新。
type Config struct {
	Webhook
	APIKey string
	// TenantID 是这行配置的归属公司，空串表示平台兜底行。
	TenantID string
	// FromFallback 标记本次读取是否回落到平台兜底行，供 webhook 日志区分。
	FromFallback bool
}

// Repository 是 system_settings 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

const selectColumns = `webhook_provider, webhook_enabled, webhook_url, webhook_secret, webhook_fields, api_key, coalesce(tenant_id::text, '')`

// tenantPredicate 生成「定位某一行」的 WHERE 片段：
// tenantID 非空按公司定位，空则定位平台兜底行。
// 用 IS NULL 而不是 = ”：tenant_id 是 UUID 列，只有 NULL 能表达「平台级」。
func tenantPredicate(tenantID string) (string, []any) {
	if tenantID == "" {
		return `tenant_id IS NULL`, nil
	}
	return `tenant_id = $1::uuid`, []any{tenantID}
}

// Load 读取指定公司的配置行（tenantID 为空读平台兜底行）。
// 该行不存在时返回 ErrNotFound，由 service 决定是「懒创建」还是报错——
// 仓储不擅自造数据，否则「读」会变成隐式写。
func (r *Repository) Load(ctx context.Context, tenantID string) (Config, error) {
	where, args := tenantPredicate(tenantID)
	var cfg Config
	var raw string
	row := r.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM system_settings WHERE `+where, args...)
	if err := row.Scan(&cfg.Provider, &cfg.Enabled, &cfg.URL, &cfg.Secret, &raw, &cfg.APIKey, &cfg.TenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return Config{}, ErrNotFound
		}
		return Config{}, fmt.Errorf("读取系统设置失败: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &cfg.Fields); err != nil {
		return Config{}, fmt.Errorf("解析 Webhook 字段配置失败: %w", err)
	}
	return cfg, nil
}

// LoadForDelivery 读取「投递时应使用」的配置：优先本公司那行，
// 没有配置本公司时回落到平台兜底行。
//
// 为什么投递要回落：公司刚建好、管理员还没配 Webhook 时，包仍要能正常发布，
// 不能被「没有配置」卡住；兜底行的存在让行为退回到改造前（全平台一份），
// 而不是静默不通知。
func (r *Repository) LoadForDelivery(ctx context.Context, tenantID string) (Config, error) {
	cfg, err := r.Load(ctx, tenantID)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Config{}, err
	}
	if tenantID == "" {
		return Config{}, err
	}
	fallback, fallbackErr := r.Load(ctx, "")
	if fallbackErr != nil {
		return Config{}, fallbackErr
	}
	fallback.FromFallback = true
	return fallback, nil
}

// EnsureRow 保证该公司有配置行（不存在则以默认值创建），返回该行配置。
// 设置页在「刚建好的公司还没存过配置」时会用到；幂等，靠唯一索引兜底并发。
func (r *Repository) EnsureRow(ctx context.Context, tenantID string) (Config, error) {
	cfg, err := r.Load(ctx, tenantID)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Config{}, err
	}

	// 用 INSERT ... SELECT WHERE NOT EXISTS 而不是 ON CONFLICT：
	// 冲突目标是部分唯一索引，ON CONFLICT 需要写索引谓词，可读性差且易写错。
	//
	// 初始 api_key 留空串 = 「尚未生成」：库里只存摘要，自动生成一把却没有明文交付路径，
	// 那把它填给用户必然 401。让管理员主动点「轮换」拿一次性明文，是唯一自洽的做法。
	// 空 Key 在反查时会被 TenantByAPIKey 直接判为无效（见那里的空串判断）。
	//
	// 并发下仍可能有两个请求同时通过 NOT EXISTS 判断，只有一个能插成功；
	// 后到的会撞唯一索引。这不是错误——对方刚替我们建好了行，重读即可。
	_, insertErr := r.db.ExecContext(ctx,
		`INSERT INTO system_settings (tenant_id, api_key)
		 SELECT NULLIF($1, '')::uuid, ''
		 WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE tenant_id IS NOT DISTINCT FROM NULLIF($1, '')::uuid)`, tenantID)
	if insertErr != nil {
		// 落库失败后仍尝试重读：若对方已建好，本次请求照样能拿到配置。
		if cfg, err := r.Load(ctx, tenantID); err == nil {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("初始化公司配置失败: %w", insertErr)
	}
	return r.Load(ctx, tenantID)
}

// SaveWebhook 原子更新 Webhook 类型与配置，避免类型、地址和签名密钥只生效一半。
// 行不存在时先 EnsureRow，保证首次保存也能落库。
func (r *Repository) SaveWebhook(ctx context.Context, tenantID string, hook Webhook) error {
	raw, err := json.Marshal(hook.Fields)
	if err != nil {
		return fmt.Errorf("序列化 Webhook 字段失败: %w", err)
	}
	if _, err := r.EnsureRow(ctx, tenantID); err != nil {
		return err
	}
	where, args := tenantPredicate(tenantID)
	// $1..$5 是五个待写值，WHERE 里的定位占位符整体后移 5 位。
	query := `UPDATE system_settings
SET webhook_enabled = $1, webhook_url = $2, webhook_secret = $3,
    webhook_fields = $4, webhook_provider = $5, updated_at = now()
WHERE ` + rebasePlaceholders(where, 5)
	if _, err := r.db.ExecContext(ctx, query,
		append([]any{hook.Enabled, hook.URL, hook.Secret, string(raw), hook.Provider}, args...)...); err != nil {
		return fmt.Errorf("保存 Webhook 配置失败: %w", err)
	}
	return nil
}

// RotateAPIKey 生成并保存新的 API Key，返回明文供管理员一次性查看。
//
// 库里只落 SHA-256 摘要，明文在这条 return 之后再无任何途径可取回——
// 这样即使数据库被导出，也拿不到能直接用的 Key。
func (r *Repository) RotateAPIKey(ctx context.Context, tenantID string) (string, error) {
	if _, err := r.EnsureRow(ctx, tenantID); err != nil {
		return "", err
	}
	where, args := tenantPredicate(tenantID)
	// 明文由数据库生成一份用于返回，落库的是它的摘要。两处用同一个表达式，避免各算一次。
	query := `WITH generated AS (
  SELECT replace(gen_random_uuid()::text, '-', '') AS plain
)
UPDATE system_settings
SET api_key = (SELECT encode(sha256(plain::bytea), 'hex') FROM generated), updated_at = now()
WHERE ` + where + `
RETURNING (SELECT plain FROM generated)`

	var key string
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&key); err != nil {
		return "", fmt.Errorf("轮换 API Key 失败: %w", err)
	}
	return key, nil
}

// hashAPIKey 把调用方提供的明文折算成库里存的摘要。
// 用 SHA-256 而不是 bcrypt/argon2：Key 是 128 位随机串，本身就没有"弱口令"问题，
// 不需要抗暴力破解的慢哈希；而外部上传是热路径，慢哈希会直接把上传延迟抬上去。
func hashAPIKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// TenantByAPIKey 用调用方提供的 Key 反查它属于哪家公司（空串表示平台兜底 Key）。
// 找不到返回 ErrNotFound，中间件据此回 401——Key 无效与"没有这家公司的配置"同口径。
//
// 这是「外部上传的包该落到哪家公司」的唯一依据：不信任请求体里的任何公司标识。
//
// 比对方式：先对收到的明文算摘要，再用摘要做等值查询。
// 这样库里没有明文，而已发出的旧 Key 依旧可用（见 schema.sql 的迁移说明）。
// 等值查询走 idx_settings_api_key 唯一索引，不会全表扫。
func (r *Repository) TenantByAPIKey(ctx context.Context, apiKey string) (string, error) {
	if apiKey == "" {
		return "", ErrNotFound
	}
	var tenantID string
	err := r.db.QueryRowContext(ctx,
		`SELECT coalesce(tenant_id::text, '') FROM system_settings WHERE api_key = $1`,
		hashAPIKey(apiKey)).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("反查 API Key 归属失败: %w", err)
	}
	return tenantID, nil
}

// ErrNotFound 表示目标公司（或平台兜底行）还没有配置。
var ErrNotFound = errors.New("配置不存在")

// rebasePlaceholders 把 WHERE 片段里的占位符整体后移 offset 位。
// 用于「SET 先占 $1..$n，WHERE 复用同一套定位条件」的拼接场景。
func rebasePlaceholders(where string, offset int) string {
	if offset == 0 {
		return where
	}
	result := make([]byte, 0, len(where)+8)
	for i := 0; i < len(where); i++ {
		if where[i] == '$' && i+1 < len(where) && where[i+1] >= '1' && where[i+1] <= '9' {
			result = append(result, fmt.Sprintf("$%d", int(where[i+1]-'0')+offset)...)
			i++
			continue
		}
		result = append(result, where[i])
	}
	return string(result)
}

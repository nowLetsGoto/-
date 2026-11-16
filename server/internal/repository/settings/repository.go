// Package settings · 系统配置的持久化。
// 职责：读写 system_settings 单行配置（Webhook 与外部 API Key），只做 SQL 与行映射。
// 输入：连接池；输出：Config / Webhook 值。
// 边界：不做业务校验（在 service/settings）、不生成 Key、不投递 Webhook。
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Webhook 是 Webhook 部分的配置。
type Webhook struct {
	Enabled bool
	URL     string
	Secret  string
	Fields  []string
}

// Config 是整行配置；APIKey 只读，不随 SaveWebhook 更新。
type Config struct {
	Webhook
	APIKey string
}

// Repository 是 system_settings 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

const selectSQL = `
SELECT webhook_enabled, webhook_url, webhook_secret, webhook_fields, api_key
FROM system_settings
WHERE id = 1`

// Load 读取整行配置；表由 schema.sql 保证有 id=1 的行。
func (r *Repository) Load(ctx context.Context) (Config, error) {
	var cfg Config
	var raw string
	row := r.db.QueryRowContext(ctx, selectSQL)
	if err := row.Scan(&cfg.Enabled, &cfg.URL, &cfg.Secret, &raw, &cfg.APIKey); err != nil {
		return Config{}, fmt.Errorf("读取系统设置失败: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &cfg.Fields); err != nil {
		return Config{}, fmt.Errorf("解析 Webhook 字段配置失败: %w", err)
	}
	return cfg, nil
}

const updateSQL = `
UPDATE system_settings
SET webhook_enabled = $1, webhook_url = $2, webhook_secret = $3,
    webhook_fields = $4, updated_at = now()
WHERE id = 1`

// SaveWebhook 只更新 Webhook 四列；多列一起写，避免出现"开关已开但地址没存"的中间态。
// RotateAPIKey 生成并保存新的 API Key，返回明文供超管一次性查看。
// 生成交给数据库（gen_random_uuid 去横线，32 位十六进制），避免应用层再引一个随机源。
func (r *Repository) RotateAPIKey(ctx context.Context) (string, error) {
	const query = `UPDATE system_settings
SET api_key = replace(gen_random_uuid()::text, '-', ''), updated_at = now()
WHERE id = 1
RETURNING api_key`

	var key string
	if err := r.db.QueryRowContext(ctx, query).Scan(&key); err != nil {
		return "", fmt.Errorf("轮换 API Key 失败: %w", err)
	}
	return key, nil
}

func (r *Repository) SaveWebhook(ctx context.Context, hook Webhook) error {
	raw, err := json.Marshal(hook.Fields)
	if err != nil {
		return fmt.Errorf("序列化 Webhook 字段失败: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, updateSQL, hook.Enabled, hook.URL, hook.Secret, string(raw)); err != nil {
		return fmt.Errorf("保存 Webhook 配置失败: %w", err)
	}
	return nil
}

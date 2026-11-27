// Package settings · R2 对象存储配置的持久化。
// 职责：读写 system_settings 上属于 R2 的五列；只做 SQL 与行映射。
// 输入：连接池、公司 id（空串表示平台兜底行）。
// 输出：R2 配置值（secret 为密文，本层不加解密）。
// 边界：不做加解密、不构造客户端、不校验参数——三者分别属于 secret / storage / service。
//
// 为什么不并入 repository.go：那个文件已承载 Webhook 与 API Key 两条流程（244 行），
// 再混入一组与它们无关的列会继续膨胀。R2 配置的读写是独立的变化原因
// （存储通道调整才需要改这里），单独成文件符合单一职责。
package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// R2 是 R2 通道的配置。
//
// SecretKey 存放的是密文原样（带 v1: 前缀），解密由 service 完成——
// 仓储不认识加密格式，否则将来换算法要同时改两层。
type R2 struct {
	AccountID     string
	AccessKeyID   string
	SecretKey     string
	Bucket        string
	PublicBaseURL string
}

// LoadR2 读取指定公司的 R2 配置。
//
// 与 Load 的区别：这里不返回 ErrNotFound。R2 配置是 system_settings 的列，
// 行不存在时各列取列默认值（空串），语义上等于「未配置 R2」——
// 而「未配置」是正常状态，不是错误。把它当错误会让调用方每次都要
// 区分「没这行」和「这行没填 R2」，而两者后续处理完全相同。
func (r *Repository) LoadR2(ctx context.Context, tenantID string) (R2, error) {
	where, args := tenantPredicate(tenantID)
	var cfg R2
	err := r.db.QueryRowContext(ctx,
		`SELECT r2_account_id, r2_access_key_id, r2_secret_key, r2_bucket, r2_public_base_url
FROM system_settings WHERE `+where, args...).
		Scan(&cfg.AccountID, &cfg.AccessKeyID, &cfg.SecretKey, &cfg.Bucket, &cfg.PublicBaseURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return R2{}, nil
		}
		return R2{}, fmt.Errorf("读取 R2 配置失败: %w", err)
	}
	return cfg, nil
}

// SaveR2 覆盖式保存某公司的 R2 配置。
//
// 整体覆盖而不是逐字段更新：五项共同描述「一套凭据」，
// 只改一半会得到一套自相矛盾的配置（如换了桶名却留着旧账号），
// 这种状态既不能用于上传，排查时也很难看出哪项是旧的。
//
// secret 传空串表示「清除」，不是「保持不变」：
// 需要保持不变时由 service 先读出旧密文再原样传回——
// 让仓储做「空串跳过」的隐式语义，会让「清空密钥」这个合法操作无法表达。
func (r *Repository) SaveR2(ctx context.Context, tenantID string, cfg R2) error {
	if _, err := r.EnsureRow(ctx, tenantID); err != nil {
		return err
	}
	where, args := tenantPredicate(tenantID)
	// $1..$5 是五个待写值，WHERE 里的定位占位符整体后移 5 位。
	query := `UPDATE system_settings
SET r2_account_id = $1, r2_access_key_id = $2, r2_secret_key = $3,
    r2_bucket = $4, r2_public_base_url = $5, updated_at = now()
WHERE ` + rebasePlaceholders(where, 5)
	if _, err := r.db.ExecContext(ctx, query,
		append([]any{cfg.AccountID, cfg.AccessKeyID, cfg.SecretKey, cfg.Bucket, cfg.PublicBaseURL}, args...)...); err != nil {
		return fmt.Errorf("保存 R2 配置失败: %w", err)
	}
	return nil
}

// ListConfiguredTenants 返回所有已填写 R2 配置的公司 id。
//
// 用途是启动时只给这些公司建客户端：未配置的公司按严格隔离视为未启用 R2，
// 不需要（也不应该）占用一个客户端实例。
//
// 判据用 r2_account_id 非空即可：剩下的必填项由 storage.R2Config.Enabled 判定，
// 两处判据各管一段，避免这里再重复一遍四项齐全的规则。
func (r *Repository) ListConfiguredTenants(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT tenant_id::text FROM system_settings
		 WHERE tenant_id IS NOT NULL AND r2_account_id <> ''`)
	if err != nil {
		return nil, fmt.Errorf("列出已配置 R2 的公司失败: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取已配置 R2 的公司失败: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

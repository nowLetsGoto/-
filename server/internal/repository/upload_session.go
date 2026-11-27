// upload_session.go · 浏览器直传 R2 的上传会话仓储。
//
// 职责：记录"谁要上传多大的什么文件到哪个 R2 key"，以及该会话的推进状态。
// 输入：会话令牌、R2 对象名、声明大小与类型、过期时间。
// 输出：会话实体；查询、状态推进与过期清理。
// 边界：不生成签名 URL（属 storage.R2）、不校验文件内容（属 service）、不碰本地磁盘。
//
//	单表不分分区：直传会话与租户无关，跨分区共用一条流程。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// 会话状态。pending 是"URL 已签发但浏览器还没传完"。
const (
	UploadStatePending   = "pending"
	UploadStateUploaded  = "uploaded"
	UploadStateCompleted = "completed"
	UploadStateExpired   = "expired"
)

// UploadSession 是一条直传会话。
type UploadSession struct {
	Token        string
	R2Key        string
	DeclaredSize int64
	ContentType  string
	FileExt      string
	OriginalName string
	State        string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	// TenantID 是会话所属公司，空串表示未绑定（存量会话或平台级调用）。
	// R2 按公司隔离后，commit / abort / 过期清理都需要凭它选中对应公司的客户端——
	// 从 R2Key 解析前缀是不可靠的，命名习惯会变。
	TenantID string
}

// UploadSessionRepository 是 upload_sessions 的唯一访问入口。
type UploadSessionRepository struct {
	db *sql.DB
}

func NewUploadSessionRepository(db *sql.DB) *UploadSessionRepository {
	return &UploadSessionRepository{db: db}
}

// Create 落一条 pending 会话。token 已存在时返回错误（token 由 CSPRNG 生成，撞了就是 bug）。
func (r *UploadSessionRepository) Create(ctx context.Context, s UploadSession) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO upload_sessions
			(token, r2_key, declared_size, content_type, file_ext, original_name, state, expires_at, tenant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::uuid)`,
		s.Token, s.R2Key, s.DeclaredSize, s.ContentType, s.FileExt, s.OriginalName, s.State, s.ExpiresAt, s.TenantID)
	if err != nil {
		return fmt.Errorf("创建上传会话失败: %w", err)
	}
	return nil
}

// Find 按 token 取会话；不存在返回 nil, nil（让上层给出"请重新选择文件"而不是 500）。
func (r *UploadSessionRepository) Find(ctx context.Context, token string) (*UploadSession, error) {
	var s UploadSession
	err := r.db.QueryRowContext(ctx, `
		SELECT token, r2_key, declared_size, content_type, file_ext, original_name, state,
		       created_at, expires_at, coalesce(tenant_id::text, '')
		FROM upload_sessions WHERE token = $1`, token).
		Scan(&s.Token, &s.R2Key, &s.DeclaredSize, &s.ContentType, &s.FileExt,
			&s.OriginalName, &s.State, &s.CreatedAt, &s.ExpiresAt, &s.TenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("查询上传会话失败: %w", err)
	}
	return &s, nil
}

// MarkUploaded 把会话推进到 uploaded。带状态条件，避免并发重复推进。
func (r *UploadSessionRepository) MarkUploaded(ctx context.Context, token string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE upload_sessions SET state = $2 WHERE token = $1 AND state = $3`,
		token, UploadStateUploaded, UploadStatePending)
	if err != nil {
		return fmt.Errorf("更新上传会话状态失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 已经是 uploaded 也算成功（幂等），只有不存在或已过期才需要上层介入。
		return nil
	}
	return nil
}

// Delete 删除会话。回拉完成或放弃后调用，避免表无限增长。
func (r *UploadSessionRepository) Delete(ctx context.Context, token string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM upload_sessions WHERE token = $1`, token); err != nil {
		return fmt.Errorf("删除上传会话失败: %w", err)
	}
	return nil
}

// PurgeExpired 清理过期会话并返回其 R2 key，供上层删除 R2 上的孤立对象。
// 只清理 pending / uploaded —— completed 的会话已被正常消费。
func (r *UploadSessionRepository) PurgeExpired(ctx context.Context, now time.Time) ([]PurgedObject, error) {
	rows, err := r.db.QueryContext(ctx, `
		DELETE FROM upload_sessions
		WHERE expires_at < $1 AND state IN ($2, $3)
		RETURNING r2_key, coalesce(tenant_id::text, '')`,
		now, UploadStatePending, UploadStateUploaded)
	if err != nil {
		return nil, fmt.Errorf("清理过期上传会话失败: %w", err)
	}
	defer rows.Close()
	var objects []PurgedObject
	for rows.Next() {
		var obj PurgedObject
		if err := rows.Scan(&obj.Key, &obj.TenantID); err != nil {
			return objects, fmt.Errorf("读取待清理对象名失败: %w", err)
		}
		objects = append(objects, obj)
	}
	return objects, rows.Err()
}

// PurgedObject 是一条待清理的孤立对象及其所属公司。
//
// 必须带上 TenantID：R2 按公司隔离后，删除对象要先知道去哪个桶删。
// 只返回 key 会让清理逻辑无从下手（或被迫从 key 前缀猜测归属）。
type PurgedObject struct {
	Key      string
	TenantID string
}

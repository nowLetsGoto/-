// 职责：读取一个分区内绑定单个安装包的分享授权。
// 输入：分享 ID 或包 ID 集合；输出：授权记录及到期、撤销状态。
// 边界：只做 SQL 与行映射，不签名、不判断有效性、不生成地址；模式由装配固定。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"dandelion/server/internal/contract"
)

type BuildShare struct {
	ID        string
	BuildID   string
	Revision  int64
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

type BuildShareRepository struct {
	db    *sql.DB
	table string
}

func NewBuildSharePartitioned(db *sql.DB) Partitioned[*BuildShareRepository] {
	return NewPartitioned(
		&BuildShareRepository{db: db, table: PartitionTeam.Table("build_shares")},
		&BuildShareRepository{db: db, table: PartitionSolo.Table("build_shares")},
	)
}

func (r *BuildShareRepository) FindByID(ctx context.Context, id string) (BuildShare, error) {
	var share BuildShare
	err := r.db.QueryRowContext(ctx, `SELECT id::text, build_id::text, revision, expires_at, revoked_at FROM `+r.table+` WHERE id=$1`, id).
		Scan(&share.ID, &share.BuildID, &share.Revision, &share.ExpiresAt, &share.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return share, contract.NotFound(contract.ResourceShare)
	}
	if err != nil {
		return share, fmt.Errorf("读取分享授权失败: %w", err)
	}
	return share, nil
}

// ListByBuildIDs 批量读取已有授权，不创建或复活已撤销的分享。
func (r *BuildShareRepository) ListByBuildIDs(ctx context.Context, ids []string) (map[string]BuildShare, error) {
	shares := make(map[string]BuildShare, len(ids))
	if len(ids) == 0 {
		return shares, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id::text, build_id::text, revision, expires_at, revoked_at FROM `+r.table+` WHERE build_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("读取包的分享授权失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var share BuildShare
		if err := rows.Scan(&share.ID, &share.BuildID, &share.Revision, &share.ExpiresAt, &share.RevokedAt); err != nil {
			return nil, fmt.Errorf("映射分享授权失败: %w", err)
		}
		shares[share.BuildID] = share
	}
	return shares, rows.Err()
}

// Package tenant · 公司的持久化。
// 职责：读取 tenants 表，给身份投影提供公司名。
// 输入：连接池与公司 id；输出：公司名（查不到或 id 为空时为空串）。
// 边界：不做业务判断（租户归属由 authz.Service.ScopeOf 决定）、不写库；
//
//	本阶段只需要"按 id 取名"这一个读操作，公司的建/改/停用属于 P2。
package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Repository 是 tenants 的唯一 SQL 出入口。
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// NameByID 按公司 id 取名字。
// 空 id（超管没有公司）与已删除的公司都返回空串：/me 不必为"没有公司"分流两套逻辑，
// 前端拿到空串就显示"平台"或隐藏公司标签，与"查不到"是同一种展示。
func (r *Repository) NameByID(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	var name string
	err := r.db.QueryRowContext(ctx, `SELECT name FROM tenants WHERE id = $1`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("查询公司名称失败: %w", err)
	}
	return name, nil
}

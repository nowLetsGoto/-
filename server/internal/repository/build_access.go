// 职责：按调用者范围定位包的真实项目，并在读取包时再次限定项目及 owner。
// 输入：包 ID、账号 ID、可信 scope；输出：项目 key 或包详情。
// 边界：只做 SQL 过滤，不计算权限、不解析凭证；公开分享在服务层验签后使用独立读取方法。
package repository

import (
	"context"
	"database/sql"
	"errors"

	"dandelion/server/internal/contract"
)

func (r *BuildRepository) ProjectKeyForBuild(ctx context.Context, id, owner string, scope contract.Scope) (string, error) {
	f := NewFilter(0).Add(`b.id = ?`, id)
	if r.ownerScoped {
		f.Add(`b.owner_user_id = ?`, owner).Add(`p.owner_user_id = ?`, owner)
	} else {
		ApplyTenant(f, "p", scope)
		ApplyTeam(f, `p.id IN (SELECT tp.project_id FROM team_projects tp WHERE tp.team_id = ANY(?))`, scope)
	}
	var key string
	err := r.db.QueryRowContext(ctx, `SELECT p.key FROM `+r.table+` b JOIN `+r.project+` p ON p.id=b.project_id`+f.Where(), f.Args()...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", contract.NotFound(contract.ResourceBuild)
	}
	return key, err
}

func (r *BuildRepository) FindInProject(ctx context.Context, id, projectID, owner string) (contract.Build, error) {
	f := NewFilter(0).Add(`id = ?`, id).Add(`project_id = ?`, projectID).
		AddIf(r.ownerScoped, `owner_user_id = ?`, owner)
	build, err := rowToBuild(r.db.QueryRowContext(ctx, `SELECT `+r.columns()+` FROM `+r.table+f.Where(), f.Args()...))
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Build{}, contract.NotFound(contract.ResourceBuild)
	}
	return build, err
}

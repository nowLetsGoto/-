// 职责：更新应用包展示字段或删除包，使用项目与 owner 条件兜住写入边界。
// 输入：包 ID、已解析项目 ID、账号 ID 与展示字段；输出：更新后的包或已删文件路径。
// 边界：只执行 SQL；分享授权由外键级联删除，文件清理由服务层在提交后执行。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"dandelion/server/internal/contract"
)

func (r *BuildRepository) Update(ctx context.Context, id, projectID, owner string, in contract.BuildUpdateInput) (contract.Build, error) {
	f := NewFilter(5).Add(`id = ?`, id).Add(`project_id = ?`, projectID).
		AddIf(r.ownerScoped, `owner_user_id = ?`, owner)
	args := append([]any{in.AppName, in.Version, in.BuildNumber, in.Environment, in.Changelog}, f.Args()...)
	build, err := rowToBuild(r.db.QueryRowContext(ctx, `UPDATE `+r.table+`
		SET app_name=$1, version=$2, build_number=$3, environment=$4, changelog=$5`+
		f.Where()+` RETURNING `+r.columns(), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Build{}, contract.NotFound(contract.ResourceBuild)
	}
	if err != nil {
		return contract.Build{}, fmt.Errorf("编辑包元数据失败: %w", err)
	}
	return build, nil
}

func (r *BuildRepository) Delete(ctx context.Context, id, projectID, owner string) (string, error) {
	f := NewFilter(0).Add(`id = ?`, id).Add(`project_id = ?`, projectID).
		AddIf(r.ownerScoped, `owner_user_id = ?`, owner)
	var relativePath string
	err := r.db.QueryRowContext(ctx, `DELETE FROM `+r.table+f.Where()+` RETURNING relative_path`, f.Args()...).Scan(&relativePath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", contract.NotFound(contract.ResourceBuild)
	}
	if err != nil {
		return "", fmt.Errorf("删除应用包失败: %w", err)
	}
	return relativePath, nil
}

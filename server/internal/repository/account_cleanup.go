// account_cleanup.go · 删除账号时清理它遗留在单人分区的业务数据。
// 职责：在一个事务里按 owner 删除 solo.builds 与 solo.projects，并交回被删包的文件相对路径。
// 输入：账号 id；输出：待删除的包文件相对路径列表。
// 边界：只碰 solo 分区——team 分区是团队资产，账号消失后包与项目仍归属团队，不能跟着删；
//       不按账号的 mode 判断要不要清理：登录不校验账号模式，团队账号也可能持有单人数据，
//       判定依据只能是「这个 id 在 solo 分区有没有数据」，多清一次是空转，少清一次就是永久孤儿；
//       不删 users 行（在 repository/user）、不删磁盘文件（由 service 负责，事务只保证库内一致）；
//       不删审计日志——审计是"只追加"的事实记录，且已冗余 actor_name，账号消失后仍可读。
package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// AccountCleanup 是「账号退出后的数据善后」的唯一实现处。
// 它必须独占一个类型：跨 projects / builds 两张表的删除顺序是硬约束，散在业务层容易写错。
type AccountCleanup struct {
	db *sql.DB
}

func NewAccountCleanup(db *sql.DB) *AccountCleanup {
	return &AccountCleanup{db: db}
}

// PurgeSoloData 删除该账号在单人分区的全部包与项目，返回被删包的相对路径。
//
// 为什么必须先删包再删项目：builds.project_id 是外键且**没有** ON DELETE CASCADE，
// 反过来删项目会被外键约束挡下，整笔清理直接失败。
// 为什么要返回路径：库里的行删掉了，磁盘上的文件还在，得由调用方接着删；
// 但文件删除放在事务之外——文件系统没有事务，把它塞进事务只会造出"库回滚了文件却没了"的假象。
func (c *AccountCleanup) PurgeSoloData(ctx context.Context, ownerUserID string) ([]string, error) {
	if ownerUserID == "" {
		return nil, nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启账号清理事务失败: %w", err)
	}
	// 任何一步失败都整笔回滚：宁可留数据让人重试，也不要留下半套孤立记录。
	defer func() { _ = tx.Rollback() }()

	// 包可能由本人上传（owner_user_id）或挂在本人项目下，两个来源都要覆盖。
	const deleteBuilds = `DELETE FROM solo.builds b USING solo.projects p
WHERE b.project_id = p.id AND (b.owner_user_id = $1 OR p.owner_user_id = $1)
RETURNING b.relative_path`

	rows, err := tx.QueryContext(ctx, deleteBuilds, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("清理账号的单人包失败: %w", err)
	}
	paths := make([]string, 0)
	for rows.Next() {
		var relativePath string
		if err := rows.Scan(&relativePath); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取待清理包路径失败: %w", err)
		}
		paths = append(paths, relativePath)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历待清理包失败: %w", err)
	}
	rows.Close()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM solo.projects WHERE owner_user_id = $1`, ownerUserID); err != nil {
		return nil, fmt.Errorf("清理账号的单人项目失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交账号清理事务失败: %w", err)
	}
	return paths, nil
}

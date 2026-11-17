// schema.go · 建表 DDL 的内嵌与执行。
// 职责：把 schema.sql 打进二进制，并在进程启动时幂等执行一次。
// 输入：连接池、context；输出：无（失败返回带中文上下文的错误）。
// 边界：只执行 DDL，不做数据修补以外的任何业务动作——迁移存量表也在 DDL 里一次做完，
//       这样"代码版本"与"库结构版本"永远同源，不需要单独的迁移工具。
package repository

import (
	"context"
	_ "embed"
	"database/sql"
	"fmt"
	"strings"
)

// schemaDDL 内嵌建表 DDL：DDL 与仓储层同包，二进制自带，
// 启动时幂等执行、不依赖当前工作目录；容器里由 initdb 执行同一份文件作为双保险。
//
//go:embed schema.sql
var schemaDDL string

// EnsureSchema 幂等执行内嵌 DDL，进程启动时调用一次。
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("初始化数据表失败: %w%s", err, ownershipHint(err))
	}
	return nil
}

// ownershipHint 把「schema 属主不是当前账号」这类部署事故翻译成可执行的指导。
// 这不是代码缺陷：分区的访问权由属主决定，用别的账号手工跑过 DDL 就会造出连不上的分区。
func ownershipHint(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "permission denied for schema") || strings.Contains(msg, "must be owner of") {
		return "\n提示：team / solo 分区只能由应用自己的数据库账号创建。" +
			"请用 DATABASE_URL 里的账号重跑 server/internal/repository/schema.sql，" +
			"或把这些 schema 及其表的所有者改为该账号。"
	}
	return ""
}

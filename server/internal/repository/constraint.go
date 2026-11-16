// constraint.go · PostgreSQL 约束错误的统一识别。
// 职责：把 pgconn 的 SQLSTATE 翻译成布尔判断，供各 service 生成中文提示。
// 输入：数据库错误；输出：是否唯一键冲突 / 是否外键冲突。
// 边界：不构造业务错误、不打日志、不依赖任何具体表。
package repository

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)

// IsUniqueViolation 判断是否违反唯一约束（例如项目 key、人员登录名重复）。
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateUniqueViolation
}

// IsForeignKeyViolation 判断是否违反外键（例如关联了不存在的团队或项目）。
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateForeignKeyViolation
}

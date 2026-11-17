// filter.go · WHERE 条件的构造器（分表后所有查询共用的拼装工具）。
// 职责：按顺序累积 AND 条件与参数，自动为每个参数分配 $n 占位符。
// 输入：条件模板（用 ? 表示参数位置）；输出：可直接拼进 SQL 的 WHERE 片段与参数。
// 边界：只处理 AND 连接；OR / 子查询等复杂条件请直接写 SQL，
//       不要把这里做成"万能 SQL 构造器"——它的价值只是消掉手算 $1 $2 的错位风险。
package repository

import (
	"fmt"
	"strings"
)

// Filter 累积一组 AND 条件，让"写条件"与"分配占位符"成为同一件事。
// 为什么需要它：同一份查询要在 team / solo 两套表上跑，而 owner 这类条件时有时无，
// 手写 $1 $2 极易错位，且改动一个条件就要重排后面所有编号。
type Filter struct {
	// base 是构造前已被占用的参数个数。UPDATE 的 SET 子句先写了 $1..$n，
	// WHERE 就必须从 $n+1 开始，否则整条语句的参数会错位。
	base       int
	conditions []string
	args       []any
}

// NewFilter 创建条件构造器；base 为 WHERE 之前已用掉的参数个数，普通 SELECT 传 0。
func NewFilter(base int) *Filter { return &Filter{base: base} }

// Add 追加一个 AND 条件；模板中的 ? 会被替换成该参数对应的占位符。
func (f *Filter) Add(template string, arg any) *Filter {
	f.args = append(f.args, arg)
	f.conditions = append(f.conditions, strings.Replace(template, "?", f.Placeholder(len(f.args)-1), 1))
	return f
}

// AddIf 仅在 ok 为真时追加条件，省掉调用点的 if 包裹（可选筛选条件用）。
func (f *Filter) AddIf(ok bool, template string, arg any) *Filter {
	if ok {
		f.Add(template, arg)
	}
	return f
}

// Where 返回 " WHERE a = $1 AND b = $2"；无条件时返回空串，直接接在表名后面也安全。
func (f *Filter) Where() string {
	if len(f.conditions) == 0 {
		return ""
	}
	return " WHERE " + f.Cond()
}

// Cond 返回不带 WHERE 关键字的条件，供 "WHERE a = $1 AND b = $2" 之外
// 需要自己写关键字的语句使用（例如 UPDATE ... WHERE 之后还要接 RETURNING）。
func (f *Filter) Cond() string { return strings.Join(f.conditions, " AND ") }

// Args 返回与 Where 中占位符顺序一致的参数，可直接展开给 QueryContext。
func (f *Filter) Args() []any { return f.args }

// Placeholder 返回第 offset 个参数（从 0 起）的占位符文本。
func (f *Filter) Placeholder(offset int) string {
	return fmt.Sprintf("$%d", f.base+offset+1)
}

// Next 返回紧跟已有条件之后第 offset 个占位符，供 LIMIT / OFFSET 这类追加参数使用：
// Next(0) 是 LIMIT 的参数，Next(1) 是 OFFSET 的参数。
func (f *Filter) Next(offset int) string { return f.Placeholder(len(f.args) + offset) }

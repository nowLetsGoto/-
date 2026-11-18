// filter.go · WHERE 条件的构造器（分表后所有查询共用的拼装工具）。
// 职责：按顺序累积 AND 条件与参数，自动为每个参数分配 $n 占位符。
// 输入：条件模板（用 ? 表示参数位置）；输出：可直接拼进 SQL 的 WHERE 片段与参数。
// 边界：只处理 AND 连接；OR / 子查询等复杂条件请直接写 SQL，
//       不要把这里做成"万能 SQL 构造器"——它的价值只是消掉手算 $1 $2 的错位风险。
package repository

import (
	"fmt"
	"strings"

	"dandelion/server/internal/contract"
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
	return f.AddMulti(template, arg)
}

// AddMulti 追加一个含多个占位符的条件：模板里的 ? 依次绑定 args。
// 用于同一条件内比较多个列——如 (a ILIKE ? OR b ILIKE ?) 传同一个模式串，
// 若拆成两次 Add 会变成 AND，语义就错了。
func (f *Filter) AddMulti(template string, args ...any) *Filter {
	offset := len(f.args)
	f.args = append(f.args, args...)
	for i := range args {
		template = strings.Replace(template, "?", f.Placeholder(offset+i), 1)
	}
	f.conditions = append(f.conditions, template)
	return f
}

// AddRaw 追加一个不带参数的 AND 条件（如 `1 = 0`）。
// 与 Add 的区别：Add 会绑定一个参数并分配占位符，AddRaw 只拼字面条件。
// 失败关闭时用得上——需要「恒假」但没有任何值可绑定。
func (f *Filter) AddRaw(condition string) *Filter {
	f.conditions = append(f.conditions, condition)
	return f
}

// ScopeFilter 生成数据范围条件：把 scope 翻译成第一条 WHERE 子句， caller 再 Add 业务条件。
// 这是「隔离是仓储性质」的落地点——所有跨公司的表查询都必须先经过它。
//
// 三条规则（顺序即优先级）：
//   - scope.All（超管平台视角 / 系统内部解析）：不加条件，看全部；
//   - 非 All 且有 TenantID：追加 `alias.tenant_id = ?`，只看本公司；
//   - 非 All 且 TenantID 为空：追加 `1 = 0` 失败关闭。宁可返回空，
//     也不能在「忘了注入 scope」时退化成全量——那是租户隔离最危险的漏洞。
//
// base 语义同 NewFilter：SELECT 传 0，UPDATE 传 SET 已占用的参数个数。
// alias 是主表别名（如 `p`），列名固定为 tenant_id（所有业务表同名）。
func ScopeFilter(base int, alias string, scope contract.Scope) *Filter {
	return ApplyTenant(NewFilter(base), alias, scope)
}

// ApplyTenant 在已有 Filter 上追加租户条件，语义与 ScopeFilter 完全一致。
// 供「先按 owner 过滤、再叠加租户」的分区仓储使用——它不能新建 Filter，
// 否则会把已经加好的 owner 条件丢掉。
func ApplyTenant(filter *Filter, alias string, scope contract.Scope) *Filter {
	if scope.All {
		return filter
	}
	if scope.TenantID == "" {
		return filter.AddRaw(`1 = 0`)
	}
	return filter.Add(alias+`.tenant_id = ?`, scope.TenantID)
}

// ApplyTeam 在已有 Filter 上追加团队边界，仅对 TeamScoped 的身份生效。
// 团队边界的 SQL 形态因实体而异（直接 team_id 列 / 经 team_members / 经 team_projects），
// 所以这里只负责「有 TeamIDs 就绑定一个 uuid 数组参数」，具体表达式由各仓储传入：
//
//	expr 形如 `t.id = ANY(?)`，其中 ? 会被替换为该参数对应的占位符并自动加 ::uuid[]。
//
// 两种情况不追加任何条件：
//   - scope.All（超管平台视角）或 !scope.TeamScoped（平台管理员看全公司）；
//   - 其它身份若 TeamScoped=true 却没有团队，追加 `1 = 0` 失败关闭——
//     没团队的团队管理员/成员不该看到任何数据，不能退化成看全公司。
func ApplyTeam(filter *Filter, expr string, scope contract.Scope) *Filter {
	if scope.All || !scope.TeamScoped {
		return filter
	}
	if len(scope.TeamIDs) == 0 {
		return filter.AddRaw(`1 = 0`)
	}
	offset := len(filter.args)
	filter.args = append(filter.args, scope.TeamIDs)
	filter.conditions = append(filter.conditions,
		strings.ReplaceAll(expr, "?", filter.Placeholder(offset)+"::uuid[]"))
	return filter
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

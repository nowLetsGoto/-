// columns_test.go · 角色查询语句的结构约束。
// 职责：钉住 columns 与 fromClause 必须配套使用——两者拆开就会产生 SQL 错误。
// 输入：包内的查询常量与源码；输出：配套关系断言。
// 边界：读源码做静态检查，不连数据库（本仓单测不依赖 PostgreSQL）。
package role

import (
	"os"
	"strings"
	"testing"
)

// TestFindByIDUsesFromClause 防止再次踩到的坑：
// columns 带 r. 别名且含 coalesce(t.name, '')，必须 JOIN tenants 才能执行。
// 曾有 findByID 漏配 fromClause，导致"新建角色"在最后回读时 500——
// 其余查询都配了，唯独它漏了，单元测试又都不连库，所以一直没发现。
func TestFindByIDUsesFromClause(t *testing.T) {
	data, err := os.ReadFile("repository.go")
	if err != nil {
		t.Fatalf("读取源码失败: %v", err)
	}
	source := string(data)

	idx := strings.Index(source, "func (r *Repository) findByID")
	if idx < 0 {
		t.Fatal("找不到 findByID")
	}
	// 只看 findByID 这一段（到下一个函数为止）。
	tail := source[idx:]
	if end := strings.Index(tail, "\nfunc "); end > 0 {
		tail = tail[:end]
	}

	if !strings.Contains(tail, "SELECT `+columns+fromClause") {
		t.Fatal("findByID 必须拼接 fromClause：columns 带 r. 别名且引用了 tenants 表")
	}
	if strings.Contains(tail, "FROM roles WHERE") {
		t.Fatal("findByID 不能再用裸 FROM roles：columns 需要别名 r 与 JOIN tenants")
	}
}

// TestQueryConstantsAreCoupled 把配套关系本身固定下来：
// columns 一旦带别名/JOIN 字段，本测试就要求所有用到它的查询都带 fromClause。
func TestQueryConstantsAreCoupled(t *testing.T) {
	if !strings.Contains(columns, "r.id") || !strings.Contains(columns, "t.name") {
		t.Fatal("columns 应带 r. 别名并包含 t.name；若改动了列结构，请同步检查所有调用点")
	}
	if !strings.Contains(fromClause, "JOIN tenants t") {
		t.Fatal("fromClause 必须 JOIN tenants t，否则 columns 里的 t.name 无法解析")
	}
}

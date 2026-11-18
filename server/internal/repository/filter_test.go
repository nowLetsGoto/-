// filter_test.go · 验证 ScopeFilter 的三条隔离规则。
// 这是租户隔离的核心：一旦这里的语义被改坏，所有业务表的查询都会跟着失控，
// 因此用不依赖数据库的纯单元测试把它钉死——SQL 片段与参数顺序都能直接断言。
package repository

import (
	"reflect"
	"testing"

	"dandelion/server/internal/contract"
)

func TestScopeFilter(t *testing.T) {
	tests := []struct {
		// name 说明这条规则体现的设计意图。
		name string
		// scope 是权限层推导出的可见范围，是过滤的唯一输入。
		scope contract.Scope
		// base 模拟 UPDATE 的 SET 已占用的参数个数，验证占位符不错位。
		base int
		// wantCond 是期望追加的 WHERE 条件（不含 WHERE 关键字）。
		wantCond string
		// wantArgs 是期望绑定的参数，nil 表示无参数。
		wantArgs []any
	}{
		{
			name:     "超管看全部：不加任何条件",
			scope:    contract.Scope{All: true},
			base:     0,
			wantCond: "",
			wantArgs: nil,
		},
		{
			name:     "公司账号：限定 tenant_id，占位符从 base 起算",
			scope:    contract.Scope{TenantID: "t-1"},
			base:     4,
			wantCond: "p.tenant_id = $5",
			wantArgs: []any{"t-1"},
		},
		{
			name:     "无公司且非超管：失败关闭，恒假条件且零参数",
			scope:    contract.Scope{},
			base:     0,
			wantCond: "1 = 0",
			wantArgs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScopeFilter(tt.base, "p", tt.scope)
			if got.Cond() != tt.wantCond {
				t.Fatalf("条件不符：want %q, got %q", tt.wantCond, got.Cond())
			}
			if !reflect.DeepEqual(got.Args(), tt.wantArgs) {
				t.Fatalf("参数不符：want %v, got %v", tt.wantArgs, got.Args())
			}
		})
	}
}

func TestApplyTenantPreservesExistingConditions(t *testing.T) {
	// 分区仓储先加 owner 条件、再叠加租户条件：ApplyTenant 不能把已有条件冲掉，
	// 否则单人分区之外的项目查询会退化成「只看公司、不看 owner」的越权。
	filter := NewFilter(0).Add(`p.owner_user_id = ?`, "u-1")
	filter = ApplyTenant(filter, "p", contract.Scope{TenantID: "t-1"})

	want := "p.owner_user_id = $1 AND p.tenant_id = $2"
	if got := filter.Cond(); got != want {
		t.Fatalf("条件不符：want %q, got %q", want, got)
	}
	wantArgs := []any{"u-1", "t-1"}
	if got := filter.Args(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("参数不符：want %v, got %v", wantArgs, got)
	}
}

func TestApplyTenantAllIsNoop(t *testing.T) {
	// 超管（All）叠加租户条件时应原样保留已有条件——超管走平台视角看全部。
	filter := NewFilter(0).Add(`p.owner_user_id = ?`, "u-1")
	filter = ApplyTenant(filter, "p", contract.Scope{All: true})

	if got, want := filter.Cond(), "p.owner_user_id = $1"; got != want {
		t.Fatalf("超管不应追加租户条件：want %q, got %q", want, got)
	}
}

func TestApplyTeam(t *testing.T) {
	tests := []struct {
		name     string
		scope    contract.Scope
		wantCond string
		wantArgs []any
	}{
		{
			name:     "超管不受团队边界限制",
			scope:    contract.Scope{All: true, TeamIDs: []string{"team-a"}},
			wantCond: "",
			wantArgs: nil,
		},
		{
			name:     "平台管理员看全公司，不受团队边界限制",
			scope:    contract.Scope{TenantID: "t-1", TeamIDs: []string{}},
			wantCond: "",
			wantArgs: nil,
		},
		{
			name:     "团队管理员按关联团队过滤",
			scope:    contract.Scope{TenantID: "t-1", TeamScoped: true, TeamIDs: []string{"team-a", "team-b"}},
			wantCond: "t.id = ANY($1::uuid[])",
			wantArgs: []any{[]string{"team-a", "team-b"}},
		},
		{
			name:     "团队管理员无关联团队时失败关闭",
			scope:    contract.Scope{TenantID: "t-1", TeamScoped: true, TeamIDs: []string{}},
			wantCond: "1 = 0",
			wantArgs: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyTeam(NewFilter(0), "t.id = ANY(?)", tt.scope)
			if got.Cond() != tt.wantCond {
				t.Fatalf("条件不符：want %q, got %q", tt.wantCond, got.Cond())
			}
			if !reflect.DeepEqual(got.Args(), tt.wantArgs) {
				t.Fatalf("参数不符：want %v, got %v", tt.wantArgs, got.Args())
			}
		})
	}
}

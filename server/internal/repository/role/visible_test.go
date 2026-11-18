// visible_test.go · 验证角色可见性条件。
// 角色不能直接用 ApplyTenant：内置角色靠 tenant_id IS NULL 表示「平台级、所有公司共享」，
// 而 ApplyTenant 会把 NULL 行滤掉。这条测试守住这个差异，避免以后有人"顺手复用"。
package role

import (
	"reflect"
	"testing"

	"dandelion/server/internal/contract"
)

func TestVisibleFilter(t *testing.T) {
	tests := []struct {
		name     string
		scope    contract.Scope
		wantCond string
		wantArgs []any
	}{
		{
			name:     "超管看全部角色",
			scope:    contract.Scope{All: true},
			wantCond: "",
			wantArgs: nil,
		},
		{
			name:     "公司账号看内置加本公司自定义",
			scope:    contract.Scope{TenantID: "t-1"},
			wantCond: "(tenant_id IS NULL OR tenant_id = $1)",
			wantArgs: []any{"t-1"},
		},
		{
			name:     "无公司身份看不到任何角色",
			scope:    contract.Scope{},
			wantCond: "1 = 0",
			wantArgs: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := visibleFilter(0, tt.scope)
			if got.Cond() != tt.wantCond {
				t.Fatalf("条件不符：want %q, got %q", tt.wantCond, got.Cond())
			}
			if !reflect.DeepEqual(got.Args(), tt.wantArgs) {
				t.Fatalf("参数不符：want %v, got %v", tt.wantArgs, got.Args())
			}
		})
	}
}

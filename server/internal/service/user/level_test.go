// level_test.go · 账号创建的层级护栏。
// 职责：钉住"经账号接口只能建 member，且超管/平台管理员永不经此发放"。
// 输入：请求的层级；输出：归一化后的层级或错误。
// 边界：纯函数测试。"谁有资格调这个接口"由路由 RequireMinLevel 决定，不在本文件覆盖。
package user

import (
	"errors"
	"testing"

	"dandelion/server/internal/contract"
)

// TestResolveCreateLevel 覆盖归一化与两条硬底线。
func TestResolveCreateLevel(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		wantLevel string
		wantErr   bool
	}{
		{name: "建 member 原样通过", requested: contract.AccountLevelMember, wantLevel: contract.AccountLevelMember},
		// 请求 team_admin 也归一化为 member：两档现已统一，层级不再有管理含义。
		{name: "请求 team_admin 归一化为 member", requested: contract.AccountLevelTeamAdmin, wantLevel: contract.AccountLevelMember},
		{name: "缺省层级为 member", requested: "", wantLevel: contract.AccountLevelMember},
		// 两条硬底线：任何调用者都不能经本接口发放这两种身份。
		{name: "不能建超管", requested: contract.AccountLevelSuperAdmin, wantErr: true},
		{name: "不能建平台管理员", requested: contract.AccountLevelPlatformAdmin, wantErr: true},
		{name: "非法层级被拒绝", requested: "hacker", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			level, err := resolveCreateLevel(test.requested)
			if test.wantErr {
				if err == nil {
					t.Fatal("应被拒绝，实际放行")
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if level != test.wantLevel {
				t.Fatalf("层级应为 %q，实际 %q", test.wantLevel, level)
			}
		})
	}
}

// TestResolveCreateLevelSuperAdminForbidden 确认超管返回禁止错误，
// 而不是"参数不合法"——两者对调用方语义不同（权限问题 vs 填错）。
func TestResolveCreateLevelSuperAdminForbidden(t *testing.T) {
	_, err := resolveCreateLevel(contract.AccountLevelSuperAdmin)
	if !errors.Is(err, contract.ErrForbidden) {
		t.Fatalf("建超管应返回 ErrForbidden，实际 %v", err)
	}
}

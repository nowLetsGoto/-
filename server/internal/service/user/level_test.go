// level_test.go · 账号创建的层级护栏。
// 职责：钉住"账号接口只能建 member，且必须有 directory.write"。
// 输入：请求的层级与是否具备权限点；输出：归一化后的层级或错误。
// 边界：纯函数测试，不连数据库；越权的具体数据边界由 boundary_test.go 覆盖。
package user

import (
	"errors"
	"testing"

	"dandelion/server/internal/contract"
)

// TestResolveCreateLevel 覆盖新模型下的全部边界。
//
// 关键不变量：super_admin 与 platform_admin 永远不能经账号接口创建——
// 前者是平台身份，后者必须随公司原子创建。这两条不受权限点影响。
func TestResolveCreateLevel(t *testing.T) {
	tests := []struct {
		name           string
		requested      string
		hasAccountWrite bool
		wantLevel      string
		wantErr        bool
	}{
		{name: "有权限建账号时只能建 member", requested: contract.AccountLevelMember, hasAccountWrite: true, wantLevel: contract.AccountLevelMember},
		// 请求 team_admin 也会被归一化为 member：两档现已统一，层级不再有管理含义。
		{name: "请求 team_admin 归一化为 member", requested: contract.AccountLevelTeamAdmin, hasAccountWrite: true, wantLevel: contract.AccountLevelMember},
		{name: "缺省层级为 member", requested: "", hasAccountWrite: true, wantLevel: contract.AccountLevelMember},
		{name: "无权限不能建账号", requested: contract.AccountLevelMember, hasAccountWrite: false, wantErr: true},
		{name: "无权限也不能建 team_admin", requested: contract.AccountLevelTeamAdmin, hasAccountWrite: false, wantErr: true},
		// 两条硬底线：即使有权限也不能突破。
		{name: "不能建超管", requested: contract.AccountLevelSuperAdmin, hasAccountWrite: true, wantErr: true},
		{name: "不能建平台管理员", requested: contract.AccountLevelPlatformAdmin, hasAccountWrite: true, wantErr: true},
		{name: "非法层级被拒绝", requested: "hacker", hasAccountWrite: true, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			level, err := resolveCreateLevel(test.requested, test.hasAccountWrite)
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

// TestResolveCreateLevelSuperAdminForbidden 单独确认超管返回的是禁止错误，
// 而不是"参数不合法"——两者对调用方语义不同（前者是权限问题，后者是填错）。
func TestResolveCreateLevelSuperAdminForbidden(t *testing.T) {
	_, err := resolveCreateLevel(contract.AccountLevelSuperAdmin, true)
	if !errors.Is(err, contract.ErrForbidden) {
		t.Fatalf("建超管应返回 ErrForbidden，实际 %v", err)
	}
}

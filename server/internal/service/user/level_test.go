// level_test.go · 验证三级账号创建的核心护栏 resolveCreateLevel。
// 用纯函数测试把「谁能建谁、能管哪个范围」钉死：这是三级体系的安全边界，
// 一旦改坏就会让公司管理员造出超管、或团队管理员跨团队授权，因此不依赖数据库直接测。
package user

import (
	"testing"

	"dandelion/server/internal/contract"
)

func TestResolveCreateLevel(t *testing.T) {
	tests := []struct {
		name        string
		caller      contract.Identity
		requested   string
		teamIDs     []string
		visibleTeam []string
		wantLevel   string
		wantErr     bool
	}{
		{
			name:      "团队管理员只能建普通成员",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelTeamAdmin, TenantID: "t-1"},
			requested: contract.AccountLevelMember,
			wantLevel: contract.AccountLevelMember,
		},
		{
			name:      "团队管理员不能建团队管理员",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelTeamAdmin, TenantID: "t-1"},
			requested: contract.AccountLevelTeamAdmin,
			wantErr:   true,
		},
		{
			name:      "团队管理员不能建平台管理员",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelTeamAdmin, TenantID: "t-1"},
			requested: contract.AccountLevelPlatformAdmin,
			wantErr:   true,
		},
		{
			name:      "平台管理员不能建平台管理员",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelPlatformAdmin, TenantID: "t-1"},
			requested: contract.AccountLevelPlatformAdmin,
			wantErr:   true,
		},
		{
			name:      "平台管理员可以建团队管理员",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelPlatformAdmin, TenantID: "t-1"},
			requested: contract.AccountLevelTeamAdmin,
			wantLevel: contract.AccountLevelTeamAdmin,
		},
		{
			name:      "任何人都不能建超管",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelSuperAdmin},
			requested: contract.AccountLevelSuperAdmin,
			wantErr:   true,
		},
		{
			name:      "普通成员不能建任何账号",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelMember, TenantID: "t-1"},
			requested: contract.AccountLevelMember,
			wantErr:   true,
		},
		{
			name:      "团队管理员不能跨团队授权",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelTeamAdmin, TenantID: "t-1"},
			requested: contract.AccountLevelMember,
			teamIDs:   []string{"team-b"},
			visibleTeam: []string{"team-a"},
			wantErr:   true,
		},
		{
			name:      "团队管理员可给自己团队的账号指定团队",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelTeamAdmin, TenantID: "t-1"},
			requested: contract.AccountLevelMember,
			teamIDs:   []string{"team-a"},
			visibleTeam: []string{"team-a"},
			wantLevel: contract.AccountLevelMember,
		},
		{
			name:      "平台管理员建普通账号时团队参数被忽略",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelPlatformAdmin, TenantID: "t-1"},
			requested: contract.AccountLevelMember,
			teamIDs:   []string{"team-b"},
			wantLevel: contract.AccountLevelMember,
		},
		{
			name:      "无公司的团队管理员被拒绝",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelTeamAdmin},
			requested: contract.AccountLevelMember,
			wantErr:   true,
		},
		{
			name:      "非法层级被拒绝",
			caller:    contract.Identity{AccountLevel: contract.AccountLevelPlatformAdmin, TenantID: "t-1"},
			requested: "hacker",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level, _, err := resolveCreateLevel(tt.caller, tt.requested, tt.teamIDs, tt.visibleTeam)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望拒绝但通过了：level=%s", level)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望通过但被拒绝：%v", err)
			}
			if level != tt.wantLevel {
				t.Fatalf("层级不符：want %s, got %s", tt.wantLevel, level)
			}
		})
	}
}

func TestResolveCreateLevelTeamIDsRule(t *testing.T) {
	// 只有 team_admin 账号才保留团队关联：其它层级即使传了 teamIds 也必须清空，
	// 否则会出现 member 被误写成团队管理员的数据。
	caller := contract.Identity{AccountLevel: contract.AccountLevelPlatformAdmin, TenantID: "t-1"}
	_, teamIDs, err := resolveCreateLevel(caller, contract.AccountLevelTeamAdmin, []string{"team-a"}, nil)
	if err != nil {
		t.Fatalf("平台管理员建团队管理员应通过：%v", err)
	}
	if len(teamIDs) != 1 || teamIDs[0] != "team-a" {
		t.Fatalf("team_admin 应保留团队关联，got %v", teamIDs)
	}
}

// TestIsSuperAdminRequiresSuperAdminCaller 固化「非超管不能造超管」的红线。
// canGrantSuperAdmin 把它抽成纯判断，便于不启数据库直接测。
func TestCreateIsSuperAdminAllowed(t *testing.T) {
	if canGrantSuperAdmin(contract.Identity{AccountLevel: contract.AccountLevelSuperAdmin}) != true {
		t.Fatal("超管应能设置 isSuperAdmin 标记")
	}
	for _, level := range []string{contract.AccountLevelPlatformAdmin, contract.AccountLevelTeamAdmin, contract.AccountLevelMember} {
		if canGrantSuperAdmin(contract.Identity{AccountLevel: level}) {
			t.Fatalf("%s 不应能设置 isSuperAdmin 标记", level)
		}
	}
}

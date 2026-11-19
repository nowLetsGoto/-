// team_boundary_test.go · 账号查询的团队边界条件。
// 职责：钉住「谁能出现在账号列表里」——这是团队负责人横向越权的最后一道兜底。
// 输入：手工构造的 contract.Scope；输出：Filter 生成的 SQL 条件与参数。
// 边界：只测条件拼装，不连数据库；真实生效由端到端场景覆盖。
package user

import (
	"strings"
	"testing"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// TestApplyTeamBoundary 覆盖四种身份下的条件形态。
func TestApplyTeamBoundary(t *testing.T) {
	tests := []struct {
		name        string
		scope       contract.Scope
		wantNoWhere bool
		wantParts   []string
		wantArgs    int
	}{
		{
			// 超管跨全部公司：不该被任何团队条件收窄。
			name:        "超管不加团队条件",
			scope:       contract.Scope{All: true},
			wantNoWhere: true,
		},
		{
			// 平台管理员管整家公司，TeamScoped=false 是关键判据。
			name:        "平台管理员不加团队条件",
			scope:       contract.Scope{TenantID: "t-1", TeamIDs: []string{}},
			wantNoWhere: true,
		},
		{
			name: "团队负责人只看到自己与本团队普通成员",
			scope: contract.Scope{
				TenantID: "t-1", TeamIDs: []string{"team-a"}, TeamScoped: true, SelfUserID: "self-id",
			},
			wantParts: []string{"u.id = $1", "u.account_level = $2", "team_members", "$3::uuid[]"},
			wantArgs:  3,
		},
		{
			// 失败关闭：团队负责人没有关联任何团队时，条件仍然存在，
			// ANY(空数组) 恒假 → 只剩自己那一行，绝不退化成看全公司。
			name: "团队负责人无团队时只剩自己",
			scope: contract.Scope{
				TenantID: "t-1", TeamIDs: []string{}, TeamScoped: true, SelfUserID: "self-id",
			},
			wantParts: []string{"u.id = $1", "team_members", "$3::uuid[]"},
			wantArgs:  3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filter := repository.NewFilter(0)
			applyTeamBoundary(filter, test.scope)

			cond := filter.Cond()
			if test.wantNoWhere {
				if cond != "" {
					t.Fatalf("不应追加条件，实际得到 %q", cond)
				}
				return
			}
			for _, part := range test.wantParts {
				if !strings.Contains(cond, part) {
					t.Fatalf("条件里应包含 %q，实际 %q", part, cond)
				}
			}
			if got := len(filter.Args()); got != test.wantArgs {
				t.Fatalf("参数个数应为 %d，实际 %d（%v）", test.wantArgs, got, filter.Args())
			}
		})
	}
}

// TestApplyTeamBoundaryExcludesManagementLevels 单独钉住「只放行 member」这一条。
//
// 为什么值得单独测：如果只按团队过滤而不限制层级，两个团队负责人之间就能
// 互相停用账号——他们本该平级，谁都无权处置对方。这条限制是 condition 里的
// u.account_level = member，容易在重构时被"简化"掉。
func TestApplyTeamBoundaryExcludesManagementLevels(t *testing.T) {
	filter := repository.NewFilter(0)
	applyTeamBoundary(filter, contract.Scope{
		TenantID: "t-1", TeamIDs: []string{"team-a"}, TeamScoped: true, SelfUserID: "self-id",
	})

	if !strings.Contains(filter.Cond(), "u.account_level = ") {
		t.Fatalf("必须限制目标层级为普通成员，实际条件 %q", filter.Cond())
	}
	args := filter.Args()
	if len(args) < 2 || args[1] != contract.AccountLevelMember {
		t.Fatalf("层级参数应为 %q，实际 %v", contract.AccountLevelMember, args)
	}
}

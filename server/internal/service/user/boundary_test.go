// boundary_test.go · 账号管理的团队边界（守卫层）。
// 职责：钉住「团队负责人能管谁」这条横向规则——它挡住的是同公司内的账号接管。
// 输入：手工构造的 Identity / Scope 与假团队查询；输出：是否被拒。
// 边界：只测判定逻辑，不连数据库；仓储兜底另由 team_boundary_test 覆盖。
package user

import (
	"context"
	"errors"
	"testing"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	userrepo "dandelion/server/internal/repository/user"
)

// fakeTeams 只实现边界校验用到的两个方法。
// 刻意用固定数据而不是真实仓储：这两条规则的正确性与 SQL 无关，
// 用假实现能把「人员属于哪些团队」变成可直接阅读的常量。
type fakeTeams struct {
	adminTeams  map[string][]string // userID → 它管理的团队
	memberTeams map[string][]string // personID → 它所属的团队
}

func (f fakeTeams) AdminTeamIDs(_ context.Context, userID string) ([]string, error) {
	return f.adminTeams[userID], nil
}

func (f fakeTeams) MemberTeamIDs(_ context.Context, personID string) ([]string, error) {
	return f.memberTeams[personID], nil
}

// newTestService 构造只带 teams 依赖的 Service：其余字段在本组判定中不会被触达。
func newTestService(teams teamLookup) *Service {
	return &Service{teams: teams}
}

// TestPersonInTeams 覆盖「目标人员是否落在调用者团队内」的所有分支。
func TestPersonInTeams(t *testing.T) {
	svc := newTestService(fakeTeams{memberTeams: map[string][]string{
		"person-in":     {"team-a"},
		"person-multi":  {"team-x", "team-b"},
		"person-other":  {"team-z"},
	}})

	tests := []struct {
		name     string
		personID string
		teamIDs  []string
		want     bool
	}{
		{name: "人员在本团队", personID: "person-in", teamIDs: []string{"team-a", "team-c"}, want: true},
		{name: "多团队中命中其一", personID: "person-multi", teamIDs: []string{"team-b"}, want: true},
		{name: "人员不在本团队", personID: "person-other", teamIDs: []string{"team-a"}, want: false},
		// 失败关闭：没有人员或没有团队时一律判为不在，绝不因为"信息缺失"放大成可见。
		{name: "账号未绑定人员", personID: "", teamIDs: []string{"team-a"}, want: false},
		{name: "调用者没有任何团队", personID: "person-in", teamIDs: nil, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := svc.personInTeams(context.Background(), test.personID, test.teamIDs)
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if got != test.want {
				t.Fatalf("want %v, got %v", test.want, got)
			}
		})
	}
}

// TestEnsureInScopeTeamBoundary 覆盖三层判定：公司 → 团队 → 层级。
//
// 这组规则等价于「团队负责人能否接管同公司其他团队的账号」，
// 是本次修复的核心；每条用例都对应一个曾经实测成功的攻击路径。
func TestEnsureInScopeTeamBoundary(t *testing.T) {
	svc := newTestService(fakeTeams{memberTeams: map[string][]string{
		"person-mine":  {"team-a"},
		"person-other": {"team-b"},
	}})
	// 团队负责人的 scope：TeamScoped=true 且带自己的 id。
	teamScope := contract.Scope{
		TenantID: "t-1", TeamIDs: []string{"team-a"}, TeamScoped: true, SelfUserID: "self-id",
	}
	// 平台管理员的 scope：TeamScoped=false，管整家公司。
	// 这不是测试便利，而是 ScopeOf 的真实产物——区间边界只认 scope，不认 caller 层级。
	platformScope := contract.Scope{TenantID: "t-1", TeamIDs: []string{}}

	teamAdmin := contract.Identity{
		UserID: "self-id", TenantID: "t-1", AccountLevel: contract.AccountLevelTeamAdmin,
	}

	tests := []struct {
		name    string
		caller  contract.Identity
		scope   contract.Scope
		target  userrepo.Record
		wantErr bool
	}{
		{
			name:   "自己始终可操作",
			caller: teamAdmin,
			scope:  teamScope,
			// 自己的层级是 team_admin，若不显式放行会被下面的层级规则挡住。
			target: userrepo.Record{User: contract.User{
				ID: "self-id", AccountLevel: contract.AccountLevelTeamAdmin,
			}, TenantID: "t-1"},
		},
		{
			name:   "本团队的普通成员可操作",
			caller: teamAdmin,
			scope:  teamScope,
			target: userrepo.Record{User: contract.User{
				ID: "u-1", PersonID: "person-mine", AccountLevel: contract.AccountLevelMember,
			}, TenantID: "t-1"},
		},
		{
			name:   "别团队的普通成员不可操作",
			caller: teamAdmin,
			scope:  teamScope,
			target: userrepo.Record{User: contract.User{
				ID: "u-2", PersonID: "person-other", AccountLevel: contract.AccountLevelMember,
			}, TenantID: "t-1"},
			wantErr: true,
		},
		{
			// 平级之间互相处置是最容易被忽略的一条：两个团队负责人本该对冲。
			name:   "另一个团队负责人不可操作",
			caller: teamAdmin,
			scope:  teamScope,
			target: userrepo.Record{User: contract.User{
				ID: "u-3", PersonID: "person-mine", AccountLevel: contract.AccountLevelTeamAdmin,
			}, TenantID: "t-1"},
			wantErr: true,
		},
		{
			name:   "平台管理员账号不可被团队负责人操作",
			caller: teamAdmin,
			scope:  teamScope,
			target: userrepo.Record{User: contract.User{
				ID: "u-4", PersonID: "person-mine", AccountLevel: contract.AccountLevelPlatformAdmin,
			}, TenantID: "t-1"},
			wantErr: true,
		},
		{
			name:   "未绑定人员的账号不可操作",
			caller: teamAdmin,
			scope:  teamScope,
			target: userrepo.Record{User: contract.User{
				ID: "u-5", PersonID: "", AccountLevel: contract.AccountLevelMember,
			}, TenantID: "t-1"},
			wantErr: true,
		},
		{
			// 平台管理员 TeamScoped=false，管整家公司：同一批目标对它必须全部放行。
			name:   "平台管理员不受团队边界限制",
			caller: contract.Identity{UserID: "pa", TenantID: "t-1", AccountLevel: contract.AccountLevelPlatformAdmin},
			scope:  platformScope,
			target: userrepo.Record{User: contract.User{
				ID: "u-6", PersonID: "person-other", AccountLevel: contract.AccountLevelMember,
			}, TenantID: "t-1"},
		},
		{
			name:   "跨公司一律不可见",
			caller: teamAdmin,
			scope:  teamScope,
			target: userrepo.Record{User: contract.User{
				ID: "u-7", PersonID: "person-mine", AccountLevel: contract.AccountLevelMember,
			}, TenantID: "t-other"},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := authctx.WithScope(context.Background(), test.scope)
			err := svc.ensureInScope(ctx, test.caller, test.target)
			if test.wantErr {
				if !errors.Is(err, errAccountNotFound) {
					t.Fatalf("应返回 errAccountNotFound，实际: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错，实际: %v", err)
			}
		})
	}
}

// TestEnsurePersonAssignable 钉住「团队负责人建/换绑账号必须绑定本团队人员」。
func TestEnsurePersonAssignable(t *testing.T) {
	svc := newTestService(fakeTeams{memberTeams: map[string][]string{
		"person-mine":  {"team-a"},
		"person-other": {"team-b"},
	}})
	ctx := authctx.WithScope(context.Background(), contract.Scope{
		TenantID: "t-1", TeamIDs: []string{"team-a"}, TeamScoped: true, SelfUserID: "self-id",
	})
	teamAdmin := contract.Identity{
		UserID: "self-id", TenantID: "t-1", AccountLevel: contract.AccountLevelTeamAdmin,
	}
	platformAdmin := contract.Identity{
		UserID: "pa", TenantID: "t-1", AccountLevel: contract.AccountLevelPlatformAdmin,
	}

	tests := []struct {
		name     string
		caller   contract.Identity
		personID string
		wantErr  bool
	}{
		{name: "本团队人员可绑定", caller: teamAdmin, personID: "person-mine"},
		{name: "别团队人员不可绑定", caller: teamAdmin, personID: "person-other", wantErr: true},
		// 必填：账号的权限来自人员，无人员账号对团队负责人既看不到也用不上。
		{name: "团队负责人必须绑定人员", caller: teamAdmin, personID: "", wantErr: true},
		// 与前端 AccountFormPage 的必填校验保持一致：除超管外都得绑定人员。
		// 两层各说一套会让人以为界面拦错了。
		{name: "平台管理员也必须绑定人员", caller: platformAdmin, personID: "", wantErr: true},
		{name: "平台管理员可绑定任意本公司人员", caller: platformAdmin, personID: "person-other"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := svc.ensurePersonAssignable(ctx, test.caller, test.personID)
			if test.wantErr {
				if err == nil {
					t.Fatal("应被拒绝，实际放行")
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错，实际: %v", err)
			}
		})
	}
}

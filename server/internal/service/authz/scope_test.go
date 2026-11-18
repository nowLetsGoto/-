// scope_test.go · 数据范围推导的四级覆盖。
// 职责：验证 super_admin / platform_admin / team_admin / member 各自推导出正确的 Scope，
//       以及过渡期 is_super_admin 布尔与未知层级的边界行为。
// 输入：内存假仓储 + 身份；输出：Scope 断言。
// 边界：不连数据库、不启动 HTTP；仓储用假实现只为了满足窄接口，不验证 SQL 本身。
package authz

import (
	"context"
	"reflect"
	"testing"

	"dandelion/server/internal/contract"
	userrepo "dandelion/server/internal/repository/user"
)

// fakeUsers / fakePeople / fakeTeams 是三个最小假实现。
// 只填测试需要的那张表；未命中的键返回 ErrNotFound，模拟"查不到"。
type fakeUsers struct {
	byID   map[string]userrepo.Record
	byName map[string]userrepo.Record
}

func (f *fakeUsers) FindByID(_ context.Context, id string) (userrepo.Record, error) {
	record, ok := f.byID[id]
	if !ok {
		return userrepo.Record{}, contract.ErrNotFound
	}
	return record, nil
}

func (f *fakeUsers) FindByUsername(_ context.Context, username string) (userrepo.Record, error) {
	record, ok := f.byName[username]
	if !ok {
		return userrepo.Record{}, contract.ErrNotFound
	}
	return record, nil
}

type fakePeople struct {
	byID   map[string]contract.Person
	byName map[string]contract.Person
}

func (f *fakePeople) FindByID(_ context.Context, id string, _ contract.Scope) (contract.Person, error) {
	person, ok := f.byID[id]
	if !ok {
		return contract.Person{}, contract.ErrNotFound
	}
	return person, nil
}

func (f *fakePeople) FindByUsername(_ context.Context, username string, _ contract.Scope) (contract.Person, error) {
	person, ok := f.byName[username]
	if !ok {
		return contract.Person{}, contract.ErrNotFound
	}
	return person, nil
}

type fakeTeams struct {
	admin  map[string][]string
	member map[string][]string
}

func (f *fakeTeams) AdminTeamIDs(_ context.Context, userID string) ([]string, error) {
	return append([]string{}, f.admin[userID]...), nil
}

func (f *fakeTeams) MemberTeamIDs(_ context.Context, personID string) ([]string, error) {
	return append([]string{}, f.member[personID]...), nil
}

// newScopeService 装配一个只够跑 ScopeOf 的 Service。
// projects / roles 与 scope 无关，留零值即可（ScopeOf 不会碰到它们）。
func newScopeService(teams *fakeTeams, users *fakeUsers, people *fakePeople) *Service {
	return &Service{users: users, people: people, teams: teams}
}

func TestScopeOfFourLevels(t *testing.T) {
	teams := &fakeTeams{
		admin: map[string][]string{
			"admin-user": {"team-a", "team-b"},
		},
		member: map[string][]string{
			"person-1": {"team-c"},
		},
	}
	users := &fakeUsers{
		byID: map[string]userrepo.Record{
			// 成员账号显式绑定人员；ScopeOf 必须从 users.person_id 找到团队关系的主体。
			"member-user": {User: contract.User{PersonID: "person-1"}},
		},
		byName: map[string]userrepo.Record{},
	}
	people := &fakePeople{byID: map[string]contract.Person{}, byName: map[string]contract.Person{}}
	svc := newScopeService(teams, users, people)

	cases := []struct {
		name     string
		identity contract.Identity
		want     contract.Scope
	}{
		{
			name:     "超管跨全部公司",
			identity: contract.Identity{AccountLevel: contract.AccountLevelSuperAdmin, IsSuperAdmin: true},
			want:     contract.Scope{All: true},
		},
		{
			name: "平台管理员限本公司全部团队",
			identity: contract.Identity{
				AccountLevel: contract.AccountLevelPlatformAdmin, TenantID: "tenant-1", UserID: "platform-user",
			},
			want: contract.Scope{TenantID: "tenant-1", TeamIDs: []string{}},
		},
		{
			name: "团队管理员取 team_admins 关联并集",
			identity: contract.Identity{
				AccountLevel: contract.AccountLevelTeamAdmin, TenantID: "tenant-1", UserID: "admin-user",
			},
			want: contract.Scope{TenantID: "tenant-1", TeamIDs: []string{"team-a", "team-b"}},
		},
		{
			name: "普通成员取 team_members 关联并集",
			identity: contract.Identity{
				AccountLevel: contract.AccountLevelMember, TenantID: "tenant-1", UserID: "member-user",
			},
			want: contract.Scope{TenantID: "tenant-1", TeamIDs: []string{"team-c"}},
		},
		{
			name: "过渡期旧超管布尔仍按超管推导",
			identity: contract.Identity{
				AccountLevel: contract.AccountLevelMember, IsSuperAdmin: true, TenantID: "tenant-1",
			},
			want: contract.Scope{All: true},
		},
		{
			name: "成员没有绑定人员时边界为空而不是全量",
			identity: contract.Identity{
				AccountLevel: contract.AccountLevelMember, TenantID: "tenant-1", UserID: "no-person",
			},
			want: contract.Scope{TenantID: "tenant-1", TeamIDs: []string{}},
		},
		{
			name:     "未知层级失败关闭",
			identity: contract.Identity{AccountLevel: "hacker", TenantID: "tenant-1"},
			want:     contract.Scope{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.ScopeOf(context.Background(), tc.identity)
			if err != nil {
				t.Fatalf("ScopeOf 不应报错: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Scope 不符: got=%+v want=%+v", got, tc.want)
			}
			// 所有非超管分支都不能退化成 All；租户边界由上面的精确断言覆盖。
			if !tc.want.All && got.All {
				t.Fatalf("非超管 scope 不能跨全部公司: %+v", got)
			}
		})
	}
}

// TestScopeOfIgnoresUnboundPersonFallback 验证存量账号的"登录名回退"也能拿到成员团队：
// /me 展示的人与 scope 用的人必须是同一个，这是两条路径共用 PersonID 的意义。
func TestScopeOfIgnoresUnboundPersonFallback(t *testing.T) {
	svc := newScopeService(
		&fakeTeams{member: map[string][]string{"person-by-name": {"team-x"}}},
		&fakeUsers{byID: map[string]userrepo.Record{
			// 存量账号存在，但还没绑定人员 → 走按登录名回退的兼容分支。
			"legacy-id": {User: contract.User{Username: "legacy-user", PersonID: ""}},
		}, byName: map[string]userrepo.Record{}},
		&fakePeople{byID: map[string]contract.Person{}, byName: map[string]contract.Person{
			"legacy-user": {ID: "person-by-name"},
		}},
	)
	got, err := svc.ScopeOf(context.Background(), contract.Identity{
		AccountLevel: contract.AccountLevelMember, TenantID: "tenant-2",
		UserID: "legacy-id", Username: "legacy-user",
	})
	if err != nil {
		t.Fatalf("ScopeOf 不应报错: %v", err)
	}
	want := contract.Scope{TenantID: "tenant-2", TeamIDs: []string{"team-x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("登录名回退的成员 scope 不符: got=%+v want=%+v", got, want)
	}
}

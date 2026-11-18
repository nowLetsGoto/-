// level_test.go · 管理层级守门器的行为锁。
// 职责：验证 RequireMinLevel 按四级顺序放行/拒绝，RequireSuperAdmin 别名语义不变，
//       以及过渡期旧 is_super_admin 布尔仍能通过超管门槛。
// 输入：httptest 请求 + 上下文身份；输出：HTTP 状态码与"是否到达下游 handler"。
// 边界：不连数据库、不校验令牌（RequireAuth 的职责），只测层级比较本身。
package login

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
)

// runGate 把身份塞进 ctx 后过一遍中间件，返回状态码与下游是否被调用。
func runGate(middleware func(http.Handler) http.Handler, identity contract.Identity) (int, bool) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	middleware(next).ServeHTTP(rec, req.WithContext(authctx.With(req.Context(), identity)))
	return rec.Code, reached
}

func TestRequireMinLevel(t *testing.T) {
	cases := []struct {
		name     string
		min      string
		identity contract.Identity
		wantCode int
	}{
		{
			name: "超管过超管门槛", min: contract.AccountLevelSuperAdmin,
			identity: contract.Identity{AccountLevel: contract.AccountLevelSuperAdmin, IsSuperAdmin: true},
			wantCode: http.StatusNoContent,
		},
		{
			name: "平台管理员过平台门槛", min: contract.AccountLevelPlatformAdmin,
			identity: contract.Identity{AccountLevel: contract.AccountLevelPlatformAdmin},
			wantCode: http.StatusNoContent,
		},
		{
			name: "团队管理员过团队门槛", min: contract.AccountLevelTeamAdmin,
			identity: contract.Identity{AccountLevel: contract.AccountLevelTeamAdmin},
			wantCode: http.StatusNoContent,
		},
		{
			name: "普通成员过成员门槛", min: contract.AccountLevelMember,
			identity: contract.Identity{AccountLevel: contract.AccountLevelMember},
			wantCode: http.StatusNoContent,
		},
		{
			name: "团队管理员进不了平台门槛", min: contract.AccountLevelPlatformAdmin,
			identity: contract.Identity{AccountLevel: contract.AccountLevelTeamAdmin},
			wantCode: http.StatusForbidden,
		},
		{
			name: "平台管理员进不了超管门槛", min: contract.AccountLevelSuperAdmin,
			identity: contract.Identity{AccountLevel: contract.AccountLevelPlatformAdmin},
			wantCode: http.StatusForbidden,
		},
		{
			name: "旧布尔超管仍过超管门槛", min: contract.AccountLevelSuperAdmin,
			identity: contract.Identity{AccountLevel: contract.AccountLevelMember, IsSuperAdmin: true},
			wantCode: http.StatusNoContent,
		},
		{
			name: "空身份进不了任何门槛", min: contract.AccountLevelMember,
			identity: contract.Identity{},
			wantCode: http.StatusForbidden,
		},
		{
			name: "门槛拼错时失败关闭", min: "superadmin",
			identity: contract.Identity{AccountLevel: contract.AccountLevelSuperAdmin, IsSuperAdmin: true},
			wantCode: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, reached := runGate(RequireMinLevel(tc.min), tc.identity)
			if code != tc.wantCode {
				t.Fatalf("状态码=%d，期望 %d", code, tc.wantCode)
			}
			if reached != (tc.wantCode == http.StatusNoContent) {
				t.Fatalf("下游是否被调用=%v，与状态码 %d 矛盾", reached, code)
			}
		})
	}
}

func TestRequireSuperAdminAlias(t *testing.T) {
	// 别名必须和 RequireMinLevel(super_admin) 完全等价，存量路由才不会在过渡期改变行为。
	alias := func(next http.Handler) http.Handler { return RequireSuperAdmin(next) }
	code, reached := runGate(alias, contract.Identity{AccountLevel: contract.AccountLevelSuperAdmin, IsSuperAdmin: true})
	if code != http.StatusNoContent || !reached {
		t.Fatalf("超管应通过别名守门器: code=%d reached=%v", code, reached)
	}
	code, reached = runGate(alias, contract.Identity{AccountLevel: contract.AccountLevelPlatformAdmin})
	if code != http.StatusForbidden || reached {
		t.Fatalf("平台管理员应被别名守门器拒绝: code=%d reached=%v", code, reached)
	}
}

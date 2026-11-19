// builtin_test.go · 内置角色的定义约束。
// 职责：钉住「角色体系里不存在身份角色」这条领域规则——
// 超级管理员与平台管理员是账号身份（users.is_super_admin / account_level），
// 一旦有人又把它们加回 builtinRoles，这里的测试会立刻失败。
// 输入：包内的 builtinRoles 定义；输出：断言结果。
// 边界：纯数据断言，不碰数据库、不启动 HTTP。
package role

import (
	"testing"

	"dandelion/server/internal/contract"
)

// TestBuiltinRolesContainOnlyBusinessRoles 保证内置角色只描述「能做什么业务」，
// 不混入「能管到哪一层」的身份。
//
// 为什么值得单独钉死：super-admin 角色曾经存在过，权限判定从不读它（第一步就按
// is_super_admin 短路），它唯一的作用是让角色列表里出现一个"看起来像超管"的选项，
// 已经被人误解过一次。加这条测试，防止将来又被"顺手"加回去。
func TestBuiltinRolesContainOnlyBusinessRoles(t *testing.T) {
	forbidden := map[string]string{
		"super-admin":    "超级管理员是账号身份，不是角色",
		"platform-admin": "平台管理员由超管建公司时创建，是账号层级，不是角色",
	}

	for _, definition := range builtinRoles {
		if reason, bad := forbidden[definition.Key]; bad {
			t.Fatalf("内置角色不应包含 %q：%s", definition.Key, reason)
		}
	}
}

// TestBuiltinRolesHaveExpectedKeys 固定内置角色的清单与顺序。
// 顺序有意义：前端按这个顺序展示，改顺序会让用户觉得"角色列表变了"。
func TestBuiltinRolesHaveExpectedKeys(t *testing.T) {
	want := []string{"project-owner", "developer", "tester", "guest"}

	if len(builtinRoles) != len(want) {
		t.Fatalf("内置角色应为 %d 个，实际 %d 个", len(want), len(builtinRoles))
	}
	for i, key := range want {
		if builtinRoles[i].Key != key {
			t.Fatalf("第 %d 个内置角色应为 %q，实际 %q", i+1, key, builtinRoles[i].Key)
		}
	}
}

// TestBuiltinRolesAssignOnlySelectablePermissions 保证内置角色只勾可勾选的业务权限点。
//
// 管理类权限点（directory.write / settings.write / admin.access / role.delete）
// 由 account_level 直接下发，不该出现在任何角色的权限里——否则同一个能力会有
// 「身份给的」和「角色给的」两个来源，排查权限问题时无法确定是哪一个生效。
func TestBuiltinRolesAssignOnlySelectablePermissions(t *testing.T) {
	for _, definition := range builtinRoles {
		for _, permission := range definition.Permissions {
			if !contract.IsValidPermission(permission) {
				t.Fatalf("内置角色 %q 含不可勾选的管理权限点 %q", definition.Key, permission)
			}
		}
	}
}

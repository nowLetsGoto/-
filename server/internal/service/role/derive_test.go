// derive_test.go · 权限由菜单树推导的规则。
// 职责：钉住"有菜单才有查看权、操作权限必须挂在已勾菜单下"这两条不变量。
// 输入：勾选的菜单 + 请求里的权限点；输出：推导后的最终权限集合。
// 边界：纯函数测试，不连数据库。
package role

import (
	"testing"

	"dandelion/server/internal/contract"
)

// TestDerivePermissionsViewFromMenu 验证勾菜单自动获得查看权限。
func TestDerivePermissionsViewFromMenu(t *testing.T) {
	tests := []struct {
		name     string
		menuKeys []string
		want     string // 期望获得的查看权限
	}{
		{"勾角色管理得到 role.read", []string{"/directory/roles"}, contract.PermissionRoleRead},
		{"勾审计日志得到 audit.read", []string{"/admin/audit-logs"}, contract.PermissionAuditRead},
		{"勾平台集成得到 settings.read", []string{"/settings"}, contract.PermissionSettingsRead},
		{"勾项目管理得到 project.read", []string{"/directory/projects"}, "project.read"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := derivePermissions(test.menuKeys, nil)
			if _, ok := contains(got, test.want); !ok {
				t.Fatalf("勾菜单 %v 应自动获得 %q，实际 %v", test.menuKeys, test.want, got)
			}
		})
	}
}

// TestDerivePermissionsRejectsViewWithoutMenu 是关键安全护栏：
// 请求里偷偷塞查看权限，但没勾对应菜单，必须被丢弃。
// 这正是旧设计里"用户勾了菜单却忘了勾 role.read"问题的反面——
// 现在反过来也不允许"不勾菜单却拿查看权"。
func TestDerivePermissionsRejectsViewWithoutMenu(t *testing.T) {
	got := derivePermissions([]string{"/overview"}, []string{
		contract.PermissionRoleRead,
		contract.PermissionAuditRead,
		contract.PermissionSettingsRead,
	})
	viewPermissions := contract.ViewPermissions()
	for _, permission := range got {
		// build.read 来自 overview，其余塞进来的查看权限都应被清掉。
		if _, isView := viewPermissions[permission]; isView && permission != "build.read" {
			t.Fatalf("未勾菜单却拿到了查看权限 %q：%v", permission, got)
		}
	}
}

// TestDerivePermissionsActionsNeedMenu 验证操作权限必须挂在已勾菜单下。
func TestDerivePermissionsActionsNeedMenu(t *testing.T) {
	tests := []struct {
		name       string
		menuKeys   []string
		requested  []string
		wantAction string
		wantKept   bool
	}{
		{"勾应用包可保留上传", []string{"/packages"}, []string{"build.write"}, "build.write", true},
		{"没勾应用包则丢弃上传", []string{"/overview"}, []string{"build.write"}, "build.write", false},
		{"勾角色管理可保留编辑", []string{"/directory/roles"}, []string{contract.PermissionRoleWrite}, contract.PermissionRoleWrite, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := derivePermissions(test.menuKeys, test.requested)
			_, kept := contains(got, test.wantAction)
			if kept != test.wantKept {
				t.Fatalf("操作权限 %q 保留=%v 期望=%v，实际 %v", test.wantAction, kept, test.wantKept, got)
			}
		})
	}
}

// TestDerivePermissionsNoDuplicates 防止重复权限点写入（数据库有主键，重复会插入失败）。
func TestDerivePermissionsNoDuplicates(t *testing.T) {
	got := derivePermissions(
		[]string{"/overview", "/packages", "/apps"}, // 三项都授予 build.read
		[]string{"build.write", "build.write"},
	)
	seen := make(map[string]struct{})
	for _, permission := range got {
		if _, dup := seen[permission]; dup {
			t.Fatalf("推导结果有重复权限 %q: %v", permission, got)
		}
		seen[permission] = struct{}{}
	}
}

// contains 是测试内的小工具，避免为一次查找引入 slices 包。
func contains(values []string, target string) (int, bool) {
	for i, value := range values {
		if value == target {
			return i, true
		}
	}
	return -1, false
}

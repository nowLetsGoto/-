// service_test.go · 权限基线。
// 职责：验证无角色账号只拿到「只读」基线，人可看、人不可改；单人模式不因此获得平台能力。
// 输入：内存中的 Identity；输出：断言结果。
// 边界：不碰数据库、不启动 HTTP；团队模式的角色解析另由集成场景覆盖。
package authz

import (
	"context"
	"testing"

	"dandelion/server/internal/contract"
)

// TestTeamDefaultGrantsDirectoryReadOnly 是本次需求的核心断言：
// 团队模式的普通成员（没有任何角色）必须能进人员管理，但不能新增或编辑。
func TestTeamDefaultGrantsDirectoryReadOnly(t *testing.T) {
	// 没有角色的账号在 resolveRoleKey 之后会直接返回基线，不会查到角色表；
	// 这里用空 repository 足以覆盖到该分支。
	svc := &Service{}
	permissions, err := svc.Permissions(context.Background(), contract.Identity{
		Username: "team-member",
		Mode:     contract.ModeTeam,
	}, "")
	if err != nil {
		t.Fatalf("团队模式的基本判定不应报错: %v", err)
	}

	for _, permission := range []string{"build.read", "project.read", "directory.read"} {
		if _, ok := permissions[permission]; !ok {
			t.Fatalf("团队模式基线应包含 %s", permission)
		}
	}
	// 写权限与管理类权限必须留在角色里，不能随基线一起放开。
	for _, forbidden := range []string{"directory.write", "settings.write", "admin.access"} {
		if _, ok := permissions[forbidden]; ok {
			t.Fatalf("团队模式基线不应包含 %s", forbidden)
		}
	}
	// 角色管理已收归超管，权限目录里不再有 role.*；基线若出现它们说明目录又漂了。
	for _, removed := range []string{"role.read", "role.write"} {
		if _, ok := permissions[removed]; ok {
			t.Fatalf("权限目录已删除 %s，基线不应包含它", removed)
		}
	}
}

func TestSoloModeGrantsBusinessPermissionsWithoutRole(t *testing.T) {
	// 单人分支会在查库前返回，因此这里不需要构造 repository。
	svc := &Service{}
	permissions, err := svc.Permissions(context.Background(), contract.Identity{
		Username: "solo-user",
		Mode:     contract.ModeSolo,
	}, "default")
	if err != nil {
		t.Fatalf("单人模式不应报错: %v", err)
	}

	for _, permission := range []string{"build.read", "build.write", "build.delete", "project.read", "project.write"} {
		if _, ok := permissions[permission]; !ok {
			t.Fatalf("单人模式应具备 %s", permission)
		}
	}
	for _, forbidden := range []string{"settings.write", "admin.access"} {
		if _, ok := permissions[forbidden]; ok {
			t.Fatalf("单人模式不应获得平台权限 %s", forbidden)
		}
	}
}

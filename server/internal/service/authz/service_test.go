// service_test.go · 单人模式的权限基线。
// 职责：验证单人账号不依赖角色即可使用业务菜单，同时不会拿到平台管理权限。
// 输入：内存中的 Identity；输出：断言结果。
// 边界：不碰数据库、不启动 HTTP；团队模式的角色解析另由集成场景覆盖。
package authz

import (
	"context"
	"testing"

	"dandelion/server/internal/contract"
)

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

	for _, permission := range []string{"build.read", "build.write", "project.read"} {
		if _, ok := permissions[permission]; !ok {
			t.Fatalf("单人模式应具备 %s", permission)
		}
	}
	for _, forbidden := range []string{"settings.write", "role.write", "admin.access"} {
		if _, ok := permissions[forbidden]; ok {
			t.Fatalf("单人模式不应获得平台权限 %s", forbidden)
		}
	}
}

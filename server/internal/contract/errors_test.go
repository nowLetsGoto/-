// errors_test.go · 资源缺失错误的文案与判定。
// 职责：钉住「一处定义、两处生效」——NotFound(resource) 既生成准确文案，
//       又能被所有既有的 errors.Is(err, ErrNotFound) 判断识别。
// 输入：contract 的错误构造函数；输出：文案断言与 errors.Is 断言。
// 边界：纯值测试，不碰 handler、不连数据库。
package contract

import (
	"errors"
	"strings"
	"testing"
)

// TestNotFoundMessageIsResourceSpecific 保证每个资源拿到的是自己的名字，
// 而不是历史上那个写死"包"的通用 sentinel 文案。
func TestNotFoundMessageIsResourceSpecific(t *testing.T) {
	tests := []struct {
		resource string
		want     string
	}{
		{ResourceBuild, "应用包不存在或已被移除"},
		{ResourcePerson, "人员不存在或已被移除"},
		{ResourceTeam, "团队不存在或已被移除"},
		{ResourceUser, "账号不存在或已被移除"},
		{ResourceRole, "角色不存在或已被移除"},
		{ResourceTenant, "公司不存在或已被移除"},
	}

	for _, test := range tests {
		err := NotFound(test.resource)
		if err.Error() != test.want {
			t.Fatalf("%s 的文案应为 %q，实际 %q", test.resource, test.want, err.Error())
		}
	}
}

// TestNotFoundDoesNotLeakOldPhrase 防止有人再把文案改回绑定具体资源的说法。
//
// 这条测试看似琐碎，实则守着本次改造的全部价值：
// 一旦有人把 Error() 改回 "包不存在或已被移除"，人员/团队的 404 就会再次串场。
func TestNotFoundDoesNotLeakOldPhrase(t *testing.T) {
	for _, resource := range []string{ResourcePerson, ResourceTeam, ResourceUser, ResourceRole} {
		message := NotFound(resource).Error()
		if strings.Contains(message, "包不存在") {
			t.Fatalf("%s 的文案不应出现「包不存在」：%q", resource, message)
		}
		if !strings.Contains(message, resource) {
			t.Fatalf("%s 的文案应包含资源名：%q", resource, message)
		}
	}
}

// TestNotFoundMatchesSentinel 是整套改造能平滑落地的关键。
//
// 仓储返回的是资源化的 NotFoundError，但中间件与 service 里仍有大量
// errors.Is(err, contract.ErrNotFound) 判断。Is 方法让两者等价，
// 于是「改文案」和「改判定」解耦，不必动那些判断。
func TestNotFoundMatchesSentinel(t *testing.T) {
	for _, resource := range []string{ResourceBuild, ResourcePerson, ResourceTeam, ResourceUser} {
		err := NotFound(resource)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("NotFound(%s) 应能被 ErrNotFound 识别", resource)
		}
	}
	// 反向不成立：通用 sentinel 不应该被识别成某个具体资源错误。
	var missing *NotFoundError
	if errors.As(ErrNotFound, &missing) {
		t.Fatal("通用 ErrNotFound 不应匹配具体的 NotFoundError")
	}
}

// TestGenericSentinelMessage 保证兜底文案也是中性的。
func TestGenericSentinelMessage(t *testing.T) {
	if strings.Contains(ErrNotFound.Error(), "包") {
		t.Fatalf("兜底文案不应绑定具体资源：%q", ErrNotFound.Error())
	}
}

// TestMenusAreUniqueAndValid 钉住菜单目录的结构约束。
//
// 菜单 key 同时是前端路由路径和数据库授权值，一旦重复或为空，
// 授权树会出现两个同名项、或者拿起一个永远匹配不上路由的 key。
func TestMenusAreUniqueAndValid(t *testing.T) {
	seen := make(map[string]struct{}, len(Menus))
	for _, menu := range Menus {
		if menu.Key == "" || menu.Key[0] != '/' {
			t.Fatalf("菜单 key 应是页面路径，实际 %q", menu.Key)
		}
		if menu.Label == "" || menu.Group == "" {
			t.Fatalf("菜单 %q 缺少 label 或 group", menu.Key)
		}
		if _, dup := seen[menu.Key]; dup {
			t.Fatalf("菜单 key 重复: %s", menu.Key)
		}
		seen[menu.Key] = struct{}{}
	}
}

// TestAllMenuKeysMatchesCatalog 保证"默认全量"与目录始终同源。
func TestAllMenuKeysMatchesCatalog(t *testing.T) {
	keys := AllMenuKeys()
	if len(keys) != len(Menus) {
		t.Fatalf("全量 key 数=%d 与目录数=%d 不一致", len(keys), len(Menus))
	}
	for i, menu := range Menus {
		if keys[i] != menu.Key {
			t.Fatalf("第 %d 项：全量=%q 目录=%q", i, keys[i], menu.Key)
		}
		if !IsValidMenu(menu.Key) {
			t.Fatalf("目录里的 %q 应通过合法性校验", menu.Key)
		}
	}
}

// TestIsValidMenuRejectsUnknown 防止伪造的菜单 key 被写入角色。
func TestIsValidMenuRejectsUnknown(t *testing.T) {
	for _, key := range []string{"", "/unknown", "overview", "/admin/secret"} {
		if IsValidMenu(key) {
			t.Fatalf("非目录菜单 %q 不应通过校验", key)
		}
	}
}

// TestSuperAdminOnlyMenusAreNotAuthorizable 防止有人把超管专属菜单加回授权目录。
//
// 「公司管理」只有超管能进，而超管始终全量、不靠角色授权。
// 把它放进目录只会让平台管理员在授权树里看到一个自己无权进入、
// 且勾了也不会生效的菜单项（真实收到过这个反馈）。
func TestSuperAdminOnlyMenusAreNotAuthorizable(t *testing.T) {
	const superAdminOnly = "/admin/tenants"
	if IsValidMenu(superAdminOnly) {
		t.Fatalf("%s 是超管专属菜单，不应出现在可授权目录里", superAdminOnly)
	}
	for _, menu := range Menus {
		if menu.Key == superAdminOnly {
			t.Fatalf("目录不应包含 %s", superAdminOnly)
		}
	}
}

// id_test.go · 角色接口的 id 语义。
// 职责：钉住"非 UUID 的路径参数按 404 处理"，而不是让它变成 500。
// 输入：各种形态的 id 字符串；输出：是否被判定为合法 UUID。
// 边界：纯契约测试，不连数据库。
package role

import (
	"testing"

	"dandelion/server/internal/contract"
)

// TestRoleIDMustBeUUID 防止把角色 key 当 id 传入时炸成 500。
//
// 历史背景：接口曾用 key 定位角色，改为 id 后，旧链接（/roles/project-owner）
// 会把非 UUID 字符串绑到 uuid 列，Postgres 抛类型错误 → 500。
// 现在前置校验成 404（"角色不存在"），语义正确也更好排查。
func TestRoleIDMustBeUUID(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		{"标准 UUID", "1a5d2308-400c-4896-87b8-821f9c035c3b", true},
		{"大写 UUID", "1A5D2308-400C-4896-87B8-821F9C035C3B", true},
		{"角色 key（旧链接）", "project-owner", false},
		{"空串", "", false},
		{"缺段", "1a5d2308-400c-4896", false},
		{"多余字符", "1a5d2308-400c-4896-87b8-821f9c035c3b-x", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := contract.IsValidUUID(test.id); got != test.valid {
				t.Fatalf("IsValidUUID(%q) = %v，期望 %v", test.id, got, test.valid)
			}
		})
	}
}

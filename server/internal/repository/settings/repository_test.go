// repository_test.go · 验证 SET/WHERE 拼接时的占位符偏移。
// rebasePlaceholders 是手写实现，一旦偏移算错，UPDATE 会把条件套到错误的参数上
// （轻则改 0 行，重则改错公司的那一行），而这类错误单元测试比端到端更容易定位。
package settings

import "testing"

func TestRebasePlaceholders(t *testing.T) {
	tests := []struct {
		name   string
		where  string
		offset int
		want   string
	}{
		{
			name:   "零偏移原样返回",
			where:  `tenant_id = $1::uuid`,
			offset: 0,
			want:   `tenant_id = $1::uuid`,
		},
		{
			name:   "整体后移四位",
			where:  `tenant_id = $1::uuid`,
			offset: 4,
			want:   `tenant_id = $5::uuid`,
		},
		{
			name:   "无参数的条件不受影响",
			where:  `tenant_id IS NULL`,
			offset: 4,
			want:   `tenant_id IS NULL`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rebasePlaceholders(tt.where, tt.offset); got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}

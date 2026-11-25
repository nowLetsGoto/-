// 职责：验证飞书配置校验、通用协议兼容和下载入口必选规则。
// 输入：不同通知类型、机器人地址与字段组合；输出：归一化配置或校验错误。
// 边界：仅测试业务校验，不访问数据库或发送消息。
package settings

import (
	"testing"

	"dandelion/server/internal/contract"
)

func TestNormalizeWebhookProvider(t *testing.T) {
	for _, tc := range []struct {
		name, provider, target string
		fields                 []string
		disabled, wantError    bool
	}{
		{"存量通用配置", "", "https://hooks.example/receiver", []string{"appName"}, false, false},
		{"飞书配置", "feishu", "https://open.feishu.cn/open-apis/bot/v2/hook/test", []string{"downloadPageUrl"}, false, false},
		{"未知类型", "other", "https://hooks.example", []string{"appName"}, false, true},
		{"缺少下载地址", "feishu", "https://open.feishu.cn/open-apis/bot/v2/hook/test", []string{"appName"}, false, true},
		{"禁止HTTP机器人", "feishu", "http://open.feishu.cn/open-apis/bot/v2/hook/test", []string{"downloadPageUrl"}, false, true},
		{"禁止伪造域名", "feishu", "https://open.feishu.cn.untrusted.example/open-apis/bot/v2/hook/test", []string{"downloadPageUrl"}, false, true},
		{"禁止认证信息", "feishu", "https://name:pass@open.feishu.cn/open-apis/bot/v2/hook/test", []string{"downloadPageUrl"}, false, true},
		{"机器人ID为空", "feishu", "https://open.feishu.cn/open-apis/bot/v2/hook/", []string{"downloadPageUrl"}, false, true},
		{"错误飞书路径", "feishu", "https://open.feishu.cn/document/test", []string{"downloadPageUrl"}, false, true},
		{"停用可清空地址", "feishu", "", []string{"downloadPageUrl"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := normalize(contract.WebhookConfig{Provider: tc.provider, Enabled: !tc.disabled, URL: tc.target, Fields: tc.fields})
			if (err != nil) != tc.wantError {
				t.Fatalf("配置校验结果错误：cfg=%+v err=%v", cfg, err)
			}
			if !tc.wantError && tc.provider == "" && cfg.Provider != "generic" {
				t.Fatal("存量配置未归一化到通用协议")
			}
		})
	}
}

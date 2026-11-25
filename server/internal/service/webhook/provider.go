// 职责：校验通知协议与接收地址是否匹配，防止飞书地址误用通用 JSON 协议。
// 输入：通知类型与规范化的接收地址；输出：可展示的配置错误或 nil。
// 边界：纯校验，不读配置、不写库、不发送消息、不包含机器人凭证。
package webhook

import (
	"net/url"
	"strings"

	"dandelion/server/internal/contract"
)

// ValidateProviderURL 在保存及投递前共用，存量错误配置也不能误报投递成功。
func ValidateProviderURL(provider, target string) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return &contract.ValidationError{Message: "Webhook 地址必须是 http(s) 绝对地址"}
	}
	const prefix = "/open-apis/bot/v2/hook/"
	if provider != contract.WebhookProviderFeishu {
		if strings.EqualFold(u.Hostname(), "open.feishu.cn") && strings.HasPrefix(u.Path, prefix) {
			return &contract.ValidationError{Message: "接收地址是飞书机器人，请将通知类型切换为「飞书机器人」并保存"}
		}
		return nil
	}
	if u.Scheme != "https" || u.Host != "open.feishu.cn" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, prefix) ||
		strings.TrimPrefix(u.Path, prefix) == "" || strings.Contains(strings.TrimPrefix(u.Path, prefix), "/") {
		return &contract.ValidationError{Message: "请填写飞书自定义机器人的 HTTPS Webhook 地址"}
	}
	return nil
}

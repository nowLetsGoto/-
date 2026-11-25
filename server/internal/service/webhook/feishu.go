// 职责：将已发布包转换为飞书 2.0 消息卡片，生成机器人签名并解释投递回执。
// 输入：现有 Webhook 配置、带分享地址的包、测试标记与时间；输出：飞书消息或可读错误。
// 边界：不访问数据库、不生成下载 URL、不发送请求；测试消息不提供下载入口。
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dandelion/server/internal/contract"
	settingsrepo "dandelion/server/internal/repository/settings"
)

type feishuPayload struct {
	MsgType   string         `json:"msg_type"`
	Card      map[string]any `json:"card"`
	Timestamp string         `json:"timestamp,omitempty"`
	Sign      string         `json:"sign,omitempty"`
}

func feishuMessage(cfg settingsrepo.Config, build contract.Build, testing bool, now time.Time) (feishuPayload, error) {
	title := "应用更新提醒"
	if testing {
		title = "应用更新提醒 · 测试消息"
	} else {
		page, err := url.Parse(build.DownloadPageURL)
		if err != nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" || page.Query().Get("shareToken") == "" {
			return feishuPayload{}, fmt.Errorf("安装包缺少有效分享地址，无法发送飞书下载卡片")
		}
	}

	var content strings.Builder
	if !testing {
		content.WriteString("您的应用上传了新版本。\n")
	}
	content.WriteString(feishuBuildSummary(build, cfg.Fields))
	if testing {
		content.WriteString("\n这是一条通道测试消息。上传真实安装包后，通知将显示“打开下载页”按钮。")
	}
	elements := []any{map[string]any{"tag": "markdown", "content": content.String()}}
	if !testing {
		elements = append(elements, feishuLink("打开下载页", build.DownloadPageURL, "primary"))
		if _, ok := pickFields(build, cfg.Fields)[contract.FieldDownloadFileURL]; ok && build.DownloadFileURL != "" {
			elements = append(elements, feishuLink("下载安装包", build.DownloadFileURL, "default"))
		}
	}
	message := feishuPayload{
		MsgType: "interactive",
		Card: map[string]any{
			"schema": "2.0",
			"header": map[string]any{
				"title":    map[string]string{"tag": "plain_text", "content": title},
				"template": "blue",
			},
			"body": map[string]any{"elements": elements},
		},
	}
	if cfg.Secret != "" {
		message.Timestamp = strconv.FormatInt(now.Unix(), 10)
		mac := hmac.New(sha256.New, []byte(message.Timestamp+"\n"+cfg.Secret))
		message.Sign = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	return message, nil
}

// feishuBuildSummary 仅格式化已勾选字段；版本与构建号合并时仍分别遵守字段选择。
func feishuBuildSummary(build contract.Build, fields []string) string {
	selected := pickFields(build, fields)
	handled := make(map[string]bool)
	var content strings.Builder
	write := func(field, label, value string) {
		handled[field] = true
		if _, ok := selected[field]; !ok || value == "" {
			return
		}
		fmt.Fprintf(&content, "**%s：** %s\n", label, escapeCardText(value))
	}
	write(contract.FieldAppName, "应用名称", build.AppName)
	platform := build.Platform
	if info, ok := contract.PlatformInfos[platform]; ok {
		platform = info.Label
	}
	write(contract.FieldPlatform, "应用类型", platform)
	version := build.Version
	_, includeBuild := selected[contract.FieldBuildNumber]
	_, includeVersion := selected[contract.FieldVersion]
	if includeVersion && includeBuild && version != "" && build.BuildNumber != "" {
		version += "(Build " + build.BuildNumber + ")"
		handled[contract.FieldBuildNumber] = true
	}
	write(contract.FieldVersion, "版本信息", version)
	if !handled[contract.FieldBuildNumber] {
		write(contract.FieldBuildNumber, "构建号", build.BuildNumber)
	}
	write(contract.FieldFileSize, "应用大小", feishuFileSize(build.FileSize))
	write(contract.FieldCreatedAt, "更新时间", feishuPublishedAt(build.CreatedAt))
	changelog := build.Changelog
	if strings.TrimSpace(changelog) == "" {
		changelog = "发布方暂未填写更新说明。"
	}
	write(contract.FieldChangelog, "更新内容", changelog)
	for _, field := range fields {
		if handled[field] || field == contract.FieldDownloadPageURL || field == contract.FieldDownloadFileURL {
			continue
		}
		if value, ok := selected[field]; ok {
			write(field, contract.WebhookFieldLabels[field], fmt.Sprint(value))
		}
	}
	return content.String()
}

// 字节单位与下载页保持一致：按 1024 进位，大于等于 10 的数值保留一位小数。
func feishuFileSize(bytes int64) string {
	if bytes < 0 {
		return "—"
	}
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value, index := float64(bytes)/1024, 0
	for value >= 1024 && index < len(units)-1 {
		value /= 1024
		index++
	}
	precision := 2
	if value >= 10 {
		precision = 1
	}
	return fmt.Sprintf("%.*f %s", precision, value, units[index])
}

// 通知不继承服务器本地时区；更新时间是版本上传发布时间，统一使用北京时间。
func feishuPublishedAt(value string) string {
	published, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return published.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04:05")
}

func feishuLink(label, target, kind string) map[string]any {
	return map[string]any{
		"tag": "button", "type": kind,
		"text":      map[string]string{"tag": "plain_text", "content": label},
		"behaviors": []any{map[string]string{"type": "open_url", "default_url": target}},
	}
}

// 限制长更新说明，并转义用户输入，避免通知里的 Markdown 改变链接或触发 @ 标签。
func escapeCardText(text string) string {
	runes := []rune(text)
	if len(runes) > 1500 {
		text = string(runes[:1500]) + "…（完整内容见下载页）"
	}
	return strings.NewReplacer(
		"\\", "\\\\", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]",
		"`", "\\`", "<", "&lt;", ">", "&gt;",
	).Replace(text)
}

func checkFeishuResponse(reader io.Reader) error {
	// 指针区分 code=0 与缺少 code；空 JSON 或 HTML 不能误报成功。
	var result struct {
		Code *int `json:"code"`
	}
	body, err := io.ReadAll(io.LimitReader(reader, (4<<10)+1))
	if err != nil || len(body) > 4<<10 || json.Unmarshal(body, &result) != nil || result.Code == nil {
		return fmt.Errorf("飞书返回了无效回执，请检查机器人地址")
	}
	if *result.Code == 0 {
		return nil
	}
	hint := "请检查机器人配置"
	switch *result.Code {
	case 19021:
		hint = "请检查签名密钥与服务器时间"
	case 19022:
		hint = "请检查机器人 IP 白名单"
	case 19024:
		hint = "请检查机器人关键词，可配置 应用更新提醒"
	case 11232:
		hint = "机器人发送频率受限，请稍后重试"
	case 9499:
		hint = "请检查飞书卡片格式"
	}
	return fmt.Errorf("飞书拒绝消息（code=%d），%s", *result.Code, hint)
}

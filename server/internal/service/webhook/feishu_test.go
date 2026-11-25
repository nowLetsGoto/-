// 职责：验证飞书协议、签名、分享按钮、错误回执以及异步通知的公司边界。
// 输入：内存配置与模拟 HTTP 响应；输出：消息结构、投递结果和失败隔离断言。
// 边界：不连接数据库、不访问飞书、不发送真实消息；全部请求由内存 Transport 接收。
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"dandelion/server/internal/contract"
	settingsrepo "dandelion/server/internal/repository/settings"
)

type memoryTransport func(*http.Request) (*http.Response, error)

func (f memoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func publishedBuild() contract.Build {
	b := sampleBuild()
	b.DownloadPageURL = "https://distribution.example/d/" + b.ID + "?shareToken=s1.team.share.signature"
	b.DownloadFileURL = "https://distribution.example/api/v1/public/builds/" + b.ID + "/file?shareToken=s1.team.share.signature"
	b.Changelog = "修复登录；<at id=all></at> [其它链接](https://untrusted.example)"
	return b
}

func feishuConfig() settingsrepo.Config {
	return settingsrepo.Config{Webhook: settingsrepo.Webhook{
		Provider: contract.WebhookProviderFeishu, Enabled: true,
		URL: "https://open.feishu.cn/open-apis/bot/v2/hook/test-only", Secret: "demo",
		Fields: []string{contract.FieldAppName, contract.FieldVersion, contract.FieldChangelog, contract.FieldDownloadPageURL},
	}, TenantID: "company-a", APIKey: "must-never-appear"}
}

func TestFeishuCardCarriesExactShareURLAndOfficialSignature(t *testing.T) {
	cfg, build := feishuConfig(), publishedBuild()
	message, err := feishuMessage(cfg, build, false, time.Unix(1599360473, 0))
	if err != nil {
		t.Fatal(err)
	}
	if message.MsgType != "interactive" || message.Timestamp != "1599360473" || message.Sign != "l1N0gAcBjdwBvGm1xMjOF0XSyaLRpR7tuO5dHfhAYc8=" {
		t.Fatalf("飞书消息类型或官方签名算法不匹配：%+v", message)
	}
	elements := message.Card["body"].(map[string]any)["elements"].([]any)
	button := elements[1].(map[string]any)
	target := button["behaviors"].([]any)[0].(map[string]string)["default_url"]
	if target != build.DownloadPageURL || button["text"].(map[string]string)["content"] != "打开下载页" {
		t.Fatalf("按钮必须完整保留服务端分享 URL：%v", button)
	}
	content := elements[0].(map[string]any)["content"].(string)
	if strings.Contains(content, "<at") || strings.Contains(content, "[其它链接]") || strings.Contains(content, "环境") {
		t.Fatalf("用户输入未转义或泄露未勾选字段：%s", content)
	}
	body, _ := json.Marshal(message)
	for _, private := range []string{cfg.APIKey, cfg.Secret, "relativePath", "ownerUserId"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("通知包含不应发送的字段：%s", private)
		}
	}
}

func TestFeishuTestCardDoesNotInventDownloadLink(t *testing.T) {
	cfg := feishuConfig()
	cfg.Secret = ""
	message, err := feishuMessage(cfg, sampleBuild(), true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(message)
	for _, absent := range []string{"open_url", "timestamp", "sign", "https://dandelion.example.com"} {
		if strings.Contains(string(body), absent) {
			t.Fatalf("测试卡片出现虚假下载入口或多余签名字段：%s", body)
		}
	}
	if !strings.Contains(string(body), "测试消息") {
		t.Fatal("测试卡片未明确标识")
	}
	if _, err := feishuMessage(cfg, sampleBuild(), false, time.Now()); err == nil {
		t.Fatal("正式通知不应接受缺失分享授权的地址")
	}
}

func TestFeishuDownloadOnlyCardHasNonemptyBody(t *testing.T) {
	cfg := feishuConfig()
	cfg.Fields = []string{contract.FieldDownloadPageURL}
	message, err := feishuMessage(cfg, publishedBuild(), false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	elements := message.Card["body"].(map[string]any)["elements"].([]any)
	if elements[0].(map[string]any)["content"] == "" || len(elements) != 2 {
		t.Fatal("仅勾选下载地址时也必须生成有效卡片")
	}
}

func TestFeishuUpdateReminderIncludesSelectedReleaseDetails(t *testing.T) {
	build := publishedBuild()
	build.AppName, build.Platform = "景田", "android"
	build.Version, build.BuildNumber = "26.11.103", "2076"
	build.FileSize, build.CreatedAt = 178362777, "2026-11-24T08:37:43.123Z"
	build.Changelog = "test环境，真机，所有模块，构建人：zhangjiawei"
	cfg := feishuConfig()
	cfg.Fields = []string{"appName", "platform", "version", "buildNumber", "fileSize", "createdAt", "changelog", "downloadPageUrl"}
	message, err := feishuMessage(cfg, build, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	elements := message.Card["body"].(map[string]any)["elements"].([]any)
	content := elements[0].(map[string]any)["content"].(string)
	for _, text := range []string{"应用名称：** 景田", "应用类型：** Android", "版本信息：** 26.11.103(Build 2076)", "应用大小：** 170.1 MB", "更新时间：** 2026-11-24 16:37:43", "更新内容：** " + build.Changelog} {
		if !strings.Contains(content, text) {
			t.Errorf("消息缺少 %q：%s", text, content)
		}
	}
	for _, tc := range []struct {
		field, present, absent string
	}{
		{"version", "26.11.103", "2076"},
		{"buildNumber", "2076", "26.11.103"},
	} {
		content := feishuBuildSummary(build, []string{tc.field})
		if !strings.Contains(content, tc.present) || strings.Contains(content, tc.absent) {
			t.Fatalf("版本合并未遵守勾选字段：%s", content)
		}
	}
	build.Changelog = ""
	if content := feishuBuildSummary(build, []string{"changelog"}); !strings.Contains(content, "发布方暂未填写更新说明") {
		t.Fatal("空更新内容缺少兜底")
	}
}

func TestFeishuReleaseSizeAndTimeFormatting(t *testing.T) {
	for bytes, want := range map[int64]string{-1: "—", 0: "0 B", 1023: "1023 B", 1024: "1.00 KB", 10485760: "10.0 MB", 1073741824: "1.00 GB"} {
		if got := feishuFileSize(bytes); got != want {
			t.Errorf("文件大小 %d = %q，预期 %q", bytes, got, want)
		}
	}
	if got := feishuPublishedAt("2026-11-24T00:37:43-08:00"); got != "2026-11-24 16:37:43" {
		t.Errorf("通知时间未统一到北京时间：%s", got)
	}
	if got := feishuPublishedAt("invalid"); got != "invalid" {
		t.Errorf("无法解析的时间被伪造：%s", got)
	}
}

func TestWebhookPostValidatesFeishuReceiptAndKeepsGenericProtocol(t *testing.T) {
	cases := []struct {
		name, provider, body string
		status               int
		wantError            string
	}{
		{"飞书成功", "feishu", `{"code":0,"msg":"success"}`, 200, ""},
		{"签名错误仍为HTTP200", "feishu", `{"code":19021}`, 200, "签名密钥"},
		{"关键词错误", "feishu", `{"code":19024}`, 200, "关键词"},
		{"业务拒绝", "feishu", `{"code":9499}`, 200, "卡片格式"},
		{"缺少业务状态码", "feishu", `{}`, 200, "无效回执"},
		{"非JSON回执", "feishu", `<html>ok</html>`, 200, "无效回执"},
		{"超大回执", "feishu", strings.Repeat(" ", 5000), 200, "无效回执"},
		{"HTTP失败", "feishu", `{"code":0}`, 503, "HTTP 503"},
		{"拒绝重定向", "feishu", "", 302, "HTTP 302"},
		{"通用无需JSON回执", "generic", "accepted", 202, ""},
		{"存量通用类型", "", "", 204, ""},
		{"通用HTTP失败", "generic", "failure", 500, "HTTP 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, build := feishuConfig(), publishedBuild()
			cfg.Provider = tc.provider
			if tc.provider != contract.WebhookProviderFeishu {
				cfg.URL = "https://hooks.example/receiver"
			}
			svc := &Service{client: &http.Client{Transport: memoryTransport(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				if req.Method != "POST" || req.URL.String() != cfg.URL || req.Header.Get("Content-Type") != "application/json" {
					t.Fatal("请求方法、地址或内容类型有误")
				}
				if tc.provider == "feishu" {
					if req.Header.Get(signatureHeader) != "" || !strings.Contains(string(body), `"msg_type":"interactive"`) {
						t.Fatal("飞书协议混入通用签名头")
					}
				} else {
					if req.Header.Get(signatureHeader) != "sha256="+sign(cfg.Secret, body) {
						t.Fatal("通用 Webhook 的签名兼容性被破坏")
					}
					var envelope payload
					if err := json.Unmarshal(body, &envelope); err != nil || envelope.Event != "build.created" || envelope.TenantID != cfg.TenantID || envelope.Build["downloadPageUrl"] != build.DownloadPageURL {
						t.Fatal("通用 Webhook 的信封或分享地址被改变")
					}
				}
				return response(tc.status, tc.body), nil
			})}}
			status, err := svc.post(context.Background(), cfg, build, false)
			if status != tc.status || (tc.wantError == "" && err != nil) || (tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError))) {
				t.Fatalf("status=%d err=%v，预期 status=%d error=%q", status, err, tc.status, tc.wantError)
			}
		})
	}
}

type memoryConfig struct {
	cfg     settingsrepo.Config
	loaded  chan string
	unblock chan struct{}
}

func (m memoryConfig) EnsureRow(context.Context, string) (settingsrepo.Config, error) {
	return m.cfg, nil
}
func (m memoryConfig) LoadForDelivery(_ context.Context, tenantID string) (settingsrepo.Config, error) {
	m.loaded <- tenantID
	<-m.unblock
	return m.cfg, nil
}

func TestNotifyBuildRemainsAsyncAndUsesBuildCompany(t *testing.T) {
	cfg := memoryConfig{cfg: feishuConfig(), loaded: make(chan string, 1), unblock: make(chan struct{})}
	sent := make(chan error, 1)
	svc := &Service{repo: cfg, client: &http.Client{Transport: memoryTransport(func(req *http.Request) (*http.Response, error) {
		sent <- req.Context().Err()
		return response(200, `{"code":19021}`), nil
	})}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	returned := make(chan struct{})
	go func() { svc.NotifyBuild(ctx, "company-a", publishedBuild()); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("配置查询阻塞了上传响应")
	}
	if company := <-cfg.loaded; company != "company-a" {
		t.Fatalf("通知使用了错误公司：%s", company)
	}
	close(cfg.unblock)
	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("上传连接结束不应取消已发布通知：%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("没有发送异步通知")
	}
}

func TestTestDeliveryReportsBusinessFailureWithHTTPStatus(t *testing.T) {
	svc := &Service{repo: memoryConfig{cfg: feishuConfig()}, client: &http.Client{Transport: memoryTransport(func(*http.Request) (*http.Response, error) {
		return response(200, `{"code":19021}`), nil
	})}}
	result, err := svc.Test(context.Background(), contract.Scope{TenantID: "company-a"})
	if err != nil || result.Delivered || result.StatusCode != 200 || !strings.Contains(result.Message, "签名密钥") {
		t.Fatalf("飞书拒绝消息不应显示测试成功：%+v err=%v", result, err)
	}
	if _, err := svc.Test(context.Background(), contract.Scope{}); err != contract.ErrForbidden {
		t.Fatal(fmt.Sprintf("没有公司范围应拒绝测试：%v", err))
	}
}

func TestMisconfiguredFeishuDoesNotSendOrReportSuccess(t *testing.T) {
	for _, provider := range []string{"", contract.WebhookProviderGeneric} {
		t.Run("provider="+provider, func(t *testing.T) {
			cfg := feishuConfig()
			cfg.Provider = provider
			svc := &Service{repo: memoryConfig{cfg: cfg}, client: &http.Client{Transport: memoryTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("类型不匹配时不应向飞书发送通用 JSON")
				return nil, nil
			})}}
			result, err := svc.Test(context.Background(), contract.Scope{TenantID: "company-a"})
			if err != nil || result.Delivered || result.StatusCode != 0 || !strings.Contains(result.Message, "通知类型") {
				t.Fatalf("错误协议不应显示投递成功：%+v err=%v", result, err)
			}
		})
	}
}

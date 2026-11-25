// Package webhook · 发布成功后的 Webhook 投递。
// 职责：按系统设置挑字段、组装 payload、可选 HMAC 签名、POST 到目标地址；并提供「测试投递」。
// 输入：repository/settings 的配置、contract.Build。
// 输出：投递结果（测试用）或一条投递日志（正式推送用）。
// 边界：不写库、不重试、不在失败时影响上传主流程——Webhook 是旁路，不是事务的一部分。
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"dandelion/server/internal/contract"
	settingsrepo "dandelion/server/internal/repository/settings"
)

const (
	// deliverTimeout 单次投递上限：够慢网络完成握手，又不至于堆积连接。
	deliverTimeout = 10 * time.Second
	// eventBuildCreated 目前唯一的事件类型；将来加事件时在此扩展枚举。
	eventBuildCreated = "build.created"

	eventHeader     = "X-Dandelion-Event"
	signatureHeader = "X-Dandelion-Signature"
	userAgent       = "Dandelion-Webhook/1"
)

// payload 是投递到目标地址的请求体结构。
// TenantID 让接收端能区分"这是哪家公司的包"——同一套接收服务可以服务多家公司，
// 靠这个字段分流；它在字段勾选之外恒定存在，属于信封信息而不是业务字段。
type payload struct {
	Event    string         `json:"event"`
	SentAt   string         `json:"sentAt"`
	TenantID string         `json:"tenantId"`
	Build    map[string]any `json:"build"`
}

// Service 读取配置并完成投递，由 main.go 注入。
type Service struct {
	repo   configurationStore
	client *http.Client
}

type configurationStore interface {
	LoadForDelivery(context.Context, string) (settingsrepo.Config, error)
	EnsureRow(context.Context, string) (settingsrepo.Config, error)
}

func NewService(repo *settingsrepo.Repository) *Service {
	return &Service{repo: repo, client: &http.Client{
		Timeout: deliverTimeout,
		// Webhook 是凭证地址，禁止重定向把消息、签名或分享凭证发送给其它目标。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// NotifyBuild 在发布成功后异步投递。
// 有意丢弃请求 ctx：上传响应不该等第三方，客户端断开也不该取消已经排队的通知。
//
// tenantID 是包所属公司：配置优先用本公司的，没配则回落平台兜底行。
// 这样两家公司的回调地址互不相干，也不会因为新公司还没配 Webhook 就不通知。
func (s *Service) NotifyBuild(_ context.Context, tenantID string, build contract.Build) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
		defer cancel()

		cfg, err := s.repo.LoadForDelivery(ctx, tenantID)
		if err != nil {
			log.Printf("webhook 读取配置失败 build=%s tenant=%s err=%v", build.ID, tenantID, err)
			return
		}
		if !cfg.Enabled || cfg.URL == "" {
			return
		}
		status, err := s.post(ctx, cfg, build, false)
		if err != nil {
			// 失败只记日志：Webhook 挂了不能让发布失败，也不阻塞后续发布。
			log.Printf("webhook 投递失败 provider=%s build=%s tenant=%s fallback=%v status=%d err=%v",
				cfg.Provider, build.ID, tenantID, cfg.FromFallback, status, err)
			return
		}
		log.Printf("webhook 投递成功 provider=%s build=%s tenant=%s fallback=%v status=%d",
			cfg.Provider, build.ID, tenantID, cfg.FromFallback, status)
	}()
}

// Test 同步投递一条示例消息，并把目标状态码带回设置页。
// 目标不可达不算接口错误：返回 200 + delivered=false，比抛 500 更利于排查。
//
// 用当前 scope 对应公司的配置（严格，不回落兜底）：用户在"我这家的设置页"点测试，
// 想验证的是我填的这个地址通不通；回落到兜底地址会测出一个不是他填的结果。
func (s *Service) Test(ctx context.Context, scope contract.Scope) (contract.WebhookTestResult, error) {
	tenantID := ""
	if !scope.All {
		if scope.TenantID == "" {
			return contract.WebhookTestResult{}, contract.ErrForbidden
		}
		tenantID = scope.TenantID
	}
	cfg, err := s.repo.EnsureRow(ctx, tenantID)
	if err != nil {
		return contract.WebhookTestResult{}, err
	}
	if !cfg.Enabled || cfg.URL == "" {
		return contract.WebhookTestResult{}, &contract.ValidationError{Message: "请先启用 Webhook 并填写地址"}
	}

	status, err := s.post(ctx, cfg, sampleBuild(), true)
	if err != nil {
		return contract.WebhookTestResult{
			Delivered: false, StatusCode: status, Message: "投递失败：" + err.Error(),
		}, nil
	}
	return contract.WebhookTestResult{Delivered: true, StatusCode: status, Message: "测试消息投递成功；请上传真实安装包验证下载按钮"}, nil
}

// post 组装并发送一次请求；返回目标状态码（未收到响应时为 0）。
func (s *Service) post(ctx context.Context, cfg settingsrepo.Config, build contract.Build, testing bool) (int, error) {
	if err := ValidateProviderURL(cfg.Provider, cfg.URL); err != nil {
		return 0, err
	}
	// tenantId 取「配置所属公司」而不是包的公司：回落到兜底行时，
	// 接收端看到的是平台兜底配置发出的通知，标成具体公司反而误导。
	var message any = payload{
		Event:    eventBuildCreated,
		SentAt:   time.Now().UTC().Format(time.RFC3339),
		TenantID: cfg.TenantID,
		Build:    pickFields(build, cfg.Fields),
	}
	if cfg.Provider == contract.WebhookProviderFeishu {
		card, err := feishuMessage(cfg, build, testing, time.Now())
		if err != nil {
			return 0, err
		}
		message = card
	}
	body, err := json.Marshal(message)
	if err != nil {
		return 0, fmt.Errorf("组装请求体失败: %w", err)
	}
	if cfg.Provider == contract.WebhookProviderFeishu && len(body) > 20<<10 {
		return 0, fmt.Errorf("飞书卡片超过 20 KB，请减少推送字段")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("Webhook 地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set(eventHeader, eventBuildCreated)
	if cfg.Secret != "" && cfg.Provider != contract.WebhookProviderFeishu {
		req.Header.Set(signatureHeader, "sha256="+sign(cfg.Secret, body))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		// net/http 的错误通常带完整请求 URL，飞书地址中含机器人凭证，必须去掉。
		if requestErr, ok := err.(*url.Error); ok {
			err = requestErr.Err
		}
		return 0, err
	}
	// 响应体只用于排空连接，最多读 4KB，避免目标返回巨量内容拖住调用方。
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("目标返回 HTTP %d，请检查机器人地址或接收端", resp.StatusCode)
	}
	if cfg.Provider == contract.WebhookProviderFeishu {
		if err := checkFeishuResponse(resp.Body); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// sign 计算 HMAC-SHA256 签名，接收端用同一 secret 校验即可确认来源。
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// pickFields 按配置挑选字段：只推送用户勾选的内容，避免把内部路径等无关信息外泄。
func pickFields(build contract.Build, fields []string) map[string]any {
	selected := make(map[string]any, len(fields))
	for _, field := range fields {
		switch field {
		case contract.FieldID:
			selected[field] = build.ID
		case contract.FieldAppName:
			selected[field] = build.AppName
		case contract.FieldPackageID:
			selected[field] = build.PackageID
		case contract.FieldVersion:
			selected[field] = build.Version
		case contract.FieldBuildNumber:
			selected[field] = build.BuildNumber
		case contract.FieldEnvironment:
			selected[field] = build.Environment
		case contract.FieldPlatform:
			selected[field] = build.Platform
		case contract.FieldChangelog:
			selected[field] = build.Changelog
		case contract.FieldFileSize:
			selected[field] = build.FileSize
		case contract.FieldOriginalFileName:
			selected[field] = build.OriginalFileName
		case contract.FieldDownloadPageURL:
			selected[field] = build.DownloadPageURL
		case contract.FieldDownloadFileURL:
			selected[field] = build.DownloadFileURL
		case contract.FieldDownloadCount:
			selected[field] = build.DownloadCount
		case contract.FieldCreatedAt:
			selected[field] = build.CreatedAt
		}
		// 未知字段由 service/settings 的校验挡住，这里静默跳过即可。
	}
	return selected
}

// sampleBuild 是「测试投递」用的数据，不伪造可下载包或分享凭证。
func sampleBuild() contract.Build {
	const id = "11111111-1111-4111-8111-111111111111"
	pagePath := "/d/" + id
	return contract.Build{
		ID:               id,
		Environment:      contract.EnvPrd,
		Platform:         contract.PlatformAndroid,
		AppName:          "示例应用",
		PackageID:        "com.example.demo",
		Version:          "1.0.0",
		BuildNumber:      "1",
		Changelog:        "这是一条 Webhook 测试消息",
		OriginalFileName: "demo-1.0.0.apk",
		FileSize:         12345678,
		FileExt:          "apk",
		MetaSource:       contract.MetaParsed,
		DownloadPagePath: pagePath,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

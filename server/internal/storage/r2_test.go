// r2_test.go · R2 地址生成的行为锁。
//
// 职责：验证公开 CDN 直链与私有签名 URL 两条路径的正确性，尤其是
//
//	"自定义域名不能被当成签名 URL 的 host 替换目标" 这条易错规则。
//
// 输入：R2Config（不触发任何真实网络请求）。
// 输出：断言 URL 拼接结果。
package storage

import (
	"context"
	"strings"
	"testing"
)

func TestPublicURL(t *testing.T) {
	cfg := R2Config{
		AccountID:     "acct",
		AccessKeyID:   "ak",
		SecretKey:     "sk",
		Bucket:        "b",
		PublicBaseURL: "https://download.example.com/base/",
	}
	r := &R2{cfg: cfg}

	got, err := r.PublicURL("direct/abc-123.apk")
	if err != nil {
		t.Fatalf("PublicURL 失败: %v", err)
	}
	want := "https://download.example.com/base/direct/abc-123.apk"
	if got != want {
		t.Fatalf("PublicURL = %q, 期望 %q", got, want)
	}
	// 公开直链绝不能带任何签名参数——签了反而会被 CDN 拒绝。
	if strings.Contains(got, "X-Amz-") {
		t.Fatalf("公开直链不应带签名参数: %s", got)
	}
}

func TestPublicURLDisabled(t *testing.T) {
	r := &R2{cfg: R2Config{AccountID: "acct", AccessKeyID: "ak", SecretKey: "sk", Bucket: "b"}}
	if _, err := r.PublicURL("a.apk"); err != ErrR2Disabled {
		t.Fatalf("未配自定义域名应返回 ErrR2Disabled, got %v", err)
	}
}

func TestDownloadURLUsesPublicWhenConfigured(t *testing.T) {
	r := &R2{cfg: R2Config{
		AccountID:     "acct",
		AccessKeyID:   "ak",
		SecretKey:     "sk",
		Bucket:        "b",
		PublicBaseURL: "https://download.example.com",
	}}
	got, err := r.DownloadURL(context.Background(), "direct/abc.apk", 0)
	if err != nil {
		t.Fatalf("DownloadURL 失败: %v", err)
	}
	if strings.Contains(got, "r2.cloudflarestorage.com") {
		t.Fatalf("配了自定义域名时不应走 S3 端点: %s", got)
	}
	if !strings.HasPrefix(got, "https://download.example.com/") {
		t.Fatalf("DownloadURL 未使用自定义域名: %s", got)
	}
}

func TestIsPublicCDN(t *testing.T) {
	if (&R2{cfg: R2Config{PublicBaseURL: "https://x.com"}}).IsPublicCDN() != true {
		t.Fatal("配了域名应返回 true")
	}
	if (&R2{cfg: R2Config{}}).IsPublicCDN() != false {
		t.Fatal("未配域名应返回 false")
	}
}

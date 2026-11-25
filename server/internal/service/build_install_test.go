// 职责：验证 iOS 安装地址、清单元数据与异常包的失败行为。
// 输入：临时 IPA、固定分享凭证和 HTTPS/HTTP 配置；输出：可解析的 Apple 清单及授权边界断言。
// 边界：不访问外网或数据库、不安装 App；真实签名及设备资格仍需真机验证。
package service

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"howett.net/plist"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/storage"
)

func TestIOSInstallURLUsesHTTPSAndTheCurrentShare(t *testing.T) {
	for _, tc := range []struct {
		name, base, platform, token string
		wantInstall                 bool
	}{
		{"HTTPS iOS", "https://downloads.example/releases/", "ios", "current-share", true},
		{"HTTP iOS", "http://192.168.1.2:5173", "ios", "current-share", false},
		{"Android", "https://downloads.example", "android", "current-share", false},
		{"无分享", "https://downloads.example", "ios", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &BuildService{cfg: config.Config{PublicBaseURL: tc.base}}
			build := contract.Build{ID: "67ebdb8c-81ec-4598-a3d8-dc9f99980754", Platform: tc.platform}
			svc.fillURLs(&build, tc.token)
			if (build.IOSInstallURL != "") != tc.wantInstall {
				t.Fatalf("安装入口签发错误：%q", build.IOSInstallURL)
			}
			if !tc.wantInstall {
				return
			}
			install, err := url.Parse(build.IOSInstallURL)
			if err != nil || install.Scheme != "itms-services" || install.Query().Get("action") != "download-manifest" {
				t.Fatalf("安装协议错误：%v", err)
			}
			manifest, err := url.Parse(install.Query().Get("url"))
			if err != nil || manifest.Scheme != "https" || manifest.Host != "downloads.example" ||
				manifest.Path != "/releases/api/v1/public/builds/"+build.ID+"/manifest.plist" || manifest.Query().Get("shareToken") != tc.token {
				t.Fatalf("清单地址未保留基址或当前凭证：%v err=%v", manifest, err)
			}
		})
	}
}

func testInstallIPA(t *testing.T, info map[string]any, format int) (*BuildService, contract.Build) {
	t.Helper()
	paths, err := storage.NewPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	build := contract.Build{
		ID: "67ebdb8c-81ec-4598-a3d8-dc9f99980754", Environment: "test", Platform: "ios", FileExt: "ipa",
		PackageID: "edited.bundle", BuildNumber: "99999", Version: "edited-version", AppName: "edited-title",
	}
	build.RelativePath = paths.RelativePath("test", "ios", build.ID, "ipa")
	abs := paths.FinalPath("test", "ios", build.ID, "ipa")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	entry, err := archive.Create("Payload/App.app/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	data, err := plist.Marshal(info, format)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &BuildService{cfg: config.Config{PublicBaseURL: "https://downloads.example"}, paths: paths}
	svc.fillURLs(&build, "current-share")
	return svc, build
}

func TestIOSManifestUsesOriginalIPAMetadataAndEscapesXML(t *testing.T) {
	for _, format := range []int{plist.XMLFormat, plist.BinaryFormat} {
		svc, build := testInstallIPA(t, map[string]any{
			"CFBundleIdentifier": "com.example.original", "CFBundleVersion": "1547",
			"CFBundleShortVersionString": "26.11.99", "CFBundleDisplayName": "景田 <测试> & 内部应用",
		}, format)
		data, err := svc.iosManifest(build)
		if err != nil {
			t.Fatal(err)
		}
		var decoded iosManifest
		if _, err := plist.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Apple 清单必须是有效 plist：%v", err)
		}
		if len(decoded.Items) != 1 || len(decoded.Items[0].Assets) != 1 {
			t.Fatalf("清单必须仅绑定当前 IPA：%+v", decoded)
		}
		item := decoded.Items[0]
		if item.Metadata.BundleIdentifier != "com.example.original" || item.Metadata.BundleVersion != "1547" ||
			item.Metadata.Title != "景田 <测试> & 内部应用" || item.Metadata.Kind != "software" {
			t.Fatalf("清单应取原始 IPA 元数据而非展示字段：%+v", item.Metadata)
		}
		if item.Assets[0].Kind != "software-package" || item.Assets[0].URL != build.DownloadFileURL {
			t.Fatalf("清单必须使用同凭证的 HTTPS 文件地址：%+v", item.Assets)
		}
		if !strings.Contains(string(data), "&amp;") || strings.Contains(string(data), "edited.bundle") {
			t.Fatal("XML 未正确转义，或错误使用了展示字段")
		}
	}
}

func TestIOSManifestRejectsUninstallableInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*BuildService, *contract.Build)
	}{
		{"HTTP", func(s *BuildService, b *contract.Build) {
			s.cfg.PublicBaseURL = "http://downloads.example"
			s.fillURLs(b, "current-share")
		}},
		{"非 iOS", func(_ *BuildService, b *contract.Build) { b.Platform = "android" }},
		{"无分享入口", func(_ *BuildService, b *contract.Build) { b.IOSInstallURL = "" }},
		{"原始构建号缺失", func(_ *BuildService, _ *contract.Build) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := map[string]any{"CFBundleIdentifier": "com.example.original", "CFBundleVersion": "1547"}
			if tc.name == "原始构建号缺失" {
				delete(info, "CFBundleVersion")
			}
			svc, build := testInstallIPA(t, info, plist.XMLFormat)
			tc.mutate(svc, &build)
			var validation *contract.ValidationError
			if _, err := svc.iosManifest(build); !errors.As(err, &validation) {
				t.Fatalf("不可安装输入应明确拒绝：%v", err)
			}
		})
	}
}

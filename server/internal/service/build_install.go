// 职责：为已授权的 iOS 包生成 Apple OTA 安装清单。
// 输入：包 ID、独立分享凭证及原始 IPA；输出：包含同凭证 HTTPS 文件地址的 XML plist。
// 边界：先校验分享再读文件；签名有效性与设备安装资格由 iOS 判断；不改包、不写库、不计下载、不接触 HTTP。
package service

import (
	"context"
	"os"
	"strings"

	"howett.net/plist"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/parser"
)

type iosManifest struct {
	Items []iosManifestItem `plist:"items"`
}

type iosManifestItem struct {
	Assets   []iosManifestAsset `plist:"assets"`
	Metadata iosManifestMeta    `plist:"metadata"`
}

type iosManifestAsset struct {
	Kind string `plist:"kind"`
	URL  string `plist:"url"`
}

type iosManifestMeta struct {
	BundleIdentifier string `plist:"bundle-identifier"`
	BundleVersion    string `plist:"bundle-version"`
	Kind             string `plist:"kind"`
	Title            string `plist:"title"`
}

// SharedIOSManifest 与详情、IPA 下载共用分享校验，清单不能绕过撤销或跨包限制。
func (s *BuildService) SharedIOSManifest(ctx context.Context, id, token string) ([]byte, error) {
	build, _, err := s.sharedBuild(ctx, id, token)
	if err != nil {
		return nil, err
	}
	return s.iosManifest(build)
}

// iosManifest 只接收已经授权的包；安装元数据取文件事实，避免可编辑的展示字段影响安装。
func (s *BuildService) iosManifest(build contract.Build) ([]byte, error) {
	if build.Platform != contract.PlatformIOS || build.FileExt != "ipa" {
		return nil, &contract.ValidationError{Message: "在线安装清单仅支持 iOS IPA"}
	}
	if build.IOSInstallURL == "" || !strings.HasPrefix(build.DownloadFileURL, "https://") {
		return nil, &contract.ValidationError{Message: "iOS 在线安装需要将 PUBLIC_BASE_URL 配置为手机可信的 HTTPS 地址"}
	}
	ref, err := s.resolveBuildFile(build)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(ref.AbsPath)
	if err != nil {
		return nil, contract.NotFound(contract.ResourceBuild)
	}
	defer file.Close()
	meta, err := parser.Parse(file, ref.Size, contract.PlatformIOS)
	if err != nil || strings.TrimSpace(meta.PackageID) == "" || strings.TrimSpace(meta.BuildNumber) == "" {
		return nil, &contract.ValidationError{Message: "IPA 中缺少有效的 Bundle ID 或构建号，请检查原始安装包后重新上传"}
	}
	title := meta.AppName
	if strings.TrimSpace(title) == "" {
		title = build.AppName
	}
	return plist.Marshal(iosManifest{Items: []iosManifestItem{{
		Assets: []iosManifestAsset{{Kind: "software-package", URL: build.DownloadFileURL}},
		Metadata: iosManifestMeta{
			BundleIdentifier: meta.PackageID, BundleVersion: meta.BuildNumber,
			Kind: "software", Title: title,
		},
	}}}, plist.XMLFormat)
}

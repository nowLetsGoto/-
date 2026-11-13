// IPA 解析：用标准库 archive/zip 找 Payload/*.app/Info.plist，用 howett.net/plist 解 4 个字段。
// 职责：应用名 / CFBundleIdentifier / CFBundleShortVersionString / CFBundleVersion。
// 输入：IPA 的 io.ReaderAt 与总大小。
// 输出：ParsedMeta 与可降级的 error；字段可能部分为空，由表单兜底。
// 边界：纯 Go，不用 cgo、不调用 unzip 等外部命令；plist 读取 ≤5MB、zip 条目数上限；
//       不落盘、不写库、不解析图标。
package parser

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"howett.net/plist"

	"dandelion/server/internal/contract"
)

func parseIpa(ctx context.Context, r io.ReaderAt, size int64) (contract.ParsedMeta, error) {
	meta := contract.ParsedMeta{}
	if err := checkContext(ctx); err != nil {
		return meta, err
	}

	zr, err := zip.NewReader(r, size)
	if err != nil {
		return meta, fmt.Errorf("IPA 不是合法 zip: %w", err)
	}
	if len(zr.File) > maxZipEntries {
		return meta, fmt.Errorf("IPA 条目数 %d 超过上限 %d", len(zr.File), maxZipEntries)
	}

	// 只取 Payload/<某个.app>/Info.plist 这一层，跳过嵌套资源里的同名文件。
	var plistFile *zip.File
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if !strings.HasPrefix(name, "Payload/") || !strings.HasSuffix(name, "/Info.plist") {
			continue
		}
		if strings.Count(strings.TrimPrefix(name, "Payload/"), "/") == 1 {
			plistFile = f
			break
		}
	}
	if plistFile == nil {
		return meta, fmt.Errorf("IPA 中未找到 Payload/*.app/Info.plist")
	}
	if int64(plistFile.UncompressedSize64) > maxReadBytes {
		return meta, fmt.Errorf("Info.plist 超过 %d MB 读取上限", contract.ParseReadLimitMB)
	}
	if err := checkContext(ctx); err != nil {
		return meta, err
	}

	rc, err := plistFile.Open()
	if err != nil {
		return meta, fmt.Errorf("打开 Info.plist 失败: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxReadBytes+1))
	if err != nil {
		return meta, fmt.Errorf("读取 Info.plist 失败: %w", err)
	}
	if int64(len(data)) > maxReadBytes {
		return meta, fmt.Errorf("Info.plist 超过 %d MB 读取上限", contract.ParseReadLimitMB)
	}

	// plist 值类型不固定（版本号可能是字符串或数字），统一按 any 解码再转字符串。
	var raw map[string]any
	if _, err := plist.Unmarshal(data, &raw); err != nil {
		return meta, fmt.Errorf("解析 Info.plist 失败: %w", err)
	}
	meta.AppName = firstString(raw["CFBundleDisplayName"], raw["CFBundleName"])
	meta.PackageID = firstString(raw["CFBundleIdentifier"])
	meta.Version = firstString(raw["CFBundleShortVersionString"])
	meta.BuildNumber = firstString(raw["CFBundleVersion"])
	return meta, nil
}

// firstString 取第一个非空值；plist 的数值字段转成十进制字符串。
func firstString(values ...any) string {
	for _, value := range values {
		switch v := value.(type) {
		case string:
			if v != "" {
				return v
			}
		case int:
			return fmt.Sprintf("%d", v)
		case int64:
			return fmt.Sprintf("%d", v)
		case uint64:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

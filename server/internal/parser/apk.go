// APK 解析：读取二进制 AndroidManifest.xml 与 resources.arsc，输出 4 个规范字段。
// 职责：包名 / versionName / versionCode / 应用名；应用名为资源引用时用 (*Apk).Label(nil) 解开。
// 输入：APK 的 io.ReaderAt 与总大小（来自 stage 落盘后的临时文件）。
// 输出：ParsedMeta 与可降级的 error。
// 边界：只调用 androidbinary 的读取 API，绝不调用 Icon()（应用图标解析不在第一版范围）；
//       manifest 读取上限 5MB、zip 条目数上限防 zip bomb；不落盘、不写库。
package parser

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/shogo82148/androidbinary/apk"

	"dandelion/server/internal/contract"
)

func parseApk(ctx context.Context, r io.ReaderAt, size int64) (contract.ParsedMeta, error) {
	meta := contract.ParsedMeta{}
	if err := checkContext(ctx); err != nil {
		return meta, err
	}

	// 先用标准库 zip 做便宜的安全检查：条目数量与 manifest 声明大小，防 zip bomb 与超大 manifest。
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return meta, fmt.Errorf("APK 不是合法 zip: %w", err)
	}
	if len(zr.File) > maxZipEntries {
		return meta, fmt.Errorf("APK 条目数 %d 超过上限 %d", len(zr.File), maxZipEntries)
	}
	for _, f := range zr.File {
		if f.Name == "AndroidManifest.xml" && int64(f.UncompressedSize64) > maxReadBytes {
			return meta, fmt.Errorf("AndroidManifest.xml 超过 %d MB 读取上限", contract.ParseReadLimitMB)
		}
	}
	if err := checkContext(ctx); err != nil {
		return meta, err
	}

	// SectionReader 覆盖整个包，zip 中央目录在文件末尾，不能按 5MB 截断。
	a, err := apk.OpenZipReader(io.NewSectionReader(r, 0, size), size)
	if err != nil {
		return meta, fmt.Errorf("读取 APK 清单失败: %w", err)
	}
	defer a.Close()

	manifest := a.Manifest()
	var problems []error
	if value, e := manifest.Package.String(); e == nil {
		meta.PackageID = value
	} else {
		problems = append(problems, fmt.Errorf("包名: %w", e))
	}
	if value, e := manifest.VersionName.String(); e == nil {
		meta.Version = value
	} else {
		problems = append(problems, fmt.Errorf("版本号: %w", e))
	}
	if value, e := manifest.VersionCode.Int32(); e == nil {
		meta.BuildNumber = strconv.FormatInt(int64(value), 10)
	} else {
		problems = append(problems, fmt.Errorf("构建号: %w", e))
	}

	// Label() 会解析 resources.arsc 解开 @string/app_name；失败时回退到清单里的原始字符串。
	if label, e := a.Label(nil); e == nil && label != "" {
		meta.AppName = label
	} else if value, e := manifest.App.Label.String(); e == nil {
		meta.AppName = value
	} else {
		problems = append(problems, errors.New("应用名"))
	}
	return meta, errors.Join(problems...)
}

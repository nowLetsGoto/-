// Package parser 只做一件事：从安装包字节中读取 4 个规范化元数据字段。
// 职责：统一入口 Parse，按平台分派到 APK / IPA 解析，统一超时、读取上限与 zip 条目上限。
// 输入：只读 io.ReaderAt、文件总字节数与平台提示。
// 输出：ParsedMeta（允许部分字段为空）+ error；错误绝不阻塞上传，只由调用方写成 warnings。
// 边界：不落盘、不写 DB、不发网络、不调 service/repository、不生成 token、不解析图标；
//       纯 Go 实现，不引 cgo、不调用 aapt/unzip 等外部命令。
package parser

import (
	"context"
	"fmt"
	"io"
	"time"

	"dandelion/server/internal/contract"
)

const (
	parseTimeout  = time.Duration(contract.ParseTimeoutSeconds) * time.Second
	maxReadBytes  = int64(contract.ParseReadLimitMB) * 1024 * 1024
	maxZipEntries = 4096
)

// Parse 是解析的唯一入口。超时通过 goroutine + select 真正生效；
// 解析内部允许「部分成功」，返回已取到的字段并附带 error，调用方据此生成 warnings。
func Parse(r io.ReaderAt, size int64, platform string) (contract.ParsedMeta, error) {
	ctx, cancel := context.WithTimeout(context.Background(), parseTimeout)
	defer cancel()

	type outcome struct {
		meta contract.ParsedMeta
		err  error
	}
	// 缓冲为 1：即便超时后解析协程才结束，也不会阻塞泄漏。
	ch := make(chan outcome, 1)
	go func() {
		var meta contract.ParsedMeta
		var err error
		switch platform {
		case contract.PlatformAndroid:
			meta, err = parseApk(ctx, r, size)
		case contract.PlatformIOS:
			meta, err = parseIpa(ctx, r, size)
		case contract.PlatformHarmony:
			// 鸿蒙包（.hap）暂不解析：给出可上屏的原因，由调用方写成 warning，不阻塞上传。
			err = fmt.Errorf("暂不支持解析 .hap，请手动填写应用名与版本号")
		default:
			err = fmt.Errorf("未知平台: %s", platform)
		}
		ch <- outcome{meta: meta, err: err}
	}()

	select {
	case out := <-ch:
		return out.meta, out.err
	case <-ctx.Done():
		return contract.ParsedMeta{}, fmt.Errorf("解析超过 %d 秒，已放弃自动识别", contract.ParseTimeoutSeconds)
	}
}

// checkContext 在可能耗时的步骤之间检查取消信号。
func checkContext(ctx context.Context) error {
	return ctx.Err()
}

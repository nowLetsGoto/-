// build_direct_upload_test.go · 直传票据请求头的单测。
//
// 职责：锁定哪些头交给浏览器显式设置，哪些由浏览器自己生成。
// 输入：内容类型与原始文件名。
// 输出：对 uploadTicketHeaders 返回值的断言。
// 边界：纯函数测试，不连库、不碰对象存储。
package service

import (
	"testing"

	"dandelion/server/internal/storage"
)

func TestUploadTicketHeaders(t *testing.T) {
	headers := uploadTicketHeaders("application/vnd.android.package-archive", "景田.apk")

	if got := headers["Content-Type"]; got != "application/vnd.android.package-archive" {
		t.Errorf("Content-Type 应原样传出，got=%q", got)
	}
	if got := headers["Cache-Control"]; got != storage.ObjectCacheControl() {
		t.Errorf("Cache-Control 必须与签名一致，got=%q", got)
	}
	if headers["Content-Disposition"] == "" {
		t.Error("Content-Disposition 不能为空，否则下载文件名会退化为对象名")
	}

	// 浏览器禁止手动设置 Content-Length；它会按文件大小自动发送且参与签名。
	// 一旦有人把它加回票据，上传页控制台就会报 unsafe header。
	if _, ok := headers["Content-Length"]; ok {
		t.Error("Content-Length 不应出现在票据请求头中")
	}
}

// handler_test.go · multipart 解析的单元测试。
// 职责：钉住文本字段白名单、图片 MIME 嗅探/扩展名归一、5MB/9 张限制与失败清理。
// 输入：手工构造的 multipart/form-data 请求与临时 storage.Paths。
// 输出：断言解析结果的字段/图片，或 400/413 错误且 tmp 无残留。
// 边界：不连数据库、不起 HTTP 服务；只测 parseMultipart 与 cleanupStagedImages 纯流程。
package rdc

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	"dandelion/server/internal/storage"
)

// pngBytes 是能被 http.DetectContentType 识别为 image/png 的最小签名。
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// jpegBytes 是能被识别为 image/jpeg 的最小签名。
var jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01")

type testPart struct {
	name     string
	fileName string
	content  []byte
}

// newMultipartRequest 按给定顺序写 parts，顺序可控才能确定「先 stage 后失败」的清理路径。
func newMultipartRequest(t *testing.T, parts []testPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		var target io.Writer
		if part.fileName == "" {
			target, _ = writer.CreateFormField(part.name)
		} else {
			target, _ = writer.CreateFormFile(part.name, part.fileName)
		}
		if _, err := target.Write(part.content); err != nil {
			t.Fatalf("构造测试 multipart 失败: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭测试 multipart 失败: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/rdc/requirements", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

// newTestPaths 建一个 tmp 目录存在的临时 storage 根。
func newTestPaths(t *testing.T) *storage.Paths {
	t.Helper()
	paths, err := storage.NewPaths(t.TempDir())
	if err != nil {
		t.Fatalf("构造 storage.Paths 失败: %v", err)
	}
	if err := os.MkdirAll(paths.TmpDir(), 0o755); err != nil {
		t.Fatalf("创建 tmp 目录失败: %v", err)
	}
	return paths
}

func stagedCount(t *testing.T, paths *storage.Paths) int {
	t.Helper()
	entries, err := os.ReadDir(paths.TmpDir())
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("读取 tmp 目录失败: %v", err)
	}
	return len(entries)
}

func assertStatus(t *testing.T, err error, want int) {
	t.Helper()
	var appErr *handler.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("期望 AppError(%d)，实际 %T: %v", want, err, err)
	}
	if appErr.Status != want {
		t.Fatalf("期望状态码 %d，实际 %d: %s", want, appErr.Status, appErr.Message)
	}
}

func textField(name, value string) testPart {
	return testPart{name: name, content: []byte(value)}
}

func imageField(content []byte) testPart {
	return testPart{name: "images", fileName: "设计稿.png", content: content}
}

func validParts() []testPart {
	return []testPart{
		textField("title", "优化登录"),
		textField("description", "缩短首屏加载时间"),
		textField("status", "confirmed"),
		textField("acceptanceCriteria", "低于 2 秒"),
		textField("projectId", "11111111-1111-4111-8111-111111111111"),
		imageField(pngBytes),
	}
}

func TestParseMultipartAcceptsValidForm(t *testing.T) {
	paths := newTestPaths(t)
	parts := validParts()
	parts = append(parts, imageField(jpegBytes))
	request := newMultipartRequest(t, parts)

	form, err := parseMultipart(request, paths)
	if err != nil {
		t.Fatalf("合法 multipart 不应被拒绝: %v", err)
	}
	if form.Fields.Title != "优化登录" || form.Fields.Description != "缩短首屏加载时间" ||
		form.Fields.Status != "confirmed" || form.Fields.AcceptanceCriteria != "低于 2 秒" ||
		form.Fields.ProjectID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("文本字段提取不完整: %+v", form.Fields)
	}
	if len(form.Images) != 2 {
		t.Fatalf("应有 2 张图片，实际 %d", len(form.Images))
	}
	if form.Images[0].Ext != "png" || form.Images[0].ContentType != "image/png" {
		t.Fatalf("第一张应识别为 png: %+v", form.Images[0])
	}
	if form.Images[1].Ext != "jpg" || form.Images[1].ContentType != "image/jpeg" {
		t.Fatalf("jpeg 应归一为 jpg: %+v", form.Images[1])
	}
	if form.Images[0].CleanName != "设计稿.png" || form.Images[0].FileSize != int64(len(pngBytes)) {
		t.Fatalf("展示名或体积不正确: %+v", form.Images[0])
	}
	if stagedCount(t, paths) != 2 {
		t.Fatal("两张图片都应落在 tmp")
	}
	cleanupStagedImages(form.Images, paths)
	if stagedCount(t, paths) != 0 {
		t.Fatal("清理后 tmp 应为空")
	}
}

func TestParseMultipartRejectsUnknownTextFieldAfterStage(t *testing.T) {
	paths := newTestPaths(t)
	parts := validParts()
	parts = append(parts, textField("tenantId", "hack"))
	_, err := parseMultipart(newMultipartRequest(t, parts), paths)
	assertStatus(t, err, http.StatusBadRequest)
	if stagedCount(t, paths) != 0 {
		t.Fatal("未知字段拒绝后必须清理已 stage 的图片")
	}
}

func TestParseMultipartRejectsNonImage(t *testing.T) {
	paths := newTestPaths(t)
	parts := validParts()
	parts[len(parts)-1] = imageField([]byte("this is not an image"))
	_, err := parseMultipart(newMultipartRequest(t, parts), paths)
	assertStatus(t, err, http.StatusBadRequest)
	if stagedCount(t, paths) != 0 {
		t.Fatal("非图片拒绝后 tmp 应为空")
	}
}

func TestParseMultipartRejectsTooManyImages(t *testing.T) {
	paths := newTestPaths(t)
	parts := validParts()
	for i := 0; i < contract.MaxRDCImages; i++ {
		parts = append(parts, imageField(pngBytes))
	}
	_, err := parseMultipart(newMultipartRequest(t, parts), paths)
	assertStatus(t, err, http.StatusBadRequest)
	if stagedCount(t, paths) != 0 {
		t.Fatal("超过 9 张时已 stage 的图片必须全部清理")
	}
}

func TestParseMultipartRejectsOversizeImage(t *testing.T) {
	paths := newTestPaths(t)
	parts := validParts()
	parts[len(parts)-1] = imageField(append(append([]byte(nil), pngBytes...),
		bytes.Repeat([]byte("x"), contract.MaxRDCImageBytes+1)...))
	_, err := parseMultipart(newMultipartRequest(t, parts), paths)
	assertStatus(t, err, http.StatusRequestEntityTooLarge)
	if stagedCount(t, paths) != 0 {
		t.Fatal("超限图片拒绝后 tmp 应为空")
	}
}

func TestParseMultipartRejectsNonMultipartRequest(t *testing.T) {
	paths := newTestPaths(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/rdc/requirements", bytes.NewBufferString("{}"))
	request.Header.Set("Content-Type", "application/json")
	_, err := parseMultipart(request, paths)
	assertStatus(t, err, http.StatusBadRequest)
}

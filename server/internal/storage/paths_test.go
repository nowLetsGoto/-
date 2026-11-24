// paths_test.go 验证存储路径的隔离边界与跨平台文件名清洗。
// 职责：覆盖正式路径往返、暂存目录边界和路径穿越拒绝行为。
// 输入：临时根目录、正常包路径及恶意路径；输出：测试断言。
// 边界：不读写安装包、不连接数据库，不以字符串前缀代替目录边界。
package storage

import (
	"path/filepath"
	"testing"
)

func TestPathsRoundTrip(t *testing.T) {
	root := t.TempDir()
	paths, err := NewPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root() != root {
		t.Fatalf("根目录 = %q，期望 %q", paths.Root(), root)
	}
	relative := paths.RelativePath("test", "android", "build-id", "apk")
	if relative != "test/android/build-id.apk" {
		t.Fatalf("数据库相对路径 = %q", relative)
	}
	abs, err := paths.SafeResolve(relative)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "uploads", "test", "android", "build-id.apk")
	if abs != want || paths.FinalPath("test", "android", "build-id", "apk") != want {
		t.Fatalf("正式路径未正确往返：%q，期望 %q", abs, want)
	}
	stage := paths.StagePath("token", "ipa")
	if resolved, err := paths.SafeResolveStage(stage); err != nil || resolved != filepath.Join(root, "tmp", "token.ipa") {
		t.Fatalf("暂存路径解析失败：%q，%v", resolved, err)
	}
	if paths.StageNamePath("token") != filepath.Join(root, "tmp", "token.name") {
		t.Fatal("原始文件名伴生路径不正确")
	}
}

func TestSafeResolveRejectsEscapes(t *testing.T) {
	paths, err := NewPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"", ".", "..", "../secret", "test/../../../secret",
		paths.UploadsDir(), filepath.Join(paths.Root(), "secret"),
		"..\\secret", "C:\\secret.apk", "C:/secret.apk", "test/\x00.apk",
	} {
		t.Run(input, func(t *testing.T) {
			if resolved, err := paths.SafeResolve(input); err == nil {
				t.Fatalf("危险路径 %q 被解析为 %q", input, resolved)
			}
		})
	}
}

func TestSafeResolveStageRejectsSiblingAndParent(t *testing.T) {
	paths, err := NewPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"token.apk", paths.TmpDir(), paths.Root(),
		filepath.Join(paths.Root(), "tmp-old", "token.apk"),
		filepath.Join(paths.UploadsDir(), "token.apk"),
		filepath.Join(paths.TmpDir(), "..", "secret"),
	} {
		t.Run(input, func(t *testing.T) {
			if resolved, err := paths.SafeResolveStage(input); err == nil {
				t.Fatalf("暂存目录外路径 %q 被解析为 %q", input, resolved)
			}
		})
	}
}

func TestCleanFileName(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"/home/user/应用.apk", "应用.apk"},
		{"C:\\fakepath\\应用.ipa", "应用.ipa"},
		{"  应用\r\n\x00.hap  ", "应用.hap"},
		{"../../应用.apk", "应用.apk"},
		{"", ""}, {".", ""}, {"..", ""}, {"/", ""},
	} {
		t.Run(test.raw, func(t *testing.T) {
			if got := CleanFileName(test.raw); got != test.want {
				t.Fatalf("CleanFileName(%q) = %q，期望 %q", test.raw, got, test.want)
			}
		})
	}
}

func TestInvalidStorageInputs(t *testing.T) {
	if _, err := NewPaths(" "); err == nil {
		t.Fatal("空根目录应被拒绝")
	}
	if _, err := FileExt("unknown"); err == nil {
		t.Fatal("未知平台不能生成扩展名")
	}
	if _, err := ContentType("unknown"); err == nil {
		t.Fatal("未知平台不能生成 Content-Type")
	}
}

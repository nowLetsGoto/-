// build_resolve_test.go · 下载回落规则的验证。
//
// 职责：锁定「什么时候可以走 CDN、什么时候必须用本地文件」这条判断。
// 输入：构造的 Build 与临时文件。
// 输出：对 FileRef.R2Key 与错误分支的断言。
// 边界：不连数据库、不发网络，只验证 resolveBuildFile 这一个纯函数。
//
// 为什么这条判断值得单测：它同时决定「淘汰后能否下载」与「会不会 302 到已删除对象」。
// 判据一旦退回成「r2_key 是否为空」，淘汰后 key 仍保留，
// 就会签发一个指向已删除对象的直链，用户点下载得到 404——而这不会报错，只是静默失效。
package service

import (
	"os"
	"path/filepath"
	"testing"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/storage"
)

// newTestService 构造一个只带路径解析能力的 service，够 resolveBuildFile 用。
func newTestService(t *testing.T, root string) *BuildService {
	t.Helper()
	paths, err := storage.NewPaths(root)
	if err != nil {
		t.Fatalf("构造 Paths 失败: %v", err)
	}
	return &BuildService{paths: paths}
}

// writeLocalFile 在 uploads 下写一个文件，返回其相对路径。
func writeLocalFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, "uploads", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

// TestResolveBuildFileUsesR2WhenSynced 验证对象在桶里时给出 R2Key，下载走 CDN。
func TestResolveBuildFileUsesR2WhenSynced(t *testing.T) {
	root := t.TempDir()
	svc := newTestService(t, root)

	ref, err := svc.resolveBuildFile(contract.Build{
		ID:               "id-1",
		Platform:         contract.PlatformAndroid,
		FileExt:          "apk",
		OriginalFileName: "demo.apk",
		FileSize:         1024,
		RelativePath:     "test/android/id-1.apk",
		R2Key:            "tenant/proj/direct/id-1.apk",
		R2State:          contract.R2StateSynced,
	})
	if err != nil {
		t.Fatalf("应成功，got err=%v", err)
	}
	if ref.R2Key == "" {
		t.Fatal("已同步的包必须给出 R2Key，否则下载无法走 CDN")
	}
}

// TestResolveBuildFileFallsBackToLocalWhenEvicted 是本次改造的核心断言。
//
// 淘汰后 r2_key 仍保留（供重推复用），但对象已不在桶里。
// 此时必须不给出 R2Key，让下载回落到本地副本；
// 若仍给出，就会 302 到一个已删除的对象，用户得到 404。
func TestResolveBuildFileFallsBackToLocalWhenEvicted(t *testing.T) {
	root := t.TempDir()
	svc := newTestService(t, root)
	const rel = "test/android/id-2.apk"
	writeLocalFile(t, root, rel, "local-copy")

	ref, err := svc.resolveBuildFile(contract.Build{
		ID:               "id-2",
		Platform:         contract.PlatformAndroid,
		FileExt:          "apk",
		OriginalFileName: "demo.apk",
		FileSize:         1024,
		RelativePath:     rel,
		R2Key:            "tenant/proj/direct/id-2.apk", // 淘汰后仍保留
		R2State:          contract.R2StateEvicted,
	})
	if err != nil {
		t.Fatalf("本地有副本时应成功，got err=%v", err)
	}
	if ref.R2Key != "" {
		t.Fatalf("已淘汰的包不应给出 R2Key（会 302 到已删对象），got=%q", ref.R2Key)
	}
	if ref.Size != int64(len("local-copy")) {
		t.Fatalf("大小应取自本地文件，got=%d", ref.Size)
	}
}

// TestResolveBuildFileMissingLocalAfterEvict 验证淘汰且本地无副本时明确失败。
//
// 这是第一批仍存在的边界：直传包在本机从未落过盘，
// 一旦被淘汰就无副本可用，应返回 NotFound 而不是给出坏链接。
func TestResolveBuildFileMissingLocalAfterEvict(t *testing.T) {
	root := t.TempDir()
	svc := newTestService(t, root)

	_, err := svc.resolveBuildFile(contract.Build{
		ID:               "id-3",
		Platform:         contract.PlatformAndroid,
		FileExt:          "apk",
		OriginalFileName: "demo.apk",
		FileSize:         1024,
		RelativePath:     "test/android/id-3.apk",
		R2Key:            "tenant/proj/direct/id-3.apk",
		R2State:          contract.R2StateEvicted,
	})
	if err == nil {
		t.Fatal("本地无副本且 R2 已淘汰时应返回错误")
	}
}

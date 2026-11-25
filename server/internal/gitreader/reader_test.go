// Package gitreader 的安全边界测试。
// 职责：验证命令固定、路径白名单、脱敏和有界输出。
// 输入：临时目录及恶意文本；输出：测试断言。
// 边界：只在临时仓库调用 git init，不触碰业务仓库。
package gitreader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dandelion/server/internal/contract"
)

func TestFixedCommandsAndOutputBound(t *testing.T) {
	for _, op := range []operation{opVerify, opLog, opStatus, opBranch} {
		argv, ok := args(op, "/tmp/repo")
		if !ok || strings.Contains(strings.Join(argv, " "), "push") || argv[len(argv)-1] == "" {
			t.Fatalf("非法 argv: %v", argv)
		}
	}
	if _, ok := args(operation(99), "/tmp/repo"); ok {
		t.Fatal("未知命令必须拒绝")
	}
	b := &limitedBuffer{limit: 4}
	if _, err := b.Write([]byte("12345")); err == nil || b.Len() != 0 {
		t.Fatal("输出必须硬限且不保留超限数据")
	}
}
func TestPathBoundaryAndExternalGitdir(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "repo")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRepoPath(inside, []string{root}); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{filepath.Join(root, ".."), link, root + "2", "relative"} {
		if _, err := ResolveRepoPath(candidate, []string{root}); err == nil {
			t.Fatalf("越界路径被接受: %q", candidate)
		}
	}
	if err := os.WriteFile(filepath.Join(inside, ".git"), []byte("gitdir: "+outside), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGitDir(inside, []string{root}); err == nil {
		t.Fatal("外部 gitdir 应拒绝")
	}
}
func TestSanitizeSensitiveFields(t *testing.T) {
	raw := "ghp_1234567890abcdefgh xoxb-1234567890secret Authorization: Bearer abcdef123 token=secret AKIAABCDEFGHIJKLMNOP 0123456789abcdef0123456789abcdef"
	cleaned := Sanitize(raw)
	for _, secret := range []string{"ghp_", "xoxb-", "Authorization:", "token=", "AKIA", "0123456789abcdef"} {
		if strings.Contains(cleaned, secret) {
			t.Fatalf("敏感字段泄漏: %s", secret)
		}
	}
}
func TestReaderSnapshotNoFileNames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	cmd = exec.Command("git", "-C", repo, "-c", "user.name=Tester", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "ghp_1234567890abcdefgh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %v", out, err)
	}
	name := "token=hidden-file"
	if err := os.WriteFile(filepath.Join(repo, name), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	reader := NewReader([]string{root}, 5)
	snapshot := reader.Read(context.Background(), repo)
	if snapshot.Status != contract.GitOK || snapshot.WorkspaceClean || snapshot.ChangedFileCount != 1 || len(snapshot.Commits) != 1 {
		t.Fatalf("错误快照: %+v", snapshot)
	}
	if strings.Contains(snapshot.Commits[0].Subject, "ghp_") {
		t.Fatal("提交主题泄漏")
	}
	if strings.Contains(snapshot.Message, name) || strings.Contains(snapshot.Branch, name) {
		t.Fatal("文件名泄漏")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if status := reader.Read(ctx, repo).Status; status != contract.GitTimeout {
		t.Fatalf("超时状态=%s", status)
	}
}

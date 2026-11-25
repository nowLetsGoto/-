// Package gitreader 负责受限本机 Git 只读查询。
// 职责：解析仓库路径并限制到运维允许根目录。
// 输入：公司配置的绝对路径与平台允许根；输出：规范化路径或安全错误。
// 边界：不执行命令、不回显未经校验的路径。
package gitreader

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrPathForbidden = errors.New("仓库路径不在允许范围内")
var ErrNotConfigured = errors.New("仓库尚未配置")

// ResolveRepoPath 对符号链接解析后的目录应用组件边界检查。
func ResolveRepoPath(configured string, roots []string) (string, error) {
	if configured == "" || len(roots) == 0 {
		return "", ErrNotConfigured
	}
	if !filepath.IsAbs(configured) {
		return "", ErrPathForbidden
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(configured))
	if err != nil {
		return "", ErrPathForbidden
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", ErrPathForbidden
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		safeRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(safeRoot, resolved)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", ErrPathForbidden
}

// ValidateGitDir 禁止指向允许根之外的 gitdir 和 worktree 元数据。
func ValidateGitDir(repo string, roots []string) error {
	dotGit := filepath.Join(repo, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return ErrPathForbidden
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		_, err = ResolveRepoPath(dotGit, roots)
		return err
	}
	// .git 文件代表 worktree 或 gitdir 重定向；本版直接禁用，避免 metadata 越界。
	return ErrPathForbidden
}

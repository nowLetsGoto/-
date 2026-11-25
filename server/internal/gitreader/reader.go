// Package gitreader 负责受限本机 Git 只读查询。
// 职责：隔离 Git 子进程环境、执行固定只读命令并有界解析输出。
// 输入：请求上下文及公司仓库路径；输出：脱敏 GitSnapshot 或友好失败状态。
// 边界：不执行 shell/测试/联网/仓库写入，不回显路径、stderr 或文件名。
package gitreader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dandelion/server/internal/contract"
)

const stdoutLimit = 256 << 10
const stderrLimit = 16 << 10

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)
var errOutputLimit = errors.New("git output limit exceeded")

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.exceeded = true
		if b.cancel != nil {
			b.cancel()
		}
		return 0, errOutputLimit
	}
	return b.Buffer.Write(p)
}

// Reader 持有启动时固定的可信 Git 路径及运维约束。
type Reader struct {
	roots      []string
	timeout    time.Duration
	executable string
}

// NewReader 解析可信可执行文件；没有 git 时 Read 返回友好错误。
func NewReader(roots []string, timeoutSeconds int) *Reader {
	if timeoutSeconds < 1 || timeoutSeconds > contract.RDCMaxGitTimeoutSeconds {
		timeoutSeconds = contract.RDCDefaultGitTimeoutSeconds
	}
	executable, _ := exec.LookPath("git")
	if executable != "" {
		executable, _ = filepath.EvalSymlinks(executable)
		if !filepath.IsAbs(executable) {
			executable = ""
		}
	}
	return &Reader{roots: append([]string(nil), roots...), timeout: time.Duration(timeoutSeconds) * time.Second, executable: executable}
}

func (r *Reader) run(ctx context.Context, op operation, path string) (string, error) {
	argv, ok := args(op, path)
	if !ok || r.executable == "" {
		return "", errors.New("git unavailable")
	}
	cmdCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, r.executable, argv...)
	cmd.Dir = path
	cmd.Stdin = strings.NewReader("")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/dev/null", "XDG_CONFIG_HOME=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_PAGER=cat", "PAGER=cat", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_LFS_SKIP_SMUDGE=1"}
	stdout := &limitedBuffer{limit: stdoutLimit, cancel: cancel}
	stderr := &limitedBuffer{limit: stderrLimit, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return "", errOutputLimit
	}
	if cmdCtx.Err() != nil {
		return "", cmdCtx.Err()
	}
	if err != nil {
		return "", errors.New("git command failed")
	}
	return stdout.String(), nil
}

// Read 返回只读快照；错误类别从固定中文文案表达，原始输出不离开本包。
func (r *Reader) Read(ctx context.Context, configuredPath string) contract.GitSnapshot {
	result := contract.GitSnapshot{Status: contract.GitError, Message: "读取 Git 记录失败，请稍后重试。", Commits: []contract.DeliveryCommit{}, ReadAt: time.Now().UTC().Format(time.RFC3339)}
	path, err := ResolveRepoPath(configuredPath, r.roots)
	if errors.Is(err, ErrNotConfigured) {
		result.Status, result.Message = contract.GitNotConfigured, "尚未配置 Git 仓库路径，请联系管理员。"
		return result
	}
	if err != nil {
		result.Status, result.Message = contract.GitPathForbidden, "配置的仓库路径不在允许范围内。"
		return result
	}
	if err := ValidateGitDir(path, r.roots); err != nil {
		result.Status, result.Message = contract.GitPathForbidden, "配置的仓库路径不在允许范围内。"
		return result
	}
	verify, err := r.run(ctx, opVerify, path)
	if err == nil && strings.TrimSpace(verify) != "true" {
		result.Status, result.Message = contract.GitNotRepo, "配置的路径不是有效的 Git 仓库。"
		return result
	}
	if err != nil {
		return gitFailure(result, err, true)
	}
	output, err := r.run(ctx, opLog, path)
	if err != nil {
		return gitFailure(result, err, false)
	}
	for _, raw := range strings.Split(output, "\x1e") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		fields := strings.SplitN(strings.TrimSpace(raw), "\x1f", 4)
		if len(fields) != 4 || !shaPattern.MatchString(fields[0]) {
			return result
		}
		result.Commits = append(result.Commits, contract.DeliveryCommit{Sha: fields[0], ShortSha: fields[0][:7], Author: Sanitize(fields[1]), Time: Sanitize(fields[2]), Subject: Sanitize(fields[3])})
	}
	status, err := r.run(ctx, opStatus, path)
	if err != nil {
		return gitFailure(result, err, false)
	}
	result.ChangedFileCount = len(strings.Split(strings.TrimSuffix(status, "\n"), "\n"))
	if status == "" {
		result.ChangedFileCount = 0
	}
	result.WorkspaceClean = result.ChangedFileCount == 0
	branch, err := r.run(ctx, opBranch, path)
	if err != nil {
		return gitFailure(result, err, false)
	}
	result.Branch = Sanitize(strings.TrimSpace(branch))
	result.Status, result.Message = contract.GitOK, ""
	return result
}

func gitFailure(result contract.GitSnapshot, err error, verify bool) contract.GitSnapshot {
	result.Commits = []contract.DeliveryCommit{}
	result.Branch = ""
	result.ChangedFileCount = 0
	if errors.Is(err, context.DeadlineExceeded) {
		result.Status, result.Message = contract.GitTimeout, "读取 Git 记录超时，请稍后重试。"
	} else if verify {
		result.Status, result.Message = contract.GitNotRepo, "配置的路径不是有效的 Git 仓库。"
	}
	return result
}

var _ io.Writer = (*limitedBuffer)(nil)

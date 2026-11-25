// Package gitreader 负责受限本机 Git 只读查询。
// 职责：固定私有操作及参数模板，不接受外部命令字符串。
// 输入：包内操作与已校验路径；输出：exec 的 argv。
// 边界：不调用 shell、不提供任何 Git 写操作。
package gitreader

type operation uint8

const (
	opVerify operation = iota
	opLog
	opStatus
	opBranch
)

var allowedCommands = map[operation][]string{
	opVerify: {"rev-parse", "--is-inside-work-tree"},
	opLog:    {"log", "-n", "10", "--date=iso-strict", "--pretty=format:%H%x1f%an%x1f%aI%x1f%s%x1e"},
	opStatus: {"status", "--porcelain=v1", "--untracked-files=normal"},
	opBranch: {"branch", "--show-current"},
}

func args(op operation, path string) ([]string, bool) {
	template, ok := allowedCommands[op]
	if !ok {
		return nil, false
	}
	argv := []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "core.pager=cat", "--no-pager", "-C", path}
	return append(argv, template...), true
}

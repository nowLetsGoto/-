// Package tunnel · 公网隧道的只读状态查询。
// 职责：展示隧道是否配置、是否运行、公网是否可达，并提供一次显式的深度检查。
// 输入：进程配置（PUBLIC_BASE_URL）与本机文件系统（~/.cloudflared/config.yml）。
// 输出：contract.TunnelStatus。
// 边界：不修改任何配置、不代做登录、不创建隧道、不写 DNS。
//
// 为什么是只读：隧道凭据在 ~/.cloudflared/ 下，登录要浏览器人工授权，
// DNS 记录只能去 Cloudflare 控制台改。做成编辑表单只会产生
// 「页面改了但文件没改」的不一致——那比没有页面更糟。
package tunnel

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
)

// checkTimeout 是公网探活与 tunnel info 的时限。
//
// 10 秒：隧道握手正常在 1-2 秒完成，超过 10 秒基本可判为不通。
const checkTimeout = 10 * time.Second

// Service 是隧道状态的唯一查询入口。
type Service struct {
	cfg config.Config
}

func NewService(cfg config.Config) *Service {
	return &Service{cfg: cfg}
}

// Status 返回隧道的只读状态（快路径）。
//
// 只做本地文件读取与一次探活，不跑 cloudflared 命令——后者要 1-2 秒，
// 不适合每次进设置页都执行。深度检查由 Check 提供，前端点按钮才调用。
func (s *Service) Status(ctx context.Context) (contract.TunnelStatus, error) {
	status := contract.TunnelStatus{
		Connectors: -1, // -1 表示「未检查」，与 0 个连接器区分开
		Hostname:   s.cfg.PublicBaseURL,
	}

	confPath := filepath.Join(os.Getenv("HOME"), ".cloudflared", "config.yml")
	data, err := os.ReadFile(confPath)
	if err != nil {
		status.Configured = false
		status.Message = "本机未找到隧道配置（~/.cloudflared/config.yml）。公网访问需要先配置 cloudflared。"
		return status, nil
	}
	status.Configured = true
	status.TunnelID = parseField(string(data), "tunnel")
	status.Upstream = parseIngressService(string(data))
	status.Running = isRunning()

	// 探活：用户真正关心的是「公网能不能打开」，而不是进程在不在。
	status.Reachable = s.probe(ctx)

	switch {
	case status.Reachable:
		status.Message = "公网可访问，隧道工作正常。"
	case !status.Running:
		status.Message = "隧道进程未运行。可执行 bash scripts/install-autostart.sh 重新注册。"
	default:
		status.Message = "隧道进程在运行，但公网暂时无法访问。可能是隧道掉线或上游服务未启动。"
	}
	return status, nil
}

// Check 做一次深度检查，补上连接器数量（慢路径）。
//
// 通过 cloudflared tunnel info 读取真实连接数——这是判断隧道是否握手的
// 最直接证据，探活 200 只能证明「有人在服务」，不能证明服务来自本机。
//
// 命令路径写死为常见安装位置；找不到时返回错误而不是回退到 PATH 搜索，
// 避免执行到 PATH 上同名但来源不明的程序。
func (s *Service) Check(ctx context.Context) (contract.TunnelStatus, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return status, err
	}
	if !status.Configured {
		return status, nil
	}

	const binary = "/opt/homebrew/bin/cloudflared"
	if _, err := os.Stat(binary); err != nil {
		status.Message = "未找到 cloudflared（/opt/homebrew/bin/cloudflared），无法读取连接数。"
		return status, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, binary, "tunnel", "info", "dandelion")
	out, err := cmd.Output()
	if err != nil {
		status.Message = "读取隧道连接信息失败：" + err.Error()
		return status, nil
	}

	status.Connectors = countConnectors(string(out))
	if status.Connectors > 0 {
		status.Message = fmt.Sprintf("公网可访问，%d 个活跃连接器。", status.Connectors)
	} else {
		status.Message = "隧道无活跃连接器，公网访问可能中断。"
	}
	return status, nil
}

// Restart 重启隧道服务。
//
// 用 kickstart -k 而不是先 bootout 再 bootstrap：
// 后者在 launchd 冷却期会返回 I/O error，反而把隧道弄停——
// install-autostart.sh 里已踩过这个坑，这里不重蹈覆辙。
func (s *Service) Restart(ctx context.Context) error {
	label := fmt.Sprintf("gui/%d/com.dandelion.tunnel", os.Getuid())
	cmd := exec.CommandContext(ctx, "/bin/launchctl", "kickstart", "-k", label)
	if out, err := cmd.CombinedOutput(); err == nil {
		return nil
	} else {
		// launchd 没注册过这个服务（直接托管模式）：kickstart 必然失败。
		// 这时不能报错了事——用户点了「重启」却什么都没发生更糟。
		// 但也不在这里代替 install-autostart.sh 重新拉起进程：
		// 那个脚本会重建四个服务，是独立的运维动作，不该由页面按钮隐式触发。
		_ = out
	}

	return fmt.Errorf("隧道未注册到 launchd，无法从页面重启；请在终端执行 bash scripts/install-autostart.sh")
}

// probe 检查 PUBLIC_BASE_URL 是否可达。
func (s *Service) probe(ctx context.Context) bool {
	if s.cfg.PublicBaseURL == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, s.cfg.PublicBaseURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// 只要 Cloudflare 有响应就算可达：404/500 说明隧道通了但上游有问题，
	// 那是另一类故障，不该混进「隧道是否工作」的判断。
	return resp.StatusCode < 500
}

// isRunning 判断隧道进程是否真的在跑。
//
// 不能只查 launchctl：install-autostart.sh 在 launchd 注册失败时会降级为
// 直接托管进程（nohup / start_new_session），此时隧道明明在跑，
// launchctl print 却查不到。只查 launchctl 会显示「未运行」而公网实际可用，
// 管理员据此去重启反而把好端端的隧道弄断。
//
// 两路判断：launchctl 注册成功，或进程列表里能找到 tunnel run。
func isRunning() bool {
	const label = "com.dandelion.tunnel"
	cmd := exec.Command("/bin/launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
	if out, err := cmd.Output(); err == nil && strings.Contains(string(out), label) {
		return true
	}

	// ps 只匹配本用户进程，且模式固定为 cloudflared tunnel run，
	// 避免误匹配到编辑器命令行里出现的同样文字。
	cmd = exec.Command("/bin/ps", "-u", fmt.Sprint(os.Getuid()), "-o", "args=")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "cloudflared tunnel run") &&
			!strings.Contains(line, "grep") {
			return true
		}
	}
	return false
}

// parseField 从 config.yml 里取一个顶层 key 的值。
//
// 不引入 YAML 库：这里只需要两个标量字段，为一个只读展示页加依赖不划算。
func parseField(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, key+":") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
	}
	return ""
}

// parseIngressService 取 ingress 里第一个 http 上游地址。
func parseIngressService(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "service: http") {
			return strings.TrimSpace(strings.TrimPrefix(line, "service:"))
		}
	}
	return ""
}

// countConnectors 数 tunnel info 输出中的连接器行。
//
// 输出形如：
//
//	CONNECTOR ID  CREATED  ARCHITECTURE  VERSION  ORIGIN IP  EDGE
//	6f31c450-...  2026-... darwin_arm64 ...
//
// 以 UUID 格式的 CONNECTOR ID 开头作为行判据，避开表头与空行。
func countConnectors(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 36 || line[8] != '-' || line[13] != '-' || line[18] != '-' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := time.Parse("2006-01-02T15:04:05Z", fields[1]); err == nil {
			count++
		}
	}
	return count
}

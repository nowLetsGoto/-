#!/bin/bash
# 安装 Dandelion 全套后台服务（在你自己的终端里执行一次即可）。
#
# 职责：把四个 LaunchAgent 注册到 launchd 并立即启动：
#   1. com.dandelion.server     生产版后端（10088），崩溃自动重启
#   2. com.dandelion.vite       本地开发前端（5173），崩溃自动重启
#   3. com.dandelion.tunnel     cloudflared 隧道，断线自动重连
#   4. com.dandelion.keepawake  防休眠断言，阻止系统空闲休眠
# 输入：可缺省——plist 不存在时由 scripts/bootstrap-plists.py 按本机环境生成。
# 输出：四个服务长期运行，登录时自启、崩溃自恢复。
# 边界：只做「生成 → 注册 → 校验」，不启停业务进程、不改项目代码。
#
# 用法：  bash scripts/install-autostart.sh
# 卸载：  bash scripts/uninstall-autostart.sh
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UID_NUM="$(id -u)"
DOMAIN="gui/${UID_NUM}"
AGENTS="$HOME/Library/LaunchAgents"
LABELS="com.dandelion.server com.dandelion.vite com.dandelion.tunnel com.dandelion.keepawake"

# start_without_launchd 在 launchd 注册失败时直接托管进程。
#
# 参数：label（com.dandelion.xxx）。
# 做法：从 plist 里取出 ProgramArguments 与 EnvironmentVariables 原样启动，
#       保证行为与 launchd 托管时完全一致（同样的命令、环境、工作目录）。
# 代价：失去开机自启与崩溃自恢复，但服务是可用的——这比停着强得多。
#
# 用 python3 解析 plist 而不是 grep/sed：plist 是 XML，
# 手工解析环境变量字典容易在值含空格或特殊字符时出错。
start_without_launchd() {
  local label="$1"
  local plist="${AGENTS}/${label}.plist"
  python3 - "$plist" "${RUNTIME}/logs" <<'PY'
import plistlib, os, subprocess, sys

plist_path, log_dir = sys.argv[1], sys.argv[2]
with open(plist_path, 'rb') as f:
    cfg = plistlib.load(f)

# 剔除主动式工具注入的沙箱变量。
# 它们指向一个已关闭的 broker，被 Vite 之类会派生子进程的工具继承后，
# 子进程会尝试连回那个 broker 并失败，表现为 HTTP 500
# （实测错误："execution attempt 不存在或已关闭"）。
env = {k: v for k, v in os.environ.items() if not k.startswith('CODEBUDDY_')}
env.update(cfg.get('EnvironmentVariables', {}))
os.makedirs(log_dir, exist_ok=True)

short = cfg['Label'].replace('com.dandelion.', '')
log_path = os.path.join(log_dir, short + '.log')

with open(log_path, 'a') as log:
    p = subprocess.Popen(
        cfg['ProgramArguments'],
        env=env,
        cwd=cfg.get('WorkingDirectory') or os.path.expanduser('~'),
        stdout=log, stderr=subprocess.STDOUT,
        start_new_session=True,  # 脱离本脚本，脚本退出后继续跑
    )
print(f"  已直接托管 {cfg['Label']}（PID {p.pid}）")
PY
}

# 第零步之一：确保 plist 存在且指向本机路径。
#
# 原先这里是「缺一个就 exit 1」，但 plist 从来不在仓库里——它只存在于
# 原作者那台机器的 ~/Library/LaunchAgents。换台电脑 clone 下来跑本脚本，
# 第一句就退出，脚本等于不可用。
#
# 现在交给 bootstrap-plists.py：已存在且路径正确的保持不动（不冲掉手工调整），
# 缺失或路径过期的按本机环境重新生成。
echo "检查 LaunchAgent 配置..."
if [ ! -f "${ROOT}/scripts/bootstrap-plists.py" ]; then
  echo "缺少 ${ROOT}/scripts/bootstrap-plists.py，无法生成 plist" >&2
  exit 1
fi
python3 "${ROOT}/scripts/bootstrap-plists.py" "${ROOT}"

# 生成后仍要回查：脚本可能因权限问题没写成功。
for label in $LABELS; do
  if [ ! -f "${AGENTS}/${label}.plist" ]; then
    echo "生成失败：${AGENTS}/${label}.plist 仍不存在" >&2
    exit 1
  fi
done

# 第零步之二：把可执行文件同步到 ~/dandelion-runtime。
# 原因：macOS 的 TCC 保护 Desktop/Documents/Downloads，launchd 启动的程序
# 执行这些目录下的二进制会被内核拒绝（posix_spawn: Operation not permitted，
# 表现为 launchd 报 EX_CONFIG 78）。把程序放到家目录根部即可绕开。
#
# 数据与日志同理，都不放仓库：仓库通常在桌面，任何持续读写都会反复触发
# 授权弹窗，且授权范围是整个桌面。两者统一收到家目录：
#   数据 → ~/dandelion-data/storage
#   日志 → ~/dandelion-runtime/logs
RUNTIME="$HOME/dandelion-runtime"
echo "同步运行时到 ${RUNTIME} ..."
mkdir -p "${RUNTIME}/logs"
cp -f "${ROOT}/bin/dandelion" "${RUNTIME}/dandelion"
chmod +x "${RUNTIME}/dandelion"

# 第一步：停掉手动启动的旧进程，否则 launchd 启动时会端口冲突。
# 只按端口定位，不误杀 PostgreSQL(5432) 等其他服务。
echo "清理手动启动的旧进程..."
for port in 10088 5173 20241; do
  pids="$(lsof -nP -iTCP:${port} -sTCP:LISTEN -t 2>/dev/null || true)"
  if [ -n "$pids" ]; then
    echo "$pids" | xargs kill 2>/dev/null || true
  fi
done
sleep 2

# 第二步：只 bootout「已注册但需要重载」的，不再无差别清空。
#
# 原先无条件 bootout 全部四个，然后逐个 bootstrap。一旦 bootstrap 因
# launchd 冷却期返回 I/O error，就落得「服务全停、新的一个没起来」的空窗——
# 实测四个服务同时掉线、本地和公网全部不可用。
# 改成：注册不上就直接托管进程，宁可暂时不是 launchd 管的，也不能让服务停着。
for label in $LABELS; do
  launchctl bootout "${DOMAIN}/${label}" 2>/dev/null || true
done
# launchd 对刚卸载的 label 有短暂冷却期，立即 bootstrap 会返回 EX_CONFIG(78)。
sleep 3

# 第三步：注册并逐个校验真实结果。
#
# 不能只看 bootstrap 的返回码：它可能返回 0 却因 label 残留而实际未注册，
# 也可能报 I/O error 而后台确实起来了。唯一可靠的判据是
# 「launchctl print 能否查到该服务」，所以注册后必须回查一次。
# 曾经的写法把 stderr 丢进 /dev/null，注册失败也打印「已加载」，
# 结果是隧道没起来、公网 530，却看不出哪一步错了。
for label in $LABELS; do
  err="$(launchctl bootstrap "$DOMAIN" "${AGENTS}/${label}.plist" 2>&1)" || true
  # 回查：能查到 print 结果才算真的注册上。
  if launchctl print "${DOMAIN}/${label}" >/dev/null 2>&1; then
    echo "已加载 ${label}"
  else
    echo "加载失败 ${label}：${err:-未知原因}" >&2
    # 重试一次：launchd 的冷却期偶尔比 3 秒更长。
    # 重试仍失败才放弃，避免无意义地反复尝试。
    sleep 2
    launchctl bootstrap "$DOMAIN" "${AGENTS}/${label}.plist" 2>/dev/null || true
    if launchctl print "${DOMAIN}/${label}" >/dev/null 2>&1; then
      echo "重试后已加载 ${label}"
    else
      echo "重试仍失败 ${label}，改用直接托管（服务不能停着）" >&2
      # 降级：launchd 注册不上就直接把进程拉起来。
      #
      # 这是本脚本最重要的兜底。原先失败就只打印一行诊断，
      # 而此时四个服务已被 bootout 清空，结果是本地和公网全部不可用——
      # 一次「修服务」的脚本反而把服务弄停了，这是最不该发生的事。
      # 直接拖管后功能完全可用，只是失去「开机自启」与「崩溃自恢复」，
      # 下次再跑一次本脚本仍会优先尝试 launchd。
      start_without_launchd "${label}"
    fi
  fi
done

echo ""
echo "等待服务就绪..."
sleep 12

echo "=== 服务状态 ==="
launchctl list | grep com.dandelion || true

# 78 = EX_CONFIG：launchd 拒绝启动。打印诊断，避免反复盲试。
echo ""
echo "=== 启动诊断 ==="
for label in $LABELS; do
  code="$(launchctl print "${DOMAIN}/${label}" 2>/dev/null | grep 'last exit code' | sed -E 's/.*= ([0-9]+):.*/\1/')"
  state="$(launchctl print "${DOMAIN}/${label}" 2>/dev/null | grep 'state =' | head -1 | sed -E 's/.*= //')"
  echo "${label}: state=${state:-?} exit=${code:-none}"
done

echo ""
echo "--- 错误日志 ---"
for label in $LABELS; do
  short="${label#com.dandelion.}"
  # 日志统一在 runtime 下（见第零步之二），不在仓库里：
  # 仓库常在桌面，往那里持续写日志会触发 TCC 授权弹窗。
  err="${RUNTIME}/logs/${short}.err.log"
  if [ -s "$err" ]; then
    echo "[$label]"; tail -10 "$err"
  fi
done

echo ""
echo "=== 防休眠断言 ==="
pmset -g assertions 2>/dev/null | grep -E "PreventUserIdleSystemSleep|PreventSystemSleep|caffeinate" | head -8 || true

echo ""
echo "=== 连通性验证 ==="
curl -s -o /dev/null -w "后端 10088:  HTTP %{http_code}\n" http://127.0.0.1:10088/ || true
curl -s -o /dev/null -w "前端 5173:   HTTP %{http_code}\n" http://127.0.0.1:5173/ || true
PUBLIC_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 25 https://dandelion.dreamgarden.world/ || echo 000)"
echo "公网地址:    HTTP ${PUBLIC_CODE}"

# 第四步：公网不通时自动补一条兜底。
#
# 为什么不只打印错误就走：公网 530 意味着隧道没连上，
# 而 launchd 偶尔会在 bootstrap 后立刻判定服务退出（KeepAlive 来不及生效）。
# 这时再 bootout/bootstrap 往往还是失败，但直接托管进程能立刻恢复访问。
# 兜底只是应急，脚本下次运行仍会优先走 launchd——真正修好后不会再走到这里。
if [ "${PUBLIC_CODE}" != "200" ]; then
  echo ""
  echo "公网未通（HTTP ${PUBLIC_CODE}），尝试兜底启动隧道..."

  # 先确认是隧道的问题，而不是后端挂了。
  if launchctl print "${DOMAIN}/com.dandelion.tunnel" >/dev/null 2>&1; then
    launchctl kickstart -k "${DOMAIN}/com.dandelion.tunnel" 2>/dev/null || true
  else
    # 注册不上就直接拉起进程。nohup + 后台，脱离本脚本生命周期。
    pkill -f "cloudflared tunnel run" 2>/dev/null || true
    sleep 1
    cd "${RUNTIME}" && nohup /opt/homebrew/bin/cloudflared tunnel run dandelion \
      >> "${RUNTIME}/logs/tunnel.log" 2>&1 &
    echo "已直接启动 cloudflared（PID $!）"
  fi

  # 隧道要十几秒才能完成握手，轮询等它通。
  for i in $(seq 1 10); do
    sleep 5
    PUBLIC_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 https://dandelion.dreamgarden.world/ || echo 000)"
    if [ "${PUBLIC_CODE}" = "200" ]; then
      echo "公网已恢复：HTTP 200（等待 $((i * 5)) 秒）"
      break
    fi
  done
  if [ "${PUBLIC_CODE}" != "200" ]; then
    echo "公网仍未通：HTTP ${PUBLIC_CODE}" >&2
    echo "排查命令：" >&2
    echo "  cloudflared tunnel info dandelion     # 看 CONNECTIONS 是否为空" >&2
    echo "  tail -30 ${RUNTIME}/logs/tunnel.err.log" >&2
  fi
fi

echo ""
echo "完成。四个服务已后台常驻：登录自启、崩溃自恢复、系统不空闲休眠。"
# 日志实际写在运行时目录（plist 里的 StandardOutPath / StandardErrorPath 指向这里），
# 不是仓库里的 ${ROOT}/logs/ —— 那个目录是早期直接托管进程时的遗留，
# 提示错了会让人 tail -f 一个不再写入的旧文件。
echo "日志目录：${HOME}/dandelion-runtime/logs/"
echo ""
echo "合盖场景仍需一条 sudo 命令（只需执行一次）："
echo "  sudo pmset -c sleep 0 disksleep 0"

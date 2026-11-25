#!/bin/bash
# 安装 Dandelion 全套后台服务（在你自己的终端里执行一次即可）。
#
# 职责：把四个 LaunchAgent 注册到 launchd 并立即启动：
#   1. com.dandelion.server     生产版后端（10088），崩溃自动重启
#   2. com.dandelion.vite       本地开发前端（5173），崩溃自动重启
#   3. com.dandelion.tunnel     cloudflared 隧道，断线自动重连
#   4. com.dandelion.keepawake  防休眠断言，阻止系统空闲休眠
# 输入：~/Library/LaunchAgents/ 下已存在的四个 plist。
# 输出：四个服务长期运行，登录时自启、崩溃自恢复。
# 边界：只做 launchctl 注册与清理旧进程，不改 plist、不碰项目代码。
#
# 用法：  bash scripts/install-autostart.sh
# 卸载：  bash scripts/uninstall-autostart.sh
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UID_NUM="$(id -u)"
DOMAIN="gui/${UID_NUM}"
AGENTS="$HOME/Library/LaunchAgents"
LABELS="com.dandelion.server com.dandelion.vite com.dandelion.tunnel com.dandelion.keepawake"

for label in $LABELS; do
  if [ ! -f "${AGENTS}/${label}.plist" ]; then
    echo "缺少 ${AGENTS}/${label}.plist，请先生成" >&2
    exit 1
  fi
done

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

# 第二步：先 bootout 再 bootstrap，保证幂等（重复执行不会重复注册）。
for label in $LABELS; do
  launchctl bootout "${DOMAIN}/${label}" 2>/dev/null || true
done
for label in $LABELS; do
  launchctl bootstrap "$DOMAIN" "${AGENTS}/${label}.plist"
  echo "已加载 ${label}"
done

echo ""
echo "等待服务就绪..."
sleep 12

echo "=== 服务状态 ==="
launchctl list | grep com.dandelion || true

echo ""
echo "=== 防休眠断言 ==="
pmset -g assertions 2>/dev/null | grep -E "PreventUserIdleSystemSleep|PreventSystemSleep|caffeinate" | head -8 || true

echo ""
echo "=== 连通性验证 ==="
curl -s -o /dev/null -w "后端 10088:  HTTP %{http_code}\n" http://127.0.0.1:10088/ || true
curl -s -o /dev/null -w "前端 5173:   HTTP %{http_code}\n" http://127.0.0.1:5173/ || true
curl -s -o /dev/null -w "公网地址:    HTTP %{http_code}\n" --max-time 20 https://dandelion.dreamgarden.world/ || true

echo ""
echo "完成。四个服务已后台常驻：登录自启、崩溃自恢复、系统不空闲休眠。"
echo "日志目录：${ROOT}/logs/"
echo ""
echo "合盖场景仍需一条 sudo 命令（只需执行一次）："
echo "  sudo pmset -c sleep 0 disksleep 0"

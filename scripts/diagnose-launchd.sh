#!/bin/bash
# diagnose-launchd.sh · 诊断 launchd 拒绝注册服务的真实原因。
#
# 职责：收集 bootstrap 失败的完整错误、plist 归属、目录权限与系统日志。
# 输入：无（读固定的四个 Dandelion plist）。
# 输出：诊断报告打印到终端。
# 边界：只读，不注册、不删除、不改配置。
#
# 为什么需要 sudo：launchctl 对普通用户只回一个笼统的
# "Bootstrap failed: 5: Input/output error"，真实的拒绝原因
# （TCC、权限、plist 属主、macOS 版本策略）只在系统日志里。
set -u
LABEL="$1"
DOMAIN="gui/$(id -u)"
PLIST="$HOME/Library/LaunchAgents/${LABEL}.plist"

echo "=== 1. plist 基本信息 ==="
ls -l "$PLIST" 2>&1
echo "属主应为 $(id -un):$(id -gn)，权限应含 r--"
plutil -lint "$PLIST" 2>&1

echo ""
echo "=== 2. 尝试注册（完整错误）==="
launchctl bootstrap "$DOMAIN" "$PLIST" 2>&1
echo "退出码: $?"

echo ""
echo "=== 3. 是否已注册 ==="
launchctl print "${DOMAIN}/${LABEL}" 2>&1 | head -5

echo ""
echo "=== 4. 最近 2 分钟的 launchd 日志（需要 sudo）==="
echo "若下面为空或提示无权限，请用: sudo bash $0 $LABEL"
log show --last 2m --predicate 'process == "launchd"' 2>/dev/null \
  | grep -i "${LABEL##*.}" | tail -10 || echo "（无权限读取日志，请加 sudo）"

echo ""
echo "=== 5. 系统版本 ==="
sw_vers

echo ""
echo "=== 6. 目录权限 ==="
ls -ld "$HOME/Library/LaunchAgents"

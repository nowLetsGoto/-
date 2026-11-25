#!/bin/bash
# 卸载 Dandelion 后台服务（停止服务并取消注册，不删除 plist 文件）。
#
# 职责：从 launchd 移除四个服务并停止进程（后端 / 前端 / 隧道 / 防休眠）。
# 输入：无；输出：服务停止，系统恢复可正常休眠。
# 边界：不删除 plist、不删日志、不碰项目代码——只是"不再开机自启"。
#       想彻底删除需手动 rm ~/Library/LaunchAgents/com.dandelion.*.plist
set -eu

DOMAIN="gui/$(id -u)"

for label in com.dandelion.server com.dandelion.vite com.dandelion.tunnel com.dandelion.keepawake; do
  launchctl bootout "${DOMAIN}/${label}" 2>/dev/null || true
  echo "已卸载 ${label}"
done

echo ""
echo "完成。服务已停止，不再开机自启。"

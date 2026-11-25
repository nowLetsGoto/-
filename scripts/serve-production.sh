#!/bin/bash
# Dandelion 生产服务启动脚本（供 launchd 调用）。
#
# 职责：加载 .env 的环境变量，然后 exec 生产二进制。
# 输入：仓库根的 .env（由用户维护）；输出：长期运行的服务进程。
# 边界：不构建、不改配置、不做任何业务逻辑——只负责"把环境摆好并启动"。
#
# 为什么不用 source .env：launchd 环境里没有 shell 的 source 语义保证，
# 且 .env 的值含 ? / & 等字符。这里逐行 export，只认 KEY=VALUE，忽略注释与空行。
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# 逐行读取 .env：跳过注释与空行，其余整行交给 export。
# 用 read -r 保留反斜杠，IFS= 保留行首尾空格。
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    ''|'#'*) continue ;;
  esac
  export "$line"
done < "$ROOT/.env"

# exec 让二进制接管 PID，launchd 才能正确监控与回收进程。
exec "$ROOT/bin/dandelion"

#!/usr/bin/env bash
# 检测各公司 R2 凭据能否被当前 AUTH_SECRET 解开（只读，不改库）。
#
# 为什么需要它：AUTH_SECRET 变更后密文会全部失效，但页面仍显示
# secretConfigured=true，运行时只是静默降级为本机下载。
# 这个脚本把故障明确报告出来，并指出逐家重填的修复路径。
#
# 边界：只读 system_settings。不写库、不重加密、不打印任何密钥材料。
#
# 用法：
#   bash scripts/check-r2-secret.sh              # 人可读报告
#   bash scripts/check-r2-secret.sh --json       # 机器可读
#   bash scripts/check-r2-secret.sh --use-build  # 用已部署的二进制（默认用仓库最新源码 go run）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLIST="${HOME}/Library/LaunchAgents/com.dandelion.server.plist"
USE_BUILD=0
ARGS=()

for arg in "$@"; do
  case "$arg" in
    --use-build) USE_BUILD=1 ;;
    *) ARGS+=("$arg") ;;
  esac
done

# 服务由 launchd 托管，环境变量写在 plist 里而不是 shell。
# 直接从 shell 跑会读到空值，误报「连不上数据库」——
# 必须从 plist 读回同一套环境，才能和服务看到同一个 AUTH_SECRET。
load_plist_env() {
  if [[ ! -f "${PLIST}" ]]; then
    echo "未找到 ${PLIST}；请确认服务已通过 install-autostart.sh 注册。" >&2
    return 1
  fi
  while IFS='=' read -r key value; do
    # plist 的值是字符串，去掉两端可能存在的引号。
    value="${value%\"}"; value="${value#\"}"
    export "$key=$value"
  done < <(python3 - "$PLIST" <<'PY'
import plistlib, sys
with open(sys.argv[1], 'rb') as f:
    env = plistlib.load(f).get('EnvironmentVariables', {})
for k, v in env.items():
    print(f'{k}={v}')
PY
  )
}

load_plist_env

if [[ ${USE_BUILD} -eq 1 ]]; then
  BIN="${HOME}/dandelion-runtime/dandelion"
  if [[ ! -x "${BIN}" ]]; then
    echo "未找到 ${BIN}；先去掉 --use-build 用源码运行。" >&2
    exit 1
  fi
  exec "${BIN}" r2check "${ARGS[@]}"
fi

cd "${ROOT}/server"
exec go run ./cmd/server r2check "${ARGS[@]}"

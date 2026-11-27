#!/usr/bin/env bash
# 新机器的 Cloudflare 隧道引导：把能自动的做完，把不能自动的讲清楚。
#
# 职责（按顺序）：
#   1. 检查 cloudflared 是否安装
#   2. 检查账号凭据 cert.pem（缺失 → 指引浏览器授权，不代劳）
#   3. 检查隧道是否存在（缺失 → 指引创建，不代劳）
#   4. 生成 config.yml（已有则不覆盖）
#   5. 打印 DNS 配置指引（必须去控制台，脚本改不了）
#
# 边界：不写 cert.pem、不创建隧道、不改 DNS、不装 launchd 服务。
#       这三件事都要求浏览器人工授权或控制台操作，脚本假装完成只会更糟。
#
# 用法：bash scripts/setup-cloudflare.sh
# 重复运行安全：每一步都先检查，已配置的文件不会被覆盖。
set -euo pipefail

CF_DIR="${HOME}/.cloudflared"
CF_BIN="/opt/homebrew/bin/cloudflared"
TUNNEL_NAME="dandelion"
TUNNEL_ID_FILE="${CF_DIR}/tunnel-id"
CONFIG_FILE="${CF_DIR}/config.yml"
CERT_FILE="${CF_DIR}/cert.pem"
LOCAL_PORT="${DANDELION_PORT:-10088}"
PUBLIC_HOST="${DANDELION_HOST:-dandelion.dreamgarden.world}"

# 用函数而不是满屏 echo：每段指引都带「为什么」和「怎么做」，
# 用户照着做完能理解自己在做什么，下次换机器不用再问人。
section() { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }
info()    { printf '  %s\n' "$1"; }
warn()    { printf '  \033[33m%s\033[0m\n' "$1"; }
ok()      { printf '  \033[32m✓ %s\033[0m\n' "$1"; }
action()  { printf '  \033[36m→ %s\033[0m\n' "$1"; }

section '1/5 检查 cloudflared'

if [[ ! -x "${CF_BIN}" ]]; then
  warn "未找到 ${CF_BIN}"
  action '安装：brew install cloudflare/cloudflare/cloudflared'
  exit 1
fi
ok "cloudflared 已安装 $("${CF_BIN}" --version 2>/dev/null || echo '')"

section '2/5 检查账号凭据'

mkdir -p "${CF_DIR}"
if [[ ! -f "${CERT_FILE}" ]]; then
  warn "缺少 ${CERT_FILE}"
  info "隧道登录是 OAuth：必须在浏览器里选账号、点授权，脚本无法代劳。"
  info "把下面命令输出的 URL 复制到浏览器打开，完成后 cert.pem 会自动写到上面的路径。"
  printf '\n'
  printf '    %s tunnel login\n' "${CF_BIN}"
  printf '\n'
  warn "授权完成后重新运行本脚本：bash scripts/setup-cloudflare.sh"
  exit 0   # 不是错误：这是新机器的正常第一步，停下来等用户授权
fi
ok "账号凭据存在：${CERT_FILE}"

section '3/5 检查隧道'

TUNNEL_ID=''
# 优先读本地记录：tunnel list 要联网，本地已有的 ID 不必每次去查。
if [[ -f "${TUNNEL_ID_FILE}" ]]; then
  TUNNEL_ID="$(tr -d '[:space:]' < "${TUNNEL_ID_FILE}")"
fi

if [[ -z "${TUNNEL_ID}" ]]; then
  TUNNEL_ID="$("${CF_BIN}" tunnel list 2>/dev/null \
    | awk -v name="${TUNNEL_NAME}" '$2 == name { print $1; exit }' || true)"
fi

if [[ -z "${TUNNEL_ID}" ]]; then
  warn "没有名为 ${TUNNEL_NAME} 的隧道"
  info "创建隧道需要账号授权，这一步必须你亲自执行："
  printf '\n'
  printf '    %s tunnel create %s\n' "${CF_BIN}" "${TUNNEL_NAME}"
  printf '\n'
  warn "创建后重新运行本脚本，会自动读到新隧道并写入配置。"
  exit 0   # 同样是正常的中间态，不是失败
fi
ok "隧道 ${TUNNEL_NAME}：${TUNNEL_ID}"
printf '%s' "${TUNNEL_ID}" > "${TUNNEL_ID_FILE}"

section '4/5 生成 config.yml'

if [[ -f "${CONFIG_FILE}" ]]; then
  # 不覆盖：管理员可能改过 ingress（比如加了别的域名或路径），
  # 脚本自作主张重写会把他改的东西抹掉，且没有任何提示。
  ok "已存在，保持不变：${CONFIG_FILE}"
  info "如需重建，先手动备份再删除该文件。"
else
  cat > "${CONFIG_FILE}" <<YAML
tunnel: ${TUNNEL_ID}
credentials-file: ${CF_DIR}/${TUNNEL_ID}.json
ingress:
  - hostname: ${PUBLIC_HOST}
    service: http://localhost:${LOCAL_PORT}
  - service: http_status:404
YAML
  ok "已生成：${CONFIG_FILE}"
  info "上游端口：${LOCAL_PORT}    公网域名：${PUBLIC_HOST}"
  info "改端口/域名可设 DANDELION_PORT / DANDELION_HOST 后重跑（需先删旧配置）。"
fi

section '5/5 DNS 配置（脚本无法代劳）'

info "CNAME 记录必须在 Cloudflare 控制台添加，脚本改不了你的 DNS："
printf '\n'
printf '    类型  名称                     目标\n'
printf '    CNAME %-25s %s.cfargotunnel.com\n' "${PUBLIC_HOST%%.*}" "${TUNNEL_ID}"
printf '\n'
info "打开 https://dash.cloudflare.com/ → 选中域名 dreamgarden.world"
info "→ DNS → 记录 → 添加记录，按上面三项填写，代理状态选「仅 DNS」。"

section '完成'

ok "本机配置已就绪"
info "接下来：bash scripts/install-autostart.sh   # 注册后端与隧道的开机自启"
info "验证：  ${CF_BIN} tunnel info ${TUNNEL_NAME}   # 看 CONNECTIONS 是否非空"

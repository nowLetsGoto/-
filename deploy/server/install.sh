#!/bin/bash
# install.sh · 云主机一键部署 dandelion。
#
# 职责：安装 Docker、生成配置、构建并启动 PostgreSQL + 服务、验证可用性。
# 输入：第一个参数为公网 IP（如 1.2.3.4）；同目录需有 dandelion / schema.sql /
#       data-migration.sql / Dockerfile.runtime / docker-compose.yml。
# 输出：宿主机 10088 端口对公网提供完整服务。
# 边界：不改云厂商安全组（必须在控制台放行端口）；不迁移安装包文件。
#
# 用法： sudo bash install.sh <公网IP>
set -eu

cd "$(dirname "$0")"

PUBLIC_IP="${1:-}"
if [ -z "$PUBLIC_IP" ]; then
  echo "用法: sudo bash install.sh <公网IP>" >&2
  echo "示例: sudo bash install.sh 1.2.3.4" >&2
  exit 1
fi

if ! printf '%s' "$PUBLIC_IP" | grep -qE '^[0-9]{1,3}(\.[0-9]{1,3}){3}$'; then
  echo "参数不是 IPv4 地址: $PUBLIC_IP" >&2
  exit 1
fi

for f in dandelion schema.sql Dockerfile.runtime docker-compose.yml; do
  if [ ! -f "$f" ]; then
    echo "缺少文件: $f（部署包不完整）" >&2
    exit 1
  fi
done

echo "===== 1/5 检查 Docker ====="
if ! command -v docker >/dev/null 2>&1; then
  echo "未安装 Docker，尝试用系统源安装（走内网镜像，比官方源稳）..."
  # 优先用 Ubuntu 自带源：docker.io 来自 mirrors.tencentyun.com，国内直连。
  # 不走 get.docker.com —— 它依赖 mirrors.aliyun.com，实测有连接重置的情况。
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y docker.io docker-compose-v2
  systemctl enable --now docker
else
  echo "Docker 已存在: $(docker --version)"
fi

echo ""
echo "===== 2/5 生成配置 ====="
if [ -f .env ]; then
  echo ".env 已存在，沿用现有配置（如需重设请先删除）"
else
  DB_PASSWORD="$(openssl rand -hex 16)"
  AUTH_SECRET="$(openssl rand -hex 32)"
  ADMIN_PASSWORD="Ddl_$(openssl rand -base64 12 | tr -d '/+=' | head -c 12)"
  cat > .env <<EOF
# 由 install.sh 生成于 $(date '+%Y-%m-%d %H:%M:%S')
DB_PASSWORD=${DB_PASSWORD}
PUBLIC_BASE_URL=http://${PUBLIC_IP}:10088
ADMIN_USERNAME=admin
ADMIN_PASSWORD=${ADMIN_PASSWORD}
AUTH_SECRET=${AUTH_SECRET}

# Cloudflare R2 对象存储（可选，留空则不启用，下载走本机磁盘）
# 配置方法见 R2-SETUP.md
R2_ACCOUNT_ID=
R2_ACCESS_KEY_ID=
R2_SECRET_ACCESS_KEY=
R2_BUCKET=
R2_PUBLIC_BASE_URL=
EOF
  chmod 600 .env
  echo "已生成 .env"
  echo ""
  echo "┌──────────────── 请记录管理员凭据 ────────────────"
  echo "│ 访问地址: http://${PUBLIC_IP}:10088"
  echo "│ 用户名:   admin"
  echo "│ 密码:     ${ADMIN_PASSWORD}"
  echo "└──────────────────────────────────────────────────"
fi

# PUBLIC_BASE_URL 末尾带斜杠会让后端校验失败（要求 URL 的 Path 为空）。
if grep -q "PUBLIC_BASE_URL=.*/$" .env; then
  echo "检测到 PUBLIC_BASE_URL 末尾有斜杠，自动修正" >&2
  sed -i 's#\(PUBLIC_BASE_URL=.*\)/$#\1#' .env
fi

echo ""
echo "===== 3/5 检查端口 ====="
if ss -lntp 2>/dev/null | grep -q ":10088 "; then
  echo "端口 10088 已被占用，请先释放：" >&2
  ss -lntp | grep ":10088 " >&2
  exit 1
fi
echo "端口 10088 空闲"

mkdir -p storage

echo ""
echo "===== 4/5 构建并启动 ====="
docker compose down --remove-orphans 2>/dev/null || true
docker compose build --no-cache server
docker compose up -d

echo ""
echo "===== 5/5 等待就绪 ====="
for i in $(seq 1 40); do
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:10088/" || true)"
  if [ "$code" = "200" ]; then
    echo "服务已就绪"
    break
  fi
  sleep 3
  if [ "$i" = "40" ]; then
    echo "启动超时，查看日志：" >&2
    docker compose logs --tail=40 server >&2
    exit 1
  fi
done

echo ""
echo "===== 验证 ====="
docker compose ps
echo ""
echo -n "本地 10088:    " && curl -s -o /dev/null -w "HTTP %{http_code}\n" --max-time 10 http://127.0.0.1:10088/ || true
echo -n "本地 /login:   " && curl -s -o /dev/null -w "HTTP %{http_code}\n" --max-time 10 http://127.0.0.1:10088/login || true

# 启用 R2 时提示一下，避免用户以为没生效。
if grep -qE '^R2_BUCKET=.+' .env 2>/dev/null; then
  echo -n "对象存储:      " && docker compose logs --tail=50 server 2>/dev/null | grep -o "对象存储镜像已启用.*" | tail -1 || echo "（日志中未找到，请检查配置）"
fi

echo ""
echo "════════════════════════════════════════════════════"
echo " 部署完成"
echo ""
echo " 公网访问: http://${PUBLIC_IP}:10088"
echo ""
echo " ⚠ 还需在云厂商控制台做一件事："
echo "   安全组 / 防火墙放行 TCP 10088（来源 0.0.0.0/0 或公司出口 IP）"
echo ""
echo " 常用命令："
echo "   docker compose logs -f server    # 看服务日志"
echo "   docker compose restart server    # 重启服务"
echo "   docker compose down              # 停止"
echo ""
echo " 想让下载变快（绕开本机带宽）：见 R2-SETUP.md"
echo "════════════════════════════════════════════════════"

#!/usr/bin/env bash
# One-command Linux Docker deployment for sub2api-custom. This also installs
# the host updater so the admin page can update this instance later.

set -euo pipefail

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "请使用 root 权限运行：sudo $0" >&2
  exit 1
fi

INSTALL_DIR="/opt/sub2api-custom"
SERVER_PORT="8080"
PROJECT="sub2api-custom"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --directory)
      [[ $# -ge 2 ]] || exit 2
      INSTALL_DIR="$2"
      shift 2
      ;;
    --port)
      [[ $# -ge 2 ]] || exit 2
      SERVER_PORT="$2"
      shift 2
      ;;
    --project)
      [[ $# -ge 2 ]] || exit 2
      PROJECT="$2"
      shift 2
      ;;
    -h|--help)
      echo "用法：sudo $0 [--directory /opt/sub2api-custom] [--port 8080] [--project sub2api-custom]"
      exit 0
      ;;
    *)
      echo "未知参数：$1" >&2
      exit 2
      ;;
  esac
done
IMAGE_PROJECT="$(printf '%s' "$PROJECT" | tr '[:upper:]' '[:lower:]')"

[[ "$SERVER_PORT" =~ ^[0-9]+$ && "$SERVER_PORT" -ge 1 && "$SERVER_PORT" -le 65535 ]] || {
  echo "端口必须是 1-65535。" >&2
  exit 2
}
[[ "$PROJECT" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ]] || {
  echo "Compose 项目名无效。" >&2
  exit 2
}
command -v git >/dev/null 2>&1 || { echo "请先安装 git。" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "请先安装 Docker。" >&2; exit 1; }
command -v openssl >/dev/null 2>&1 || { echo "请先安装 openssl。" >&2; exit 1; }

if [[ ! -d "$INSTALL_DIR/.git" ]]; then
  mkdir -p "$(dirname "$INSTALL_DIR")"
  git clone --branch sub2api-custom --single-branch \
    https://github.com/DeanZFC/sub2api-custom.git "$INSTALL_DIR"
fi
INSTALL_DIR="$(cd "$INSTALL_DIR" && pwd)"
DEPLOY_DIR="$INSTALL_DIR/deploy"
ENV_FILE="$DEPLOY_DIR/.env"
[[ -f "$DEPLOY_DIR/docker-compose.custom.yml" ]] || { echo "仓库缺少 Docker Compose 文件。" >&2; exit 1; }

if [[ ! -f "$ENV_FILE" ]]; then
  cp "$DEPLOY_DIR/.env.example" "$ENV_FILE"
fi
upsert_env() {
  local key="$1" value="$2"
  if grep -qE "^${key}=" "$ENV_FILE"; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
  else
    printf '\n%s=%s\n' "$key" "$value" >> "$ENV_FILE"
  fi
}
value_or_secret() {
  local key="$1" current
  current="$(sed -n "s/^${key}=//p" "$ENV_FILE" | tail -n 1)"
  if [[ -z "$current" || "$current" == "change_this_secure_password" ]]; then
    current="$(openssl rand -hex 32)"
    upsert_env "$key" "$current"
  fi
}
value_or_secret POSTGRES_PASSWORD
value_or_secret JWT_SECRET
value_or_secret TOTP_ENCRYPTION_KEY
upsert_env SERVER_PORT "$SERVER_PORT"
upsert_env SUB2API_IMAGE "${IMAGE_PROJECT}:local"
upsert_env GATEWAY_CODEX_QUOTA_OVERDRAFT_ENABLED true
upsert_env GATEWAY_OPENAI_ACCOUNT_UNIQUE_FINGERPRINT_ENABLED true
chmod 600 "$ENV_FILE"
mkdir -p "$DEPLOY_DIR/data" "$DEPLOY_DIR/postgres_data" "$DEPLOY_DIR/redis_data"

cd "$DEPLOY_DIR"
COMPOSE_ARGS=(
  -f docker-compose.local.yml
  -f docker-compose.custom.yml
)
docker compose -p "$PROJECT" "${COMPOSE_ARGS[@]}" \
  up -d --build

APP_CONTAINER="$(docker compose -p "$PROJECT" "${COMPOSE_ARGS[@]}" ps -q sub2api)"
[[ -n "$APP_CONTAINER" ]] || { echo "无法找到已启动的 sub2api 容器。" >&2; exit 1; }

"$DEPLOY_DIR/install-source-updater.sh" \
  --repo-dir "$INSTALL_DIR" \
  --container "$APP_CONTAINER"

echo "sub2api-custom 部署完成。"
echo "访问地址：http://服务器IP:${SERVER_PORT}"
echo "以后可直接在管理后台版本菜单点击更新。"

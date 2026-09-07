#!/usr/bin/env bash
# Install the source-build updater for one already-running Compose instance.
# Compose labels preserve custom project, file, and container configuration.

set -euo pipefail

usage() {
  cat <<'EOF'
用法：
  sudo ./deploy/install-source-updater.sh --repo-dir /opt/sub2api-custom --container sub2api-custom

要求：目标源码已经通过 Docker Compose 启动。
EOF
}

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "请使用 root 权限运行此脚本。" >&2
  exit 1
fi

REPO_DIR=""
CONTAINER=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-dir)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      REPO_DIR="$2"
      shift 2
      ;;
    --container)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      CONTAINER="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数：$1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ -z "$REPO_DIR" || -z "$CONTAINER" ]]; then
  usage
  exit 2
fi
REPO_DIR="$(cd "$REPO_DIR" && pwd)"
[[ -d "$REPO_DIR/.git" ]] || { echo "不是 Git 仓库：$REPO_DIR" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "未找到 docker。" >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "未找到 systemctl；此安装器只支持 Linux systemd。" >&2; exit 1; }
command -v openssl >/dev/null 2>&1 || { echo "未找到 openssl。" >&2; exit 1; }

WORKDIR="$(docker inspect "$CONTAINER" --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}')"
PROJECT="$(docker inspect "$CONTAINER" --format '{{index .Config.Labels "com.docker.compose.project"}}')"
FILES="$(docker inspect "$CONTAINER" --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}')"
APP_CONTAINER="$(docker inspect "$CONTAINER" --format '{{.Name}}' | sed 's#^/##')"
APP_IMAGE_REF="$(docker inspect "$CONTAINER" --format '{{.Config.Image}}')"

[[ "$WORKDIR" == "$REPO_DIR/deploy" ]] || {
  echo "容器 Compose 工作目录与 --repo-dir/deploy 不一致：$WORKDIR" >&2
  exit 1
}
[[ "$PROJECT" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ]] || { echo "无效 Compose 项目名。" >&2; exit 1; }
[[ "$APP_CONTAINER" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ]] || { echo "无效容器名。" >&2; exit 1; }
[[ -n "$FILES" ]] || { echo "无法读取容器的 Compose 配置文件。" >&2; exit 1; }
[[ -n "$APP_IMAGE_REF" ]] || { echo "无法读取当前应用镜像。" >&2; exit 1; }

ENV_FILE="$WORKDIR/.env"
[[ -f "$ENV_FILE" ]] || { echo "缺少环境文件：$ENV_FILE" >&2; exit 1; }
IMAGE_PROJECT="$(printf '%s' "$PROJECT" | tr '[:upper:]' '[:lower:]')"

upsert_env() {
  local key="$1" value="$2"
  if grep -qE "^${key}=" "$ENV_FILE"; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
  else
    printf '\n%s=%s\n' "$key" "$value" >> "$ENV_FILE"
  fi
}

TOKEN="$(sed -n 's/^SUB2API_UPDATER_TOKEN=//p' "$ENV_FILE" | tail -n 1)"
if [[ ! "$TOKEN" =~ ^[A-Za-z0-9_-]{32,256}$ ]]; then
  TOKEN="$(openssl rand -hex 32)"
fi
SOCKET_DIR="/run/sub2api-updater"
SOCKET_PATH="$SOCKET_DIR/$PROJECT.sock"
STATE_DIR="/var/lib/sub2api-updater/$PROJECT"
DOCKER_CONFIG_DIR="$STATE_DIR/docker"
TOKEN_DIR="/etc/sub2api-updater"
TOKEN_FILE="$TOKEN_DIR/$PROJECT.token"
SERVICE_NAME="sub2api-updater-$PROJECT"
SERVICE_FILE="/etc/systemd/system/$SERVICE_NAME.service"
INSTALL_DIR="/usr/local/libexec"
INSTALL_BIN="$INSTALL_DIR/sub2api-updater-$PROJECT"

mkdir -p "$TOKEN_DIR" "$STATE_DIR" "$DOCKER_CONFIG_DIR" "$SOCKET_DIR" "$INSTALL_DIR"
chmod 700 "$DOCKER_CONFIG_DIR"
printf '%s\n' "$TOKEN" > "$TOKEN_FILE"
chmod 600 "$TOKEN_FILE"
upsert_env SUB2API_UPDATER_TOKEN "$TOKEN"
upsert_env SUB2API_UPDATER_DIR "$SOCKET_DIR"
upsert_env SUB2API_UPDATER_SOCKET "$SOCKET_PATH"
upsert_env SUB2API_IMAGE "${IMAGE_PROJECT}:local"
chmod 600 "$ENV_FILE"
docker image tag "$APP_IMAGE_REF" "${IMAGE_PROJECT}:local"

TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT
echo "使用 Docker 构建宿主机更新器。"
BUILDER_IMAGE="sub2api-source-updater-builder:$PROJECT"
BUILDER_CONTAINER=""
cleanup_builder() {
  if [[ -n "$BUILDER_CONTAINER" ]]; then
    docker rm -f "$BUILDER_CONTAINER" >/dev/null 2>&1 || true
  fi
  rm -rf "$TEMP_DIR"
}
trap cleanup_builder EXIT
docker build -f "$REPO_DIR/deploy/Dockerfile.updater" -t "$BUILDER_IMAGE" "$REPO_DIR"
BUILDER_CONTAINER="$(docker create "$BUILDER_IMAGE")"
docker cp "$BUILDER_CONTAINER:/sub2api-updater" "$TEMP_DIR/sub2api-updater"
docker rm "$BUILDER_CONTAINER" >/dev/null
BUILDER_CONTAINER=""
install -m 0755 "$TEMP_DIR/sub2api-updater" "$INSTALL_BIN"

IFS=',' read -r -a FILE_LIST <<< "$FILES"
COMPOSE_FILES=""
for file in "${FILE_LIST[@]}"; do
  file="$(printf '%s' "$file" | xargs)"
  [[ "$file" == "$REPO_DIR/"* && -f "$file" ]] || {
    echo "Compose 文件必须位于仓库中且真实存在：$file" >&2
    exit 1
  }
  if [[ -n "$COMPOSE_FILES" ]]; then
    COMPOSE_FILES+=","
  fi
  COMPOSE_FILES+="$file"
done

systemd_quote() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "$value"
}

cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Sub2API source updater ($PROJECT)
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=$(systemd_quote "$INSTALL_BIN") --socket-path $(systemd_quote "$SOCKET_PATH") --token-file $(systemd_quote "$TOKEN_FILE") --repo-dir $(systemd_quote "$REPO_DIR") --state-dir $(systemd_quote "$STATE_DIR") --updater-binary $(systemd_quote "$INSTALL_BIN") --compose-project $(systemd_quote "$PROJECT") --compose-files $(systemd_quote "$COMPOSE_FILES") --app-service "sub2api" --postgres-service "postgres" --app-container $(systemd_quote "$APP_CONTAINER")
Environment=DOCKER_CONFIG=$(systemd_quote "$DOCKER_CONFIG_DIR")
Restart=always
RestartSec=5
User=root
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=read-only
ProtectSystem=full
ReadWritePaths=$(systemd_quote "$REPO_DIR") $(systemd_quote "$STATE_DIR") $(systemd_quote "$SOCKET_DIR") $(systemd_quote "$INSTALL_DIR")
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME.service"
systemctl restart "$SERVICE_NAME.service"

for _ in {1..20}; do
  if [[ -S "$SOCKET_PATH" ]]; then
    break
  fi
  if ! systemctl is-active --quiet "$SERVICE_NAME.service"; then
    echo "更新器服务启动失败：$SERVICE_NAME.service" >&2
    journalctl -u "$SERVICE_NAME.service" -n 30 --no-pager >&2 || true
    exit 1
  fi
  sleep 1
done
[[ -S "$SOCKET_PATH" ]] || {
  echo "更新器 Socket 未在预期时间内出现：$SOCKET_PATH" >&2
  exit 1
}

COMPOSE_ARGS=()
for file in "${FILE_LIST[@]}"; do
  file="$(printf '%s' "$file" | xargs)"
  COMPOSE_ARGS+=(-f "$file")
done
docker compose -p "$PROJECT" "${COMPOSE_ARGS[@]}" up -d --force-recreate --no-deps sub2api

echo "宿主机更新器已安装：$SERVICE_NAME"
echo "Socket：$SOCKET_PATH"
echo "查看状态：systemctl status $SERVICE_NAME.service"
echo "查看日志：journalctl -u $SERVICE_NAME.service -f"

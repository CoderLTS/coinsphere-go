#!/usr/bin/env bash
set -Eeuo pipefail

VERSION=${1:?用法: deploy.sh vX.Y.Z}
REGISTRY=${COINSPHERE_REGISTRY:-127.0.0.1:5000}
DEPLOY_DIR=${COINSPHERE_DEPLOY_DIR:-/home/infrastructure/dpanel/compose/coinsphere-go}
WEB_BIND=${COINSPHERE_WEB_BIND:-127.0.0.1}
WEB_PORT=${COINSPHERE_WEB_PORT:-8080}
DATA_VOLUME=coinsphere-backend-data
BACKUP_IMAGE=alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
SOURCE_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
DOCKER_CONFIG_FILE="${DOCKER_CONFIG:-${HOME:?HOME 未设置}/.docker}/config.json"

if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "版本号必须符合 vX.Y.Z 格式: $VERSION" >&2
  exit 2
fi
if [[ -f $DOCKER_CONFIG_FILE ]]; then
  command -v jq >/dev/null || { echo "缺少命令: jq" >&2; exit 3; }
  if ! docker_config_state=$(jq -r -s '
    if length != 1 or (.[0] | type) != "object" then "invalid"
    elif (.[0] | has("proxies")) then "proxies"
    else "clean"
    end
  ' "$DOCKER_CONFIG_FILE" 2>/dev/null); then
    docker_config_state=invalid
  fi
  case "$docker_config_state" in
    clean) ;;
    proxies)
      echo "Docker 客户端配置禁止包含全局 proxies: $DOCKER_CONFIG_FILE" >&2
      echo "请移除全局代理后再执行部署，避免代理注入运行容器。" >&2
      exit 5
      ;;
    *)
      echo "Docker 客户端配置不是单个有效 JSON 对象: $DOCKER_CONFIG_FILE" >&2
      exit 3
      ;;
  esac
fi

mkdir -p "$DEPLOY_DIR/backups"
if [[ ! -f $DEPLOY_DIR/runtime.env ]]; then
  echo "缺少 $DEPLOY_DIR/runtime.env，请先按 runtime.env.example 创建生产配置" >&2
  exit 3
fi
if ! docker network inspect infrastructure >/dev/null 2>&1; then
  echo "缺少 Docker external network: infrastructure" >&2
  exit 4
fi

next_env=$(mktemp "$DEPLOY_DIR/.env.next.XXXXXX")
previous_env=$(mktemp "$DEPLOY_DIR/.env.previous.XXXXXX")
previous_compose=$(mktemp "$DEPLOY_DIR/.compose.previous.XXXXXX.yaml")
had_previous=false
had_data_volume=false
backup_ready=false
data_may_have_changed=false
backup_file=

cleanup() {
  rm -f "$next_env" "$previous_env" "$previous_compose"
}
trap cleanup EXIT

if [[ -f $DEPLOY_DIR/.env && -f $DEPLOY_DIR/compose.yaml ]]; then
  cp "$DEPLOY_DIR/.env" "$previous_env"
  cp "$DEPLOY_DIR/compose.yaml" "$previous_compose"
  had_previous=true
fi

install -m 0644 "$SOURCE_DIR/compose.yaml" "$DEPLOY_DIR/compose.yaml"
if [[ $SOURCE_DIR != "$DEPLOY_DIR" ]]; then
  install -m 0755 "$SOURCE_DIR/deploy.sh" "$DEPLOY_DIR/deploy.sh"
  install -m 0644 "$SOURCE_DIR/runtime.env.example" "$DEPLOY_DIR/runtime.env.example"
fi
cat >"$next_env" <<EOF
COINSPHERE_VERSION=$VERSION
COINSPHERE_BACKEND_IMAGE=$REGISTRY/coinsphere/backend:$VERSION
COINSPHERE_WEB_IMAGE=$REGISTRY/coinsphere/web:$VERSION
COINSPHERE_WEB_BIND=$WEB_BIND
COINSPHERE_WEB_PORT=$WEB_PORT
EOF

compose_with() {
  local env_file=$1
  local compose_file=$2
  shift 2
  docker compose --project-directory "$DEPLOY_DIR" --env-file "$env_file" -f "$compose_file" "$@"
}

restore_data() {
  docker volume rm "$DATA_VOLUME" >/dev/null 2>&1 || true
  if $had_data_volume; then
    if ! $backup_ready; then
      echo "数据卷需要恢复，但没有完整备份" >&2
      return 1
    fi
    docker volume create "$DATA_VOLUME" >/dev/null
    docker run --rm \
      -v "$DATA_VOLUME:/data" \
      -v "$DEPLOY_DIR/backups:/backup:ro" \
      "$BACKUP_IMAGE" tar -xzf "/backup/$(basename "$backup_file")" -C /data
  fi
}

rollback() {
  local status=$1
  trap - ERR
  set +e
  echo "发布失败，开始恢复上一版本" >&2
  compose_with "$next_env" "$DEPLOY_DIR/compose.yaml" down --remove-orphans
  if $data_may_have_changed; then
    restore_data
  fi
  if $had_previous; then
    install -m 0644 "$previous_compose" "$DEPLOY_DIR/compose.yaml"
    install -m 0600 "$previous_env" "$DEPLOY_DIR/.env"
    compose_with "$DEPLOY_DIR/.env" "$DEPLOY_DIR/compose.yaml" pull
    compose_with "$DEPLOY_DIR/.env" "$DEPLOY_DIR/compose.yaml" up -d --wait --wait-timeout 180
  else
    rm -f "$DEPLOY_DIR/.env"
  fi
  exit "$status"
}
trap 'rollback $?' ERR

compose_with "$next_env" "$DEPLOY_DIR/compose.yaml" pull

if $had_previous; then
  compose_with "$previous_env" "$previous_compose" stop
fi

if docker volume inspect "$DATA_VOLUME" >/dev/null 2>&1; then
  had_data_volume=true
  backup_file="$DEPLOY_DIR/backups/sqlite-${VERSION}-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
  docker run --rm \
    -v "$DATA_VOLUME:/data:ro" \
    -v "$DEPLOY_DIR/backups:/backup" \
    "$BACKUP_IMAGE" tar -czf "/backup/$(basename "$backup_file")" -C /data .
  backup_ready=true
fi

data_may_have_changed=true
compose_with "$next_env" "$DEPLOY_DIR/compose.yaml" run --rm backend \
  /app/coinsphere-migrate -config /app/config.yml -direction up
compose_with "$next_env" "$DEPLOY_DIR/compose.yaml" up -d --wait --wait-timeout 180
curl --fail --show-error --retry 10 --retry-all-errors --retry-delay 3 \
  "http://127.0.0.1:$WEB_PORT/health" >/dev/null

install -m 0600 "$next_env" "$DEPLOY_DIR/.env"
find "$DEPLOY_DIR/backups" -maxdepth 1 -type f -name 'sqlite-*.tar.gz' -printf '%T@ %p\n' \
  | sort -nr | tail -n +11 | cut -d' ' -f2- | xargs -r rm -f --
trap - ERR
echo "CoinSphere $VERSION 发布成功"

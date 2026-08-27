#!/usr/bin/env bash
set -Eeuo pipefail

if (( $# < 1 || $# > 2 )); then
  echo "用法: deploy.sh vX.Y.Z [release-manifest.json]" >&2
  exit 2
fi

VERSION=$1
MANIFEST_FILE=${2:-}
REGISTRY=${COINSPHERE_REGISTRY:-127.0.0.1:5000}
SOURCE_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
STACK_ROOT=${COINSPHERE_STACK_ROOT:-}
DEPLOY_DIR=${COINSPHERE_DEPLOY_DIR:-}
WEB_BIND=${COINSPHERE_WEB_BIND:-127.0.0.1}
WEB_PORT=${COINSPHERE_WEB_PORT:-8080}
DOCKER_CONFIG_FILE="${DOCKER_CONFIG:-${HOME:?HOME 未设置}/.docker}/config.json"

if [[ -z $DEPLOY_DIR && -z $STACK_ROOT ]]; then
  existing_container=$(docker ps -aq --filter label=com.docker.compose.project=coinsphere-go | sed -n '1p')
  if [[ -n $existing_container ]]; then
    candidate_dir=$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' "$existing_container")
    [[ -f $candidate_dir/compose.yaml ]] && DEPLOY_DIR=$candidate_dir
  fi
fi

if [[ -z $DEPLOY_DIR && -n $STACK_ROOT ]]; then
  DEPLOY_DIR=$STACK_ROOT/compose/coinsphere-go
fi
if [[ -z $DEPLOY_DIR ]]; then
  echo "请设置 COINSPHERE_DEPLOY_DIR 或 COINSPHERE_STACK_ROOT" >&2
  exit 3
fi

if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "版本号必须符合 vX.Y.Z 格式: $VERSION" >&2
  exit 2
fi
if [[ ! $REGISTRY =~ ^[0-9A-Za-z.-]+(:[0-9]{1,5})?$ ]]; then
  echo "Registry 地址格式无效" >&2
  exit 2
fi
if [[ ! $WEB_PORT =~ ^[0-9]+$ ]] || ((WEB_PORT < 1 || WEB_PORT > 65535)); then
  echo "COINSPHERE_WEB_PORT 无效" >&2
  exit 2
fi

BACKEND_IMAGE=$REGISTRY/coinsphere/backend:$VERSION
WEB_IMAGE=$REGISTRY/coinsphere/web:$VERSION
if [[ -n $MANIFEST_FILE ]]; then
  command -v jq >/dev/null || { echo "缺少命令: jq" >&2; exit 3; }
  if [[ ! -f $MANIFEST_FILE || -L $MANIFEST_FILE ]]; then
    echo "发布 Manifest 不是普通文件" >&2
    exit 2
  fi
  if ! manifest_images=$(jq -er --arg version "$VERSION" \
    --arg backend "$REGISTRY/coinsphere/backend" --arg web "$REGISTRY/coinsphere/web" '
    def digest($repository):
      type == "string"
      and startswith($repository + "@sha256:")
      and (ltrimstr($repository + "@sha256:") | test("^[0-9a-f]{64}$"));
    if type == "object"
      and (keys == ["backendDigest", "backendImage", "commit", "version", "webDigest", "webImage"])
      and .version == $version
      and (.commit | type == "string" and test("^[0-9a-f]{40}$"))
      and .backendImage == ($backend + ":" + $version)
      and .webImage == ($web + ":" + $version)
      and (.backendDigest | digest($backend))
      and (.webDigest | digest($web))
    then .backendDigest, .webDigest
    else error("invalid release manifest")
    end
  ' "$MANIFEST_FILE" 2>/dev/null); then
    echo "发布 Manifest 与版本或 Registry 不匹配" >&2
    exit 2
  fi
  mapfile -t release_images <<<"$manifest_images"
  BACKEND_IMAGE=${release_images[0]}
  WEB_IMAGE=${release_images[1]}
fi

if [[ -f $DOCKER_CONFIG_FILE ]]; then
  command -v jq >/dev/null || { echo "缺少命令: jq" >&2; exit 3; }
  docker_config_state=$(jq -r -s '
    if length != 1 or (.[0] | type) != "object" then "invalid"
    elif (.[0] | has("proxies")) then "proxies"
    else "clean"
    end
  ' "$DOCKER_CONFIG_FILE" 2>/dev/null || printf invalid)
  case "$docker_config_state" in
    clean) ;;
    proxies)
      echo "Docker 客户端配置禁止包含全局 proxies: $DOCKER_CONFIG_FILE" >&2
      exit 5
      ;;
    *)
      echo "Docker 客户端配置不是单个有效 JSON 对象: $DOCKER_CONFIG_FILE" >&2
      exit 3
      ;;
  esac
fi

mkdir -p "$DEPLOY_DIR"
DATA_DIR=$DEPLOY_DIR/data
mkdir -p "$DATA_DIR/backend/uploads" \
  "$DATA_DIR/backend/static" "$DATA_DIR/backend/artifacts"
RUNTIME_ENV=$DEPLOY_DIR/runtime.env
if [[ ! -f $RUNTIME_ENV ]]; then
  runtime_source=${COINSPHERE_RUNTIME_ENV_FILE:-}
  if [[ -z $runtime_source && -n $STACK_ROOT ]]; then
    runtime_source=$STACK_ROOT/secrets/coinsphere-runtime.env
  fi
  if [[ -z $runtime_source || ! -f $runtime_source ]]; then
    echo "缺少 $RUNTIME_ENV，请先按 runtime.env.example 创建生产配置" >&2
    exit 3
  fi
  install -m 0600 "$runtime_source" "$RUNTIME_ENV"
fi

had_previous=false
previous_env=$(mktemp "$DEPLOY_DIR/.env.previous.XXXXXX")
previous_compose=$(mktemp "$DEPLOY_DIR/.compose.previous.XXXXXX.yaml")
next_env=$(mktemp "$DEPLOY_DIR/.env.next.XXXXXX")
if [[ -f $DEPLOY_DIR/.env && -f $DEPLOY_DIR/compose.yaml ]]; then
  cp "$DEPLOY_DIR/.env" "$previous_env"
  cp "$DEPLOY_DIR/compose.yaml" "$previous_compose"
  had_previous=true
fi

cleanup() {
  rm -f "$next_env" "$previous_env" "$previous_compose"
}
trap cleanup EXIT

if [[ $SOURCE_DIR != "$DEPLOY_DIR" ]]; then
  install -m 0644 "$SOURCE_DIR/compose.yaml" "$DEPLOY_DIR/compose.yaml"
  install -m 0755 "$SOURCE_DIR/deploy.sh" "$DEPLOY_DIR/deploy.sh"
  install -m 0644 "$SOURCE_DIR/runtime.env.example" "$DEPLOY_DIR/runtime.env.example"
fi

database_password=${COINSPHERE_DATABASE_PASSWORD:-}
if $had_previous; then
  database_password=$(sed -n 's/^COINSPHERE_DATABASE_PASSWORD=//p' "$previous_env")
fi
if [[ -z $database_password ]]; then
  command -v openssl >/dev/null || { echo "缺少命令: openssl" >&2; exit 3; }
  database_password=$(openssl rand -hex 32)
fi
if [[ ! $database_password =~ ^[0-9A-Za-z._~-]{32,128}$ ]]; then
  echo "COINSPHERE_DATABASE_PASSWORD 格式无效" >&2
  exit 3
fi

cat >"$next_env" <<EOF
COINSPHERE_VERSION=$VERSION
COINSPHERE_BACKEND_IMAGE=$BACKEND_IMAGE
COINSPHERE_WEB_IMAGE=$WEB_IMAGE
COINSPHERE_WEB_BIND=$WEB_BIND
COINSPHERE_WEB_PORT=$WEB_PORT
EOF
printf '%s=%s\n' 'COINSPHERE_DATABASE_PASSWORD' "$database_password" >>"$next_env"

compose_with() {
  local env_file=$1
  local compose_file=$2
  shift 2
  docker compose --project-name coinsphere-go --project-directory "$DEPLOY_DIR" \
    --env-file "$env_file" -f "$compose_file" "$@"
}

previous_services=()
obsolete_services=()
if $had_previous; then
  while IFS= read -r service; do
    case "$service" in
      backend|web) previous_services+=("$service") ;;
      timescaledb|worker|executor)
        previous_services+=("$service")
        obsolete_services+=("$service")
        ;;
    esac
  done < <(compose_with "$previous_env" "$previous_compose" ps --services --status running)
fi

rollback() {
  local status=$1
  trap - ERR INT TERM
  set +e
  echo "发布失败，开始恢复上一版本" >&2
  compose_with "$next_env" "$DEPLOY_DIR/compose.yaml" down --remove-orphans
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
trap 'rollback 130' INT
trap 'rollback 143' TERM

compose_with "$next_env" "$DEPLOY_DIR/compose.yaml" pull
if ((${#previous_services[@]} > 0)); then
  compose_with "$previous_env" "$previous_compose" stop "${previous_services[@]}"
fi
docker run --rm --network none --user 0:0 \
  --mount "type=bind,src=$DATA_DIR/backend,dst=/target" \
  --entrypoint sh "$BACKEND_IMAGE" -c 'chown -R app:app /target'
compose_with "$next_env" "$DEPLOY_DIR/compose.yaml" run --rm --no-deps backend \
  /app/coinsphere-migrate -config /app/config.yml -direction up

compose_with "$next_env" "$DEPLOY_DIR/compose.yaml" up -d --wait --wait-timeout 180 backend web
curl --fail --show-error --retry 10 --retry-all-errors --retry-delay 3 \
  "http://127.0.0.1:$WEB_PORT/health" >/dev/null
if ((${#obsolete_services[@]} > 0)); then
  compose_with "$previous_env" "$previous_compose" rm -f "${obsolete_services[@]}"
fi

install -m 0600 "$next_env" "$DEPLOY_DIR/.env"
trap - ERR INT TERM
echo "CoinSphere $VERSION 已发布到独立 Compose 项目 coinsphere-go"

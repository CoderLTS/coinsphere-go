#!/usr/bin/env bash
set -Eeuo pipefail

if (( $# != 2 )); then
  echo "用法: deploy-dpanel-stack.sh vX.Y.Z release-manifest.json" >&2
  exit 2
fi

VERSION=$1
MANIFEST_FILE=$2
REGISTRY=${COINSPHERE_REGISTRY:-127.0.0.1:5000}
STACK_ROOT=${COINSPHERE_STACK_ROOT:-/srv/dpanel-stack}
COMPOSE_DIR=$STACK_ROOT/compose/apps
COMPOSE_FILE=$COMPOSE_DIR/docker-compose.yaml
ENV_FILE=$STACK_ROOT/secrets/apps.env
RUNTIME_ENV_FILE=$STACK_ROOT/secrets/coinsphere-runtime.env
WORKER_RUNTIME_ENV_FILE=$STACK_ROOT/secrets/coinsphere-worker-runtime.env
EXECUTOR_RUNTIME_ENV_FILE=$STACK_ROOT/secrets/coinsphere-executor-runtime.env
DOCKER_CONFIG_FILE="${DOCKER_CONFIG:-${HOME:?HOME 未设置}/.docker}/config.json"
SERVICES=(coinsphere-backend coinsphere-web coinsphere-worker coinsphere-worker-backtest coinsphere-executor)
PREVIOUS_SERVICES=(coinsphere-backend coinsphere-web)

if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "版本号必须符合 vX.Y.Z 格式: $VERSION" >&2
  exit 2
fi
if [[ ! $REGISTRY =~ ^[0-9A-Za-z.-]+(:[0-9]{1,5})?$ ]]; then
  echo "Registry 地址格式无效" >&2
  exit 2
fi
for command_name in curl docker jq; do
  command -v "$command_name" >/dev/null || { echo "缺少命令: $command_name" >&2; exit 3; }
done
for regular_file in "$MANIFEST_FILE" "$COMPOSE_FILE" "$ENV_FILE" "$RUNTIME_ENV_FILE" "$WORKER_RUNTIME_ENV_FILE" "$EXECUTOR_RUNTIME_ENV_FILE"; do
  if [[ ! -f $regular_file || -L $regular_file ]]; then
    echo "发布输入不是普通文件: $regular_file" >&2
    exit 3
  fi
done

if ! manifest_images=$(jq -er --arg version "$VERSION" \
  --arg backend "$REGISTRY/coinsphere/backend" --arg web "$REGISTRY/coinsphere/web" \
  --arg worker "$REGISTRY/coinsphere/worker" '
  def digest($repository):
    type == "string"
    and startswith($repository + "@sha256:")
    and (ltrimstr($repository + "@sha256:") | test("^[0-9a-f]{64}$"));
  if type == "object"
    and (keys == ["backendDigest", "backendImage", "commit", "version", "webDigest", "webImage", "workerDigest", "workerImage"])
    and .version == $version
    and (.commit | type == "string" and test("^[0-9a-f]{40}$"))
    and .backendImage == ($backend + ":" + $version)
    and .webImage == ($web + ":" + $version)
    and .workerImage == ($worker + ":" + $version)
    and (.backendDigest | digest($backend))
    and (.webDigest | digest($web))
    and (.workerDigest | digest($worker))
  then .backendDigest, .webDigest, .workerDigest
  else error("invalid release manifest")
  end
' "$MANIFEST_FILE" 2>/dev/null); then
  echo "发布 Manifest 与版本或 Registry 不匹配" >&2
  exit 2
fi
mapfile -t release_images <<<"$manifest_images"
if [[ ${#release_images[@]} -ne 3 ]]; then
  echo "发布 Manifest 镜像字段无效" >&2
  exit 2
fi
BACKEND_IMAGE=${release_images[0]}
WEB_IMAGE=${release_images[1]}
WORKER_IMAGE=${release_images[2]}

if [[ -f $DOCKER_CONFIG_FILE ]]; then
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
      exit 5
      ;;
    *)
      echo "Docker 客户端配置不是单个有效 JSON 对象: $DOCKER_CONFIG_FILE" >&2
      exit 3
      ;;
  esac
fi

for key in COINSPHERE_VERSION COINSPHERE_BACKEND_IMAGE COINSPHERE_WEB_IMAGE; do
  count=$(grep -c "^$key=" "$ENV_FILE" || true)
  if [[ $count -ne 1 ]]; then
    echo "$ENV_FILE 必须且只能包含一个 $key" >&2
    exit 3
  fi
done
worker_image_count=$(grep -c '^COINSPHERE_WORKER_IMAGE=' "$ENV_FILE" || true)
if [[ $worker_image_count -gt 1 ]]; then
  echo "$ENV_FILE 最多包含一个 COINSPHERE_WORKER_IMAGE" >&2
  exit 3
fi
worker_was_deployed=false
if [[ $worker_image_count -eq 1 ]]; then
  worker_was_deployed=true
  PREVIOUS_SERVICES+=(coinsphere-worker coinsphere-worker-backtest)
fi
executor_enabled_count=$(grep -c '^COINSPHERE_PAPER_EXECUTOR_ENABLED=' "$ENV_FILE" || true)
if [[ $executor_enabled_count -gt 1 ]]; then
  echo "$ENV_FILE 最多包含一个 COINSPHERE_PAPER_EXECUTOR_ENABLED" >&2
  exit 3
fi
if [[ $executor_enabled_count -eq 1 ]] && grep -Fxq 'COINSPHERE_PAPER_EXECUTOR_ENABLED=1' "$ENV_FILE"; then
  PREVIOUS_SERVICES+=(coinsphere-executor)
fi
WEB_PORT=$(sed -n 's/^COINSPHERE_WEB_PORT=//p' "$ENV_FILE" | tail -n 1)
WEB_PORT=${WEB_PORT:-8080}
if [[ ! $WEB_PORT =~ ^[0-9]+$ ]] || ((WEB_PORT < 1 || WEB_PORT > 65535)); then
  echo "COINSPHERE_WEB_PORT 无效" >&2
  exit 3
fi

next_env=$(mktemp "$(dirname "$ENV_FILE")/.apps.env.next.XXXXXX")
previous_env=$(mktemp "$(dirname "$ENV_FILE")/.apps.env.previous.XXXXXX")
services_stopped=false
cleanup() {
  rm -f "$next_env" "$previous_env"
}
trap cleanup EXIT
cp -p "$ENV_FILE" "$next_env"
cp -p "$ENV_FILE" "$previous_env"
if ! $worker_was_deployed; then
  printf 'COINSPHERE_WORKER_IMAGE=%s\n' "$WORKER_IMAGE" >>"$next_env"
  printf 'COINSPHERE_WORKER_IMAGE=%s\n' "$WORKER_IMAGE" >>"$previous_env"
fi
if [[ $executor_enabled_count -eq 0 ]]; then
  printf 'COINSPHERE_PAPER_EXECUTOR_ENABLED=1\n' >>"$next_env"
fi
sed -i \
  -e "s|^COINSPHERE_VERSION=.*$|COINSPHERE_VERSION=$VERSION|" \
  -e "s|^COINSPHERE_BACKEND_IMAGE=.*$|COINSPHERE_BACKEND_IMAGE=$BACKEND_IMAGE|" \
  -e "s|^COINSPHERE_WEB_IMAGE=.*$|COINSPHERE_WEB_IMAGE=$WEB_IMAGE|" \
  -e "s|^COINSPHERE_WORKER_IMAGE=.*$|COINSPHERE_WORKER_IMAGE=$WORKER_IMAGE|" \
  "$next_env"

compose_with() {
  local env_file=$1
  shift
  docker compose --project-name apps --project-directory "$COMPOSE_DIR" \
    --env-file "$env_file" -f "$COMPOSE_FILE" "$@"
}

rollback() {
  local status=$1
  trap - ERR INT TERM
  set +e
  echo "发布失败，开始恢复 CoinSphere 上一版本" >&2
  if $services_stopped; then
    compose_with "$next_env" stop "${SERVICES[@]}" >/dev/null 2>&1 || true
    compose_with "$next_env" rm -f "${SERVICES[@]}" >/dev/null 2>&1 || true
    compose_with "$previous_env" pull "${PREVIOUS_SERVICES[@]}" || echo "上一版本镜像拉取失败，将尝试使用本地镜像" >&2
    compose_with "$previous_env" up -d --no-deps --wait --wait-timeout 180 "${PREVIOUS_SERVICES[@]}" \
      || echo "自动恢复失败，请按发布 Runbook 手工处理" >&2
  fi
  exit "$status"
}
trap 'rollback $?' ERR
trap 'rollback 130' INT
trap 'rollback 143' TERM

compose_with "$next_env" pull "${SERVICES[@]}"
services_stopped=true
compose_with "$previous_env" stop "${PREVIOUS_SERVICES[@]}"
# Release fixed network addresses before Compose creates replacement containers.
compose_with "$previous_env" rm -f "${PREVIOUS_SERVICES[@]}"
docker run --rm --network dpanel_stack --env-file "$RUNTIME_ENV_FILE" "$BACKEND_IMAGE" \
  /app/coinsphere-migrate -config /app/config.yml -direction up
compose_with "$next_env" up -d --no-deps --wait --wait-timeout 180 "${SERVICES[@]}"
curl --fail --show-error --retry 10 --retry-all-errors --retry-delay 3 \
  "http://127.0.0.1:$WEB_PORT/health" >/dev/null

mv -f "$next_env" "$ENV_FILE"
trap - ERR INT TERM
echo "CoinSphere $VERSION 已发布到共享 DPanel Stack"

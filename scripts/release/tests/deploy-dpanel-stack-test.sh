#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
STACK_ROOT=$TEST_DIR/stack
VERSION=v1.2.3
REGISTRY=127.0.0.1:5000
BACKEND_DIGEST=$(printf 'a%.0s' {1..64})
WEB_DIGEST=$(printf 'b%.0s' {1..64})
WORKER_DIGEST=$(printf 'c%.0s' {1..64})

cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

command -v jq >/dev/null || { echo "缺少命令: jq" >&2; exit 3; }
mkdir -p "$TEST_DIR/bin" "$STACK_ROOT/compose/apps" "$STACK_ROOT/secrets"
printf 'COINSPHERE_DATABASE__DSN=postgresql://test-only\n' >"$STACK_ROOT/secrets/coinsphere-runtime.env"
printf 'COINSPHERE_WORKER_DATABASE_DSN=postgresql://worker-test-only\n' >"$STACK_ROOT/secrets/coinsphere-worker-runtime.env"
printf 'COINSPHERE_DATABASE__DSN=postgresql://executor-test-only\n' >"$STACK_ROOT/secrets/coinsphere-executor-runtime.env"
cat >"$STACK_ROOT/compose/apps/docker-compose.yaml" <<'EOF'
name: apps
services:
  coinsphere-backend:
    image: ${COINSPHERE_BACKEND_IMAGE}
  coinsphere-web:
    image: ${COINSPHERE_WEB_IMAGE}
  coinsphere-worker:
    image: ${COINSPHERE_WORKER_IMAGE}
  coinsphere-worker-backtest:
    image: ${COINSPHERE_WORKER_IMAGE}
  coinsphere-executor:
    image: ${COINSPHERE_BACKEND_IMAGE}
  new-api:
    image: example/new-api:latest
EOF
cat >"$TEST_DIR/release-manifest.json" <<EOF
{
  "version": "$VERSION",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "backendImage": "$REGISTRY/coinsphere/backend:$VERSION",
  "backendDigest": "$REGISTRY/coinsphere/backend@sha256:$BACKEND_DIGEST",
  "webImage": "$REGISTRY/coinsphere/web:$VERSION",
  "webDigest": "$REGISTRY/coinsphere/web@sha256:$WEB_DIGEST",
  "workerImage": "$REGISTRY/coinsphere/worker:$VERSION",
  "workerDigest": "$REGISTRY/coinsphere/worker@sha256:$WORKER_DIGEST"
}
EOF
cat >"$TEST_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$DEPLOY_DOCKER_LOG"
exit 0
EOF
cat >"$TEST_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
[[ ${FAIL_HEALTH:-false} != true ]]
EOF
chmod +x "$TEST_DIR/bin/docker" "$TEST_DIR/bin/curl"

write_old_env() {
  local include_worker=${1:-false}
  local include_executor=${2:-false}
  cat >"$STACK_ROOT/secrets/apps.env" <<'EOF'
COINSPHERE_VERSION=v1.0.0
COINSPHERE_BACKEND_IMAGE=127.0.0.1:5000/coinsphere/backend@sha256:old-backend
COINSPHERE_WEB_IMAGE=127.0.0.1:5000/coinsphere/web@sha256:old-web
EOF
  if $include_worker; then
    printf '%s\n' 'COINSPHERE_WORKER_IMAGE=127.0.0.1:5000/coinsphere/worker@sha256:old-worker' >>"$STACK_ROOT/secrets/apps.env"
  fi
  if $include_executor; then
    printf '%s\n' 'COINSPHERE_PAPER_EXECUTOR_ENABLED=1' >>"$STACK_ROOT/secrets/apps.env"
  fi
  cat >>"$STACK_ROOT/secrets/apps.env" <<'EOF'
COINSPHERE_WEB_PORT=8080
SESSION_SECRET=test-secret-must-survive
EOF
}

export PATH="$TEST_DIR/bin:$PATH"
export DEPLOY_DOCKER_LOG="$TEST_DIR/docker.log"
export DOCKER_CONFIG="$TEST_DIR/docker-config"
export COINSPHERE_STACK_ROOT="$STACK_ROOT"

write_old_env false false
: >"$DEPLOY_DOCKER_LOG"
status=0
FAIL_HEALTH=true bash "$ROOT_DIR/scripts/release/deploy-dpanel-stack.sh" \
  "$VERSION" "$TEST_DIR/release-manifest.json" >"$TEST_DIR/first-worker-rollback.log" 2>&1 || status=$?
if [[ $status -eq 0 ]] || ! grep -Fxq 'COINSPHERE_VERSION=v1.0.0' "$STACK_ROOT/secrets/apps.env" \
  || grep -q '^COINSPHERE_WORKER_IMAGE=' "$STACK_ROOT/secrets/apps.env"; then
  echo "首次 Worker 发布失败时必须保留原 Backend/Web 环境" >&2
  exit 1
fi
if ! grep -Eq 'up -d --no-deps --wait --wait-timeout 180 coinsphere-backend coinsphere-web$' "$DEPLOY_DOCKER_LOG"; then
  echo "首次 Worker 发布失败时必须只恢复原 Backend/Web" >&2
  exit 1
fi

if ! grep -Fq 'rm -f coinsphere-backend coinsphere-web' "$DEPLOY_DOCKER_LOG"; then
  echo "release must remove the stopped Backend/Web containers before replacement" >&2
  exit 1
fi
if ! grep -Fq 'rm -f coinsphere-backend coinsphere-web coinsphere-worker coinsphere-worker-backtest coinsphere-executor' "$DEPLOY_DOCKER_LOG"; then
  echo "rollback must remove candidate CoinSphere containers before replacement" >&2
  exit 1
fi

write_old_env false false
: >"$DEPLOY_DOCKER_LOG"
compose_checksum=$(sha256sum "$STACK_ROOT/compose/apps/docker-compose.yaml")

bash "$ROOT_DIR/scripts/release/deploy-dpanel-stack.sh" "$VERSION" "$TEST_DIR/release-manifest.json"
if ! grep -Fxq "COINSPHERE_BACKEND_IMAGE=$REGISTRY/coinsphere/backend@sha256:$BACKEND_DIGEST" "$STACK_ROOT/secrets/apps.env" \
  || ! grep -Fxq "COINSPHERE_WEB_IMAGE=$REGISTRY/coinsphere/web@sha256:$WEB_DIGEST" "$STACK_ROOT/secrets/apps.env" \
  || ! grep -Fxq "COINSPHERE_WORKER_IMAGE=$REGISTRY/coinsphere/worker@sha256:$WORKER_DIGEST" "$STACK_ROOT/secrets/apps.env" \
  || ! grep -Fxq 'COINSPHERE_PAPER_EXECUTOR_ENABLED=1' "$STACK_ROOT/secrets/apps.env" \
  || ! grep -Fxq 'SESSION_SECRET=test-secret-must-survive' "$STACK_ROOT/secrets/apps.env"; then
  echo "共享环境文件必须只更新 CoinSphere 版本和镜像" >&2
  exit 1
fi
if [[ $(sha256sum "$STACK_ROOT/compose/apps/docker-compose.yaml") != "$compose_checksum" ]]; then
  echo "发布不得覆盖共享 Compose" >&2
  exit 1
fi

write_old_env true true
: >"$DEPLOY_DOCKER_LOG"
status=0
FAIL_HEALTH=true bash "$ROOT_DIR/scripts/release/deploy-dpanel-stack.sh" \
  "$VERSION" "$TEST_DIR/release-manifest.json" >"$TEST_DIR/rollback.log" 2>&1 || status=$?
if [[ $status -eq 0 ]] || ! grep -Fxq 'COINSPHERE_VERSION=v1.0.0' "$STACK_ROOT/secrets/apps.env"; then
  echo "健康检查失败时必须恢复上一版本环境文件" >&2
  exit 1
fi
if grep -Eq '(^| )down( |$)|(^| )new-api( |$)' "$DEPLOY_DOCKER_LOG"; then
  echo "共享 Stack 发布不得执行 down 或操作 new-api" >&2
  exit 1
fi
if [[ $(grep -Fc 'rm -f coinsphere-backend coinsphere-web coinsphere-worker coinsphere-worker-backtest coinsphere-executor' "$DEPLOY_DOCKER_LOG") -ne 2 ]]; then
  echo "release and rollback must each remove stopped CoinSphere containers" >&2
  exit 1
fi
if ! grep -Fq 'stop coinsphere-backend coinsphere-web coinsphere-worker coinsphere-worker-backtest coinsphere-executor' "$DEPLOY_DOCKER_LOG" \
  || ! grep -Fq 'rm -f coinsphere-backend coinsphere-web coinsphere-worker coinsphere-worker-backtest coinsphere-executor' "$DEPLOY_DOCKER_LOG" \
  || ! grep -Fq 'up -d --no-deps --wait --wait-timeout 180 coinsphere-backend coinsphere-web coinsphere-worker coinsphere-worker-backtest coinsphere-executor' "$DEPLOY_DOCKER_LOG" \
  || ! grep -Fq 'run --rm --network dpanel_stack --env-file' "$DEPLOY_DOCKER_LOG" \
  || ! grep -Fq "$REGISTRY/coinsphere/backend@sha256:$BACKEND_DIGEST" "$DEPLOY_DOCKER_LOG" \
  || ! grep -Fq '/app/coinsphere-migrate' "$DEPLOY_DOCKER_LOG"; then
  echo "发布和回滚必须只操作 CoinSphere 服务" >&2
  exit 1
fi

echo "共享 DPanel Stack 发布测试通过"

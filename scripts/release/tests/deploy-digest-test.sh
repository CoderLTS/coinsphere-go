#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
VERSION=v1.2.3
COMMIT=0123456789abcdef0123456789abcdef01234567
REGISTRY=127.0.0.1:5000
BACKEND_DIGEST=$(printf 'a%.0s' {1..64})
WEB_DIGEST=$(printf 'b%.0s' {1..64})
WORKER_DIGEST=$(printf 'c%.0s' {1..64})
POSTGRES_DSN='postgresql://coinsphere:test-only-password@timescaledb:5432/coinsphere?sslmode=disable&options=-csearch_path%3Dpublic'
WORKER_POSTGRES_DSN='postgresql://coinsphere_worker:test-only-password@timescaledb:5432/coinsphere?sslmode=disable&options=-csearch_path%3Dpublic'
EXECUTOR_POSTGRES_DSN='postgresql://coinsphere_executor:test-only-password@timescaledb:5432/coinsphere?sslmode=disable&options=-csearch_path%3Dpublic'

cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

command -v jq >/dev/null || { echo "缺少命令: jq" >&2; exit 3; }
mkdir -p "$TEST_DIR/bin" "$TEST_DIR/deploy"
printf 'COINSPHERE_DATABASE__DSN=%s\nCOINSPHERE_AUTH__SECRET_KEY=replace-with-random-value\n' \
  "$POSTGRES_DSN" >"$TEST_DIR/deploy/runtime.env"
printf 'COINSPHERE_WORKER_DATABASE_DSN=%s\n' \
  "$WORKER_POSTGRES_DSN" >"$TEST_DIR/deploy/worker-runtime.env"
printf 'COINSPHERE_DATABASE__DSN=%s\n' \
  "$EXECUTOR_POSTGRES_DSN" >"$TEST_DIR/deploy/executor-runtime.env"
cat >"$TEST_DIR/release-manifest.json" <<EOF
{
  "version": "$VERSION",
  "commit": "$COMMIT",
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
set -Eeuo pipefail
printf '%s\n' "$*" >>"$DEPLOY_DOCKER_LOG"
if [[ ${1:-} == volume || $* == *sqlite* || $* == *backup* ]]; then
  echo "部署不得操作旧 SQLite 卷或备份" >&2
  exit 97
fi
exit 0
EOF
cat >"$TEST_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$TEST_DIR/bin/docker" "$TEST_DIR/bin/curl"

export PATH="$TEST_DIR/bin:$PATH"
export DEPLOY_DOCKER_LOG="$TEST_DIR/docker.log"
DOCKER_CONFIG="$TEST_DIR/docker-config" \
COINSPHERE_REGISTRY=$REGISTRY \
COINSPHERE_DEPLOY_DIR="$TEST_DIR/deploy" \
bash "$ROOT_DIR/deploy/production/deploy.sh" "$VERSION" "$TEST_DIR/release-manifest.json" \
  >"$TEST_DIR/deploy.log" 2>&1

if ! grep -Fxq "COINSPHERE_BACKEND_IMAGE=$REGISTRY/coinsphere/backend@sha256:$BACKEND_DIGEST" "$TEST_DIR/deploy/.env" \
  || ! grep -Fxq "COINSPHERE_WEB_IMAGE=$REGISTRY/coinsphere/web@sha256:$WEB_DIGEST" "$TEST_DIR/deploy/.env" \
  || ! grep -Fxq "COINSPHERE_WORKER_IMAGE=$REGISTRY/coinsphere/worker@sha256:$WORKER_DIGEST" "$TEST_DIR/deploy/.env"; then
  echo "自动部署必须使用 Manifest 中的不可变镜像 digest" >&2
  exit 1
fi
if ! grep -Fxq "COINSPHERE_DATABASE__DSN=$POSTGRES_DSN" "$TEST_DIR/deploy/runtime.env"; then
  echo "生产部署必须保留 PostgreSQL DSN" >&2
  exit 1
fi
if ! grep -Fxq "COINSPHERE_WORKER_DATABASE_DSN=$WORKER_POSTGRES_DSN" "$TEST_DIR/deploy/worker-runtime.env"; then
  echo "生产部署必须保留 Worker 独立 PostgreSQL DSN" >&2
  exit 1
fi
if ! grep -Fxq "COINSPHERE_DATABASE__DSN=$EXECUTOR_POSTGRES_DSN" "$TEST_DIR/deploy/executor-runtime.env"; then
  echo "生产部署必须保留 Executor 独立 PostgreSQL DSN" >&2
  exit 1
fi
if ! grep -Fq "run --rm backend /app/coinsphere-migrate -config /app/config.yml -direction up" "$DEPLOY_DOCKER_LOG"; then
  echo "启动服务前必须通过后端镜像执行 PostgreSQL migration" >&2
  exit 1
fi
if grep -Eiq '(^|[[:space:]])volume([[:space:]]|$)|sqlite|backup' "$DEPLOY_DOCKER_LOG" \
  || find "$TEST_DIR/deploy" -maxdepth 1 -type f \( -iname '*sqlite*' -o -iname '*.db*' -o -iname '*backup*' \) -print -quit | grep -q .; then
  echo "生产部署不得创建或操作旧 SQLite 卷与备份" >&2
  exit 1
fi

cat >"$TEST_DIR/invalid-manifest.json" <<EOF
{"version":"$VERSION","backendDigest":"$REGISTRY/coinsphere/backend:latest"}
EOF
status=0
DOCKER_CONFIG="$TEST_DIR/docker-config" \
COINSPHERE_REGISTRY=$REGISTRY \
COINSPHERE_DEPLOY_DIR="$TEST_DIR/rejected" \
bash "$ROOT_DIR/deploy/production/deploy.sh" "$VERSION" "$TEST_DIR/invalid-manifest.json" \
  >"$TEST_DIR/rejected.log" 2>&1 || status=$?
if [[ $status -ne 2 || -e $TEST_DIR/rejected ]]; then
  echo "无效 Manifest 必须在部署前 fail-closed" >&2
  exit 1
fi

echo "生产部署镜像 digest 绑定测试通过"

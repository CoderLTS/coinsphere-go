#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
VERSION=v1.2.3
COMMIT=0123456789abcdef0123456789abcdef01234567
REGISTRY=127.0.0.1:5000
BACKEND_DIGEST=$(printf 'a%.0s' {1..64})
WEB_DIGEST=$(printf 'b%.0s' {1..64})
POSTGRES_DSN='postgresql://coinsphere:test-only-password@timescaledb:5432/coinsphere?sslmode=disable&options=-csearch_path%3Dpublic'

cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

command -v jq >/dev/null || { echo "缺少命令: jq" >&2; exit 3; }
DEPLOY_DIR="$TEST_DIR/stack/compose/coinsphere-go"
mkdir -p "$TEST_DIR/bin" "$TEST_DIR/stack/compose/apps" "$TEST_DIR/stack/secrets"
touch "$TEST_DIR/stack/compose/apps/docker-compose.yaml" "$TEST_DIR/stack/secrets/apps.env"
printf 'COINSPHERE_DATABASE__DSN=%s\nCOINSPHERE_AUTH__SECRET_KEY=replace-with-random-value\n' \
  "$POSTGRES_DSN" >"$TEST_DIR/stack/secrets/coinsphere-runtime.env"
cat >"$TEST_DIR/release-manifest.json" <<EOF
{
  "version": "$VERSION",
  "commit": "$COMMIT",
  "backendImage": "$REGISTRY/coinsphere/backend:$VERSION",
  "backendDigest": "$REGISTRY/coinsphere/backend@sha256:$BACKEND_DIGEST",
  "webImage": "$REGISTRY/coinsphere/web:$VERSION",
  "webDigest": "$REGISTRY/coinsphere/web@sha256:$WEB_DIGEST"
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
if [[ ${1:-} == ps && $* == *com.docker.compose.project=coinsphere-go* ]]; then
  exit 0
fi
if [[ ${1:-} == ps && $* == *com.docker.compose.service=coinsphere-backend* ]]; then
  printf 'legacy-backend\n'
  exit 0
fi
if [[ ${1:-} == inspect ]]; then
  printf '%s\n' "$MOCK_LEGACY_WORKING_DIR"
  exit 0
fi
if [[ ${1:-} == compose && $* == *'config --services'* ]]; then
  printf 'coinsphere-backend\ncoinsphere-executor\ncoinsphere-timescaledb\nsub2api\n'
  exit 0
fi
if [[ ${1:-} == compose && $* == *'ps --services --status running'* ]]; then
  printf 'coinsphere-backend\ncoinsphere-executor\ncoinsphere-timescaledb\nsub2api\n'
  exit 0
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
export MOCK_LEGACY_WORKING_DIR="$TEST_DIR/stack/compose/apps"
DOCKER_CONFIG="$TEST_DIR/docker-config" \
COINSPHERE_REGISTRY=$REGISTRY \
bash "$ROOT_DIR/deploy/production/deploy.sh" "$VERSION" "$TEST_DIR/release-manifest.json" \
  >"$TEST_DIR/deploy.log" 2>&1

if ! grep -Fxq "COINSPHERE_BACKEND_IMAGE=$REGISTRY/coinsphere/backend@sha256:$BACKEND_DIGEST" "$DEPLOY_DIR/.env" \
  || ! grep -Fxq "COINSPHERE_WEB_IMAGE=$REGISTRY/coinsphere/web@sha256:$WEB_DIGEST" "$DEPLOY_DIR/.env"; then
  echo "自动部署必须使用 Manifest 中的不可变镜像 digest" >&2
  exit 1
fi
if ! grep -Fxq "COINSPHERE_DATABASE__DSN=$POSTGRES_DSN" "$DEPLOY_DIR/runtime.env"; then
  echo "生产部署必须保留 Backend 运行配置" >&2
  exit 1
fi
if ! grep -Eq '^COINSPHERE_DATABASE_PASSWORD=[0-9a-f]{64}$' "$DEPLOY_DIR/.env"; then
  echo "独立 Compose 必须生成并保存数据库密码" >&2
  exit 1
fi
if ! grep -Fq -- '--project-name coinsphere-go' "$DEPLOY_DOCKER_LOG"; then
  echo "生产部署必须使用独立 Compose 项目" >&2
  exit 1
fi
if ! grep -Fq "run --rm --no-deps backend /app/coinsphere-migrate -config /app/config.yml -direction up" "$DEPLOY_DOCKER_LOG"; then
  echo "启动服务前必须通过后端镜像执行 PostgreSQL migration" >&2
  exit 1
fi
if ! grep -Fq "stop coinsphere-backend coinsphere-executor coinsphere-timescaledb" "$DEPLOY_DOCKER_LOG" \
  || ! grep -Fq "rm -f coinsphere-backend coinsphere-executor coinsphere-timescaledb" "$DEPLOY_DOCKER_LOG"; then
  echo "首次独立部署必须只移除旧 CoinSphere 服务" >&2
  exit 1
fi
if grep -Eq '(stop|rm -f).*sub2api' "$DEPLOY_DOCKER_LOG"; then
  echo "首次独立部署不得操作 sub2api" >&2
  exit 1
fi
if grep -Eiq '(^|[[:space:]])volume([[:space:]]|$)|sqlite|backup' "$DEPLOY_DOCKER_LOG" \
  || find "$DEPLOY_DIR" -maxdepth 1 -type f \( -iname '*sqlite*' -o -iname '*.db*' -o -iname '*backup*' \) -print -quit | grep -q .; then
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

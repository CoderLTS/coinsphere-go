#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
VERSION=v1.2.3
COMMIT=0123456789abcdef0123456789abcdef01234567
REGISTRY=127.0.0.1:5000
BACKEND_DIGEST=$(printf 'a%.0s' {1..64})
WEB_DIGEST=$(printf 'b%.0s' {1..64})

cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

command -v jq >/dev/null || { echo "缺少命令: jq" >&2; exit 3; }
mkdir -p "$TEST_DIR/bin" "$TEST_DIR/deploy"
printf 'COINSPHERE_AUTH__SECRET_KEY=replace-with-random-value\n' >"$TEST_DIR/deploy/runtime.env"
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
if [[ ${1:-} == volume && ${2:-} == inspect ]]; then
  exit 1
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
  || ! grep -Fxq "COINSPHERE_WEB_IMAGE=$REGISTRY/coinsphere/web@sha256:$WEB_DIGEST" "$TEST_DIR/deploy/.env"; then
  echo "自动部署必须使用 Manifest 中的不可变镜像 digest" >&2
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

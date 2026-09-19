#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

mkdir -p "$TEST_DIR/bin" "$TEST_DIR/docker"
cat >"$TEST_DIR/docker/config.json" <<'EOF'
{"proxies":{"default":{"httpProxy":"http://127.0.0.1:17890"}}}
EOF
cat >"$TEST_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$DEPLOY_DOCKER_LOG"
exit 1
EOF
chmod +x "$TEST_DIR/bin/docker"

export PATH="$TEST_DIR/bin:$PATH"
export DEPLOY_DOCKER_LOG="$TEST_DIR/docker.log"
status=0
DOCKER_CONFIG="$TEST_DIR/docker" \
COINSPHERE_DEPLOY_DIR="$TEST_DIR/deploy" \
bash "$ROOT_DIR/deploy/production/deploy.sh" v1.2.3 \
  >"$TEST_DIR/deploy.log" 2>&1 || status=$?

if [[ $status -ne 5 ]]; then
  echo "Docker 全局代理应以退出码 5 阻止部署" >&2
  exit 1
fi
if [[ -e $DEPLOY_DOCKER_LOG || -e $TEST_DIR/deploy ]]; then
  echo "拒绝全局代理后不得调用 Docker 或创建部署目录" >&2
  exit 1
fi

echo "生产部署代理隔离测试通过"

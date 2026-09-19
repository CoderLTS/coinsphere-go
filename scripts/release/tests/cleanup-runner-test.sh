#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
cleanup() {
  rm -rf -- "$TEST_DIR" "$ROOT_DIR/dist"
}
trap cleanup EXIT

mkdir -p "$TEST_DIR/bin" "$TEST_DIR/runner-temp" "$ROOT_DIR/dist"
touch "$ROOT_DIR/dist/release.tmp" "$TEST_DIR/runner-temp/fresh.tmp"
touch -d '2 days ago' "$TEST_DIR/runner-temp/stale.tmp"

cat >"$TEST_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ ${1:-} == buildx && ${2:-} == inspect ]]; then
  exit 0
fi
if [[ ${1:-} == buildx && ${2:-} == prune ]]; then
  printf 'prune\n' >>"$CLEANUP_DOCKER_LOG"
  exit 9
fi
if [[ ${1:-} == buildx && ${2:-} == stop ]]; then
  printf 'stop\n' >>"$CLEANUP_DOCKER_LOG"
  exit 0
fi
echo "测试 Docker 收到未预期参数: $*" >&2
exit 1
EOF
chmod +x "$TEST_DIR/bin/docker"

export PATH="$TEST_DIR/bin:$PATH"
export RUNNER_TEMP="$TEST_DIR/runner-temp"
export CLEANUP_DOCKER_LOG="$TEST_DIR/docker.log"

status=0
bash "$ROOT_DIR/scripts/release/cleanup-runner.sh" >"$TEST_DIR/cleanup.log" 2>&1 || status=$?
if [[ $status -ne 9 ]]; then
  echo "缓存清理失败时应返回原始错误码" >&2
  exit 1
fi
if [[ -e $ROOT_DIR/dist ]]; then
  echo "应删除发布产物目录" >&2
  exit 1
fi
if [[ -e $TEST_DIR/runner-temp/stale.tmp || ! -e $TEST_DIR/runner-temp/fresh.tmp ]]; then
  echo "应只删除过期 Runner 临时文件" >&2
  exit 1
fi
if [[ $(tail -n 1 "$CLEANUP_DOCKER_LOG") != stop ]]; then
  echo "缓存清理失败后仍应停止 Builder" >&2
  exit 1
fi

echo "持久型 Runner 清理测试通过"

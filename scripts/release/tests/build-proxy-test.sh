#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

mkdir -p "$TEST_DIR/bin" "$TEST_DIR/docker-clean"
cat >"$TEST_DIR/docker-clean/config.json" <<'EOF'
{"auths":{"127.0.0.1:5000":{"auth":"test"}}}
EOF

cat >"$TEST_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

printf '%s\n' "$*" >>"$BUILD_DOCKER_ALL_LOG"

if [[ ${1:-} == buildx && ${2:-} == version ]]; then
  exit 0
fi
if [[ ${1:-} == buildx && ${2:-} == inspect ]]; then
  exit 0
fi
if [[ ${1:-} == buildx && ${2:-} == build ]]; then
  [[ ${HTTP_PROXY:-} == "$EXPECTED_HTTP_PROXY" && ${http_proxy:-} == "$EXPECTED_HTTP_PROXY" ]]
  [[ ${HTTPS_PROXY:-} == "$EXPECTED_HTTPS_PROXY" && ${https_proxy:-} == "$EXPECTED_HTTPS_PROXY" ]]
  [[ ${NO_PROXY:-} == "$EXPECTED_NO_PROXY" && ${no_proxy:-} == "$EXPECTED_NO_PROXY" ]]

  printf '%s\n' "$*" >>"$BUILD_DOCKER_BUILD_LOG"
  output=
  target=
  shift 2
  while (($#)); do
    case "$1" in
      --output)
        output=$2
        shift 2
        ;;
      --target)
        target=$2
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  if [[ $output == type=local,dest=* ]]; then
    destination=${output#type=local,dest=}
    mkdir -p "$destination"
    case "$target" in
      binaries)
        touch "$destination/coinsphere-server" "$destination/coinsphere-migrate"
        ;;
      assets)
        touch "$destination/index.html"
        ;;
    esac
  fi
  exit 0
fi
if [[ ${1:-} == push ]]; then
  exit 0
fi
if [[ ${1:-} == image && ${2:-} == inspect ]]; then
  repository=${3%:*}
  printf '%s@sha256:%064d\n' "$repository" 1
  exit 0
fi

echo "测试 Docker 收到未预期参数: $*" >&2
exit 1
EOF
chmod +x "$TEST_DIR/bin/docker"

export PATH="$TEST_DIR/bin:$PATH"
export BUILD_DOCKER_ALL_LOG="$TEST_DIR/docker-all.log"
export BUILD_DOCKER_BUILD_LOG="$TEST_DIR/docker-build.log"
export EXPECTED_HTTP_PROXY=http://127.0.0.1:17890
export EXPECTED_HTTPS_PROXY=http://127.0.0.1:17891
export EXPECTED_NO_PROXY=127.0.0.1,localhost
unset HTTP_PROXY HTTPS_PROXY NO_PROXY
export http_proxy=$EXPECTED_HTTP_PROXY
export https_proxy=$EXPECTED_HTTPS_PROXY
export no_proxy=$EXPECTED_NO_PROXY

DOCKER_CONFIG="$TEST_DIR/docker-clean" \
COINSPHERE_BUILDER=coinsphere-proxy-test \
bash "$ROOT_DIR/scripts/release/build.sh" \
  v1.2.3 0123456789abcdef0123456789abcdef01234567 "$TEST_DIR/output"

if [[ $(wc -l <"$BUILD_DOCKER_BUILD_LOG") -ne 5 ]]; then
  echo "应执行五次 Buildx 构建" >&2
  exit 1
fi
for proxy_name in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
  if [[ $(grep -o -- "--build-arg $proxy_name" "$BUILD_DOCKER_BUILD_LOG" | wc -l) -ne 5 ]]; then
    echo "每次构建都应传入代理变量名: $proxy_name" >&2
    exit 1
  fi
done
for proxy_value in "$EXPECTED_HTTP_PROXY" "$EXPECTED_HTTPS_PROXY" "$EXPECTED_NO_PROXY"; do
  if grep -Fq "$proxy_value" "$BUILD_DOCKER_ALL_LOG"; then
    echo "代理值不得出现在 Docker 命令参数中" >&2
    exit 1
  fi
done

mkdir -p "$TEST_DIR/docker-global"
cat >"$TEST_DIR/docker-global/config.json" <<'EOF'
{"auths":{},"proxies":{"default":{"httpProxy":"http://127.0.0.1:17890"}}}
EOF

mkdir -p "$TEST_DIR/docker-array" "$TEST_DIR/docker-multiple"
printf '[]\n' >"$TEST_DIR/docker-array/config.json"
printf '{}\n{}\n' >"$TEST_DIR/docker-multiple/config.json"

assert_config_rejected() {
  local config_dir=$1
  local expected_status=$2
  local case_name=$3
  : >"$BUILD_DOCKER_ALL_LOG"
  local status=0
  DOCKER_CONFIG="$config_dir" \
  bash "$ROOT_DIR/scripts/release/build.sh" \
    v1.2.4 0123456789abcdef0123456789abcdef01234567 "$TEST_DIR/output-$case_name" \
    >"$TEST_DIR/$case_name.log" 2>&1 || status=$?
  if [[ $status -ne $expected_status ]]; then
    echo "Docker 配置 $case_name 应以退出码 $expected_status 阻止构建" >&2
    exit 1
  fi
  if [[ -s $BUILD_DOCKER_ALL_LOG ]]; then
    echo "Docker 配置 $case_name 被拒绝后不得调用 Docker" >&2
    exit 1
  fi
}

assert_config_rejected "$TEST_DIR/docker-global" 4 global-proxy
assert_config_rejected "$TEST_DIR/docker-array" 3 array
assert_config_rejected "$TEST_DIR/docker-multiple" 3 multiple-documents

echo "发布构建代理隔离测试通过"

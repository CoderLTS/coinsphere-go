#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
PYTHON=${PYTHON:-python3}
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
  if [[ ${3:-} != --bootstrap && ${MOCK_BUILDER_EXISTS:-true} != true ]]; then
    exit 1
  fi
  exit 0
fi
if [[ ${1:-} == container && ${2:-} == inspect ]]; then
  [[ ${MOCK_BUILDER_EXISTS:-true} == true ]]
  exit 0
fi
if [[ ${1:-} == buildx && ${2:-} == create ]]; then
  [[ $* == *"--driver-opt memory=$EXPECTED_BUILDER_MEMORY --driver-opt memory-swap=$EXPECTED_BUILDER_MEMORY"* ]]
  exit 0
fi
if [[ ${1:-} == update ]]; then
  [[ $* == "update --memory $EXPECTED_BUILDER_MEMORY --memory-swap $EXPECTED_BUILDER_MEMORY buildx_buildkit_${EXPECTED_BUILDER}0" ]]
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
        touch \
          "$destination/coinsphere-server" \
          "$destination/coinsphere-migrate"
        ;;
      web-assets)
        touch \
          "$destination/index.html" \
          "$destination/app.js.gz" \
          "$destination/role-edit-dialog.vue_vue_type_script_setup_true_lang-paLwghtU.js"
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
export EXPECTED_BUILDER=coinsphere-proxy-test
export EXPECTED_BUILDER_MEMORY=2560m
unset HTTP_PROXY HTTPS_PROXY NO_PROXY
export http_proxy=$EXPECTED_HTTP_PROXY
export https_proxy=$EXPECTED_HTTPS_PROXY
export no_proxy=$EXPECTED_NO_PROXY

TAR_OPTIONS=--blocking-factor=64 \
DOCKER_CONFIG="$TEST_DIR/docker-clean" \
COINSPHERE_BUILDER=coinsphere-proxy-test \
COINSPHERE_BUILDER_MEMORY=$EXPECTED_BUILDER_MEMORY \
bash "$ROOT_DIR/scripts/release/build.sh" \
  v1.2.3 0123456789abcdef0123456789abcdef01234567 "$TEST_DIR/output"

if tar -tzf "$TEST_DIR/output/coinsphere-v1.2.3-linux-amd64.tar.gz" |
  grep -Fq '/web/app.js.gz'; then
  echo "未使用的前端预压缩文件不得进入发布包" >&2
  exit 1
fi
"$PYTHON" - "$ROOT_DIR" "$TEST_DIR/output" <<'PY'
import importlib.util
import io
import os
import sys
import tarfile
import tempfile
from pathlib import Path

root_dir, output_dir = map(Path, sys.argv[1:])
spec = importlib.util.spec_from_file_location(
    "artifact_scanner", root_dir / "scripts/release/scan-artifacts.py"
)
scanner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scanner)
scanner.scan_zip(
    output_dir / "coinsphere-v1.2.3-windows-x86.zip", "windows-x86", "v1.2.3"
)
linux_archive = output_dir / "coinsphere-v1.2.3-linux-amd64.tar.gz"
with tempfile.TemporaryFile() as tar_stream:
    scanner.decompress_gzip(linux_archive, tar_stream)
    scanner.validate_tar_layout(tar_stream, linux_archive.name)
if os.name != "nt":
    scanner.scan_tar(linux_archive, "linux-amd64", "v1.2.3")
scanner.scan_tar(
    output_dir / "coinsphere-v1.2.3-docker.tar.gz", "docker", "v1.2.3"
)

boundary_member = tarfile.TarInfo("boundary.bin")
boundary_member.size = tarfile.RECORDSIZE - tarfile.BLOCKSIZE * 2
boundary_tar = boundary_member.tobuf(format=tarfile.GNU_FORMAT)
boundary_tar += b"x" * boundary_member.size
boundary_tar += tarfile.NUL * (tarfile.RECORDSIZE * 2 - len(boundary_tar))
scanner.validate_tar_layout(io.BytesIO(boundary_tar), "record-boundary.tar")
try:
    scanner.validate_tar_layout(
        io.BytesIO(boundary_tar + tarfile.NUL * tarfile.RECORDSIZE),
        "overpadded.tar",
    )
except scanner.ScanError:
    pass
else:
    raise AssertionError("额外 TAR 零记录必须被拒绝")
PY

if [[ $(wc -l <"$BUILD_DOCKER_BUILD_LOG") -ne 4 ]]; then
  echo "应执行四次 Buildx 构建" >&2
  exit 1
fi
for proxy_name in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
  if [[ $(grep -o -- "--build-arg $proxy_name" "$BUILD_DOCKER_BUILD_LOG" | wc -l) -ne 4 ]]; then
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

: >"$BUILD_DOCKER_ALL_LOG"
: >"$BUILD_DOCKER_BUILD_LOG"
images_only_output="$TEST_DIR/images-only-output"
export EXPECTED_BUILDER=coinsphere-images-only-test
export MOCK_BUILDER_EXISTS=false
DOCKER_CONFIG="$TEST_DIR/docker-clean" \
COINSPHERE_BUILDER=coinsphere-images-only-test \
COINSPHERE_BUILDER_MEMORY=$EXPECTED_BUILDER_MEMORY \
bash "$ROOT_DIR/scripts/release/build.sh" \
  v1.2.3 0123456789abcdef0123456789abcdef01234567 "$images_only_output" images-only

if [[ $(wc -l <"$BUILD_DOCKER_BUILD_LOG") -ne 1 ]]; then
  echo "images-only should run one Buildx image build" >&2
  exit 1
fi
if [[ $(find "$images_only_output" -maxdepth 1 -type f | wc -l) -ne 1 ]] ||
  [[ ! -f "$images_only_output/release-manifest.json" ]]; then
  echo "images-only should only produce release-manifest.json" >&2
  exit 1
fi
unset MOCK_BUILDER_EXISTS
jq -e '
  .version == "v1.2.3" and
  .commit == "0123456789abcdef0123456789abcdef01234567" and
  (.backendDigest | test("@sha256:[0-9]{64}$")) and
  (keys == ["backendDigest", "backendImage", "commit", "version"])
' "$images_only_output/release-manifest.json" >/dev/null

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

: >"$BUILD_DOCKER_ALL_LOG"
status=0
COINSPHERE_BUILDER_MEMORY=unlimited \
bash "$ROOT_DIR/scripts/release/build.sh" \
  v1.2.4 0123456789abcdef0123456789abcdef01234567 "$TEST_DIR/output-invalid-memory" \
  >"$TEST_DIR/invalid-memory.log" 2>&1 || status=$?
if [[ $status -ne 2 || -s $BUILD_DOCKER_ALL_LOG ]]; then
  echo "无效内存限制应在调用 Docker 前被拒绝" >&2
  exit 1
fi

echo "发布构建代理隔离测试通过"

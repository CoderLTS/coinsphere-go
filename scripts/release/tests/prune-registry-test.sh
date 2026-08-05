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
{"auths":{"127.0.0.1:5000":{"auth":"dGVzdDp0ZXN0"}}}
EOF
cat >"$TEST_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ ${1:-} == container && ${2:-} == inspect && ${3:-} == coinsphere-backend ]]; then
  printf 'v1.0.1\n'
  exit 0
fi
if [[ ${1:-} == image && ${2:-} == ls ]]; then
  exit 0
fi
echo "测试 Docker 收到未预期参数: $*" >&2
exit 1
EOF

cat >"$TEST_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

method=GET
output=
headers=
url=
while (($#)); do
  case "$1" in
    --config|--header|--retry|--write-out)
      shift 2
      ;;
    --dump-header|--output)
      variable=${1#--}
      if [[ $variable == dump-header ]]; then
        headers=$2
      else
        output=$2
      fi
      shift 2
      ;;
    --request)
      method=$2
      shift 2
      ;;
    --head)
      method=HEAD
      shift
      ;;
    --silent|--show-error)
      shift
      ;;
    http://*)
      url=$1
      shift
      ;;
    *)
      echo "测试 curl 收到未预期参数: $1" >&2
      exit 1
      ;;
  esac
done

if [[ $url == */tags/list?n=10000 && $method == GET ]]; then
  printf '{"tags":["sha-deadbeef","v1.0.1","v1.0.2","v1.0.3","v1.0.4","v1.0.5","v1.0.6","v1.0.7","v1.0.8","v1.0.9","v1.0.10","v1.0.11","v1.0.12"]}' >"$output"
  printf '200'
  exit 0
fi

reference=${url##*/}
if [[ $method == HEAD ]]; then
  number=${reference##*.}
  printf 'HTTP/1.1 200 OK\r\nDocker-Content-Digest: sha256:%064d\r\n\r\n' "$number" >"$headers"
  printf '200'
  exit 0
fi
if [[ $method == DELETE ]]; then
  printf '%s\n' "$url" >>"$DELETE_LOG"
  printf '202'
  exit 0
fi

echo "测试 curl 未处理请求: $method $url" >&2
exit 1
EOF
chmod +x "$TEST_DIR/bin/docker" "$TEST_DIR/bin/curl"

export PATH="$TEST_DIR/bin:$PATH"
export DOCKER_CONFIG="$TEST_DIR/docker"
export COINSPHERE_REGISTRY_KEEP_RELEASES=10
export DELETE_LOG="$TEST_DIR/deleted"

bash "$ROOT_DIR/scripts/release/prune-registry.sh" --dry-run >"$TEST_DIR/dry-run.log"
if [[ -e $DELETE_LOG ]]; then
  echo "默认预演模式不应删除 Manifest" >&2
  exit 1
fi

bash "$ROOT_DIR/scripts/release/prune-registry.sh" --apply >"$TEST_DIR/apply.log"
if [[ $(wc -l <"$DELETE_LOG") -ne 2 ]]; then
  echo "应分别删除 backend/web 的一个过期 Manifest" >&2
  exit 1
fi
if [[ $(grep -c 'sha256:0*2$' "$DELETE_LOG") -ne 2 ]]; then
  echo "应删除 v1.0.2，并保留当前部署的 v1.0.1" >&2
  exit 1
fi

echo "Registry 保留策略测试通过"

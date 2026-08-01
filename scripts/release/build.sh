#!/usr/bin/env bash
set -Eeuo pipefail

VERSION=${1:?用法: build.sh vX.Y.Z COMMIT_SHA [OUTPUT_DIR]}
COMMIT_SHA=${2:?用法: build.sh vX.Y.Z COMMIT_SHA [OUTPUT_DIR]}
OUTPUT_DIR=${3:-dist}
REGISTRY=${COINSPHERE_REGISTRY:-127.0.0.1:5000}
GO_PROXY=${COINSPHERE_GO_PROXY:-https://goproxy.cn,direct}
BUILDER=${COINSPHERE_BUILDER:-coinsphere-release}
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
OUTPUT_DIR=$(mkdir -p "$OUTPUT_DIR" && cd "$OUTPUT_DIR" && pwd)
DOCKER_CONFIG_FILE="${DOCKER_CONFIG:-${HOME:?HOME 未设置}/.docker}/config.json"

HTTP_PROXY=${HTTP_PROXY:-${http_proxy:-}}
HTTPS_PROXY=${HTTPS_PROXY:-${https_proxy:-}}
NO_PROXY=${NO_PROXY:-${no_proxy:-}}
export HTTP_PROXY HTTPS_PROXY NO_PROXY
export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY" no_proxy="$NO_PROXY"

if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "版本号必须符合 vX.Y.Z 格式: $VERSION" >&2
  exit 2
fi
if [[ ! $COMMIT_SHA =~ ^[0-9a-f]{40}$ ]]; then
  echo "Commit SHA 必须是 40 位小写十六进制" >&2
  exit 2
fi
if [[ ! $BUILDER =~ ^[0-9A-Za-z][0-9A-Za-z_.-]*$ ]]; then
  echo "Buildx Builder 名称无效: $BUILDER" >&2
  exit 2
fi
for command_name in docker jq zip tar sha256sum; do
  command -v "$command_name" >/dev/null || { echo "缺少命令: $command_name" >&2; exit 3; }
done
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
      echo "请将代理保留在 Runner 环境中，由发布脚本仅传给 BuildKit。" >&2
      exit 4
      ;;
    *)
      echo "Docker 客户端配置不是单个有效 JSON 对象: $DOCKER_CONFIG_FILE" >&2
      exit 3
      ;;
  esac
fi
docker buildx version >/dev/null
if find "$OUTPUT_DIR" -mindepth 1 -print -quit | grep -q .; then
  echo "输出目录必须为空: $OUTPUT_DIR" >&2
  exit 3
fi

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

backend_image="$REGISTRY/coinsphere/backend:$VERSION"
web_image="$REGISTRY/coinsphere/web:$VERSION"
sha_tag="sha-${COMMIT_SHA:0:12}"
proxy_build_args=(
  --build-arg HTTP_PROXY
  --build-arg HTTPS_PROXY
  --build-arg NO_PROXY
  --build-arg http_proxy
  --build-arg https_proxy
  --build-arg no_proxy
)

run_buildx() {
  docker buildx build --builder "$BUILDER" "${proxy_build_args[@]}" "$@"
}

if ! docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
  docker buildx create --name "$BUILDER" --driver docker-container --driver-opt network=host \
    --buildkitd-config "$ROOT_DIR/scripts/release/buildkitd.toml" >/dev/null
fi
docker buildx inspect --bootstrap "$BUILDER" >/dev/null

run_buildx --load \
  --label "org.opencontainers.image.version=$VERSION" \
  --label "org.opencontainers.image.revision=$COMMIT_SHA" \
  --build-arg TARGETOS=linux --build-arg TARGETARCH=amd64 --build-arg "GOPROXY=$GO_PROXY" \
  -t "$backend_image" -t "$REGISTRY/coinsphere/backend:$sha_tag" \
  "$ROOT_DIR/backend"
run_buildx --load \
  --label "org.opencontainers.image.version=$VERSION" \
  --label "org.opencontainers.image.revision=$COMMIT_SHA" \
  --build-arg "VITE_VERSION=$VERSION" \
  -t "$web_image" -t "$REGISTRY/coinsphere/web:$sha_tag" \
  "$ROOT_DIR/frontend"

run_buildx \
  --build-arg TARGETOS=linux --build-arg TARGETARCH=amd64 --build-arg "GOPROXY=$GO_PROXY" \
  --target binaries --output "type=local,dest=$work_dir/linux" "$ROOT_DIR/backend"
run_buildx \
  --build-arg TARGETOS=windows --build-arg TARGETARCH=386 --build-arg "GOPROXY=$GO_PROXY" \
  --target binaries --output "type=local,dest=$work_dir/windows" "$ROOT_DIR/backend"
run_buildx --build-arg "VITE_VERSION=$VERSION" \
  --target assets --output "type=local,dest=$work_dir/web" "$ROOT_DIR/frontend"

windows_name="coinsphere-$VERSION-windows-x86"
linux_name="coinsphere-$VERSION-linux-amd64"
docker_name="coinsphere-$VERSION-docker"
mkdir -p "$work_dir/packages/$windows_name/web" "$work_dir/packages/$linux_name/web" "$work_dir/packages/$docker_name"

install -m 0755 "$work_dir/windows/coinsphere-server" "$work_dir/packages/$windows_name/coinsphere-server.exe"
install -m 0755 "$work_dir/windows/coinsphere-migrate" "$work_dir/packages/$windows_name/coinsphere-migrate.exe"
install -m 0755 "$work_dir/linux/coinsphere-server" "$work_dir/packages/$linux_name/coinsphere-server"
install -m 0755 "$work_dir/linux/coinsphere-migrate" "$work_dir/packages/$linux_name/coinsphere-migrate"
for package_name in "$windows_name" "$linux_name"; do
  install -m 0644 "$ROOT_DIR/backend/config.yml" "$work_dir/packages/$package_name/config.yml"
  install -m 0644 "$ROOT_DIR/frontend/nginx.conf" "$work_dir/packages/$package_name/nginx.conf"
  install -m 0644 "$ROOT_DIR/deploy/packages/README.md" "$work_dir/packages/$package_name/README.md"
  cp -a "$work_dir/web/." "$work_dir/packages/$package_name/web/"
done
install -m 0644 "$ROOT_DIR/deploy/production/compose.yaml" "$work_dir/packages/$docker_name/compose.yaml"
install -m 0755 "$ROOT_DIR/deploy/production/deploy.sh" "$work_dir/packages/$docker_name/deploy.sh"
install -m 0644 "$ROOT_DIR/deploy/production/runtime.env.example" "$work_dir/packages/$docker_name/runtime.env.example"
install -m 0644 "$ROOT_DIR/deploy/production/README.md" "$work_dir/packages/$docker_name/README.md"

(cd "$work_dir/packages" && zip -X -qr "$OUTPUT_DIR/$windows_name.zip" "$windows_name")
tar -C "$work_dir/packages" -czf "$OUTPUT_DIR/$linux_name.tar.gz" "$linux_name"
tar -C "$work_dir/packages" -czf "$OUTPUT_DIR/$docker_name.tar.gz" "$docker_name"

docker push "$backend_image"
docker push "$REGISTRY/coinsphere/backend:$sha_tag"
docker push "$web_image"
docker push "$REGISTRY/coinsphere/web:$sha_tag"

backend_digest=$(docker image inspect "$backend_image" --format '{{range .RepoDigests}}{{println .}}{{end}}' | grep -F "$REGISTRY/coinsphere/backend@" | head -n 1)
web_digest=$(docker image inspect "$web_image" --format '{{range .RepoDigests}}{{println .}}{{end}}' | grep -F "$REGISTRY/coinsphere/web@" | head -n 1)
cat >"$OUTPUT_DIR/release-manifest.json" <<EOF
{
  "version": "$VERSION",
  "commit": "$COMMIT_SHA",
  "backendImage": "$backend_image",
  "backendDigest": "$backend_digest",
  "webImage": "$web_image",
  "webDigest": "$web_digest"
}
EOF

(cd "$OUTPUT_DIR" && sha256sum ./*.zip ./*.tar.gz ./release-manifest.json >SHA256SUMS)

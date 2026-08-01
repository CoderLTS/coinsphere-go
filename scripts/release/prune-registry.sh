#!/usr/bin/env bash
set -Eeuo pipefail

MODE=${1:---dry-run}
REGISTRY=${COINSPHERE_REGISTRY:-127.0.0.1:5000}
KEEP_RELEASES=${COINSPHERE_REGISTRY_KEEP_RELEASES:-10}
DEPLOY_DIR=${COINSPHERE_DEPLOY_DIR:-/home/infrastructure/dpanel/compose/coinsphere-go}
DOCKER_CONFIG_FILE=${DOCKER_CONFIG:-$HOME/.docker}/config.json
MANIFEST_ACCEPT='application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'

case "$MODE" in
  --apply)
    apply=true
    ;;
  --dry-run)
    apply=false
    ;;
  *)
    echo "用法: prune-registry.sh [--dry-run|--apply]" >&2
    exit 2
    ;;
esac

if [[ $REGISTRY != 127.0.0.1:5000 ]]; then
  echo "Registry 保留任务只允许访问本机 127.0.0.1:5000" >&2
  exit 2
fi
if [[ ! $KEEP_RELEASES =~ ^[0-9]+$ ]] || ((KEEP_RELEASES < 2)); then
  echo "Registry 至少保留 2 个版本" >&2
  exit 2
fi
for command_name in curl docker jq sort; do
  command -v "$command_name" >/dev/null || { echo "缺少命令: $command_name" >&2; exit 3; }
done
if [[ ! -f $DOCKER_CONFIG_FILE ]]; then
  echo "缺少 Docker 登录配置: $DOCKER_CONFIG_FILE" >&2
  exit 3
fi

registry_auth=$(jq -er --arg registry "$REGISTRY" '.auths[$registry].auth // empty' "$DOCKER_CONFIG_FILE")
work_dir=$(mktemp -d)
curl_config="$work_dir/curl.conf"
cleanup() {
  rm -rf -- "$work_dir"
}
trap cleanup EXIT
chmod 0700 "$work_dir"
printf 'header = "Authorization: Basic %s"\n' "$registry_auth" >"$curl_config"
chmod 0600 "$curl_config"
unset registry_auth

current_version=
if [[ -f $DEPLOY_DIR/.env ]]; then
  current_version=$(sed -n 's/^COINSPHERE_VERSION=//p' "$DEPLOY_DIR/.env" | tail -n 1)
fi

manifest_digest() {
  local repository=$1
  local tag=$2
  local headers="$work_dir/headers"
  local status

  status=$(curl --config "$curl_config" --silent --show-error --retry 3 \
    --header "Accept: $MANIFEST_ACCEPT" --head --dump-header "$headers" --output /dev/null \
    --write-out '%{http_code}' "http://$REGISTRY/v2/$repository/manifests/$tag")
  if [[ $status != 200 ]]; then
    echo "读取 Manifest 失败: $repository:$tag (HTTP $status)" >&2
    return 1
  fi
  awk -F ': ' 'tolower($1) == "docker-content-digest" { sub(/\r$/, "", $2); print $2; exit }' "$headers"
}

prune_local_images() {
  local repository=$1
  local keep_name=$2
  local -n keep_ref=$keep_name
  local reference version
  local -a references=()

  mapfile -t references < <(docker image ls "$REGISTRY/$repository" --format '{{.Repository}}:{{.Tag}}' | grep -v ':<none>$' || true)
  for reference in "${references[@]}"; do
    version=$(docker image inspect "$reference" --format '{{index .Config.Labels "org.opencontainers.image.version"}}' 2>/dev/null || true)
    if [[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ && -z ${keep_ref[$version]+x} ]]; then
      if $apply; then
        docker image rm "$reference" >/dev/null
        echo "已删除本地旧镜像标签: $reference"
      else
        echo "[dry-run] 将删除本地旧镜像标签: $reference"
      fi
    fi
  done
}

prune_repository() {
  local repository=$1
  local tags_file="$work_dir/${repository//\//_}-tags.json"
  local status curl_status=0 tag digest versions_text
  local -a versions=()
  local -A keep_tags=()
  local -A tag_digests=()
  local -A keep_digests=()
  local -A delete_digests=()

  status=$(curl --config "$curl_config" --silent --show-error --retry 3 \
    --output "$tags_file" --write-out '%{http_code}' \
    "http://$REGISTRY/v2/$repository/tags/list?n=10000") || curl_status=$?
  if [[ $status == 404 ]]; then
    echo "Registry 中尚无仓库: $repository"
    return
  fi
  if ((curl_status != 0)) || [[ $status != 200 ]]; then
    echo "读取 Registry 标签失败: $repository (HTTP $status)" >&2
    return 1
  fi

  mapfile -t versions < <(jq -r '.tags[]?' "$tags_file" \
    | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$' \
    | sort -Vr || true)
  if ((${#versions[@]} == 0)); then
    echo "Registry 中没有 CoinSphere 版本标签: $repository"
    return
  fi

  for ((index = 0; index < ${#versions[@]} && index < KEEP_RELEASES; index++)); do
    keep_tags["${versions[$index]}"]=1
  done
  if [[ $current_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
    keep_tags["$current_version"]=1
  fi

  for tag in "${versions[@]}"; do
    digest=$(manifest_digest "$repository" "$tag")
    if [[ -z $digest ]]; then
      echo "Manifest 缺少内容摘要: $repository:$tag" >&2
      return 1
    fi
    tag_digests["$tag"]=$digest
    if [[ -n ${keep_tags[$tag]+x} ]]; then
      keep_digests["$digest"]=1
    fi
  done

  for tag in "${versions[@]}"; do
    if [[ -n ${keep_tags[$tag]+x} ]]; then
      continue
    fi
    digest=${tag_digests[$tag]}
    if [[ -z ${keep_digests[$digest]+x} ]]; then
      delete_digests["$digest"]=1
    fi
  done

  versions_text=$(printf '%s ' "${!keep_tags[@]}")
  echo "$repository 保留版本: ${versions_text% }"
  for digest in "${!delete_digests[@]}"; do
    if $apply; then
      status=$(curl --config "$curl_config" --silent --show-error --retry 3 \
        --request DELETE --output /dev/null --write-out '%{http_code}' \
        "http://$REGISTRY/v2/$repository/manifests/$digest")
      if [[ $status != 202 && $status != 404 ]]; then
        echo "删除 Registry Manifest 失败: $repository@$digest (HTTP $status)" >&2
        return 1
      fi
      echo "已删除 Registry 旧 Manifest: $repository@$digest"
    else
      echo "[dry-run] 将删除 Registry 旧 Manifest: $repository@$digest"
    fi
  done

  prune_local_images "$repository" keep_tags
}

prune_repository coinsphere/backend
prune_repository coinsphere/web

if $apply; then
  echo "Registry 版本保留策略执行完成"
else
  echo "Registry 版本保留策略预演完成，未删除任何内容"
fi

#!/usr/bin/env bash
set -Eeuo pipefail

CACHE_MAX_AGE=${COINSPHERE_BUILD_CACHE_MAX_AGE:-168h}
STALE_TEMP_MINUTES=${COINSPHERE_RUNNER_TEMP_MAX_AGE_MINUTES:-1440}
BUILDER=${COINSPHERE_BUILDER:-coinsphere-release}
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

if [[ ! $CACHE_MAX_AGE =~ ^[1-9][0-9]*[smhd]$ ]]; then
  echo "Build Cache 保留时长无效: $CACHE_MAX_AGE" >&2
  exit 2
fi
if [[ ! $STALE_TEMP_MINUTES =~ ^[1-9][0-9]*$ ]]; then
  echo "Runner 临时文件保留分钟数无效: $STALE_TEMP_MINUTES" >&2
  exit 2
fi
if [[ ! $BUILDER =~ ^[0-9A-Za-z][0-9A-Za-z_.-]*$ ]]; then
  echo "Buildx Builder 名称无效: $BUILDER" >&2
  exit 2
fi

rm -rf -- "$ROOT_DIR/dist"

if [[ -n ${RUNNER_TEMP:-} && -d $RUNNER_TEMP ]]; then
  find "$RUNNER_TEMP" -depth -mindepth 1 -mmin "+$STALE_TEMP_MINUTES" -delete
fi

if docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
  cleanup_status=0
  docker buildx prune --builder "$BUILDER" --force --filter "until=$CACHE_MAX_AGE" || cleanup_status=$?
  docker buildx stop "$BUILDER" >/dev/null || cleanup_status=$?
  if ((cleanup_status != 0)); then
    echo "Buildx Builder 清理未完整完成" >&2
    exit "$cleanup_status"
  fi
fi

echo "持久型 Runner 清理完成"

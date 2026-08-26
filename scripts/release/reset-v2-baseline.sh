#!/usr/bin/env bash
set -Eeuo pipefail

EXPECTED_CONFIRMATION='RESET coinsphere-go-timescale-data'

if (( $# != 3 )); then
  echo "用法: reset-v2-baseline.sh vX.Y.Z release-manifest.json '$EXPECTED_CONFIRMATION'" >&2
  exit 2
fi

VERSION=$1
MANIFEST_FILE=$2
CONFIRMATION=$3
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
DATABASE_VOLUME=coinsphere-go-timescale-data

[[ $CONFIRMATION == "$EXPECTED_CONFIRMATION" ]] || {
  echo "数据库重置确认文本不匹配" >&2
  exit 2
}
[[ $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || {
  echo "版本号格式无效" >&2
  exit 2
}
[[ $VERSION == v0.3.1 ]] || {
  echo "V2 基线重置只允许用于 v0.3.1 freeze Release" >&2
  exit 2
}
[[ -f $MANIFEST_FILE && ! -L $MANIFEST_FILE ]] || {
  echo "发布 Manifest 不是普通文件" >&2
  exit 2
}

timescaledb_container=$(docker ps -aq \
  --filter label=com.docker.compose.project=coinsphere-go \
  --filter label=com.docker.compose.service=timescaledb | sed -n '1p')
[[ -n $timescaledb_container ]] || {
  echo "找不到 CoinSphere TimescaleDB 容器，拒绝猜测部署目标" >&2
  exit 1
}

deploy_dir=$(docker inspect --format \
  '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' \
  "$timescaledb_container")
[[ $deploy_dir == /* && -d $deploy_dir && ! -L $deploy_dir \
  && -f $deploy_dir/compose.yaml && ! -L $deploy_dir/compose.yaml \
  && -f $deploy_dir/.env && ! -L $deploy_dir/.env ]] || {
  echo "CoinSphere 部署目录不完整或不安全" >&2
  exit 1
}

if docker ps -q \
  --filter label=com.docker.compose.project=coinsphere-go \
  --filter label=com.docker.compose.service=executor | grep -q .; then
  echo "Private Executor 正在运行，拒绝重置数据库" >&2
  exit 1
fi

compose=(docker compose --project-name coinsphere-go --project-directory "$deploy_dir" \
  --env-file "$deploy_dir/.env" -f "$deploy_dir/compose.yaml")
mapfile -t configured_services < <("${compose[@]}" config --services)
for service in timescaledb backend web; do
  printf '%s\n' "${configured_services[@]}" | grep -Fxq "$service" || {
    echo "CoinSphere Compose 缺少服务: $service" >&2
    exit 1
  }
done
for forbidden_service in worker executor; do
  if printf '%s\n' "${configured_services[@]}" | grep -Fxq "$forbidden_service"; then
    echo "CoinSphere V2 Compose 不得包含服务: $forbidden_service" >&2
    exit 1
  fi
done

declare -A volume_keys=(
  [coinsphere-go-timescale-data]=timescale-data
  [coinsphere-go-backend-artifacts]=backend-artifacts
  [coinsphere-go-backend-uploads]=backend-uploads
  [coinsphere-go-backend-static]=backend-static
)
backup_volumes=()
for volume in "${!volume_keys[@]}"; do
  if ! docker volume inspect "$volume" >/dev/null 2>&1; then
    [[ $volume != "$DATABASE_VOLUME" ]] || {
      echo "找不到目标数据库卷" >&2
      exit 1
    }
    continue
  fi
  volume_project=$(docker volume inspect --format \
    '{{ index .Labels "com.docker.compose.project" }}' "$volume")
  volume_key=$(docker volume inspect --format \
    '{{ index .Labels "com.docker.compose.volume" }}' "$volume")
  [[ $volume_project == coinsphere-go && $volume_key == "${volume_keys[$volume]}" ]] || {
    echo "目标卷不属于 CoinSphere 独立 Compose: $volume" >&2
    exit 1
  }
  backup_volumes+=("$volume")
done

backup_image=$(docker inspect --format '{{.Image}}' "$timescaledb_container")
[[ $backup_image =~ ^sha256:[0-9a-f]{64}$ ]] || {
  echo "无法确定 TimescaleDB 本地镜像 digest" >&2
  exit 1
}

mkdir -p "$deploy_dir/backups"
chmod 0700 "$deploy_dir/backups"
backup_dir=$(mktemp -d "$deploy_dir/backups/$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")
backup_id=${backup_dir##*/}
cp "$deploy_dir/.env" "$backup_dir/deployment.env"
cp "$deploy_dir/compose.yaml" "$backup_dir/compose.yaml"
chmod 0600 "$backup_dir/deployment.env"
chmod 0644 "$backup_dir/compose.yaml"

database_removed=false
rollback() {
  local status=$1
  trap - ERR INT TERM
  set +e
  echo "V2 基线部署失败，开始恢复重置前数据卷" >&2

  if $database_removed; then
    "${compose[@]}" stop web backend timescaledb
    "${compose[@]}" rm -f web backend timescaledb
    restore_failed=false
    if ! (cd "$backup_dir" && sha256sum -c SHA256SUMS >/dev/null); then
      restore_failed=true
    else
      for volume in "${backup_volumes[@]}"; do
        archive=${volume}.tar
        if docker volume inspect "$volume" >/dev/null 2>&1; then
          if ! docker volume rm "$volume" >/dev/null; then
            restore_failed=true
            continue
          fi
        fi
        if ! docker volume create \
          --label com.docker.compose.project=coinsphere-go \
          --label "com.docker.compose.volume=${volume_keys[$volume]}" \
          "$volume" >/dev/null; then
          restore_failed=true
          continue
        fi
        docker run --rm -i --network none --user 0:0 \
          --mount "type=volume,src=$volume,dst=/target" \
          --entrypoint tar "$backup_image" -C /target -xf - \
          <"$backup_dir/$archive" || restore_failed=true
      done
    fi
    cp "$backup_dir/deployment.env" "$deploy_dir/.env" || restore_failed=true
    cp "$backup_dir/compose.yaml" "$deploy_dir/compose.yaml" || restore_failed=true
    chmod 0600 "$deploy_dir/.env" || restore_failed=true
    chmod 0644 "$deploy_dir/compose.yaml" || restore_failed=true
    if ! $restore_failed; then
      "${compose[@]}" up -d --wait --wait-timeout 180 timescaledb backend web || restore_failed=true
    fi
    if $restore_failed; then
      echo "自动恢复失败；保留备份 $backup_id，必须按 Runbook 人工恢复" >&2
    else
      echo "已从备份 $backup_id 恢复重置前服务" >&2
    fi
  else
    "${compose[@]}" up -d --wait --wait-timeout 180 timescaledb backend web || true
  fi
  exit "$status"
}
trap 'rollback $?' ERR
trap 'rollback 130' INT
trap 'rollback 143' TERM

"${compose[@]}" stop web backend timescaledb

archive_names=()
for volume in "${backup_volumes[@]}"; do
  archive=${volume}.tar
  docker run --rm --network none --user 0:0 \
    --mount "type=volume,src=$volume,dst=/source,readonly" \
    --entrypoint tar "$backup_image" -C /source -cf - . \
    >"$backup_dir/$archive"
  chmod 0600 "$backup_dir/$archive"
  tar -tf "$backup_dir/$archive" >/dev/null
  archive_names+=("$archive")
done
(
  cd "$backup_dir"
  sha256sum -- "${archive_names[@]}" >SHA256SUMS
  sha256sum -c SHA256SUMS >/dev/null
)

"${compose[@]}" rm -f web backend timescaledb
if docker ps -aq --filter volume="$DATABASE_VOLUME" | grep -q .; then
  echo "数据库卷仍被容器使用，拒绝删除" >&2
  exit 1
fi
docker volume rm "$DATABASE_VOLUME" >/dev/null
database_removed=true
if docker volume inspect "$DATABASE_VOLUME" >/dev/null 2>&1; then
  echo "数据库卷删除校验失败" >&2
  exit 1
fi

echo "已验证备份 $backup_id 并删除 $DATABASE_VOLUME；开始部署 $VERSION"
COINSPHERE_DEPLOY_DIR=$deploy_dir \
  bash "$ROOT_DIR/deploy/production/deploy.sh" "$VERSION" "$MANIFEST_FILE"

trap - ERR INT TERM
echo "V2 基线已重建；重置前备份保留为 $backup_id"

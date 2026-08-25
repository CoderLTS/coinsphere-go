#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
DEPLOY_DIR=$TEST_DIR/deploy
STATE_DIR=$TEST_DIR/volumes
VERSION=v0.3.0
COMMIT=0123456789abcdef0123456789abcdef01234567
REGISTRY=127.0.0.1:5000
BACKEND_DIGEST=$(printf 'a%.0s' {1..64})
WEB_DIGEST=$(printf 'b%.0s' {1..64})
DATABASE_PASSWORD=$(printf '0%.0s' {1..64})

cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

mkdir -p "$TEST_DIR/bin" "$DEPLOY_DIR" "$STATE_DIR"
cp "$ROOT_DIR/deploy/production/compose.yaml" "$DEPLOY_DIR/compose.yaml"
cat >"$DEPLOY_DIR/.env" <<EOF
COINSPHERE_VERSION=v0.2.9
COINSPHERE_BACKEND_IMAGE=$REGISTRY/coinsphere/backend@sha256:$BACKEND_DIGEST
COINSPHERE_WEB_IMAGE=$REGISTRY/coinsphere/web@sha256:$WEB_DIGEST
COINSPHERE_WEB_BIND=127.0.0.1
COINSPHERE_WEB_PORT=8080
COINSPHERE_DATABASE_PASSWORD=$DATABASE_PASSWORD
EOF
cat >"$DEPLOY_DIR/runtime.env" <<'EOF'
COINSPHERE_AUTH__SECRET_KEY=test-only-secret-key
EOF
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

for volume in \
  coinsphere-go-timescale-data \
  coinsphere-go-backend-artifacts \
  coinsphere-go-backend-uploads \
  coinsphere-go-backend-static; do
  touch "$STATE_DIR/$volume"
done

cat >"$TEST_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"$MOCK_DOCKER_LOG"

if [[ ${1:-} == ps ]]; then
  if [[ $* == *'com.docker.compose.service=timescaledb'* ]]; then
    printf 'timescaledb-container\n'
  fi
  exit 0
fi

if [[ ${1:-} == inspect ]]; then
  if [[ $* == *'{{.Image}}'* ]]; then
    printf 'sha256:%064d\n' 0
  else
    printf '%s\n' "$MOCK_DEPLOY_DIR"
  fi
  exit 0
fi

if [[ ${1:-} == volume ]]; then
  action=${2:-}
  volume=${!#}
  case "$action" in
    inspect)
      [[ -f "$MOCK_VOLUME_STATE_DIR/$volume" ]] || exit 1
      if [[ $* == *'com.docker.compose.project'* ]]; then
        printf 'coinsphere-go\n'
      elif [[ $* == *'com.docker.compose.volume'* ]]; then
        case "$volume" in
          coinsphere-go-timescale-data) printf 'timescale-data\n' ;;
          coinsphere-go-backend-artifacts) printf 'backend-artifacts\n' ;;
          coinsphere-go-backend-uploads) printf 'backend-uploads\n' ;;
          coinsphere-go-backend-static) printf 'backend-static\n' ;;
          *) exit 1 ;;
        esac
      fi
      ;;
    rm)
      rm -f -- "$MOCK_VOLUME_STATE_DIR/$volume"
      printf '%s\n' "$volume"
      ;;
    create)
      touch "$MOCK_VOLUME_STATE_DIR/$volume"
      printf '%s\n' "$volume"
      ;;
    *) exit 1 ;;
  esac
  exit 0
fi

if [[ ${1:-} == run ]]; then
  if [[ $* == *'dst=/source'* ]]; then
    tar -cf - --files-from /dev/null
  else
    cat >/dev/null
  fi
  exit 0
fi

if [[ ${1:-} == compose ]]; then
  if [[ $* == *'config --services'* ]]; then
    printf 'timescaledb\nbackend\nweb\n'
  elif [[ $* == *'ps --services --status running'* ]]; then
    printf 'timescaledb\nbackend\nweb\n'
  elif [[ $* == *'run --rm --no-deps backend /app/coinsphere-migrate'* \
    && -n ${MOCK_FAIL_MIGRATION_FILE:-} && -f $MOCK_FAIL_MIGRATION_FILE ]]; then
    rm -f -- "$MOCK_FAIL_MIGRATION_FILE"
    exit 1
  fi
  exit 0
fi

exit 0
EOF
cat >"$TEST_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$TEST_DIR/bin/jq" <<'EOF'
#!/usr/bin/env bash
printf '%s\n%s\n' "$MOCK_BACKEND_IMAGE" "$MOCK_WEB_IMAGE"
EOF
chmod +x "$TEST_DIR/bin/docker" "$TEST_DIR/bin/curl" "$TEST_DIR/bin/jq"

export PATH="$TEST_DIR/bin:$PATH"
export MOCK_DEPLOY_DIR=$DEPLOY_DIR
export MOCK_DOCKER_LOG=$TEST_DIR/docker.log
export MOCK_VOLUME_STATE_DIR=$STATE_DIR
export MOCK_BACKEND_IMAGE=$REGISTRY/coinsphere/backend@sha256:$BACKEND_DIGEST
export MOCK_WEB_IMAGE=$REGISTRY/coinsphere/web@sha256:$WEB_DIGEST

status=0
bash "$ROOT_DIR/scripts/release/reset-v2-baseline.sh" \
  "$VERSION" "$TEST_DIR/release-manifest.json" wrong \
  >"$TEST_DIR/rejected.log" 2>&1 || status=$?
if [[ $status -ne 2 || -e $MOCK_DOCKER_LOG ]]; then
  echo "确认文本不匹配时不得接触 Docker" >&2
  exit 1
fi

if ! COINSPHERE_DEPLOY_DIR=$DEPLOY_DIR \
  DOCKER_CONFIG=$TEST_DIR/docker-config \
  COINSPHERE_REGISTRY=$REGISTRY \
  bash "$ROOT_DIR/scripts/release/reset-v2-baseline.sh" \
    "$VERSION" "$TEST_DIR/release-manifest.json" 'RESET coinsphere-go-timescale-data' \
    >"$TEST_DIR/success.log" 2>&1; then
  cat "$TEST_DIR/success.log" >&2
  exit 1
fi

mapfile -t backup_dirs < <(find "$DEPLOY_DIR/backups" -mindepth 1 -maxdepth 1 -type d)
if ((${#backup_dirs[@]} != 1)); then
  echo "重置前必须创建唯一备份" >&2
  exit 1
fi
backup_dir=${backup_dirs[0]}
(
  cd "$backup_dir"
  sha256sum -c SHA256SUMS >/dev/null
)
if [[ -f $STATE_DIR/coinsphere-go-timescale-data ]]; then
  echo "成功路径必须删除旧数据库卷" >&2
  exit 1
fi
backup_line=$(grep -n -m1 'dst=/source' "$MOCK_DOCKER_LOG" | cut -d: -f1)
remove_line=$(grep -n -m1 'volume rm coinsphere-go-timescale-data' "$MOCK_DOCKER_LOG" | cut -d: -f1)
migrate_line=$(grep -n -m1 'coinsphere-migrate' "$MOCK_DOCKER_LOG" | cut -d: -f1)
if [[ -z $backup_line || -z $remove_line || -z $migrate_line \
  || $backup_line -ge $remove_line || $remove_line -ge $migrate_line ]]; then
  echo "必须先备份，再删除数据库卷，最后执行 migration" >&2
  exit 1
fi

for volume in \
  coinsphere-go-timescale-data \
  coinsphere-go-backend-artifacts \
  coinsphere-go-backend-uploads \
  coinsphere-go-backend-static; do
  touch "$STATE_DIR/$volume"
done
touch "$TEST_DIR/fail-migration"
export MOCK_FAIL_MIGRATION_FILE=$TEST_DIR/fail-migration
status=0
COINSPHERE_DEPLOY_DIR=$DEPLOY_DIR \
DOCKER_CONFIG=$TEST_DIR/docker-config \
COINSPHERE_REGISTRY=$REGISTRY \
bash "$ROOT_DIR/scripts/release/reset-v2-baseline.sh" \
  "$VERSION" "$TEST_DIR/release-manifest.json" 'RESET coinsphere-go-timescale-data' \
  >"$TEST_DIR/rollback.log" 2>&1 || status=$?
if [[ $status -eq 0 ]]; then
  echo "migration 失败必须使基线重置失败" >&2
  exit 1
fi
for volume in \
  coinsphere-go-timescale-data \
  coinsphere-go-backend-artifacts \
  coinsphere-go-backend-uploads \
  coinsphere-go-backend-static; do
  [[ -f $STATE_DIR/$volume ]] || {
    echo "部署失败后未恢复数据卷: $volume" >&2
    exit 1
  }
done
grep -Fq '已从备份' "$TEST_DIR/rollback.log" || {
  echo "部署失败后必须确认备份恢复完成" >&2
  exit 1
}

echo "V2 基线备份、重置与失败恢复测试通过"

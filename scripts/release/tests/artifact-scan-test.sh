#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEST_DIR=$(mktemp -d)
PYTHON=${PYTHON:-python3}
VERSION=v1.2.3
COMMIT=0123456789abcdef0123456789abcdef01234567
REGISTRY=127.0.0.1:5000
BACKEND_DIGEST=$(printf 'a%.0s' {1..64})
WEB_DIGEST=$(printf 'b%.0s' {1..64})
WORKER_DIGEST=$(printf 'c%.0s' {1..64})

cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

command -v "$PYTHON" >/dev/null || { echo "缺少 Python: $PYTHON" >&2; exit 3; }
command -v sha256sum >/dev/null || { echo "缺少命令: sha256sum" >&2; exit 3; }

mkdir -p "$TEST_DIR/bin"
cat >"$TEST_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

remote=false
if [[ ${1:-} == image && ${2:-} == inspect && -n ${3:-} ]]; then
  reference=$3
elif [[ ${1:-} == buildx && ${2:-} == imagetools && ${3:-} == inspect && -n ${4:-} ]]; then
  remote=true
  reference=$4
else
  echo "测试 Docker 收到未预期参数: $*" >&2
  exit 1
fi

repository=${reference%:*}
tag=${reference##*:}
if [[ $tag != "$TEST_VERSION" && $tag != "sha-${TEST_COMMIT:0:12}" ]]; then
  echo "测试 Docker 收到未预期标签: $reference" >&2
  exit 1
fi

case "$repository" in
  */backend)
    digest=$TEST_BACKEND_DIGEST
    component=backend
    image_id=$(printf 'c%.0s' {1..64})
    ;;
  */web)
    digest=$TEST_WEB_DIGEST
    component=web
    image_id=$(printf 'd%.0s' {1..64})
    ;;
  */worker)
    digest=$TEST_WORKER_DIGEST
    component=worker
    image_id=$(printf 'e%.0s' {1..64})
    ;;
  *)
    echo "测试 Docker 收到未预期镜像: $reference" >&2
    exit 1
    ;;
esac

if $remote; then
  if [[ ${TEST_REMOTE_DRIFT:-} == "$component" && $tag == "$TEST_VERSION" ]]; then
    digest=$(printf 'e%.0s' {1..64})
  fi
  printf '"sha256:%s"\n' "$digest"
  exit
fi

cat <<JSON
[{"Id":"sha256:$image_id","RepoDigests":["$repository@sha256:$digest"],"Config":{"Labels":{"org.opencontainers.image.version":"$TEST_VERSION","org.opencontainers.image.revision":"$TEST_COMMIT"}}}]
JSON
EOF
chmod +x "$TEST_DIR/bin/docker"
printf '@echo off\r\nbash "%%~dp0docker" %%*\r\n' >"$TEST_DIR/bin/docker.cmd"

export PATH="$TEST_DIR/bin:$PATH"
export TEST_VERSION=$VERSION
export TEST_COMMIT=$COMMIT
export TEST_BACKEND_DIGEST=$BACKEND_DIGEST
export TEST_WEB_DIGEST=$WEB_DIGEST
export TEST_WORKER_DIGEST=$WORKER_DIGEST

create_fixture() {
  local output_dir=$1
  local mode=${2:-valid}
  "$PYTHON" - "$output_dir" "$mode" "$VERSION" "$COMMIT" "$REGISTRY" \
    "$BACKEND_DIGEST" "$WEB_DIGEST" "$WORKER_DIGEST" <<'PY'
import gzip
import io
import json
import shutil
import sys
import tarfile
import zipfile
from pathlib import Path


output_dir = Path(sys.argv[1])
mode, version, commit, registry, backend_digest, web_digest, worker_digest = sys.argv[2:]
packages_dir = output_dir.parent / f"{output_dir.name}-packages"
shutil.rmtree(output_dir, ignore_errors=True)
shutil.rmtree(packages_dir, ignore_errors=True)
output_dir.mkdir(parents=True)
packages_dir.mkdir()


def write(path, content, executable=False):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(content if isinstance(content, bytes) else content.encode())
    path.chmod(0o755 if executable else 0o644)


windows_root = packages_dir / f"coinsphere-{version}-windows-x86"
linux_root = packages_dir / f"coinsphere-{version}-linux-amd64"
docker_root = packages_dir / f"coinsphere-{version}-docker"
for root, suffix in ((windows_root, ".exe"), (linux_root, "")):
    write(root / f"coinsphere-server{suffix}", b"binary", executable=root == linux_root)
    write(root / f"coinsphere-migrate{suffix}", b"binary", executable=root == linux_root)
    executor_content = (
        b"A" * 8192 + b"\0Authorization: Bearer /modelschoicescontentHTTP\0"
        if suffix == ".exe"
        else b"binary"
    )
    write(root / f"coinsphere-executor{suffix}", executor_content, executable=root == linux_root)
    write(root / "config.yml", 'auth:\n  secret_key: "coinsphere-dev-secret"\n')
    write(root / "nginx.conf", "server { listen 80; }\n")
    write(root / "README.md", "CoinSphere package\n")
    write(root / "web/index.html", "<!doctype html><title>CoinSphere</title>\n")

write(docker_root / "compose.yaml", "services: {}\n")
write(docker_root / "deploy.sh", "#!/usr/bin/env bash\nset -Eeuo pipefail\n", executable=True)
write(docker_root / "README.md", "CoinSphere Docker package\n")
write(
    docker_root / "runtime.env.example",
    "COINSPHERE_AUTH__SECRET_KEY=replace-with-openssl-rand-hex-32\n"
    "COINSPHERE_AUTH__ENCRYPTION_KEY=replace-with-an-independent-random-value\n"
    "COINSPHERE_AUTH__WEBHOOK_PEPPER=replace-with-an-independent-random-value\n"
    "COINSPHERE_AUTH__BOOTSTRAP_ADMIN_PASSWORD=replace-with-a-strong-random-password\n",
)
write(
    docker_root / "worker-runtime.env.example",
    "COINSPHERE_WORKER_DATABASE_DSN="
    "postgresql://coinsphere_worker:replace-with-database-password@database/coinsphere\n",
)
write(
    docker_root / "executor-runtime.env.example",
    "COINSPHERE_DATABASE__DSN="
    "postgresql://coinsphere_executor:replace-with-database-password@database/coinsphere\n",
)

if mode == "credential":
    key_name = "BINANCE_" + "API_KEY"
    write(linux_root / "web/settings.txt", f"{key_name}={'A1b2' * 10}\n")
elif mode == "credential-bang":
    write(linux_root / "web/settings.txt", "PASSWORD=S3cret!value-2026-long\n")
elif mode == "credential-dotted":
    write(linux_root / "web/settings.txt", "PASSWORD=correct.horse.battery.staple\n")
elif mode == "credential-prefixed":
    write(linux_root / "web/settings.txt", "AWS_SECRET_ACCESS_KEY=FAKEONLY0123456789FAKEONLY0123456789FAKE\n")
elif mode == "short-encryption-key":
    write(linux_root / "web/settings.txt", "COINSPHERE_AUTH__ENCRYPTION_KEY=Abc123\n")
elif mode == "short-webhook-pepper":
    write(linux_root / "web/settings.txt", "COINSPHERE_AUTH__WEBHOOK_PEPPER=Abc1234\n")
elif mode == "bearer":
    write(linux_root / "web/settings.txt", "Authorization: Bearer ABCDEFGHIJKLMNOPQRSTUVWXYZ\n")
elif mode == "private-key":
    header = "-----BEGIN " + "PRIVATE KEY-----"
    footer = "-----END " + "PRIVATE KEY-----"
    write(windows_root / "web/private.txt", f"{header}\n{'QUJD' * 20}\n{footer}\n")
elif mode == "encrypted-private-key":
    header = "-----BEGIN " + "ENCRYPTED PRIVATE KEY-----"
    footer = "-----END " + "ENCRYPTED PRIVATE KEY-----"
    write(
        windows_root / "web/private.txt",
        f"{header}\n{'QUJD' * 20}\n{footer}\n",
    )
elif mode == "legacy-encrypted-private-key":
    header = "-----BEGIN " + "RSA PRIVATE KEY-----"
    footer = "-----END " + "RSA PRIVATE KEY-----"
    write(
        windows_root / "web/private.txt",
        f"{header}\n"
        "Proc-Type: 4,ENCRYPTED\n"
        "DEK-Info: AES-256-CBC,0123456789ABCDEF\n\n"
        f"{'QUJD' * 20}\n"
        f"{footer}\n",
    )
elif mode == "pgp-private-key":
    header = "-----BEGIN " + "PGP PRIVATE KEY BLOCK-----"
    footer = "-----END " + "PGP PRIVATE KEY BLOCK-----"
    write(
        windows_root / "web/private.txt",
        f"{header}\n"
        f"{'QUJD' * 20}\n"
        f"{footer}\n",
    )
elif mode == "private-path":
    write(linux_root / "web/id_ecdsa", "fake private key fixture\n")
elif mode == "runtime-env":
    write(docker_root / "runtime.env", "COINSPHERE_AUTH__SECRET_KEY=" + "A1b2" * 10 + "\n")
elif mode == "misplaced-runtime-example":
    write(linux_root / "web/runtime.env.example", "placeholder only\n")
elif mode == "docker-config":
    auth_value = "dGVz" + "dDp0ZXN0"
    write(docker_root / ".docker/config.json", json.dumps({"auths": {registry: {"auth": auth_value}}}))
elif mode in {"nested-zip", "nested-sfx-zip", "nested-gzip"}:
    if mode in {"nested-zip", "nested-sfx-zip"}:
        nested = io.BytesIO()
        with zipfile.ZipFile(nested, "w") as archive:
            archive.writestr("runtime.env", "PASSWORD=fake-only-password\n")
        content = nested.getvalue()
        if mode == "nested-sfx-zip":
            content = b"MZ-FAKE-SFX-PREFIX" + content
    else:
        content = gzip.compress(b"PASSWORD=fake-only-password\n")
    write(linux_root / "web/assets.dat", content)

windows_archive = output_dir / f"coinsphere-{version}-windows-x86.zip"
with zipfile.ZipFile(windows_archive, "w", compression=zipfile.ZIP_DEFLATED) as archive:
    for path in sorted(windows_root.rglob("*")):
        archive_name = path.relative_to(packages_dir).as_posix()
        if mode == "zip-member-metadata" and path.name == "index.html":
            info = zipfile.ZipInfo.from_file(path, archive_name)
            info.comment = b"PASSWORD=fake-only-password"
            info.extra = b"\xfe\xca\x00\x00"
            archive.writestr(info, path.read_bytes(), compress_type=zipfile.ZIP_DEFLATED)
        else:
            archive.write(path, archive_name)
    if mode == "zip-traversal":
        archive.writestr("../escape.txt", "blocked")
    if mode == "zip-log-injection":
        archive.writestr("LOG_INJECTION\nMARKER", "blocked")
    if mode == "zip-comment":
        archive.comment = b"PASSWORD=fake-only-password"
if mode == "zip-prefix":
    windows_archive.write_bytes(
        b"PASSWORD=fake-only-password\n" + windows_archive.read_bytes()
    )
elif mode == "zip-local-extra":
    with zipfile.ZipFile(windows_archive) as archive:
        member = max(archive.infolist(), key=lambda item: item.header_offset)
        central_offset = archive.start_dir
    raw = bytearray(windows_archive.read_bytes())
    filename_size = int.from_bytes(
        raw[member.header_offset + 26 : member.header_offset + 28], "little"
    )
    extra = b"PASSWORD=fake-only-password"
    raw[member.header_offset + 28 : member.header_offset + 30] = len(extra).to_bytes(
        2, "little"
    )
    insert_at = member.header_offset + 30 + filename_size
    raw[insert_at:insert_at] = extra
    end_offset = len(raw) - 22
    raw[end_offset + 16 : end_offset + 20] = (
        central_offset + len(extra)
    ).to_bytes(4, "little")
    windows_archive.write_bytes(raw)
elif mode == "zip-trailing":
    with windows_archive.open("ab") as archive:
        archive.write(b"PASSWORD=fake-only-password")


def create_tar(root, destination, add_link=False):
    archive_format = tarfile.PAX_FORMAT if mode == "tar-pax" and root == linux_root else tarfile.GNU_FORMAT

    def archive_filter(info):
        info.uid = info.gid = 0
        info.uname = info.gname = ""
        info.mtime = 0
        if root == linux_root and info.name.endswith(
            ("/coinsphere-server", "/coinsphere-migrate", "/coinsphere-executor")
        ):
            info.mode = 0o755
        if root == docker_root and info.name.endswith("/deploy.sh"):
            info.mode = 0o755
        if mode == "tar-pax" and root == linux_root and info.name.endswith("/web/index.html"):
            info.pax_headers = {"comment": "PASSWORD=fake-only-password"}
        if mode == "tar-uname" and root == linux_root and info.name.endswith("/web/index.html"):
            info.uname = "PASSWORD=fake-only-password"
        if mode == "linux-not-executable" and root == linux_root and info.name.endswith("/coinsphere-server"):
            info.mode &= ~0o111
        if mode == "docker-not-executable" and root == docker_root and info.name.endswith("/deploy.sh"):
            info.mode &= ~0o111
        return info

    tar_buffer = io.BytesIO()
    with tarfile.open(fileobj=tar_buffer, mode="w", format=archive_format) as archive:
        archive.add(root, arcname=root.name, filter=archive_filter)
        if add_link:
            link = tarfile.TarInfo(f"{root.name}/web/link")
            link.type = tarfile.SYMTYPE
            link.linkname = "../../escape"
            link.uid = link.gid = 0
            link.uname = link.gname = ""
            link.mtime = 0
            archive.addfile(link)
    tar_payload = tar_buffer.getvalue()
    if mode == "tar-trailing" and root == linux_root:
        hidden = b"PASSWORD=fake-only-password"
        tar_payload = tar_payload[: -len(hidden)] + hidden

    gzip_buffer = io.BytesIO()
    with gzip.GzipFile(
        fileobj=gzip_buffer,
        mode="wb",
        filename="",
        mtime=0,
        compresslevel=6,
    ) as archive:
        archive.write(tar_payload)
    gzip_payload = gzip_buffer.getvalue()
    if mode == "gzip-comment" and root == linux_root:
        gzip_payload = (
            gzip_payload[:3]
            + bytes([gzip_payload[3] | 0x10])
            + gzip_payload[4:10]
            + b"PASSWORD=fake-only-password\0"
            + gzip_payload[10:]
        )
    elif mode == "gzip-extra" and root == linux_root:
        extra = b"PASSWORD=fake-only-password"
        gzip_payload = (
            gzip_payload[:3]
            + bytes([gzip_payload[3] | 0x04])
            + gzip_payload[4:10]
            + len(extra).to_bytes(2, "little")
            + extra
            + gzip_payload[10:]
        )
    elif mode == "gzip-concatenated" and root == linux_root:
        gzip_payload += gzip.compress(b"PASSWORD=fake-only-password", mtime=0)
    elif mode == "gzip-trailing" and root == linux_root:
        gzip_payload += b"PASSWORD=fake-only-password"
    destination.write_bytes(gzip_payload)


create_tar(
    linux_root,
    output_dir / f"coinsphere-{version}-linux-amd64.tar.gz",
    add_link=mode == "tar-link",
)
create_tar(docker_root, output_dir / f"coinsphere-{version}-docker.tar.gz")

manifest = {
    "version": version,
    "commit": commit,
    "backendImage": f"{registry}/coinsphere/backend:{version}",
    "backendDigest": f"{registry}/coinsphere/backend@sha256:{backend_digest}",
    "webImage": f"{registry}/coinsphere/web:{version}",
    "webDigest": f"{registry}/coinsphere/web@sha256:{web_digest}",
    "workerImage": f"{registry}/coinsphere/worker:{version}",
    "workerDigest": f"{registry}/coinsphere/worker@sha256:{worker_digest}",
}
if mode == "manifest-extra":
    manifest["unexpected"] = "blocked"
elif mode == "manifest-digest":
    manifest["backendDigest"] = f"{registry}/coinsphere/backend@sha256:{'e' * 64}"
if mode == "manifest-duplicate":
    manifest_json = '{"LOG_INJECTION\\nMARKER":"one","LOG_INJECTION\\nMARKER":"two",' + json.dumps(manifest)[1:]
else:
    manifest_json = json.dumps(manifest)
(output_dir / "release-manifest.json").write_text(manifest_json, encoding="utf-8")


def sbom(component):
    digest = {
        "backend": backend_digest,
        "web": web_digest,
        "worker": worker_digest,
    }[component]
    checksum_digest = "e" * 64 if mode == "sbom-digest" and component == "backend" else digest
    repository = f"{registry}/coinsphere/{component}"
    if mode == "sbom-unbound" and component == "backend":
        repository = f"{registry}/coinsphere/unrelated"
    package_id = f"SPDXRef-Package-{component}"
    return {
        "spdxVersion": "SPDX-2.3",
        "SPDXID": "SPDXRef-DOCUMENT",
        "dataLicense": "CC0-1.0",
        "name": repository,
        "documentNamespace": f"https://coinsphere.invalid/{component}/{version}",
        "creationInfo": {"created": "2026-08-02T00:00:00Z", "creators": ["Tool: test-fixture"]},
        "packages": []
        if mode == "sbom-invalid" and component == "backend"
        else [
            {
                "SPDXID": package_id,
                "name": repository,
                "versionInfo": f"sha256:{digest}",
                "primaryPackagePurpose": "CONTAINER",
                "checksums": [{"algorithm": "SHA256", "checksumValue": checksum_digest}],
            }
        ],
        "relationships": [
            {
                "spdxElementId": "SPDXRef-DOCUMENT",
                "relationshipType": "DESCRIBES",
                "relatedSpdxElement": package_id,
            }
        ],
    }


for component in ("backend", "web", "worker"):
    path = output_dir / f"coinsphere-{version}-{component}.spdx.json"
    path.write_text(json.dumps(sbom(component)), encoding="utf-8")
PY

  (
    cd "$output_dir"
    sha256sum --text \
      "./coinsphere-$VERSION-windows-x86.zip" \
      "./coinsphere-$VERSION-linux-amd64.tar.gz" \
      "./coinsphere-$VERSION-docker.tar.gz" \
      ./release-manifest.json \
      "./coinsphere-$VERSION-backend.spdx.json" \
      "./coinsphere-$VERSION-web.spdx.json" \
      "./coinsphere-$VERSION-worker.spdx.json" >SHA256SUMS
  )
}

run_scan() {
  local output_dir=$1
  local log_file=$2
  PYTHONUTF8=1 COINSPHERE_REGISTRY=$REGISTRY TEST_REMOTE_DRIFT=${TEST_REMOTE_DRIFT:-} \
    "$PYTHON" "$ROOT_DIR/scripts/release/scan-artifacts.py" \
    "$VERSION" "$COMMIT" "$output_dir" >"$log_file" 2>&1
}

assert_rejected() {
  local case_name=$1
  local mode=$2
  local expected_message=$3
  local output_dir="$TEST_DIR/$case_name"
  local log_file="$TEST_DIR/$case_name.log"
  create_fixture "$output_dir" "$mode"
  local status=0
  run_scan "$output_dir" "$log_file" || status=$?
  if [[ $status -eq 0 ]]; then
    echo "危险产物应被拒绝: $case_name" >&2
    exit 1
  fi
  if ! grep -Fq "$expected_message" "$log_file"; then
    echo "危险产物未命中预期规则: $case_name" >&2
    cat "$log_file" >&2
    exit 1
  fi
}

create_fixture "$TEST_DIR/valid"
if ! run_scan "$TEST_DIR/valid" "$TEST_DIR/valid.log"; then
  echo "合法最终产物应通过扫描" >&2
  cat "$TEST_DIR/valid.log" >&2
  exit 1
fi

create_fixture "$TEST_DIR/symlink-target/dist"
if "$PYTHON" - "$TEST_DIR/symlink-target" "$TEST_DIR/symlink-parent" <<'PY'
import os
import sys

try:
    os.symlink(sys.argv[1], sys.argv[2], target_is_directory=True)
except OSError:
    raise SystemExit(1)
PY
then
  status=0
  run_scan "$TEST_DIR/symlink-parent/dist" "$TEST_DIR/symlink-parent.log" || status=$?
  if [[ $status -eq 0 ]] || ! grep -Fq "产物目录无效" "$TEST_DIR/symlink-parent.log"; then
    echo "经父级符号链接访问的产物目录应被拒绝" >&2
    exit 1
  fi
fi

create_fixture "$TEST_DIR/extra"
touch "$TEST_DIR/extra/unexpected.txt"
status=0
run_scan "$TEST_DIR/extra" "$TEST_DIR/extra.log" || status=$?
if [[ $status -eq 0 ]] || ! grep -Fq "最终文件清单" "$TEST_DIR/extra.log"; then
  echo "额外产物应被最终清单拒绝" >&2
  exit 1
fi

create_fixture "$TEST_DIR/checksum"
printf 'tampered' >>"$TEST_DIR/checksum/coinsphere-$VERSION-web.spdx.json"
status=0
run_scan "$TEST_DIR/checksum" "$TEST_DIR/checksum.log" || status=$?
if [[ $status -eq 0 ]] || ! grep -Fq "SHA256SUMS 校验失败" "$TEST_DIR/checksum.log"; then
  echo "篡改产物应被校验和拒绝" >&2
  exit 1
fi

assert_rejected manifest-extra manifest-extra "字段清单"
assert_rejected manifest-digest manifest-digest "RepoDigest"
assert_rejected sbom-invalid sbom-invalid "SPDX JSON 组件契约"
assert_rejected sbom-unbound sbom-unbound "SPDX JSON 组件契约"
assert_rejected sbom-digest sbom-digest "SPDX JSON 组件契约"
assert_rejected credential credential "非占位凭据赋值"
assert_rejected credential-bang credential-bang "非占位凭据赋值"
assert_rejected credential-dotted credential-dotted "非占位凭据赋值"
assert_rejected credential-prefixed credential-prefixed "非占位凭据赋值"
assert_rejected short-encryption-key short-encryption-key "非占位凭据赋值"
assert_rejected short-webhook-pepper short-webhook-pepper "非占位凭据赋值"
assert_rejected bearer bearer "Authorization 凭据"
assert_rejected private-key private-key "敏感内容规则: 私钥"
assert_rejected encrypted-private-key encrypted-private-key "敏感内容规则: 私钥"
assert_rejected legacy-encrypted-private-key legacy-encrypted-private-key "敏感内容规则: 私钥"
assert_rejected pgp-private-key pgp-private-key "敏感内容规则: 私钥"
assert_rejected private-path private-path "禁止路径: 凭据文件"
assert_rejected runtime-env runtime-env "禁止路径: 运行时 env"
assert_rejected misplaced-runtime-example misplaced-runtime-example "禁止路径: 运行时 env"
assert_rejected docker-config docker-config "禁止路径: Docker 登录配置"
assert_rejected zip-traversal zip-traversal "上级目录段"
assert_rejected zip-comment zip-comment "ZIP 注释或尾随数据"
assert_rejected zip-member-metadata zip-member-metadata "ZIP 本地文件头元数据"
assert_rejected zip-prefix zip-prefix "ZIP 注释或尾随数据"
assert_rejected zip-local-extra zip-local-extra "ZIP 本地文件头元数据"
assert_rejected zip-trailing zip-trailing "ZIP 注释或尾随数据"
assert_rejected tar-link tar-link "非规范 TAR 成员类型"
assert_rejected tar-pax tar-pax "TAR PAX 元数据"
assert_rejected tar-uname tar-uname "非规范 TAR 元数据"
assert_rejected tar-trailing tar-trailing "TAR 尾随数据"
assert_rejected gzip-comment gzip-comment "非规范 GZIP 元数据"
assert_rejected gzip-extra gzip-extra "非规范 GZIP 元数据"
assert_rejected gzip-concatenated gzip-concatenated "GZIP 尾随或串接数据"
assert_rejected gzip-trailing gzip-trailing "GZIP 尾随或串接数据"
assert_rejected nested-zip nested-zip "嵌套归档内容"
assert_rejected nested-sfx-zip nested-sfx-zip "嵌套归档内容"
assert_rejected nested-gzip nested-gzip "嵌套归档内容"
assert_rejected linux-not-executable linux-not-executable "缺少执行权限"
assert_rejected docker-not-executable docker-not-executable "缺少执行权限"

create_fixture "$TEST_DIR/remote-drift"
status=0
TEST_REMOTE_DRIFT=backend run_scan "$TEST_DIR/remote-drift" "$TEST_DIR/remote-drift.log" || status=$?
if [[ $status -eq 0 ]] || ! grep -Fq "远端镜像 digest" "$TEST_DIR/remote-drift.log"; then
  echo "远端版本标签漂移应被拒绝" >&2
  exit 1
fi

create_fixture "$TEST_DIR/duplicate-json" manifest-duplicate
status=0
run_scan "$TEST_DIR/duplicate-json" "$TEST_DIR/duplicate-json.log" || status=$?
if [[ $status -eq 0 ]] || ! grep -Fq "重复 JSON 字段" "$TEST_DIR/duplicate-json.log" || grep -Fq "MARKER" "$TEST_DIR/duplicate-json.log"; then
  echo "重复 JSON 字段应被拒绝且不得回显字段内容" >&2
  exit 1
fi

create_fixture "$TEST_DIR/zip-log-injection" zip-log-injection
status=0
run_scan "$TEST_DIR/zip-log-injection" "$TEST_DIR/zip-log-injection.log" || status=$?
if [[ $status -eq 0 ]] || ! grep -Fq "控制字符路径" "$TEST_DIR/zip-log-injection.log" || grep -Fq "MARKER" "$TEST_DIR/zip-log-injection.log"; then
  echo "危险归档路径应被拒绝且不得回显原始名称" >&2
  exit 1
fi

create_fixture "$TEST_DIR/checksum-size"
printf 'A%.0s' {1..5000} >"$TEST_DIR/checksum-size/SHA256SUMS"
status=0
run_scan "$TEST_DIR/checksum-size" "$TEST_DIR/checksum-size.log" || status=$?
if [[ $status -eq 0 ]] || ! grep -Fq "SHA256SUMS 超过允许大小" "$TEST_DIR/checksum-size.log"; then
  echo "超大 SHA256SUMS 应被拒绝" >&2
  exit 1
fi

echo "最终发布产物安全与完整性扫描测试通过"

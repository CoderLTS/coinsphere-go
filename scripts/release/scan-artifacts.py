#!/usr/bin/env python3

import argparse
import hashlib
import io
import json
import os
import re
import shutil
import stat
import struct
import subprocess
import sys
import tarfile
import tempfile
import zipfile
import zlib
from pathlib import Path

VERSION_RE = re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+(?:[.-][0-9A-Za-z.-]+)?$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
CHECKSUM_RE = re.compile(r"^([0-9a-f]{64})  \./([0-9A-Za-z][0-9A-Za-z._-]*)$")
MAX_MEMBER_COUNT = 100_000
MAX_MEMBER_SIZE = 512 * 1024 * 1024
MAX_ARCHIVE_SIZE = 1024 * 1024 * 1024
MAX_JSON_SIZE = 512 * 1024 * 1024
MAX_MANIFEST_SIZE = 64 * 1024
MAX_CHECKSUM_SIZE = 4096
ZIP_LOCAL_HEADER = struct.Struct("<4s5H3L2H")
ZIP_END_RECORD = struct.Struct("<4s4H2LH")

SAFE_PLACEHOLDERS = {
    b"changeme",
    b"coinsphere",
    b"coinsphere-dev-secret",
    b"current password",
    b"dummy",
    b"enter the api key",
    b"example",
    b"api key",
    b"access token",
    b"password",
    b"secret",
    b"test",
    "密码".encode(),
    "当前密码".encode(),
    "访问令牌".encode(),
    "签名密钥".encode(),
}
SAFE_PLACEHOLDER_PREFIXES = (b"replace-with-", b"please enter ", "请输入".encode())
SENSITIVE_KEY = (
    rb"(?:[A-Z0-9]+(?:__|[_-]))*"
    rb"(?:API[_-]?KEY|SECRET(?:[_-]?(?:ACCESS[_-]?)?KEY)?|ACCESS[_-]?TOKEN|AUTH[_-]?TOKEN|"
    rb"PASSWORD|PASSWD|PRIVATE[_-]?KEY|CLIENT[_-]?SECRET|ENCRYPTION[_-]?KEY|WEBHOOK[_-]?PEPPER)"
)
SENSITIVE_ASSIGNMENT_PATTERNS = (
    re.compile(
        rb"(?ix)(?:^|[^A-Z0-9_-])['\"]?" + SENSITIVE_KEY + rb"['\"]?\s*[:=]\s*"
        rb'(?:"([^"\r\n]{6,})"|\'([^\'\r\n]{6,})\')'
    ),
    re.compile(
        rb"(?m)(?:^|[^A-Z0-9_])(?:export[ \t]+)?"
        + SENSITIVE_KEY
        + rb"[ \t]*=[ \t]*([^\s#;,}{\]\[]{6,})"
    ),
    re.compile(
        rb"(?im)^[ \t-]*['\"]?"
        + SENSITIVE_KEY
        + rb"['\"]?[ \t]*:[ \t]*([^\s#;,}{\]\[]{6,})"
    ),
)
SENSITIVE_CONTENT_PATTERNS = (
    ("AWS 访问密钥", re.compile(rb"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b")),
    ("GitHub Token", re.compile(rb"\bgh[pousr]_[A-Za-z0-9]{36,}\b")),
    ("GitHub Token", re.compile(rb"\bgithub_pat_[A-Za-z0-9_]{50,}\b")),
    ("Slack Token", re.compile(rb"\bxox[baprs]-[A-Za-z0-9-]{20,}\b")),
    (
        "Docker 登录配置",
        re.compile(rb"(?i)['\"](?:auths|credsStore|credHelpers)['\"]\s*:"),
    ),
    (
        "私钥",
        re.compile(
            rb"-----BEGIN (?:(?:RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY|"
            rb"PGP PRIVATE KEY BLOCK)-----"
        ),
    ),
)
AUTHORIZATION_RE = re.compile(rb"(?i)\b(?:Basic|Bearer)[ \t]+([A-Za-z0-9._~+/=-]{20,})")
NESTED_ARCHIVE_SUFFIXES = (
    ".zip",
    ".tar",
    ".tar.gz",
    ".tgz",
    ".7z",
    ".rar",
    ".gz",
    ".bz2",
    ".xz",
)
PRIVATE_FILE_SUFFIXES = (".key", ".pem", ".p12", ".pfx", ".ppk")
WINDOWS_RESERVED_NAMES = {
    "con",
    "prn",
    "aux",
    "nul",
    *(f"com{number}" for number in range(1, 10)),
    *(f"lpt{number}" for number in range(1, 10)),
}


class ScanError(Exception):
    pass


def parse_args():
    parser = argparse.ArgumentParser(description="扫描 CoinSphere 最终发布产物")
    parser.add_argument("version")
    parser.add_argument("commit")
    parser.add_argument("output_dir", nargs="?", default="dist")
    return parser.parse_args()


def strict_json(raw, label):
    def reject_duplicates(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ScanError(f"{label} 包含重复 JSON 字段")
            result[key] = value
        return result

    def reject_constant(value):
        raise ScanError(f"{label} 包含无效 JSON 常量")

    try:
        return json.loads(
            raw.decode("utf-8"),
            object_pairs_hook=reject_duplicates,
            parse_constant=reject_constant,
        )
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ScanError(f"{label} 不是有效 UTF-8 JSON") from error


def read_limited(path, limit, label):
    size = path.stat().st_size
    if size > limit:
        raise ScanError(f"{label} 超过允许大小")
    return path.read_bytes()


def scan_sensitive_content(data, label):
    for rule_name, pattern in SENSITIVE_CONTENT_PATTERNS:
        if pattern.search(data):
            raise ScanError(f"{label} 命中敏感内容规则: {rule_name}")
    binary = b"\0" in data
    for match in AUTHORIZATION_RE.finditer(data):
        token = match.group(1)
        go_string_table_join = (
            binary
            and token.isalpha()
            and not token.islower()
            and not token.isupper()
            and token.endswith(b"HTTP")
        )
        if (
            not binary
            # Go 二进制字符串表会把 Bearer 前缀与相邻的 HTTP 标识符拼接。
            or (token.isalpha() and not go_string_table_join)
            or any(character in b"0123456789.+=-" for character in token)
        ):
            raise ScanError(f"{label} 命中敏感内容规则: Authorization 凭据")
    for pattern in SENSITIVE_ASSIGNMENT_PATTERNS:
        for match in pattern.finditer(data):
            value = (
                next(group for group in match.groups() if group is not None)
                .strip(b"'\"")
                .lower()
            )
            if value in SAFE_PLACEHOLDERS or value.startswith(
                SAFE_PLACEHOLDER_PREFIXES
            ):
                continue
            raise ScanError(f"{label} 包含非占位凭据赋值")


def sensitive_path(path, kind, version):
    parts = [part.casefold() for part in path.split("/")]
    basename = parts[-1]
    if kind == "docker" and parts in (
        [f"coinsphere-{version}-docker".casefold(), "runtime.env.example"],
    ):
        return None
    if basename == "runtime.env.example":
        return "运行时 env"
    if (
        basename.startswith(".env")
        or basename == "runtime.env"
        or basename.endswith(".env")
    ):
        return "运行时 env"
    if basename in {
        ".dockercfg",
        ".dockerconfigjson",
        ".netrc",
        "credentials",
        "credentials.json",
        "docker-config.json",
        "id_dsa",
        "id_ecdsa",
        "id_ed25519",
        "id_rsa",
    }:
        return "凭据文件"
    if basename.endswith(PRIVATE_FILE_SUFFIXES):
        return "私钥文件"
    if (
        len(parts) >= 2
        and parts[-2] in {".docker", "docker"}
        and basename == "config.json"
    ):
        return "Docker 登录配置"
    if basename.endswith(NESTED_ARCHIVE_SUFFIXES):
        return "嵌套归档"
    return None


def normalize_member_name(name, label):
    if not name or not name.isascii():
        raise ScanError(f"{label} 包含空名称或非 ASCII 路径")
    if "\\" in name or any(
        ord(character) < 32 or ord(character) == 127 for character in name
    ):
        raise ScanError(f"{label} 包含反斜杠或控制字符路径")
    trimmed = name[:-1] if name.endswith("/") else name  # noqa: FURB188
    if (
        not trimmed
        or trimmed.startswith(("/", "-"))
        or re.match(r"^[A-Za-z]:", trimmed)
    ):
        raise ScanError(f"{label} 包含绝对路径、盘符或危险前缀")
    parts = trimmed.split("/")
    if any(part in {"", ".", ".."} for part in parts):
        raise ScanError(f"{label} 包含空、当前或上级目录段")
    for part in parts:
        if part.endswith((" ", ".")) or ":" in part:
            raise ScanError(f"{label} 包含 Windows 危险路径")
        if part.split(".", 1)[0].casefold() in WINDOWS_RESERVED_NAMES:
            raise ScanError(f"{label} 包含 Windows 保留名称")
    return "/".join(parts)


def check_archive_inventory(kind, files, directories, version):
    root = f"coinsphere-{version}-{kind}"
    if kind == "docker":
        expected = {
            f"{root}/README.md",
            f"{root}/compose.yaml",
            f"{root}/deploy.sh",
            f"{root}/runtime.env.example",
        }
        if files != expected or any(directory != root for directory in directories):
            raise ScanError("Docker 归档内部文件清单不匹配")
        return

    executable_suffix = ".exe" if kind == "windows-x86" else ""
    fixed = {
        f"{root}/README.md",
        f"{root}/coinsphere-migrate{executable_suffix}",
        f"{root}/coinsphere-server{executable_suffix}",
        f"{root}/config.yml",
        f"{root}/nginx.conf",
    }
    web_root = f"{root}/web"
    if not fixed.issubset(files) or f"{web_root}/index.html" not in files:
        raise ScanError(f"{kind} 归档缺少固定文件或 web/index.html")
    if any(path not in fixed and not path.startswith(f"{web_root}/") for path in files):
        raise ScanError(f"{kind} 归档包含清单外文件")
    if any(
        directory != root
        and directory != web_root
        and not directory.startswith(f"{web_root}/")
        for directory in directories
    ):
        raise ScanError(f"{kind} 归档包含清单外目录")


def scan_member(reader, size, label):
    if size > MAX_MEMBER_SIZE:
        raise ScanError(f"{label} 超过单文件大小限制")
    data = reader.read(size + 1)
    if len(data) != size:
        raise ScanError(f"{label} 内容长度与归档元数据不一致")
    if (
        data.startswith(
            (
                b"PK\x03\x04",
                b"PK\x05\x06",
                b"PK\x07\x08",
                b"\x1f\x8b\x08",
                b"7z\xbc\xaf'\x1c",
                b"\xfd7zXZ\x00",
                b"BZh",
                b"Rar!\x1a\x07",
            )
        )
        or len(data) >= 262
        and data[257:262] == b"ustar"
        or zipfile.is_zipfile(io.BytesIO(data))
    ):
        raise ScanError(f"{label} 包含嵌套归档内容")
    scan_sensitive_content(data, label)


def register_member(
    name, is_directory, label, seen, seen_casefold, files, directories, kind, version
):
    normalized = normalize_member_name(name, label)
    folded = normalized.casefold()
    if normalized in seen or folded in seen_casefold:
        raise ScanError(f"{label} 包含重复或大小写冲突路径")
    seen.add(normalized)
    seen_casefold.add(folded)
    scan_sensitive_content(normalized.encode("ascii"), label)
    if is_directory:
        directories.add(normalized)
        return normalized
    rule = sensitive_path(normalized, kind, version)
    if rule:
        raise ScanError(f"{label} 包含禁止路径: {rule}")
    files.add(normalized)
    return normalized


def scan_zip(path, kind, version):
    label = path.name
    files, directories, seen, seen_casefold = set(), set(), set(), set()
    total_size = 0
    archive_size = path.stat().st_size
    if archive_size < ZIP_END_RECORD.size:
        raise ScanError(f"{label} ZIP 结构无效")
    with path.open("rb") as source:
        source.seek(-ZIP_END_RECORD.size, os.SEEK_END)
        end_record = ZIP_END_RECORD.unpack(source.read(ZIP_END_RECORD.size))
        (
            signature,
            disk_number,
            central_disk,
            disk_members,
            member_count,
            central_size,
            central_offset,
            comment_size,
        ) = end_record
        if (
            signature != b"PK\x05\x06"
            or disk_number
            or central_disk
            or disk_members != member_count
            or comment_size
            or central_offset + central_size != archive_size - ZIP_END_RECORD.size
        ):
            raise ScanError(f"{label} 包含 ZIP 注释或尾随数据")
    with zipfile.ZipFile(path) as archive, path.open("rb") as source:
        if archive.comment:
            raise ScanError(f"{label} 包含 ZIP 注释或尾随数据")
        members = archive.infolist()
        if not members or len(members) != member_count:
            raise ScanError(f"{label} ZIP 结构无效")
        if len(members) > MAX_MEMBER_COUNT:
            raise ScanError(f"{label} 成员数量超过限制")
        expected_offset = 0
        for member in sorted(members, key=lambda item: item.header_offset):
            if member.header_offset != expected_offset:
                raise ScanError(f"{label} 包含 ZIP 前缀或未索引数据")
            source.seek(member.header_offset)
            header = source.read(ZIP_LOCAL_HEADER.size)
            if len(header) != ZIP_LOCAL_HEADER.size:
                raise ScanError(f"{label} ZIP 本地文件头不完整")
            (
                local_signature,
                _extract_version,
                local_flags,
                local_compression,
                _modified_time,
                _modified_date,
                local_crc,
                local_compressed_size,
                local_file_size,
                filename_size,
                extra_size,
            ) = ZIP_LOCAL_HEADER.unpack(header)
            local_filename = source.read(filename_size)
            local_extra = source.read(extra_size)
            if (
                local_signature != b"PK\x03\x04"
                or local_flags
                or local_flags != member.flag_bits
                or local_compression != member.compress_type
                or local_crc != member.CRC
                or local_compressed_size != member.compress_size
                or local_file_size != member.file_size
                or local_filename != member.filename.encode("ascii", errors="strict")
            ):
                raise ScanError(f"{label} ZIP 本地文件头与目录不匹配")
            if local_extra:
                raise ScanError(f"{label} 包含 ZIP 本地文件头元数据")
            expected_offset = source.tell() + member.compress_size
        if expected_offset != central_offset:
            raise ScanError(f"{label} 包含 ZIP 前缀或未索引数据")
        for member in members:
            member_label = f"{label}!归档成员"
            if member.comment or member.extra:
                raise ScanError(f"{member_label} 包含 ZIP 成员元数据")
            if member.flag_bits:
                raise ScanError(f"{member_label} 包含 ZIP 标志元数据")
            mode = (member.external_attr >> 16) & 0xFFFF
            file_type = stat.S_IFMT(mode)
            is_directory = member.is_dir()
            if mode & 0o6000:
                raise ScanError(f"{member_label} 带有 setuid/setgid 权限")
            if is_directory:
                if file_type not in {0, stat.S_IFDIR}:
                    raise ScanError(f"{member_label} 目录类型异常")
            elif file_type not in {0, stat.S_IFREG}:
                raise ScanError(f"{member_label} 不是普通文件")
            normalized = register_member(
                member.filename,
                is_directory,
                member_label,
                seen,
                seen_casefold,
                files,
                directories,
                kind,
                version,
            )
            member_label = f"{label}!{normalized}"
            if (
                kind == "linux-amd64"
                and normalized.endswith(
                    ("/coinsphere-server", "/coinsphere-migrate")
                )
                and not mode & 0o111
            ):
                raise ScanError(f"{member_label} 缺少执行权限")
            if (
                kind == "docker"
                and normalized.endswith("/deploy.sh")
                and not mode & 0o111
            ):
                raise ScanError(f"{member_label} 缺少执行权限")
            if not is_directory:
                total_size += member.file_size
                if total_size > MAX_ARCHIVE_SIZE:
                    raise ScanError(f"{label} 解压后总大小超过限制")
                with archive.open(member) as reader:
                    scan_member(reader, member.file_size, f"{label}!{normalized}")
    check_archive_inventory(kind, files, directories, version)


def decompress_gzip(path, destination):
    label = path.name
    with path.open("rb") as source:
        header = source.read(10)
        if len(header) != 10 or header[:3] != b"\x1f\x8b\x08":
            raise ScanError(f"{label} GZIP 结构无效")
        if header[3] or header[4:8] != b"\0\0\0\0":
            raise ScanError(f"{label} 包含非规范 GZIP 元数据")
        source.seek(0)
        decompressor = zlib.decompressobj(16 + zlib.MAX_WBITS)
        total_size = 0
        try:
            while not decompressor.eof:
                chunk = source.read(64 * 1024)
                if not chunk:
                    break
                pending = chunk
                while pending:
                    data = decompressor.decompress(pending, 1024 * 1024)
                    pending = decompressor.unconsumed_tail
                    total_size += len(data)
                    if total_size > MAX_ARCHIVE_SIZE:
                        raise ScanError(f"{label} 解压后总大小超过限制")
                    destination.write(data)
                    if decompressor.unused_data:
                        raise ScanError(f"{label} 包含 GZIP 尾随或串接数据")
            if not decompressor.eof or decompressor.unused_data or source.read(1):
                raise ScanError(f"{label} 包含 GZIP 尾随或串接数据")
            data = decompressor.flush()
        except zlib.error as error:
            raise ScanError(f"{label} GZIP 结构无效") from error
        total_size += len(data)
        if total_size > MAX_ARCHIVE_SIZE:
            raise ScanError(f"{label} 解压后总大小超过限制")
        destination.write(data)
    destination.seek(0)


def validate_tar_layout(tar_stream, label):
    tar_stream.seek(0, os.SEEK_END)
    tar_size = tar_stream.tell()
    if not tar_size or tar_size % tarfile.RECORDSIZE:
        raise ScanError(f"{label} TAR 结束结构无效")

    offset = 0
    physical_members = 0
    while offset < tar_size:
        tar_stream.seek(offset)
        header = tar_stream.read(tarfile.BLOCKSIZE)
        if len(header) != tarfile.BLOCKSIZE:
            raise ScanError(f"{label} TAR 结构不完整")
        if header == tarfile.NUL * tarfile.BLOCKSIZE:
            trailing_size = tar_size - offset
            # 结束块从记录最后一块开始时，GNU tar 会补齐下一条完整记录。
            if (
                trailing_size < tarfile.BLOCKSIZE * 2
                or trailing_size > tarfile.RECORDSIZE + tarfile.BLOCKSIZE
            ):
                raise ScanError(f"{label} TAR 结束结构无效")
            tar_stream.seek(offset)
            for chunk in iter(lambda: tar_stream.read(64 * 1024), b""):
                if chunk.strip(tarfile.NUL):
                    raise ScanError(f"{label} 包含 TAR 尾随数据")
            tar_stream.seek(0)
            return

        physical_members += 1
        if physical_members > MAX_MEMBER_COUNT * 2:
            raise ScanError(f"{label} 成员数量超过限制")
        try:
            member = tarfile.TarInfo.frombuf(header, "ascii", "strict")
        except (tarfile.HeaderError, UnicodeError, ValueError) as error:
            raise ScanError(f"{label} TAR 结构无效") from error
        if member.type in {
            tarfile.XHDTYPE,
            tarfile.XGLTYPE,
            tarfile.SOLARIS_XHDTYPE,
        }:
            raise ScanError(f"{label} 包含 TAR PAX 元数据")
        if member.type not in {
            tarfile.REGTYPE,
            tarfile.AREGTYPE,
            tarfile.DIRTYPE,
            tarfile.GNUTYPE_LONGNAME,
        }:
            raise ScanError(f"{label} 包含非规范 TAR 成员类型")
        if (
            member.uid != 0
            or member.gid != 0
            or member.uname
            or member.gname
            or member.mtime != 0
            or member.linkname
            or member.mode & 0o6000
        ):
            raise ScanError(f"{label} 包含非规范 TAR 元数据")
        scan_sensitive_content(header, f"{label}!TAR 文件头")

        if member.size < 0 or member.size > MAX_MEMBER_SIZE:
            raise ScanError(f"{label} TAR 成员大小无效")
        data_offset = offset + tarfile.BLOCKSIZE
        padded_size = (member.size + tarfile.BLOCKSIZE - 1) // tarfile.BLOCKSIZE
        padded_size *= tarfile.BLOCKSIZE
        if data_offset + padded_size > tar_size:
            raise ScanError(f"{label} TAR 成员越过归档末尾")
        if member.type == tarfile.GNUTYPE_LONGNAME:
            if member.size > 4096:
                raise ScanError(f"{label} TAR 长路径元数据过大")
            tar_stream.seek(data_offset)
            long_name = tar_stream.read(member.size)
            if not long_name.endswith(tarfile.NUL):
                raise ScanError(f"{label} TAR 长路径元数据无效")
            scan_sensitive_content(long_name, f"{label}!TAR 长路径")
        padding_size = padded_size - member.size
        if padding_size:
            tar_stream.seek(data_offset + member.size)
            if tar_stream.read(padding_size).strip(tarfile.NUL):
                raise ScanError(f"{label} TAR 成员包含非零填充")
        offset = data_offset + padded_size

    raise ScanError(f"{label} TAR 缺少结束块")


def scan_tar(path, kind, version):
    label = path.name
    files, directories, seen, seen_casefold = set(), set(), set(), set()
    total_size = 0
    with tempfile.TemporaryFile() as tar_stream:
        decompress_gzip(path, tar_stream)
        validate_tar_layout(tar_stream, label)
        with tarfile.open(fileobj=tar_stream, mode="r:") as archive:
            if archive.pax_headers:
                raise ScanError(f"{label} 包含 TAR PAX 元数据")
            for member_count, member in enumerate(archive, start=1):
                if member_count > MAX_MEMBER_COUNT:
                    raise ScanError(f"{label} 成员数量超过限制")
                member_label = f"{label}!归档成员"
                if member.pax_headers:
                    raise ScanError(f"{member_label} 包含 TAR PAX 元数据")
                if not (member.isfile() or member.isdir()):
                    raise ScanError(f"{member_label} 不是普通文件或目录")
                if (
                    member.uid != 0
                    or member.gid != 0
                    or member.uname
                    or member.gname
                    or member.mtime != 0
                    or member.linkname
                    or member.sparse
                ):
                    raise ScanError(f"{member_label} 包含非规范 TAR 元数据")
                if member.mode & 0o6000:
                    raise ScanError(f"{member_label} 带有 setuid/setgid 权限")
                normalized = register_member(
                    member.name,
                    member.isdir(),
                    member_label,
                    seen,
                    seen_casefold,
                    files,
                    directories,
                    kind,
                    version,
                )
                member_label = f"{label}!{normalized}"
                if (
                    kind == "linux-amd64"
                    and normalized.endswith(
                        ("/coinsphere-server", "/coinsphere-migrate")
                    )
                    and not member.mode & 0o111
                ):
                    raise ScanError(f"{member_label} 缺少执行权限")
                if (
                    kind == "docker"
                    and normalized.endswith("/deploy.sh")
                    and not member.mode & 0o111
                ):
                    raise ScanError(f"{member_label} 缺少执行权限")
                if member.isfile():
                    total_size += member.size
                    if total_size > MAX_ARCHIVE_SIZE:
                        raise ScanError(f"{label} 解压后总大小超过限制")
                    reader = archive.extractfile(member)
                    if reader is None:
                        raise ScanError(f"{member_label} 无法读取")
                    with reader:
                        scan_member(reader, member.size, f"{label}!{normalized}")
    check_archive_inventory(kind, files, directories, version)


def calculate_sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_checksums(output_dir, payload_names):
    checksum_path = output_dir / "SHA256SUMS"
    try:
        lines = (
            read_limited(checksum_path, MAX_CHECKSUM_SIZE, "SHA256SUMS")
            .decode("ascii")
            .splitlines()
        )
    except UnicodeDecodeError as error:
        raise ScanError("SHA256SUMS 必须是 ASCII 文本") from error
    if len(lines) != len(payload_names):
        raise ScanError("SHA256SUMS 条目数量不匹配")
    entries = {}
    for line in lines:
        match = CHECKSUM_RE.fullmatch(line)
        if not match:
            raise ScanError("SHA256SUMS 包含非规范条目")
        digest, name = match.groups()
        if name in entries:
            raise ScanError(f"SHA256SUMS 包含重复条目: {name}")
        entries[name] = digest
    if set(entries) != set(payload_names):
        raise ScanError("SHA256SUMS 未精确覆盖最终载荷")
    for name, expected_digest in entries.items():
        if calculate_sha256(output_dir / name) != expected_digest:
            raise ScanError(f"SHA256SUMS 校验失败: {name}")


def inspect_image(reference):
    docker = shutil.which("docker")
    if not docker:
        raise ScanError(f"无法检查本地镜像: {reference}")
    command = [docker, "image", "inspect", reference]
    if os.name == "nt" and Path(docker).suffix.casefold() in {".bat", ".cmd"}:
        command = [os.environ.get("COMSPEC", "cmd.exe"), "/d", "/c", *command]
    try:
        result = subprocess.run(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            check=False,
            timeout=30,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired) as error:
        raise ScanError(f"无法检查本地镜像: {reference}") from error
    if result.returncode != 0:
        raise ScanError(f"本地镜像检查失败: {reference}")
    metadata = strict_json(result.stdout, f"镜像元数据 {reference}")
    if (
        not isinstance(metadata, list)
        or len(metadata) != 1
        or not isinstance(metadata[0], dict)
    ):
        raise ScanError(f"本地镜像元数据结构无效: {reference}")
    return metadata[0]


def inspect_remote_digest(reference):
    docker = shutil.which("docker")
    if not docker:
        raise ScanError(f"无法检查远端镜像: {reference}")
    command = [
        docker,
        "buildx",
        "imagetools",
        "inspect",
        reference,
        "--format",
        "{{json .Manifest.Digest}}",
    ]
    if os.name == "nt" and Path(docker).suffix.casefold() in {".bat", ".cmd"}:
        command = [os.environ.get("COMSPEC", "cmd.exe"), "/d", "/c", *command]
    try:
        result = subprocess.run(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            check=False,
            timeout=30,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired) as error:
        raise ScanError(f"无法检查远端镜像: {reference}") from error
    if result.returncode != 0:
        raise ScanError(f"远端镜像检查失败: {reference}")
    digest = strict_json(result.stdout, f"远端镜像元数据 {reference}")
    if not isinstance(digest, str) or not DIGEST_RE.fullmatch(digest):
        raise ScanError(f"远端镜像 digest 无效: {reference}")
    return digest


def verify_image(image, expected_digest, version, commit):
    repository = image.rsplit(":", 1)[0]
    aliases = (image, f"{repository}:sha-{commit[:12]}")
    digest = expected_digest[len(repository) + 1 :]
    metadata = [inspect_image(reference) for reference in aliases]
    if not metadata[0].get("Id") or metadata[0].get("Id") != metadata[1].get("Id"):
        raise ScanError(f"版本标签与 Commit 标签未指向同一本地镜像: {repository}")
    for reference, item in zip(aliases, metadata):
        if expected_digest not in item.get("RepoDigests", []):
            raise ScanError(f"镜像 RepoDigest 与 Manifest 不匹配: {reference}")
        labels = item.get("Config", {}).get("Labels") or {}
        if labels.get("org.opencontainers.image.version") != version:
            raise ScanError(f"镜像版本标签不匹配: {reference}")
        if labels.get("org.opencontainers.image.revision") != commit:
            raise ScanError(f"镜像 Commit 标签不匹配: {reference}")
        if inspect_remote_digest(reference) != digest:
            raise ScanError(f"远端镜像 digest 与 Manifest 不匹配: {reference}")


def verify_manifest(path, version, commit, registry):
    raw = read_limited(path, MAX_MANIFEST_SIZE, "release-manifest.json")
    scan_sensitive_content(raw, "release-manifest.json")
    manifest = strict_json(raw, "release-manifest.json")
    expected_keys = {
        "version",
        "commit",
        "backendImage",
        "backendDigest",
    }
    if not isinstance(manifest, dict) or set(manifest) != expected_keys:
        raise ScanError("release-manifest.json 字段清单不匹配")
    if any(not isinstance(value, str) or not value for value in manifest.values()):
        raise ScanError("release-manifest.json 字段必须是非空字符串")
    if manifest["version"] != version or manifest["commit"] != commit:
        raise ScanError("release-manifest.json 版本或 Commit 不匹配")
    expected_image = f"{registry}/coinsphere/backend:{version}"
    repository = expected_image.rsplit(":", 1)[0]
    if manifest["backendImage"] != expected_image:
        raise ScanError("release-manifest.json 的 backendImage 不匹配")
    digest_prefix = f"{repository}@"
    digest = manifest["backendDigest"]
    if not digest.startswith(digest_prefix) or not DIGEST_RE.fullmatch(
        digest[len(digest_prefix) :]
    ):
        raise ScanError("release-manifest.json 的 backendDigest 无效")
    verify_image(expected_image, digest, version, commit)
    return manifest


def verify_sbom(path, component, manifest):
    raw = read_limited(path, MAX_JSON_SIZE, path.name)
    scan_sensitive_content(raw, path.name)
    sbom = strict_json(raw, path.name)
    if not isinstance(sbom, dict):
        raise ScanError(f"{path.name} 根节点必须是 JSON 对象")
    creation = sbom.get("creationInfo")
    packages = sbom.get("packages")
    relationships = sbom.get("relationships")
    package_by_id = {
        package.get("SPDXID"): package
        for package in packages or []
        if isinstance(package, dict) and isinstance(package.get("SPDXID"), str)
    }
    expected_repository, expected_digest = manifest[f"{component}Digest"].split(
        "@sha256:", 1
    )
    described_ids = [
        relationship.get("relatedSpdxElement")
        for relationship in relationships or []
        if isinstance(relationship, dict)
        and relationship.get("spdxElementId") == "SPDXRef-DOCUMENT"
        and relationship.get("relationshipType") == "DESCRIBES"
    ]
    described_package = (
        package_by_id.get(described_ids[0]) if len(described_ids) == 1 else None
    )
    if (
        sbom.get("spdxVersion") != "SPDX-2.3"
        or sbom.get("SPDXID") != "SPDXRef-DOCUMENT"
        or sbom.get("dataLicense") != "CC0-1.0"
        or not isinstance(sbom.get("documentNamespace"), str)
        or not sbom.get("documentNamespace")
        or sbom.get("name") != expected_repository
        or not isinstance(creation, dict)
        or not isinstance(creation.get("created"), str)
        or not creation.get("created")
        or not isinstance(creation.get("creators"), list)
        or not creation.get("creators")
        or any(
            not isinstance(creator, str) or not creator
            for creator in creation.get("creators", [])
        )
        or not isinstance(packages, list)
        or not packages
        or any(
            not isinstance(package, dict)
            or not isinstance(package.get("SPDXID"), str)
            or not package.get("SPDXID", "").startswith("SPDXRef-")
            or package.get("SPDXID") == "SPDXRef-DOCUMENT"
            or not isinstance(package.get("name"), str)
            or not package.get("name")
            for package in packages
        )
        or len(package_by_id) != len(packages)
        or not isinstance(relationships, list)
        or not isinstance(described_package, dict)
        or described_package.get("name") != expected_repository
        or described_package.get("versionInfo") != f"sha256:{expected_digest}"
        or described_package.get("primaryPackagePurpose") != "CONTAINER"
        or not any(
            isinstance(checksum, dict)
            and checksum.get("algorithm") == "SHA256"
            and checksum.get("checksumValue") == expected_digest
            for checksum in described_package.get("checksums", [])
        )
    ):
        raise ScanError(f"{path.name} 不满足 SPDX JSON 组件契约")


def verify_inventory(output_dir, expected_names):
    if not output_dir.is_dir() or output_dir.is_symlink():
        raise ScanError(f"产物目录无效: {output_dir}")
    entries = list(os.scandir(output_dir))
    actual_names = {entry.name for entry in entries}
    if actual_names != set(expected_names):
        raise ScanError("最终文件清单存在缺失或额外项")
    if any(not entry.is_file(follow_symlinks=False) for entry in entries):
        raise ScanError("最终文件清单只能包含普通文件")


def main():
    args = parse_args()
    if not VERSION_RE.fullmatch(args.version):
        raise ScanError("版本号必须符合 vX.Y.Z 格式")
    if not COMMIT_RE.fullmatch(args.commit):
        raise ScanError("Commit SHA 必须是 40 位小写十六进制")
    registry = os.environ.get("COINSPHERE_REGISTRY", "127.0.0.1:5000").rstrip("/")
    if not re.fullmatch(r"[0-9A-Za-z.-]+(?::[0-9]{1,5})?", registry):
        raise ScanError("COINSPHERE_REGISTRY 格式无效")

    output_dir = Path(os.path.abspath(args.output_dir))
    resolved_output_dir = output_dir.resolve()
    if os.path.normcase(str(output_dir)) != os.path.normcase(str(resolved_output_dir)):
        raise ScanError(f"产物目录无效: {output_dir}")
    output_dir = resolved_output_dir
    version = args.version
    payload_names = [
        f"coinsphere-{version}-windows-x86.zip",
        f"coinsphere-{version}-linux-amd64.tar.gz",
        f"coinsphere-{version}-docker.tar.gz",
        "release-manifest.json",
        f"coinsphere-{version}-backend.spdx.json",
    ]
    expected_names = [*payload_names, "SHA256SUMS"]
    verify_inventory(output_dir, expected_names)
    verify_checksums(output_dir, payload_names)
    manifest = verify_manifest(
        output_dir / "release-manifest.json", version, args.commit, registry
    )
    verify_sbom(
        output_dir / f"coinsphere-{version}-backend.spdx.json", "backend", manifest
    )
    scan_zip(
        output_dir / f"coinsphere-{version}-windows-x86.zip", "windows-x86", version
    )
    scan_tar(
        output_dir / f"coinsphere-{version}-linux-amd64.tar.gz", "linux-amd64", version
    )
    scan_tar(output_dir / f"coinsphere-{version}-docker.tar.gz", "docker", version)
    print("最终发布产物安全与完整性扫描通过")


if __name__ == "__main__":
    try:
        main()
    except ScanError as error:
        print(f"发布产物扫描失败: {error}", file=sys.stderr)
        sys.exit(1)
    except Exception as error:  # noqa: BLE001 - 顶层必须 fail-closed 且不回显异常内容。
        print(f"发布产物扫描失败: {type(error).__name__}", file=sys.stderr)
        sys.exit(1)

"""Deterministic JSONL.gz files and content-addressed manifests."""

from __future__ import annotations

import gzip
import hashlib
import json
import os
import tempfile
from collections.abc import Iterable, Mapping, Sequence
from contextlib import suppress
from dataclasses import dataclass
from datetime import UTC, datetime
from decimal import Decimal
from pathlib import Path, PurePosixPath
from types import MappingProxyType
from typing import Final


class ArtifactError(ValueError):
    """An artifact cannot be represented by the deterministic contract."""


def decimal_text(value: Decimal) -> str:
    if not isinstance(value, Decimal) or not value.is_finite():
        raise ArtifactError("artifact Decimal values must be finite")
    text = format(value, "f")
    if "." in text:
        text = text.rstrip("0").rstrip(".")
    return "0" if text in {"", "-0"} else text


def utc_text(value: object) -> str:
    if (
        not isinstance(value, datetime)
        or value.tzinfo is None
        or value.utcoffset() != UTC.utcoffset(value)
    ):
        raise ArtifactError("artifact timestamps must be timezone-aware UTC")
    value = value.astimezone(UTC)
    return (
        f"{value.year:04d}-{value.month:02d}-{value.day:02d}"
        f"T{value.hour:02d}:{value.minute:02d}:{value.second:02d}"
        f".{value.microsecond * 1000:09d}Z"
    )


def _json_value(value: object) -> object:
    if isinstance(value, Decimal):
        return decimal_text(value)
    if isinstance(value, datetime):
        return utc_text(value)
    if isinstance(value, Mapping):
        if any(not isinstance(key, str) for key in value):
            raise ArtifactError("artifact object keys must be strings")
        return {
            key: _json_value(item) for key, item in sorted(value.items())
        }
    if isinstance(value, (list, tuple)):
        return [_json_value(item) for item in value]
    if isinstance(value, (str, int, bool)) or value is None:
        return value
    if hasattr(value, "to_record"):
        return _json_value(value.to_record())
    if hasattr(value, "__dict__"):
        return _json_value(vars(value))
    raise ArtifactError(f"unsupported artifact value: {type(value).__name__}")


def canonical_json(value: object) -> str:
    return json.dumps(
        _json_value(value),
        ensure_ascii=True,
        separators=(",", ":"),
        sort_keys=True,
        allow_nan=False,
    )


def jsonl_bytes(records: Iterable[object]) -> bytes:
    lines = [canonical_json(record).encode("utf-8") + b"\n" for record in records]
    return b"".join(lines)


def jsonl_gzip_bytes(records: Iterable[object]) -> bytes:
    payload = jsonl_bytes(records)
    with tempfile.SpooledTemporaryFile(max_size=max(len(payload) * 2, 1024)) as output:
        with gzip.GzipFile(
            fileobj=output, mode="wb", filename="", mtime=0, compresslevel=9
        ) as stream:
            stream.write(payload)
        output.seek(0)
        return output.read()


def write_jsonl_gz(path: str | Path, records: Iterable[object]) -> ArtifactFile:
    destination = Path(path)
    return _write_bytes(destination, jsonl_gzip_bytes(records))


def _write_bytes(destination: Path, data: bytes, *, root: Path | None = None) -> ArtifactFile:
    destination.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{destination.name}.", dir=destination.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, destination)
    except BaseException:
        with suppress(FileNotFoundError):
            os.unlink(temporary)
        raise
    return artifact_file(destination, root=root)


@dataclass(frozen=True, slots=True)
class ArtifactFile:
    path: str
    sha256: str
    size: int

    def __post_init__(self) -> None:
        if not isinstance(self.path, str):
            raise ArtifactError("artifact path must be a relative POSIX path")
        path = PurePosixPath(self.path)
        if (
            not self.path
            or not path.parts
            or path.is_absolute()
            or ".." in path.parts
            or "\\" in self.path
            or str(path) != self.path
            or path.parts[0].endswith(":")
            or any(ord(char) < 32 or ord(char) == 127 for char in self.path)
            or (len(path.parts[0]) >= 2 and path.parts[0][1] == ":")
        ):
            raise ArtifactError("artifact path must be a relative POSIX path")
        if (
            not isinstance(self.sha256, str)
            or len(self.sha256) != 64
            or any(char not in "0123456789abcdef" for char in self.sha256)
        ):
            raise ArtifactError("artifact sha256 must be lowercase hexadecimal")
        if isinstance(self.size, bool) or not isinstance(self.size, int) or self.size < 0:
            raise ArtifactError("artifact size must be a non-negative integer")

    def to_record(self) -> dict[str, str | int]:
        return {"path": self.path, "sha256": self.sha256, "size": self.size}


def artifact_file(path: str | Path, *, root: str | Path | None = None) -> ArtifactFile:
    file_path = Path(path)
    data = file_path.read_bytes()
    relative = file_path.name if root is None else file_path.relative_to(Path(root)).as_posix()
    return ArtifactFile(relative, hashlib.sha256(data).hexdigest(), len(data))


@dataclass(frozen=True, slots=True)
class Manifest:
    files: tuple[ArtifactFile, ...]
    schema_version: int = 1
    references: Mapping[str, str] | None = None

    def __post_init__(self) -> None:
        if self.schema_version != 1:
            raise ArtifactError("unsupported manifest schema version")
        try:
            files = tuple(self.files)
        except TypeError as exc:
            raise ArtifactError("manifest files must be ArtifactFile entries") from exc
        if any(not isinstance(item, ArtifactFile) for item in files):
            raise ArtifactError("manifest files must be ArtifactFile entries")
        if len({item.path for item in files}) != len(files):
            raise ArtifactError("manifest paths must be unique")
        if self.references is not None and not isinstance(self.references, Mapping):
            raise ArtifactError("manifest references must be a mapping")
        references = dict(self.references or {})
        if any(
            not isinstance(key, str)
            or not key
            or not isinstance(value, str)
            or not value
            for key, value in references.items()
        ):
            raise ArtifactError("manifest references must use non-empty strings")
        if any(value not in {item.path for item in files} for value in references.values()):
            raise ArtifactError("manifest references must target declared files")
        object.__setattr__(self, "files", files)
        object.__setattr__(self, "references", MappingProxyType(references))

    def to_record(self) -> dict[str, object]:
        return {
            "files": [item.to_record() for item in sorted(self.files, key=lambda item: item.path)],
            "references": dict(self.references or {}),
            "schemaVersion": self.schema_version,
        }

    @property
    def sha256(self) -> str:
        return hashlib.sha256(canonical_json(self.to_record()).encode("utf-8")).hexdigest()


def build_manifest(
    files: Sequence[str | Path | ArtifactFile],
    *,
    root: str | Path | None = None,
    references: Mapping[str, str] | None = None,
) -> Manifest:
    entries = tuple(
        item if isinstance(item, ArtifactFile) else artifact_file(item, root=root) for item in files
    )
    return Manifest(tuple(sorted(entries, key=lambda item: item.path)), references=references)


def write_manifest(path: str | Path, manifest: Manifest) -> ArtifactFile:
    destination = Path(path)
    data = (canonical_json(manifest.to_record()) + "\n").encode("utf-8")
    return _write_bytes(destination, data)


def freeze_records(
    directory: str | Path,
    *,
    input_records: Iterable[object],
    result_records: Iterable[object],
    references: Mapping[str, str] | None = None,
) -> Manifest:
    """Write input/result files and the manifest using atomic local moves."""

    root = Path(directory)
    object_root = root / "objects"
    input_data = jsonl_gzip_bytes(input_records)
    result_data = jsonl_gzip_bytes(result_records)
    input_hash = hashlib.sha256(input_data).hexdigest()
    result_hash = hashlib.sha256(result_data).hexdigest()
    input_path = f"objects/{input_hash}.jsonl.gz"
    result_path = f"objects/{result_hash}.jsonl.gz"
    manifest_references = {"input": input_path, "result": result_path}
    if references is not None and not isinstance(references, Mapping):
        raise ArtifactError("manifest references must be a mapping")
    for key, value in (references or {}).items():
        if not isinstance(key, str) or not isinstance(value, str) or not key or not value:
            raise ArtifactError("manifest references must use non-empty strings")
        if key in manifest_references:
            raise ArtifactError(f"manifest reference is reserved: {key}")
        if value not in {input_path, result_path}:
            raise ArtifactError("manifest references must target declared files")
        manifest_references[key] = value
    input_file = _write_bytes(object_root / f"{input_hash}.jsonl.gz", input_data, root=root)
    result_file = _write_bytes(object_root / f"{result_hash}.jsonl.gz", result_data, root=root)
    files = {item.path: item for item in (input_file, result_file)}
    manifest = build_manifest(tuple(files.values()), references=manifest_references)
    write_manifest(root / "manifest.json", manifest)
    return manifest


__all__: Final = [
    "ArtifactError",
    "ArtifactFile",
    "Manifest",
    "artifact_file",
    "build_manifest",
    "canonical_json",
    "decimal_text",
    "freeze_records",
    "jsonl_bytes",
    "jsonl_gzip_bytes",
    "utc_text",
    "write_jsonl_gz",
    "write_manifest",
]

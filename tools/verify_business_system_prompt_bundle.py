#!/usr/bin/env python3
"""Verify a content-addressed bundle manifest, archive, and file set."""

from __future__ import annotations

import hashlib
import json
import re
import stat
import unicodedata
import zipfile
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Any, Iterable, Optional

MANIFEST_NAME = "bundle-manifest.json"
_DIGEST_RE = re.compile(r"[0-9a-f]{64}")
_WINDOWS_INVALID_CHARS = frozenset('<>:"|?*')
_WINDOWS_RESERVED_NAMES = {
    "CON",
    "PRN",
    "AUX",
    "NUL",
    *(f"COM{number}" for number in range(1, 10)),
    *(f"LPT{number}" for number in range(1, 10)),
}


class VerificationError(ValueError):
    """Raised when a bundle violates the pinned release contract."""


@dataclass(frozen=True)
class VerificationResult:
    bundle_id: str
    zip_sha256: str
    manifest_sha256: str
    file_count: int
    entry_count: int


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _require_digest(value: str, label: str) -> str:
    normalized = value.strip().lower()
    if not _DIGEST_RE.fullmatch(normalized):
        raise VerificationError(f"{label} must be a lowercase SHA-256 digest")
    return normalized


def _safe_archive_name(name: str) -> str:
    if not name or "\x00" in name:
        raise VerificationError("unsafe empty or NUL archive path")
    if "\\" in name or name.startswith("/") or name.endswith("/"):
        raise VerificationError(f"unsafe archive path: {name!r}")
    if unicodedata.normalize("NFC", name) != name:
        raise VerificationError(f"unsafe non-NFC archive path: {name!r}")

    pure = PurePosixPath(name)
    parts = name.split("/")
    if pure.is_absolute() or not parts or any(part in {"", ".", ".."} for part in parts):
        raise VerificationError(f"unsafe archive path: {name!r}")
    for part in parts:
        if any(char in _WINDOWS_INVALID_CHARS for char in part) or part.endswith((" ", ".")):
            raise VerificationError(f"unsafe cross-platform archive path: {name!r}")
        stem = part.split(".", 1)[0].upper()
        if stem in _WINDOWS_RESERVED_NAMES:
            raise VerificationError(f"unsafe reserved archive path: {name!r}")
    return unicodedata.normalize("NFC", name).casefold()


def _check_unique_names(names: Iterable[str], label: str) -> set[str]:
    exact: set[str] = set()
    portable: dict[str, str] = {}
    for name in names:
        key = _safe_archive_name(name)
        if name in exact:
            raise VerificationError(f"duplicate {label} entry: {name!r}")
        previous = portable.get(key)
        if previous is not None:
            raise VerificationError(f"duplicate cross-platform {label} entries: {previous!r}, {name!r}")
        exact.add(name)
        portable[key] = name
    return exact


def _verify_checksum_file(checksum_path: Path, zip_path: Path, expected_zip_sha256: str) -> None:
    try:
        raw = checksum_path.read_bytes()
        text = raw.decode("ascii")
    except (OSError, UnicodeDecodeError) as exc:
        raise VerificationError(f"checksum file cannot be read as ASCII: {exc}") from exc
    lines = text.splitlines()
    if len(lines) != 1 or not lines[0]:
        raise VerificationError("checksum file must contain exactly one non-empty line")
    match = re.fullmatch(r"([0-9A-Fa-f]{64})[ \t]+\*?(.+)", lines[0])
    if match is None:
        raise VerificationError("checksum file has an invalid format")
    checksum_digest = match.group(1).lower()
    checksum_name = match.group(2)
    if checksum_digest != expected_zip_sha256 or checksum_name != zip_path.name:
        raise VerificationError("checksum file does not match the pinned archive name and SHA-256")


def _load_manifest(manifest_bytes: bytes) -> dict[str, Any]:
    try:
        decoded = manifest_bytes.decode("utf-8")
        manifest = json.loads(decoded)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise VerificationError(f"manifest is not valid UTF-8 JSON: {exc}") from exc
    if not isinstance(manifest, dict):
        raise VerificationError("manifest root must be an object")
    return manifest


def _manifest_files(manifest: dict[str, Any], expected_count: int) -> dict[str, dict[str, Any]]:
    raw_files = manifest.get("files")
    if not isinstance(raw_files, list) or len(raw_files) != expected_count:
        actual = len(raw_files) if isinstance(raw_files, list) else "non-list"
        raise VerificationError(f"manifest file count mismatch: expected {expected_count}, got {actual}")

    paths: list[str] = []
    files: dict[str, dict[str, Any]] = {}
    for index, entry in enumerate(raw_files):
        if not isinstance(entry, dict):
            raise VerificationError(f"manifest file entry {index} must be an object")
        path = entry.get("path")
        digest = entry.get("sha256")
        byte_length = entry.get("byte_length")
        if not isinstance(path, str):
            raise VerificationError(f"manifest file entry {index} has no string path")
        if not isinstance(digest, str) or not _DIGEST_RE.fullmatch(digest.lower()):
            raise VerificationError(f"manifest file {path!r} has an invalid SHA-256")
        if isinstance(byte_length, bool) or not isinstance(byte_length, int) or byte_length < 0:
            raise VerificationError(f"manifest file {path!r} has an invalid byte length")
        paths.append(path)
        files[path] = entry

    _check_unique_names(paths, "manifest")
    if MANIFEST_NAME in files:
        raise VerificationError(f"manifest files must not include {MANIFEST_NAME!r}")
    return files


def _verify_zip_info(info: zipfile.ZipInfo) -> None:
    if info.is_dir():
        raise VerificationError(f"directory entries are not allowed: {info.filename!r}")
    if info.flag_bits & 0x1:
        raise VerificationError(f"encrypted entries are not allowed: {info.filename!r}")
    unix_mode = (info.external_attr >> 16) & 0xFFFF
    file_type = stat.S_IFMT(unix_mode)
    if file_type == stat.S_IFLNK:
        raise VerificationError(f"symlink entries are not allowed: {info.filename!r}")
    if file_type not in {0, stat.S_IFREG}:
        raise VerificationError(f"special file entries are not allowed: {info.filename!r}")


def _summarize_names(names: set[str]) -> str:
    values = sorted(names)
    preview = values[:5]
    suffix = "" if len(values) <= 5 else f" (+{len(values) - 5} more)"
    return ", ".join(repr(value) for value in preview) + suffix


def verify_bundle(
    *,
    zip_path: Path,
    manifest_path: Path,
    expected_zip_sha256: str,
    expected_manifest_sha256: str,
    expected_manifest_file_count: int,
    expected_zip_entry_count: int,
    expected_bundle_id: Optional[str] = None,
    checksum_path: Optional[Path] = None,
) -> VerificationResult:
    zip_path = Path(zip_path)
    manifest_path = Path(manifest_path)
    expected_zip_sha256 = _require_digest(expected_zip_sha256, "expected ZIP SHA-256")
    expected_manifest_sha256 = _require_digest(expected_manifest_sha256, "expected manifest SHA-256")
    if expected_manifest_file_count < 0 or expected_zip_entry_count < 1:
        raise VerificationError("expected entry counts must be positive")

    try:
        actual_zip_sha256 = _sha256_file(zip_path)
        manifest_bytes = manifest_path.read_bytes()
    except OSError as exc:
        raise VerificationError(f"bundle input cannot be read: {exc}") from exc
    if actual_zip_sha256 != expected_zip_sha256:
        raise VerificationError(
            f"ZIP SHA-256 mismatch: expected {expected_zip_sha256}, got {actual_zip_sha256}"
        )
    actual_manifest_sha256 = hashlib.sha256(manifest_bytes).hexdigest()
    if actual_manifest_sha256 != expected_manifest_sha256:
        raise VerificationError(
            f"manifest SHA-256 mismatch: expected {expected_manifest_sha256}, got {actual_manifest_sha256}"
        )
    if checksum_path is not None:
        _verify_checksum_file(Path(checksum_path), zip_path, expected_zip_sha256)

    manifest = _load_manifest(manifest_bytes)
    bundle_id = manifest.get("bundle_id")
    if not isinstance(bundle_id, str) or not bundle_id.strip():
        raise VerificationError("manifest bundle_id must be a non-empty string")
    if expected_bundle_id is not None and bundle_id != expected_bundle_id:
        raise VerificationError(
            f"manifest bundle_id mismatch: expected {expected_bundle_id!r}, got {bundle_id!r}"
        )
    files = _manifest_files(manifest, expected_manifest_file_count)

    try:
        with zipfile.ZipFile(zip_path, "r") as archive:
            infos = archive.infolist()
            if len(infos) != expected_zip_entry_count:
                raise VerificationError(
                    f"ZIP entry count mismatch: expected {expected_zip_entry_count}, got {len(infos)}"
                )
            archive_names = _check_unique_names((info.filename for info in infos), "ZIP")
            for info in infos:
                _verify_zip_info(info)

            expected_names = set(files)
            expected_names.add(MANIFEST_NAME)
            missing = expected_names - archive_names
            extra = archive_names - expected_names
            if missing or extra:
                details: list[str] = []
                if missing:
                    details.append(f"missing={_summarize_names(missing)}")
                if extra:
                    details.append(f"extra={_summarize_names(extra)}")
                raise VerificationError("ZIP entry set does not match manifest: " + "; ".join(details))

            embedded_manifest = archive.read(MANIFEST_NAME)
            if embedded_manifest != manifest_bytes:
                embedded_digest = hashlib.sha256(embedded_manifest).hexdigest()
                raise VerificationError(
                    f"embedded manifest mismatch: expected {expected_manifest_sha256}, got {embedded_digest}"
                )

            info_by_name = {info.filename: info for info in infos}
            for name in sorted(files):
                entry = files[name]
                expected_length = entry["byte_length"]
                expected_digest = entry["sha256"].lower()
                info = info_by_name[name]
                if info.file_size != expected_length:
                    raise VerificationError(
                        f"entry byte length mismatch for {name!r}: expected {expected_length}, got {info.file_size}"
                    )
                digest = hashlib.sha256()
                actual_length = 0
                with archive.open(info, "r") as handle:
                    for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                        actual_length += len(chunk)
                        digest.update(chunk)
                if actual_length != expected_length:
                    raise VerificationError(
                        f"entry byte length mismatch for {name!r}: expected {expected_length}, got {actual_length}"
                    )
                actual_digest = digest.hexdigest()
                if actual_digest != expected_digest:
                    raise VerificationError(
                        f"entry content SHA-256 mismatch for {name!r}: expected {expected_digest}, got {actual_digest}"
                    )
    except zipfile.BadZipFile as exc:
        raise VerificationError(f"invalid ZIP archive: {exc}") from exc
    except OSError as exc:
        raise VerificationError(f"ZIP archive cannot be read: {exc}") from exc

    return VerificationResult(
        bundle_id=bundle_id,
        zip_sha256=actual_zip_sha256,
        manifest_sha256=actual_manifest_sha256,
        file_count=len(files),
        entry_count=expected_zip_entry_count,
    )

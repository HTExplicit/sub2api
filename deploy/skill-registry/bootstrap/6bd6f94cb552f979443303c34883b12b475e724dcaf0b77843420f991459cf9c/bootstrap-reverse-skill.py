#!/usr/bin/env python3
"""Download and materialize the published CodexRip reverse-skill bundle.

This bootstrap verifies every byte and never executes bundle content.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import re
import shutil
import stat
import sys
import tempfile
import time
import urllib.parse
import urllib.request
import uuid
import zipfile
from pathlib import Path, PurePosixPath
from typing import Any


DESCRIPTOR_URL = "https://codexrip.vip/skills/reverse-skill/current.json"
ALLOWED_HOST = "codexrip.vip"
MANIFEST_NAME = "bundle-manifest.json"
MAX_DESCRIPTOR_BYTES = 256 * 1024
MAX_MANIFEST_BYTES = 4 * 1024 * 1024
MAX_ARCHIVE_BYTES = 128 * 1024 * 1024
MAX_EXTRACTED_BYTES = 256 * 1024 * 1024
MAX_FILE_BYTES = 64 * 1024 * 1024
MAX_FILES = 2000
TASK_MAX_AGE_SECONDS = 7 * 24 * 60 * 60
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
BUNDLE_ID_RE = re.compile(r"^[A-Za-z0-9._-]{1,128}$")
WINDOWS_RESERVED = {
    "CON", "PRN", "AUX", "NUL",
    *(f"COM{i}" for i in range(1, 10)),
    *(f"LPT{i}" for i in range(1, 10)),
}


class BootstrapError(RuntimeError):
    pass


class RestrictedRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req: urllib.request.Request, fp: Any, code: int,
                         msg: str, headers: Any, newurl: str) -> urllib.request.Request:
        _validate_public_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _validate_public_url(value: str) -> str:
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme != "https" or parsed.hostname != ALLOWED_HOST:
        raise BootstrapError("descriptor references an untrusted URL")
    if parsed.username or parsed.password or parsed.port not in (None, 443):
        raise BootstrapError("descriptor URL authority is invalid")
    if parsed.fragment:
        raise BootstrapError("descriptor URL fragment is not allowed")
    return value


def _read_bounded(stream: Any, maximum: int) -> bytes:
    chunks: list[bytes] = []
    total = 0
    while True:
        chunk = stream.read(min(1024 * 1024, maximum - total + 1))
        if not chunk:
            break
        total += len(chunk)
        if total > maximum:
            raise BootstrapError("download exceeds size limit")
        chunks.append(chunk)
    return b"".join(chunks)


def _download(url: str, maximum: int) -> bytes:
    _validate_public_url(url)
    opener = urllib.request.build_opener(RestrictedRedirectHandler())
    request = urllib.request.Request(url, headers={"User-Agent": "CodexRip-Skill-Bootstrap/1"})
    with opener.open(request, timeout=30) as response:
        _validate_public_url(response.geturl())
        content_length = response.headers.get("Content-Length")
        if content_length and int(content_length) > maximum:
            raise BootstrapError("download exceeds size limit")
        return _read_bounded(response, maximum)


def _read_local_asset(root: Path, name: str, maximum: int) -> bytes:
    target = (root / name).resolve()
    if target.parent != root or target.is_symlink() or not target.is_file():
        raise BootstrapError("local contract asset path is invalid")
    data = target.read_bytes()
    if not data or len(data) > maximum:
        raise BootstrapError("local contract asset exceeds size limit")
    return data


def _default_cache_root() -> Path:
    system = platform.system()
    if system == "Windows":
        base = os.environ.get("LOCALAPPDATA") or str(Path.home() / "AppData" / "Local")
        return Path(base) / "CodexRip" / "skills"
    if system == "Darwin":
        return Path.home() / "Library" / "Caches" / "CodexRip" / "skills"
    base = os.environ.get("XDG_CACHE_HOME")
    return (Path(base) if base else Path.home() / ".cache") / "codexrip" / "skills"


def _portable_key(path: str) -> str:
    parts: list[str] = []
    for part in path.split("/"):
        normalized = part.rstrip(" .").casefold()
        stem = normalized.split(".", 1)[0].upper()
        if not normalized or stem in WINDOWS_RESERVED:
            raise BootstrapError(f"non-portable bundle path: {path}")
        parts.append(normalized)
    return "/".join(parts)


def _validate_relative_path(value: Any) -> str:
    if not isinstance(value, str) or not value or "\x00" in value or "\\" in value:
        raise BootstrapError("invalid bundle path")
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in ("", ".", "..") for part in path.parts):
        raise BootstrapError(f"unsafe bundle path: {value}")
    if ":" in path.parts[0]:
        raise BootstrapError(f"unsafe bundle path: {value}")
    _portable_key(value)
    return value


def _parse_descriptor(raw: bytes) -> dict[str, Any]:
    try:
        descriptor = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise BootstrapError("descriptor is not valid UTF-8 JSON") from exc
    required = (
        "schema_version", "bundle_id", "revision", "source_commit",
        "manifest_sha256", "archive_sha256", "manifest_url", "archive_url",
    )
    if not isinstance(descriptor, dict) or any(key not in descriptor for key in required):
        raise BootstrapError("descriptor is incomplete")
    if descriptor["schema_version"] != 1 or not isinstance(descriptor["revision"], int) or descriptor["revision"] < 1:
        raise BootstrapError("descriptor version or revision is invalid")
    if not BUNDLE_ID_RE.fullmatch(str(descriptor["bundle_id"])):
        raise BootstrapError("descriptor bundle id is invalid")
    for key in ("manifest_sha256", "archive_sha256"):
        value = str(descriptor[key]).lower()
        if not SHA256_RE.fullmatch(value):
            raise BootstrapError(f"descriptor {key} is invalid")
        descriptor[key] = value
    if not re.fullmatch(r"[0-9a-f]{40}", str(descriptor["source_commit"]).lower()):
        raise BootstrapError("descriptor source commit is invalid")
    descriptor["manifest_url"] = _validate_public_url(str(descriptor["manifest_url"]))
    descriptor["archive_url"] = _validate_public_url(str(descriptor["archive_url"]))
    return descriptor


def _parse_manifest(raw: bytes, descriptor: dict[str, Any]) -> tuple[dict[str, Any], dict[str, dict[str, Any]]]:
    if len(raw) > MAX_MANIFEST_BYTES or _sha256(raw) != descriptor["manifest_sha256"]:
        raise BootstrapError("manifest digest or size is invalid")
    try:
        manifest = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise BootstrapError("manifest is not valid UTF-8 JSON") from exc
    if not isinstance(manifest, dict) or manifest.get("schema_version") != 1:
        raise BootstrapError("manifest schema is invalid")
    if manifest.get("bundle_id") != descriptor["bundle_id"]:
        raise BootstrapError("manifest bundle id mismatch")
    files = manifest.get("files")
    if not isinstance(files, list) or not 1 <= len(files) <= MAX_FILES:
        raise BootstrapError("manifest file count is invalid")
    by_path: dict[str, dict[str, Any]] = {}
    portable: set[str] = set()
    total = 0
    for item in files:
        if not isinstance(item, dict):
            raise BootstrapError("manifest file entry is invalid")
        path = _validate_relative_path(item.get("path"))
        key = _portable_key(path)
        if path in by_path or key in portable or path == MANIFEST_NAME:
            raise BootstrapError("manifest contains duplicate or conflicting paths")
        digest = str(item.get("sha256", "")).lower()
        length = item.get("byte_length")
        kind = item.get("kind", "text")
        if not SHA256_RE.fullmatch(digest) or not isinstance(length, int) or not 0 <= length <= MAX_FILE_BYTES:
            raise BootstrapError(f"manifest metadata is invalid for {path}")
        if kind not in ("text", "binary", "script"):
            raise BootstrapError(f"manifest kind is invalid for {path}")
        item = dict(item, path=path, sha256=digest, kind=kind)
        by_path[path] = item
        portable.add(key)
        total += length
        if total > MAX_EXTRACTED_BYTES:
            raise BootstrapError("manifest extracted size exceeds limit")
    for key in ("core_files",):
        values = manifest.get(key, [])
        if not isinstance(values, list) or any(_validate_relative_path(value) not in by_path for value in values):
            raise BootstrapError("manifest core files are invalid")
    domains = manifest.get("domains", [])
    if not isinstance(domains, list):
        raise BootstrapError("manifest domains are invalid")
    route_ids: set[str] = set()
    for route in domains:
        if not isinstance(route, dict) or not isinstance(route.get("id"), str) or route["id"] in route_ids:
            raise BootstrapError("manifest route is invalid")
        route_ids.add(route["id"])
        for value in [route.get("entry"), *route.get("references", [])]:
            path = _validate_relative_path(value)
            if path not in by_path:
                raise BootstrapError(f"manifest route references an unknown file: {path}")
    return manifest, by_path


def _zip_entry_is_regular(info: zipfile.ZipInfo) -> bool:
    mode = info.external_attr >> 16
    file_type = stat.S_IFMT(mode)
    return file_type in (0, stat.S_IFREG)


def _verify_and_extract(archive: bytes, manifest_raw: bytes, manifest: dict[str, Any],
                        by_path: dict[str, dict[str, Any]], destination: Path) -> None:
    archive_path = destination.parent / (destination.name + ".zip.tmp")
    archive_path.write_bytes(archive)
    try:
        with zipfile.ZipFile(archive_path) as bundle_zip:
            infos = bundle_zip.infolist()
            expected = {MANIFEST_NAME, *by_path.keys()}
            actual: set[str] = set()
            portable: set[str] = set()
            for info in infos:
                if info.is_dir() or not _zip_entry_is_regular(info):
                    raise BootstrapError("archive contains a link, directory, or special file")
                name = _validate_relative_path(info.filename)
                key = _portable_key(name)
                if name in actual or key in portable:
                    raise BootstrapError("archive contains duplicate or conflicting paths")
                actual.add(name)
                portable.add(key)
                expected_size = len(manifest_raw) if name == MANIFEST_NAME else by_path.get(name, {}).get("byte_length")
                if expected_size is None or info.file_size != expected_size or info.file_size > MAX_FILE_BYTES:
                    raise BootstrapError(f"archive metadata mismatch for {name}")
            if actual != expected:
                raise BootstrapError("archive entry set does not match manifest")
            destination.mkdir(parents=True, exist_ok=False)
            for info in infos:
                name = info.filename
                data = _read_bounded(bundle_zip.open(info), MAX_FILE_BYTES)
                if name == MANIFEST_NAME:
                    if data != manifest_raw:
                        raise BootstrapError("archive manifest bytes mismatch")
                else:
                    entry = by_path[name]
                    if len(data) != entry["byte_length"] or _sha256(data) != entry["sha256"]:
                        raise BootstrapError(f"archive file verification failed for {name}")
                target = destination.joinpath(*PurePosixPath(name).parts)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(data)
            (destination / ".bundle.zip").write_bytes(archive)
    finally:
        archive_path.unlink(missing_ok=True)


def _verify_cached_bundle(root: Path, manifest_raw: bytes, by_path: dict[str, dict[str, Any]], archive_sha: str) -> None:
    if root.is_symlink() or not root.is_dir():
        raise BootstrapError("cached bundle root is invalid")
    if (root / MANIFEST_NAME).read_bytes() != manifest_raw:
        raise BootstrapError("cached manifest mismatch")
    archive_path = root / ".bundle.zip"
    if not archive_path.is_file() or _sha256(archive_path.read_bytes()) != archive_sha:
        raise BootstrapError("cached archive mismatch")
    for name, entry in by_path.items():
        target = root.joinpath(*PurePosixPath(name).parts)
        if target.is_symlink() or not target.is_file():
            raise BootstrapError(f"cached file is unavailable: {name}")
        data = target.read_bytes()
        if len(data) != entry["byte_length"] or _sha256(data) != entry["sha256"]:
            raise BootstrapError(f"cached file verification failed: {name}")


def _install_cache(cache_root: Path, descriptor: dict[str, Any], manifest_raw: bytes,
                   manifest: dict[str, Any], by_path: dict[str, dict[str, Any]], archive: bytes | None) -> tuple[Path, bool]:
    destination = cache_root / "bundles" / descriptor["bundle_id"] / descriptor["manifest_sha256"]
    if destination.exists():
        _verify_cached_bundle(destination, manifest_raw, by_path, descriptor["archive_sha256"])
        return destination, True
    if archive is None or _sha256(archive) != descriptor["archive_sha256"]:
        raise BootstrapError("archive digest is invalid")
    destination.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".install-", dir=destination.parent))
    staging.rmdir()
    try:
        _verify_and_extract(archive, manifest_raw, manifest, by_path, staging)
        try:
            staging.rename(destination)
        except FileExistsError:
            shutil.rmtree(staging, ignore_errors=True)
        _verify_cached_bundle(destination, manifest_raw, by_path, descriptor["archive_sha256"])
        return destination, False
    finally:
        if staging.exists():
            shutil.rmtree(staging, ignore_errors=True)


def _cleanup_tasks(task_root: Path) -> None:
    cutoff = time.time() - TASK_MAX_AGE_SECONDS
    if not task_root.is_dir():
        return
    for child in task_root.iterdir():
        try:
            if child.is_dir() and not child.is_symlink() and child.stat().st_mtime < cutoff:
                shutil.rmtree(child)
        except OSError:
            continue


def _materialize(cache: Path, task_root: Path, task_id: str | None, route_id: str | None,
                 manifest: dict[str, Any], by_path: dict[str, dict[str, Any]]) -> tuple[Path, list[str], list[str]]:
    _cleanup_tasks(task_root)
    task_root.mkdir(parents=True, exist_ok=True)
    safe_task_id = task_id or f"{int(time.time())}-{uuid.uuid4().hex[:12]}"
    if not re.fullmatch(r"[A-Za-z0-9._-]{1,128}", safe_task_id):
        raise BootstrapError("task id is invalid")
    task = task_root / safe_task_id
    task.mkdir(mode=0o700, exist_ok=False)
    selected: list[str] = list(manifest.get("core_files", []))
    if route_id:
        route = next((item for item in manifest.get("domains", []) if item.get("id") == route_id), None)
        if route is None:
            raise BootstrapError("requested route does not exist")
        selected.extend([route["entry"], *route.get("references", [])])
    selected = list(dict.fromkeys(selected))
    copied: list[str] = []
    scripts: list[str] = []
    for name in selected:
        entry = by_path[name]
        if entry["kind"] == "script" and not route_id:
            raise BootstrapError("scripts require an explicit route")
        source = cache.joinpath(*PurePosixPath(name).parts)
        data = source.read_bytes()
        if len(data) != entry["byte_length"] or _sha256(data) != entry["sha256"]:
            raise BootstrapError(f"source verification failed before task copy: {name}")
        target = task / "bundle" / Path(*PurePosixPath(name).parts)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        copied.append(str(target.resolve()))
        if entry["kind"] == "script":
            scripts.append(str(target.resolve()))
    return task.resolve(), copied, scripts


def main() -> int:
    parser = argparse.ArgumentParser(description="Verify and materialize the CodexRip reverse-skill bundle")
    parser.add_argument("--descriptor-url", default=DESCRIPTOR_URL)
    parser.add_argument("--descriptor-file", help="Local descriptor fixture for offline contract tests")
    parser.add_argument("--asset-root", help="Local manifest and ZIP root; valid only with --descriptor-file")
    parser.add_argument("--cache-root")
    parser.add_argument("--task-root")
    parser.add_argument("--task-id")
    parser.add_argument("--route-id")
    args = parser.parse_args()

    try:
        if bool(args.descriptor_file) != bool(args.asset_root):
            raise BootstrapError("--descriptor-file and --asset-root must be used together")
        asset_root = Path(args.asset_root).expanduser().resolve() if args.asset_root else None
        if args.descriptor_file:
            descriptor_raw = Path(args.descriptor_file).read_bytes()
            if len(descriptor_raw) > MAX_DESCRIPTOR_BYTES:
                raise BootstrapError("descriptor exceeds size limit")
        else:
            descriptor_raw = _download(args.descriptor_url, MAX_DESCRIPTOR_BYTES)
        descriptor = _parse_descriptor(descriptor_raw)
        manifest_raw = (_read_local_asset(asset_root, MANIFEST_NAME, MAX_MANIFEST_BYTES)
                        if asset_root else _download(descriptor["manifest_url"], MAX_MANIFEST_BYTES))
        manifest, by_path = _parse_manifest(manifest_raw, descriptor)
        cache_root = Path(args.cache_root).expanduser().resolve() if args.cache_root else _default_cache_root().resolve()
        destination = cache_root / "bundles" / descriptor["bundle_id"] / descriptor["manifest_sha256"]
        archive_name = PurePosixPath(urllib.parse.urlparse(descriptor["archive_url"]).path).name
        archive = None if destination.exists() else (
            _read_local_asset(asset_root, archive_name, MAX_ARCHIVE_BYTES)
            if asset_root else _download(descriptor["archive_url"], MAX_ARCHIVE_BYTES)
        )
        cache, reused = _install_cache(cache_root, descriptor, manifest_raw, manifest, by_path, archive)
        task_root = Path(args.task_root).expanduser().resolve() if args.task_root else cache_root / "tasks"
        task, copied, scripts = _materialize(cache, task_root, args.task_id, args.route_id, manifest, by_path)
        result = {
            "status": "ready",
            "platform": platform.system().lower(),
            "bundle_id": descriptor["bundle_id"],
            "bundle_revision": descriptor["revision"],
            "manifest_sha256": descriptor["manifest_sha256"],
            "source_commit": descriptor["source_commit"],
            "cache_reused": reused,
            "cache_path": str(cache.resolve()),
            "manifest_path": str((cache / MANIFEST_NAME).resolve()),
            "task_path": str(task),
            "materialized_files": copied,
            "materialized_scripts": scripts,
            "scripts_executed": False,
        }
        print(json.dumps(result, ensure_ascii=True, separators=(",", ":")))
        return 0
    except (BootstrapError, OSError, ValueError, zipfile.BadZipFile) as exc:
        print(json.dumps({"status": "skill_unavailable", "error": str(exc)}, ensure_ascii=True, separators=(",", ":")), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Install the published CodexRip bundle as a native user-scoped Codex Skill."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import stat
import tempfile
import urllib.parse
import urllib.request
import uuid
import zipfile
from pathlib import Path, PurePosixPath


DESCRIPTOR_URL = "https://codexrip.vip/skills/reverse-skill/current.json"
BUNDLE_ID = "codexrip-reverse-skill"
SKILL_NAME = "codexrip-reverse-skill"
MANIFEST_NAME = "bundle-manifest.json"
CLIENT_SKILL = "codexrip-client/SKILL.md"
CLIENT_OPENAI = "codexrip-client/agents/openai.yaml"
REQUIRED_CORE = ("RULES.md", "README_AI.md", "skills/SKILL.md")
LEGACY_OVERLAY_PREFIXES = ("codexrip-overlay/security-research", "moxinggang-overlay/security-research")
MAX_DESCRIPTOR_BYTES = 256 << 10
MAX_MANIFEST_BYTES = 4 << 20
MAX_ARCHIVE_BYTES = 128 << 20
MAX_FILE_BYTES = 64 << 20
MAX_TOTAL_BYTES = 256 << 20
MAX_FILE_COUNT = 2000
HEX = frozenset("0123456789abcdef")
WINDOWS_INVALID_CHARS = frozenset('<>:"|?*')
WINDOWS_RESERVED_NAMES = frozenset({"CON", "PRN", "AUX", "NUL", *{f"COM{i}" for i in range(1, 10)}, *{f"LPT{i}" for i in range(1, 10)}})


class BootstrapError(RuntimeError):
    pass


def sha256(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def is_hex(value: object, length: int) -> bool:
    return isinstance(value, str) and len(value) == length and all(char in HEX for char in value)


def safe_relative(value: object) -> str:
    if not isinstance(value, str) or not value or "\\" in value or "\x00" in value:
        raise BootstrapError("manifest path is invalid")
    parsed = PurePosixPath(value)
    if parsed.is_absolute() or any(part in ("", ".", "..") for part in parsed.parts):
        raise BootstrapError("manifest path is invalid")
    if str(parsed) != value:
        raise BootstrapError("manifest path is not canonical")
    for segment in value.split("/"):
        if (
            segment.rstrip(" .") != segment
            or any(ord(char) < 0x20 or char in WINDOWS_INVALID_CHARS for char in segment)
            or segment.split(".", 1)[0].upper() in WINDOWS_RESERVED_NAMES
        ):
            raise BootstrapError("manifest path is not portable")
    return value


def portable_key(value: str) -> str:
    return "/".join(part.rstrip(" .").lower() for part in value.split("/"))


def is_legacy_overlay_path(value: str) -> bool:
    lowered = value.casefold()
    return any(lowered == prefix or lowered.startswith(prefix + "/") for prefix in LEGACY_OVERLAY_PREFIXES)


def require_codexrip_url(value: object, expected_suffix: str, manifest_sha: str) -> str:
    if not isinstance(value, str):
        raise BootstrapError("descriptor URL is missing")
    parsed = urllib.parse.urlparse(value)
    prefix = f"/skills/reverse-skill/versions/{manifest_sha}/"
    if (
        parsed.scheme != "https"
        or parsed.hostname != "codexrip.vip"
        or parsed.username is not None
        or parsed.password is not None
        or parsed.port not in (None, 443)
        or parsed.query
        or parsed.fragment
        or not parsed.path.startswith(prefix)
        or not parsed.path.endswith(expected_suffix)
    ):
        raise BootstrapError("descriptor URL is outside the fixed CodexRip registry")
    return value


def download(url: str, maximum: int) -> bytes:
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme != "https" or parsed.hostname != "codexrip.vip":
        raise BootstrapError("download host is not allowed")
    request = urllib.request.Request(url, headers={"User-Agent": "CodexRip-Skill-Installer/2"})
    try:
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, req, fp, code, msg, headers, newurl):
                return None

        opener = urllib.request.build_opener(NoRedirect())
        with opener.open(request, timeout=45) as response:
            final = urllib.parse.urlparse(response.geturl())
            if final.scheme != "https" or final.hostname != "codexrip.vip":
                raise BootstrapError("download redirect host is not allowed")
            declared = response.headers.get("Content-Length")
            if declared and int(declared) > maximum:
                raise BootstrapError("download exceeds size limit")
            raw = response.read(maximum + 1)
    except BootstrapError:
        raise
    except Exception as exc:
        raise BootstrapError("download failed") from exc
    if not raw or len(raw) > maximum:
        raise BootstrapError("download size is invalid")
    return raw


def read_local(path: Path, maximum: int) -> bytes:
    try:
        resolved = path.resolve(strict=True)
        if not resolved.is_file() or resolved.is_symlink() or resolved.stat().st_size > maximum:
            raise BootstrapError("local contract asset is invalid")
        raw = resolved.read_bytes()
    except BootstrapError:
        raise
    except OSError as exc:
        raise BootstrapError("local contract asset is unavailable") from exc
    if not raw or len(raw) > maximum:
        raise BootstrapError("local contract asset size is invalid")
    return raw


def read_asset(url: str, maximum: int, asset_root: Path | None) -> bytes:
    if asset_root is None:
        return download(url, maximum)
    name = Path(urllib.parse.urlparse(url).path).name
    if not name or name in (".", ".."):
        raise BootstrapError("contract asset name is invalid")
    root = asset_root.resolve(strict=True)
    candidate = (root / name).resolve(strict=True)
    if candidate.parent != root:
        raise BootstrapError("contract asset escaped its root")
    return read_local(candidate, maximum)


def load_json(raw: bytes, label: str) -> dict:
    try:
        value = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise BootstrapError(f"{label} is not valid UTF-8 JSON") from exc
    if not isinstance(value, dict):
        raise BootstrapError(f"{label} root is not an object")
    return value


def validate_descriptor(value: dict) -> dict:
    manifest_sha = value.get("manifest_sha256")
    archive_sha = value.get("archive_sha256")
    source_commit = value.get("source_commit")
    core_files = value.get("core_files")
    if (
        value.get("schema_version") != 1
        or value.get("bundle_id") != BUNDLE_ID
        or not isinstance(value.get("revision"), int)
        or value["revision"] < 1
        or not is_hex(source_commit, 40)
        or not is_hex(manifest_sha, 64)
        or not is_hex(archive_sha, 64)
        or core_files != list(REQUIRED_CORE)
    ):
        raise BootstrapError("descriptor metadata is invalid")
    manifest_url = require_codexrip_url(value.get("manifest_url"), "/bundle-manifest.json", manifest_sha)
    archive_name = f"{BUNDLE_ID}-{manifest_sha}.zip"
    archive_url = require_codexrip_url(value.get("archive_url"), "/" + archive_name, manifest_sha)
    return {
        **value,
        "manifest_sha256": manifest_sha,
        "archive_sha256": archive_sha,
        "source_commit": source_commit,
        "manifest_url": manifest_url,
        "archive_url": archive_url,
    }


def validate_manifest(value: dict, descriptor: dict) -> tuple[dict[str, dict], int]:
    files = value.get("files")
    domains = value.get("domains", [])
    core = value.get("core_files", [])
    if (
        value.get("schema_version") != 1
        or value.get("bundle_id") != BUNDLE_ID
        or not isinstance(files, list)
        or not 1 <= len(files) <= MAX_FILE_COUNT
        or not isinstance(domains, list)
        or not isinstance(core, list)
    ):
        raise BootstrapError("manifest metadata is invalid")
    declared: dict[str, dict] = {}
    portable: set[str] = set()
    total = 0
    for item in files:
        if not isinstance(item, dict):
            raise BootstrapError("manifest file entry is invalid")
        name = safe_relative(item.get("path"))
        if is_legacy_overlay_path(name):
            raise BootstrapError("legacy overlay path is not allowed")
        key = portable_key(name)
        length = item.get("byte_length")
        kind = item.get("kind", "text")
        if (
            name in declared
            or key in portable
            or not is_hex(item.get("sha256"), 64)
            or not isinstance(length, int)
            or length < 0
            or length > MAX_FILE_BYTES
            or kind not in ("text", "script", "binary")
        ):
            raise BootstrapError("manifest file entry is invalid")
        declared[name] = item
        portable.add(key)
        total += length
    if total <= 0 or total > MAX_TOTAL_BYTES:
        raise BootstrapError("manifest total size is invalid")
    if tuple(core) != REQUIRED_CORE:
        raise BootstrapError("manifest core files are invalid")
    for name in REQUIRED_CORE:
        if name not in declared or declared[name].get("kind", "text") != "text":
            raise BootstrapError("manifest core file is invalid")
    if not domains:
        raise BootstrapError("manifest routes are missing")
    route_ids: set[str] = set()
    for domain in domains:
        route_id = domain.get("id") if isinstance(domain, dict) else None
        if not isinstance(route_id, str) or not route_id or route_id in route_ids:
            raise BootstrapError("manifest route is invalid")
        route_ids.add(route_id)
        if safe_relative(domain.get("entry")) not in declared:
            raise BootstrapError("manifest route entry is undeclared")
        refs = domain.get("references", [])
        keywords = domain.get("keywords", [])
        if not isinstance(refs, list) or not isinstance(keywords, list):
            raise BootstrapError("manifest route is invalid")
        if not all(isinstance(keyword, str) for keyword in keywords):
            raise BootstrapError("manifest route is invalid")
        for name in refs:
            if safe_relative(name) not in declared:
                raise BootstrapError("manifest route reference is undeclared")
    for required in (CLIENT_SKILL, CLIENT_OPENAI):
        if required not in declared or declared[required].get("kind", "text") != "text":
            raise BootstrapError("native Codex Skill entry is missing")
    if descriptor["bundle_id"] != value["bundle_id"]:
        raise BootstrapError("manifest bundle identity mismatch")
    return declared, total


def extract_verified_archive(raw: bytes, manifest_raw: bytes, declared: dict[str, dict], bundle_root: Path) -> None:
    # ZipFile accepts an in-memory stream without leaving an unverified archive on disk.
    import io

    seen: set[str] = set()
    portable: set[str] = set()
    expected = {MANIFEST_NAME, *declared}
    try:
        with zipfile.ZipFile(io.BytesIO(raw), "r") as package:
            for info in package.infolist():
                name = safe_relative(info.filename)
                key = portable_key(name)
                mode = info.external_attr >> 16
                if (
                    info.is_dir()
                    or name in seen
                    or key in portable
                    or stat.S_IFMT(mode) not in (0, stat.S_IFREG)
                    or info.file_size > MAX_FILE_BYTES
                ):
                    raise BootstrapError("archive entry is unsafe")
                seen.add(name)
                portable.add(key)
                expected_length = len(manifest_raw) if name == MANIFEST_NAME else declared.get(name, {}).get("byte_length")
                if expected_length is None or info.file_size != expected_length:
                    raise BootstrapError("archive entry length mismatch")
                data = package.read(info)
                if name == MANIFEST_NAME:
                    if data != manifest_raw:
                        raise BootstrapError("archive manifest mismatch")
                elif len(data) != expected_length or sha256(data) != declared[name]["sha256"]:
                    raise BootstrapError("archive file digest mismatch")
                target = bundle_root.joinpath(*PurePosixPath(name).parts)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(data)
                target.chmod(0o644)
    except (zipfile.BadZipFile, RuntimeError, OSError) as exc:
        if isinstance(exc, BootstrapError):
            raise
        raise BootstrapError("archive extraction failed") from exc
    if seen != expected:
        raise BootstrapError("archive entry set mismatch")


def install_skill(descriptor: dict, manifest_raw: bytes, manifest: dict, declared: dict[str, dict], archive_raw: bytes, codex_home: Path) -> tuple[Path, bool]:
    skills_root = codex_home / "skills"
    target = skills_root / SKILL_NAME
    skills_root.mkdir(parents=True, exist_ok=True)
    if target.is_symlink() or (target.exists() and not target.is_dir()):
        raise BootstrapError("native Skill target is not a regular directory")
    staging = Path(tempfile.mkdtemp(prefix=f".{SKILL_NAME}-new-", dir=skills_root))
    backup = skills_root / f".{SKILL_NAME}-old-{uuid.uuid4().hex}"
    replaced = target.exists()
    old_moved = False
    try:
        bundle_root = staging / "bundle"
        bundle_root.mkdir()
        extract_verified_archive(archive_raw, manifest_raw, declared, bundle_root)
        shutil.copyfile(bundle_root / CLIENT_SKILL, staging / "SKILL.md")
        (staging / "agents").mkdir()
        shutil.copyfile(bundle_root / CLIENT_OPENAI, staging / "agents" / "openai.yaml")
        metadata = {
            "schema_version": 1,
            "skill_name": SKILL_NAME,
            "bundle_id": descriptor["bundle_id"],
            "bundle_revision": descriptor["revision"],
            "source_commit": descriptor["source_commit"],
            "manifest_sha256": descriptor["manifest_sha256"],
            "archive_sha256": descriptor["archive_sha256"],
        }
        (staging / ".codexrip-install.json").write_text(
            json.dumps(metadata, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )
        if replaced:
            os.replace(target, backup)
            old_moved = True
        os.replace(staging, target)
        if old_moved:
            # The new verified tree is already committed; cleanup is best effort.
            shutil.rmtree(backup, ignore_errors=True)
        return target.resolve(), replaced
    except Exception:
        if old_moved and not target.exists() and backup.exists():
            os.replace(backup, target)
        raise
    finally:
        if staging.exists():
            shutil.rmtree(staging, ignore_errors=True)
        if backup.exists() and target.exists():
            shutil.rmtree(backup, ignore_errors=True)


def resolve_codex_home(value: str | None) -> Path:
    raw = value or os.environ.get("CODEX_HOME") or str(Path.home() / ".codex")
    if not raw.strip() or "\x00" in raw:
        raise BootstrapError("CODEX_HOME is invalid")
    return Path(raw).expanduser().resolve(strict=False)


def run(args: argparse.Namespace) -> dict:
    asset_root = Path(args.asset_root) if args.asset_root else None
    if args.descriptor_file:
        descriptor_raw = read_local(Path(args.descriptor_file), MAX_DESCRIPTOR_BYTES)
    else:
        if args.descriptor_url != DESCRIPTOR_URL:
            raise BootstrapError("descriptor URL must match the fixed CodexRip endpoint")
        descriptor_raw = download(args.descriptor_url, MAX_DESCRIPTOR_BYTES)
    descriptor = validate_descriptor(load_json(descriptor_raw, "descriptor"))
    manifest_raw = read_asset(descriptor["manifest_url"], MAX_MANIFEST_BYTES, asset_root)
    if sha256(manifest_raw) != descriptor["manifest_sha256"]:
        raise BootstrapError("manifest digest is invalid")
    manifest = load_json(manifest_raw, "manifest")
    declared, _ = validate_manifest(manifest, descriptor)
    archive_raw = read_asset(descriptor["archive_url"], MAX_ARCHIVE_BYTES, asset_root)
    if sha256(archive_raw) != descriptor["archive_sha256"]:
        raise BootstrapError("archive digest is invalid")
    skill_path, replaced = install_skill(
        descriptor,
        manifest_raw,
        manifest,
        declared,
        archive_raw,
        resolve_codex_home(args.codex_home),
    )
    return {
        "status": "ready",
        "skill_name": SKILL_NAME,
        "skill_path": str(skill_path),
        "manifest_path": str(skill_path / "bundle" / MANIFEST_NAME),
        "bundle_revision": descriptor["revision"],
        "source_commit": descriptor["source_commit"],
        "manifest_sha256": descriptor["manifest_sha256"],
        "archive_sha256": descriptor["archive_sha256"],
        "replaced": replaced,
        "scripts_executed": False,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--descriptor-url", default=DESCRIPTOR_URL)
    parser.add_argument("--descriptor-file")
    parser.add_argument("--asset-root")
    parser.add_argument("--codex-home")
    args = parser.parse_args()
    try:
        result = run(args)
    except Exception as exc:
        message = str(exc) if isinstance(exc, BootstrapError) else "unexpected installer failure"
        print(json.dumps({"status": "skill_unavailable", "error": message}, separators=(",", ":")))
        return 1
    print(json.dumps(result, ensure_ascii=False, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

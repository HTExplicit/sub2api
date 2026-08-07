#!/usr/bin/env python3
"""Build the initial CodexRip remote-skill registry seed from the pinned .4 bundle."""

from __future__ import annotations

import hashlib
import json
import stat
import zipfile
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath


ROOT = Path(__file__).resolve().parents[1]
OLD_ID = "moxinggang-reverse-skill"
NEW_ID = "codexrip-reverse-skill"
OLD_MANIFEST_SHA = "22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7"
OLD_ARCHIVE_SHA = "977de70881ef67f15aa804f9cfa3e1a93ba441b46bb4bda1e30c4b4dd07a1c6a"
SOURCE_COMMIT = "d8bf34540cbc1aa34052e1b142576fc36a1f1437"
OLD_ROOT = ROOT / "deploy" / "skill-bundles" / OLD_ID
OLD_MANIFEST = OLD_ROOT / "bundle-manifest.json"
OLD_ARCHIVE = OLD_ROOT / f"{OLD_ID}-{OLD_MANIFEST_SHA}.zip"
OUTPUT = ROOT / "deploy" / "skill-registry" / "seed"
MANIFEST_NAME = "bundle-manifest.json"
OVERLAY_ALLOWLIST = {
    "RULES.md",
    "README_AI.md",
    "SKILL.md",
    "references/precedent-auth.md",
    "references/experience-index.md",
    "references/routing.md",
    "skills/sec-ai-security/INSTRUCTIONS.md",
    "references/ctf/ai-ml/index.md",
    "references/ai-security.md",
    "references/ctf/ai-ml/llm-attacks.md",
    "skills/sec-ai-security/references/llm-deep/prompt-injection-methodology.md",
    "skills/sec-ai-security/references/llm-deep/_llm-security-workflow.md",
    "references/scope-and-evidence.md",
    "references/environment-and-resources.md",
    "skills/sec-ai-security/references/llm-deep/owasp-llm-top10.md",
    "skills/sec-ai-security/references/llm-deep/agent-security-testing.md",
    "skills/sec-ai-security/references/llm-deep/agent-obedience-engineering.md",
}

SCRIPT_EXTENSIONS = {
    ".ps1", ".psm1", ".sh", ".bash", ".zsh", ".fish", ".py", ".rb",
    ".pl", ".lua", ".js", ".mjs", ".cjs", ".ts", ".bat", ".cmd",
}
BINARY_EXTENSIONS = {
    ".png", ".jpg", ".jpeg", ".gif", ".webp", ".jar", ".zip", ".gz",
    ".7z", ".exe", ".dll", ".so", ".pdf", ".docx",
}


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def file_kind(name: str, data: bytes) -> str:
    if data.startswith(b"#!") or PurePosixPath(name).suffix.lower() in SCRIPT_EXTENSIONS:
        return "script"
    if PurePosixPath(name).suffix.lower() in BINARY_EXTENSIONS:
        return "binary"
    try:
        data.decode("utf-8")
    except UnicodeDecodeError:
        return "binary"
    return "text"


def normalize_path(value: str) -> str:
    if value.startswith("moxinggang-overlay/"):
        return "codexrip-overlay/" + value.removeprefix("moxinggang-overlay/")
    return value


def normalize_overlay(data: bytes) -> bytes:
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return data
    text = text.replace(
        "https://moxinggang.com/skills/security-research/current",
        "codexrip-overlay/security-research",
    )
    text = text.replace(
        r"C:\Users\Administrator\AppData\Local\模型港\reverse-skill",
        "codexrip-overlay/security-research",
    )
    text = text.replace("moxinggang-overlay/", "codexrip-overlay/")
    text = text.replace("Moxinggang", "CodexRip")
    text = text.replace("moxinggang", "codexrip")
    text = text.replace("模型港", "CodexRip")
    return text.encode("utf-8")


def validate_relative(value: str) -> None:
    path = PurePosixPath(value)
    if not value or "\\" in value or path.is_absolute() or any(part in ("", ".", "..") for part in path.parts):
        raise ValueError(f"unsafe path: {value}")


def read_old_bundle() -> tuple[dict, dict[str, bytes]]:
    manifest_raw = OLD_MANIFEST.read_bytes()
    archive_raw = OLD_ARCHIVE.read_bytes()
    if sha256(manifest_raw) != OLD_MANIFEST_SHA or sha256(archive_raw) != OLD_ARCHIVE_SHA:
        raise ValueError("pinned .4 bundle fingerprint mismatch")
    manifest = json.loads(manifest_raw.decode("utf-8"))
    declared = {entry["path"]: entry for entry in manifest["files"]}
    files: dict[str, bytes] = {}
    with zipfile.ZipFile(OLD_ARCHIVE) as archive:
        names = {entry.filename for entry in archive.infolist()}
        if names != {MANIFEST_NAME, *declared}:
            raise ValueError("pinned .4 ZIP entry set mismatch")
        for info in archive.infolist():
            if info.filename == MANIFEST_NAME:
                if archive.read(info) != manifest_raw:
                    raise ValueError("pinned .4 embedded manifest mismatch")
                continue
            validate_relative(info.filename)
            mode = info.external_attr >> 16
            if stat.S_IFMT(mode) not in (0, stat.S_IFREG):
                raise ValueError(f"non-regular source entry: {info.filename}")
            data = archive.read(info)
            expected = declared[info.filename]
            if len(data) != expected["byte_length"] or sha256(data) != expected["sha256"]:
                raise ValueError(f"pinned .4 file mismatch: {info.filename}")
            new_path = normalize_path(info.filename)
            if new_path.startswith("codexrip-overlay/"):
                data = normalize_overlay(data)
            if new_path in files:
                raise ValueError(f"normalized path collision: {new_path}")
            files[new_path] = data
    return manifest, files


def overlay_digest(files: dict[str, bytes]) -> tuple[str, dict[str, str]]:
    hashes: dict[str, str] = {}
    for relative in sorted(OVERLAY_ALLOWLIST):
        name = "codexrip-overlay/security-research/" + relative
        if name not in files:
            raise ValueError(f"allowlisted overlay file missing: {name}")
        hashes[name] = sha256(files[name])
    digest = hashlib.sha256()
    for name in sorted(hashes):
        digest.update(name.encode("utf-8"))
        digest.update(b"\0")
        digest.update(hashes[name].encode("ascii"))
        digest.update(b"\n")
    return digest.hexdigest(), hashes


def build_manifest(old: dict, files: dict[str, bytes]) -> bytes:
    entries = []
    for name in sorted(files):
        entries.append({
            "path": name,
            "sha256": sha256(files[name]),
            "byte_length": len(files[name]),
            "kind": file_kind(name, files[name]),
            "required": True,
        })
    domains = []
    for route in old.get("domains", []):
        domains.append({
            "id": route["id"],
            "keywords": route.get("keywords", []),
            "entry": normalize_path(route["entry"]),
            "references": [normalize_path(value) for value in route.get("references", [])],
            **({"priority": route["priority"]} if route.get("priority") else {}),
        })
    manifest = {
        "schema_version": 1,
        "bundle_id": NEW_ID,
        "version": f"2.8.0+{SOURCE_COMMIT}+codexrip.1",
        "core_files": [normalize_path(value) for value in old["core_files"]],
        "files": entries,
        "domains": domains,
    }
    return json.dumps(manifest, ensure_ascii=False, indent=2, separators=(",", ": ")).encode("utf-8")


def build_archive(manifest: bytes, files: dict[str, bytes]) -> bytes:
    output = Path(OUTPUT / ".seed.tmp.zip")
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_STORED) as archive:
        for name, data in [(MANIFEST_NAME, manifest), *((name, files[name]) for name in sorted(files))]:
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_STORED
            info.external_attr = (stat.S_IFREG | 0o644) << 16
            info.create_system = 3
            archive.writestr(info, data, compress_type=zipfile.ZIP_STORED)
    raw = output.read_bytes()
    output.unlink()
    return raw


def main() -> None:
    old, files = read_old_bundle()
    overlay_sha, _ = overlay_digest(files)
    manifest = build_manifest(old, files)
    manifest_sha = sha256(manifest)
    OUTPUT.mkdir(parents=True, exist_ok=True)
    for stale in OUTPUT.glob(f"{NEW_ID}-*.zip*"):
        if stale.is_file():
            stale.unlink()
    archive = build_archive(manifest, files)
    archive_sha = sha256(archive)
    total_bytes = sum(len(data) for data in files.values())
    base_url = f"https://codexrip.vip/skills/reverse-skill/versions/{manifest_sha}"
    descriptor = {
        "schema_version": 1,
        "bundle_id": NEW_ID,
        "revision": 1,
        "source_commit": SOURCE_COMMIT,
        "overlay_sha256": overlay_sha,
        "manifest_sha256": manifest_sha,
        "archive_sha256": archive_sha,
        "manifest_url": f"{base_url}/{MANIFEST_NAME}",
        "archive_url": f"{base_url}/{NEW_ID}-{manifest_sha}.zip",
        "files_base_url": f"{base_url}/",
        "core_files": [normalize_path(value) for value in old["core_files"]],
        "file_count": len(files),
        "total_bytes": total_bytes,
        "published_at": datetime(2026, 8, 7, tzinfo=timezone.utc).isoformat().replace("+00:00", "Z"),
        "bootstrap_policy": "download_verify_cache_materialize_only",
    }
    (OUTPUT / MANIFEST_NAME).write_bytes(manifest)
    archive_name = f"{NEW_ID}-{manifest_sha}.zip"
    (OUTPUT / archive_name).write_bytes(archive)
    (OUTPUT / f"{archive_name}.sha256").write_text(f"{archive_sha}  {archive_name}\n", encoding="ascii")
    (OUTPUT / "seed-descriptor.json").write_text(
        json.dumps(descriptor, ensure_ascii=False, indent=2, separators=(",", ": ")),
        encoding="utf-8",
    )
    print(json.dumps({
        "manifest_sha256": manifest_sha,
        "archive_sha256": archive_sha,
        "overlay_sha256": overlay_sha,
        "file_count": len(files),
        "total_bytes": total_bytes,
    }, separators=(",", ":")))


if __name__ == "__main__":
    main()

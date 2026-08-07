#!/usr/bin/env python3
"""Verify the fixed prompt and content-addressed CodexRip remote-skill release."""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import zipfile
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

from verify_business_system_prompt_bundle import VerificationError, verify_bundle


BUNDLE_ID = "codexrip-reverse-skill"
MANIFEST_NAME = "bundle-manifest.json"
MANIFEST_SHA256 = "510fed48ae78a2580548d27290259bab1848639538af0dd53acaa3f71c855fea"
ARCHIVE_SHA256 = "1b676ba6e12ffa7c4d16b95e94f82a8330a3afa34f664aa98c3ac808927a60bd"
OVERLAY_SHA256 = "1e4145c7c8afad0dc698cf0e5432a25705012752ed676ea51ec9afa303bc6ae3"
SOURCE_COMMIT = "d8bf34540cbc1aa34052e1b142576fc36a1f1437"
PROMPT_SHA256 = "cbf75cc85cd77860e53d06820e7120802d83c069e9d24b48715711acc15893c6"
PROMPT_BYTES = 7045
FILE_COUNT = 538
TOTAL_BYTES = 7_948_026
ARCHIVE_NAME = f"{BUNDLE_ID}-{MANIFEST_SHA256}.zip"
CHECKSUM_NAME = f"{ARCHIVE_NAME}.sha256"
DESCRIPTOR_NAME = "seed-descriptor.json"
BASE_URL = f"https://codexrip.vip/skills/reverse-skill/versions/{MANIFEST_SHA256}"
DESCRIPTOR_URL = "https://codexrip.vip/skills/reverse-skill/current.json"
BOOTSTRAPS = {
    "bootstrap-reverse-skill.ps1": "e3dfee2e99fad9c890295a9de6fd1d2882c428971579049c3038b94d10668edd",
    "bootstrap-reverse-skill.py": "6bd6f94cb552f979443303c34883b12b475e724dcaf0b77843420f991459cf9c",
}
FORBIDDEN_RUNTIME_BYTES = (
    b"moxinggang.com",
    b"C:\\Users\\Administrator",
    "模型港".encode("utf-8"),
)


@dataclass(frozen=True)
class RegistryVerificationResult:
    manifest_sha256: str
    archive_sha256: str
    prompt_sha256: str
    file_count: int
    route_document_count: int
    bootstrap_count: int


def sha256(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def require_runtime_text_clean(label: str, raw: bytes) -> None:
    lowered = raw.lower()
    for forbidden in FORBIDDEN_RUNTIME_BYTES:
        if forbidden.lower() in lowered:
            raise VerificationError(f"{label} contains a forbidden legacy runtime reference")


def load_json_object(path: Path, label: str) -> tuple[bytes, dict]:
    try:
        raw = path.read_bytes()
        value = json.loads(raw.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise VerificationError(f"{label} is not valid UTF-8 JSON: {exc}") from exc
    if not isinstance(value, dict):
        raise VerificationError(f"{label} root must be an object")
    return raw, value


def verify_descriptor(path: Path, manifest: dict) -> dict:
    raw, descriptor = load_json_object(path, "descriptor")
    require_runtime_text_clean("descriptor", raw)
    exact = {
        "schema_version": 1,
        "bundle_id": BUNDLE_ID,
        "revision": 1,
        "source_commit": SOURCE_COMMIT,
        "overlay_sha256": OVERLAY_SHA256,
        "manifest_sha256": MANIFEST_SHA256,
        "archive_sha256": ARCHIVE_SHA256,
        "manifest_url": f"{BASE_URL}/{MANIFEST_NAME}",
        "archive_url": f"{BASE_URL}/{ARCHIVE_NAME}",
        "files_base_url": f"{BASE_URL}/",
        "file_count": FILE_COUNT,
        "total_bytes": TOTAL_BYTES,
        "bootstrap_policy": "download_verify_cache_materialize_only",
    }
    for key, expected in exact.items():
        if descriptor.get(key) != expected:
            raise VerificationError(f"descriptor {key} does not match the pinned release")
    if descriptor.get("core_files") != manifest.get("core_files"):
        raise VerificationError("descriptor core_files do not match the manifest")
    published_at = descriptor.get("published_at")
    if not isinstance(published_at, str) or not published_at.endswith("Z"):
        raise VerificationError("descriptor published_at must be a UTC timestamp")
    return descriptor


def verify_manifest_contract(manifest: dict) -> set[str]:
    if manifest.get("schema_version") != 1 or manifest.get("bundle_id") != BUNDLE_ID:
        raise VerificationError("manifest identity does not match the remote-skill contract")
    entries = manifest.get("files")
    if not isinstance(entries, list) or len(entries) != FILE_COUNT:
        raise VerificationError("manifest file count does not match the pinned release")
    declared = {entry.get("path") for entry in entries if isinstance(entry, dict)}
    if len(declared) != FILE_COUNT or not all(isinstance(name, str) for name in declared):
        raise VerificationError("manifest contains invalid or duplicate file paths")
    by_path = {entry.get("path"): entry for entry in entries if isinstance(entry, dict)}
    gradle_wrapper = by_path.get("burp-mcp-full/gradlew")
    if not isinstance(gradle_wrapper, dict) or gradle_wrapper.get("kind") != "script":
        raise VerificationError("shebang executables must be classified as scripts")
    total = sum(entry.get("byte_length", -1) for entry in entries if isinstance(entry, dict))
    if total != TOTAL_BYTES:
        raise VerificationError("manifest total byte length does not match the pinned release")
    runtime_documents: set[str] = set()
    core = manifest.get("core_files")
    if not isinstance(core, list) or not core:
        raise VerificationError("manifest core_files are missing")
    runtime_documents.update(core)
    domains = manifest.get("domains")
    if not isinstance(domains, list) or not domains:
        raise VerificationError("manifest routes are missing")
    route_ids: set[str] = set()
    for route in domains:
        if not isinstance(route, dict) or not isinstance(route.get("id"), str) or route["id"] in route_ids:
            raise VerificationError("manifest contains an invalid or duplicate route")
        route_ids.add(route["id"])
        entry = route.get("entry")
        references = route.get("references", [])
        if not isinstance(entry, str) or not isinstance(references, list) or not all(isinstance(item, str) for item in references):
            raise VerificationError("manifest route document references are invalid")
        runtime_documents.add(entry)
        runtime_documents.update(references)
    missing = runtime_documents - declared
    if missing:
        raise VerificationError("manifest core or route document is not declared")
    return runtime_documents


def verify_bootstraps(root: Path, prompt: bytes) -> int:
    found: dict[str, str] = {}
    try:
        directories = [item for item in root.iterdir() if item.is_dir() and any(item.iterdir())]
    except OSError as exc:
        raise VerificationError(f"bootstrap root cannot be read: {exc}") from exc
    for directory in directories:
        children = list(directory.iterdir())
        if len(children) != 1 or not children[0].is_file() or children[0].name not in BOOTSTRAPS:
            raise VerificationError("bootstrap content-addressed directory shape is invalid")
        raw = children[0].read_bytes()
        digest = sha256(raw)
        if digest != directory.name or digest != BOOTSTRAPS[children[0].name]:
            raise VerificationError("bootstrap path or pinned SHA-256 does not match its bytes")
        url = f"https://codexrip.vip/skills/bootstrap/{digest}/{children[0].name}".encode("ascii")
        if url not in prompt or digest.encode("ascii") not in prompt:
            raise VerificationError("fixed prompt does not pin a release bootstrap")
        found[children[0].name] = digest
    if found != BOOTSTRAPS:
        raise VerificationError("release must contain exactly the pinned PowerShell and Python bootstraps")
    return len(found)


def verify_registry(
    *,
    archive_path: Path,
    manifest_path: Path,
    checksum_path: Path,
    descriptor_path: Path,
    bootstrap_root: Path,
    prompt_path: Path,
) -> RegistryVerificationResult:
    prompt = prompt_path.read_bytes()
    if len(prompt) != PROMPT_BYTES or sha256(prompt) != PROMPT_SHA256 or prompt.endswith((b"\r", b"\n")):
        raise VerificationError("fixed system prompt bytes do not match the pinned release")
    require_runtime_text_clean("fixed system prompt", prompt)
    if DESCRIPTOR_URL.encode("ascii") not in prompt:
        raise VerificationError("fixed system prompt does not reference the public descriptor")

    verify_bundle(
        zip_path=archive_path,
        manifest_path=manifest_path,
        expected_zip_sha256=ARCHIVE_SHA256,
        expected_manifest_sha256=MANIFEST_SHA256,
        expected_manifest_file_count=FILE_COUNT,
        expected_zip_entry_count=FILE_COUNT + 1,
        expected_bundle_id=BUNDLE_ID,
        checksum_path=checksum_path,
    )
    manifest_raw, manifest = load_json_object(manifest_path, "manifest")
    require_runtime_text_clean("manifest", manifest_raw)
    runtime_documents = verify_manifest_contract(manifest)
    verify_descriptor(descriptor_path, manifest)
    bootstrap_count = verify_bootstraps(bootstrap_root, prompt)

    with zipfile.ZipFile(archive_path) as archive:
        if any(entry.compress_type != zipfile.ZIP_STORED for entry in archive.infolist()):
            raise VerificationError("release ZIP must use the canonical stored representation")
        for name in sorted(runtime_documents):
            require_runtime_text_clean(f"runtime document {name}", archive.read(name))

    return RegistryVerificationResult(
        manifest_sha256=MANIFEST_SHA256,
        archive_sha256=ARCHIVE_SHA256,
        prompt_sha256=PROMPT_SHA256,
        file_count=FILE_COUNT,
        route_document_count=len(runtime_documents),
        bootstrap_count=bootstrap_count,
    )


def default_paths() -> dict[str, Path]:
    root = Path(__file__).resolve().parents[1]
    seed = root / "deploy" / "skill-registry" / "seed"
    return {
        "archive_path": seed / ARCHIVE_NAME,
        "manifest_path": seed / MANIFEST_NAME,
        "checksum_path": seed / CHECKSUM_NAME,
        "descriptor_path": seed / DESCRIPTOR_NAME,
        "bootstrap_root": root / "deploy" / "skill-registry" / "bootstrap",
        "prompt_path": root / "backend" / "internal" / "service" / "prompts" / "codexrip_reverse_skill_system_prompt.txt",
    }


def main(argv: Optional[list[str]] = None) -> int:
    defaults = default_paths()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--zip", type=Path, default=defaults["archive_path"])
    parser.add_argument("--manifest", type=Path, default=defaults["manifest_path"])
    parser.add_argument("--checksum", type=Path, default=defaults["checksum_path"])
    parser.add_argument("--descriptor", type=Path, default=defaults["descriptor_path"])
    parser.add_argument("--bootstrap-root", type=Path, default=defaults["bootstrap_root"])
    parser.add_argument("--prompt", type=Path, default=defaults["prompt_path"])
    args = parser.parse_args(argv)
    try:
        result = verify_registry(
            archive_path=args.zip,
            manifest_path=args.manifest,
            checksum_path=args.checksum,
            descriptor_path=args.descriptor,
            bootstrap_root=args.bootstrap_root,
            prompt_path=args.prompt,
        )
    except (OSError, VerificationError, zipfile.BadZipFile) as exc:
        print(f"remote skill registry verification failed: {exc}", file=sys.stderr)
        return 1
    print(
        "remote skill registry verified: "
        f"manifest_sha256={result.manifest_sha256} archive_sha256={result.archive_sha256} "
        f"prompt_sha256={result.prompt_sha256} files={result.file_count} "
        f"runtime_documents={result.route_document_count} bootstraps={result.bootstrap_count}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

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
MANIFEST_SHA256 = "68ef7d1da27e4dd9c523c34ff71adfcd218232613051a1722759708897fef1b3"
ARCHIVE_SHA256 = "b5e682af9d8fcd31f7cd216679b5ffe67896e3faaeda28b80230829fbd3295e4"
OVERLAY_SHA256 = "c71df9943ba6f5d5d9409947267cbe7c19761d2382fce1be133f2223ba591898"
SOURCE_COMMIT = "a5d8c9233b98c52df387d5b1a0ef669fcaa51374"
PROMPT_SHA256 = "5813c55c0763e1472becec874232f3daafb28a69107b94ca8284daf44fceb2a0"
PROMPT_BYTES = 9034
FILE_COUNT = 545
TOTAL_BYTES = 7_925_332
ARCHIVE_NAME = f"{BUNDLE_ID}-{MANIFEST_SHA256}.zip"
CHECKSUM_NAME = f"{ARCHIVE_NAME}.sha256"
DESCRIPTOR_NAME = "seed-descriptor.json"
BASE_URL = f"https://codexrip.vip/skills/reverse-skill/versions/{MANIFEST_SHA256}"
DESCRIPTOR_URL = "https://codexrip.vip/skills/reverse-skill/current.json"
BOOTSTRAP_REPOSITORY_URL = "https://github.com/HTExplicit/sub2api.git"
BOOTSTRAP_REPOSITORY_REF = "v0.1.171-codexrip.7"
BOOTSTRAP_REPOSITORY_COMMIT = "176dad47dd049b34d45a032d889a0dc11405a39e"
BOOTSTRAPS = {
    "bootstrap-reverse-skill.ps1": "8595884159988ff653c1d66be66d25acc62a359009c85a7924a23dbaf45d4246",
    "bootstrap-reverse-skill.py": "2db6ff2d1a5182b73920aabe701d914cca83643aeab89443c0561b1a67430b42",
}
CLIENT_FILES = {
    "codexrip-client/SKILL.md",
    "codexrip-client/agents/openai.yaml",
}
CORE_FILES = ["RULES.md", "README_AI.md", "skills/SKILL.md"]
CORE_SHA256 = {
    "RULES.md": "2d86efa38f8a8b9ef23fa71edcae35cf111a8fef9027a8893ff66e7e4086afa0",
    "README_AI.md": "d79c9b34beba0160c1a290763ce40ddf9f4027d2086f575a1b396188ddef87c9",
    "skills/SKILL.md": "2c7994642ae2cd97a15fffc0d6e119e07e83582ca70cc9a7a5d212aa9a947a56",
}
FORBIDDEN_RUNTIME_BYTES = (
    b"moxinggang.com",
    b"C:\\Users\\Administrator\\AppData\\Local",
    "模型港".encode("utf-8"),
    b"README_RECONSTRUCTED.md",
    b"SOURCE-MANIFEST.json",
    b"inline-system-instructions.txt",
    b"codexrip-overlay/security-research",
    b"REMOTE_ROOT",
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
        "bootstrap_policy": "download_verify_native_skill_atomic_replace",
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
    if not CLIENT_FILES.issubset(declared):
        raise VerificationError("manifest does not include the native Codex Skill entry files")
    if any(isinstance(name, str) and name.startswith("codexrip-overlay/security-research/") for name in declared):
        raise VerificationError("manifest still contains the legacy security-research overlay")
    gradle_wrapper = by_path.get("burp-mcp-full/gradlew")
    if not isinstance(gradle_wrapper, dict) or gradle_wrapper.get("kind") != "script":
        raise VerificationError("shebang executables must be classified as scripts")
    total = sum(entry.get("byte_length", -1) for entry in entries if isinstance(entry, dict))
    if total != TOTAL_BYTES:
        raise VerificationError("manifest total byte length does not match the pinned release")
    runtime_documents: set[str] = set()
    core = manifest.get("core_files")
    if core != CORE_FILES:
        raise VerificationError("manifest core_files do not use the pinned upstream-native paths")
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
    by_route = {route["id"]: route for route in domains}
    for route_id, required_keywords in {
        "api-security": {"接口安全", "鉴权"},
        "js-reverse": {"js逆向", "前端逆向"},
    }.items():
        route = by_route.get(route_id)
        keywords = set(route.get("keywords", [])) if isinstance(route, dict) else set()
        if not required_keywords.issubset(keywords):
            raise VerificationError(f"manifest route {route_id} is missing pinned bilingual keywords")
    return runtime_documents


def verify_bootstraps(root: Path, prompt: bytes) -> int:
    for value in (BOOTSTRAP_REPOSITORY_URL, BOOTSTRAP_REPOSITORY_REF, BOOTSTRAP_REPOSITORY_COMMIT):
        if value.encode("ascii") in prompt:
            raise VerificationError("fixed prompt must not expose bootstrap repository identity")
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
        if digest.encode("ascii") in prompt or children[0].name.encode("ascii") in prompt:
            raise VerificationError("fixed prompt must not expose release bootstrap coordinates")
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
    prompt_source = prompt_path.read_bytes()
    # business_system_prompt_policy.go embeds the tracked LF source and removes
    # exactly one trailing LF so runtime bytes keep the published identity.
    prompt = prompt_source[:-1] if prompt_source.endswith(b"\n") else prompt_source
    if len(prompt) != PROMPT_BYTES or sha256(prompt) != PROMPT_SHA256 or prompt.endswith((b"\r", b"\n")):
        raise VerificationError("fixed system prompt bytes do not match the pinned release")
    require_runtime_text_clean("fixed system prompt", prompt)
    prompt_text = prompt.decode("utf-8")
    if prompt_text.count("宝宝") != 1 or 'The only allowed user address is exactly "老板".' not in prompt_text:
        raise VerificationError("fixed system prompt does not preserve the original address restriction")
    if DESCRIPTOR_URL.encode("ascii") in prompt:
        raise VerificationError("fixed system prompt must not reference the public descriptor")
    for forbidden in (b"DESCRIPTOR_URL", b"REPOSITORY_URL", b"REPOSITORY_COMMIT", b"POWERSHELL_BOOTSTRAP", b"PYTHON_BOOTSTRAP"):
        if forbidden in prompt:
            raise VerificationError("fixed system prompt exposes a supply-chain coordinate")

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
        forbidden_names = {"README_RECONSTRUCTED.md", "SOURCE-MANIFEST.json", "inline-system-instructions.txt"}
        if any(Path(entry.filename).name in forbidden_names for entry in archive.infolist()):
            raise VerificationError("release ZIP contains a removed provenance or captured-prompt file")
        for name in sorted(runtime_documents):
            raw = archive.read(name)
            require_runtime_text_clean(f"runtime document {name}", raw)
            if name in CORE_SHA256 and sha256(raw) != CORE_SHA256[name]:
                raise VerificationError(f"runtime core file does not match pinned upstream bytes: {name}")
        client_skill = archive.read("codexrip-client/SKILL.md")
        client_openai = archive.read("codexrip-client/agents/openai.yaml")
        require_runtime_text_clean("native Skill entry", client_skill)
        require_runtime_text_clean("native Skill metadata", client_openai)
        if not client_skill.startswith(b"---\nname: codexrip-reverse-skill\n") or any(
            value not in client_skill
            for value in (b"bundle/RULES.md", b"bundle/README_AI.md", b"bundle/skills/SKILL.md")
        ):
            raise VerificationError("native Skill entry does not route into the verified bundle")
        without_allowed_origin = client_skill.replace(b"https://codexrip.vip", b"")
        if b"https://" in without_allowed_origin or b"http://" in without_allowed_origin:
            raise VerificationError("native Skill entry contains a foreign acquisition source")
        if b'display_name: "CodexRip Reverse Skill"' not in client_openai or b"$codexrip-reverse-skill" not in client_openai:
            raise VerificationError("native Skill OpenAI metadata does not match the installed Skill")

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

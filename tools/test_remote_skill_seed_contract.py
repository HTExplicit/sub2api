#!/usr/bin/env python3
"""Regression contract for the published remote Skill seed and entry prompt."""

from __future__ import annotations

import json
import zipfile
from pathlib import Path

from build_remote_skill_seed import (
    BOOTSTRAPS,
    REMOTE_ROOT,
    SOURCE_COMMIT,
    SOURCE_ID,
    contains_remote_skill_acquisition,
    is_legacy_overlay_path,
)
from verify_remote_skill_registry import (
    MOXINGGANG_ROOT,
    VerificationError,
    document_urls,
    is_legacy_overlay_path as verifier_is_legacy_overlay_path,
    validate_source_root,
    verify_native_skill_contract,
)


ROOT = Path(__file__).resolve().parents[1]
SEED = ROOT / "deploy" / "skill-registry" / "seed"
PROMPT = ROOT / "backend" / "internal" / "service" / "prompts" / "codexrip_reverse_skill_system_prompt.txt"
EXPECTED_CORE = ["RULES.md", "README_AI.md", "skills/SKILL.md"]


def expect_source_root_rejected(source_id: str, remote_root: str, source_commit: str) -> None:
    try:
        validate_source_root(source_id, remote_root, source_commit)
    except VerificationError:
        return
    raise AssertionError(f"unsafe source root accepted: {remote_root}")


def main() -> int:
    validate_source_root(SOURCE_ID, REMOTE_ROOT, SOURCE_COMMIT)
    validate_source_root("moxinggang", MOXINGGANG_ROOT, "1" * 40)
    for source_id, remote_root, source_commit in (
        ("github_official", REMOTE_ROOT + "?ref=mutable", SOURCE_COMMIT),
        ("github_official", REMOTE_ROOT.replace("raw.githubusercontent.com", "github.example"), SOURCE_COMMIT),
        ("github_official", REMOTE_ROOT.replace(SOURCE_COMMIT, "a" * 40), SOURCE_COMMIT),
        ("moxinggang", MOXINGGANG_ROOT + "/../escape", "1" * 40),
        ("moxinggang", MOXINGGANG_ROOT.replace("moxinggang.com", "user@moxinggang.com"), "1" * 40),
        ("moxinggang", MOXINGGANG_ROOT, ""),
        ("moxinggang", MOXINGGANG_ROOT, "not-a-commit"),
        ("moxinggang", MOXINGGANG_ROOT, "../escape"),
        ("moxinggang", MOXINGGANG_ROOT, "A" * 40),
        ("unknown", REMOTE_ROOT, SOURCE_COMMIT),
    ):
        expect_source_root_rejected(source_id, remote_root, source_commit)

    descriptor = json.loads((SEED / "seed-descriptor.json").read_text(encoding="utf-8"))
    manifest = json.loads((SEED / "bundle-manifest.json").read_text(encoding="utf-8"))
    if descriptor.get("source_id") != SOURCE_ID:
        raise AssertionError("seed descriptor source_id is not the GitHub default")
    if descriptor.get("source_commit") != SOURCE_COMMIT or descriptor.get("remote_root") != REMOTE_ROOT:
        raise AssertionError("seed descriptor provenance is not pinned")
    if descriptor.get("schema_version") != 1 or descriptor.get("bootstraps") != BOOTSTRAPS:
        raise AssertionError("seed descriptor does not publish content-addressed bootstrap metadata")
    if manifest.get("core_files") != EXPECTED_CORE:
        raise AssertionError("seed manifest core paths changed")

    paths = {entry.get("path") for entry in manifest.get("files", [])}
    if any(isinstance(path, str) and is_legacy_overlay_path(path) for path in paths):
        raise AssertionError("legacy security-research overlay remains in the seed")
    if any(isinstance(path, str) and verifier_is_legacy_overlay_path(path) for path in paths):
        raise AssertionError("formal verifier missed a legacy overlay path")
    for path in [*EXPECTED_CORE, "codexrip-client/SKILL.md", "codexrip-client/agents/openai.yaml"]:
        if path not in paths:
            raise AssertionError(f"required seed file is missing: {path}")

    prompt = PROMPT.read_bytes()
    if prompt.endswith(b"\n"):
        prompt = prompt[:-1]
    if document_urls(prompt) != [REMOTE_ROOT]:
        raise AssertionError("base prompt does not use the single pinned GitHub root")
    if prompt.count(b"REMOTE_ROOT/SKILL.md") != 1:
        raise AssertionError("base prompt does not contain exactly one remote Skill entry")
    for forbidden in (
        b"LOCAL_BUNDLE_ROOT",
        b"[CODEXRIP VERIFIED SKILL DOCUMENTS]",
        b"[BUSINESS SYSTEM PROMPT: OFFLINE SKILL BUNDLE]",
    ):
        if forbidden in prompt:
            raise AssertionError("base prompt contains an offline wrapper")

    archive_path = SEED / f"codexrip-reverse-skill-{descriptor['manifest_sha256']}.zip"
    kinds = {entry.get("path"): entry.get("kind", "text") for entry in manifest.get("files", [])}
    with zipfile.ZipFile(archive_path) as archive:
        client_skill = archive.read("codexrip-client/SKILL.md")
        verify_native_skill_contract(client_skill, descriptor)
        for source, replacement in (
            (b"Perform exactly one version check through", b"Perform a version check through"),
            (b"verify its URL bytes against its SHA-256", b"download its URL bytes"),
            (b"existing local installation verifies successfully", b"existing local installation exists"),
            (b"do not repeat the version check or those three reads", b"may repeat the version check"),
        ):
            try:
                verify_native_skill_contract(client_skill.replace(source, replacement, 1), descriptor)
            except VerificationError:
                pass
            else:
                raise AssertionError("native Skill lifecycle mutation was accepted")
        reordered = client_skill.replace(b"bundle/RULES.md", b"bundle/TEMP.md", 1)
        reordered = reordered.replace(b"bundle/README_AI.md", b"bundle/RULES.md", 1)
        reordered = reordered.replace(b"bundle/TEMP.md", b"bundle/README_AI.md", 1)
        try:
            verify_native_skill_contract(reordered, descriptor)
        except VerificationError:
            pass
        else:
            raise AssertionError("native Skill core read reordering was accepted")
        non_atomic = dict(descriptor)
        non_atomic["bootstrap_policy"] = "download_only"
        try:
            verify_native_skill_contract(client_skill, non_atomic)
        except VerificationError:
            pass
        else:
            raise AssertionError("non-atomic native Skill policy was accepted")
        for name in archive.namelist():
            if name == "bundle-manifest.json" or kinds.get(name) == "binary":
                continue
            raw = archive.read(name)
            if name != "codexrip-client/SKILL.md" and contains_remote_skill_acquisition(raw):
                raise AssertionError(f"runtime document retains remote acquisition: {name}")

    by_route = {route.get("id"): set(route.get("keywords", [])) for route in manifest.get("domains", [])}
    for route, keywords in {
        "ida-reverse": {"ida pro", "idapython"},
        "dotnet-reverse": {".net", "dnspy"},
        "ghidra-reverse": {"ghidra", "decompiler"},
        "firmware-pentest": {"firmware", "binwalk"},
        "identity-federation": {"saml", "oidc", "sso"},
    }.items():
        actual = {value.casefold() for value in by_route.get(route, set())}
        if not keywords.issubset(actual):
            raise AssertionError(f"seed route lost compatibility keywords: {route}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

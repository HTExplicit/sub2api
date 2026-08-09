#!/usr/bin/env python3
"""Regression contract for the native upstream Skill seed."""

from __future__ import annotations

import json
import zipfile
from pathlib import Path

from build_remote_skill_seed import (
    FORBIDDEN_SKILL_SOURCE,
    LOCAL_PACKAGE_CONTRACT_LINE,
    LOCAL_RECOVERY_LINE,
    ROUTER_RECOVERY_LINE,
    UPSTREAM_CLONE_COMMAND,
    contains_remote_skill_acquisition,
    is_legacy_overlay_path,
    rewrite_package_contract,
)
from verify_remote_skill_registry import (
    FORBIDDEN_RUNTIME_BYTES,
    contains_remote_skill_acquisition as verifier_contains_remote_skill_acquisition,
    is_legacy_overlay_path as verifier_is_legacy_overlay_path,
)


ROOT = Path(__file__).resolve().parents[1]
SEED = ROOT / "deploy" / "skill-registry" / "seed"
EXPECTED_CORE = ["RULES.md", "README_AI.md", "skills/SKILL.md"]
PINNED_COMMIT = "a5d8c9233b98c52df387d5b1a0ef669fcaa51374"
EXPECTED_BOOTSTRAPS = {
    "powershell": {
        "url": "https://codexrip.vip/skills/bootstrap/2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9/bootstrap-reverse-skill.ps1",
        "sha256": "2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9",
    },
    "python": {
        "url": "https://codexrip.vip/skills/bootstrap/353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9/bootstrap-reverse-skill.py",
        "sha256": "353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9",
    },
}


def main() -> int:
    legacy_overlay_marker = b"moxinggang-overlay/security-research"
    if legacy_overlay_marker not in FORBIDDEN_SKILL_SOURCE or legacy_overlay_marker not in FORBIDDEN_RUNTIME_BYTES:
        raise AssertionError("legacy overlay text guard is inactive")
    rewritten = rewrite_package_contract("README.md", UPSTREAM_CLONE_COMMAND + b"\n")
    if UPSTREAM_CLONE_COMMAND in rewritten or LOCAL_PACKAGE_CONTRACT_LINE not in rewritten:
        raise AssertionError("reviewed package acquisition was not deterministically rewritten")
    rewritten_route = rewrite_package_contract(
        "skills/scripts/master-route.ps1", ROUTER_RECOVERY_LINE + b"\r\n"
    )
    if ROUTER_RECOVERY_LINE in rewritten_route or LOCAL_RECOVERY_LINE not in rewritten_route:
        raise AssertionError("reviewed router recovery instruction was not rewritten locally")
    for path, raw in (
        ("docs/install.md", UPSTREAM_CLONE_COMMAND + b"\n"),
        ("README.md", UPSTREAM_CLONE_COMMAND + b"\n" + UPSTREAM_CLONE_COMMAND + b"\n"),
        ("README.md", UPSTREAM_CLONE_COMMAND + b" --branch attacker\n"),
        ("README.md", UPSTREAM_CLONE_COMMAND + b" && echo attacker\n"),
    ):
        try:
            rewrite_package_contract(path, raw)
        except ValueError:
            pass
        else:
            raise AssertionError("unreviewed package acquisition rewrite was accepted")
    for path in (
        "codexrip-overlay/security-research/SKILL.md",
        "CodexRip-Overlay/Security-Research/SKILL.md",
        "MoxingGang-Overlay/Security-Research/SKILL.md",
    ):
        if not is_legacy_overlay_path(path):
            raise AssertionError("legacy overlay path guard is inactive")
        if not verifier_is_legacy_overlay_path(path):
            raise AssertionError("verifier legacy overlay path guard is inactive")
    if not contains_remote_skill_acquisition(b"Run git pull and retry.\n"):
        raise AssertionError("git pull acquisition guard is inactive")
    if not verifier_contains_remote_skill_acquisition(b"Run git pull and retry.\n"):
        raise AssertionError("verifier git pull acquisition guard is inactive")

    descriptor = json.loads((SEED / "seed-descriptor.json").read_text(encoding="utf-8"))
    manifest = json.loads((SEED / "bundle-manifest.json").read_text(encoding="utf-8"))
    if descriptor.get("source_commit") != PINNED_COMMIT:
        raise AssertionError("seed descriptor is not pinned to the latest upstream commit")
    if descriptor.get("schema_version") != 1 or descriptor.get("bootstraps") != EXPECTED_BOOTSTRAPS:
        raise AssertionError("seed descriptor does not publish content-addressed bootstrap metadata")
    if manifest.get("core_files") != EXPECTED_CORE:
        raise AssertionError("seed manifest still uses the legacy overlay core paths")
    paths = {entry.get("path") for entry in manifest.get("files", [])}
    if any(isinstance(path, str) and is_legacy_overlay_path(path) for path in paths):
        raise AssertionError("legacy security-research overlay remains in the seed")
    for path in EXPECTED_CORE:
        if path not in paths:
            raise AssertionError(f"native core file is missing: {path}")
    by_route = {route.get("id"): set(route.get("keywords", [])) for route in manifest.get("domains", [])}
    expected_routes = {
        "ida-reverse": {"ida pro", "idapython"},
        "dotnet-reverse": {".net", "dnspy"},
        "ghidra-reverse": {"ghidra", "decompiler"},
        "firmware-pentest": {"firmware", "binwalk"},
        "identity-federation": {"saml", "oidc", "sso"},
    }
    for route, keywords in expected_routes.items():
        actual = {value.casefold() for value in by_route.get(route, set())}
        if not keywords.issubset(actual):
            raise AssertionError(f"seed route lost compatibility keywords: {route}")
    archive_path = SEED / f"codexrip-reverse-skill-{descriptor['manifest_sha256']}.zip"
    with zipfile.ZipFile(archive_path) as archive:
        for name in archive.namelist():
            raw = archive.read(name).lower()
            if b"git clone https://github.com/zhaoxuya520/reverse-skill" in raw or b"git pull" in raw:
                raise AssertionError(f"runtime document retains GitHub Skill acquisition: {name}")
        route = archive.read("skills/scripts/master-route.ps1")
        if LOCAL_RECOVERY_LINE not in route or ROUTER_RECOVERY_LINE in route:
            raise AssertionError("runtime router does not use the local recovery contract")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

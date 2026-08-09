#!/usr/bin/env python3
"""Regression contract for the native upstream Skill seed."""

from __future__ import annotations

import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SEED = ROOT / "deploy" / "skill-registry" / "seed"
EXPECTED_CORE = ["RULES.md", "README_AI.md", "skills/SKILL.md"]
PINNED_COMMIT = "a5d8c9233b98c52df387d5b1a0ef669fcaa51374"
EXPECTED_BOOTSTRAPS = {
    "powershell": {
        "url": "https://codexrip.vip/skills/bootstrap/8595884159988ff653c1d66be66d25acc62a359009c85a7924a23dbaf45d4246/bootstrap-reverse-skill.ps1",
        "sha256": "8595884159988ff653c1d66be66d25acc62a359009c85a7924a23dbaf45d4246",
    },
    "python": {
        "url": "https://codexrip.vip/skills/bootstrap/2db6ff2d1a5182b73920aabe701d914cca83643aeab89443c0561b1a67430b42/bootstrap-reverse-skill.py",
        "sha256": "2db6ff2d1a5182b73920aabe701d914cca83643aeab89443c0561b1a67430b42",
    },
}


def main() -> int:
    descriptor = json.loads((SEED / "seed-descriptor.json").read_text(encoding="utf-8"))
    manifest = json.loads((SEED / "bundle-manifest.json").read_text(encoding="utf-8"))
    if descriptor.get("source_commit") != PINNED_COMMIT:
        raise AssertionError("seed descriptor is not pinned to the latest upstream commit")
    if descriptor.get("schema_version") != 1 or descriptor.get("bootstraps") != EXPECTED_BOOTSTRAPS:
        raise AssertionError("seed descriptor does not publish content-addressed bootstrap metadata")
    if manifest.get("core_files") != EXPECTED_CORE:
        raise AssertionError("seed manifest still uses the legacy overlay core paths")
    paths = {entry.get("path") for entry in manifest.get("files", [])}
    if any(path.startswith("codexrip-overlay/security-research/") for path in paths):
        raise AssertionError("legacy security-research overlay remains in the seed")
    for path in EXPECTED_CORE:
        if path not in paths:
            raise AssertionError(f"native core file is missing: {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

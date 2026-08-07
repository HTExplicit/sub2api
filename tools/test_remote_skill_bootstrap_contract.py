#!/usr/bin/env python3
"""Verify native Codex Skill installation without network access."""

from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import stat
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
BUNDLE_ID = "codexrip-reverse-skill"
SOURCE_COMMIT = "1" * 40
SKILL_NAME = "codexrip-reverse-skill"
PYTHON_BOOTSTRAP = ROOT / "deploy" / "skill-registry" / "bootstrap" / "2db6ff2d1a5182b73920aabe701d914cca83643aeab89443c0561b1a67430b42" / "bootstrap-reverse-skill.py"
POWERSHELL_BOOTSTRAP = ROOT / "deploy" / "skill-registry" / "bootstrap" / "8595884159988ff653c1d66be66d25acc62a359009c85a7924a23dbaf45d4246" / "bootstrap-reverse-skill.ps1"


def sha256(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def build_fixture(root: Path) -> tuple[Path, str]:
    files = {
        "RULES.md": b"contract core\n",
        "README_AI.md": b"contract readme\n",
        "skills/SKILL.md": b"---\nname: upstream-router\ndescription: upstream router\n---\n",
        "skills/MASTER-ROUTING.md": b"contract routing\n",
        "skills/sentinel/SKILL.md": b"---\nname: sentinel\ndescription: sentinel route\n---\n",
        "skills/sentinel/sentinel.py": b"from pathlib import Path\nPath(__file__).with_name('EXECUTED').write_text('ran')\n",
        "codexrip-client/SKILL.md": (
            b"---\nname: codexrip-reverse-skill\n"
            b"description: Route reverse engineering, security research, and CTF tasks.\n---\n"
            b"Read bundle/RULES.md, bundle/README_AI.md, bundle/skills/SKILL.md, "
            b"and bundle/skills/MASTER-ROUTING.md before selecting a route.\n"
        ),
        "codexrip-client/agents/openai.yaml": b"interface:\n  display_name: CodexRip Reverse Skill\n",
    }
    entries = [
        {
            "path": name,
            "sha256": sha256(raw),
            "byte_length": len(raw),
            "kind": "script" if name.endswith(".py") else "text",
            "required": True,
        }
        for name, raw in sorted(files.items())
    ]
    manifest = {
        "schema_version": 1,
        "bundle_id": BUNDLE_ID,
        "version": "contract-v1",
        "core_files": ["RULES.md", "README_AI.md", "skills/SKILL.md"],
        "files": entries,
        "domains": [
            {
                "id": "sentinel",
                "keywords": ["sentinel", "哨兵"],
                "entry": "skills/sentinel/SKILL.md",
                "references": ["skills/sentinel/sentinel.py"],
            }
        ],
    }
    manifest_raw = json.dumps(manifest, separators=(",", ":"), sort_keys=True).encode("utf-8")
    manifest_sha = sha256(manifest_raw)
    archive_name = f"{BUNDLE_ID}-{manifest_sha}.zip"
    archive_path = root / archive_name
    with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, raw in [("bundle-manifest.json", manifest_raw), *sorted(files.items())]:
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 3
            info.external_attr = (stat.S_IFREG | 0o644) << 16
            archive.writestr(info, raw)
    archive_sha = sha256(archive_path.read_bytes())
    (root / "bundle-manifest.json").write_bytes(manifest_raw)
    descriptor = {
        "schema_version": 1,
        "bundle_id": BUNDLE_ID,
        "revision": 1,
        "source_commit": SOURCE_COMMIT,
        "manifest_sha256": manifest_sha,
        "archive_sha256": archive_sha,
        "manifest_url": f"https://codexrip.vip/skills/reverse-skill/versions/{manifest_sha}/bundle-manifest.json",
        "archive_url": f"https://codexrip.vip/skills/reverse-skill/versions/{manifest_sha}/{archive_name}",
    }
    descriptor_path = root / "descriptor.json"
    descriptor_path.write_text(json.dumps(descriptor, separators=(",", ":")), encoding="utf-8")
    return descriptor_path, manifest_sha


def parse_result(completed: subprocess.CompletedProcess[str]) -> dict:
    if completed.returncode != 0:
        raise RuntimeError(f"bootstrap failed: {completed.stderr.strip()}")
    lines = [line for line in completed.stdout.splitlines() if line.strip()]
    if not lines:
        raise RuntimeError("bootstrap produced no JSON result")
    result = json.loads(lines[-1])
    if result.get("status") != "ready" or result.get("scripts_executed") is not False:
        raise RuntimeError("bootstrap result violates the execution contract")
    return result


def command_for(implementation: str, descriptor: Path, assets: Path, codex_home: Path) -> list[str]:
    if implementation == "python":
        return [
            sys.executable,
            str(PYTHON_BOOTSTRAP),
            "--descriptor-file",
            str(descriptor),
            "--asset-root",
            str(assets),
            "--codex-home",
            str(codex_home),
        ]
    pwsh = shutil.which("pwsh")
    if not pwsh:
        raise RuntimeError("pwsh 7 is required for the PowerShell bootstrap contract")
    return [
        pwsh,
        "-NoLogo",
        "-NoProfile",
        "-File",
        str(POWERSHELL_BOOTSTRAP),
        "-DescriptorFile",
        str(descriptor),
        "-AssetRoot",
        str(assets),
        "-CodexHome",
        str(codex_home),
    ]


def run_implementation(implementation: str) -> None:
    with tempfile.TemporaryDirectory(prefix=f"codexrip-{implementation}-") as raw_root:
        root = Path(raw_root)
        assets = root / "assets"
        codex_home = root / "codex-home"
        assets.mkdir()
        descriptor, manifest_sha = build_fixture(assets)

        first = parse_result(
            subprocess.run(
                command_for(implementation, descriptor, assets, codex_home),
                text=True,
                capture_output=True,
                timeout=60,
                check=False,
            )
        )
        skill = codex_home / "skills" / SKILL_NAME
        if Path(first["skill_path"]) != skill.resolve() or first.get("replaced") is not False:
            raise RuntimeError("first install returned the wrong native Skill state")
        if first.get("manifest_sha256") != manifest_sha:
            raise RuntimeError("first install returned the wrong manifest")
        if not (skill / "SKILL.md").is_file() or not (skill / "agents" / "openai.yaml").is_file():
            raise RuntimeError("native Skill entry files are missing")
        if not (skill / "bundle" / "RULES.md").is_file() or not (skill / "bundle" / "skills" / "sentinel" / "sentinel.py").is_file():
            raise RuntimeError("complete verified bundle is missing")
        stale = skill / "STALE"
        stale.write_text("must be removed", encoding="utf-8")
        (skill / "bundle" / "RULES.md").write_text("corrupt", encoding="utf-8")

        second = parse_result(
            subprocess.run(
                command_for(implementation, descriptor, assets, codex_home),
                text=True,
                capture_output=True,
                timeout=60,
                check=False,
            )
        )
        if second.get("replaced") is not True or stale.exists():
            raise RuntimeError("manual update did not directly replace the old Skill")
        if (skill / "bundle" / "RULES.md").read_bytes() != b"contract core\n":
            raise RuntimeError("manual update did not restore verified bytes")
        if list(root.rglob("EXECUTED")):
            raise RuntimeError("bundle script executed during installation")
        if list((codex_home / "skills").glob(f".{SKILL_NAME}-old-*")):
            raise RuntimeError("installer retained a previous client version")
        print(f"{implementation} native Skill contract verified: replaced=true scripts_executed=false")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--implementation", choices=("python", "powershell", "all"), default="all")
    args = parser.parse_args()
    implementations = ("python", "powershell") if args.implementation == "all" else (args.implementation,)
    for implementation in implementations:
        run_implementation(implementation)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

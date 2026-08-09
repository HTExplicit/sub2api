#!/usr/bin/env python3
"""Verify native Codex Skill installation without network access."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import shutil
import stat
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
BUNDLE_ID = "codexrip-reverse-skill"
SOURCE_COMMIT = "1" * 40
SKILL_NAME = "codexrip-reverse-skill"
PYTHON_BOOTSTRAP = ROOT / "deploy" / "skill-registry" / "bootstrap" / "353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9" / "bootstrap-reverse-skill.py"
POWERSHELL_BOOTSTRAP = ROOT / "deploy" / "skill-registry" / "bootstrap" / "2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9" / "bootstrap-reverse-skill.ps1"


def sha256(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def build_fixture(
    root: Path,
    path_overrides: dict[str, str] | None = None,
    omit_files: set[str] | None = None,
    core_files: list[str] | None = None,
) -> tuple[Path, str]:
    path_overrides = path_overrides or {}
    omit_files = omit_files or set()

    def output_path(name: str) -> str:
        return path_overrides.get(name, name)

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
    files = {name: raw for name, raw in files.items() if name not in omit_files}
    entries = [
        {
            "path": output_path(name),
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
        "core_files": core_files if core_files is not None else ["RULES.md", "README_AI.md", "skills/SKILL.md"],
        "files": entries,
        "domains": [
            {
                "id": "sentinel",
                "keywords": ["sentinel", "哨兵"],
                "entry": "skills/sentinel/SKILL.md",
                "references": [output_path("skills/sentinel/sentinel.py")],
            }
        ],
    }
    manifest_raw = json.dumps(manifest, separators=(",", ":"), sort_keys=True).encode("utf-8")
    manifest_sha = sha256(manifest_raw)
    archive_name = f"{BUNDLE_ID}-{manifest_sha}.zip"
    archive_path = root / archive_name
    with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, raw in [("bundle-manifest.json", manifest_raw), *((output_path(name), raw) for name, raw in sorted(files.items()))]:
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
        "core_files": core_files if core_files is not None else ["RULES.md", "README_AI.md", "skills/SKILL.md"],
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


def verify_bootstrap_transport_and_commit_guards() -> None:
    powershell_text = POWERSHELL_BOOTSTRAP.read_text(encoding="utf-8")
    if "-MaximumRedirection 0" not in powershell_text or "-MaximumRedirection 3" in powershell_text:
        raise RuntimeError("PowerShell bootstrap permits automatic redirects")
    if "$uri.Query" not in powershell_text:
        raise RuntimeError("PowerShell bootstrap permits URL queries")
    if "catch { Write-Verbose 'previous Skill cleanup deferred after a successful atomic install' }" not in powershell_text:
        raise RuntimeError("PowerShell bootstrap does not make old-tree cleanup best effort")

    python_text = PYTHON_BOOTSTRAP.read_text(encoding="utf-8")
    if "build_opener(NoRedirect())" not in python_text or "class NoRedirect" not in python_text:
        raise RuntimeError("Python bootstrap does not disable automatic redirects")
    if "shutil.rmtree(backup, ignore_errors=True)" not in python_text:
        raise RuntimeError("Python bootstrap does not make old-tree cleanup best effort")

    # Loading the content-addressed source must not add __pycache__ beside it.
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("codexrip_bootstrap_contract", PYTHON_BOOTSTRAP)
    if spec is None or spec.loader is None:
        raise RuntimeError("Python bootstrap module could not be loaded")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    original_build_opener = module.urllib.request.build_opener

    class RedirectProbe:
        def __init__(self, handlers):
            self.handlers = handlers

        def open(self, request, timeout):
            redirect_handler = next(
                handler for handler in self.handlers if isinstance(handler, urllib.request.HTTPRedirectHandler)
            )
            redirected = redirect_handler.redirect_request(
                request,
                None,
                302,
                "Found",
                {},
                "https://outside.example/skill.py",
            )
            if redirected is not None:
                raise AssertionError("Python bootstrap followed an external redirect")
            raise urllib.error.HTTPError(request.full_url, 302, "redirect rejected", {}, None)

    module.urllib.request.build_opener = lambda *handlers: RedirectProbe(handlers)
    try:
        try:
            module.download("https://codexrip.vip/skills/bootstrap/test.py", 1024)
        except module.BootstrapError:
            pass
        else:
            raise RuntimeError("Python bootstrap accepted a redirect response")
    finally:
        module.urllib.request.build_opener = original_build_opener


def corrupt_update_archive(assets: Path, descriptor_path: Path) -> None:
    descriptor = json.loads(descriptor_path.read_text(encoding="utf-8"))
    archive_path = assets / Path(descriptor["archive_url"]).name
    with zipfile.ZipFile(archive_path, "r") as archive:
        contents = {info.filename: archive.read(info.filename) for info in archive.infolist()}
    contents["RULES.md"] = b"tampered update\n"
    with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, raw in sorted(contents.items()):
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 3
            info.external_attr = (stat.S_IFREG | 0o644) << 16
            archive.writestr(info, raw)
    descriptor["archive_sha256"] = sha256(archive_path.read_bytes())
    descriptor_path.write_text(json.dumps(descriptor, separators=(",", ":")), encoding="utf-8")


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
        if Path(first["skill_path"]).resolve() != skill.resolve() or first.get("replaced") is not False:
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
        preserved = skill / "PRESERVED"
        preserved.write_text("old install must survive", encoding="utf-8")
        corrupt_update_archive(assets, descriptor)
        failed = subprocess.run(
            command_for(implementation, descriptor, assets, codex_home),
            text=True,
            capture_output=True,
            timeout=60,
            check=False,
        )
        if failed.returncode == 0:
            raise RuntimeError("corrupt update unexpectedly succeeded")
        if not preserved.is_file() or (skill / "bundle" / "RULES.md").read_bytes() != b"contract core\n":
            raise RuntimeError("corrupt update did not preserve the old Skill")
        if list(root.rglob("EXECUTED")):
            raise RuntimeError("bundle script executed during failed installation")
        if list((codex_home / "skills").glob(f".{SKILL_NAME}-old-*")):
            raise RuntimeError("failed update retained a previous-version staging directory")
        print(f"{implementation} native Skill contract verified: replaced=true scripts_executed=false")


def run_rejects_nonportable_manifest_path(implementation: str) -> None:
    invalid_paths = (
        "skills/sentinel/file:ads",
        "skills/sentinel/file?.txt",
        "skills/sentinel/trailing. ",
        "skills/sentinel/COM1.txt",
    )
    for invalid_path in invalid_paths:
        with tempfile.TemporaryDirectory(prefix=f"codexrip-{implementation}-path-") as raw_root:
            root = Path(raw_root)
            assets = root / "assets"
            codex_home = root / "codex-home"
            assets.mkdir()
            descriptor, _ = build_fixture(
                assets,
                {"skills/sentinel/sentinel.py": invalid_path},
            )
            completed = subprocess.run(
                command_for(implementation, descriptor, assets, codex_home),
                text=True,
                capture_output=True,
                timeout=60,
                check=False,
            )
            if completed.returncode == 0:
                raise RuntimeError(f"{implementation} bootstrap accepted non-portable path {invalid_path}")
            if (codex_home / "skills" / SKILL_NAME).exists():
                raise RuntimeError(f"{implementation} bootstrap installed a rejected manifest {invalid_path}")


def run_rejects_missing_core(implementation: str) -> None:
    with tempfile.TemporaryDirectory(prefix=f"codexrip-{implementation}-core-") as raw_root:
        root = Path(raw_root)
        assets = root / "assets"
        codex_home = root / "codex-home"
        assets.mkdir()
        descriptor, _ = build_fixture(
            assets,
            omit_files={"RULES.md", "README_AI.md", "skills/SKILL.md"},
            core_files=[],
        )
        completed = subprocess.run(
            command_for(implementation, descriptor, assets, codex_home),
            text=True,
            capture_output=True,
            timeout=60,
            check=False,
        )
        if completed.returncode == 0:
            raise RuntimeError(f"{implementation} bootstrap accepted a manifest without native core files")
        if (codex_home / "skills" / SKILL_NAME).exists():
            raise RuntimeError(f"{implementation} bootstrap installed a manifest without native core files")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--implementation", choices=("python", "powershell", "all"), default="all")
    args = parser.parse_args()
    verify_bootstrap_transport_and_commit_guards()
    implementations = ("python", "powershell") if args.implementation == "all" else (args.implementation,)
    for implementation in implementations:
        run_implementation(implementation)
        run_rejects_nonportable_manifest_path(implementation)
        run_rejects_missing_core(implementation)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

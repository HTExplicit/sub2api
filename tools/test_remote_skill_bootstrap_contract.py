#!/usr/bin/env python3
"""Run the PowerShell and Python bootstrap lifecycle contract without networking."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
BUNDLE_ID = "codexrip-contract-fixture"
SOURCE_COMMIT = "1" * 40
PYTHON_BOOTSTRAP = ROOT / "deploy" / "skill-registry" / "bootstrap" / "6bd6f94cb552f979443303c34883b12b475e724dcaf0b77843420f991459cf9c" / "bootstrap-reverse-skill.py"
POWERSHELL_BOOTSTRAP = ROOT / "deploy" / "skill-registry" / "bootstrap" / "e3dfee2e99fad9c890295a9de6fd1d2882c428971579049c3038b94d10668edd" / "bootstrap-reverse-skill.ps1"


def sha256(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def build_fixture(root: Path) -> tuple[Path, Path, str]:
    files = {
        "RULES.md": b"contract core\n",
        "routes/sentinel.md": b"contract route\n",
        "routes/sentinel.py": b"from pathlib import Path\nPath(__file__).with_name('EXECUTED').write_text('ran')\n",
    }
    entries = [
        {"path": name, "sha256": sha256(raw), "byte_length": len(raw), "kind": "script" if name.endswith(".py") else "text", "required": True}
        for name, raw in sorted(files.items())
    ]
    manifest = {
        "schema_version": 1,
        "bundle_id": BUNDLE_ID,
        "version": "contract-v1",
        "core_files": ["RULES.md"],
        "files": entries,
        "domains": [{"id": "sentinel", "keywords": ["sentinel"], "entry": "routes/sentinel.md", "references": ["routes/sentinel.py"]}],
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
    return descriptor_path, archive_path, sha256(files["routes/sentinel.py"])


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


def command_for(implementation: str, descriptor: Path, assets: Path, cache: Path, tasks: Path, task_id: str, route: bool) -> list[str]:
    if implementation == "python":
        command = [sys.executable, str(PYTHON_BOOTSTRAP), "--descriptor-file", str(descriptor), "--asset-root", str(assets), "--cache-root", str(cache), "--task-root", str(tasks), "--task-id", task_id]
        if route:
            command += ["--route-id", "sentinel"]
        return command
    pwsh = shutil.which("pwsh")
    if not pwsh:
        raise RuntimeError("pwsh 7 is required for the PowerShell bootstrap contract")
    command = [pwsh, "-NoLogo", "-NoProfile", "-File", str(POWERSHELL_BOOTSTRAP), "-DescriptorFile", str(descriptor), "-AssetRoot", str(assets), "-CacheRoot", str(cache), "-TaskRoot", str(tasks), "-TaskId", task_id]
    if route:
        command += ["-RouteId", "sentinel"]
    return command


def run_implementation(implementation: str) -> None:
    with tempfile.TemporaryDirectory(prefix=f"codexrip-{implementation}-") as raw_root:
        root = Path(raw_root)
        assets = root / "assets"
        cache = root / "cache"
        tasks = root / "tasks"
        assets.mkdir()
        descriptor, archive, script_sha = build_fixture(assets)

        first = parse_result(subprocess.run(
            command_for(implementation, descriptor, assets, cache, tasks, "first", False),
            text=True, capture_output=True, timeout=60, check=False,
        ))
        if first.get("cache_reused") is not False or first.get("materialized_scripts"):
            raise RuntimeError("first bootstrap run did not create a clean non-route cache")

        stale = tasks / "expired-task"
        stale.mkdir()
        old = time.time() - 9 * 24 * 60 * 60
        os.utime(stale, (old, old))
        archive.unlink()

        second = parse_result(subprocess.run(
            command_for(implementation, descriptor, assets, cache, tasks, "second", True),
            text=True, capture_output=True, timeout=60, check=False,
        ))
        scripts = second.get("materialized_scripts", [])
        if second.get("cache_reused") is not True or len(scripts) != 1:
            raise RuntimeError("second bootstrap run did not reuse cache or materialize the explicit script")
        script_path = Path(scripts[0])
        if sha256(script_path.read_bytes()) != script_sha:
            raise RuntimeError("materialized script digest mismatch")
        if stale.exists() or list(tasks.rglob("EXECUTED")):
            raise RuntimeError("task cleanup or script non-execution contract failed")
        if Path(first["task_path"]) == Path(second["task_path"]):
            raise RuntimeError("bootstrap reused a task directory")
        print(f"{implementation} bootstrap contract verified: cache_reused=true scripts_executed=false")


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

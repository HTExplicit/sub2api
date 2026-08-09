#!/usr/bin/env python3
"""Build the CodexRip native-skill registry seed from a pinned upstream ZIP."""

from __future__ import annotations

import hashlib
import io
import json
import os
import re
import stat
import urllib.parse
import urllib.request
import zipfile
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath


ROOT = Path(__file__).resolve().parents[1]
BUNDLE_ID = "codexrip-reverse-skill"
SOURCE_COMMIT = "a5d8c9233b98c52df387d5b1a0ef669fcaa51374"
SOURCE_URL = f"https://codeload.github.com/zhaoxuya520/reverse-skill/zip/{SOURCE_COMMIT}"
SOURCE_ARCHIVE_SHA256 = "c6cc4a531b62ded1fae92cc8cdace9cf7833fe23978350161d90dedff77f80df"
SOURCE_ARCHIVE_ENV = "CODEXRIP_REMOTE_SKILL_SOURCE_ZIP"
OUTPUT = ROOT / "deploy" / "skill-registry" / "seed"
MANIFEST_NAME = "bundle-manifest.json"
MAX_SOURCE_BYTES = 128 << 20
MAX_FILE_BYTES = 64 << 20
MAX_TOTAL_BYTES = 256 << 20
MAX_FILE_COUNT = 2000
BOOTSTRAPS = {
    "powershell": {
        "url": "https://codexrip.vip/skills/bootstrap/8595884159988ff653c1d66be66d25acc62a359009c85a7924a23dbaf45d4246/bootstrap-reverse-skill.ps1",
        "sha256": "8595884159988ff653c1d66be66d25acc62a359009c85a7924a23dbaf45d4246",
    },
    "python": {
        "url": "https://codexrip.vip/skills/bootstrap/2db6ff2d1a5182b73920aabe701d914cca83643aeab89443c0561b1a67430b42/bootstrap-reverse-skill.py",
        "sha256": "2db6ff2d1a5182b73920aabe701d914cca83643aeab89443c0561b1a67430b42",
    },
}

CORE_FILES = ["RULES.md", "README_AI.md", "skills/SKILL.md"]
CORE_SHA256 = {
    "RULES.md": "2d86efa38f8a8b9ef23fa71edcae35cf111a8fef9027a8893ff66e7e4086afa0",
    "README_AI.md": "d79c9b34beba0160c1a290763ce40ddf9f4027d2086f575a1b396188ddef87c9",
    "skills/SKILL.md": "2c7994642ae2cd97a15fffc0d6e119e07e83582ca70cc9a7a5d212aa9a947a56",
}

CLIENT_FILES = {
    "codexrip-client/SKILL.md": b"""---
name: codexrip-reverse-skill
description: Use for reverse engineering, security research, or CTF tasks, including decompilation, binary or mobile analysis, protocol analysis, vulnerability validation, exploitation, malware, forensics, prompt-injection assessment, packet capture, PCAP, and traffic analysis.
---

# CodexRip Reverse Skill

On the first matching task in each conversation:

1. Resolve this installed Skill through native Skill discovery; never assume an absolute path.
2. Perform exactly one version check through `https://codexrip.vip`. Install a missing or newer validated bundle atomically using the published server bootstrap contract.
3. If acquisition or update fails, report the failed stage and continue only when the existing local installation verifies successfully. Otherwise report `skill unavailable`.
4. Read `bundle/RULES.md`, `bundle/README_AI.md`, and `bundle/skills/SKILL.md` completely in that order.

Later matching tasks in the same conversation do not repeat the version check or those three reads. Resolve every package-local reference relative to the installed `bundle/`; never load Skill content remotely at runtime.
""",
    "codexrip-client/agents/openai.yaml": b"""interface:
  display_name: "CodexRip Reverse Skill"
  short_description: "Route reverse engineering, security research, and CTF tasks through the verified local bundle"
  default_prompt: "Use $codexrip-reverse-skill and load its verified local core in order."

policy:
  allow_implicit_invocation: true
""",
}

SCRIPT_EXTENSIONS = {
    ".ps1", ".psm1", ".sh", ".bash", ".zsh", ".fish", ".py", ".rb",
    ".pl", ".lua", ".js", ".mjs", ".cjs", ".ts", ".bat", ".cmd",
}
BINARY_EXTENSIONS = {
    ".png", ".jpg", ".jpeg", ".gif", ".webp", ".jar", ".zip", ".gz",
    ".7z", ".exe", ".dll", ".so", ".pdf", ".docx",
}
EXCLUDED_SOURCE_NAMES = {
    "README_RECONSTRUCTED.md",
    "SOURCE-MANIFEST.json",
    "inline-system-instructions.txt",
}
FORBIDDEN_SKILL_SOURCE = (
    b"moxinggang.com",
    b"codexrip-overlay/security-research",
    b"REMOTE_ROOT",
    b"C:\\Users\\Administrator\\AppData\\Local",
    "\u6a21\u578b\u6e2f".encode("utf-8"),
)
LOCAL_PACKAGE_CONTRACT_LINE = b"# Package root: verified installed bundle/"
HTTP_URL_PATTERN = re.compile(rb"https?://[^\s<>\"'`]+", re.IGNORECASE)

ROUTE_KEYWORDS = {
    "api-security": ["api", "http", "rest", "graphql", "jwt", "oauth", "authentication", "authorization", "\u63a5\u53e3\u5b89\u5168", "\u9274\u6743", "\u8ba4\u8bc1", "\u8d8a\u6743"],
    "apk-reverse": ["apk", "android", "smali", "frida", "jadx", "\u5b89\u5353\u9006\u5411", "\u5e94\u7528\u9006\u5411"],
    "attack-chain": ["attack chain", "exploit chain", "kill chain", "lateral movement", "\u653b\u51fb\u94fe", "\u5229\u7528\u94fe", "\u6a2a\u5411\u79fb\u52a8"],
    "binary-diff": ["binary diff", "bindiff", "patch diff", "\u4e8c\u8fdb\u5236\u5bf9\u6bd4", "\u8865\u4e01\u5bf9\u6bd4"],
    "browser-automation": ["browser automation", "playwright", "selenium", "cdp", "\u6d4f\u89c8\u5668\u81ea\u52a8\u5316"],
    "browser-extension-reverse": ["browser extension", "chrome extension", "firefox extension", "\u6d4f\u89c8\u5668\u6269\u5c55\u9006\u5411", "\u63d2\u4ef6\u9006\u5411"],
    "cloud-k8s": ["cloud", "kubernetes", "k8s", "container", "docker", "\u4e91\u5b89\u5168", "\u5bb9\u5668\u5b89\u5168"],
    "code-audit": ["code audit", "source audit", "sast", "code review", "\u4ee3\u7801\u5ba1\u8ba1", "\u6e90\u7801\u5ba1\u8ba1"],
    "database-security": ["database", "sql", "postgresql", "mysql", "redis", "\u6570\u636e\u5e93\u5b89\u5168", "\u6570\u636e\u5e93\u5ba1\u8ba1"],
    "digital-forensics": ["forensics", "disk image", "memory dump", "pcap", "\u6570\u5b57\u53d6\u8bc1", "\u5185\u5b58\u53d6\u8bc1", "\u6d41\u91cf\u53d6\u8bc1"],
    "dotnet-reverse": [".net", "dotnet", "c#", "dnspy", "ilspy", ".net\u9006\u5411", "c#\u9006\u5411"],
    "edr-bypass-re": ["edr", "endpoint detection", "unhook", "telemetry", "edr\u9006\u5411", "\u7aef\u70b9\u68c0\u6d4b"],
    "email-security": ["email security", "smtp", "spf", "dkim", "dmarc", "\u90ae\u4ef6\u5b89\u5168"],
    "firmware-pentest": ["firmware", "embedded", "binwalk", "uart", "\u56fa\u4ef6\u5b89\u5168", "\u56fa\u4ef6\u9006\u5411", "\u5d4c\u5165\u5f0f\u5b89\u5168"],
    "ghidra-reverse": ["ghidra", "decompiler", "ghidra\u9006\u5411", "\u53cd\u7f16\u8bd1\u5668"],
    "go-rust-reverse": ["golang binary", "go binary", "rust binary", "go\u9006\u5411", "rust\u9006\u5411"],
    "hardware-security": ["hardware security", "jtag", "spi", "side channel", "\u786c\u4ef6\u5b89\u5168", "\u4fa7\u4fe1\u9053"],
    "ida-reverse": ["ida pro", "idapython", "disassembly", "ida\u9006\u5411", "\u53cd\u6c47\u7f16"],
    "identity-federation": ["saml", "oidc", "identity federation", "single sign-on", "sso", "\u8eab\u4efd\u8054\u5408", "\u5355\u70b9\u767b\u5f55"],
    "js-reverse": ["javascript reverse", "js reverse", "web reverse", "wasm", "obfuscation", "js\u9006\u5411", "\u7f51\u9875\u9006\u5411", "\u524d\u7aef\u9006\u5411", "\u53cd\u6df7\u6dc6"],
    "llm-security": ["llm security", "prompt injection", "jailbreak", "agent security", "\u5927\u6a21\u578b\u5b89\u5168", "\u63d0\u793a\u8bcd\u6ce8\u5165", "\u8d8a\u72f1", "\u667a\u80fd\u4f53\u5b89\u5168"],
    "macos-reverse": ["macos", "mach-o", "objective-c", "swift binary", "macos\u9006\u5411", "\u82f9\u679c\u7535\u8111\u9006\u5411"],
    "malware-analysis": ["malware", "ransomware", "trojan", "sandbox", "yara", "\u6076\u610f\u8f6f\u4ef6", "\u6076\u610f\u6837\u672c", "\u52d2\u7d22\u8f6f\u4ef6", "\u6728\u9a6c\u5206\u6790"],
    "mobile-reverse": ["mobile reverse", "ios reverse", "android reverse", "\u79fb\u52a8\u7aef\u9006\u5411", "ios\u9006\u5411", "\u5b89\u5353\u9006\u5411"],
    "ot-ics": ["ot security", "ics", "scada", "modbus", "\u5de5\u63a7\u5b89\u5168", "\u5de5\u4e1a\u63a7\u5236"],
    "patch-diff-exploit": ["patch diff", "cve patch", "vulnerability patch", "\u8865\u4e01\u5206\u6790", "\u8865\u4e01\u5dee\u5206", "\u6f0f\u6d1e\u8865\u4e01"],
    "protocol-reverse": ["protocol reverse", "packet format", "protobuf", "websocket", "\u534f\u8bae\u9006\u5411", "\u534f\u8bae\u5206\u6790", "\u6570\u636e\u5305\u683c\u5f0f"],
    "pwn-chain": ["pwn", "buffer overflow", "rop", "heap exploit", "shellcode", "\u4e8c\u8fdb\u5236\u5229\u7528", "\u7f13\u51b2\u533a\u6ea2\u51fa", "\u5806\u5229\u7528"],
    "radare2": ["radare2", "r2", "rizin", "radare2\u9006\u5411"],
    "radio-sdr": ["radio", "sdr", "rf", "signal", "\u65e0\u7ebf\u7535\u5b89\u5168", "\u8f6f\u4ef6\u65e0\u7ebf\u7535", "\u4fe1\u53f7\u5206\u6790"],
    "reverse-engineering": ["reverse engineering", "decompile", "disassemble", "binary analysis", "\u9006\u5411\u5de5\u7a0b", "\u53cd\u7f16\u8bd1", "\u53cd\u6c47\u7f16", "\u4e8c\u8fdb\u5236\u5206\u6790"],
    "supply-chain-security": ["supply chain", "dependency confusion", "sbom", "\u4f9b\u5e94\u94fe\u5b89\u5168", "\u4f9d\u8d56\u6df7\u6dc6"],
    "thick-client": ["thick client", "desktop client", "windows client", "\u684c\u9762\u5ba2\u6237\u7aef", "\u80d6\u5ba2\u6237\u7aef"],
    "threat-hunting": ["threat hunting", "ioc", "sigma", "mitre attack", "\u5a01\u80c1\u72e9\u730e", "\u5a01\u80c1\u730e\u6740", "\u6307\u6807\u5206\u6790"],
    "wifi-wireless": ["wifi", "wireless", "802.11", "wpa", "\u65e0\u7ebf\u5b89\u5168", "wifi\u5b89\u5168"],
    "windows-ad": ["active directory", "windows ad", "kerberos", "ldap", "\u57df\u5b89\u5168", "\u6d3b\u52a8\u76ee\u5f55", "\u57df\u6e17\u900f"],
}


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def validate_relative(value: str) -> None:
    path = PurePosixPath(value)
    if not value or "\\" in value or path.is_absolute() or any(part in ("", ".", "..") for part in path.parts):
        raise ValueError(f"unsafe path: {value}")
    if str(path) != value:
        raise ValueError(f"non-canonical path: {value}")


def portable_key(value: str) -> str:
    return "/".join(part.rstrip(" .").casefold() for part in value.split("/"))


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


def rewrite_package_contract(data: bytes) -> bytes:
    clone = b"git clone https://github.com/zhaoxuya520/reverse-skill.git"
    data = data.replace(clone + b"\r\ncd reverse-skill", LOCAL_PACKAGE_CONTRACT_LINE)
    data = data.replace(clone + b"\ncd reverse-skill", LOCAL_PACKAGE_CONTRACT_LINE)
    return data.replace(clone, LOCAL_PACKAGE_CONTRACT_LINE)


def contains_remote_skill_acquisition(data: bytes) -> bool:
    text = data.decode("utf-8").casefold().replace("\r\n", "\n")
    for line in text.split("\n"):
        if "git clone" in line and "github.com/zhaoxuya520/reverse-skill" in line:
            return True
        package_document = any(
            marker in line
            for marker in ("skill.md", "rules.md", "readme_ai.md", "reverse-skill.git", "reverse-skill/zip")
        )
        acquisition = any(
            marker in line
            for marker in ("git clone", "curl ", "wget ", "invoke-webrequest", "download ", "fetch ", "load ")
        )
        remote = any(marker in line for marker in ("http://", "https://", "github", "remote"))
        if package_document and acquisition and remote:
            return True
    return False


def document_urls(data: bytes) -> list[str]:
    return [match.rstrip(b".,;:!?)]}").decode("ascii") for match in HTTP_URL_PATTERN.findall(data)]


def read_source_archive() -> bytes:
    local = os.environ.get(SOURCE_ARCHIVE_ENV, "").strip()
    if local:
        raw = Path(local).read_bytes()
    else:
        request = urllib.request.Request(SOURCE_URL, headers={"User-Agent": "CodexRip-Seed-Builder/3"})
        with urllib.request.urlopen(request, timeout=60) as response:
            final = urllib.parse.urlparse(response.geturl())
            if final.scheme != "https" or final.hostname != "codeload.github.com":
                raise ValueError("upstream ZIP redirected outside the pinned source")
            raw = response.read(MAX_SOURCE_BYTES + 1)
    if not raw or len(raw) > MAX_SOURCE_BYTES or sha256(raw) != SOURCE_ARCHIVE_SHA256:
        raise ValueError("pinned upstream ZIP fingerprint mismatch")
    return raw


def extract_source(raw: bytes) -> dict[str, bytes]:
    files: dict[str, bytes] = {}
    portable: set[str] = set()
    total = 0
    expected_root = f"reverse-skill-{SOURCE_COMMIT}"
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        for info in archive.infolist():
            if "\\" in info.filename or info.filename.startswith("/"):
                raise ValueError("upstream ZIP path is unsafe")
            parts = info.filename.split("/")
            if not parts or parts[0] != expected_root:
                raise ValueError("upstream ZIP root does not match the pinned commit")
            if len(parts) == 1 or info.is_dir():
                continue
            relative = "/".join(parts[1:])
            validate_relative(relative)
            mode = info.external_attr >> 16
            if stat.S_IFMT(mode) not in (0, stat.S_IFREG):
                raise ValueError(f"upstream ZIP contains a non-regular file: {relative}")
            if PurePosixPath(relative).name in EXCLUDED_SOURCE_NAMES:
                continue
            key = portable_key(relative)
            if relative in files or key in portable:
                raise ValueError(f"upstream ZIP contains a portable path collision: {relative}")
            if info.file_size > MAX_FILE_BYTES:
                raise ValueError(f"upstream file exceeds limit: {relative}")
            data = archive.read(info)
            if len(data) != info.file_size:
                raise ValueError(f"upstream file length mismatch: {relative}")
            if file_kind(relative, data) != "binary":
                data = rewrite_package_contract(data)
            files[relative] = data
            portable.add(key)
            total += len(data)
            if len(files) > MAX_FILE_COUNT - len(CLIENT_FILES) or total > MAX_TOTAL_BYTES:
                raise ValueError("upstream package exceeds registry limits")
    if not files:
        raise ValueError("upstream package is empty")
    return files


def verify_native_contract(files: dict[str, bytes]) -> None:
    for name, expected in CORE_SHA256.items():
        raw = files.get(name)
        if raw is None or sha256(raw) != expected:
            raise ValueError(f"pinned upstream core file mismatch: {name}")
    for name, raw in files.items():
        if file_kind(name, raw) == "binary":
            continue
        lowered = raw.lower()
        if any(value.lower() in lowered for value in FORBIDDEN_SKILL_SOURCE):
            raise ValueError(f"upstream runtime document contains a forbidden Skill source: {name}")
        if contains_remote_skill_acquisition(raw):
            raise ValueError(f"upstream runtime document contains remote Skill acquisition: {name}")
    root = CLIENT_FILES["codexrip-client/SKILL.md"]
    lowered = root.lower()
    if any(value.lower() in lowered for value in FORBIDDEN_SKILL_SOURCE):
        raise ValueError("native Skill entry contains a forbidden Skill source")
    if document_urls(root) != ["https://codexrip.vip"]:
        raise ValueError("native Skill acquisition source is not fixed")
    for required in (b"bundle/RULES.md", b"bundle/README_AI.md", b"bundle/skills/SKILL.md"):
        if required not in root:
            raise ValueError("native Skill entry does not resolve the installed bundle")


def stable_unique(values: list[str]) -> list[str]:
    result: list[str] = []
    seen: set[str] = set()
    for value in values:
        normalized = value.strip().casefold()
        if normalized and normalized not in seen:
            seen.add(normalized)
            result.append(value.strip())
    return result


def frontmatter_keywords(route_id: str, raw: bytes) -> list[str]:
    keywords = [route_id, route_id.replace("-", " ")]
    try:
        text = raw.decode("utf-8").replace("\r\n", "\n")
    except UnicodeDecodeError:
        return keywords
    if not text.startswith("---\n") or "\n---\n" not in text[4:]:
        return keywords
    frontmatter = text[4:].split("\n---\n", 1)[0]
    lines = frontmatter.splitlines()
    name = ""
    description = ""
    index = 0
    while index < len(lines):
        line = lines[index]
        if line.startswith("name:"):
            name = line.split(":", 1)[1].strip().strip("\"'")
        elif line.startswith("description:"):
            value = line.split(":", 1)[1].strip()
            if value in ("|", ">", "|-", ">-"):
                block: list[str] = []
                index += 1
                while index < len(lines) and (not lines[index] or lines[index][0].isspace()):
                    block.append(lines[index].strip())
                    index += 1
                description = "\n".join(block)
                continue
            description = value.strip("\"'")
        index += 1
    keywords.append(name)
    for token in re.split(r"[,.;:/\\|()\[\]{}\n\r\t\u3001\u3002\uff0c\uff1b\uff1a]+", description):
        token = token.strip()
        if 2 <= len(token) <= 48:
            keywords.append(token)
    return keywords


def build_routes(files: dict[str, bytes]) -> list[dict]:
    route_ids = sorted({
        parts[1]
        for name in files
        if len(parts := name.split("/")) == 3 and parts[0] == "skills" and parts[2] == "SKILL.md"
    })
    routes = []
    for route_id in route_ids:
        prefix = f"skills/{route_id}/references/"
        references = sorted(
            name for name in files if name.startswith(prefix) and name.lower().endswith(".md")
        )[:8]
        routes.append({
            "id": route_id,
            "keywords": stable_unique([
                *frontmatter_keywords(route_id, files[f"skills/{route_id}/SKILL.md"]),
                *ROUTE_KEYWORDS.get(route_id, []),
            ]),
            "entry": f"skills/{route_id}/SKILL.md",
            "references": references,
        })
    return routes


def build_manifest(files: dict[str, bytes]) -> bytes:
    entries = [
        {
            "path": name,
            "sha256": sha256(files[name]),
            "byte_length": len(files[name]),
            "kind": file_kind(name, files[name]),
            "required": True,
        }
        for name in sorted(files)
    ]
    manifest = {
        "schema_version": 1,
        "bundle_id": BUNDLE_ID,
        "version": f"3.0.0+{SOURCE_COMMIT}+codexrip.3",
        "core_files": CORE_FILES,
        "files": entries,
        "domains": build_routes(files),
    }
    return json.dumps(manifest, ensure_ascii=False, indent=2, separators=(",", ": ")).encode("utf-8")


def build_archive(manifest: bytes, files: dict[str, bytes]) -> bytes:
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_STORED) as archive:
        for name, data in [(MANIFEST_NAME, manifest), *((name, files[name]) for name in sorted(files))]:
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_STORED
            info.external_attr = (stat.S_IFREG | 0o644) << 16
            info.create_system = 3
            archive.writestr(info, data, compress_type=zipfile.ZIP_STORED)
    return output.getvalue()


def hash_client_set(files: dict[str, bytes]) -> str:
    digest = hashlib.sha256()
    for name in sorted(CLIENT_FILES):
        digest.update(name.encode("utf-8"))
        digest.update(b"\0")
        digest.update(sha256(files[name]).encode("ascii"))
        digest.update(b"\n")
    return digest.hexdigest()


def main() -> None:
    files = extract_source(read_source_archive())
    verify_native_contract(files)
    for name, data in CLIENT_FILES.items():
        if name in files or portable_key(name) in {portable_key(value) for value in files}:
            raise ValueError(f"native client path collides with upstream: {name}")
        files[name] = data
    manifest = build_manifest(files)
    manifest_sha = sha256(manifest)
    archive = build_archive(manifest, files)
    archive_sha = sha256(archive)
    total_bytes = sum(len(data) for data in files.values())
    base_url = f"https://codexrip.vip/skills/reverse-skill/versions/{manifest_sha}"
    descriptor = {
        "schema_version": 1,
        "bundle_id": BUNDLE_ID,
        "revision": 1,
        "source_commit": SOURCE_COMMIT,
        "overlay_sha256": hash_client_set(files),
        "manifest_sha256": manifest_sha,
        "archive_sha256": archive_sha,
        "manifest_url": f"{base_url}/{MANIFEST_NAME}",
        "archive_url": f"{base_url}/{BUNDLE_ID}-{manifest_sha}.zip",
        "files_base_url": f"{base_url}/",
        "core_files": CORE_FILES,
        "file_count": len(files),
        "total_bytes": total_bytes,
        "published_at": datetime(2026, 8, 9, tzinfo=timezone.utc).isoformat().replace("+00:00", "Z"),
        "bootstrap_policy": "download_verify_native_skill_atomic_replace",
        "bootstraps": BOOTSTRAPS,
    }

    OUTPUT.mkdir(parents=True, exist_ok=True)
    for stale in OUTPUT.glob(f"{BUNDLE_ID}-*.zip*"):
        if stale.is_file():
            stale.unlink()
    archive_name = f"{BUNDLE_ID}-{manifest_sha}.zip"
    (OUTPUT / MANIFEST_NAME).write_bytes(manifest)
    (OUTPUT / archive_name).write_bytes(archive)
    (OUTPUT / f"{archive_name}.sha256").write_text(f"{archive_sha}  {archive_name}\n", encoding="ascii")
    (OUTPUT / "seed-descriptor.json").write_text(
        json.dumps(descriptor, ensure_ascii=False, indent=2, separators=(",", ": ")),
        encoding="utf-8",
    )
    print(json.dumps({
        "manifest_sha256": manifest_sha,
        "archive_sha256": archive_sha,
        "overlay_sha256": descriptor["overlay_sha256"],
        "file_count": len(files),
        "total_bytes": total_bytes,
    }, separators=(",", ":")))


if __name__ == "__main__":
    main()

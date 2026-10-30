#!/usr/bin/env python3
"""Select checks for actual changed inputs; unclassified executable inputs fail closed."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
from typing import Iterable

ROOT = Path(__file__).resolve().parents[2]
SCHEMA = "ci-plan/v1"

# These are existing contracts, not a growing default regression suite. A rule
# owns the risk of its source input; a new unowned input must add its own rule.
RISK_RULES = (
    (r"backend/internal/(?:service/deepseek_reasoning[^/]*|handler/admin/deepseek_reasoning[^/]*|server/routes/deepseek_reasoning[^/]*)\.go$", ("deepseek",)),
    (r"backend/internal/(?:service|handler)/.*claude.*(?:signature|thinking)[^/]*\.go$", ("claude",)),
    (r"backend/internal/service/pelican_(?:test_runner|execution|repeats|background)[^/]*\.go$", ("pelican_race",)),
    (r"backend/internal/service/account_test_sse_keepalive[^/]*\.go$", ("keepalive_race",)),
    (r"backend/internal/service/openai_account_runtime_block_fastpath[^/]*\.go$", ("probe_race",)),
    (r"backend/internal/handler/openai_management_isolation[^/]*\.go$", ("management_race",)),
    (r"backend/internal/(?:service|handler(?:/admin)?)/.*(?:available_models|models_handler)[^/]*\.go$", ("model_list",)),
    (r"backend/internal/(?:service|handler(?:/admin)?)/.*turn_state[^/]*\.go$", ("turn_state",)),
    (r"backend/internal/(?:pkg/httputil/|handler/request_body)[^/]*\.go$", ("request_body",)),
    (r"backend/internal/(?:service|handler/admin)/account_job[^/]*\.go$", ("account_jobs",)),
    (r"backend/internal/(?:service|repository|handler/admin)/.*(?:account_taxonomy|account_folder)[^/]*\.go$|backend/migrations/192_[^/]*\.sql$", ("taxonomy_db",)),
    (r"backend/migrations/274_[^/]*\.sql$", ("retirement_migration",)),
    (r"backend/migrations/236_[^/]*\.sql$|backend/internal/repository/(?:audit_log|system_log)[^/]*\.go$", ("repository_audit",)),
    (r"backend/internal/pkg/codextransport/[^/]+$|backend/cmd/codex-transport-probe/[^/]+\.go$|\.github/scripts/(?:verify|capture)_codex_transport\.py$", ("transport",)),
    (r"backend/internal/(?:pkg/apicompat/[^/]+\.go|web/(?:agent[^/]*\.go|testdata/agent_cache_fixture\.js))$|\.github/scripts/test_agent_protocol\.sh$", ("agent_protocol",)),
    (r"backend/internal/service/pelican_renderer\.go$", ("renderer",)),
)

# A shared feature prefix is not a test boundary. Only reviewed source inputs
# may select these exact test files; new shared inputs require their own rule.
SOURCE_TEST_FILES = {
    "backend/internal/handler/admin/pelican_gallery_handler.go": (
        "backend/internal/handler/admin/pelican_gallery_handler_test.go",
    ),
    "backend/internal/repository/pelican_deletion_repo.go": (
        "backend/internal/repository/pelican_deletion_repo_integration_test.go",
    ),
    "backend/internal/repository/pelican_background_repo.go": (
        "backend/internal/repository/pelican_background_repo_integration_test.go",
    ),
    "backend/migrations/279_pelican_test_deletion.sql": (
        "backend/internal/repository/pelican_deletion_repo_integration_test.go",
    ),
    "backend/migrations/281_pelican_repeat_samples.sql": (
        "backend/internal/repository/pelican_repeats_repo_integration_test.go",
    ),
}

DEPLOY_RULES = {
    "deploy/apple-container.sh": ["deploy/tests/apple-container-test.sh"],
    "deploy/docker-compose.yml": ["deploy/tests/docker-compose-security-test.sh",
                                   "deploy/tests/docker-compose-gateway-env-test.sh",
                                   "deploy/tests/docker-runtime-resources-test.sh"],
    "deploy/docker-compose.standalone.yml": ["deploy/tests/docker-compose-simple-mode-env-test.sh"],
    "deploy/Caddyfile": ["deploy/test-caddyfile-cache.sh"],
    "deploy/install.sh": ["deploy/tests/install-github-token-test.sh"],
}

# Only reviewed leaf helpers may derive their unit selector from a sibling.
# Shared service/handler/repository entries require the explicit risk rules.
SAFE_LEAF_PACKAGES = {"backend/internal/pkg/requestmodel"}


def git(*args: str, repo: Path = ROOT) -> str:
    result = subprocess.run(["git", "-C", str(repo), *args], stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, check=False)
    if result.returncode:
        raise ValueError("git_metadata_unavailable")
    return result.stdout.decode("utf-8", errors="strict").strip()


def definition_digest(repo: Path = ROOT) -> str:
    """Changes to selection, execution, receipts or workflows invalidate reuse."""
    paths = sorted(set((repo / ".github/scripts").glob("ci_*.py")) |
                   set((repo / ".github/scripts").glob("test_ci_*.py")) |
                   set((repo / ".github/workflows").glob("*.yml")) |
                   set((repo / ".github/actions/selective-ci").glob("*.yml")))
    manifest = repo / ".github/scripts/ci_risks.json"
    if manifest.is_file():
        paths.append(manifest)
    digest = hashlib.sha256()
    for path in paths:
        digest.update(path.relative_to(repo).as_posix().encode() + b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def parse_name_status(raw: bytes) -> list[str]:
    """Read git's NUL format, including both names of a rename/copy."""
    entries = raw.decode("utf-8", errors="strict").split("\0")
    paths: list[str] = []
    cursor = 0
    while cursor < len(entries) and entries[cursor]:
        status = entries[cursor]
        cursor += 1
        if not re.fullmatch(r"[ACDMRTUXB](?:[0-9]+)?", status):
            raise ValueError("invalid_diff_status")
        count = 2 if status[0] in "RC" else 1
        if cursor + count > len(entries) or any(not x for x in entries[cursor:cursor + count]):
            raise ValueError("incomplete_diff")
        paths.extend(entries[cursor:cursor + count])
        cursor += count
    return sorted(set(paths))


def changed_paths(base: str, head: str, repo: Path = ROOT) -> list[str]:
    result = subprocess.run(["git", "-C", str(repo), "diff", "--name-status", "-z",
                             "--find-renames", base, head, "--"], stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, check=False)
    if result.returncode:
        raise ValueError("diff_unavailable")
    return parse_name_status(result.stdout)


def _go_test(path: Path, relative: str) -> dict | None:
    if not path.is_file() or path.is_symlink():
        return None
    content = path.read_text(encoding="utf-8")
    names = re.findall(r"(?m)^func\s+(Test[A-Za-z0-9_]+)\s*\(\s*\w+\s+\*testing\.T\s*\)", content)
    if not names:
        return None
    build = re.search(r"(?m)^//go:build\s+(.+)$", content)
    expression = build.group(1) if build else ""
    tags = [name for name in ("unit", "integration", "e2e")
            if re.search(rf"(?<![\w!]){name}\b", expression)]
    # E2E and nontrivial tag expressions need an explicit reviewed command.
    if "e2e" in tags or (expression and expression not in ("unit", "integration")):
        return None
    return {"packages": ["./" + str(PurePosixPath(relative).parent.relative_to("backend"))],
            "pattern": "^(?:" + "|".join(sorted(set(names))) + ")$", "tags": tags,
            "race": False, "reason": relative}


def direct_importers(packages: Iterable[str], repo: Path = ROOT) -> list[str]:
    """Compile only direct reverse imports to catch an affected package interface."""
    module = re.search(r"(?m)^module\s+(\S+)", (repo / "backend/go.mod").read_text(encoding="utf-8"))
    if module is None:
        raise ValueError("go_module_missing")
    targets = {module.group(1) + "/" + package.removeprefix("./") for package in packages}
    result: set[str] = set()
    for path in (repo / "backend").rglob("*.go"):
        if path.name.endswith("_test.go") or any(part in ("vendor", ".git", "node_modules") for part in path.parts):
            continue
        text = path.read_text(encoding="utf-8")
        imports = set()
        for match in re.finditer(r"(?ms)^import\s+(?:\((.*?)^\)|([^\n]+))", text):
            imports.update(re.findall(r'"([^"\n]+)"', match.group(1) or match.group(2)))
        if imports & targets:
            package = "./" + path.parent.relative_to(repo / "backend").as_posix()
            if package not in packages:
                result.add(package)
    return sorted(result)


def classify_paths(paths: Iterable[str], repo: Path = ROOT) -> dict:
    groups: set[str] = set()
    reasons: dict[str, list[str]] = {}
    failures: list[str] = []
    files = sorted(set(paths))
    tests: list[dict] = []
    packages: set[str] = set()
    source_packages: set[str] = set()
    frontend_files: set[str] = set()
    frontend_tests: set[str] = set()
    frontend_related: set[str] = set()
    static_files: set[str] = set()
    deleted_static_files: set[str] = set()
    deploy_tests: set[str] = set()
    typecheck = False
    backend_dependency_graph = False

    def select(group: str, path: str):
        groups.add(group)
        reasons.setdefault(group, []).append(path)

    for relative in files:
        path = PurePosixPath(relative)
        if path.is_absolute() or ".." in path.parts or "\\" in relative or relative.startswith("-"):
            failures.append("invalid_path:" + relative)
            continue
        disk_path = repo.joinpath(*path.parts)
        if any(item.is_symlink() for item in [disk_path, *list(disk_path.parents)[:len(path.parts)-1]]):
            failures.append("linked_input:" + relative)
            continue
        known = False
        source_tests = SOURCE_TEST_FILES.get(relative, ())
        if source_tests:
            known = True
            for target in source_tests:
                selected = _go_test(repo / target, target)
                if selected is None:
                    failures.append("source_test_selection_requires_mapping:" + relative)
                    known = False
                    continue
                selected["reason"] = relative
                tests.append(selected)
                select("backend_focused", relative)
        if relative.startswith("docs/legal/") and path.suffix == ".md":
            select("static_docs", relative)
            select("frontend_validate", relative)
            select("candidate_image", relative)
            typecheck = True
            (static_files if disk_path.is_file() else deleted_static_files).add(relative)
            known = True
        elif relative in ("THIRD_PARTY_NOTICES.md", "deploy/DOCKER.md"):
            select("static_docs", relative)
            select("candidate_image" if relative == "THIRD_PARTY_NOTICES.md" else "generic_release_contracts", relative)
            (static_files if disk_path.is_file() else deleted_static_files).add(relative)
            known = True
        elif path.suffix == ".md" or relative in ("LICENSE", "CLA.md"):
            select("static_docs", relative)
            (static_files if disk_path.is_file() else deleted_static_files).add(relative)
            known = True
        elif relative.startswith(".github/workflows/") or relative.startswith(".github/actions/selective-ci/") or relative.startswith(".github/scripts/ci_") or relative.startswith(".github/scripts/test_ci_"):
            select("ci_contracts", relative)
            known = True
        elif relative in (".github/scripts/release_preflight.py", ".github/scripts/test_release_preflight.py"):
            select("release_contracts", relative)
            known = True
        elif relative.startswith(".github/scripts/upstream_") or relative.startswith(".github/scripts/test_upstream_") or relative in (".downstream/upstream-base", ".downstream/upstream-risk.json"):
            select("upstream_contracts", relative)
            known = True
        elif relative.startswith(".github/release-tools/") or re.fullmatch(r"\.goreleaser(?:\.[^/]+)?\.ya?ml", relative):
            select("generic_release_contracts", relative)
            known = True
        elif relative in (".github/audit-exceptions.yml", "tools/check_pnpm_audit_exceptions.py"):
            select("frontend_security", relative)
            known = True
        elif relative in ("Dockerfile", ".dockerignore", ".github/buildkitd.toml"):
            select("candidate_image", relative)
            if relative == "Dockerfile":
                select("renderer", relative)
            known = True
        elif relative in DEPLOY_RULES or (relative.startswith("deploy/tests/") and path.suffix == ".sh") or relative == "deploy/test-caddyfile-cache.sh":
            select("deploy_contracts", relative)
            deploy_tests.update(DEPLOY_RULES.get(relative, [relative]))
            known = True
        elif relative in (".gitignore", ".gitattributes", ".editorconfig"):
            select("ci_contracts", relative)
            known = True
        elif relative in ("backend/go.mod", "backend/go.sum"):
            select("backend_security", relative)
            select("candidate_image", relative)
            backend_dependency_graph = True
            known = True
        elif relative in ("frontend/package.json", "frontend/pnpm-lock.yaml"):
            select("frontend_security", relative)
            select("frontend_validate", relative)
            select("candidate_image", relative)
            typecheck = True
            known = True
        elif relative.startswith("frontend/src/") and path.suffix in (".vue", ".ts", ".tsx", ".js", ".jsx"):
            frontend_relative = relative.removeprefix("frontend/")
            if not disk_path.is_file():
                failures.append("removed_frontend_input_needs_mapping:" + relative)
                continue
            frontend_files.add(frontend_relative)
            select("frontend_validate", relative)
            select("frontend_focused", relative)
            typecheck = not bool(re.search(r"\.(?:spec|test)\.[tj]sx?$", relative)) or typecheck
            if re.search(r"\.(?:spec|test)\.[tj]sx?$", relative):
                frontend_tests.add(frontend_relative)
            else:
                frontend_related.add(frontend_relative)
                select("candidate_image", relative)
            known = True

        if relative.startswith("backend/") and path.suffix == ".go":
            package = "./" + str(path.parent.relative_to("backend"))
            if disk_path.parent.is_dir():
                packages.add(package)
            if not relative.endswith("_test.go"):
                source_packages.add(package)
                select("backend_lint", relative)
                select("candidate_image", relative)
            matched = []
            for pattern, risk_groups in RISK_RULES:
                if re.fullmatch(pattern, relative):
                    matched.extend(risk_groups)
            if relative.endswith("_test.go"):
                selected = _go_test(disk_path, relative)
                if selected:
                    selected["race"] = any(group.endswith("_race") for group in matched)
                    tests.append(selected)
                    select("backend_focused", relative)
                    known = True
                else:
                    failures.append("test_selection_requires_mapping:" + relative)
                    continue
            elif matched:
                for group in matched:
                    select(group, relative)
                # A shared Pelican runner also owns ordinary behavior; a race
                # marker alone cannot claim that behavior has been validated.
                if not (relative.startswith("backend/internal/service/pelican_") and
                        all(group.endswith("_race") for group in matched)):
                    known = True
            else:
                target = relative if relative.endswith("_test.go") else relative[:-3] + "_test.go"
                selected = _go_test(repo / target, target)
                if selected and (relative.endswith("_test.go") or
                                 (str(path.parent) in SAFE_LEAF_PACKAGES and "integration" not in selected["tags"])):
                    tests.append(selected)
                    select("backend_focused", relative)
                    known = True
        else:
            for pattern, risk_groups in RISK_RULES:
                if re.fullmatch(pattern, relative):
                    for group in risk_groups:
                        select(group, relative)
                    known = True
        if known and (relative == "backend/internal/pkg/codextransport/reference.json" or
                      (relative.startswith("backend/migrations/") and path.suffix == ".sql")):
            select("candidate_image", relative)
        if known and relative.startswith("backend/") and path.suffix == ".go" and not relative.endswith("_test.go"):
            select("backend_security", relative)
        if not known:
            failures.append("unclassified_input:" + relative)

    for test_path in deploy_tests:
        if not (repo / test_path).is_file():
            failures.append("missing_deploy_test:" + test_path)
    unique_tests = {json.dumps(test, sort_keys=True): test for test in tests}
    compile_packages = direct_importers(source_packages, repo) if source_packages else []
    if compile_packages:
        for package in sorted(source_packages):
            select("backend_compile", "direct-importers-of:" + package)
    return {"files": files, "groups": sorted(groups),
            "reasons": {key: sorted(set(value)) for key, value in sorted(reasons.items())},
            "backend_tests": list(unique_tests.values()),
            "backend_packages": ["./..."] if backend_dependency_graph else sorted(packages),
            "backend_lint_packages": sorted(source_packages),
            "backend_dependency_graph": backend_dependency_graph,
            "backend_compile_packages": compile_packages, "deploy_tests": sorted(deploy_tests),
            "frontend_files": sorted(frontend_files), "frontend_tests": sorted(frontend_tests),
            "frontend_related": sorted(frontend_related), "frontend_typecheck": typecheck,
            "static_files": sorted(static_files), "deleted_static_files": sorted(deleted_static_files),
            "failures": failures, "passed": not failures}


def make_plan(base: str, head: str, repo: Path = ROOT) -> dict:
    if not re.fullmatch(r"[0-9a-f]{40}", base) or not re.fullmatch(r"[0-9a-f]{40}", head):
        raise ValueError("full_commit_sha_required")
    if git("rev-parse", "--verify", base + "^{commit}", repo=repo) != base:
        raise ValueError("base_commit_mismatch")
    if git("rev-parse", "--verify", head + "^{commit}", repo=repo) != head:
        raise ValueError("head_commit_mismatch")
    tested = git("rev-parse", "HEAD", repo=repo)
    if tested != head:
        raise ValueError("checkout_does_not_match_tested_head")
    return {"schema": SCHEMA, "base": base, "head": head, "tested_sha": tested,
            "tree_sha": git("rev-parse", head + "^{tree}", repo=repo),
            "definition_sha256": definition_digest(repo),
            **classify_paths(changed_paths(base, head, repo), repo)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        plan = make_plan(args.base, args.head)
    except (ValueError, UnicodeError, OSError):
        plan = {"schema": SCHEMA, "passed": False, "groups": [], "failures": ["plan_metadata_invalid"]}
    payload = json.dumps(plan, ensure_ascii=False, sort_keys=True, indent=2) + "\n"
    if args.output:
        args.output.write_text(payload, encoding="utf-8", newline="\n")
    print(payload, end="")
    raise SystemExit(0 if plan["passed"] else 1)


if __name__ == "__main__":
    main()

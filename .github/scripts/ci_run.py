#!/usr/bin/env python3
"""Run each selected risk once, retaining the existing required check contexts."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from urllib.parse import unquote

import ci_plan
import ci_receipts

ROOT = Path(__file__).resolve().parents[2]
OWNERS = {
    "shell": {"deploy_contracts"},
    "test": {"backend_focused", "backend_compile"},
    "frontend": {"frontend_validate"},
    "golangci-lint": {"backend_lint"},
    "release-helpers": {"generic_release_contracts"},
    "Downstream backend": {
        "static_docs", "ci_contracts", "release_contracts", "upstream_contracts",
        "transport", "agent_protocol", "deepseek", "claude",
        "pelican_race", "keepalive_race", "probe_race", "management_race",
        "request_body", "account_jobs", "taxonomy_db", "retirement_migration", "repository_audit",
        "model_list", "turn_state",
    },
    "Downstream frontend": {"frontend_focused"},
    "Candidate OCI image": {"candidate_image", "renderer"},
    "backend-security": {"backend_security"},
    "frontend-security": {"frontend_security"},
}
EXTERNAL = {"backend_lint", "candidate_image", "renderer"}


def write_json(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n", encoding="utf-8")


def read_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def git(*args: str) -> str:
    return ci_plan.git(*args)


def event_range(event: dict, name: str, tested: str) -> tuple[str, str, str]:
    if name == "pull_request":
        pr = event["pull_request"]
        return pr["base"]["sha"], pr["base"]["sha"], pr["head"]["sha"]
    if name == "schedule":
        return tested, tested, tested
    if name == "push":
        before = event.get("before", "")
        if not re.fullmatch(r"[0-9a-f]{40}", before) or before == "0" * 40:
            raise ValueError("push_base_unavailable")
        return before, before, tested
    if name == "workflow_dispatch":
        if os.environ.get("GITHUB_REF") == "refs/heads/main":
            base = git("rev-parse", tested + "^")
        else:
            base = git("merge-base", "origin/main", tested)
        return base, base, tested
    raise ValueError("unsupported_event")


def selected_groups(plan: dict, context: str) -> list[str]:
    if context not in OWNERS:
        raise ValueError("unknown_context")
    known = set().union(*OWNERS.values())
    unknown = set(plan["groups"]) - known
    if unknown:
        raise ValueError("unowned_check_group:" + ",".join(sorted(unknown)))
    return sorted(set(plan["groups"]) & OWNERS[context])


def prepare(context: str, workflow: str, directory: Path) -> dict:
    tested = git("rev-parse", "HEAD")
    event = read_json(Path(os.environ["GITHUB_EVENT_PATH"]))
    name = os.environ["GITHUB_EVENT_NAME"]
    base, source_base, source_head = event_range(event, name, tested)
    plan = ci_plan.make_plan(base, tested)
    plan.update(source_base_sha=source_base, source_head_sha=source_head, event_name=name)
    environment = {key: os.environ.get(key, "") for key in
                   ("RUNNER_OS", "RUNNER_ARCH", "ImageOS", "ImageVersion")}
    environment.update(python=list(sys.version_info[:3]), expected_node="20", expected_pnpm="9.15.9",
                       expected_go=re.search(r"(?m)^go (\S+)", (ROOT / "backend/go.mod").read_text()).group(1))
    plan["environment_sha256"] = hashlib.sha256(json.dumps(environment, sort_keys=True).encode()).hexdigest()
    if name == "schedule":
        plan["groups"] = ["backend_security", "frontend_security"]
    if not plan["passed"]:
        write_json(directory / "plan.json", plan)
        raise ValueError("unclassified_changes:" + ",".join(plan["failures"]))
    resolve_go_checks(plan)
    plan["groups"] = selected_groups(plan, context)
    probe = ci_receipts.probe(plan, os.environ.get("GITHUB_REPOSITORY", ""), context, workflow,
                              current_run=os.environ.get("GITHUB_RUN_ID", ""), event=name)
    pending = sorted(set(plan["groups"]) - set(probe["reused_groups"]))
    write_json(directory / "plan.json", plan)
    write_json(directory / "execution.json", {"context": context, "workflow": workflow,
                                               "pending": pending, "probe": probe})
    outputs = {
        "needs_go": any(x in pending for x in OWNERS["Downstream backend"] -
                         {"static_docs", "ci_contracts", "release_contracts", "upstream_contracts"})
                    or bool(set(pending) & {"backend_focused", "backend_compile", "backend_lint", "backend_security", "renderer"}),
        "needs_node": bool(set(pending) & {"frontend_focused", "frontend_validate", "frontend_security"}),
        "needs_lint": "backend_lint" in pending,
        "needs_candidate": "candidate_image" in pending,
        "needs_renderer": "renderer" in pending,
        "lint_args": "--timeout=30m " + " ".join(plan.get("backend_lint_packages", [])),
    }
    if outputs["needs_renderer"] and not outputs["needs_candidate"]:
        # Renderer execution requires the tested candidate image in this job.
        pending = sorted(set(pending) | {"candidate_image"})
        plan["groups"] = sorted(set(plan["groups"]) | {"candidate_image"})
        outputs["needs_candidate"] = True
        write_json(directory / "plan.json", plan)
        write_json(directory / "execution.json", {"context": context, "workflow": workflow,
                                                   "pending": pending, "probe": probe})
    if outputs["needs_lint"] and not plan.get("backend_lint_packages"):
        raise ValueError("lint_packages_missing")
    if "GITHUB_OUTPUT" in os.environ:
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as handle:
            for key, value in outputs.items():
                handle.write(f"{key}={str(value).lower() if isinstance(value, bool) else value}\n")
    return outputs


def command(arguments: list[str], cwd: str = "", environment: dict | None = None) -> None:
    subprocess.run(arguments, cwd=ROOT / cwd, env=environment, check=True)


def go_test(packages: list[str], pattern: str, tags=(), race: bool = False) -> list[str]:
    if not packages or not pattern:
        raise ValueError("empty_go_selection")
    arguments = ["go", "test", "-p=1"]
    if tags:
        arguments += ["-tags", ",".join(tags)]
    if race:
        arguments.append("-race")
    return arguments + packages + ["-run", pattern, "-count=1"]


GO_GROUPS = {
    "deepseek": (["./internal/service", "./internal/handler/admin", "./internal/server/routes"], "^TestDeepSeekDefault", ("unit",), False),
    "claude": (["./internal/service", "./internal/handler"], "^TestClaude(ThinkingSignatureError|Signature|OpsErrorLogger)|^TestGatewayService_Forward_(PreOutputSSE|PostOutputSSE)", ("unit",), False),
    "pelican_race": (["./internal/service"], "^TestPelican(RunnerCancellationStopsQueuedAndInflightAndRetainsRawOutput|RepeatModel.*|AccountWaitReleases.*)$", ("unit",), True),
    "keepalive_race": (["./internal/service"], "^TestAccountTestKeepalive", ("unit",), True),
    "probe_race": (["./internal/service"], "TestOpenAIRuntimeBreaker_(APIKeySameAccountRetryDoesNotUseRedisLease|OAuthSameAccountRetryTransfersProbeUntilFinalFailure|SuppressedRetryReleasesSelectionClaim)", ("unit",), True),
    "management_race": (["./internal/handler"], "^TestOpenAIBlockedUpstreamsDoNotSerializeManagementPlane$", (), True),
    "model_list": (["./internal/service", "./internal/handler", "./internal/handler/admin"], "^Test.*(GatewayModels|WriteOpenAIModelsList|AccountHandlerGetAvailableModels|AccountAvailableModels|FetchOpenAIAccountModels)", ("unit",), False),
    "turn_state": (["./internal/service"], "^Test.*TurnState", ("unit",), False),
    "request_body": (["./internal/pkg/httputil", "./internal/handler"], "Test(ReadRequestBodyWithPrealloc|ReadLenientJSONRequestBodyWithPrealloc|RequestBodyReadDiagnostics|ClassifyRequestContentEncoding)", (), False),
    "account_jobs": (["./internal/service", "./internal/handler/admin"], "^TestAccountJob", ("unit",), False),
    "taxonomy_db": (["./internal/repository"], "^Test(AccountTaxonomy.*|DeleteAccountFolder.*)Integration$", ("integration",), False),
    "retirement_migration": (["./migrations"], "^TestMigration274RemovesReleaseAcceptanceKeysSafely$", ("integration",), False),
    "repository_audit": (["./internal/repository"], "^Test(PGXBatchInsertAuditAndSystemLogs|Migration236.*)$", ("integration",), False),
}


def resolve_go_checks(plan: dict) -> None:
    """Expand fixed selections to real functions and run overlapping race cases once."""
    descriptions = {}
    for group in plan["groups"]:
        if group in GO_GROUPS:
            packages, pattern, tags, race = GO_GROUPS[group]
            descriptions[group] = [{"packages": packages, "pattern": pattern, "tags": list(tags), "race": race}]
        elif group == "backend_focused":
            descriptions[group] = plan.get("backend_tests", [])
        elif group == "agent_protocol":
            descriptions[group] = [
                {"packages": ["./internal/web"], "pattern": "Test(Agent|FrontendServer_Middleware|ServeEmbeddedFrontend|OverrideFilesNeverReceiveImmutableCacheHeaders)", "tags": ["unit", "embed"]},
                {"packages": ["./internal/pkg/apicompat"], "pattern": "Test(Agent|Stream_|.*Chat.*Responses|.*Responses.*Chat|ResponsesAgentMessageCapability)", "tags": []},
                {"packages": ["./internal/service", "./internal/handler"], "pattern": "Test(Agent|ForwardResponses_.*(Chat|Reasoning)|ForwardAsAnthropic_ForceChatCompletions|ForwardAsRawChatCompletions|ForwardAsChatCompletionsForGrokStreaming|OpenAIRawStreamTerminalState|ResponsesAgentMessageRoutingPreservesChatEligibility|.*NoAccount|.*SelectionFailure)", "tags": ["unit"]},
            ]
    checks, assigned = {}, set()
    # A race execution covers the same function in the same build configuration.
    ordered = sorted(descriptions, key=lambda group: (not any(x.get("race") for x in descriptions[group]),
                                                     group != "backend_focused", group))
    for group in ordered:
        selected = []
        for description in descriptions[group]:
            regex, tags = re.compile(description["pattern"]), description.get("tags", [])
            found = False
            for package in description["packages"]:
                names = set()
                directory = ROOT / "backend" / package.removeprefix("./")
                for path in directory.glob("*_test.go"):
                    content = path.read_text(encoding="utf-8")
                    build = re.search(r"(?m)^//go:build\s+(.+)$", content)
                    if build:
                        expression = build.group(1).strip()
                        if expression in ("unit", "integration", "e2e", "embed") and expression not in tags:
                            continue
                        if expression.startswith("!") and expression[1:] in tags:
                            continue
                        if expression not in ("unit", "integration", "e2e", "embed", "!unit"):
                            if any(regex.search(name) for name in re.findall(r"(?m)^func\s+(Test[A-Za-z0-9_]+)\s*\(", content)):
                                raise ValueError("mapped_test_build_tags_require_review:" + group)
                            continue
                    for name in re.findall(r"(?m)^func\s+(Test[A-Za-z0-9_]+)\s*\(", content):
                        if name != "TestMain" and regex.search(name):
                            found = True
                            identity = (package, name, tuple(sorted(tags)))
                            if identity not in assigned:
                                assigned.add(identity)
                                names.add(name)
                if names:
                    selected.append({"packages": [package], "pattern": "^(?:" + "|".join(sorted(names)) + ")$",
                                     "tags": tags, "race": description.get("race", False)})
            if not found:
                raise ValueError("mapped_test_pattern_matches_no_functions:" + group)
        if selected:
            checks[group] = selected
        else:
            plan["groups"].remove(group)
            plan.setdefault("covered_by_other_groups", []).append(group)
    plan["go_checks"] = checks


def markdown_structure(text: str) -> tuple[int, bool]:
    """Count document titles outside correctly matched Markdown code fences."""
    headings = 0
    fence = ""
    for line in text.splitlines():
        if fence:
            if re.fullmatch(r" {0,3}" + re.escape(fence[0]) + "{" + str(len(fence)) + r",}[ \t]*", line):
                fence = ""
            continue
        opening = re.match(r"^ {0,3}(`{3,}|~{3,})(.*)$", line)
        if opening and (opening[1][0] != "`" or "`" not in opening[2]):
            fence = opening[1]
            continue
        if re.match(r"^ {0,3}#(?:[ \t]+|$)", line):
            headings += 1
    return headings, not fence


def static_docs(plan: dict) -> None:
    errors = []
    for relative in plan.get("static_files", []):
        path = ROOT / relative
        raw = path.read_bytes()
        text = raw.decode("utf-8")
        if raw.startswith(b"\xef\xbb\xbf") or "\r" in text or (text and not text.endswith("\n")) or re.search(r"[ \t]+\n", text):
            errors.append(relative + ":format")
        if path.suffix != ".md":
            continue
        headings, closed_fences = markdown_structure(text)
        if headings != 1 or not closed_fences:
            errors.append(relative + ":headings_or_fences")
        for target in re.findall(r"\[[^\]]*\]\(([^)]+)\)", text):
            target = target.strip("<>")
            if re.match(r"(?:[a-z][a-z0-9+.-]*:|#|//)", target, re.I):
                continue
            target = unquote(re.split(r"[?#]", target, maxsplit=1)[0])
            if target and not (path.parent / target).exists():
                errors.append(relative + ":missing_local_link")
    if plan.get("deleted_static_files"):
        for relative in git("ls-files", "*.md").splitlines():
            path = ROOT / relative
            if not path.is_file():
                continue
            for target in re.findall(r"\[[^\]]*\]\(([^)]+)\)", path.read_text(encoding="utf-8")):
                target = unquote(re.split(r"[?#]", target.strip("<>"), maxsplit=1)[0])
                if re.match(r"(?:[a-z][a-z0-9+.-]*:|//)", target, re.I):
                    continue
                if any((path.parent / target).resolve() == (ROOT / deleted).resolve()
                       for deleted in plan["deleted_static_files"]):
                    errors.append(relative + ":link_to_removed_document")
    if errors:
        raise ValueError(",".join(sorted(set(errors))))


def run_group(group: str, plan: dict) -> None:
    if group == "static_docs":
        static_docs(plan)
    elif group == "ci_contracts":
        for test in ("test_ci_plan.py", "test_ci_receipts.py", "test_ci_run.py", "test_ci_workflows.py"):
            command([sys.executable, "-B", ".github/scripts/" + test])
    elif group == "release_contracts":
        command([sys.executable, "-B", ".github/scripts/test_release_preflight.py"])
    elif group == "upstream_contracts":
        command([sys.executable, "-B", ".github/scripts/test_upstream_risk.py"])
        command([sys.executable, "-B", ".github/scripts/test_upstream_automation_contract.py"])
    elif group == "deploy_contracts":
        for test in plan.get("deploy_tests", []):
            command(["bash", test])
        if not plan.get("deploy_tests"):
            raise ValueError("deployment_test_selection_empty")
    elif group == "backend_focused" or group in GO_GROUPS or group == "agent_protocol":
        tests = plan.get("go_checks", {}).get(group, [])
        if not tests:
            raise ValueError("backend_test_selection_empty")
        if group == "agent_protocol":
            destination = ROOT / "backend/internal/web/dist"
            destination.mkdir(parents=True, exist_ok=True)
            for source, name in (("frontend/index.html", "index.html"), ("frontend/public/logo.svg", "logo.svg")):
                if not (destination / name).exists():
                    shutil.copyfile(ROOT / source, destination / name)
            if not (destination / "assets").is_dir():
                (destination / "assets").mkdir()
                source = ROOT / "backend/internal/web/testdata/agent_cache_fixture.js"
                suffix = hashlib.sha256(source.read_bytes()).hexdigest()[:8]
                shutil.copyfile(source, destination / "assets" / f"agent-{suffix}.js")
        for item in tests:
            command(go_test(item["packages"], item["pattern"], item.get("tags", []), item.get("race", False)), "backend")
    elif group == "backend_compile":
        if not plan.get("backend_compile_packages"):
            raise ValueError("backend_compile_selection_empty")
        command(["go", "test", *plan["backend_compile_packages"], "-run", "^$"], "backend")
    elif group == "transport":
        command([sys.executable, "-m", "pip", "install", "cryptography==48.0.1", "h2==4.3.0"])
        probe = str(Path(os.environ["RUNNER_TEMP"]) / "codex-transport-probe")
        command(["go", "-C", "backend", "build", "-o", probe, "./cmd/codex-transport-probe"])
        command([sys.executable, ".github/scripts/verify_codex_transport.py", "--probe", probe,
                 "--reference", "backend/internal/pkg/codextransport/reference.json",
                 "--output", str(Path(os.environ["RUNNER_TEMP"]) / "codex-wire.json")])
    elif group == "frontend_validate":
        files = [path for path in plan.get("frontend_files", []) if (ROOT / "frontend" / path).is_file()]
        if files:
            command(["pnpm", "exec", "eslint", *files], "frontend")
        if plan.get("frontend_typecheck"):
            command(["pnpm", "run", "typecheck"], "frontend")
    elif group == "frontend_focused":
        # Vitest includes a directly named spec as well as importers of source.
        files = sorted(set(plan.get("frontend_related", []) + plan.get("frontend_tests", [])))
        if not files:
            raise ValueError("frontend_test_selection_empty")
        command(["pnpm", "exec", "vitest", "related", "--run", *files], "frontend")
    elif group == "backend_security":
        command(["go", "install", "golang.org/x/vuln/cmd/govulncheck@latest"], "backend")
        packages = ["./..."] if plan.get("event_name") == "schedule" else plan.get("backend_packages", [])
        if not packages:
            raise ValueError("security_package_selection_empty")
        command(["govulncheck", *packages], "backend")
    elif group == "frontend_security":
        result = subprocess.run(["pnpm", "audit", "--prod", "--audit-level=high", "--json"],
                                cwd=ROOT / "frontend", stdout=subprocess.PIPE, check=False)
        (ROOT / "frontend/audit.json").write_bytes(result.stdout)
        command([sys.executable, "tools/check_pnpm_audit_exceptions.py", "--audit", "frontend/audit.json",
                 "--exceptions", ".github/audit-exceptions.yml"])
    elif group == "generic_release_contracts":
        command([sys.executable, "-m", "pip", "install", "-r", ".github/release-tools/requirements-release.txt"])
        command([sys.executable, "-m", "unittest", "discover", "-s", ".github/release-tools", "-p", "test_release_matrix.py"])
        command(["bash", "-n", ".github/release-tools/release-images.sh"])
    else:
        raise ValueError("unimplemented_check_group:" + group)


def run(directory: Path) -> None:
    plan, execution = read_json(directory / "plan.json"), read_json(directory / "execution.json")
    results = {"groups": {}}
    pending = set(execution["pending"])
    if any("integration" in item.get("tags", []) for group in pending
           for item in plan.get("go_checks", {}).get(group, [])):
        command([sys.executable, ".github/scripts/prepare_test_images.py"])
    for group in plan["groups"]:
        if group not in pending:
            results["groups"][group] = {"state": "reused", "success": True,
                                          "source_url": execution["probe"]["sources"][group]}
            continue
        if group in EXTERNAL:
            continue
        try:
            run_group(group, plan)
            results["groups"][group] = {"state": "executed", "success": True}
        except (ValueError, OSError, subprocess.CalledProcessError):
            results["groups"][group] = {"state": "failed", "success": False}
            write_json(directory / "results.json", results)
            raise
        write_json(directory / "results.json", results)
    write_json(directory / "results.json", results)


def finish(directory: Path) -> None:
    plan, execution = read_json(directory / "plan.json"), read_json(directory / "execution.json")
    path = directory / "results.json"
    results = read_json(path) if path.is_file() else {"groups": {}}
    failed = False
    for group in plan["groups"]:
        if group in execution["pending"] and group in EXTERNAL:
            outcome = os.environ.get({"backend_lint": "LINT_OUTCOME", "candidate_image": "BUILD_OUTCOME",
                                      "renderer": "RENDER_OUTCOME"}[group])
            results["groups"][group] = {"state": "executed" if outcome == "success" else "failed",
                                          "success": outcome == "success"}
        if group not in results["groups"]:
            results["groups"][group] = {"state": "failed", "success": False}
        failed |= not results["groups"][group]["success"]
    write_json(path, results)
    lines = [f"### {execution['context']}", ""]
    if not plan["groups"]:
        lines.append("not_applicable: no changed input requires this context; no tests executed.")
    for group, value in results["groups"].items():
        link = " " + value["source_url"] if value.get("source_url") else ""
        lines.append(f"- {group}: {value['state']}{link}")
    print("\n".join(lines))
    if "GITHUB_STEP_SUMMARY" in os.environ:
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as handle:
            handle.write("\n".join(lines) + "\n")
    if failed:
        raise ValueError("selected_checks_failed_or_incomplete")
    receipt = ci_receipts.record(plan, results, os.environ.get("GITHUB_REPOSITORY", ""),
                                 execution["context"], execution["workflow"])
    write_json(directory / "receipt.json", receipt)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "run", "finish"))
    parser.add_argument("--directory", required=True, type=Path)
    parser.add_argument("--context")
    parser.add_argument("--workflow")
    options = parser.parse_args()
    try:
        if options.mode == "prepare":
            prepare(options.context, options.workflow, options.directory)
        elif options.mode == "run":
            run(options.directory)
        else:
            finish(options.directory)
        return 0
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError) as error:
        # Commands retain their normal diagnostic output; metadata errors expose
        # only our bounded identifiers, never full GitHub responses or secrets.
        reason = str(error) if isinstance(error, ValueError) else "ci_step_failed"
        print(json.dumps({"state": "failed", "reason": reason}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

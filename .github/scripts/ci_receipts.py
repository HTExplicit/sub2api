#!/usr/bin/env python3
"""Reuse only independently verified successful GitHub Actions test receipts."""
from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import zipfile


SCHEMA = "ci-receipt/v1"
WORKFLOWS = {
    ".github/workflows/backend-ci.yml": {"shell", "test", "frontend", "golangci-lint", "release-helpers"},
    ".github/workflows/downstream-verify.yml": {"Downstream backend", "Downstream frontend", "Candidate OCI image"},
    ".github/workflows/security-scan.yml": {"backend-security", "frontend-security"},
}
SHA = re.compile(r"[0-9a-f]{40}\Z")
HASH = re.compile(r"[0-9a-f]{64}\Z")
MAX_RECEIPT_BYTES = 256 * 1024


class Unavailable(RuntimeError):
    """An existing result could not be verified; selected checks must execute."""


def invoke(args: list[str]) -> bytes:
    try:
        result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                check=False, timeout=60)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise Unavailable("metadata_unavailable") from error
    if result.returncode:
        # Command output can contain credentials or upstream error bodies.
        raise Unavailable("metadata_unavailable")
    return result.stdout


def git(*args: str) -> str:
    try:
        return invoke(["git", *args]).decode("utf-8").strip()
    except (Unavailable, UnicodeError) as error:
        raise Unavailable("source_unavailable") from error


def api(repository: str, path: str):
    try:
        return json.loads(invoke(["gh", "api", "--hostname", "github.com", f"repos/{repository}/{path}"]))
    except (ValueError, UnicodeError) as error:
        raise Unavailable("metadata_invalid") from error


def valid_request(repository: str, context: str, workflow: str) -> bool:
    return bool(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository)
                and context in WORKFLOWS.get(workflow, set()))


def group_ids(plan: dict) -> set[str]:
    groups = plan.get("groups", [])
    if isinstance(groups, dict):
        groups = list(groups)
    if not isinstance(groups, list) or any(not isinstance(group, str) for group in groups):
        raise Unavailable("plan_groups_invalid")
    return set(groups)


def selection_digest(plan: dict) -> str:
    """Bind a receipt to concrete targets, including base-dependent selection.

    A matching source tree does not imply an identical diff: two bases may
    select different tests under the same group name. Commit identities stay
    outside this digest so an equivalent merge can still reuse exact targets.
    """
    groups = sorted(group_ids(plan))
    selection = {"groups": groups}
    for field in ("backend_tests", "backend_packages", "backend_lint_packages", "backend_compile_packages",
                  "go_checks", "covered_by_other_groups",
                  "frontend_files", "frontend_tests", "frontend_related",
                  "static_files", "deleted_static_files", "deploy_tests"):
        selection[field] = plan.get(field, [])
    for field in ("frontend_typecheck", "typecheck", "backend_dependency_graph"):
        selection[field] = plan.get(field, False)
    reasons = plan.get("reasons", {})
    if not isinstance(reasons, dict):
        raise Unavailable("plan_reasons_invalid")
    selection["reasons"] = {group: reasons.get(group, []) for group in groups}
    try:
        payload = json.dumps(selection, ensure_ascii=True, sort_keys=True,
                             separators=(",", ":"), allow_nan=False).encode("utf-8")
    except (ValueError, TypeError) as error:
        raise Unavailable("plan_selection_invalid") from error
    return hashlib.sha256(payload).hexdigest()


def plan_identity(plan: dict) -> tuple[str, str, str]:
    tested = plan.get("tested_sha") or plan.get("head")
    tree, definition = plan.get("tree_sha"), plan.get("definition_sha256")
    environment = plan.get("environment_sha256")
    if (plan.get("schema") != "ci-plan/v1"
            or not isinstance(tested, str) or not SHA.fullmatch(tested)
            or not isinstance(tree, str) or not SHA.fullmatch(tree)
            or not isinstance(definition, str) or not HASH.fullmatch(definition)
            or not isinstance(environment, str) or not HASH.fullmatch(environment)
            or plan.get("passed") is False or plan.get("failures")):
        raise Unavailable("plan_identity_invalid")
    return tested, tree, definition


def git_identity(tested: str, *, fetch: bool = False) -> tuple[str, list[str]]:
    if not SHA.fullmatch(tested):
        raise Unavailable("source_identity_invalid")
    try:
        tree = git("rev-parse", "--verify", f"{tested}^{{tree}}")
    except Unavailable:
        if not fetch:
            raise
        # Fetch one exact object using the checkout's existing Git credentials.
        git("fetch", "--no-tags", "--depth=1", "origin", tested)
        tree = git("rev-parse", "--verify", f"{tested}^{{tree}}")
    # cat-file retains actual parent headers even after a shallow object fetch.
    headers = git("cat-file", "-p", tested).split("\n\n", 1)[0].splitlines()
    parents = [line[7:] for line in headers if line.startswith("parent ")]
    if not SHA.fullmatch(tree):
        raise Unavailable("source_identity_invalid")
    return tree, parents


def source_pair(plan: dict) -> tuple[str, str]:
    return (plan.get("source_base_sha") or plan.get("pr_base_sha") or plan.get("base", ""),
            plan.get("source_head_sha") or plan.get("pr_head_sha") or plan.get("head", ""))


def verify_checkout(identity: dict, *, fetch: bool = False) -> None:
    tested = identity.get("tested_sha", "")
    tree, parents = git_identity(tested, fetch=fetch)
    if tree != identity.get("tree_sha"):
        raise Unavailable("source_tree_mismatch")
    recorded = identity.get("parents")
    if recorded is not None and recorded != parents:
        raise Unavailable("source_parents_mismatch")
    if identity.get("event") == "pull_request":
        base, head = source_pair(identity)
        if not SHA.fullmatch(base) or not SHA.fullmatch(head) or parents != [base, head]:
            raise Unavailable("pr_merge_unverified")


def eligible_run(run: dict, repository: str, workflow: str, current_run: str = "") -> bool:
    return (str(run.get("id", "")) != str(current_run)
            and isinstance(run.get("id"), int)
            and run.get("status") == "completed" and run.get("conclusion") == "success"
            and run.get("path", "").split("@", 1)[0] == workflow
            and run.get("repository", {}).get("full_name") == repository
            and run.get("head_repository", {}).get("full_name") == repository
            and run.get("event") in {"push", "pull_request", "workflow_dispatch"}
            and SHA.fullmatch(run.get("head_sha", "")) is not None)


def eligible_job(jobs: dict, context: str) -> bool:
    matching = [job for job in jobs.get("jobs", []) if job.get("name") == context]
    return (len(matching) == 1 and matching[0].get("status") == "completed"
            and matching[0].get("conclusion") == "success")


def read_artifact(repository: str, artifact: dict) -> dict:
    if (not isinstance(artifact.get("id"), int) or artifact.get("expired") is not False
            or not isinstance(artifact.get("size_in_bytes"), int)
            or not 0 < artifact["size_in_bytes"] <= MAX_RECEIPT_BYTES):
        raise Unavailable("artifact_invalid")
    archive = invoke(["gh", "api", "--hostname", "github.com",
                      f"repos/{repository}/actions/artifacts/{artifact['id']}/zip"])
    if len(archive) > MAX_RECEIPT_BYTES:
        raise Unavailable("artifact_too_large")
    try:
        with zipfile.ZipFile(io.BytesIO(archive)) as stream:
            files = [item for item in stream.infolist() if not item.is_dir()]
            if (len(files) != 1 or not files[0].filename.endswith(".json")
                    or files[0].file_size > MAX_RECEIPT_BYTES):
                raise Unavailable("artifact_invalid")
            receipt = json.loads(stream.read(files[0]))
        if not isinstance(receipt, dict):
            raise Unavailable("artifact_invalid")
        return receipt
    except (ValueError, zipfile.BadZipFile, RuntimeError, OSError) as error:
        raise Unavailable("artifact_invalid") from error


def receipt_groups(receipt: dict, run: dict, plan: dict, repository: str,
                   context: str, workflow: str) -> set[str]:
    _, tree, definition = plan_identity(plan)
    if (receipt.get("schema") != SCHEMA or receipt.get("repository") != repository
            or receipt.get("workflow") != workflow or receipt.get("context") != context
            or receipt.get("run_id") != run.get("id")
            or receipt.get("run_attempt") != run.get("run_attempt", 1)
            or receipt.get("event") != run.get("event")
            or receipt.get("tree_sha") != tree or receipt.get("definition_sha256") != definition
            or receipt.get("environment_sha256") != plan["environment_sha256"]
            or receipt.get("selection_sha256") != selection_digest(plan)):
        return set()
    # The Actions API reports the PR head whereas GITHUB_SHA/checkout normally
    # identify its synthetic merge commit. Both must refer to this same pair.
    expected_heads = {receipt.get("tested_sha")}
    if receipt.get("event") == "pull_request":
        expected_heads.add(source_pair(receipt)[1])
    if (receipt.get("run_head_sha") != receipt.get("tested_sha")
            or run.get("head_sha") not in expected_heads):
        return set()
    verify_checkout(receipt, fetch=True)
    results = receipt.get("groups", {})
    if not isinstance(results, dict):
        return set()
    # Only original executions can be forwarded. A reused run keeps its source
    # URL for review but cannot create an indefinitely growing receipt chain.
    successes = {group for group, result in results.items()
                 if isinstance(group, str) and isinstance(result, dict)
                 and result.get("state") == "executed" and result.get("success") is True}
    return successes & group_ids(plan)


def probe(plan: dict, repository: str, context: str, workflow: str,
          *, current_run: str = "", event: str = "") -> dict:
    empty = {"available": False, "reused_groups": [], "source_url": "", "sources": {},
             "reason": "no_verified_receipt"}
    if not valid_request(repository, context, workflow):
        return {**empty, "reason": "request_invalid"}
    if event == "schedule":
        return {**empty, "reason": "scheduled_scan_requires_fresh_data"}
    try:
        tested, tree, _ = plan_identity(plan)
        if git_identity(tested)[0] != tree:
            raise Unavailable("source_tree_mismatch")
        desired = group_ids(plan)
        if not desired:
            return {**empty, "reason": "no_applicable_groups"}
        runs = api(repository, f"actions/workflows/{Path(workflow).name}/runs?status=success&per_page=20")
        candidates = runs.get("workflow_runs", [])
        if not isinstance(candidates, list):
            raise Unavailable("metadata_invalid")
        sources: dict[str, str] = {}
        for run in candidates[:20]:
            if not isinstance(run, dict) or not eligible_run(run, repository, workflow, current_run):
                continue
            artifacts = api(repository, f"actions/runs/{run['id']}/artifacts?per_page=100")
            matches = [item for item in artifacts.get("artifacts", [])
                       if item.get("name") == f"ci-receipt-{context}" and item.get("expired") is False]
            for artifact in sorted(matches, key=lambda item: item.get("id", 0), reverse=True):
                try:
                    receipt = read_artifact(repository, artifact)
                    reusable = receipt_groups(receipt, run, plan, repository, context, workflow) - sources.keys()
                except Unavailable as error:
                    if str(error) == "metadata_unavailable":
                        raise
                    continue
                if not reusable:
                    continue
                attempt = run.get("run_attempt", 1)
                jobs = api(repository, f"actions/runs/{run['id']}/attempts/{attempt}/jobs?per_page=100")
                if eligible_job(jobs, context):
                    url = f"https://github.com/{repository}/actions/runs/{run['id']}/attempts/{attempt}"
                    sources.update({group: url for group in reusable})
                    break
            if desired <= sources.keys():
                break
        if sources:
            return {"available": True, "reused_groups": sorted(sources),
                    "source_url": next(iter(sources.values())), "sources": sources,
                    "reason": "verified_successful_execution"}
        return empty
    except (Unavailable, KeyError, TypeError, AttributeError, ValueError) as error:
        reason = str(error) if isinstance(error, Unavailable) else "metadata_invalid"
        return {**empty, "reason": reason}


def record(plan: dict, results: dict, repository: str, context: str, workflow: str,
           environment: dict[str, str] | None = None) -> dict:
    environment = os.environ if environment is None else environment
    if not valid_request(repository, context, workflow):
        raise Unavailable("request_invalid")
    tested, tree, definition = plan_identity(plan)
    if git("rev-parse", "HEAD") != tested:
        raise Unavailable("checkout_changed")
    identity = {"tested_sha": tested, "tree_sha": tree,
                "event": environment.get("GITHUB_EVENT_NAME", ""),
                "source_base_sha": source_pair(plan)[0], "source_head_sha": source_pair(plan)[1]}
    verify_checkout(identity)
    _, parents = git_identity(tested)
    run_id, attempt = environment.get("GITHUB_RUN_ID", ""), environment.get("GITHUB_RUN_ATTEMPT", "1")
    head = environment.get("GITHUB_SHA", "")
    if (not run_id.isdecimal() or not attempt.isdecimal()
            or not SHA.fullmatch(head) or head != tested):
        raise Unavailable("run_identity_invalid")
    groups = results.get("groups")
    if not isinstance(groups, dict) or not set(groups) <= group_ids(plan):
        raise Unavailable("results_invalid")
    for value in groups.values():
        if (not isinstance(value, dict)
                or value.get("state") not in {"executed", "reused", "not_applicable"}
                or (value.get("state") in {"executed", "reused"} and value.get("success") is not True)
                or (value.get("state") == "not_applicable" and value.get("success") is not False)
                or (value.get("state") == "reused" and not value.get("source_url"))):
            raise Unavailable("results_incomplete")
    return {"schema": SCHEMA, "repository": repository, "workflow": workflow,
            "context": context, "run_id": int(run_id), "run_attempt": int(attempt),
            "run_head_sha": head, **identity, "parents": parents,
            "definition_sha256": definition, "selection_sha256": selection_digest(plan),
            "environment_sha256": plan["environment_sha256"],
            "groups": groups}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("probe", "record"))
    parser.add_argument("--plan", required=True, type=Path)
    parser.add_argument("--context", required=True)
    parser.add_argument("--workflow", required=True)
    parser.add_argument("--results", type=Path)
    parser.add_argument("--output", required=True, type=Path)
    options = parser.parse_args()
    try:
        plan = json.loads(options.plan.read_text(encoding="utf-8"))
        repository = os.environ.get("GITHUB_REPOSITORY", "")
        if options.mode == "probe":
            result = probe(plan, repository, options.context, options.workflow,
                           current_run=os.environ.get("GITHUB_RUN_ID", ""),
                           event=os.environ.get("GITHUB_EVENT_NAME", ""))
        else:
            if options.results is None:
                parser.error("record requires --results")
            results = json.loads(options.results.read_text(encoding="utf-8"))
            result = record(plan, results, repository, options.context, options.workflow)
        options.output.parent.mkdir(parents=True, exist_ok=True)
        options.output.write_text(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n", encoding="utf-8")
        return 0
    except (Unavailable, ValueError, OSError, TypeError) as error:
        reason = str(error) if isinstance(error, Unavailable) else "receipt_input_unavailable"
        print(json.dumps({"state": "failed", "reason": reason}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

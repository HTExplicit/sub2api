#!/usr/bin/env python3
"""Promote one reviewed upstream PR through immutable release and protected deployment."""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import time
from pathlib import Path

from upstream_risk import analyze

REPOSITORY = "HTExplicit/sub2api"
UPSTREAM = "Wei-Shaw/sub2api"
REQUIRED_CHECKS = {
    "shell", "test", "frontend", "golangci-lint",
    "Downstream backend", "Downstream frontend", "Candidate OCI image",
    "backend-security", "frontend-security", "Upstream risk gate",
}


def command(*args: str) -> str:
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise RuntimeError(f"{args[0]} operation failed ({result.returncode}); inspect the linked workflow or PR")
    return result.stdout.strip()


def api(path: str, *args: str):
    return json.loads(command("gh", "api", f"repos/{REPOSITORY}/{path}", *args))


def validate_candidate(pr, manifest, recorded_base: str) -> str:
    if pr["head"]["repo"]["full_name"] != REPOSITORY or pr["base"]["ref"] != "main":
        raise ValueError("candidate must belong to this repository and target main")
    tag = manifest["upstream_tag"]
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", tag):
        raise ValueError("invalid upstream stable tag")
    if pr["head"]["ref"] != f"sync/upstream-{tag[1:]}" or recorded_base != tag:
        raise ValueError("candidate branch and recorded upstream version disagree")
    if manifest.get("merge_conflicts") != []:
        raise ValueError("unresolved merge conflicts")
    if manifest["risk_class"] == "safe":
        if manifest["overlap_file_count"] or manifest["critical_file_count"]:
            raise ValueError("safe classification contains protected changes")
    elif manifest["risk_class"] == "review_required":
        if "upstream-reviewed" not in {label["name"] for label in pr["labels"]}:
            raise ValueError("review-required candidate has not been reviewed")
    else:
        raise ValueError("unknown risk classification")
    return f"{tag}-codexrip.1"


def require_checks(sha: str) -> None:
    deadline = time.monotonic() + 7200
    while time.monotonic() < deadline:
        runs = json.loads(command("gh", "api", "--paginate", "--slurp",
            f"repos/{REPOSITORY}/commits/{sha}/check-runs?per_page=100"))
        latest = {}
        for page in runs:
            for check in page["check_runs"]:
                if check["name"] not in latest or check["id"] > latest[check["name"]]["id"]:
                    latest[check["name"]] = check
        failures = [
            name for name in REQUIRED_CHECKS if name in latest
            and latest[name]["status"] == "completed"
            and latest[name]["conclusion"] != "success"
        ]
        if failures:
            raise RuntimeError("required checks failed: " + ", ".join(sorted(failures)))
        if all(name in latest and latest[name]["conclusion"] == "success" for name in REQUIRED_CHECKS):
            return
        print("Waiting for required checks on " + sha, flush=True)
        time.sleep(20)
    raise TimeoutError("required checks are still missing/pending; production unchanged")


def release_ready(tag: str, sha: str) -> bool:
    result = subprocess.run(["gh", "release", "view", tag, "--repo", REPOSITORY,
        "--json", "tagName,isDraft,isPrerelease,body"], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        return False
    release = json.loads(result.stdout)
    if release["tagName"] != tag or release["isDraft"] or release["isPrerelease"]:
        raise ValueError("release is not stable")
    if re.findall(r"^Source: `([0-9a-f]{40})`$", release["body"], re.M) != [sha]:
        raise ValueError("release source is not the promoted merge")
    images = re.findall(r"^Image: `([^`]+)`$", release["body"], re.M)
    if len(images) != 1 or not re.fullmatch(
        re.escape(f"ghcr.io/htexplicit/sub2api:{tag[1:]}@sha256:") + r"[0-9a-f]{64}", images[0]
    ):
        raise ValueError("release has no unique immutable image")
    return True


def promote(number: int) -> None:
    pr = api(f"pulls/{number}")
    head = pr["head"]["sha"]
    command("git", "fetch", "origin", head, "main")
    manifest = json.loads(command("git", "show", f"{head}:.downstream/upstream-risk.json"))
    recorded_base = command("git", "show", f"{head}:.downstream/upstream-base")
    tag = validate_candidate(pr, manifest, recorded_base)
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", manifest["upstream_base"]):
        raise ValueError("invalid recorded base")
    if not re.fullmatch(r"[0-9a-f]{40}", manifest["downstream_commit"]):
        raise ValueError("invalid downstream commit")
    upstream_release = json.loads(command("gh", "api", f"repos/{UPSTREAM}/releases/tags/{recorded_base}"))
    if upstream_release["draft"] or upstream_release["prerelease"]:
        raise ValueError("official release is not stable")
    command("git", "fetch", f"https://github.com/{UPSTREAM}.git",
        f"refs/tags/{recorded_base}:refs/tags/{recorded_base}",
        f"refs/tags/{manifest['upstream_base']}:refs/tags/{manifest['upstream_base']}")
    command("git", "merge-base", "--is-ancestor", recorded_base, head)
    recomputed = analyze(Path.cwd(), manifest["upstream_base"], recorded_base, manifest["downstream_commit"])
    for key, value in recomputed.items():
        if manifest.get(key) != value:
            raise ValueError(f"risk manifest mismatch: {key}")
    require_checks(head)
    current = api(f"pulls/{number}")
    if current["head"]["sha"] != head:
        raise ValueError("candidate changed during verification; re-review required")
    validate_candidate(current, manifest, recorded_base)
    if not current["merged"]:
        if current["state"] != "open":
            raise ValueError("candidate is closed without merging")
        command("gh", "pr", "merge", str(number), "--repo", REPOSITORY,
            "--merge", "--match-head-commit", head)
        current = api(f"pulls/{number}")
    if not current["merged"]:
        raise RuntimeError("branch protection has not allowed the merge")
    merged = current["merge_commit_sha"]
    command("git", "fetch", "origin", "main", merged)
    command("git", "merge-base", "--is-ancestor", merged, "origin/main")
    existing = subprocess.run(["gh", "api", f"repos/{REPOSITORY}/git/ref/tags/{tag}"],
        text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if existing.returncode == 0:
        command("git", "fetch", "origin", f"refs/tags/{tag}:refs/tags/{tag}")
        if command("git", "rev-list", "-n", "1", tag) != merged:
            raise ValueError("immutable release tag is already owned by another commit")
    else:
        api("git/refs", "-X", "POST", "-f", f"ref=refs/tags/{tag}", "-f", f"sha={merged}")
    if not release_ready(tag, merged):
        # GITHUB_TOKEN pushes do not trigger push workflows. Dispatch explicitly.
        runs = api("actions/workflows/downstream-release.yml/runs?per_page=100")["workflow_runs"]
        matches = [r for r in runs if r.get("display_title") == f"Release {tag}" or r.get("head_branch") == tag]
        if matches and matches[0]["status"] == "completed" and matches[0]["conclusion"] != "success":
            raise RuntimeError("previous release run failed; inspect it before retrying")
        if not matches:
            command("gh", "workflow", "run", "downstream-release.yml", "--repo", REPOSITORY,
                "--ref", "main", "-f", f"release_tag={tag}")
        deadline = time.monotonic() + 7200
        while not release_ready(tag, merged):
            runs = api("actions/workflows/downstream-release.yml/runs?per_page=30")["workflow_runs"]
            matching = [r for r in runs if r.get("display_title") == f"Release {tag}" or r.get("head_branch") == tag]
            if matching and matching[0]["status"] == "completed" and matching[0]["conclusion"] != "success":
                raise RuntimeError("release build failed: " + matching[0]["html_url"])
            if time.monotonic() >= deadline:
                raise TimeoutError("immutable release is not ready")
            time.sleep(20)
    title = f"Deploy {tag} (preserve)"
    deployments = api("actions/workflows/production-deploy.yml/runs?per_page=100")["workflow_runs"]
    previous = [r for r in deployments if r.get("display_title") == title]
    if previous:
        if previous[0]["status"] == "completed" and previous[0]["conclusion"] != "success":
            raise RuntimeError("previous production run failed; runtime audit required before retry")
        print("Existing protected deployment: " + previous[0]["html_url"])
        return
    command("gh", "workflow", "run", "production-deploy.yml", "--repo", REPOSITORY,
        "--ref", "main", "-f", "operation=deploy-preserve", "-f", f"release_tag={tag}", "-f", "confirmation=DEPLOY")
    print(f"Protected deployment dispatched: {tag}; production approval remains required.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--pr", required=True, type=int)
    args = parser.parse_args()
    if args.pr <= 0 or os.environ.get("GITHUB_REPOSITORY", REPOSITORY) != REPOSITORY:
        raise SystemExit("invalid repository or PR number")
    promote(args.pr)

#!/usr/bin/env python3
"""Classify an upstream stable-release merge against downstream main."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
from pathlib import Path


CRITICAL_PATHS = (
    re.compile(r"^\.github/(?:workflows|actions)/"),
    re.compile(r"^(?:Dockerfile|docker-compose[^/]*\.ya?ml|deploy/|\.dockerignore$)"),
    re.compile(r"^(?:backend/go\.(?:mod|sum)|frontend/(?:package\.json|pnpm-lock\.yaml))$"),
    re.compile(r"^backend/(?:migrations|ent)/"),
    re.compile(
        r"^backend/internal/(?:auth|securityaudit|repository|server/middleware|service/"
        r"(?:billing|pricing|ratelimit|account_scheduler|gateway|openai_|api_key_auth))"
    ),
)


def git_lines(repo: Path, *args: str) -> list[str]:
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return sorted({line.strip() for line in result.stdout.splitlines() if line.strip()})


def is_critical(path: str) -> bool:
    return any(pattern.search(path) for pattern in CRITICAL_PATHS)


def classify(upstream_files: list[str], downstream_files: list[str]) -> dict[str, object]:
    upstream = sorted(set(upstream_files))
    downstream = sorted(set(downstream_files))
    overlap = sorted(set(upstream).intersection(downstream))
    critical = sorted(path for path in upstream if is_critical(path))
    risk_class = "safe" if not overlap and not critical else "review_required"
    return {
        "risk_class": risk_class,
        "upstream_files": upstream,
        "downstream_files": downstream,
        "overlap_files": overlap,
        "critical_files": critical,
        "upstream_file_count": len(upstream),
        "downstream_file_count": len(downstream),
        "overlap_file_count": len(overlap),
        "critical_file_count": len(critical),
    }


def analyze(repo: Path, base: str, tag: str, downstream_ref: str) -> dict[str, object]:
    upstream_files = git_lines(repo, "diff", "--name-only", f"{base}..{tag}")
    downstream_commit = git_lines(repo, "rev-parse", "--verify", f"{downstream_ref}^{{commit}}")[0]
    downstream_files = git_lines(repo, "diff", "--name-only", f"{base}..{downstream_commit}")
    result = classify(upstream_files, downstream_files)
    result.update({
        "schema": 1,
        "upstream_base": base,
        "upstream_tag": tag,
        "downstream_commit": downstream_commit,
        "merge_conflicts": [],
    })
    return result


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--base", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--downstream-ref", default="HEAD")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--github-output", type=Path)
    args = parser.parse_args()

    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", args.base):
        raise SystemExit("invalid upstream base tag")
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", args.tag):
        raise SystemExit("invalid upstream release tag")

    result = analyze(args.repo.resolve(), args.base, args.tag, args.downstream_ref)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    if args.github_output:
        with args.github_output.open("a", encoding="utf-8") as handle:
            for key in ("risk_class", "overlap_file_count", "critical_file_count"):
                handle.write(f"{key}={result[key]}\n")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Fixed read-only GitHub/GHCR publication checks; never signs or publishes."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


ROOT = Path(__file__).resolve().parents[2]
REPOSITORY = "HTExplicit/sub2api"
IMAGE = "ghcr.io/htexplicit/sub2api"
HOST_TAG = re.compile(r"v[0-9]+\.[0-9]+\.[0-9]+-codexrip\.[1-9][0-9]*")
SHA = re.compile(r"[a-f0-9]{40}")
DIGEST = re.compile(r"sha256:[a-f0-9]{64}")
MAX_PAGES = 100
MAX_METADATA_BYTES = 16 * 1024 * 1024


class PreflightError(ValueError):
    pass


def require(condition: bool, code: str) -> None:
    if not condition:
        raise PreflightError(code)


def decode_json(raw: bytes):
    require(len(raw) <= MAX_METADATA_BYTES, "metadata_too_large")
    try:
        return json.loads(raw)
    except (UnicodeError, ValueError):
        raise PreflightError("metadata_invalid_json") from None


def github_json(path: str, runner=subprocess.run):
    try:
        result = runner(
            ["gh", "api", "--hostname", "github.com", "--method", "GET", "repos/" + REPOSITORY + "/" + path],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False, timeout=30,
        )
    except (OSError, subprocess.TimeoutExpired):
        raise PreflightError("github_metadata_unavailable") from None
    # In particular, 403/404/429 or a failed page are never an empty inventory.
    require(result.returncode == 0, "github_metadata_unavailable")
    return decode_json(result.stdout)


def list_releases(github) -> list[dict]:
    releases, seen = [], set()
    for page in range(1, MAX_PAGES + 1):
        batch = github(f"releases?per_page=100&page={page}")
        require(isinstance(batch, list) and len(batch) <= 100, "release_inventory_invalid")
        for item in batch:
            require(isinstance(item, dict) and type(item.get("id")) is int and item["id"] > 0
                    and isinstance(item.get("tag_name"), str) and item["tag_name"], "release_inventory_invalid")
            require(item["id"] not in seen, "release_inventory_changed")
            seen.add(item["id"])
            releases.append(item)
        if len(batch) < 100:
            return releases
    raise PreflightError("release_inventory_incomplete")


def tag_commit(github, tag: str) -> str:
    ref = github("git/ref/tags/" + urllib.parse.quote(tag, safe="/"))
    require(isinstance(ref, dict) and ref.get("ref") == "refs/tags/" + tag, "tag_reference_invalid")
    target, seen = ref.get("object"), set()
    for _ in range(16):
        require(isinstance(target, dict) and isinstance(target.get("sha"), str)
                and SHA.fullmatch(target["sha"]) is not None, "tag_target_invalid")
        if target.get("type") == "commit":
            return target["sha"]
        require(target.get("type") == "tag" and target["sha"] not in seen, "tag_target_invalid")
        seen.add(target["sha"])
        annotated = github("git/tags/" + target["sha"])
        require(isinstance(annotated, dict), "tag_target_invalid")
        target = annotated.get("object")
    raise PreflightError("tag_target_invalid")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, newurl):
        return None


def http_get(url: str, headers: dict[str, str]):
    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with urllib.request.build_opener(NoRedirect()).open(request, timeout=30) as response:
            return response.status, dict(response.headers), response.read(MAX_METADATA_BYTES + 1)
    except urllib.error.HTTPError as error:
        return error.code, dict(error.headers), error.read(MAX_METADATA_BYTES + 1)
    except (OSError, urllib.error.URLError):
        raise PreflightError("registry_metadata_unavailable") from None


def registry_manifest(version: str, request=http_get, token=None, actor=None):
    require(HOST_TAG.fullmatch("v" + version) is not None, "image_version_invalid")
    token = token if token is not None else os.environ.get("GH_TOKEN", "")
    actor = actor if actor is not None else os.environ.get("GITHUB_ACTOR", "")
    require(bool(token) and bool(actor), "registry_auth_unavailable")
    authorization = base64.b64encode((actor + ":" + token).encode()).decode()
    query = urllib.parse.urlencode({"service": "ghcr.io", "scope": "repository:htexplicit/sub2api:pull"})
    status, _, raw = request("https://ghcr.io/token?" + query, {"Authorization": "Basic " + authorization})
    require(status == 200, "registry_auth_unavailable")
    identity = decode_json(raw)
    require(isinstance(identity, dict), "registry_auth_invalid")
    bearer = identity.get("token") or identity.get("access_token")
    require(isinstance(bearer, str) and 0 < len(bearer) <= 16384
            and not any(character in bearer for character in "\r\n\x00"), "registry_auth_invalid")
    status, headers, raw = request("https://ghcr.io/v2/htexplicit/sub2api/manifests/" + version, {
        "Authorization": "Bearer " + bearer,
        "Accept": ", ".join(("application/vnd.oci.image.index.v1+json", "application/vnd.oci.image.manifest.v1+json",
                             "application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.docker.distribution.manifest.v2+json")),
    })
    require(status in (200, 404), "registry_metadata_unavailable")
    document = decode_json(raw)
    require(isinstance(document, dict), "registry_metadata_invalid")
    if status == 404:
        errors = document.get("errors")
        require(isinstance(errors, list) and bool(errors)
                and all(isinstance(error, dict) and error.get("code") == "MANIFEST_UNKNOWN" for error in errors),
                "registry_absence_unconfirmed")
        return None
    digest = {key.lower(): value for key, value in headers.items()}.get("docker-content-digest", "")
    require(document.get("schemaVersion") == 2 and DIGEST.fullmatch(digest) is not None
            and digest == "sha256:" + hashlib.sha256(raw).hexdigest(), "registry_digest_invalid")
    return digest


def local_assets(args, root: Path) -> dict[str, tuple[int, str]]:
    # Native domains ship in the immutable OCI image; there are no separate
    # first-party package assets attached to a host release.
    return {}


def check(args, github=github_json, registry=registry_manifest, root=ROOT):
    require(SHA.fullmatch(args.source_sha) is not None, "source_sha_invalid")
    require(args.kind == "host", "release_kind_invalid")
    require(HOST_TAG.fullmatch(args.tag) is not None, "release_tag_invalid")
    releases = list_releases(github)
    matches = [release for release in releases if release["tag_name"] == args.tag]
    if args.stage == "before-publish":
        require(len(matches) == 1 and matches[0].get("draft") is True, "unique_draft_required")
        release = matches[0]
        target = release.get("target_commitish")
        require(isinstance(target, str) and bool(target), "draft_target_invalid")
        # GitHub may retain a branch name for an existing tag; only the tag's
        # dereferenced commit below is authoritative. A supplied SHA must agree.
        require(SHA.fullmatch(target) is None or target == args.source_sha, "draft_target_mismatch")
        expected = local_assets(args, root)
        assets = release.get("assets")
        require(isinstance(assets, list) and len(assets) == len(expected), "draft_assets_incomplete")
        seen = set()
        for asset in assets:
            require(isinstance(asset, dict) and asset.get("name") in expected
                    and asset["name"] not in seen and asset.get("state") == "uploaded", "draft_assets_incomplete")
            seen.add(asset["name"])
            size, digest = expected[asset["name"]]
            require(type(asset.get("size")) is int and asset["size"] == size
                    and asset.get("digest") == digest, "draft_asset_digest_mismatch")
    else:
        require(not matches, "release_already_exists")
    if args.kind == "host":
        digest = registry(args.tag[1:])
        if args.stage in ("before-build", "before-push"):
            require(digest is None, "version_image_already_exists")
        else:
            require(DIGEST.fullmatch(args.image_digest) is not None and digest == args.image_digest, "published_image_digest_mismatch")
    # These are deliberately the final remote reads before the workflow's next
    # write. They reduce the gap, but cannot replace server-side immutable tags.
    require(tag_commit(github, args.tag) == args.source_sha, "release_tag_changed")
    result = {"state": "verified", "stage": args.stage, "tag": args.tag, "source_sha": args.source_sha}
    if args.stage == "before-publish":
        result["release_id"] = matches[0]["id"]
    return result


def main(argv=None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kind", choices=("host",), required=True)
    parser.add_argument("--stage", choices=("before-build", "before-push", "before-create", "before-publish"), required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--image-digest", default="")
    args = parser.parse_args(argv)
    try:
        require(os.environ.get("GITHUB_REPOSITORY", "").casefold() == REPOSITORY.casefold(), "repository_mismatch")
        require(bool(os.environ.get("GH_TOKEN")), "github_token_unavailable")
        print(json.dumps(check(args), separators=(",", ":")))
        return 0
    except Exception as error:
        code = str(error) if isinstance(error, PreflightError) else "metadata_operation_failed"
        print(json.dumps({"state": "failed", "code": code}, separators=(",", ":")), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

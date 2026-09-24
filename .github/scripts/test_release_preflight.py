#!/usr/bin/env python3
"""Only publication metadata mocks and workflow contracts; no external writes."""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import textwrap
import unittest
from unittest import mock

import release_preflight as guard


SOURCE = "a" * 40
TAG = "v0.2.7-codexrip.1"
IMAGE_DIGEST = "sha256:" + "c" * 64


def options(stage="before-push", kind="host"):
    return argparse.Namespace(kind=kind, stage=stage, tag=TAG, source_sha=SOURCE,
                              image_digest=IMAGE_DIGEST if stage in ("before-create", "before-publish") else "")


class Repository:
    def __init__(self, args, pages=None):
        self.args = args
        self.calls = []
        self.source = SOURCE
        self.pages = pages or {1: []}

    def __call__(self, path):
        self.calls.append(path)
        if path.startswith("releases?"):
            return copy.deepcopy(self.pages.get(int(path.rsplit("=", 1)[1]), []))
        if path.startswith("git/ref/tags/"):
            tag = path.removeprefix("git/ref/tags/")
            sha = self.source
            return {"ref": "refs/tags/" + tag, "object": {"type": "commit", "sha": sha}}
        raise AssertionError("unexpected metadata request: " + path)


class ReleaseMetadataTests(unittest.TestCase):
    def test_existing_draft_on_a_later_page_blocks_every_write(self):
        args = options()
        page = [{"id": index + 1, "tag_name": "unrelated-" + str(index)} for index in range(100)]
        repo = Repository(args, {1: page, 2: [{"id": 101, "tag_name": TAG, "draft": True}]})
        registry = mock.Mock(side_effect=AssertionError("must stop before registry lookup"))
        with self.assertRaisesRegex(guard.PreflightError, "release_already_exists"):
            guard.check(args, github=repo, registry=registry)
        registry.assert_not_called()
        self.assertEqual(2, len(repo.calls))

    def test_incomplete_or_failed_pagination_is_not_absence(self):
        for result in (None, {"message": "forbidden"}, "not a list"):
            with self.subTest(result=result), self.assertRaises(guard.PreflightError):
                guard.list_releases(lambda _: result)
        batch = [{"id": i + 1, "tag_name": "unrelated-" + str(i)} for i in range(100)]
        with mock.patch.object(guard, "MAX_PAGES", 1), self.assertRaisesRegex(guard.PreflightError, "inventory_incomplete"):
            guard.list_releases(lambda _: batch)
        with self.assertRaisesRegex(guard.PreflightError, "inventory_changed"):
            guard.list_releases(lambda _: batch)
        for code in (1, 4):
            runner = mock.Mock(return_value=subprocess.CompletedProcess([], code, b"[]", b"fixture-sensitive-error"))
            with self.assertRaisesRegex(guard.PreflightError, "github_metadata_unavailable"):
                guard.github_json("releases?per_page=100&page=1", runner=runner)
            self.assertEqual(["gh", "api", "--hostname", "github.com", "--method", "GET"], runner.call_args.args[0][:6])

    def test_only_confirmed_missing_registry_manifest_allows_push(self):
        for status, document, allowed in (
            (404, {"errors": [{"code": "MANIFEST_UNKNOWN"}]}, True),
            (404, {"errors": [{"code": "NAME_UNKNOWN"}]}, False),
            (404, {}, False),
            (401, {}, False), (403, {}, False), (429, {}, False), (500, {}, False),
        ):
            with self.subTest(status=status, document=document):
                request = mock.Mock(side_effect=[(200, {}, b'{"token":"fixture-bearer"}'), (status, {}, json.dumps(document).encode())])
                if allowed:
                    self.assertIsNone(guard.registry_manifest(TAG[1:], request, "fixture-token", "fixture-actor"))
                else:
                    with self.assertRaises(guard.PreflightError):
                        guard.registry_manifest(TAG[1:], request, "fixture-token", "fixture-actor")
                self.assertTrue(all(call.args[0].startswith("https://ghcr.io/") for call in request.call_args_list))

    def test_registry_digest_must_match_returned_manifest_bytes(self):
        raw = b'{"schemaVersion":2,"layers":[]}'
        digest = "sha256:" + hashlib.sha256(raw).hexdigest()
        for reported in (digest, IMAGE_DIGEST, ""):
            with self.subTest(reported=reported):
                request = mock.Mock(side_effect=[(200, {}, b'{"token":"fixture-bearer"}'), (200, {"Docker-Content-Digest": reported}, raw)])
                if reported == digest:
                    self.assertEqual(digest, guard.registry_manifest(TAG[1:], request, "fixture-token", "fixture-actor"))
                else:
                    with self.assertRaisesRegex(guard.PreflightError, "registry_digest_invalid"):
                        guard.registry_manifest(TAG[1:], request, "fixture-token", "fixture-actor")

    def test_tag_is_checked_last_and_must_resolve_to_admitted_commit(self):
        args = options()
        repo = Repository(args)
        self.assertEqual("verified", guard.check(args, github=repo, registry=lambda _: None)["state"])
        self.assertEqual("git/ref/tags/" + TAG, repo.calls[-1])
        repo.source = "d" * 40
        with self.assertRaisesRegex(guard.PreflightError, "release_tag_changed"):
            guard.check(args, github=repo, registry=lambda _: None)
        annotated = {
            "git/ref/tags/" + TAG: {"ref": "refs/tags/" + TAG, "object": {"type": "tag", "sha": "e" * 40}},
            "git/tags/" + "e" * 40: {"object": {"type": "commit", "sha": SOURCE}},
        }
        self.assertEqual(SOURCE, guard.tag_commit(annotated.__getitem__, TAG))

    def test_existing_image_and_later_digest_drift_are_rejected(self):
        args = options()
        with self.assertRaisesRegex(guard.PreflightError, "version_image_already_exists"):
            guard.check(args, github=Repository(args), registry=lambda _: IMAGE_DIGEST)
        args = options("before-create")
        for observed in (None, "sha256:" + "d" * 64):
            with self.subTest(observed=observed), self.assertRaisesRegex(guard.PreflightError, "published_image_digest_mismatch"):
                guard.check(args, github=Repository(args), registry=lambda _: observed)

    def test_complete_draft_assets_and_source_allow_publication_by_id(self):
        for target in (SOURCE, "main"):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as tmp:
                args = options("before-publish")
                release = {"id": 101, "tag_name": args.tag, "draft": True,
                           "target_commitish": target, "assets": []}
                repo = Repository(args, {1: [release]})
                result = guard.check(args, github=repo, registry=lambda _: IMAGE_DIGEST, root=Path(tmp))
                self.assertEqual(101, result["release_id"])
                self.assertEqual("git/ref/tags/" + args.tag, repo.calls[-1])

    def test_missing_or_changed_draft_asset_metadata_prevents_publication(self):
        for mutation in ("unexpected-package", "invalid-assets", "wrong-target"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as tmp:
                args = options("before-publish")
                release = {"id": 101, "tag_name": TAG, "draft": True,
                           "target_commitish": SOURCE, "assets": []}
                if mutation == "unexpected-package":
                    release["assets"] = [{"name": "old.s2plugin", "state": "uploaded"}]
                if mutation == "invalid-assets":
                    release["assets"] = None
                if mutation == "wrong-target":
                    release["target_commitish"] = "d" * 40
                with self.assertRaises(guard.PreflightError):
                    guard.check(args, github=Repository(args, {1: [release]}), registry=lambda _: IMAGE_DIGEST, root=Path(tmp))


class ReleaseWorkflowContracts(unittest.TestCase):
    def test_git_authentication_is_temporary_and_only_fetches_main(self):
        if os.name == "nt":
            bash = "D:/software/Git/bin/bash.exe"
            if not Path(bash).is_file():
                self.skipTest("local Git Bash required; never start the WSL shim")
        else:
            bash = shutil.which("bash")
            if bash is None:
                self.skipTest("Bash is required for the environment-only fixture")
        for name in ("downstream-release.yml",):
            source = (guard.ROOT / ".github/workflows" / name).read_text(encoding="utf-8")
            snippet = re.search(r"(?ms)^          authorization=\$\(.*?^          unset authorization$", source)
            self.assertIsNotNone(snippet)
            body = r'''
set -euo pipefail
GH_TOKEN=fixture-job-token
calls=0
git() {
  [[ "$*" == 'fetch origin main' ]]
  [[ "$GIT_CONFIG_COUNT" == 1 && "$GIT_CONFIG_KEY_0" == http.https://github.com/.extraheader ]]
  expected=$(printf 'x-access-token:%s' "$GH_TOKEN" | base64 -w0)
  [[ "$GIT_CONFIG_VALUE_0" == "AUTHORIZATION: basic $expected" ]]
  calls=$((calls + 1))
}
{
''' + textwrap.dedent(snippet[0]) + r'''
} >/dev/null
[[ "$calls" == 1 && -z ${authorization+x} && -z ${GIT_CONFIG_COUNT+x} && -z ${GIT_CONFIG_VALUE_0+x} ]]
printf 'TEMPORARY_GIT_AUTH|state=passed|real_git=false\n'
'''
            with self.subTest(workflow=name), tempfile.TemporaryDirectory() as tmp:
                result = subprocess.run([bash, "-c", body], cwd=tmp, capture_output=True, text=True, timeout=15)
                self.assertEqual(0, result.returncode)
                self.assertIn("TEMPORARY_GIT_AUTH|state=passed", result.stdout)
                self.assertNotIn("fixture-job-token", result.stdout + result.stderr)

    def test_write_credentials_are_not_in_build_environments(self):
        for name in ("downstream-release.yml",):
            source = (guard.ROOT / ".github/workflows" / name).read_text(encoding="utf-8")
            self.assertEqual([], re.findall(r"(?m)^      GH_TOKEN:.*$", source), "a job-wide token reaches dependency/build subprocesses")
            self.assertEqual(source.count("uses: actions/checkout@"), source.count("persist-credentials: false"))
            self.assertIn("GIT_CONFIG_COUNT=1", source)
            self.assertIn("GIT_CONFIG_VALUE_0=", source)
            self.assertNotIn("git config --global", source)
            self.assertNotIn("git config --local", source)
            for step in source.split("      - "):
                if "pnpm --dir frontend install" in step or "go build" in step or "uses: docker/build-push-action@" in step:
                    self.assertNotIn("GH_TOKEN:", step)
        host = (guard.ROOT / ".github/workflows/downstream-release.yml").read_text(encoding="utf-8")
        self.assertLess(host.index("name: Build release image"), host.index("uses: docker/login-action@"))
        self.assertLess(host.index("uses: docker/login-action@"), host.index('docker push "$IMAGE:$VERSION"'))

    def test_host_uses_admitted_sha_and_builds_before_guarded_push(self):
        source = (guard.ROOT / ".github/workflows/downstream-release.yml").read_text(encoding="utf-8")
        self.assertIn("ref: refs/tags/${{ env.RELEASE_TAG }}", source)
        self.assertIn("ref: ${{ needs.verify.outputs.source_sha }}", source)
        self.assertIn("load: true", source)
        self.assertIn("push: false", source)
        self.assertLess(source.index("--stage before-push"), source.index('docker push "$IMAGE:$VERSION"'))
        self.assertIn("--draft", source)
        self.assertIn('--target "$SOURCE_SHA"', source)
        self.assertIn("--stage before-publish", source)
        self.assertIn('releases/$RELEASE_ID', source)
        self.assertNotIn("--clobber", source)
        self.assertNotIn("gh release delete", source)

    def test_native_release_has_no_first_party_signing_or_upload(self):
        source = (guard.ROOT / ".github/workflows/downstream-release.yml").read_text(encoding="utf-8")
        for retired in ("SUB2API_PLUGIN_SIGNING_KEY", "package-plugin", "plugin-bundle", ".s2plugin", "extensionapi/ui"):
            self.assertNotIn(retired, source)
        self.assertIn("Attest image provenance", source)
        self.assertIn("Image:", source)
        self.assertIn("Source:", source)
        self.assertFalse((guard.ROOT / ".github/workflows/plugin-release.yml").exists())


if __name__ == "__main__":
    unittest.main()

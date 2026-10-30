#!/usr/bin/env python3
"""Offline fixtures for CI receipt provenance; never execute business tests."""
from __future__ import annotations

import io
import json
import unittest
from unittest.mock import patch
import zipfile

import ci_receipts as receipts


REPO = "HTExplicit/sub2api"
WORKFLOW = ".github/workflows/downstream-verify.yml"
CONTEXT = "Downstream backend"
COMMIT, TREE, DEFINITION = "a" * 40, "b" * 40, "c" * 64
BASE, HEAD = "d" * 40, "e" * 40


def plan():
    return {"schema": "ci-plan/v1", "base": BASE, "head": COMMIT,
            "tested_sha": COMMIT, "tree_sha": TREE, "definition_sha256": DEFINITION,
            "environment_sha256": "4" * 64,
            "source_base_sha": BASE, "source_head_sha": HEAD,
            "groups": ["transport", "agent"], "passed": True, "failures": []}


def run():
    return {"id": 21, "run_attempt": 1, "head_sha": COMMIT,
            "status": "completed", "conclusion": "success", "path": WORKFLOW,
            "repository": {"full_name": REPO}, "head_repository": {"full_name": REPO},
            "event": "push"}


def receipt():
    return {"schema": receipts.SCHEMA, "repository": REPO, "workflow": WORKFLOW,
            "context": CONTEXT, "run_id": 21, "run_attempt": 1,
            "event": "push", "run_head_sha": COMMIT, "tested_sha": COMMIT,
            "tree_sha": TREE, "definition_sha256": DEFINITION,
            "environment_sha256": "4" * 64,
            "selection_sha256": receipts.selection_digest(plan()),
            "parents": [BASE], "source_base_sha": BASE, "source_head_sha": HEAD,
            "groups": {"transport": {"state": "executed", "success": True},
                       "agent": {"state": "not_applicable", "success": False}}}


class ReceiptProofTests(unittest.TestCase):
    def test_same_tree_definition_and_selection_require_identical_environment(self):
        with patch.object(receipts, "git_identity", return_value=(TREE, [BASE])):
            for environment in ("5" * 64, None):
                with self.subTest(environment=environment):
                    record = receipt()
                    record["environment_sha256"] = environment
                    self.assertEqual(set(), receipts.receipt_groups(record, run(), plan(), REPO, CONTEXT, WORKFLOW))
        for environment in (None, "malformed"):
            with self.subTest(plan_environment=environment):
                current = plan()
                current["environment_sha256"] = environment
                with self.assertRaisesRegex(receipts.Unavailable, "plan_identity_invalid"):
                    receipts.plan_identity(current)

    def test_same_tree_and_group_with_different_targets_cannot_reuse(self):
        variants = {
            "backend_tests": [{"package": "./internal/service", "names": ["TestAnother"]}],
            "backend_packages": ["./internal/service"],
              "backend_lint_packages": ["./internal/service"],
              "backend_dependency_graph": True,
            "backend_compile_packages": ["./internal/handler"],
            "go_checks": [{"package": "./internal/service", "names": ["TestAnother"], "tags": ["unit"], "race": True}],
            "covered_by_other_groups": {"transport": ["TestAnother"]},
            "frontend_files": ["src/another.ts"],
            "frontend_tests": ["src/__tests__/another.spec.ts"],
            "frontend_related": ["src/another.ts"],
            "frontend_typecheck": True,
            "static_files": ["DOWNSTREAM.md"],
            "deleted_static_files": ["obsolete.md"],
            "deploy_tests": ["deploy/tests/another-test.sh"],
            "reasons": {"transport": ["backend/internal/pkg/codextransport/another.go"]},
        }
        with patch.object(receipts, "git_identity", return_value=(TREE, [BASE])):
            for field, value in variants.items():
                with self.subTest(field=field):
                    current = plan()
                    current[field] = value
                    self.assertEqual(set(), receipts.receipt_groups(receipt(), run(), current, REPO, CONTEXT, WORKFLOW))

    def test_selection_digest_excludes_only_commit_identity_and_is_deterministic(self):
        current = plan()
        current.update(base="f" * 40, source_base_sha="f" * 40)
        current["groups"] = list(reversed(current["groups"]))
        self.assertEqual(receipts.selection_digest(plan()), receipts.selection_digest(current))
        with patch.object(receipts, "git_identity", return_value=(TREE, [BASE])):
            self.assertEqual({"transport"}, receipts.receipt_groups(receipt(), run(), current, REPO, CONTEXT, WORKFLOW))

    def test_only_original_successful_execution_is_reusable(self):
        record = receipt()
        with patch.object(receipts, "git_identity", return_value=(TREE, [BASE])):
            self.assertEqual({"transport"}, receipts.receipt_groups(record, run(), plan(), REPO, CONTEXT, WORKFLOW))
            for state in ("reused", "not_applicable", "failed", "skipped"):
                record["groups"]["transport"]["state"] = state
                self.assertEqual(set(), receipts.receipt_groups(record, run(), plan(), REPO, CONTEXT, WORKFLOW))
            record["groups"]["transport"] = {"state": "executed", "success": False}
            self.assertEqual(set(), receipts.receipt_groups(record, run(), plan(), REPO, CONTEXT, WORKFLOW))

    def test_receipt_identity_mismatches_are_not_reused(self):
        changes = {"repository": "someone/else", "workflow": ".github/workflows/backend-ci.yml",
                   "context": "Downstream frontend", "run_id": 22, "run_attempt": 2,
                   "event": "workflow_dispatch", "tree_sha": "f" * 40,
                   "definition_sha256": "f" * 64, "run_head_sha": "f" * 40}
        with patch.object(receipts, "git_identity", return_value=(TREE, [BASE])):
            for key, value in changes.items():
                with self.subTest(key=key):
                    record = receipt()
                    record[key] = value
                    self.assertEqual(set(), receipts.receipt_groups(record, run(), plan(), REPO, CONTEXT, WORKFLOW))

    def test_git_objects_and_tree_must_be_independently_available(self):
        for response in (receipts.Unavailable("source_unavailable"), ("f" * 40, [BASE])):
            with self.subTest(response=response), patch.object(receipts, "git_identity") as identity:
                if isinstance(response, Exception):
                    identity.side_effect = response
                else:
                    identity.return_value = response
                with self.assertRaises(receipts.Unavailable):
                    receipts.receipt_groups(receipt(), run(), plan(), REPO, CONTEXT, WORKFLOW)

    def test_pr_merge_validates_actual_base_and_head_parents(self):
        record, source = receipt(), run()
        record.update(event="pull_request", parents=[BASE, HEAD])
        source.update(event="pull_request", head_sha=HEAD)
        with patch.object(receipts, "git_identity", return_value=(TREE, [BASE, HEAD])):
            self.assertEqual({"transport"}, receipts.receipt_groups(record, source, plan(), REPO, CONTEXT, WORKFLOW))
        for parents in ([BASE], [HEAD, BASE], [BASE, "f" * 40]):
            record["parents"] = parents
            with self.subTest(parents=parents), patch.object(receipts, "git_identity", return_value=(TREE, parents)):
                with self.assertRaises(receipts.Unavailable):
                    receipts.receipt_groups(record, source, plan(), REPO, CONTEXT, WORKFLOW)

    def test_run_requires_same_repository_trusted_path_and_success(self):
        self.assertTrue(receipts.eligible_run(run(), REPO, WORKFLOW))
        for change in ({"status": "in_progress"}, {"conclusion": "failure"},
                       {"conclusion": "cancelled"}, {"conclusion": "skipped"},
                       {"path": ".github/workflows/untrusted.yml"},
                       {"repository": {"full_name": "someone/else"}},
                       {"head_repository": {"full_name": "someone/else"}},
                       {"event": "schedule"}):
            with self.subTest(change=change):
                self.assertFalse(receipts.eligible_run({**run(), **change}, REPO, WORKFLOW))
        self.assertFalse(receipts.eligible_run(run(), REPO, WORKFLOW, "21"))

    def test_receipt_rejects_invalid_plan_schema(self):
        current = plan()
        current["schema"] = "invented-plan/v1"
        with self.assertRaisesRegex(receipts.Unavailable, "plan_identity_invalid"):
            receipts.plan_identity(current)

    def test_required_job_must_itself_succeed(self):
        for conclusion in ("success", "failure", "cancelled", "skipped"):
            jobs = {"jobs": [{"name": CONTEXT, "status": "completed", "conclusion": conclusion}]}
            self.assertEqual(conclusion == "success", receipts.eligible_job(jobs, CONTEXT))
        self.assertFalse(receipts.eligible_job({"jobs": []}, CONTEXT))


class ReceiptProbeTests(unittest.TestCase):
    def test_unavailable_or_stalled_cli_is_a_safe_cache_miss(self):
        for error in (FileNotFoundError(), receipts.subprocess.TimeoutExpired("gh", 60)):
            with self.subTest(error=type(error)), patch.object(receipts.subprocess, "run", side_effect=error):
                with self.assertRaisesRegex(receipts.Unavailable, "metadata_unavailable"):
                    receipts.invoke(["gh", "api", "fixture"])

    def test_probe_returns_original_execution_link_after_run_and_job_checks(self):
        artifact = {"id": 7, "name": f"ci-receipt-{CONTEXT}", "expired": False}
        responses = [{"workflow_runs": [run()]}, {"artifacts": [artifact]},
                     {"jobs": [{"name": CONTEXT, "status": "completed", "conclusion": "success"}]}]
        with patch.object(receipts, "git_identity", return_value=(TREE, [BASE])), \
                patch.object(receipts, "api", side_effect=responses) as api, \
                patch.object(receipts, "read_artifact", return_value=receipt()):
            result = receipts.probe(plan(), REPO, CONTEXT, WORKFLOW)
        self.assertTrue(result["available"])
        self.assertEqual(["transport"], result["reused_groups"])
        self.assertEqual("https://github.com/HTExplicit/sub2api/actions/runs/21/attempts/1", result["source_url"])
        self.assertEqual(3, api.call_count)

    def test_api_failure_missing_artifact_and_failed_job_never_create_success(self):
        for responses in (
            receipts.Unavailable("metadata_unavailable"),
            [{"workflow_runs": [run()]}, {"artifacts": []}],
            [{"workflow_runs": [run()]}, {"artifacts": [{"id": 7, "name": f"ci-receipt-{CONTEXT}", "expired": False}]},
             {"jobs": [{"name": CONTEXT, "status": "completed", "conclusion": "failure"}]}],
        ):
            with self.subTest(responses=responses), \
                    patch.object(receipts, "git_identity", return_value=(TREE, [BASE])), \
                    patch.object(receipts, "api", side_effect=responses), \
                    patch.object(receipts, "read_artifact", return_value=receipt()):
                result = receipts.probe(plan(), REPO, CONTEXT, WORKFLOW)
                self.assertFalse(result["available"])
                self.assertEqual([], result["reused_groups"])

    def test_scheduled_scans_never_read_old_security_results(self):
        with patch.object(receipts, "api") as api:
            result = receipts.probe(plan(), REPO, "backend-security", ".github/workflows/security-scan.yml", event="schedule")
        self.assertFalse(result["available"])
        api.assert_not_called()

    def test_artifact_is_bounded_json_without_filesystem_extraction(self):
        archive = io.BytesIO()
        with zipfile.ZipFile(archive, "w") as stream:
            stream.writestr("receipt.json", json.dumps(receipt()))
        artifact = {"id": 7, "expired": False, "size_in_bytes": len(archive.getvalue())}
        with patch.object(receipts, "invoke", return_value=archive.getvalue()):
            self.assertEqual(receipt(), receipts.read_artifact(REPO, artifact))
        with self.assertRaises(receipts.Unavailable):
            receipts.read_artifact(REPO, {**artifact, "expired": True})
        with self.assertRaises(receipts.Unavailable):
            receipts.read_artifact(REPO, {**artifact, "size_in_bytes": receipts.MAX_RECEIPT_BYTES + 1})


class ReceiptRecordingTests(unittest.TestCase):
    def setUp(self):
        self.environment = {"GITHUB_RUN_ID": "21", "GITHUB_RUN_ATTEMPT": "1",
                            "GITHUB_SHA": COMMIT, "GITHUB_EVENT_NAME": "push"}

    def test_record_does_not_call_not_applicable_a_successful_test(self):
        results = {"groups": {"transport": {"state": "executed", "success": True},
                              "agent": {"state": "not_applicable", "success": False}}}
        with patch.object(receipts, "git", return_value=COMMIT), \
                patch.object(receipts, "git_identity", return_value=(TREE, [BASE])):
            result = receipts.record(plan(), results, REPO, CONTEXT, WORKFLOW, self.environment)
        self.assertEqual("not_applicable", result["groups"]["agent"]["state"])
        self.assertFalse(result["groups"]["agent"]["success"])
        self.assertEqual([BASE], result["parents"])
        self.assertEqual("4" * 64, result["environment_sha256"])

    def test_record_rejects_failed_checks_and_checkout_drift(self):
        for value in ({"state": "failed", "success": False}, {"state": "executed", "success": False},
                      {"state": "reused", "success": True}, {"state": "not_applicable", "success": True}):
            with self.subTest(value=value), patch.object(receipts, "git", return_value=COMMIT), \
                    patch.object(receipts, "git_identity", return_value=(TREE, [BASE])):
                with self.assertRaises(receipts.Unavailable):
                    receipts.record(plan(), {"groups": {"transport": value}}, REPO, CONTEXT, WORKFLOW, self.environment)
        with patch.object(receipts, "git", return_value="f" * 40):
            with self.assertRaises(receipts.Unavailable):
                receipts.record(plan(), {"groups": {}}, REPO, CONTEXT, WORKFLOW, self.environment)


if __name__ == "__main__":
    unittest.main()

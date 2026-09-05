#!/usr/bin/env python3

import re
import unittest
from pathlib import Path
from upstream_promote import validate_candidate


ROOT = Path(__file__).resolve().parents[2]


class UpstreamAutomationContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.sync = (ROOT / ".github/workflows/upstream-sync.yml").read_text(encoding="utf-8")
        cls.gate = (ROOT / ".github/workflows/upstream-risk-gate.yml").read_text(encoding="utf-8")
        cls.release = (ROOT / ".github/workflows/upstream-auto-release.yml").read_text(encoding="utf-8")
        cls.handoff = (ROOT / ".github/workflows/upstream-auto-deploy.yml").read_text(encoding="utf-8")
        cls.production = (ROOT / ".github/workflows/production-deploy.yml").read_text(encoding="utf-8")
        cls.promotion = (ROOT / ".github/workflows/upstream-promote.yml").read_text(encoding="utf-8")
        cls.promoter = (ROOT / ".github/scripts/upstream_promote.py").read_text(encoding="utf-8")
        cls.publisher = (ROOT / ".github/workflows/downstream-release.yml").read_text(encoding="utf-8")

    def test_sync_is_hourly_stable_only_and_stops_on_risk(self):
        self.assertIn("cron: '17 * * * *'", self.sync)
        self.assertIn("releases/latest", self.sync)
        self.assertIn(".draft == false and .prerelease == false", self.sync)
        self.assertIn("--force-with-lease", self.sync)
        self.assertIn("upstream-review-required", self.sync)
        self.assertIn(
            'gh pr merge "$pr" --repo "$GITHUB_REPOSITORY" --auto --merge',
            self.sync,
        )
        self.assertIn("/tmp/upstream-conflict.md", self.sync)

    def test_risk_gate_executes_only_trusted_code(self):
        self.assertIn("pull_request_target:", self.gate)
        self.assertIn("path: trusted", self.gate)
        self.assertIn("path: candidate", self.gate)
        self.assertIn("python3 trusted/.github/scripts/upstream_risk.py", self.gate)
        self.assertIn("upstream-reviewed", self.gate)

    def test_safe_merge_tags_once_and_handoff_waits_for_approval(self):
        self.assertIn("upstream-safe-candidate", self.release)
        self.assertIn("upstream-promote.yml", self.release)
        self.assertIn("immutable release tag is already owned", self.promoter)
        self.assertIn("-codexrip.1", self.promoter)
        self.assertIn("endsWith(github.event.workflow_run.head_branch, '-codexrip.1')", self.handoff)
        self.assertIn("Manual codexrip releases must remain independent", self.handoff)
        self.assertIn("operation=deploy-preserve", self.handoff)
        self.assertIn("environment:\n      name: production", self.production)
        self.assertIn("runtime=preserve", self.production)
        self.assertNotIn("pending_deployments", self.handoff)
        self.assertNotIn("pending_deployments", self.promoter)
        self.assertIn("--match-head-commit", self.promoter)
        self.assertIn("operation=deploy-preserve", self.promoter)
        self.assertIn("source_sha", self.publisher)
        self.assertIn("ref: ${{ env.RELEASE_TAG }}", self.publisher)

    def test_bot_chain_is_explicit_and_does_not_overwrite_manual_resolutions(self):
        self.assertIn("upstream_tag:", self.sync)
        self.assertIn("preserve manual resolutions", self.sync)
        self.assertIn("upstream-conflict.json", self.sync)
        for workflow in ("backend-ci.yml", "security-scan.yml", "downstream-verify.yml", "upstream-risk-gate.yml"):
            self.assertIn(workflow, self.sync)
        self.assertIn('"downstream-release.yml"', self.promoter)
        self.assertIn("workflow_dispatch:", self.publisher)

    def test_candidate_review_and_repository_contract(self):
        pr = {"head": {"repo": {"full_name": "HTExplicit/sub2api"}, "ref": "sync/upstream-0.2.1"},
              "base": {"ref": "main"}, "labels": []}
        manifest = {"upstream_tag": "v0.2.1", "merge_conflicts": [], "risk_class": "review_required",
                    "overlap_file_count": 1, "critical_file_count": 1}
        with self.assertRaises(ValueError):
            validate_candidate(pr, manifest, "v0.2.1")
        pr["labels"] = [{"name": "upstream-reviewed"}]
        self.assertEqual("v0.2.1-codexrip.1", validate_candidate(pr, manifest, "v0.2.1"))
        manifest["risk_class"] = "safe"
        with self.assertRaises(ValueError):
            validate_candidate(pr, manifest, "v0.2.1")
        manifest.update(overlap_file_count=0, critical_file_count=0)
        self.assertEqual("v0.2.1-codexrip.1", validate_candidate(pr, manifest, "v0.2.1"))
        pr["head"]["repo"]["full_name"] = "elsewhere/sub2api"
        with self.assertRaises(ValueError):
            validate_candidate(pr, manifest, "v0.2.1")

    def test_failures_create_issues_without_retry_loops(self):
        self.assertIn("upstream-automation-failed", self.release)
        self.assertIn("upstream-automation-failed", self.handoff)
        self.assertNotIn("for attempt", self.release)
        self.assertNotIn("for attempt", self.handoff)
        for workflow in (self.release, self.handoff):
            self.assertIn('gh label create --repo "$GITHUB_REPOSITORY"', workflow)
            self.assertIn('gh issue list --repo "$GITHUB_REPOSITORY"', workflow)
            self.assertIn('gh issue create --repo "$GITHUB_REPOSITORY"', workflow)

    def test_repository_mutations_never_depend_on_git_remote_inference(self):
        local_commands = re.compile(r"\bgh (label|issue|pr|release|run|workflow)\b")
        for workflow in (self.sync, self.release, self.handoff):
            for line in workflow.splitlines():
                if local_commands.search(line):
                    self.assertIn('--repo "$GITHUB_REPOSITORY"', line, line)


if __name__ == "__main__":
    unittest.main()

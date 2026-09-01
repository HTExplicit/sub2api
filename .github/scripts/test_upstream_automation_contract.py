#!/usr/bin/env python3

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


class UpstreamAutomationContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.sync = (ROOT / ".github/workflows/upstream-sync.yml").read_text(encoding="utf-8")
        cls.gate = (ROOT / ".github/workflows/upstream-risk-gate.yml").read_text(encoding="utf-8")
        cls.release = (ROOT / ".github/workflows/upstream-auto-release.yml").read_text(encoding="utf-8")
        cls.handoff = (ROOT / ".github/workflows/upstream-auto-deploy.yml").read_text(encoding="utf-8")
        cls.production = (ROOT / ".github/workflows/production-deploy.yml").read_text(encoding="utf-8")

    def test_sync_is_hourly_stable_only_and_stops_on_risk(self):
        self.assertIn("cron: '17 * * * *'", self.sync)
        self.assertIn("releases/latest", self.sync)
        self.assertIn(".draft == false and .prerelease == false", self.sync)
        self.assertIn("--force-with-lease", self.sync)
        self.assertIn("upstream-review-required", self.sync)
        self.assertIn("gh pr merge \"$pr\" --auto --merge", self.sync)
        self.assertIn("/tmp/upstream-conflict.md", self.sync)

    def test_risk_gate_executes_only_trusted_code(self):
        self.assertIn("pull_request_target:", self.gate)
        self.assertIn("path: trusted", self.gate)
        self.assertIn("path: candidate", self.gate)
        self.assertIn("python3 trusted/.github/scripts/upstream_risk.py", self.gate)
        self.assertIn("upstream-reviewed", self.gate)

    def test_safe_merge_tags_once_and_handoff_waits_for_approval(self):
        self.assertIn("upstream-safe-candidate", self.release)
        self.assertIn("git ls-remote --exit-code --tags", self.release)
        self.assertIn("-codexrip.1", self.release)
        self.assertIn("operation=deploy-preserve", self.handoff)
        self.assertIn("environment:\n      name: production", self.production)
        self.assertIn("runtime=preserve", self.production)
        self.assertNotIn("pending_deployments", self.handoff)

    def test_failures_create_issues_without_retry_loops(self):
        self.assertIn("upstream-automation-failed", self.release)
        self.assertIn("upstream-automation-failed", self.handoff)
        self.assertNotIn("for attempt", self.release)
        self.assertNotIn("for attempt", self.handoff)


if __name__ == "__main__":
    unittest.main()

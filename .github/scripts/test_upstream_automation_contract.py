#!/usr/bin/env python3

import json
import re
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import call, patch
from upstream_promote import REQUIRED_CHECKS, ensure_release, promote, require_checks, validate_candidate


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

    def test_publisher_reuses_pr_validation_without_duplicate_business_tests(self):
        self.assertIn('git merge-base --is-ancestor "$source_sha" origin/main', self.publisher)
        self.assertIn('ref: ${{ env.RELEASE_TAG }}', self.publisher)
        self.assertIn('source_sha=$(git rev-parse HEAD)', self.publisher)
        self.assertIn('Build and publish image', self.publisher)
        self.assertIn('Attest image provenance', self.publisher)
        self.assertIn('--verify-tag', self.publisher)
        self.assertNotRegex(self.publisher, r'\bgo test\b|\bvitest\b|pnpm run typecheck')
        self.assertNotIn('.github/scripts/test_', self.publisher)

    def test_image_build_skips_validation_without_changing_daily_build(self):
        dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
        vite = (ROOT / "frontend/vite.config.ts").read_text(encoding="utf-8")
        package = json.loads((ROOT / "frontend/package.json").read_text(encoding="utf-8"))
        self.assertIn("RUN SUB2API_ARTIFACT_BUILD=1 pnpm exec vite build", dockerfile)
        self.assertNotIn("RUN pnpm run build", dockerfile)
        self.assertNotIn("ENV SUB2API_ARTIFACT_BUILD", dockerfile)
        self.assertIn("process.env.SUB2API_ARTIFACT_BUILD === '1'", vite)
        self.assertIn("enableBuild: !artifactBuild", vite)
        self.assertEqual(package["scripts"]["build"], "pnpm run check:i18n && vue-tsc -b && vite build")

    def test_bot_chain_is_explicit_and_does_not_overwrite_manual_resolutions(self):
        self.assertIn("upstream_tag:", self.sync)
        self.assertIn("preserve manual resolutions", self.sync)
        self.assertIn("upstream-conflict.json", self.sync)
        for workflow in ("backend-ci.yml", "security-scan.yml", "downstream-verify.yml", "upstream-risk-gate.yml"):
            self.assertIn(workflow, self.sync)
        self.assertIn('"downstream-release.yml"', self.promoter)
        self.assertIn("workflow_dispatch:", self.publisher)

    def test_reviewed_manual_release_does_not_trigger_auto_handoff_or_false_failure(self):
        self.assertIn('if [[ "$risk" == review_required ]]; then', self.handoff)
        manual_branch = self.handoff.split('if [[ "$risk" == review_required ]]; then', 1)[1].split("fi", 1)[0]
        self.assertIn("echo 'eligible=false'", manual_branch)
        self.assertIn("exit 0", manual_branch)
        self.assertNotIn("gh workflow", manual_branch)
        self.assertIn('[[ "$risk" == safe ]]', self.handoff)
        self.assertIn("echo 'eligible=true'", self.handoff)
        self.assertIn("if: steps.candidate.outputs.eligible == 'true'", self.handoff)

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


class UpstreamPromotionPollingTest(unittest.TestCase):
    tag = "v0.2.4-codexrip.1"
    sha = "a" * 40

    @staticmethod
    def check_payload(status):
        checks = [
            {"id": index + 1, "name": name, "status": "completed", "conclusion": "success"}
            for index, name in enumerate(sorted(REQUIRED_CHECKS))
        ]
        checks[0].update(status=status, conclusion="success" if status == "completed" else None)
        return json.dumps([{"check_runs": checks}])

    def release_run(self, status="queued", conclusion=None):
        return {"id": 17, "display_title": f"Release {self.tag}", "head_branch": "main",
                "status": status, "conclusion": conclusion, "html_url": "https://github.com/example/run/17"}

    def test_checks_wait_five_minutes_then_ten_until_state_changes(self):
        payloads = [self.check_payload(status) for status in ("queued", "queued", "in_progress", "completed")]
        with patch("upstream_promote.command", side_effect=payloads), \
             patch("upstream_promote.time.monotonic", return_value=0), \
             patch("upstream_promote.time.sleep") as sleep, patch("builtins.print"):
            require_checks(self.sha)
        self.assertEqual(sleep.call_args_list, [call(300), call(600), call(300)])

    def test_release_reuses_pending_run_and_backs_off_without_immediate_repoll(self):
        events = []
        runs = iter([self.release_run(), self.release_run(), self.release_run("in_progress")])
        readiness = iter([False, False, False, True])

        def read_release(*_):
            events.append("release")
            return next(readiness)

        def read_runs(*_):
            events.append("runs")
            return {"workflow_runs": [next(runs)]}

        with patch("upstream_promote.release_ready", side_effect=read_release), \
             patch("upstream_promote.api", side_effect=read_runs), \
             patch("upstream_promote.command") as command, \
             patch("upstream_promote.time.monotonic", return_value=0), \
             patch("upstream_promote.time.sleep", side_effect=events.append):
            ensure_release(self.tag, self.sha)
        command.assert_not_called()
        self.assertEqual(events, ["release", "runs", 300, "release", "runs", 600,
                                  "release", "runs", 300, "release"])

    def test_release_dispatches_once_or_reuses_ready_artifact(self):
        for ready in (False, True):
            with self.subTest(ready=ready), \
                 patch("upstream_promote.release_ready", side_effect=[True] if ready else [False, True]), \
                 patch("upstream_promote.api", return_value={"workflow_runs": []}) as api, \
                 patch("upstream_promote.command") as command, \
                 patch("upstream_promote.time.monotonic", return_value=0), \
                 patch("upstream_promote.time.sleep") as sleep:
                ensure_release(self.tag, self.sha)
                if ready:
                    api.assert_not_called()
                    command.assert_not_called()
                    sleep.assert_not_called()
                else:
                    command.assert_called_once_with("gh", "workflow", "run", "downstream-release.yml",
                        "--repo", "HTExplicit/sub2api", "--ref", "main", "-f", f"release_tag={self.tag}")
                    sleep.assert_called_once_with(300)

    def test_failed_release_is_not_redispatched(self):
        with patch("upstream_promote.release_ready", return_value=False), \
             patch("upstream_promote.api", return_value={"workflow_runs": [self.release_run("completed", "failure")]}), \
             patch("upstream_promote.command") as command, patch("upstream_promote.time.sleep") as sleep:
            with self.assertRaisesRegex(RuntimeError, "previous release run failed"):
                ensure_release(self.tag, self.sha)
        command.assert_not_called()
        sleep.assert_not_called()

    def test_merged_pr_reuses_checks_and_existing_tag_release_deployment(self):
        merged = "b" * 40
        manifest = {"upstream_tag": "v0.2.4", "upstream_base": "v0.2.3",
                    "downstream_commit": "c" * 40, "merge_conflicts": [], "risk_class": "review_required"}
        pr = {"head": {"sha": self.sha, "repo": {"full_name": "HTExplicit/sub2api"},
                       "ref": "sync/upstream-0.2.4"}, "base": {"ref": "main"},
              "labels": [{"name": "upstream-reviewed"}], "merged": True,
              "state": "closed", "merge_commit_sha": merged}

        def run_command(*args):
            if args[:2] == ("git", "show"):
                return json.dumps(manifest) if args[2].endswith("upstream-risk.json") else "v0.2.4"
            if args[:3] == ("gh", "api", "repos/Wei-Shaw/sub2api/releases/tags/v0.2.4"):
                return json.dumps({"draft": False, "prerelease": False})
            if args[:2] == ("git", "rev-list"):
                return merged
            return ""

        for existing in (False, True):
            def read_api(path, *args):
                if path == "pulls/152":
                    return pr
                if path == "git/refs":
                    return {}
                if path == "actions/workflows/production-deploy.yml/runs?per_page=100":
                    runs = [{"display_title": f"Deploy {self.tag} (preserve)", "status": "in_progress",
                             "conclusion": None, "html_url": "https://github.com/example/run/18"}] if existing else []
                    return {"workflow_runs": runs}
                self.fail(f"unexpected API call: {path}")

            with self.subTest(existing=existing), patch("upstream_promote.api", side_effect=read_api) as api, \
                 patch("upstream_promote.command", side_effect=run_command) as command, \
                 patch("upstream_promote.analyze", return_value={}), \
                 patch("upstream_promote.require_checks") as checks, \
                 patch("upstream_promote.ensure_release") as release, \
                 patch("upstream_promote.subprocess.run", return_value=SimpleNamespace(returncode=0 if existing else 1)), \
                 patch("builtins.print"):
                promote(152)
                checks.assert_called_once_with(self.sha)
                release.assert_called_once_with(self.tag, merged)
                self.assertFalse(any(c.args[:3] == ("gh", "pr", "merge") for c in command.call_args_list))
                creates = [c for c in api.call_args_list if c.args[0] == "git/refs"]
                self.assertEqual(len(creates), 0 if existing else 1)
                dispatches = [c for c in command.call_args_list if c.args[:3] == ("gh", "workflow", "run")]
                self.assertEqual(dispatches, [] if existing else [call("gh", "workflow", "run",
                    "production-deploy.yml", "--repo", "HTExplicit/sub2api", "--ref", "main", "-f",
                    "operation=deploy-preserve", "-f", f"release_tag={self.tag}", "-f", "confirmation=DEPLOY")])


if __name__ == "__main__":
    unittest.main()

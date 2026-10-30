#!/usr/bin/env python3
"""Check protected contexts and conditional execution without running workflows."""
from pathlib import Path
import re
import unittest

import ci_run
from upstream_promote import REQUIRED_CHECKS

ROOT = Path(__file__).resolve().parents[2]


class WorkflowSelectionTests(unittest.TestCase):
    def test_protected_contexts_keep_names_and_complete_without_path_filters(self):
        found = set()
        for filename in ("backend-ci.yml", "downstream-verify.yml", "security-scan.yml"):
            source = (ROOT / ".github/workflows" / filename).read_text(encoding="utf-8")
            found.update(re.findall(r"(?m)^    name: (.+)$", source))
            self.assertNotRegex(source, r"(?m)^\s+paths(?:-ignore)?:")
            self.assertIn("  workflow_dispatch:", source)
            self.assertIn("  actions: read", source)
            self.assertNotIn("- '**'", source)
            self.assertNotIn("sync/**", source)
            self.assertEqual(len(re.findall(r"(?m)^  [a-z-]+:\n    name:", source)), source.count("uses: ./.github/actions/selective-ci"))
        self.assertEqual(REQUIRED_CHECKS - {"Upstream risk gate"}, found - {"release-helpers"})
        self.assertIn("    runs-on: macos-15-intel", (ROOT / ".github/workflows/backend-ci.yml").read_text())

    def test_dependency_setup_and_image_build_are_selected_not_global(self):
        source = (ROOT / ".github/actions/selective-ci/action.yml").read_text(encoding="utf-8")
        for feature in ("needs_go", "needs_node", "needs_lint", "needs_candidate", "needs_renderer"):
            self.assertIn("steps.plan.outputs." + feature + " == 'true'", source)
        self.assertIn("push: false", source)
        self.assertNotIn("docker/login", source)
        self.assertIn("LINT_OUTCOME: ${{ steps.lint.outcome }}", source)
        self.assertIn("BUILD_OUTCOME: ${{ steps.build.outcome }}", source)
        self.assertIn("if: always() && steps.plan.outcome == 'success'", source)
        self.assertIn("name: ci-receipt-${{ inputs.context }}", source)

    def test_upstream_gate_ordinary_pr_has_real_result_and_candidate_stays_trusted(self):
        source = (ROOT / ".github/workflows/upstream-risk-gate.yml").read_text(encoding="utf-8")
        self.assertNotRegex(source, r"(?m)^    if:")
        self.assertIn("not_applicable: this PR is not an upstream candidate", source)
        self.assertEqual(3, source.count("if: steps.pr.outputs.candidate == 'true'"))
        self.assertIn("ref: main", source)
        self.assertIn("python3 trusted/.github/scripts/upstream_risk.py", source)
        sync = (ROOT / ".github/workflows/upstream-sync.yml").read_text(encoding="utf-8")
        self.assertIn("backend-ci.yml security-scan.yml downstream-verify.yml", sync)

    def test_every_executable_group_has_one_owner_and_runner_implementation(self):
        self.assertEqual(set(ci_run.GO_GROUPS) - set().union(*ci_run.OWNERS.values()), set())
        self.assertEqual({"backend_lint", "candidate_image", "renderer"}, ci_run.EXTERNAL)


if __name__ == "__main__":
    unittest.main()

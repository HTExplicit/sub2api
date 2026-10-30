#!/usr/bin/env python3
"""Offline execution and result-state boundaries; never invoke business tests."""
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import ci_run


class SelectedExecutionTests(unittest.TestCase):
    def test_pr_range_uses_base_and_actual_head_metadata(self):
        event = {"pull_request": {"base": {"sha": "a" * 40}, "head": {"sha": "b" * 40}}}
        self.assertEqual(("a" * 40, "a" * 40, "b" * 40), ci_run.event_range(event, "pull_request", "c" * 40))
        with self.assertRaises(ValueError):
            ci_run.event_range({"before": "0" * 40}, "push", "a" * 40)

    def test_required_owners_do_not_duplicate_groups(self):
        groups = [group for owner in ci_run.OWNERS.values() for group in owner]
        self.assertEqual(len(groups), len(set(groups)))
        with self.assertRaises(ValueError):
            ci_run.selected_groups({"groups": ["new_unreviewed_suite"]}, "test")

    def test_frontend_related_names_direct_specs_and_sources_once(self):
        plan = {"frontend_tests": ["src/a.spec.ts"], "frontend_related": ["src/a.ts", "src/a.ts"]}
        with patch.object(ci_run, "command") as run:
            ci_run.run_group("frontend_focused", plan)
        # Vitest 2 includes the test itself as well as its dependency graph.
        run.assert_called_once_with(["pnpm", "exec", "vitest", "related", "--run", "src/a.spec.ts", "src/a.ts"], "frontend")
        self.assertNotIn("--passWithNoTests", run.call_args.args[0])

    def test_security_scans_only_affected_packages_unless_scheduled(self):
        with patch.object(ci_run, "command") as run:
            ci_run.run_group("backend_security", {"backend_packages": ["./internal/pkg/one"]})
            self.assertEqual(["govulncheck", "./internal/pkg/one"], run.call_args.args[0])
        with self.assertRaises(ValueError), patch.object(ci_run, "command"):
            ci_run.run_group("backend_security", {})

    def test_license_is_plain_text_and_deleted_doc_links_are_checked(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "LICENSE").write_text("GNU LGPL\n", encoding="utf-8", newline="\n")
            (root / "README.md").write_text("# Current\n\n[old](old.md)\n", encoding="utf-8", newline="\n")
            with patch.object(ci_run, "ROOT", root), patch.object(ci_run, "git", return_value="README.md"):
                ci_run.static_docs({"static_files": ["LICENSE"]})
                with self.assertRaisesRegex(ValueError, "link_to_removed_document"):
                    ci_run.static_docs({"deleted_static_files": ["old.md"]})

    def test_race_covers_duplicate_normal_function_once(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / "backend/internal/service"
            directory.mkdir(parents=True)
            (directory / "pelican_test.go").write_text("//go:build unit\npackage service\nfunc TestPelicanOrdinary(t *testing.T) {}\nfunc TestPelicanRepeatModelOne(t *testing.T) {}\n", encoding="utf-8")
            plan = {"groups": ["pelican", "pelican_race"]}
            with patch.object(ci_run, "ROOT", root):
                ci_run.resolve_go_checks(plan)
            ordinary = plan["go_checks"]["pelican"][0]["pattern"]
            raced = plan["go_checks"]["pelican_race"][0]["pattern"]
            self.assertIn("TestPelicanOrdinary", ordinary)
            self.assertNotIn("TestPelicanRepeatModelOne", ordinary)
            self.assertIn("TestPelicanRepeatModelOne", raced)

    def test_empty_mapping_fails_instead_of_go_zero_test_success(self):
        with tempfile.TemporaryDirectory() as temporary, patch.object(ci_run, "ROOT", Path(temporary)):
            with self.assertRaisesRegex(ValueError, "matches_no_functions"):
                ci_run.resolve_go_checks({"groups": ["pelican"]})

    def test_embed_and_negative_unit_tags_follow_selected_build(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / "backend/internal/web"
            directory.mkdir(parents=True)
            for name, tag in (("embedded", "embed"), ("plain", "!unit")):
                (directory / (name + "_test.go")).write_text(f"//go:build {tag}\npackage web\nfunc Test{name}(t *testing.T) {{}}\n", encoding="utf-8", newline="\n")
            definitions = {"fixture": (["./internal/web"], "^Test", ("unit", "embed"), False)}
            with patch.object(ci_run, "ROOT", root), patch.dict(ci_run.GO_GROUPS, definitions):
                plan = {"groups": ["fixture"]}
                ci_run.resolve_go_checks(plan)
            self.assertIn("Testembedded", plan["go_checks"]["fixture"][0]["pattern"])
            self.assertNotIn("Testplain", plan["go_checks"]["fixture"][0]["pattern"])

    def test_failed_external_step_cannot_record_success(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            ci_run.write_json(directory / "plan.json", {"groups": ["candidate_image"]})
            ci_run.write_json(directory / "execution.json", {"context": "Candidate OCI image", "pending": ["candidate_image"]})
            with patch.dict(os.environ, {"BUILD_OUTCOME": "failure"}, clear=True), patch.object(ci_run.ci_receipts, "record") as record:
                with self.assertRaisesRegex(ValueError, "incomplete"):
                    ci_run.finish(directory)
                record.assert_not_called()
            self.assertEqual("failed", json.loads((directory / "results.json").read_text())["groups"]["candidate_image"]["state"])


if __name__ == "__main__":
    unittest.main()

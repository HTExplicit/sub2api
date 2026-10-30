#!/usr/bin/env python3
"""Offline fixtures for change selection; never starts applications or Go tests."""
import json
from pathlib import Path
import tempfile
import unittest

import ci_plan


class ChangeSelectionTests(unittest.TestCase):
    def test_docs_only_select_static_checks(self):
        plan = ci_plan.classify_paths(["DOWNSTREAM.md", ".downstream/native-domains.md"])
        self.assertTrue(plan["passed"], plan["failures"])
        self.assertEqual(["static_docs"], plan["groups"])
        self.assertFalse(plan["backend_tests"])
        self.assertFalse(plan["frontend_typecheck"])

    def test_legal_markdown_is_a_build_input(self):
        plan = ci_plan.classify_paths(["docs/legal/admin-compliance.en.md"])
        self.assertTrue(plan["passed"])
        self.assertEqual(["candidate_image", "frontend_validate", "static_docs"], plan["groups"])

    def test_runtime_notices_remain_a_candidate_image_input(self):
        plan = ci_plan.classify_paths(["THIRD_PARTY_NOTICES.md"])
        self.assertTrue(plan["passed"])
        self.assertEqual(["candidate_image", "static_docs"], plan["groups"])

    def test_docker_readme_uses_generic_release_without_main_image_build(self):
        plan = ci_plan.classify_paths(["deploy/DOCKER.md"])
        self.assertTrue(plan["passed"])
        self.assertEqual(["generic_release_contracts", "static_docs"], plan["groups"])

    def test_unresolved_tags_or_helper_only_go_tests_block(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            path = root / "backend/internal/service/pelican_fixture_test.go"
            path.parent.mkdir(parents=True)
            for content in (
                "//go:build unit && !special\npackage service\nimport \"testing\"\nfunc TestScoped(t *testing.T) {}\n",
                "//go:build unit\npackage service\nfunc fixture() {}\n",
            ):
                with self.subTest(content=content):
                    path.write_text(content, encoding="utf-8")
                    plan = ci_plan.classify_paths([path.relative_to(root).as_posix()], root)
                    self.assertFalse(plan["passed"])
                    self.assertTrue(plan["failures"][0].startswith("test_selection_requires_mapping:"))
                    self.assertFalse(plan["backend_tests"])

    def test_unknown_source_or_dependency_blocks_without_broad_fallback(self):
        for path in ("backend/internal/service/new_gateway.go",
                     "frontend/vite.config.ts", "deploy/new-helper.sh",
                     "Dockerfile.goreleaser"):
            with self.subTest(path=path):
                plan = ci_plan.classify_paths([path])
                self.assertFalse(plan["passed"])
                self.assertIn("unclassified_input:" + path, plan["failures"])
                self.assertFalse(plan["backend_tests"])

    def test_go_dependency_graph_selects_security_and_build_without_business_suite(self):
        plan = ci_plan.classify_paths(["backend/go.mod", "backend/go.sum"])
        self.assertTrue(plan["passed"], plan["failures"])
        self.assertEqual(["backend_security", "candidate_image"], plan["groups"])
        self.assertEqual(["./..."], plan["backend_packages"])
        self.assertTrue(plan["backend_dependency_graph"])
        self.assertFalse(plan["backend_lint_packages"])
        self.assertFalse(plan["backend_tests"])

    def test_node_dependency_graph_selects_security_typecheck_and_build_without_history(self):
        plan = ci_plan.classify_paths(["frontend/package.json", "frontend/pnpm-lock.yaml"])
        self.assertTrue(plan["passed"], plan["failures"])
        self.assertEqual(["candidate_image", "frontend_security", "frontend_validate"], plan["groups"])
        self.assertTrue(plan["frontend_typecheck"])
        self.assertFalse(plan["frontend_tests"])
        self.assertFalse(plan["frontend_related"])

    def test_shared_entry_cannot_claim_sibling_tests_are_complete(self):
        plan = ci_plan.classify_paths(["backend/internal/service/account_job.go",
                                       "backend/internal/service/openai_gateway_service.go"])
        self.assertFalse(plan["passed"])
        self.assertIn("account_jobs", plan["groups"])
        self.assertIn("unclassified_input:backend/internal/service/openai_gateway_service.go", plan["failures"])

    def test_explicit_test_file_selects_only_its_actual_functions(self):
        path = "backend/internal/service/account_job_test.go"
        plan = ci_plan.classify_paths([path])
        self.assertTrue(plan["passed"], plan["failures"])
        self.assertEqual(["backend_focused"], plan["groups"])
        self.assertEqual(["./internal/service"], plan["backend_tests"][0]["packages"])
        self.assertIn("TestAccountJobSubmitRequiresIdempotencyKeyAndEncryptsPayload", plan["backend_tests"][0]["pattern"])
        self.assertEqual(["unit"], plan["backend_tests"][0]["tags"])
        self.assertFalse(plan["backend_tests"][0]["race"])

    def test_db_and_concurrency_are_opt_in_by_source_risk(self):
        ordinary = ci_plan.classify_paths(["backend/internal/service/account_job.go"])
        self.assertTrue(ordinary["passed"])
        self.assertFalse(any(group.endswith("_race") or group.endswith("_db") for group in ordinary["groups"]))
        database = ci_plan.classify_paths(["backend/migrations/281_pelican_repeat_samples.sql"])
        self.assertTrue(database["passed"])
        self.assertIn("pelican_db", database["groups"])
        self.assertIn("candidate_image", database["groups"])
        concurrency = ci_plan.classify_paths(["backend/internal/service/account_test_sse_keepalive.go"])
        self.assertTrue(concurrency["passed"])
        self.assertIn("keepalive_race", concurrency["groups"])

    def test_embedded_transport_reference_is_a_runtime_build_input(self):
        plan = ci_plan.classify_paths(["backend/internal/pkg/codextransport/reference.json"])
        self.assertTrue(plan["passed"])
        self.assertEqual(["candidate_image", "transport"], plan["groups"])

    def test_go_source_selects_reachability_scan_but_test_only_does_not(self):
        source = ci_plan.classify_paths(["backend/internal/service/account_job.go"])
        self.assertTrue(source["passed"])
        self.assertIn("backend_security", source["groups"])
        self.assertEqual(["./internal/service"], source["backend_packages"])
        test = ci_plan.classify_paths(["backend/internal/service/account_job_test.go"])
        self.assertTrue(test["passed"])
        self.assertNotIn("backend_security", test["groups"])

    def test_model_list_and_turn_state_have_independent_risks(self):
        models = ci_plan.classify_paths(["backend/internal/handler/openai_models_handler.go"])
        self.assertTrue(models["passed"])
        self.assertIn("model_list", models["groups"])
        self.assertNotIn("turn_state", models["groups"])
        self.assertNotIn("models", models["groups"])
        turn = ci_plan.classify_paths(["backend/internal/service/openai_codex_turn_state.go"])
        self.assertTrue(turn["passed"])
        self.assertIn("turn_state", turn["groups"])
        self.assertNotIn("model_list", turn["groups"])
        self.assertNotIn("models", turn["groups"])

    def test_frontend_related_and_explicit_test_are_separate_inputs(self):
        source = "frontend/src/components/common/BaseDialog.vue"
        test = "frontend/src/components/common/__tests__/BaseDialog.spec.ts"
        plan = ci_plan.classify_paths([source, test])
        self.assertTrue(plan["passed"])
        self.assertEqual([source.removeprefix("frontend/")], plan["frontend_related"])
        self.assertEqual([test.removeprefix("frontend/")], plan["frontend_tests"])
        self.assertTrue(plan["frontend_typecheck"])
        self.assertIn("candidate_image", plan["groups"])
        test_only = ci_plan.classify_paths([test])
        self.assertNotIn("candidate_image", test_only["groups"])
        self.assertFalse(test_only["frontend_typecheck"])

    def test_ci_definition_changes_select_only_offline_contracts(self):
        plan = ci_plan.classify_paths([".github/scripts/ci_plan.py",
                                       ".github/workflows/downstream-verify.yml",
                                       ".github/actions/selective-ci/action.yml"])
        self.assertTrue(plan["passed"])
        self.assertEqual(["ci_contracts"], plan["groups"])

    def test_deploy_source_selects_real_minimal_scripts(self):
        plan = ci_plan.classify_paths(["deploy/Caddyfile"])
        self.assertTrue(plan["passed"])
        self.assertEqual(["deploy/test-caddyfile-cache.sh"], plan["deploy_tests"])

    def test_generic_release_tools_have_their_own_offline_contract(self):
        plan = ci_plan.classify_paths([".github/release-tools/release_matrix.py", ".goreleaser.yaml"])
        self.assertTrue(plan["passed"])
        self.assertEqual(["generic_release_contracts"], plan["groups"])

    def test_nul_diff_preserves_both_sides_of_rename_and_delete(self):
        self.assertEqual(["docs/new.md", "docs/old.md", "gone.md"], ci_plan.parse_name_status(
            b"R100\0docs/old.md\0docs/new.md\0D\0gone.md\0"))
        with self.assertRaisesRegex(ValueError, "incomplete_diff"):
            ci_plan.parse_name_status(b"R100\0docs/old.md\0")

    def test_path_escape_is_rejected(self):
        self.assertFalse(ci_plan.classify_paths(["../outside.py"])["passed"])

    def test_direct_reverse_imports_are_compile_only_and_not_transitive(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for name, content in {
                "backend/go.mod": "module example.test/app\n",
                "backend/internal/pkg/requestmodel/model.go": "package requestmodel\n",
                "backend/internal/pkg/requestmodel/model_test.go": 'package requestmodel\nimport "testing"\nfunc TestModel(t *testing.T) {}\n',
                "backend/internal/direct/direct.go": 'package direct\nimport "example.test/app/internal/pkg/requestmodel"\n',
                "backend/internal/transitive/transitive.go": 'package transitive\nimport "example.test/app/internal/direct"\n',
            }.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content, encoding="utf-8")
            self.assertEqual(["./internal/direct"], ci_plan.direct_importers(["./internal/pkg/requestmodel"], root))
            plan = ci_plan.classify_paths(["backend/internal/pkg/requestmodel/model.go"], root)
            self.assertTrue(plan["passed"], plan["failures"])
            self.assertEqual(["./internal/direct"], plan["backend_compile_packages"])
            self.assertIn("backend_compile", plan["groups"])
            self.assertNotIn("./internal/transitive", plan["backend_compile_packages"])

    def test_definition_hash_ignores_receipts_but_tracks_policy_and_workflow(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / ".github/scripts").mkdir(parents=True)
            (root / ".github/workflows").mkdir(parents=True)
            (root / ".github/scripts/ci_plan.py").write_text("definition\n")
            receipt = root / ".github/scripts/receipt.json"
            receipt.write_text(json.dumps({"result": "executed"}))
            initial = ci_plan.definition_digest(root)
            receipt.write_text(json.dumps({"result": "failed"}))
            self.assertEqual(initial, ci_plan.definition_digest(root))
            (root / ".github/workflows/ci.yml").write_text("name: CI\n")
            self.assertNotEqual(initial, ci_plan.definition_digest(root))


if __name__ == "__main__":
    unittest.main()

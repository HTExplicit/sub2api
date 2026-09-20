#!/usr/bin/env python3

import unittest
import subprocess
from pathlib import Path
from tempfile import TemporaryDirectory, gettempdir

from upstream_risk import analyze, classify, is_critical


class UpstreamRiskTest(unittest.TestCase):
    def test_zero_overlap_noncritical_is_safe(self):
        result = classify(["README.md", "backend/internal/domain/model.go"], ["frontend/src/App.vue"])
        self.assertEqual(result["risk_class"], "safe")
        self.assertEqual(result["overlap_file_count"], 0)
        self.assertEqual(result["critical_file_count"], 0)

    def test_overlap_requires_review(self):
        result = classify(["frontend/src/App.vue"], ["frontend/src/App.vue"])
        self.assertEqual(result["risk_class"], "review_required")
        self.assertEqual(result["overlap_files"], ["frontend/src/App.vue"])

    def test_every_guarded_family_is_critical(self):
        for path in (
            ".github/workflows/ci.yml",
            "Dockerfile",
            "deploy/docker-compose.yml",
            "backend/go.mod",
            "frontend/pnpm-lock.yaml",
            "backend/migrations/999_change.sql",
            "backend/ent/schema/account.go",
            "backend/internal/repository/ent.go",
            "backend/internal/securityaudit/policy.go",
            "backend/internal/service/billing_service.go",
            "backend/internal/service/openai_ws_pool.go",
            "backend/internal/service/plugin_update.go",
            "backend/pkg/extensionapi/v1/rpc.go",
            "backend/pkg/pluginapi/v1/plugin.proto",
            "plugins/codex-runtime/manifest.source.json",
        ):
            with self.subTest(path=path):
                self.assertTrue(is_critical(path))

    def test_real_git_unicode_workflow_path_cannot_become_safe(self):
        with TemporaryDirectory(prefix="sub2api-risk-paths-") as temporary:
            repo = Path(temporary).resolve()
            self.assertEqual(repo.parent, Path(gettempdir()).resolve())
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.PIPE)
            git("init", "-q")
            (repo / "README.md").write_text("fixture\n", encoding="utf-8")
            git("add", "README.md")
            git("-c", "user.name=Risk fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "base")
            git("tag", "v1.0.0")
            workflow = ".github/workflows/发布.yml"
            (repo / workflow).parent.mkdir(parents=True)
            (repo / workflow).write_text("name: fixture\n", encoding="utf-8")
            git("add", workflow)
            git("-c", "user.name=Risk fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "candidate")
            git("tag", "v1.1.0")
            result = analyze(repo, "v1.0.0", "v1.1.0", "v1.0.0")
            self.assertEqual(result["critical_files"], [workflow])
            self.assertEqual(result["risk_class"], "review_required")


if __name__ == "__main__":
    unittest.main()

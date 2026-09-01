#!/usr/bin/env python3

import unittest

from upstream_risk import classify, is_critical


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
        ):
            with self.subTest(path=path):
                self.assertTrue(is_critical(path))


if __name__ == "__main__":
    unittest.main()

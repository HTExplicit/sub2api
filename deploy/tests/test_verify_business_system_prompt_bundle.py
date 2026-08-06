from __future__ import annotations

import hashlib
import json
import stat
import sys
import tempfile
import unittest
import warnings
import zipfile
from pathlib import Path
from typing import Optional


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "tools"))

from verify_business_system_prompt_bundle import (  # noqa: E402
    ASSET_NAME,
    CHECKSUM_NAME,
    EXPECTED_MANIFEST_FILE_COUNT,
    EXPECTED_ZIP_ENTRY_COUNT,
    MANIFEST_SHA256,
    ZIP_SHA256,
    VerificationError,
    verify_bundle,
)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class BundleFixture:
    def __init__(
        self,
        root: Path,
        files: list[tuple[str, bytes]],
        *,
        sha_overrides: Optional[dict[str, str]] = None,
        length_overrides: Optional[dict[str, int]] = None,
    ):
        self.root = root
        self.files = files
        overrides = sha_overrides or {}
        lengths = length_overrides or {}
        self.manifest_bytes = json.dumps(
            {
                "schema_version": 1,
                "bundle_id": "test-bundle",
                "version": "test",
                "core": files[0][0],
                "files": [
                    {
                        "path": name,
                        "sha256": overrides.get(name, sha256(data)),
                        "byte_length": lengths.get(name, len(data)),
                        "kind": "text",
                        "required": True,
                    }
                    for name, data in files
                ],
                "domains": [],
            },
            ensure_ascii=False,
            indent=2,
        ).encode("utf-8")
        self.manifest_path = root / "bundle-manifest.json"
        self.manifest_path.write_bytes(self.manifest_bytes)
        self.zip_path = root / "bundle.zip"

    def write_zip(self, *, duplicate: bool = False, symlink: bool = False) -> None:
        with zipfile.ZipFile(self.zip_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            archive.writestr("bundle-manifest.json", self.manifest_bytes)
            for name, data in self.files:
                if symlink:
                    info = zipfile.ZipInfo(name)
                    info.create_system = 3
                    info.external_attr = (stat.S_IFLNK | 0o777) << 16
                    archive.writestr(info, data)
                else:
                    archive.writestr(name, data)
                if duplicate:
                    with warnings.catch_warnings():
                        warnings.simplefilter("ignore", UserWarning)
                        archive.writestr(name, data)

    def verify(self, *, entry_count: Optional[int] = None, checksum_path: Optional[Path] = None):
        return verify_bundle(
            zip_path=self.zip_path,
            manifest_path=self.manifest_path,
            expected_zip_sha256=sha256(self.zip_path.read_bytes()),
            expected_manifest_sha256=sha256(self.manifest_bytes),
            expected_manifest_file_count=len(self.files),
            expected_zip_entry_count=entry_count if entry_count is not None else len(self.files) + 1,
            checksum_path=checksum_path,
        )


class VerifyBusinessSystemPromptBundleTests(unittest.TestCase):
    def test_accepts_exact_safe_archive(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(Path(temp), [("RULES.md", b"rules"), ("references/auth.md", b"auth")])
            fixture.write_zip()
            result = fixture.verify()

            self.assertEqual(result.bundle_id, "test-bundle")
            self.assertEqual(result.file_count, 2)
            self.assertEqual(result.entry_count, 3)

    def test_rejects_duplicate_archive_entries(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(Path(temp), [("RULES.md", b"rules")])
            fixture.write_zip(duplicate=True)

            with self.assertRaisesRegex(VerificationError, "duplicate"):
                fixture.verify(entry_count=3)

    def test_rejects_portable_name_collisions(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(Path(temp), [("RULES.md", b"one"), ("rules.md", b"two")])
            fixture.write_zip()

            with self.assertRaisesRegex(VerificationError, "cross-platform"):
                fixture.verify()

    def test_rejects_unsafe_paths(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(Path(temp), [("../escape.md", b"escape")])
            fixture.write_zip()

            with self.assertRaisesRegex(VerificationError, "unsafe"):
                fixture.verify()

    def test_rejects_symlink_entries(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(Path(temp), [("RULES.md", b"target")])
            fixture.write_zip(symlink=True)

            with self.assertRaisesRegex(VerificationError, "symlink"):
                fixture.verify()

    def test_rejects_manifest_content_hash_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(
                Path(temp),
                [("RULES.md", b"rules")],
                sha_overrides={"RULES.md": "0" * 64},
            )
            fixture.write_zip()

            with self.assertRaisesRegex(VerificationError, "content SHA-256"):
                fixture.verify()

    def test_rejects_manifest_byte_length_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(
                Path(temp),
                [("RULES.md", b"rules")],
                length_overrides={"RULES.md": 4},
            )
            fixture.write_zip()

            with self.assertRaisesRegex(VerificationError, "byte length"):
                fixture.verify()

    def test_checksum_must_name_and_match_the_archive(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(Path(temp), [("RULES.md", b"rules")])
            fixture.write_zip()
            checksum_path = Path(temp) / "bundle.zip.sha256"
            checksum_path.write_text(f"{'0' * 64}  bundle.zip\n", encoding="ascii")

            with self.assertRaisesRegex(VerificationError, "checksum"):
                fixture.verify(checksum_path=checksum_path)

    def test_rejects_unexpected_bundle_identity(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = BundleFixture(Path(temp), [("RULES.md", b"rules")])
            fixture.write_zip()

            with self.assertRaisesRegex(VerificationError, "bundle_id mismatch"):
                verify_bundle(
                    zip_path=fixture.zip_path,
                    manifest_path=fixture.manifest_path,
                    expected_zip_sha256=sha256(fixture.zip_path.read_bytes()),
                    expected_manifest_sha256=sha256(fixture.manifest_bytes),
                    expected_manifest_file_count=1,
                    expected_zip_entry_count=2,
                    expected_bundle_id="other-bundle",
                )

    def test_checked_in_release_asset_matches_fixed_contract(self) -> None:
        bundle_dir = REPO_ROOT / "deploy" / "skill-bundles" / "moxinggang-reverse-skill"
        result = verify_bundle(
            zip_path=bundle_dir / ASSET_NAME,
            manifest_path=bundle_dir / "bundle-manifest.json",
            expected_zip_sha256=ZIP_SHA256,
            expected_manifest_sha256=MANIFEST_SHA256,
            expected_manifest_file_count=EXPECTED_MANIFEST_FILE_COUNT,
            expected_zip_entry_count=EXPECTED_ZIP_ENTRY_COUNT,
            expected_bundle_id="moxinggang-reverse-skill",
            checksum_path=bundle_dir / CHECKSUM_NAME,
        )

        self.assertEqual(result.file_count, 538)
        self.assertEqual(result.entry_count, 539)


if __name__ == "__main__":
    unittest.main()

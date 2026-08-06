# Moxinggang reverse-skill bundle

`bundle-manifest.json` describes the external reconstructed package pinned at
upstream commit `d8bf34540cbc1aa34052e1b142576fc36a1f1437`.

- Bundle ID: `moxinggang-reverse-skill`
- Manifest SHA-256: `22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7`
- Manifest contents: 538 files and 39 deterministic domain routes
- Release ZIP: `moxinggang-reverse-skill-22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7.zip`
- Release ZIP SHA-256: `977de70881ef67f15aa804f9cfa3e1a93ba441b46bb4bda1e30c4b4dd07a1c6a`
- Checksum asset: `moxinggang-reverse-skill-22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7.zip.sha256`

The 538 package files are deliberately not embedded in the application image.
Deployment copies the reconstructed package and this manifest to the
content-addressed host directory:

```text
/opt/sub2api/skill-bundles/moxinggang-reverse-skill/22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7
```

That directory is mounted read-only at the matching `/app/skill-bundles/...`
path. Runtime loading is filesystem-only; bundle scripts and binary assets are
verified but never executed by Sub2API.

The deterministic Release ZIP contains the manifest plus exactly the 538 files
declared by it. CI, downstream release, and production resolve use the same
cross-platform verifier. It rejects unsafe paths, duplicate or portable-name
collisions, symlink/special entries, an unexpected 539-entry set, and any byte
length or content hash mismatch. Run the full local gate with Python 3:

```text
python tools/verify_business_system_prompt_bundle.py
```

The verifier pins both the ZIP and manifest SHA-256 values in source. The
published `.sha256` file is verified against those pins and the deterministic
asset name; it is not used as a substitute trust anchor.

Regenerate the manifest with PowerShell 7:

```powershell
./tools/generate-business-system-prompt-bundle-manifest.ps1 `
    -BundleRoot <reconstructed-package-root> `
    -OutputPath ./deploy/skill-bundles/moxinggang-reverse-skill/bundle-manifest.json
```

The generated digest is a database and cache contract. Changing it requires a
new content-addressed directory plus the corresponding seed/migration update.

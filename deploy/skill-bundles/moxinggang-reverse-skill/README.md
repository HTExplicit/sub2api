# Moxinggang reverse-skill bundle

`bundle-manifest.json` describes the external reconstructed package pinned at
upstream commit `d8bf34540cbc1aa34052e1b142576fc36a1f1437`.

- Bundle ID: `moxinggang-reverse-skill`
- Manifest SHA-256: `22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7`
- Manifest contents: 538 files and 39 deterministic domain routes

The 538 package files are deliberately not embedded in the application image.
Deployment copies the reconstructed package and this manifest to the
content-addressed host directory:

```text
/opt/sub2api/skill-bundles/moxinggang-reverse-skill/22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7
```

That directory is mounted read-only at the matching `/app/skill-bundles/...`
path. Runtime loading is filesystem-only; bundle scripts and binary assets are
verified but never executed by Sub2API.

Regenerate the manifest with PowerShell 7:

```powershell
./tools/generate-business-system-prompt-bundle-manifest.ps1 `
    -BundleRoot <reconstructed-package-root> `
    -OutputPath ./deploy/skill-bundles/moxinggang-reverse-skill/bundle-manifest.json
```

The generated digest is a database and cache contract. Changing it requires a
new content-addressed directory plus the corresponding seed/migration update.

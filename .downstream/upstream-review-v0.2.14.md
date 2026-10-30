# Official v0.2.14 integration

Historical review; current contracts are in [DOWNSTREAM](../DOWNSTREAM.md).

- Official tag: `v0.2.14`, commit `0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`; downstream start: `7132011d7a4275e7d6407c92db4ec205a1c14ad1`.
- An unchanged-tree ours merge of `b8dece9000c68815a5b867ca5a1e6f236e173905` records already integrated v0.2.13 ancestry.
- Three conflicts involved frontend dependencies, lockfile and audit exceptions. Official security constraints were retained with patched SheetJS 0.20.3 and no renewed XLSX exceptions.
- Adopt official security fixes and remote Codex API-key discovery, skipping administrator credentials and preserving payment reconciliation. Source VERSION was corrected to 0.2.14; no unreleased feature or database migration was imported.
- Historical SQL, migration 274, runtime configuration and independent UI behavior were unchanged.

Recorded validation covered affected setup/EasyPay/return-URL and mounted Codex configuration cases plus normal required PR checks. Source/package checks did not prove deployment or live model/payment effects; the operator production pointer owns those results.

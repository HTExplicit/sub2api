# Official v0.2.14 integration

- Official tag: `v0.2.14`, commit `0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`.
- Downstream starting commit: `7132011d7a4275e7d6407c92db4ec205a1c14ad1`.
- The already integrated v0.2.13 ancestry is recorded by an ours merge of
  `b8dece9000c68815a5b867ca5a1e6f236e173905`; this marker changes no files.
- The three conflicts are the frontend package, lockfile, and audit exceptions.
  Vue uses `^3.5.43`; upstream DOMPurify/source-map-js constraints and downstream
  constraints are retained. XLSX remains the patched SheetJS CDN 0.20.3 artifact,
  with no renewed XLSX exceptions. The final lockfile installs frozen.
- Security fixes and remote Codex API-key model discovery follow the official tag.
  Existing administrator credentials are skipped, and existing payment reconcile
  paths remain. File catalogs, the 1 MiB fallback, auth/WS and source-bound capacity
  contracts remain. Runtime configuration and UI styling are unchanged.
- VERSION metadata is synchronized to 0.2.14; the upstream release tag itself
  retains the older fallback value. No unreleased upstream feature is imported.
- Historical SQL, migration 274 and Ent schemas are unchanged. No new database
  migration is introduced. Retired acceptance/plugin/resource/rollback paths
  remain retired. The current interfaces are in [DOWNSTREAM](../DOWNSTREAM.md).

Validation is the affected setup/EasyPay/return URL cases and mounted Codex
configuration scenarios, plus the normal required PR checks. Source/package
checks do not prove deployment or live model/payment effects. Production status
remains owned by the operator workspace's production pointer.

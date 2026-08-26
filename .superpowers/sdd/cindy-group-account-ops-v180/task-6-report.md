# Task 6 Report

## Status

Implemented Cindy admin UI integration on the existing full account console.

## Changes

- Added Cindy to account/group platform types, concrete/group catalogs, icons,
  channel platform reconstruction, and English/Chinese admin translations.
- Added the Cindy group-row action linking to `/admin/accounts` with the exact
  `platforms=cindy&cindy_only=true&group_id=<id>` query contract.
- Standardized account console route, list, export, taxonomy, and probe filters
  on `group_id`; added independent Cindy banned metrics, filter state, preview,
  confirmation, and account-job cleanup flow beside insufficient balance.
- Added server import preview API usage, strict Cindy target-group selection,
  redacted per-item preview labels, execution-time revalidation, and task-drawer
  job handoff. The backend plan ruling intentionally keeps preview informational
  and reuses the decision engine at execution rather than adding a token or
  fingerprint gate.
- Preserved the existing table/compact/card surfaces and bounded the preview
  table in both axes for narrow viewports.

## Verification

```text
pnpm typecheck
pnpm exec eslint <all changed frontend files>
pnpm test:run -- <13 focused Task 6 test files>
git diff --check
```

Focused frontend result: 13 test files, 44 tests passed. No full frontend suite
or browser production build was run locally; CI remains the full matrix gate.

## Deferred / ruled behavior

- Import preview and commit intentionally do not exchange a stale-preview token
  or fingerprint. Both paths call the same decision engine, and execution
  revalidates every item before mutation, per the approved plan ruling.

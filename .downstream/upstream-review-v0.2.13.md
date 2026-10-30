# Upstream v0.2.13 integration

Historical review of v0.2.13-codexrip.1. Current behavior and migration restrictions are in [DOWNSTREAM](../DOWNSTREAM.md) and [stored data and rollback](native-domains.md#release-and-rollback-boundary).

## Source

- Official v0.2.13: `3040209f205472038c1ba745a1bedd2edd9053b1`; tag object `7d0c0067f406c380f0a94cfc3879cdae7049b467`. The merged VERSION-only follow-up is `b8dece9000c68815a5b867ca5a1e6f236e173905`.
- Downstream base: `291d25e7f8a8c82d45072a1eee742c21ba3b8d21` (v0.2.11-codexrip.9); integration and follow-ups were admitted through PRs #232–234.
- Official v0.2.12 (`5106065716e494204fc0e8db16f68f6e9d576be0`) is included. The prior squash ancestry is recorded by an unchanged-tree ours merge of `42bc7f6cffe24bcb471608e48e66b4a0afa1f882`.
- The review covered 157 upstream paths and 16 initial text conflicts. Historical risk counts describe their pinned inputs, not today's checkout.

## Integration decisions

- Adopt TypeSafe/Jev System One, recharge bonus tiers, account priority editing, payment reconciliation and official security fixes. TypeSafe is API-key-only and has no system-prompt insertion point.
- Restore upstream per-group profit control, off unless the stored group switch enables it. Auth snapshot version 28 reloads old cached projections.
- Remove retired OpenAI API-key compatibility settings and account reasoning options. That release introduced a global recovery switch and removed Chat reasoning replay; the global page, switch and special recovery mechanisms were subsequently retired.
- Preserve explicit account-field clearing, untouched values, raw administrator errors and supported-path prompt/effort handling. Runtime configuration, account mappings and credentials were not rewritten by source integration.

## Migrations

The runner applies unrecorded files in filename order, each in its own transaction:

| File | Historical effect |
| --- | --- |
| `241_add_payment_order_bonus_amount.sql` | Official bonus column, default 0. |
| `241_add_typesafe_platform.sql` | Platform CHECKs; downstream quota list retains Cindy so pre-260 upgrades remain possible. |
| `265_user_platform_quota_typesafe_union.sql` | Final quota CHECK includes TypeSafe and excludes retired Cindy after older tail files. |
| `266_purge_retired_openai_apikey_compat_settings.sql` | Deletes only two retired settings rows. |
| `267_purge_account_reasoning_options.sql` | Removes only the two named account-extra keys, including soft-deleted rows. |
| `268_purge_system_prompt_bindings_without_insertion_point.sql` | Removes bindings only from platforms without an insertion point. |

- The empty PostgreSQL 18.1 path applied all 349 files and reached the expected constraints/bonus column. The v0.2.11-codexrip.9 production-state upgrade path was inspected, not executed against such a database.
- Migration 265 corrected fresh-install ordering after immutable 241/251/260 files; later official 242 and additive 277 remove the platform CHECKs. Historical SQL names and checksums remain fixed.
- The purge migrations are idempotent and have no down path. Older code can restore different defaults when options are absent; TypeSafe data is not understood by older releases. Migration 264 and later 270–274 impose additional [rollback restrictions](native-domains.md#release-and-rollback-boundary).
- Generated Ent/Wire outputs matched their combined schemas/providers. No production data rollback was exercised.

## Deliberate downstream deviations kept

TypeSafe tests required an answer matching the sent question, not any HTTP 200 JSON. Tests retained upstream error text and valid-terminal requirements. Prompt binding was restricted to actual injection platforms; existing account edits preserved bindings and key-level extra writers could not replace them. Manual model sync required confirmation before changing the form.

The release also retained explicit account mappings, administrator visibility and native independent features. Historical capacity, plugin and recovery implementations described in the original review have since been retired; this file does not restore them.

## Conflict resolutions

The 16 conflicts combined official TypeSafe creation/dispatch/pricing with downstream generic platform catalogs, quota rows, account normalization and console tokens. Upstream recharge settings and account-priority controls were retained. Fixtures followed the combined platform catalog; no duplicate TypeSafe-only UI list was added.

## Auto-merged areas checked

The review checked TypeSafe dispatch and answer validation, platform colors/catalogs, quota constraints, recharge UI and the shared account-test parser. Textual auto-merge alone was not accepted as behavior evidence. Current contracts supersede historical platform counts and capacity rules.

## Validation

Recorded validation covered Go build/vet/lint, affected unit tests, the recovery gate of that release, frontend types/lint/selected fixtures and isolated migration tests. Existing integration results were reused where applicable. A final subset was rerun after follow-up changes; these are historical results, not checks on a later tree.

Full suites, race tests, the prior-production-state database upgrade, an assembled-tree browser preview, real model/TypeSafe calls and deployment were not run. Source review and offline results did not establish production success.

## Release target

The historical target was immutable v0.2.13-codexrip.1 with only Sub2API recreated and runtime configuration retained. Initial effects included stored profit switches taking effect, auth-cache reload and invalidation of old password-reset links. Removed replay Redis keys were not explicitly deleted and retained their existing 24-hour expiry.

Final source SHA, PR/check results, image digest and actual production outcome belong to delivery evidence. Ordinary deployment now follows [the current release contract](../DOWNSTREAM.md#release-and-production-deployment).

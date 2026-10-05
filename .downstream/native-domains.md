# Native domains

Five first-party domains run inside the host: Codex runtime, model policy,
system prompts, account tools and observability. Native pages
retain the console theme (flat_theme_enabled), account fields and persisted task interactions.
The official third-party plugin framework and its own configuration UI remain.

## Settings and operations

All paths below are relative to `/api/v1`. Settings writes retain administrator
authentication and the existing step-up policy.

| Interface | Contract |
| --- | --- |
| `GET/PUT /admin/settings/codex-runtime` | Codex request body compression switch (`request_zstd`; a missing key is on). |
| `GET/PUT /admin/settings/observability` | Telemetry and native theme switches. |

Each of the two is stored as one `settings` row holding a JSON object of
boolean switches; `GET` and `PUT` answer with the switches in the standard
response envelope. A `PUT` body that is not a JSON object, or that carries any
other key or a non-boolean value (including `null`), returns HTTP 400 and stores
nothing. While no Codex runtime row is stored,
`gateway.openai_codex_request_zstd` decides the compression switch.

Ordinary account bulk editing remains HTTP 202; this change does not replace
background tasks with synchronous editing. Existing API-key reveal and
account-field protections remain.

## Startup and retained data

Migrations run first. Migration 264 stops startup, changing nothing, on a
database whose `codexrip.codex-runtime` installation was never retired, whose
saved Codex runtime configuration has no hash recorded by v0.2.11-codexrip.6,
.7 or .8, or whose `sub2api_plugin_state` table holds rows of another plugin
key. Starting one of those three releases once satisfies the first two
conditions.

Then, before native settings load and before the plugin manager starts, one
transaction retires the other first-party plugin installations a database still
holds, except `codexrip.image-tools`, which it does not read or change:

1. Validates the saved first-party capability scopes and decrypts configuration.
2. Imports the effective observability switches only when the native settings
   key is absent. An existing key is never overwritten. The retired Cindy
   provider installation is disabled without importing its switches.
3. Saves original installation/binding/bootstrap intent under
   `deplugin_retired_plugins`, disables the installations and their bindings,
   and marks their bootstrap records removed.

Missing settings may use deployment defaults. Database errors, malformed saved
settings and unknown/non-equivalent capability scopes stop startup; they never
silently enable features. An installation that is disabled as a whole, in a
domain without an equivalent native switch, requires an explicit migration
decision rather than automatic activation.

The retired installation rows remain and are listed read-only, with the
receipt, at `GET /admin/plugins/retired`; the `codexrip.codex-runtime` row holds
no saved configuration, package, manifest or bindings. The upstream plugin
manager cannot list, modify, delete or replace the retired first-party keys.

## Release and rollback boundary

The host is delivered as one immutable OCI image with image provenance. Normal
deployment uses the existing fixed-image `deploy-preserve` flow. It does not add
backups, canaries, model calls or automatic rollback.

`rollback-preserve` still requires a verified compatible data contract. The
deployer recognizes the old and native prompt/skill directory locations by the
same content hashes; missing or ambiguous trees and changed schema remain errors.

Migration 264 has no down path. An image change back to v0.2.11-codexrip.6, .7
or .8 after it starts only on a database that keeps a retired
`codexrip.codex-runtime` installation; without that row those images exit at
start. They do not read the stored compression switch, so request compression
follows `gateway.openai_codex_request_zstd`, and without `sub2api_plugin_state`
their outbound fingerprint view and quality-run endpoints fail while forwarding
continues. Images at or before v0.2.11-codexrip.5 also need
`sub2api_plugin_state` and `sub2api_plugin_leases` for their route code and
require a database dump taken before migration 264.

Migration 270 has no down path either. It drops the three Image Studio tables
with the job history and the records of the stored image files and deletes the
`image_tools_config` setting; the files under `<data dir>/image-studio` are not
removed. An earlier image starts on such a database only with Image Studio off,
which the missing setting leaves to `GATEWAY_IMAGE_STUDIO_ENABLED`: with that
variable `true` it exits at start, and its Image Studio job routes fail on the
dropped tables.

Migration 271 has no down path. It drops `usage_billing_dedup.account_id`, which
the billing claim no longer writes and nothing reads. An earlier image names
that column in every billing claim, so its usage billing fails on such a
database until the column is added again
(`ALTER TABLE usage_billing_dedup ADD COLUMN account_id BIGINT`). Migration 271
is already recorded by then and does not run a second time: after returning to
this release or a later one, drop the column by hand
(`ALTER TABLE usage_billing_dedup DROP COLUMN IF EXISTS account_id`). Billing
works with the column present until that is done.

Returning to a plugin-based host is not an image change. That host needs schema
objects and plugin data the current database no longer holds, so it requires a
database dump that still has them. Such a legacy-host rollback has not been run
and is not an automatic failure path.

## Verification

Normal required PR checks validate the native domain packages, account scope,
configuration/storage boundaries and frontend behavior. The release reuses that
source validation. Health endpoints establish availability only.

## Console theme

`observability.theme_enabled` (public `flat_theme_enabled`, default on) toggles
`html.flat-theme`; switching it off restores the upstream look. The console look
lives only in the central layer: `frontend/src/styles/flat-theme.css` (tokens, the
Inter and Geist Mono Latin subsets; Chinese uses MiSans loaded at runtime from
Xiaomi's font service by `utils/flatTheme.ts`, falling back to system fonts),
`frontend/tailwind.config.js` (every palette, radius, shadow and gradient resolves
through a CSS variable whose fallback is the upstream value; `primary` is Apple blue
and the hue families map to Apple system colours), `frontend/src/style.css`
(upstream recipes plus a `flat-theme` console block: colour only where upstream has
colour, Liquid Glass only on chrome) and the shared components and layout. Page
files keep upstream class strings; after upstream merges, re-run the ops-repo
de-sweep (`artifacts/tmp/admin-rework/ui/desweep/desweep.py`) instead of restyling
pages.

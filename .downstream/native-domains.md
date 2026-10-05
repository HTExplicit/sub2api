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

## Startup and stored data

Migrations run first. Migration 264 stops startup, changing nothing, on a
database whose `codexrip.codex-runtime` installation was never retired, whose
saved Codex runtime configuration has no hash recorded by v0.2.11-codexrip.6,
.7 or .8, or whose `sub2api_plugin_state` table holds rows of another plugin
key. Starting one of those three releases once satisfies the first two
conditions.

Migration 272 stops startup, changing nothing itself, in four cases:

1. One of the seven first-party plugin installations exists and no receipt of
   the retirement, the `settings` row `deplugin_retired_plugins`, is stored:
   that installation was never retired. Starting v0.2.13-codexrip.8 once
   retires it.
2. One of them exists and the stored receipt is not completed or has no entry
   for it. No release writes such a receipt and none repairs it:
   v0.2.13-codexrip.8 exits at start on a receipt that is not completed and
   retires nothing once a completed one is stored. Whether the configuration
   saved in such an installation is still needed is decided by hand; 272
   applies once the installations it names are deleted.
3. Another installation is in the state `updating`.
4. Two enabled bindings share a capability, platform and account type.

Upstream's schema admits neither 3 nor 4 and the plugin manager writes neither;
disabling or uninstalling the plugin in v0.2.13-codexrip.8 clears both.

Each migration commits on its own, so a database on which 272 has stopped
already has 270 and 271, which v0.2.13-codexrip.8 and every earlier release
lack. Such an image starts on that database only with Image Studio off, which
the deleted `image_tools_config` setting leaves to
`GATEWAY_IMAGE_STUDIO_ENABLED`: with that variable `true` it exits at start,
and its Image Studio job routes fail on the dropped tables. It names
`usage_billing_dedup.account_id` in every billing claim, so its usage billing
fails until the column is added again
(`ALTER TABLE usage_billing_dedup ADD COLUMN account_id BIGINT`). Migration 271
is recorded by then and does not run a second time: after returning to this
release, drop the column by hand
(`ALTER TABLE usage_billing_dedup DROP COLUMN IF EXISTS account_id`). Billing
works with the column present until that is done.

The plugin tables, the plugin manager and its admin interfaces are upstream's:
the manager lists and manages every installation row. Missing settings may use
deployment defaults. Database errors and malformed saved settings stop startup;
they never silently enable features.

## Release and rollback boundary

The host is delivered as one immutable OCI image with image provenance. Normal
deployment uses the existing fixed-image `deploy-preserve` flow. It does not add
backups, canaries, model calls or automatic rollback.

`rollback-preserve` still requires a verified compatible data contract. The
deployer recognizes the old and native prompt/skill directory locations by the
same content hashes; missing or ambiguous trees and changed schema remain errors.

Migration 264 has no down path. Images at or before v0.2.11-codexrip.5 need
`sub2api_plugin_state` and `sub2api_plugin_leases`, which it drops, for their
route code.

Migration 270 has no down path either. It drops the three Image Studio tables
with the job history and the records of the stored image files and deletes the
`image_tools_config` setting; the files under `<data dir>/image-studio` are not
removed.

Migration 271 has no down path. It drops `usage_billing_dedup.account_id`, which
the billing claim no longer writes and nothing reads.

Migration 272 has no down path. It deletes the seven first-party plugin
installations (`codexrip.account-tools`, `codexrip.admin-observability`,
`codexrip.cindy-provider`, `codexrip.codex-runtime`, `codexrip.image-tools`,
`codexrip.model-policy` and `codexrip.prompt-skills`) with their saved
configuration, package bytes, manifests and capability bindings, and the
`settings` row that held the receipt of their retirement. It drops the schema
only those plugins used, so that `sub2api_plugin_installations` and
`sub2api_plugin_bindings` are again as upstream's migrations 229 and 230 define
them: the tables `sub2api_plugin_bootstrap` and `sub2api_plugin_updates`, the
columns `revision`, `runtime_generation`, `package_sha256` and `update_policy`,
the state `updating`, the trigger and function `sub2api_plugin_revision`, and
the limit of the unique index on enabled bindings to two capabilities. Other
installations, their bindings and the `admin_observability_config` setting are
not written.

A migration does not reach what the seven plugins stored outside the database:
their package files `<plugin dir>/packages/codexrip.*.s2plugin`, their unpacked
trees `<plugin dir>/installed/codexrip.*/` and any Redis key
`plugin:kv:v1:codexrip.*`, where `<plugin dir>` is `plugins.data_dir` or, when
that is not set, `<data dir>/plugins`. Nothing reads these once 272 has
applied; they are deleted by hand at the release that applies it. The files
and keys of other plugins in the same directory and under the same prefix are
in use and stay.

After migration 272 no earlier image starts on the database. Every earlier
release since v0.2.8-codexrip.5 retires first-party plugin installations at
start: finding no receipt, it selects
`sub2api_plugin_installations.runtime_generation`, the query fails on the
dropped column and the process exits with `initialize native features`. A
plugin-based host needs the dropped schema and the deleted plugin data as well.
Going back to an earlier release is therefore not an image change: it requires a
database dump taken before the migrations that release does not have. Such a
rollback has not been run and is not an automatic failure path.

## Verification

Normal required PR checks validate the native domain packages, account scope,
native configuration and frontend behavior. The release reuses that
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

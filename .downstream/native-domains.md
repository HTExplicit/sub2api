# Settings, stored data and rollback

## Settings

All paths below are relative to `/api/v1`. Settings writes retain administrator
authentication and the existing step-up policy.

| Interface | Contract |
| --- | --- |
| `GET/PUT /admin/settings/codex-runtime` | Codex request body compression switch (`request_zstd`; a missing key is on). |
| `GET/PUT /admin/settings/observability` | Console theme switch (`theme_enabled`; a missing key is on). |

Each of the two is stored as one `settings` row holding a JSON object of
boolean switches; `GET` and `PUT` answer with the switches in the standard
response envelope. A `PUT` body that is not a JSON object, or that carries any
other key or a non-boolean value (including `null`), returns HTTP 400 and stores
nothing. While no Codex runtime row is stored,
`gateway.openai_codex_request_zstd` decides the compression switch.

## Codex gateway borrowing

The Extensions sidebar has one Codex Borrowing entry. Its in-page navigation
switches between three independently addressable administrator pages: settings at
`/admin/codex-gateway-borrow`, route status at
`/admin/codex-gateway-borrow/status`, and manual comparison/history at
`/admin/codex-pelican-comparison`. Opening or navigating these pages only reads
local data. Settings drafts contain configuration IDs/options only, persist in
the browser session, and never affect the saved configuration used by status or
generation. Only explicit actions save, prepare, verify or generate. The status
page polls read-only status every five seconds while preparation/verification is
active; local countdowns expire displayed routes without renewing them. Leaving
the comparison page cancels its active generation.

`GET/PUT /admin/codex-gateway-borrow/config` owns one JSON setting,
`codex_gateway_borrow_config`: `enabled`, `source_account_ids`,
`target_account_ids` and `models`. A missing row is off with empty account
lists and the two models `gpt-6-astra` and `gpt-6.1-sol`. Only existing OpenAI
OAuth-like accounts can be selected; the source and target lists are disjoint.
The extension never rewrites account model mappings, proxies, WS switches,
groups, quota or scheduling state. Normal credential refresh remains available.

Source acquisition shares one process-local `__oailb` candidate. Its maximum
lease is 230 seconds, capped by an earlier upstream expiry; receiving the same
value during a live lease does not extend it. Source preparation is sequential
and has a 90-second budget. Each target/model is validated with two completed
HTTP 200 streams, at most 45 seconds each: the first must return STATE, and
the second may omit STATE or return the same value. Qualification includes
the target identity, actual model, exit, client headers, STATE and TLS profile.
Source and target probes use separate HTTP/TLS pools. Business WS connections
stay in the ordinary account pool, with fixed one-hour continuation anchors.

Status/config/history reads do not call models. Saving an enabled config starts
one finite preparation; business cache misses prepare synchronously. There is
no renewal timer. `/prepare` and `/verify` explicitly prepare or revalidate.
All probes only record observations, including failures before business dispatch.

`POST /admin/codex-gateway-borrow/tests` accepts a timestamped UUIDv7 and explicit
account/model pairs. It sends the fixed pelican prompt once per pair, only with
matching unexpired qualification, using at most three shared generation slots
and serializing each account. It does not prepare missing routes. An expired
or future UUID is rejected; replaying a saved UUID cannot dispatch again.
Migration 275 adds only this feature's task/result tables. Records expire 24
hours after task creation, disappear from reads immediately, and are removed
by scoped minute cleanup. Output/errors retain their original bytes.

Result previews use short random capability URLs under
`/codex-gateway-borrow/preview/`, an opaque script-enabled sandbox and their own
response CSP. Inline animation and external scripts/styles/fonts/images are
allowed. Preview documents receive no administrator token or UI bridge.
Adaptation sources and retained notices are in
[gateway borrowing notices](gateway-borrow-notices.md).

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
deployment uses the fixed-image `deploy` flow. It does not add
backups, canaries, model calls or automatic rollback.

An older tag uses the same deployment form and requires database migrations
compatible with that image. Deployment does not restore dropped schema or data;
the migration boundaries below determine whether an earlier image can start.

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

Migration 273 removes the key `telemetry_enabled` from the stored
`admin_observability_config` row; `theme_enabled` keeps its value. The key
switched the per-account traffic counters, which are deleted with their route
`GET /admin/accounts/:id/traffic-telemetry`. The counters were the Redis keys
`account_traffic_observe:{<account id>}:<http|ws>` and the same with the
suffix `:minute`; each expires 24 hours after its last write and nothing
writes them any more. v0.2.13-codexrip.10 and .11 start on the migrated row and
take the missing switch as on. Saving the observability settings in one of
them stores the key again, and 273 is recorded by then: this release then
exits at start with `invalid setting admin_observability_config` until the key
is removed by hand
(`UPDATE settings SET value = (value::jsonb - 'telemetry_enabled')::text WHERE key = 'admin_observability_config'`).

Migration 274 removes the soft-deleted release-acceptance keys and their usage
records, removes their billing deduplication records, and drops `api_keys.purpose`
and `api_keys.lease_id`. Ordinary keys and diagnostic logs remain. The matching
application code no longer reads those columns; an older image that reads them
cannot be deployed after this migration. Failures require a forward fix.

## Console theme

`observability.theme_enabled` (public `flat_theme_enabled`, default on) toggles
`html.flat-theme`; switching it off restores the upstream look. The console look
lives only in the central layer: `frontend/src/styles/console/*.css` (tokens and
the console rules), `frontend/tailwind.config.js` (every palette, radius, shadow
and gradient resolves through a CSS variable whose fallback is the upstream value;
`primary` is the console's green accent and the hue families fold into five status
colours), `frontend/src/style.css` (upstream recipes plus a `flat-theme` block
that restates them for the console) and the shared components and layout. Text
uses the OS UI font stack of `frontend/src/styles/theme.css`, as upstream does; no
web fonts are shipped or loaded. Page files keep upstream class strings; after
upstream merges, re-run the ops-repo de-sweep
(`artifacts/tmp/admin-rework/ui/desweep/desweep.py`) instead of restyling pages.

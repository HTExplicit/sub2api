# Native domains

Six first-party domains run inside the host: Codex runtime, model policy,
system prompts, account tools, image tools and observability. Native pages
retain the console theme (flat_theme_enabled), account fields and persisted task interactions.
The official third-party plugin framework and its own configuration UI remain.

## Settings and operations

All paths below are relative to `/api/v1`. Settings writes retain administrator
authentication and the existing step-up policy.

| Interface | Contract |
| --- | --- |
| `GET/PUT /admin/settings/codex-runtime` | Codex runtime configuration `{"request_zstd": <bool>}` (request body compression), without an additional response envelope; `Cache-Control: no-store`. The source is encrypted at rest. |
| `GET/PUT /admin/settings/image-tools` | Image Studio switch (`studio_enabled`; a missing key is off). |
| `GET/PUT /admin/settings/observability` | Telemetry and native theme switches. |

A Codex runtime `PUT` without `request_zstd` stores `true`. The keys of the
retired Codex route acquisition (`enabled`, `fail_closed`, `proxy_url`,
`proxy_protocol`, `proxy_selection_id`, `models`, `routing_schema`) are dropped
and not stored again; any other key, or a `request_zstd` that is not a boolean
(including `null`), returns HTTP 400.

The route acquisition endpoints (`/admin/accounts/codex-tickets/*`,
`/admin/accounts/:id/codex-tickets/*`, `/admin/accounts/:id/codex-routing/validate`
and `/admin/settings/openai-codex-ticket/*`) are removed. Migration 263 deletes
the job rows of the retired kinds `codex_ticket_harvest`, `codex_ticket_stop`
and `extension_operation`; their display names are gone. A job whose kind this
version has no executor for still lists and reads with `retry_eligible=false`
and `retry_unavailable_reason=kind_unsupported`; a retry returns HTTP 409, and
its items still pending fail with `kind_unsupported`. Ordinary account bulk editing
remains HTTP 202; this change does not replace background tasks with synchronous
editing. Existing API-key reveal and account-field protections remain.

### Codex runtime receipt after v0.2.8-codexrip.5

The headers below extend the native API after `v0.2.8-codexrip.5`.
That published image remains immutable; these headers are not part of its contract.

`GET /admin/settings/codex-runtime` retains the raw JSON body and
`Cache-Control: no-store`, and adds:

| Response header | Value |
| --- | --- |
| `X-Sub2API-Codex-Hosting-Mode` | `native` |
| `X-Sub2API-Codex-Config-Version` | Active snapshot configuration version. |
| `X-Sub2API-Codex-Config-SHA256` | Lowercase SHA-256 of the exact snapshot body bytes. |
| `X-Sub2API-Codex-Runtime-Generation` | Active snapshot runtime generation. |

Version and generation are positive signed 64-bit integers serialized as canonical
decimal strings without leading zeros. The hash covers the exact `snapshot.raw`
UTF-8 bytes after decoding any response `Content-Encoding`; do not reorder JSON,
pretty-print it, or append a newline.

HTTP 200 requires the body and receipt to come from the same active snapshot,
whose runtime has started and is not cancelled, with matching persisted fence
metadata and configuration hash. Otherwise the endpoint returns HTTP 503 with
`{"code":503,"message":"Codex runtime is unavailable"}`, without receipt headers
or raw configuration. The receipt describes the epoch observed by that read;
later operations still use their existing execution fences. Retained
disabled installation rows stay unchanged; no generic metadata endpoint is added.

## Startup and retained data

Before loading native settings or starting either runtime, one transaction:

1. Validates the saved first-party capability scopes and decrypts configuration.
2. Imports the effective image and observability switches only when their native
   settings keys are absent. Existing keys are never overwritten. The retired
   Cindy provider installation is disabled without importing its switches.
3. Saves original installation/binding/bootstrap intent under
   `deplugin_retired_plugins`, disables the seven installations and their bindings,
   and marks their bootstrap records removed.
4. Advances the existing Codex `runtime_generation` once. A fresh database instead
   receives a disabled state anchor with generation 1 and no executable/artifact.

Missing settings may use deployment defaults. Database errors, malformed saved
settings and unknown/non-equivalent capability scopes stop startup; they never
silently enable features. A disabled whole Codex installation requires an
explicit migration decision rather than automatic activation.

The original installation rows, encrypted configuration, artifacts, state and
lease tables remain. The upstream plugin manager cannot list, modify, delete or
replace the retired first-party keys. Native state access uses the original
`codexrip.codex-runtime` owner, namespaces, state keys and CAS.
Closed quality-run records and their budgets are not rewritten.

An unchanged normalized configuration keeps its epoch and generation. A changed
Codex configuration is validated, the old epoch is drained, and encrypted source,
configuration hash/version and generation are committed together. A stored
configuration that still holds the retired route acquisition keys loads with
those keys dropped. Its normalized hash then differs from the recorded one, so
the first start advances `runtime_generation` once and records the new
configuration hash and version; the encrypted source keeps its old bytes until
the next save.

Migration `263_purge_retired_codex_ticket_data.sql` deletes the data written by
the retired route acquisition: the triggers, trigger functions and tables of
migrations 244 and 245; the settings `openai_codex_ticket_enabled` and
`openai_codex_ticket_harvest_proxy_url`; the `codexrip.codex-runtime` state
namespaces `tickets`, `routing-demand` and `proxy-trust` and its
`routing-account` and `tickets` leases; the route and Cookie material in
`codex-routing-private` (`bundle.*`, `clock.*`, `seen.*`, `validation-result.*`);
the account `extra` keys `codex_turn_ticket:*`, `codex_ticket_runtime:*` and
`codex_harvest_proxy_url` and the `codexrip.codex-runtime` entry of
`plugin_account_projections` (the map goes once it is empty); and job rows of
the retired kinds. `codex-routing-private` keeps the ledgers `quality-run.*`,
`validation.*` and `spent.*`, and the `wire.*` observations without the raw
Cookie values (`cookies`) they stored. Accounts without these keys are not
rewritten. Account create, update and bulk edit drop these `extra` keys and that
projection entry, so importing an account export taken before this release does
not bring them back. The encrypted Codex runtime source keeps its bytes until
the next save, as described above.

Migration `254_plugin_job_action_digest.sql` preserves full 64-character historical
task action digests. History and retries continue to use saved targets; unsupported
legacy operations fail without choosing other accounts or models.

## Release and rollback boundary

The host is delivered as one immutable OCI image with image provenance. Normal
deployment uses the existing fixed-image `deploy-preserve` flow. It does not add
backups, canaries, model calls or automatic rollback.

`rollback-preserve` still requires a verified compatible data contract. The
deployer recognizes the old and native prompt/skill directory locations by the
same content hashes; missing or ambiguous trees and changed schema remain errors.

Returning to a plugin-based host additionally needs an explicit restoration of
legacy activation/configuration intent before starting that host. The retained
receipt and artifacts supply the original data; **never restore the receipt's old
runtime generation or delete its native anchor**. A plain image change does not
perform this reverse conversion. Such a legacy-host rollback has not been run as
part of the native migration and is not an automatic failure path.

Migration 263 has no down path; the data it deletes (listed above) comes back
only from a database dump taken before it. An image that still has the route
acquisition starts without its tickets and routes. Images from
`v0.2.8-codexrip.5` (source `9f7b08dfc`) on never read the 244/245 tables; older
images still query them, so returning to one of those needs that dump.

## Verification

Normal required PR checks validate the native domain packages, account scope,
configuration/lease/ledger compatibility and frontend behavior. The release
reuses that source validation. Health endpoints establish availability only;
actual model-quality diagnostics remain separately budgeted and coordinated.

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

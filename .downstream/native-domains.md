# Settings, stored data and rollback

Current native contracts are maintained here. Account tests and system prompts
are in [models and prompts](gpt6-models-and-prompts.md); source and release rules
are in [DOWNSTREAM.md](../DOWNSTREAM.md).

## Settings

Paths are relative to `/api/v1`; writes retain administrator authentication,
audit and existing step-up policy.

| Interface | Contract |
| --- | --- |
| `GET/PUT /admin/settings/codex-runtime` | `request_zstd` compression switch. Without a stored row, `gateway.openai_codex_request_zstd` applies. |
| `GET/PUT /admin/settings/observability` | `theme_enabled` console switch; missing key defaults on. |

Each uses one JSON `settings` row and standard response envelopes. PUT requires
an object containing only the documented boolean switch; extra keys, nulls and
non-booleans return 400 without writes. Codex advanced settings live at
`/admin/codex/advanced`; old runtime URLs redirect there.

## DeepSeek default reasoning effort

`/admin/deepseek-reasoning` uses administrator-only
`GET/PUT /api/v1/admin/deepseek-reasoning`, audit and standard envelopes.
`settings.deepseek_reasoning_config` holds `{"enabled":false,"effort":"max"}`;
effort accepts `low`, `high`, `max`. Missing configuration is disabled/Max.
Saving refreshes memory immediately; other processes refresh within one minute.
Requests freeze a memory snapshot. Initial read failure leaves requests unchanged;
refresh failure retains the last valid snapshot. No migration or usage rewrite.

After mappings, Flash/V4 receive the default only for absent/null/empty effort;
other DeepSeek models require catalog support. Namespaces are recognized.
Explicit or malformed values, disabled thinking and existing aliases remain.
Group ceilings/deny rules apply once, and clean failover attempts never inherit
another model's injection. Responses, Chat and Messages retain their native
fields and other reasoning options. Usage records forwarded effort without
inventing explicit client effort. Compact/count-tokens remain unchanged;
disabling restores ordinary omitted-effort behavior.

## Model catalog interfaces

[Model discovery](../DOWNSTREAM.md#model-discovery-capacity-and-client-catalogs)
owns current official behavior. Custom capacity forms and
`POST /admin/accounts/models/context-capacities-preview`,
`GET /admin/accounts/:id/models/context-capacities` and
`GET /admin/settings/model-context-catalog` are retired. Historical SQL,
stored overrides/source values and diagnostics remain. Native sync, confirmation
picker, metadata and test/effort plans remain independent; no data cleanup.

## Codex fingerprint simulation

The Codex sidebar opens `/admin/codex/identity`; `/admin/codex` redirects there.
`/admin/codex/advanced` owns UA/version, identifier modes and compression. Old
runtime/fingerprint addresses redirect. Saved/effective fields remain available
in accessible disclosures; general settings/account forms do not write identity.

`GET/PUT /admin/settings/codex-fingerprint` requires `enabled`, `user_agent`,
`client_version`, `version_auto_sync_enabled` on PUT; existing UA/version/sync
keys remain authoritative and `codex_fingerprint_enabled` falls back to the
startup flag. GET includes synced/effective versions, default UA and source.
Writes use existing administrator authentication/audit without extra step-up.

`GET/PUT /admin/accounts/:id/codex-fingerprint` applies to OpenAI OAuth/setup-token
accounts, including credential shadows. PUT accepts only `mode`; bulk jobs use
frozen IDs and only that extra key. Read-only `device_identity`,
`effective_device` and `identifier_policy` distinguish saved/derived fields from
UA parsing. Reads never persist identity or timestamps. Details load at most
three concurrently; obsolete responses are rejected and unsaved drafts retained.
General edits preserve omitted modes, existing seeds/devices remain, and new
accounts default to `device`.

Enabled simulation uses one random seed-derived fixed credential-source device
and independent real conversations. Final originator/UA uses official
`codex_cli_rs` normalization without regenerating the device. Stored `session`
and `full` stay compatible but act as `device`; UI offers off/device. Mapping
scopes session/thread/parent/window by credential source and API key, preserving
UUIDv7 time and real root/child/fork relationships. Explicit cache keys remain;
default cache keys follow the mapping. Real turns, timestamps, permissions and
sandbox metadata remain. A request without a conversation gets one synthetic
UUIDv7 root shared across its attempts, never a permanent account root.

Embedded turn metadata is canonical; flat fields and bounded HTTP/WS headers are
projections. Malformed core metadata returns request-scoped 400; unknown fields
and large numbers remain, and resolved conflicts are recorded. Tests/compaction
use the same projection; wire status separates policy, sent identity and TLS/ALPN.

The verified transport reference is official `rust-v0.162.1`, commit
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`.
`backend/internal/pkg/codextransport/reference.json` owns archive/binary/capture
hashes and samples for Windows x86_64, Linux x86_64 and macOS arm64/x86_64.
The sampled isolated custom-CA variant does not prove all native trust stores.
It records TLS permutation, HTTP/2 order/flow, HTTP/1 headers and WS offers;
handshakes are fresh without shared session tickets.

Strict simulation never falls back to generic Go transport or replays an
uncertain POST. Existing explicit protocol recovery remains separate. Plugins
owning transmission need a matching attested profile or fail clearly while
retaining configuration. API-key/other-provider/master-off paths keep their
clients. HTTP/WS/refresh/catalog pools isolate credential source, proxy/profile.
The Codex WS adapter retains coder/websocket v1.8.14 notices and bounded deflate;
other services use the original dependency.

Version discovery runs every six hours: normalized manual version, discovered
stable, then built-in default, subject to the minimum. New client identities do
not await new transport samples. `Binding.client_version` records the requested
identity in pool keys; `Binding.version`/`validated` describe sampled transport.
Settings `version_source` is manual/synced/builtin; `verified_version` retains
the reference and compatibility `version_pending_validation` is false. The
four-field PUT contract, seeds and persisted hardware remain unchanged.

Every attempt shares one frozen identity/header/body/transport policy and
compression choice. WS reuse also checks actual device/session/thread/window,
client and policy revision, including off/device modes. Changed identity needs
a new handshake after the current turn; mode edits invalidate only that
account's subsequent connections.

## Independent Pelican tests

`/admin/pelican-tests` has Works/Tasks views for the last 24 hours. Account
name/ID, model and verdict are primary filters; effort/status/legacy scene are
secondary. Removable filters, view/task/pages persist in URL. Four-to-three
previews use 1/2/3/4 columns at widths below 768/768/1280/1920px. The composer
loads 50 accounts per page, preserves cross-page scopes and paginates selected
groups by 20; a sticky launch summary and per-user session draft retain rejected
submissions and clear on accepted creation or explicit reset.

Models use saved mappings/catalogs, including Cline/Command Code; OpenAI defaults
to `gpt-6.1-sol`, Anthropic to `claude-opus-5-5`, otherwise first text-capable
local choice. Effort uses the model default. Unsupported/duplicate rows require
correction, missing old choices require reselection, and bulk edits preserve
incompatible rows. Existing APIs retain concrete-model compatibility.

`POST /admin/pelican-tests/tasks` returns 202 and owns a background job.
`repeat_count` defaults to one, expands each unique account/model, and caps a
task at 5000 results. `repeat_index`/`repeat_count` remain in details/summaries.
List/snapshot/results/detail/SSE are read-only. Only explicit cancel stops a
background job; closing observers does not. Identical UUID/parameters replay the
snapshot, conflicts fail, and restart marks unfinished work interrupted without
replaying uncertain model calls. Original `/tests` SSE keeps request cancellation.

Whole-task/result/failed-only retests preserve model/effort/budget, copying each
selected sample once; cross-task selections create one new task per source.
Parameter copying merges repeated model rows and restores the saved count.
New tasks select Pelican and/or candy (UI defaults both). Every generation and
retest sends the original prompt verbatim:
`创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试`.
No scene/nonce/style/format constraint is added. Deprecated scene inputs only
support replay; historical prompts, scenes, nonces and verdicts are immutable.
Account-owned credentials, identity, proxy, mappings and protocol remain;
no fallback account, group policy or score is manufactured.

Candy (90s), generation (saved budget, existing 64000 output-token ceiling or an
explicit smaller limit), rendering (30s after admission) and same-model visual
review (180s) are separate. Candy failure does not prevent generation. Review
uses four 960x640 frames at 0/0.23/0.51/0.79 of a CSS/SMIL cycle or a three-second
script window, retaining sample times. Pelican/bicycle/riding/motion verdicts
are true/false/null; unsupported image input is unreviewed. All selected checks
must pass; execution status is separate. Detail evidence preserves raw bytes as
Base64 and complete administrator output. Generation requires extractable
HTML/SVG, not a nonce or animation declaration.

One on-demand Chromium renderer permits inline scripts but blocks network and
local-file requests; nested documents/navigation fail rendering. It is released
after each render. All tasks share ten model-request slots. Different models
on an account may run concurrently within business capacity; each account/model
serializes complete test flows and ordered repeats across tasks. Waiting for
turns/account/rendering holds no outbound slot, and failed admission releases it.
Generation budget defaults to 600s (60–1800); queue/preparation/generation times
are distinct. Observed leases renew every 30s and lost capacity cancels work.
Credential refresh remains; health/quota/scheduling/affinity/usage/billing writes,
including callbacks, are suppressed.

Submission IDs are timestamped UUIDv7; expired/future IDs fail and cannot
dispatch again. Migrations 275/276/280/281 preserve tables and raw bytes while
adding budgets, phases, evidence/config and sample indexes. Legacy rows retain
their 90s budget; missing/one repeat hashes stay compatible. Records expire
24h after creation and are removed by scoped minute cleanup. Finished tasks or
results can be explicitly deleted; running parents return 409. Deletes serialize
on the parent and remove it when the last result disappears. Migration 279 keeps
deleted UUID/hash/expiry but purges child output/errors; reads/replays return 410
and old preview capabilities fail. Repeated deletes are idempotent; retained
raw errors/output are never rewritten or automatically regenerated.

`GET /admin/pelican-tests/results` reads gallery summaries without raw answers,
envelopes or images. Retest accepts a new UUID and optional result/failed-only
selection, checking replay source/selection before reading old records. Saved
works remain viewable after candy/review failure. New HTML and minimally wrapped
SVG use opaque script-enabled capability previews under `/pelican-tests/preview/`,
expiring within 30 minutes and task lifetime. Existing standalone SVG keeps
`scene.svg` and no-script CSP. Browser previews permit inline animation and
external assets under their own CSP but receive no admin token or UI bridge;
server review rendering retains the stricter network block above. Mounted
previews do not reload when capabilities rotate; reopening uses the new URL.
Details, downloads and 2–4 cross-task comparisons retain full evidence. Sources
and licenses remain in [third-party notices](../THIRD_PARTY_NOTICES.md).

## Retired gateway borrowing

Gateway borrowing has no service, forwarding hooks, administrative interfaces,
page or compatibility routes. Migration 278 deletes only
`settings.codex_gateway_borrow_config` and changes the task execution-mode default
to `account`. Accounts, model mappings, proxies, groups and identity settings
are not changed. Historical migrations retain their filenames and checksums.

The independent Pelican repository retains the historical
`codex_gateway_borrow_test_tasks` and `codex_gateway_borrow_test_results` table
names to preserve records. Historical `execution_mode` and `borrow_applied`
remain readable facts. New tasks always use `account` and never apply borrowing;
non-account historical tasks cannot be executed or replayed. Account-mode request
hashes retain their original field order and wire representation.

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
web fonts are shipped or loaded. Page files keep upstream class strings; upstream integrations retain this central styling boundary.

## Login protection

The administrator-only `/admin/login-protection` page lives in the Extensions
sidebar section. `settings.login_protection_settings` defaults to enabled, a
sliding five-minute window, five allowed failures, and a 24-hour IP ban starting
on the sixth failure. Administrators can change integer window minutes (1–1440),
allowed failures (1–1000), and ban hours (1–720). Settings writes refresh the local
cache immediately; other instances refresh within 60 seconds.

Password credential failures (including nonexistent emails), invalid login TOTP
codes, and login TOTP attempt-limit rejections share a canonical source-IP
counter across account names. Successful logins do not clear it. Malformed
requests, human-verification failures, expired login sessions, and infrastructure
errors do not count. Existing per-minute and TOTP guards remain in place.

Redis-time Lua transitions own the sliding window, ban TTL, active-ban index and
idempotent unban. App restarts retain Redis state. Banned requests do not extend
expiry. Every new request reaching Sub2API is checked, including frontend
fallback, model routes, and new WebSocket upgrades. Existing streams continue.
No administrator, token or API key bypasses an active source-IP ban. Responses
use HTTP 429, `LOGIN_IP_BANNED`, `Retry-After`, and expiry metadata; they are not
cacheable. A bounded positive fallback preserves known unexpired bans during
Redis failure; password/TOTP login fails with 503 while other traffic continues.

`GET/PUT /api/v1/admin/settings/login-protection` reads/writes the four rule
fields; `GET .../bans` uses normal pagination and returns full IPs with
`blocked_at`/`expires_at`; `POST .../unban` takes `{ip}` and clears both the ban
and failures. Writes use existing administrator audit/step-up policy. Automatic
ban transitions produce one audit event; repeated blocked traffic does not add
ban events. No new password or credential values are recorded.

Disabling bypasses counting/enforcement without deleting still-expiring state;
re-enabling restores unexpired bans. Rule changes do not alter existing ban
expiry. Shared public-IP users share enforcement. Banned administrators must use
another unbanned IP to manage recovery. No historical requests are replayed.

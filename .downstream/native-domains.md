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

## DeepSeek default reasoning effort

Extensions exposes `/admin/deepseek-reasoning`. Administrator-only
`GET/PUT /api/v1/admin/deepseek-reasoning` uses the standard response envelope
and audit middleware. The complete configuration is
`{"enabled":false,"effort":"max"}`, stored as one JSON `settings` row named
`deepseek_reasoning_config`; effort accepts `low`, `high` or `max`. No stored
row means disabled with Max selected. Saving updates the local snapshot
immediately; other processes refresh in the background within one minute.
Forwarding reads memory only and freezes the configuration per request.
An initial read failure leaves requests unchanged; refresh failures retain the
last valid snapshot. No schema migration or historical usage rewrite is needed.

After channel/account mapping, DeepSeek Flash and V4 model families receive
the configured effort only when it is absent, null or empty. Namespaced model
IDs are recognized. Other DeepSeek models require model-catalog support for
the selected effort. Explicit effort, unknown nonempty values, malformed
fields, disabled thinking and existing effort aliases retain existing behavior.
The new value passes through group mappings, ceilings and deny rules once.
Every attempt uses its clean request, so failover to a different model does
not inherit an injected effort.

Responses, Chat Completions and Messages, including the existing bridges,
carry the value in their native effort field. Requests retain other reasoning
fields and request-integrity comparisons replay the same default and policy.
Usage records keep the actual forwarded effort while an internal defaulted
flag preserves absent client effort instead of inventing an explicit request.
Responses-shaped bodies on the Chat Completions URL use their Responses field.
Compact and count-tokens operations retain their existing behavior. Turning the
setting off restores the original omitted-effort behavior.

## Codex fingerprint simulation

The Extensions sidebar owns one Codex entry. `/admin/codex` redirects to
`/admin/codex/identity`; `/admin/codex/advanced` owns manual UA/version,
identifier modes and compression. Previous runtime and fingerprint URLs retain
their redirects. Full saved/effective identity fields remain available in
keyboard-accessible disclosures. General settings and account create/edit/bulk
forms do not submit fingerprint configuration.
`GET/PUT /admin/settings/codex-fingerprint` uses `enabled`, `user_agent`,
`client_version` and `version_auto_sync_enabled`; all four fields are required
on PUT. GET also returns the synced and effective simulation versions and the
default UA. Existing UA/version/sync setting keys remain authoritative; only
`codex_fingerprint_enabled` is new. Without that key the startup identity flag
provides the default. Writes use existing admin authentication and audit, with
no additional step-up. Legacy settings APIs use the same keys.

`GET/PUT /admin/accounts/:id/codex-fingerprint` applies only to OpenAI OAuth and
setup-token accounts. GET computes effective identity through the forwarding
selector, including credential shadows. PUT accepts only `mode`; bulk changes
use ordinary account jobs with explicit frozen IDs and only that extra key.
GET and the PUT response also include read-only `device_identity`,
`effective_device` and `identifier_policy`. Saved/seed-derived device fields
are separate from fields parsed from the selected UA; unparseable UA fields
remain null, and disabled simulation has no effective client device. Identifier
rules distinguish fixed values, request-derived handling and passthrough;
request-dependent threads, parent references and window indexes are not
invented from an empty request. Reads never persist identities or timestamps.
The page loads full identities automatically, limits detail reads to three,
rejects obsolete page/policy/mode responses, and retains unsaved mode drafts.
General account edits preserve the latest stored mode when it is omitted.
Existing seeds and persisted TUI identities are retained, and new accounts still
default to `device`.

Enabled simulation uses one fixed credential-source TUI device with independent
real conversations. Stored `session` / `full` values and API inputs remain
compatible but have effective mode `device`; the UI offers only `off` and
fixed-device/multiple-conversation mode. Their stored raw values are not migrated.
Every enabled mode scopes session/thread/parent/window identities one-to-one by
credential source and API key, preserving UUIDv7 timestamps and real root/child/fork
relationships. Explicit custom cache keys remain untouched; default session cache
keys follow the same mapping. Real turns, timestamps, permission and sandbox
metadata are retained. Calls without a client conversation get one synthetic UUIDv7
root per logical request (shared by its attempts), never a permanent account root.

The valid embedded body turn metadata is canonical; flat fields and bounded
HTTP/WS headers are compatibility projections. Malformed core metadata returns a
request-scoped 400. Unknown metadata, including large JSON numbers, is retained.
Original field conflicts are recorded when canonical projection resolves them.
Ordinary/compaction account tests use the same projection and synthetic root
lifecycle, including credential shadows; wire status distinguishes configured
policy, actually sent identity and negotiated TLS/ALPN.

The verified client baseline is official Codex `rust-v0.162.1`, commit
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`. The transport reference in
`backend/internal/pkg/codextransport/reference.json` records fixed official-release archive
digests, binary/capture digests and loopback sample runs for Windows x86_64,
Linux x86_64 and macOS arm64/x86_64. These captures use an isolated custom CA
configuration; verification is for that official-client transport variant, not
an assertion that every native trust-store configuration is identical. TLS
extensions are permuted as observed. HTTP/2 settings/flow/pseudo-header order,
HTTP/1 header order/spelling and WebSocket offers are verified against actual
wire captures. Native reconnect samples did not resume TLS sessions; the current
profile therefore uses fresh TLS handshakes and does not share session tickets.

Strict transport applies to enabled simulation: an unavailable profile or failed
transport does not fall back to the generic Go client or replay an uncertain POST.
Existing explicit upstream protocol-error recovery remains separate. Third-party
plugins that own transmission cannot be used under strict simulation without a
matching attested profile; their configuration is retained and a clear error is
returned. API-key/other-provider paths and master-off behavior retain their clients.
Codex HTTP, WS, refresh and model-list clients isolate their pools by credential
source, proxy and profile. The Codex-only WebSocket adapter retains upstream
coder/websocket v1.8.14 and its license, adding actual bounded deflate-window support;
other services use the original dependency.

Version discovery still runs every six hours. Automatic effective identity advances
only within the shipped verified profile set; newly discovered unsupported releases
are visible as pending validation. Saving an unsupported manual version returns an
error. Settings reads add verified/pending/strict-profile details without changing
the four-field write payload. Existing device seeds and persisted hardware identities
are retained; their supported platform/architecture classes match the verified set.

One immutable policy/identity selection is shared by each attempt's headers, body
and transport. New requests freeze the compression choice too. WS pool compatibility
includes actual header and body device/session/thread/window identity even for
`device` / `off`, plus client and policy revision. Changed identity is applied after
the current turn by requiring a new handshake, never by reusing an old conversation's
handshake. Account mode edits invalidate only that account's subsequent connections.

## Independent Pelican tests

`/admin/pelican-tests` opens a paginated gallery of all observations from the last
24 hours, with task/account/model/effort/scene/execution/verdict filters. Wide
screens show four cards, medium screens two, and phones one. The new-task composer loads
accounts in pages of 50, retains selection across pages, and offers explicit
current-page/all-matching scopes. Models use locally saved mappings and catalogs;
searchable dropdowns accept catalog choices only. Backend defaults prefer
`gpt-6.1-sol` for OpenAI and `claude-opus-5-5` for Anthropic, falling back to the
first text-capable local choice. Effort defaults to the model default.
Known unsupported choices and duplicate account/model rows must be corrected or
removed before submission; an old choice missing from today's catalog must be
reselected rather than silently replaced. Existing API consumers retain manual
concrete-model compatibility.
Bulk configuration preserves incompatible rows. Text capability and generation
use the same local resolver, including the already integrated Cline/Command Code
senders. No new provider protocol is introduced.

`POST /admin/pelican-tests/tasks` returns 202 and starts a server-owned job.
Optional `repeat_count` defaults to one; each unique account/model target produces
that many independent results. A task permits at most 5000 expanded results.
The composer shows combinations x repeats and preserves one effort per model.
Each result carries `repeat_index` and `repeat_count` in summaries and details.
The GET list, snapshot (`/:id`), paginated summaries (`/:id/results`), full single
result (`/:id/results/:result_id`), and SSE (`/:id/events`) never start generation.
Only `POST /:id/cancel` cancels a background job. Disconnecting observers or
closing the page leaves it running. Repeated client UUIDs with identical normalized
parameters return the existing snapshot; conflicting parameters fail. Restart and
shutdown preserve completed works, mark unfinished work interrupted, and never
replay uncertain model calls. The existing task/result tables remain in place.

The original `/tests` SSE endpoint retains request-bound cancellation and its
request/response contract. Both APIs use the same runner, history and execution
capacity. Summary SQL does not load raw responses, answers or HTML; details retain
complete administrator evidence. The page supports 2–4 same/cross-task comparisons,
filters, enlargement, downloads and parameter copying. Whole-task, single-result
and failed-item retests directly create a new task with original model/effort
parameters and budget. Cross-task selected retries create one task per source,
preserving each source budget. Retests copy each selected result once, preserving
its sample index and count, using the original fixed prompt; they never multiply
by the task repeat count again. Parameter copying merges repeated model rows
and restores the saved repeat count. New submissions retain UUID replay protection;
history, browsing, reconnecting and comparison are read-only.
New manual tasks select Pelican and/or candy (the UI defaults to both). Every
generation, including repeats and retests, sends the original prompt verbatim:
`创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试`.
No scene, style, anatomy, color, verification code or format constraints are added.
Deprecated scene inputs remain accepted for replay compatibility but do not affect
new generation. Historical scene/nonce/prompt/verdict evidence is never rewritten.
New results use opaque HTML previews, including minimally wrapped bare SVG, and
retain inline scripts and original source. Saved works remain previewable after
candy or review failure. Existing standalone SVG works keep image previews.
The actual sending path, credentials,
identity, model mapping, proxy, TLS and protocol remain owned by each account. No API Key, group policy, model score or fallback account
is manufactured for a test.

Candy (90 seconds), HTML/SVG generation (the saved generation budget), rendering
(30 seconds after renderer admission), and same-model visual review (180 seconds)
run as distinct stages. Candy failure does not prevent generation. Review uses
four 960x640 frames at 0/0.23/0.51/0.79 of the sampled CSS/SMIL animation cycle.
Script-driven works use the same fractions of a three-second real-time observation
window and retain actual sampling times without rewriting scripts. Review checks
pelican/bicycle/riding/motion, and keeps true/false/null judgments. Unsupported
image input is unreviewed. Only all selected checks passing gives a passed verdict;
execution status is independent. Stage evidence retains raw bytes as Base64 in
detail JSON. Generation checks extractable HTML/SVG rather than animation
declarations or a nonce. A single on-demand Chromium process renders HTML with
inline scripts; all network/local-file requests, nested documents and page
navigations are blocked. The browser is released after each render.

All batches share ten model request execution slots. Different models on one
account may run concurrently, subject to the unchanged business account capacity.
A process-wide account/model turn serializes the entire selected test flow,
including repeats in sample order and matching models in other tasks. Waiting
for a model turn, account capacity, or rendering holds no outbound execution slot.
If account admission fails, its global slot is released before retrying admission. Generation starts after preparation and admission; its default
budget is 600 seconds, configurable from 60 to 1800 seconds. Queue, preparation
and generation durations are recorded separately. Observed business account slots
renew every 30 seconds; lost capacity cancels the operation. Existing credential
refresh remains available, while account health, quota, scheduling, affinity,
usage and billing writes are suppressed, including background callbacks.

Submission IDs are timestamped UUIDv7 values. Expired/future IDs are rejected and replay
cannot dispatch again. Migration 275 owns the task/result tables; migration 276
adds budgets, execution phases and actual invocation metadata in place, preserving
old records and raw BYTEA output. Migration 281 adds sample indexes, defaults
existing rows to one, and replaces the account/model unique key with a sample key.
Single-run hashes remain compatible with existing submissions; omitted and
explicit one-repeat requests are equivalent. Existing records retain their 90-second budget.
Records expire 24 hours after task creation, immediately disappear from reads,
and are removed by scoped minute cleanup. Administrators can explicitly delete a
finished task or one of its results through `DELETE /tasks/:id` and
`DELETE /tasks/:id/results/:result_id`. Running parents return 409. Result deletes
serialize on the parent and update remaining counts; deleting the last result
also removes the empty task. Migration 279 adds `deleted_at` to the task table:
deleted tasks keep their original submission identity/hash and expiry, but purge
child output and task errors. Reads and submission replays return 410; old preview
capabilities can no longer resolve removed results. Repeated deletes are
idempotent. Output/errors of retained results preserve original bytes;
binary upstream envelopes use lossless Base64. Truncated or incomplete output is
recorded without another generation or automatic repair.

`GET /admin/pelican-tests/results` reads only gallery summaries.
`POST /admin/pelican-tests/tasks/:id/retest` accepts a new client UUID, optional
result IDs or failed-only selection, preserves source configuration/scenes and
budget, and creates fresh verification codes. Replays compare the retest source
and selection before consulting source records. Migration 280 adds task config,
small evaluation metadata and byte-backed evidence/SVG/review image in place.
Old records remain unscored and are never automatically re-executed.

New SVG previews use `scene.svg` image capabilities and a no-script CSP.
Gallery and preview SQL do not read original answers, upstream envelopes or review
images. An already generated SVG remains viewable after candy/review failure.
Legacy HTML result previews use short random capability URLs under
`/pelican-tests/preview/` only,
expire within 30 minutes and the task's remaining lifetime, and use an opaque
script-enabled sandbox and their own
response CSP. Inline animation and external scripts/styles/fonts/images are
allowed. Preview documents receive no administrator token or UI bridge.
Adaptation sources and retained notices are in
[third-party notices](../THIRD_PARTY_NOTICES.md).

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
web fonts are shipped or loaded. Page files keep upstream class strings; after
upstream merges, re-run the ops-repo de-sweep
(`artifacts/tmp/admin-rework/ui/desweep/desweep.py`) instead of restyling pages.

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

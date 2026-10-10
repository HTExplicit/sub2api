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

## Codex fingerprint simulation

The Extensions sidebar owns one Codex entry. `/admin/codex` combines borrowing
configuration and status; `/admin/codex/identity` shows client identity; and
`/admin/codex/advanced` owns manual UA/version, identifier modes and compression.
The previous runtime, fingerprint, borrowing and status URLs redirect to these
pages. Configured borrowing opens with its editor folded; a new setup opens the
three-step editor. Full saved/effective identity fields remain available in
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

Enabled simulation applies a fixed account TUI identity to HTTP, passthrough,
WS, account tests and credential refresh. Disabled simulation preserves caller
identity and identifiers, filling only missing protocol identity headers.
Mode `off` preserves device/session identifiers while simulation stays enabled;
`device` changes only device identifiers; `session` keeps separate real threads
and maps their parent references; `full` combines threads and removes self-parent
references. Real turn IDs, parent/root turn IDs, window indices and timestamps
are retained. Sandbox `none`, `external`, permission modes and unknown tags are
preserved; known platform backends are aligned without inventing a sandbox for
an absent tag. Explicit custom cache keys are not rewritten.

The built-in protocol/version baseline is official Codex `rust-v0.161.0`.
The existing six-hour stable-version sync and manual-version precedence remain.
Successful saves publish an immutable policy; failed saves retain the old one.
Each attempt shares it across headers/body. WS reuse includes identity and
policy revision. Setting changes let the current turn finish, then request a
retryable reconnect before another turn is sent through an old handshake.
Account mode changes invalidate only that account's next handshake.

Account connection tests exclude and reject `ultra` across every capability
source before dispatch; stale UI selections reset to default. Codex client
orchestration catalogs retain their existing capabilities.

## Codex gateway borrowing

The unified Codex page separates configuration, route qualification, actual
request usage and upstream completion. Pelican stays at `/admin/pelican-tests`;
its legacy comparison URL redirects there. Page reads and navigation only read
local data. Unsaved configuration stays in browser-session storage. Saving an
enabled configuration starts exactly one finite preparation, shown as queued
before the worker starts. Status polls every five seconds only while work is
active; countdowns expire routes without renewing them.

`GET/PUT /admin/codex-gateway-borrow/config` owns one JSON setting,
`codex_gateway_borrow_config`: `enabled`, `source_account_ids`,
`target_account_ids` and `models`. A missing row is off with empty account
lists and the two models `gpt-6-astra` and `gpt-6.1-sol`. Only existing OpenAI
OAuth-like accounts can be selected; the source and target lists are disjoint.
The extension never rewrites account model mappings, proxies, WS switches,
groups, quota or scheduling state. Normal credential refresh remains available.

The protocol reference is ranxi v2.10.3 (`fd1b5ee4`), non-rotating branch.
Candidates are stored per source with an active-source preference. Their maximum
lease is 230 seconds, capped by earlier upstream expiry; seeing a value again
while its lease is live does not extend it. Source acquisition uses the normal
OAuth account-test payload and OpenAI transport. Target probes use the fixed
plaintext JSON template and the host's OpenAIHarvest policy, with no copied
transport configuration and no business service tier inserted into their body.
The actual selected model is preserved: Astra follows the original; Sol is a
separate downstream model extension with independent qualification.

Source acquisition has a 90-second bound. The two target probes have 45 seconds
each and require completed HTTP 200 streams. The first must return STATE; the
second may omit it or return the same value. Probes clone the business headers,
clear business STATE and old body headers, preserve native session/thread/window
headers, and generate a new legacy `session_id` per shot. The second shot carries
only the first probe's STATE and target `__cflb` with the same borrowed `__oailb`.
Nothing is written back to the business session. The business request keeps its
own model, tier, cookies, STATE and history; borrowing replaces only `__oailb`.

The original cache identity includes route cookie, proxy, Authorization,
ChatGPT-Account-ID, UA, Originator, Version, business STATE and TLS profile.
The host adds actual model and fingerprint policy revision. Trace IDs, native
session headers and business tier are not additional cache rules. STATE changes
therefore require fresh validation. A failed target cools that qualification for
15 seconds; it does not discard a source candidate or force a replacement attempt.
After cooldown the next request may validate again; missing or expired candidates
are acquired on demand. There is no renewal timer, exit rotation, ordinary fallback
or business replay. Business WS keeps its existing continuation anchors.

Status/config/history reads do not call models. Saving an enabled config starts
one finite preparation; business misses prepare on demand. There is no renewal
timer. `/prepare` and `/verify` retain their interfaces. Same-revision, same-wire
qualifications share work; cancelling one caller does not cancel other waiters,
and the final departing waiter cancels abandoned work. Distinct targets no longer
fail because another target is validating. Outbound observations share the existing
ten execution slots and serialize by account. The two target shots retain one
account lease from mint through continuation, so a different model or client
window cannot interleave another probe. Source preparation finishes before that
lease is acquired; nested shots reuse it without taking a second permit.
Preparation has a ten-minute bound, continues after a target failure, and reports
partial readiness after rechecking
expiry. Policy, credential/model-map and exit changes invalidate displayed evidence;
actual dispatch still verifies the finalized request fingerprint.

The finalized business service tier stays in request context for diagnostics.
Probe and business inputs are reported separately. `acquisition` reports the
source trigger, stage, start/end time, failure and cooldown; each target also
reports its latest validation interval. A source candidate is not target proof.

Status adds `setup`, `observed_since` and `recent_usage`. The last actual dispatch
and in-process counters are retained per configured target/model/protocol/purpose;
configuration publication resets this index. HTTP/SSE, pooled WS and native WS
relay observations record application independently from upstream completion.
HTTP application is checked against the immutable preparation proof and final
cookie, including after restoring the caller context. A parsed successful terminal
and visible text remain authoritative when read-ahead cleanup later reports
cancellation; that detail remains available as `read_error`. Captures are snapshotted
under a lock, and the usage index preserves the first terminal event.
Diagnostic and business requests are distinguished. This index contains no request
payloads or credential/cookie values; the existing administrator error records remain.
Attempts now start before preparation: `attempt_count` and `blocked_count` are
separate from existing `count` (dispatches) and `applied_count`. `dispatched`,
`failure_stage`, client and gateway request IDs distinguish a local rejection
from an inference failure. Original target reasons survive failure cooldowns;
Ops classifies local borrowing rejection as gateway/routing with upstream status
zero, retaining the compatible client 503 code. Business evidence is displayed
ahead of diagnostics. Candidate expiry never implies a failed check is usable.

`POST /admin/codex-gateway-borrow/diagnose` streams an explicit fixed-account
comparison with `account_id`, `model`, `transport` (`http` or `ws`), and optional
`request_limit` (1–8, default 8). Its ordinary and borrowed requests use the normal
sender with observation isolation and a fixed short prompt. WS uses separate
short-lived diagnostic anchors and performs one continuation on each socket;
account/global WS settings must already permit WS. Source/target probes and all
compatibility sends count toward the same hard request limit. Events include
`phase`, `request`, `result`, `done`, and `error`; results contain original output,
completion, actual borrow application and reported model. Cancellation releases
owned work and sockets. The UI retains the current run and can download JSON;
there is no new table or migration. These observations do not prove intelligence
or physical model identity. Normal token refresh remains permitted.
Optional `scenario: codex_session` checks HTTP native client metadata, a fixed
side-effect-free echo tool call, returned STATE and complete-history continuation.
It defaults to the borrowed path to fit preparation and two business turns within
the same eight-request bound. Optional `mode` selects `ordinary` or `borrowed`;
omitting it for the original short scenario retains both-path comparison.
Optional `service_tier` exercises the effective routing tier. Results include the
specific verification (shape, actual model/tier, both completions, STATE lengths
and route replacement), failure stage and tool-round-trip result. HTTP diagnostic
history and STATE live only within that invocation, never in client sessions.
Complete `response.output_item.done` events rebuild tool calls and reasoning
items when the final successful envelope has empty output; a successful terminal
is still required. This matches native streaming continuation instead of assuming
all output is repeated inside `response.completed`.

The HTTP-only `scenario: probe_contract` compares a fixed candidate under three
request bodies: the prior sorted-map body, the current pinned template with the
same effective tier, and the pinned template without a tier. With no effective
tier, the redundant final pair is skipped. The older serializer runs first to
avoid always confounding its position with later upstream failures.
Native identity, routing hint, model,
proxy and cookie stay fixed; each shot still gets its own legacy `session_id`.
The original probe selects Astra; the result explicitly marks Sol as a downstream
model extension. `probe_only: true` results carry per-shot completion and STATE
facts under `verification`, and their common template/cookie and plaintext-body
fingerprints under `probe_context`. Top-level business dispatch, application and
completion stay false. These comparisons never publish business qualifications
or replace real usage observations. Source acquisition and all shots share the
existing eight-request cap; the snapshot's expiry cancels remaining comparisons
without silently selecting a different candidate. Page reads do not invoke it.
Optional `comparison: encoding` keeps the same current body, tier, identity,
cookie and exit, and compares plaintext probes with the configured business
codec. It changes no global setting or business qualification; actual per-shot
encoding distinguishes an enabled zstd comparison from two plaintext samples.
The same probe-only scenario can select a configured source for one plaintext,
no-tier Astra pair against its own current live candidate. It cannot generate,
test another source model, acquire a replacement or substitute a different
candidate owner. `probe_context.subject_role` distinguishes source self-checks
from target checks. Self-check results do not qualify business traffic or mutate
the source's health/status.

Ranxi reference: v2.10.3 (`fd1b5ee4eeb20961fbb783fa6f136a1704271e90`). Its
fingerprint, gateway-cookie, target-probe and automatic-setup core files are
unchanged from v2.10.2. The scoped update and retained host adaptations are
listed in [the review](upstream-review-ranxi-v2.10.3.md). OAuth search-history compatibility
is supplied by the official v0.2.15 integration. Automatic group membership,
Mihomo exit rotation and removal of harvesting attempt/concurrency bounds are
not adopted; existing account mappings, network routes and protocol switches stay
under their existing owners.

The historical `/admin/codex-gateway-borrow/tests` endpoints retain their
cache-qualified, 90-second request contract. They share execution admission with
the independent tests described below.

## Independent Pelican tests

`/admin/pelican-tests` is a task/result workbench. The new-task composer loads
accounts in pages of 50, retains selection across pages, and offers explicit
current-page/all-matching scopes. Models use locally saved mappings and catalogs;
valid backend defaults are respected and effort defaults to the model default.
Known unsupported choices and duplicate account/model rows must be corrected or
removed before submission; manual unverified models remain explicitly marked.
Bulk configuration preserves incompatible rows. Text capability and generation
use the same local resolver, including the already integrated Cline/Command Code
senders. No new provider protocol is introduced.

`POST /admin/pelican-tests/tasks` returns 202 and starts a server-owned job.
The GET list, snapshot (`/:id`), paginated summaries (`/:id/results`), full single
result (`/:id/results/:result_id`), and SSE (`/:id/events`) never start generation.
Only `POST /:id/cancel` cancels a background job. Disconnecting observers or
closing the page leaves it running. Repeated client UUIDs with identical normalized
parameters return the existing snapshot; conflicting parameters fail. Restart and
shutdown preserve completed works, mark unfinished work interrupted, and never
replay uncertain model calls. No schema migration is added.

The original `/tests` SSE endpoint retains request-bound cancellation and its
request/response contract. Both APIs use the same runner, history and execution
capacity. Summary SQL does not load raw responses, answers or HTML; details retain
complete administrator evidence. The page supports 2–4 same/cross-task comparisons,
filters, enlargement, downloads, parameter copying and failed-item retries into a
new reviewed task. History, browsing, reconnecting and comparison are read-only.
The fixed Pelican prompt is preserved. The actual sending path, credentials,
identity, model mapping, proxy, TLS, protocol and configured borrowing remain
owned by each account. No API Key, group policy, model score or fallback account
is manufactured for a test.

All batches share ten model execution slots, with one request per account.
Nested borrowing probes participate in the same coordination and normal business
account capacity. Generation starts after preparation and admission; its default
budget is 600 seconds, configurable from 60 to 1800 seconds. Queue, preparation
and generation durations are recorded separately. Observed business account slots
renew every 30 seconds; lost capacity cancels the operation. Existing credential
refresh remains available, while account health, quota, scheduling, affinity,
usage and billing writes are suppressed, including background callbacks.

Task IDs are timestamped UUIDv7 values. Expired/future IDs are rejected and replay
cannot dispatch again. Migration 275 owns the task/result tables; migration 276
adds budgets, execution phases and actual invocation metadata in place, preserving
old records and raw BYTEA output. Existing records retain their 90-second budget.
Records expire 24 hours after task creation, immediately disappear from reads,
and are removed by scoped minute cleanup. Output/errors retain original bytes;
binary upstream envelopes use lossless Base64. Truncated or incomplete output is
recorded without another generation or automatic repair.

Result previews use short random capability URLs under
`/pelican-tests/preview/` (the historical borrowing path remains an alias),
expire within 30 minutes and the task's remaining lifetime, and use an opaque
script-enabled sandbox and their own
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

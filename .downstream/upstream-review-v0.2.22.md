# Official v0.2.22 integration

- Official tag: `v0.2.22`, commit `b0fa9f3e6eeab1968a53e1289b69daa4bb40ca2b`;
  downstream start: `ffcf1b45cafa5f238940f9be25c66dcadb4dbb29` (`.21.2`).
- The complete Git difference contains 340 official changed files, 88 downstream
  overlaps and 148 critical paths. The [machine report](upstream-risk.json)
  owns exact `v0.2.21` to `v0.2.22` inputs and complete lists; no truncated
  Compare API inventory is used.
- [DOWNSTREAM](../DOWNSTREAM.md) owns current behavior. Operator delivery
  evidence owns validation, publication and the actual running version.

## Initial conflicts and resolution boundaries

| Conflict files | Resolution |
| --- | --- |
| `.github/workflows/backend-ci.yml`, `.github/workflows/security-scan.yml` | Retain selective risk checks, required contexts, receipts and candidate flow; adopt the official Go version declaration without restoring blanket test runs. |
| `DEV_GUIDE.md` | Keep accurate downstream verification and build instructions while adopting relevant official documentation. |
| `Dockerfile`, `backend/Dockerfile`, `deploy/Dockerfile` | Retain the fixed Go 1.27.4 image digest and independent Pelican renderer dependencies; do not discard downstream build inputs. |
| `backend/cmd/server/wire_gen.go` | Regenerate from source with the real Wire tool, including the official billing lane and cleanup; preserve independent management dependencies. |
| `backend/internal/repository/dashboard_aggregation_repo.go`, `backend/internal/repository/user_group_rate_repo.go` | Adopt official nonblocking aggregation and override synchronization, retaining the pgx driver adaptation. |
| `backend/internal/repository/scheduler_cache.go` | Use official shared snapshot loading and capabilities/filter projection; retain only prompt binding and administrator raw-text isolation seams. |
| `backend/internal/service/api_key_auth_cache_impl.go` | Adopt official group/Ultra Fast projections while preserving retained management contracts. |
| `backend/internal/service/gemini_chat_completions_compat_service.go` | Adopt official structured-output/tool behavior and preserve final system-prompt injection. |
| `frontend/src/components/account/AccountUsageCell.vue`, `frontend/src/components/account/UsageProgressBar.vue` | Integrate official unknown/freshness semantics through the existing batch/cache path; preserve density, stable slots and measured rendering; remove orphaned quota projections. |
| `frontend/src/components/account/EditAccountModal.vue` | Add the official opt-in model filter without changing untouched protocol settings, mappings or group assignments. |
| `frontend/src/components/charts/EndpointDistributionChart.vue` | Integrate separate administrator account cost while retaining mounted canvases and existing motion behavior; user views hide this column. |
| `frontend/src/i18n/locales/en/admin/accounts.ts`, `frontend/src/i18n/locales/zh/admin/accounts.ts` | Retain downstream administration labels and add official behavior labels in both languages. |

These rows cover all 18 initial text conflicts. Automatic clean merges follow
these same official-core and retained-extension boundaries.

## Official core and retained seams

Forwarding, protocol conversion, recovery, scheduling, identity, transport,
effort and pricing remain official. This release adds zero-copy request views,
pre-output keepalive/prelude handling, post-output stream account effects,
Antigravity thought/empty-stream handling, rotated Claude token recovery,
shared scheduler snapshots, TCP dead-connection detection and Codex fork cache
identity. Official schema, metadata, sealed function and additional-tools
compatibility changes remain intact. No downstream fallback or guard is added.

System prompts still use the final protocol insertion and instructions-echo
seams. Parsed-body replacement refreshes official derived views; HTTP/WS field
repair operates on the actual wire body. Same-turn WS reconnects retain repaired
wire bytes, while the next turn keeps clean client/replay history. The official
upstream-model observation fields are distinct from Pelican observation isolation.

Pelican/Candy, raw evidence, management jobs/taxonomy, model confirmation,
actual-effort plans, theme, no-store key reveal and login protection remain.
Pelican retains the existing usage/health/scheduling/affinity early returns;
the billing lane does not receive a second observation policy. The `.21.2`
persistent shell, measured windows, stable slots, batch prefetch and chart motion
remain. OpenAI quota presentation uses official windows; the orphaned frontend
quota estimates/state branches are removed.

Claude connection tests retain the user-selected management preference:
Sonnet 5.5 before Opus 5.5 when no model is chosen. Explicit saved choices and
Pelican's separate Opus 5.5 default remain. The official shared Claude directory
and protocol constants still follow this release.

## Billing, interfaces and stored data

The official per-user billing lane is wired by default without a configuration
switch: idle work settles immediately and queued work batches with per-command
results and existing idempotency. Official batch-failure fallback and shutdown
semantics remain; no persistent queue, pending receipt or extra retry is added.
Existing pgx storage support remains.

Group DTOs add `force_openai_ultrafast`, default false. The account option
`extra.openai_passthrough_model_filter` activates only on explicit true. Existing
accounts, mappings, whitelist entries, groups and feature switches are not
batch-modified. New custom whitelist entries reject `*`; mapping wildcard
semantics remain. Channel Bedrock compatibility uses the official boolean field.

Ultra Fast uses the official 6x multiplier for `gpt-6-astra` and `gpt-6.1-sol`,
including supported normalized Sol variants and custom token base prices.
`gpt-6-sol`, `gpt-5.6-sol` and Terra are outside the forced Ultra Fast support
set and retain the official generic 2x Ultra Fast multiplier. This does not
enable forced Ultra Fast. Actual supported Vertex us/eu
routes use the official 1.1x default-price uplift and explicit-price precedence.
DeepSeek explicitly overridden nonnegative fields, including zero, follow
official override provenance. Carbon/Hydrogen and Qwen catalog changes are
adopted without introducing scheduled price changes or recalculating old bills.

The two new migrations are `251_group_force_openai_ultrafast.sql` and
`251_group_usage_rollup_nonblocking_build.sql`. They retain official bytes and
coexist with `251_user_platform_quota_platform_union.sql` and
`252_cindy_identity_null_total.sql` under complete filenames/checksums. Historical
SQL bytes do not change. Rollup registration/calculation/publication and retained
usage cleanup use official locking; no new retry or data cleanup is introduced.

## Runtime and deployment choices

Server/gateway and Nginx limits remain 256 MiB, pure-text endpoints 32 MiB,
container memory 2.5 GiB and `GOMEMLIMIT=2048MiB`; request-body memory sizing stays
automatic. Official per-I/O deadlines and TCP dead-connection handling do not
become a total inference timeout. Existing concurrency ping configuration remains;
the new zero value is available but is not selected by integration. MCP, system
prompts and new optional switches are not enabled by this release.

The user authorized direct interruption on the fixed US target for this delivery.
A fixed previous image and target digest bind the one-run password entry; only
the drain call is skipped in memory. The installed publisher, mutex, image and
SSH boundaries remain. No first drain deployment, backup, canary, long observation
or automatic rollback is added. Failures preserve the runtime for a forward fix.
Production pointers change only after binary/source/digest/health agree.

## Validation boundary

[AGENTS](../AGENTS.md) governs targeted validation. The necessary risk set covers
request ownership and prompt/reconnect seams; official keepalive/stream/account
effects and Pelican isolation; snapshots/TCP behavior; lane amount/idempotency/
shutdown and explicit price precedence; isolated PostgreSQL upgrade/restart and
rollup write compatibility; and affected administration/presentation contracts.
Actual Wire/Ent generation, frozen frontend installation/typecheck, backend
compilation and selected tests supplement normal required CI and the candidate
image. Existing successful same-tree results are reused; failures only repeat
the failed and directly affected scope. This review does not claim tests,
Release publication or production deployment have completed.

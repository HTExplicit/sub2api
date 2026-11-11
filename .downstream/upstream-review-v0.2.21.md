# Official v0.2.21 integration

- Official tag: `v0.2.21`, commit `10ba8f9b4dc2e6023d4a9879ab77b21841babd10`;
  downstream start: `dbe4d3db1e10d926e8106f9f7fd2730383dade7b`.
- The complete Git difference contains 200 official changed files and 79
  downstream overlaps. The [machine report](upstream-risk.json) records all
  paths and exact `v0.2.20` → `v0.2.21` inputs. The approved scope also restores
  historical business-core changes that are outside that release difference.
- [DOWNSTREAM](../DOWNSTREAM.md) owns current behavior. Operator delivery
  evidence owns validation results, release identity and actual production.

## Initial conflicts and resolution boundaries

| Conflict files | Resolution |
| --- | --- |
| `backend/internal/handler/openai_gateway_handler_test.go` | Restore official request, failover, context and usage assertions; remove only tests whose entire implementation is retired. |
| `backend/internal/pkg/apicompat/anthropic_to_responses.go`, `responses_to_chatcompletions.go` | Restore official effort/history/protocol behavior, including sealed function parameters and namespaces; remove downstream strict terminal and history-preservation overlays. |
| `backend/internal/service/openai_gateway_passthrough.go`, `openai_gateway_response_handling.go`, `openai_gateway_upstream_errors.go` | Use official forwarding, partial-result accounting and errors; retain only final system-prompt/echo and scoped Pelican observation hooks. |
| `backend/internal/service/openai_responses_tool_schema_test.go` | Restore official tool assertions, including the official sealed function/namespace behavior, without keeping retired capability guards. |
| `backend/internal/service/openai_ws_forwarder_ingress.go` | Adopt official metadata buffering, TTFT, session seed, page-context normalization, search activation and later-turn 1013 behavior; remove downstream replay/turn-state and frozen-identity layers. |
| `frontend/src/components/account/AccountUsageCell.vue`, `frontend/src/components/account/__tests__/AccountUsageCell.spec.ts` | Add official Antigravity force-query through the existing query chain; ordinary table/detail allow interaction and compact/card presentations stay read-only. |
| `frontend/src/components/admin/usage/UsageTable.vue` | Add actual 30m/12h cache-write details through existing fields and display; preserve administrator raw evidence. |
| `frontend/src/views/admin/AccountsView.vue` | Retain the theme, three views, jobs, taxonomy, prompt bindings and model confirmation; all views retain rows during refresh and use skeletons only for first load. |

These rows cover all 12 initial text conflicts. Clean merges are reviewed under
the same official-core and retained-extension boundaries; a clean three-way
merge is not evidence that an old business branch should remain.

## Business-core retirement

Forwarding, protocol conversion, scheduling, recovery, identity, transport,
effort, pricing and async usage use official final source. This removes the
custom read-error recovery; refusal rewrite/extra retry/failover and scheduler
probes/leases/windows; strict business terminal/partial-usage filters; custom
error mappings; replay/turn-state overlays; persistent device/session/frozen
identity and sampled transport; request-Zstd; DeepSeek default effort and other
extra effort/history rewrites; custom pricing interpretations/fallbacks; deep
usage snapshots, pending receipts and commit-observer refresh hooks.

Official owner/source/shadow isolation, encrypted lineage, TLS profiles/proxies,
account error rules, health breaker, pool retries, profit gates, account costs
and Ultra metadata remain. Cyber-policy usage already used actual upstream
tokens: the misleading comment is corrected without presenting a new charge.
Retired settings, extras, seeds, diagnostic records and historical SQL remain.
Dedicated retired settings/account APIs and pages have no compatibility aliases.

## Retained extensions and official additions

- Pelican/Candy tasks, gallery, retests and raw evidence remain. Observation
  suppresses business billing/quota/health/scheduling writes; strict result
  acceptance is limited to evaluation.
- System-prompt library/bindings join final protocol construction and restore
  echoed instructions. Default-off means zero injection; they do not add an
  integrity/rejected-field/retry layer.
- Account folders/tags, cancellable/retryable HTTP 202 jobs, model-sync
  confirmation, test plans and actual forwarded effort remain. Theme, raw admin
  visibility, no-store password-gated key reveal and login protection remain.
- Official cache-write TTL counters and nullable 12h prices flow through the
  existing DTO/storage/UI. Actual response details determine TTL; NULL preserves
  official unified-price compatibility and explicit zero remains free. Existing
  price cards, overrides and historical bills are not rewritten.
- Official model catalog, metadata, payment fields and UI updates are adopted.
  MCP stays unconfigured/off; integration does not batch-change models,
  permissions, payment values or switches. The existing pgx driver adaptation
  remains a storage dependency, not a business-policy fork.

## Migrations and runtime limits

`249_add_usage_logs_api_key_cost_index_notx.sql` and
`250_openai_cache_write_ttl.sql` keep their official complete filenames and
bytes, beside the existing same-number migrations. Historical migration bytes
and checksums are unchanged. The concurrent index runs outside a transaction
with official invalid-index handling; no extra retry layer or retirement data
purge is added. Startup index creation can extend migration time.

The deployment uses official automatic body-memory budget sizing; the fixed
US preflight found no explicit budget override to remove. Existing 2.5 GiB container resources and
`GOMEMLIMIT=2048MiB` remain; the official algorithm yields a 1 GiB request-body
budget on the fixed US target. Server/gateway and main/www Nginx ingress limits
become 256 MiB; pure-text endpoints use the application's 32 MiB limit. Official
120-second per-I/O stall deadlines remain without a total inference timeout.

The authorized one-run direct deployment may interrupt business and wait for
startup migrations. It skips drain in memory while retaining the installed
publisher, mutex and fixed-image boundaries. A public-health timeout first
requires checking migration progress; it does not trigger another deployment
or automatic rollback. Production identity and health must agree before the
operator production pointer is updated.

## Validation boundary

Validation follows [AGENTS](../AGENTS.md): official changed-behavior tests,
TTL pricing/persistence and isolated PostgreSQL upgrade/restart checks, retained
extension seams, absence of retired routes/runtime dependencies, actual
Wire/Ent generation, frozen frontend installation/typecheck and selected
backend/frontend tests. CI gives this release's explicit deleted paths finite
ownership while preserving required checks, risk review, receipts and candidate
image builds. No blanket full/race/stress run or real model call is added.
Passed same-tree results are reusable; delivery evidence distinguishes passed,
not run and incomplete work. This source review does not claim publication or
production completion.

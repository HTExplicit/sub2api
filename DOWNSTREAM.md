# HTExplicit Sub2API Downstream

This fork maintains the `codexrip` patch set over official Sub2API releases.
Read [AGENTS.md](AGENTS.md) on every task. Current native contracts live in
[native domains](.downstream/native-domains.md); prompt and connection-test
contracts live in [models and prompts](.downstream/gpt6-models-and-prompts.md).
Versioned reviews record integration decisions. The operator workspace owns
production pointers and delivery evidence; source admission or a Release alone
does not prove deployment.

## Source baseline

- [.downstream/upstream-base](.downstream/upstream-base) records the integrated
  official stable tag.
- The [v0.2.21 review](.downstream/upstream-review-v0.2.21.md) records the official
  changes, business-core retirements and retained extension boundaries.
- [.downstream/upstream-risk.json](.downstream/upstream-risk.json) owns the exact
  Git comparison inputs and complete file lists; historical reviews keep their
  original paths.

## Official business core

Forwarding, protocol conversion, recovery, scheduling, identity, transport,
effort mapping, pricing and asynchronous usage submission follow the integrated
official source. Retained management features join the final protocol or
observation hooks; they do not supply a second business policy.

Official response owners, credential source/shadow accounts, account/API-key
isolation, encrypted-content lineage, TLS profiles, proxies, account error
rules, health breakers, pool retries, profit gates and account costs remain.
The official Ultra workflow metadata is retained. Cyber-policy usage already
records actual upstream tokens through the official cost path.

The downstream refusal rewrite/recovery, extra failover/cooldown/probes, retry
leases and storm windows are retired. Errors, stream completion,
`incomplete`/`cancelled`/interrupted responses and partial-image usage follow
official per-protocol behavior. There is no extra strict terminal filter,
forced pre-first-byte SSE, fixed rejection text, exhausted-model 400 mapping,
ordinary-failure partial-usage exclusion or pending usage receipt.

Codex uses official default-off identity and the original explicit
`off`/`device`/`session`/`full` modes. The persistent device backfill, extra
session mapping, frozen identity policy, custom transport/sampling chain and
request-Zstd override are retired. Effort defaults, caps and degradation use
official rules, including DeepSeek; old extra `none`/`max` rewrites, default
summary injection and destination-specific history/signature rules are retired.
Stored retired settings, account extras, seeds and diagnostic records remain
without controlling requests.

## Account operations and console

Account folders/tags and HTTP 202 bulk jobs retain progress, cancellation and
failed-item retry. Both edit entries freeze selected IDs when a selection
exists; without one, existing filter semantics apply. Late select-all responses
cannot replace newer selections; invalid nonempty IDs never fall back to all
results. Missing saved targets fail with `target_missing`; field clearing and
preservation of untouched fields remain.

The console theme and table/compact/card account views remain. All three views
retain existing rows during refresh and use skeletons only for the first load.
Regular console routes share one persistent shell; fullscreen Ops, Stripe popup
and embedded Model Plaza keep their page-owned conditional layouts. Sidebar
width changes settle once, with short label motion instead of continuous page
resizing. Account lists above 50 items use measured windows in all three views;
compact/card windows reuse the page's desktop batch-usage path. Shared chart
updates preserve mounted canvases, animate the first draw for 180ms and apply
later data without animation. Motion preferences apply to CSS and canvas.
On browsers with native reading order, compact/card windows retain bounded
physical slots and expose their logical order through accessibility ownership.
Desktop batch presentations clear obsolete focus, pointer ownership and
popovers when a slot changes account; independent usage probes retain their
account-keyed lifecycle. Other presentations keep account-keyed content.
Adjacent usage windows use the existing batch cache without forced refreshes.
The large accounts table retains account-keyed native row shells and mounts
cells only inside the measured window. Each active row owns its cell render
effect; batch usage updates affect their account keys. Desktop layer hints
apply only to active virtual accounts rows and release on hover or focus so
inline tooltips retain their existing stacking. Other tables keep their normal
window lifecycle. The measured-size bridge is tied to TanStack 3.13.23.
Measured account windows consume observer sizes without another animation
frame, and compact row content owns its reactive render effect.
Platform/status filters use official single selection and
`account-filter-values` storage. The official billing-rate timer retains
`immediate: false`. Settings use the official floating save control; API-key
lists and usage statistics load independently. Antigravity force-query is
available in the ordinary table and detail views; compact/card read-only
presentations remain read-only.

Administrators retain full upstream error/output evidence, password-gated
no-store account-key reveal and IP login protection. These visibility features
do not change business error classification or retry behavior.

## Model discovery, capacity and client catalogs

Model names, capacities, metadata, templates, manifests/ETags, selection,
retries, caching and client exports follow the integrated official release.
The old downstream capacity resolver, overrides/source identity, group
projection, automatic refresh and Cindy registry path remain retired.
Historical SQL, stored values and diagnostics remain. Native model sync,
models.dev enrichment and Grok diagnostics remain.

Manual model sync opens a confirmation picker; cancel changes nothing, and
account Save persists confirmed models. The account-test picker resolves raw
upstream IDs through saved mappings and retains `view=plan`, actual forwarded
effort and saved choices. Source/account changes invalidate stale requests.
Closing tests preserves list position. Use Key remote configuration uses
`{root}/v1/models`; file mode and large-catalog fallback follow official behavior.

## Usage, pricing and stored data

OpenAI cache-write accounting records actual response
`cache_creation_30m_tokens` and `cache_creation_12h_tokens` through the existing
DTO, storage and display paths. It neither infers TTL from a request nor adds
the split counters to their aggregate again. Channel, interval and account
statistics price cards carry nullable `cache_write_12h_price`: an existing
unified write price remains the official compatibility price when this field
is NULL, while explicit zero remains free. Existing prices, multipliers,
intervals and overrides remain; historical bills are not recalculated.

Pricing resolution is official. Empty token cards do not get a downstream
allowlist-only interpretation; the extra hy/Qwen/Muse fallback families and
GPT-5.6 named-family priority are retired. Usage uses the official worker,
context copy, inflight reservation and mandatory submission behavior.

Migrations retain complete filenames and historical checksums, including
same-number official/downstream files. New
`249_add_usage_logs_api_key_cost_index_notx.sql` uses the official nontransactional
concurrent-index runner and invalid-index handling;
`250_openai_cache_write_ttl.sql` adds the TTL fields. Retiring business policy
adds no data purge or Redis reset. Historical schema/rollback constraints remain
in [native domains](.downstream/native-domains.md#release-and-rollback-boundary).

## Management MCP and request boundaries

Official management MCP, OAuth grants and administrator permissions remain.
An empty `MCP_PUBLIC_URL` disables MCP. Integration does not enable MCP,
full-access compatibility mode or other new switches, or batch-change account
mappings, permissions or payment configuration.

Request reading uses official strict read-error handling, control-byte/BOM
normalization and Responses UTF-8 validation. Complete JSON after unexpected
EOF and missing compression-footer recovery are retired. Buffering, automatic
request-body memory budgets and per-operation I/O deadlines follow
[edge security](deploy/EDGE_SECURITY.md); upstream thinking has no added
whole-response deadline. Deployment settings remain authoritative. The current
release's approved limit/configuration changes are recorded in its review and
operator delivery evidence.

## Independent management extensions

System-prompt library/bindings and final-protocol injection with instructions
echo restoration remain independent. The default is off with no default
injection. Pelican/Candy tasks, retests, gallery and raw evidence remain;
observation blocks business usage/quota/health/scheduling writes. Strict result
acceptance stays within evaluation scope. Their detailed contracts are linked
at the top of this document.

## Release and production deployment

- Normal required PR checks admit `main`; verification follows [AGENTS.md](AGENTS.md).
- Immutable `vX.Y.Z-codexrip.N` tags on admitted `main` build
  `ghcr.io/htexplicit/sub2api:X.Y.Z-codexrip.N` with digest and provenance.
- Downstream Release reuses admitted validation. Normal Production Deploy takes
  `release_tag` and `confirmation=DEPLOY`, resolves the fixed digest and uses
  the restricted SSH updater with its configured production approval.
- Normal deployment naturally drains and rebuilds only Sub2API. Explicitly
  authorized direct interruption is a one-run operator exception; the permanent
  publisher, mutex, fixed-image and SSH boundaries remain.
- Other services, online data, resources, networking, subscriptions, routing and
  feature switches retain their existing boundaries unless explicitly approved.
  Updates add no business backup, canary, long observation or automatic rollback.
  A failure preserves runtime state for a forward fix.
- Health confirms availability, not model functionality. Binary version, source
  commit, image digest and health must agree before the operator production
  pointer and delivery evidence are updated.

## Upstream updates

[Release-channel rules](.downstream/README.md) own discovery, risk labels,
promotion and production approval. Discovery never deploys by itself. Promotion
requires completed review, no unresolved conflicts and `upstream-reviewed`
where the machine risk report requires review. Exact risk inputs and initial
conflicts are maintained in the current report/review linked above. Current
build versions come from manifests and workflows, not this handbook.

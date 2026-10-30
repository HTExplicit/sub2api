# HTExplicit Sub2API Downstream

This fork maintains the `codexrip` patch set over official Sub2API releases.
Read [AGENTS.md](AGENTS.md) on every task. Keep current contracts here, detailed
native contracts in [native domains](.downstream/native-domains.md), and historical
integration decisions in the versioned reviews. Operational production records
belong to the operator workspace; source admission or a Release alone does not
prove deployment.

## Source baseline

- The integrated official release is recorded in [.downstream/upstream-base](.downstream/upstream-base).
- Current integration decisions are in the [v0.2.19 review](.downstream/upstream-review-v0.2.19.md).
- The risk report and historical reviews retain their original paths and input records.

## Settings and account operations

[Native domains](.downstream/native-domains.md#settings) owns settings interfaces,
Codex identity, Pelican, login protection, stored data and rollback boundaries.
[Models and prompts](.downstream/gpt6-models-and-prompts.md) owns system-prompt
injection and account connection-test details.

Account bulk operations are HTTP 202 jobs with progress, cancellation and
failed-item retry. Both edit entries freeze selected IDs when a selection exists;
without one, existing filter semantics apply. Late select-all responses cannot
replace newer selections; invalid nonempty IDs never fall back to all results.
An item whose saved target is missing fails with `target_missing`. Folder/tag
filters, explicit field clearing and preservation of untouched fields remain.

Platform/status controls use official single selection and the
`account-filter-values` localStorage object. Legacy plural URL conditions are
ignored; the backend plural-parameter API and other URL/session filters remain.
Filters stay inline in both theme modes. The official five-minute billing-rate
timer retains `immediate: false` and is not started by the account page.
Vertex/Gemini quota layout follows official behavior; model sync and prompt
bindings remain independent.

## Official behavior and downstream contracts

OpenCode Zen/GO, billing states, WebSocket execution/pooling, Responses Lite,
Codex Images, provider metadata filters, batch administration and Ollama quota
reset follow the integrated official release.

Cindy uses ordinary OpenAI API-key accounts at `https://api.laxarouter.ai`, model
mappings and the ordinary `Cindy Catalog` channel. Any OpenAI-compatible account
receiving `budget_exceeded` as HTTP 429 or a terminal stream event enters error
with the upstream message; structured `model_not_supported` 400 cools only that
account/model. `openai_prompt_cache_key_mode` defaults to `passthrough`;
`sha256_64` hashes keys longer than 64 characters. Continuation,
refusal recovery and destination-specific reasoning summaries remain.

Quota aggregation, cleanup and reset use official semantics: absent rows mean
unlimited access; the official migration purges rows with three NULL limits.
The three `238_*` files keep distinct names/checksums. Account creation, reads
and schedulable changes use official `accountTransactionScope`; transactional
Create/SetSchedulable outbox failures log and continue, with commit owned by
the caller. Scheduler snapshots use official in-process memoization, revision
invalidation, original keys and batch Pipeline writes. Independent account
metadata remains; no custom namespace cleanup or Redis clearing is added.

## Model discovery, capacity and client catalogs

Capacity defaults, metadata enrichment, model templates, manifests, ETags and
client exports follow official v0.2.19. The downstream evidence resolver,
overrides/source identity, group projection, capacity interfaces and automatic
refresh are retired. Historical SQL, stored extras and diagnostic snapshots
remain. Native metadata sync, models.dev enrichment and Grok diagnostics remain.
Cindy's separate registry request/cache/write path is removed.

The account test picker resolves raw upstream IDs through saved mappings and
retains `view=plan`, actual forwarded effort and saved test choices. Manual sync
opens a confirmation picker; cancel changes nothing, and account Save persists
the confirmed models. A Grok configured fallback warns without opening the
picker. Source/account changes invalidate stale requests. API-key reveal remains
password-gated with no-store responses; closing tests preserves list position.

Codex manifests use official selection, retries and caching. Fixed
credential-source devices and strict transport apply at the actual fetch;
strict failures do not select another transport. Use Key remote configuration
uses `{root}/v1/models`; downloads append `?client_version=0.158.0`. File mode uses
`model_catalog_json`, including the official over-1-MiB fallback. OpenCode exports
use official limits without an additional Codex fetch or capacity projection.

## Claude streaming failures and signature recovery

Claude Messages and Anthropic compatibility streams use official v0.2.18 SSE
handling: `overloaded_error` before output records 529; other stream errors use
403. The downstream type classifier, pre-message buffering and same-account SSE
thinking repair are retired. Official HTTP 400 signature repair and its tool
fallback remain. Historical actual-status/signature diagnostics remain readable
without rewriting records. Official compatibility readers retain observed usage
after disconnect and report interruption according to the selected protocol.

## OpenAI forwarding and model identity

Explicit `reasoning.effort=none` is retained on OpenAI-wire accounts subject to
model validation. Requested effort accounting keeps the canonical client value;
cross-protocol `max` remains `max` except legacy GPT-5.4/5.5 mapping to `xhigh`.
Sol keeps six effort levels including `ultra`, Luna five. Model normalization and
pricing prefixes follow official v0.2.19, including Terra and dated names;
the GPT-5.6 named-family match still precedes its numeric-family match.

Responses requires an authoritative terminal event; Chat requires a supported
`finish_reason`. `[DONE]` or EOF alone cannot make an incomplete stream succeed.
Raw Chat records the resolved target separately from response-declared models
and conflicts. WebSocket composite scheduling uses the resolved target. Window
rollover removes the previous response ID without inventing an anchor; owner,
identity and verified-replay checks remain.

Streaming Responses-to-Chat tools use official behavior. Buffered compatibility
reading, errors, reconstruction and Grok auditing follow official v0.2.19;
the downstream consistency guard, dedicated error frames, raw terminal retention
and pending-signature branches are retired. Independent buffered reconstruction
still supplements completed arguments by actual call ID. Special HTTP 400
`upstream_error` classification/failover is retired; official account effects,
Retry-After waiting and disconnected-stream billing apply.

## Release and production deployment

- Normal required PR checks admit `main`; verification follows [AGENTS.md](AGENTS.md).
- Immutable `vX.Y.Z-codexrip.N` tags on admitted `main` build
  `ghcr.io/htexplicit/sub2api:X.Y.Z-codexrip.N` with digest and provenance.
- Downstream Release verifies source/build materials and reuses PR validation.
  Production Deploy takes only `release_tag` and `confirmation=DEPLOY`, resolves
  the fixed digest and uses the restricted SSH updater.
- Deployment retains configuration/resources, naturally drains and rebuilds only
  Sub2API. Other services, networking, accounts, subscriptions and routing retain
  their existing boundaries. Older tags require compatible migrations; see
  [rollback limits](.downstream/native-domains.md#release-and-rollback-boundary).
- Ordinary updates add no business backup, canary, long observation or automatic
  rollback. Failures preserve runtime state. SSH identity, fixed-image validation,
  mutual exclusion and command error reporting remain required.
- Health confirms availability, not model functionality. The operator production
  pointer and completed delivery evidence own the actual running version.

## OpenAI encrypted-content recovery

HTTP/WS recovery follows official v0.2.15, including official lineage memory.
Downstream targeted stripping, persistent rejection memory, SSE/missing-ID repair
and special failovers are retired. The global page and
`GET/PUT /api/v1/admin/reasoning-recovery` are removed; stored settings,
historical SQL and diagnostic ledgers remain. Claude HTTP 400 signature repair
and the independent `previous_response_not_found` terminal guard remain.

Cline/Command Code use existing prompt insertion points. Connection-test effort
requires declared model support and a path that retains it; unsupported options
and `ultra` remain unavailable. Official migration 242 removes platform CHECKs;
277 removes the two restored by immutable downstream 251/260/265 on new installs.
Existing quota/route rows and historical SQL names/checksums remain intact.

## Upstream updates

[Release-channel rules](.downstream/README.md) own discovery, risk labels,
promotion and production approval. Discovery never deploys by itself. Each
integration records its official commit and retained contracts. The current
risk report compares v0.2.18 to v0.2.19 against downstream
`6ac28b7ecf2e247b1dbe5985b6ad115016aa514f`; its gate recomputes those exact inputs.
The [current review](.downstream/upstream-review-v0.2.19.md) records all 28 initial
conflicts and resolutions. Promotion requires completed review, no unresolved
conflicts and `upstream-reviewed` where review is required.

Official v0.2.19 owns scoped administrator keys, image-count billing (default
off), Terra, Gemini/Grok fixes and DeepSeek Pro/Flash/time-based pricing and
cache-hit accounting. The downstream price registry is retired; generic override
files and independent DeepSeek default reasoning remain. Earlier integrations'
Gemini/Vertex, quota, payment, scheduling and dependency decisions are recorded
in their versioned reviews. Immutable official and downstream migration files
with the same numeric prefix coexist under their complete filenames/checksums.
Current build versions come from manifests and workflows, not this handbook.

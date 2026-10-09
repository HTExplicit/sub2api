# HTExplicit Sub2API Downstream

This fork maintains the `codexrip` patch set over official Sub2API releases.

## Source baseline

- Integrated official baseline: the release recorded in `.downstream/upstream-base`.
- Retained compatibility decisions: [upstream review](.downstream/upstream-review-v0.2.15.md).
- The operator workspace `docs/sub2api.md` owns the production version and image digest. A source merge or Release does not establish deployment completion.

## Settings and account operations

The Codex runtime and console theme settings interfaces, the conditions on
which migrations 264 and 272 stop startup, the stored data the migrations
removed and the rollback boundary are described in
[settings, stored data and rollback](.downstream/native-domains.md).

Account bulk operations remain HTTP 202 jobs with progress, cancellation and
failed-item retry. Both edit entries use the frozen selected IDs whenever there
is a selection; without a selection, filter-based updates retain their existing
semantics. A late select-all response cannot replace a newer manual selection.
Invalid nonempty ID lists cannot fall back to all filter results.

Folder/tag filters, explicit field clearing and untouched-field preservation
remain supported. A bulk-update item without a saved target account fails with
`target_missing` rather than selecting a fresh set of accounts.

The Codex runtime setting is one switch, request body compression for the
streaming `/responses` turns of OpenAI OAuth accounts; its contract is in
[settings, stored data and rollback](.downstream/native-domains.md).

## Official behavior and downstream contracts

OpenCode Zen/GO, site billing states, WebSocket execution scope and pooling,
Responses Lite namespaces, native Codex Images, model metadata/provider filters,
batch administration and Ollama asynchronous quota reset follow official behavior.

Cindy accounts are ordinary OpenAI API-key accounts (`https://api.laxarouter.ai`):
their models come from model sync and the account model mapping, and their prices
from the ordinary `Cindy Catalog` channel. On any OpenAI-compatible account, a
`budget_exceeded` HTTP 429 or terminal stream event puts the account into the error
state with the upstream message; the structured `model_not_supported` 400 cools
only that account/model. The account option `openai_prompt_cache_key_mode`
(`passthrough` by default, or `sha256_64`) replaces a prompt cache key longer than
64 characters with its SHA-256 hex. Continuation, refusal recovery and
destination-specific reasoning summaries remain supported. Account test model
choices persist; API-key reveal requires the configured password. The account test
dialog keeps list position.

Quota storage, aggregation, cleanup and resets follow official semantics. Missing
quota rows represent unlimited access; rows with three NULL limits are purged by
the official migration.
The three `238_*` migrations retain separate filenames and checksums.

## Model discovery and context capacity

The account test picker reads raw upstream model IDs and applies the saved mapping
itself. Configured request names remain testable when the upstream catalog omits
their target; wildcard mappings select request IDs from that raw catalog. The
ordinary account-model projection keeps official mapping and passthrough semantics.
Capacity resolves the real account/channel targets, rather than public aliases.

Every provider and account type uses
`custom > applicable official API > source-bound upstream > registry > unknown`.
One complete automatic evidence record wins: omitted input/output limits are not
filled from another source. A custom window overlays that record and keeps only
its compatible independently declared input/output limits. The effective window
is `max_context_window`, then `context_window`, then the input limit; a smaller
explicit maximum is still authoritative. Group capacity is the minimum of the
resolved targets of active accounts, independent of their schedulable toggle.
Unknown capacity stays unknown, with no invented fallback or hard expiry.

`upstream_model_metadata.source_identity` binds capacity observations and registry
enrichment to the normalized endpoint and protocol, including Responses mode and
escaped URL paths. An unbound or old-endpoint snapshot remains visible to
administrators but supplies no current capacity. Asynchronous writes use the
account revision gate; one reread/retry is allowed only when the source and complete
stored snapshot, including an empty catalog, are unchanged. Persistence retries
never fetch the upstream catalog again or resurrect a superseded observation.

Administrator rows, `/v1/models` and Codex manifests share this resolver. Locally
generated Codex descriptors have no capacity defaults, including the GPT-5.6,
GPT-6 and GPT-6.1 Sol families; real upstream fields remain when no effective
evidence supersedes them. OpenCode exports limits only from resolver-tagged
manifest rows (`custom`, `official`, `upstream`, `registry`) when the client schema's
required values are known, without a separate hard-coded capacity table. The
capacity self-test remains version 4 with
`priority=custom,official,upstream,registry`.

The Use Key catalog panel and fetch remain available for Codex tabs, including
OpenAI groups, and for the OpenCode tab. Codex configuration defaults to
`model_catalog_url = "{root}/v1/models"`; Codex appends its own `client_version` to
reach the same manifest handler. Fetching and downloading the catalog uses
`{root}/backend-api/codex/models` without a pinned client version. The alternative
is the downloaded `model_catalog_json` file; a response over 1 MiB switches the
Codex configuration to that file mode. OpenCode continues reading the manifest
regardless of the Codex remote/file selection.

## Claude streaming failures and signature recovery

Ordinary Claude Messages forwarding classifies SSE error events by their error
type, with a narrow thinking-signature fallback for third-party errors missing
the type. A stream error is not an HTTP permission rejection. In Ops attempt
JSON, `stream_error.upstream_status_code` retains the error's semantic status;
the optional `upstream_http_status_code` records the actual response status.
Existing records and HTTP error semantics remain compatible without a migration.

When the existing signature rectifier permits it, ordinary Claude forwarding
can repair rejected thinking history on the same account before any message
event is committed. It buffers only `message_start`, up to 64 KiB, while sending
ping immediately. Committing the next message event or exceeding the bound
ends recovery eligibility. The first thinking repair has one attempt independent
of the initial request's ten-second backoff budget; the existing HTTP-only tool
fallback retains its original conditions. Automatic passthrough and other
provider paths do not gain this repair. Fingerprint-only recovery diagnostics
stay attached to the upstream attempt and do not replace its failure or expose
new thinking text or signature values.

## OpenAI forwarding and model identity

Explicit `reasoning.effort=none` is preserved on OpenAI-wire accounts, including
compatible hosts, subject to the selected model's validation. Downstream effort
accounting preserves the requested canonical effort rather than substituting the
provider-normalized value. Explicit cross-protocol normalization retains `max`,
except that legacy GPT-5.4/5.5 models map it to `xhigh`. GPT-6 Sol's Codex catalog
keeps the six-level workflow including `ultra`; Luna keeps five levels.

Finite known model/effort aliases are normalized, while undocumented dated GPT-6
snapshots, including GPT-6.1 Sol, pass through unchanged. Unrecognized `gpt-6-*`
and `gpt-6.*` names do not borrow the default model's price; exact configured
price cards are still considered first.

Responses streams require an authoritative terminal event. Chat Completions
streams require a supported `finish_reason`; `[DONE]` and transport EOF alone
cannot turn an incomplete stream into success. Raw Chat Completions does not
write Ops `upstream_model` before dispatch: the forwarding result keeps the
resolved target and separately records the model declared by the response and
any conflicting response declarations. WebSocket composite routing uses the
resolved forward target for scheduling. A Codex WebSocket window rollover removes
the previous response ID without inferring an anchor from the old window; owner,
identity and verified-replay checks remain in force.

## Release and production deployment

- `main` is admitted through the normal required PR checks.
- `.downstream/upstream-base` records the integrated official release.
- New releases use immutable `vX.Y.Z-codexrip.N` tags on `main`.
- Images use the tag without `v`: `ghcr.io/htexplicit/sub2api:X.Y.Z-codexrip.N`.
- Downstream Release authenticates to GHCR, verifies source/build materials and
  publishes the image digest and provenance. It reuses PR validation.
- Production Deploy resolves the fixed digest through the existing restricted SSH
  updater. Its only inputs are `release_tag` and `confirmation=DEPLOY`; the host
  retains runtime settings and resources, naturally drains requests, and rebuilds
  only Sub2API. An older tag uses the same form and requires compatible migrations.
- Ordinary updates do not run business backups, canaries, long observation or
  automatic rollback. A failure preserves the runtime state for diagnosis.
- SSH identity checks, fixed image validation, mutual exclusion and command error
  reporting remain required. Other services, networking, accounts, subscriptions
  and manual routing stay within their existing configuration.
- Container/public health confirms availability, not actual model functionality.

## OpenAI encrypted-content recovery

HTTP and WebSocket encrypted-content recovery follows official v0.2.15,
including the official session lineage memory. The former downstream targeted
stripping, persistent rejection memory, SSE repair, missing-item-id repair and
special account failovers are retired. The global recovery page and its
`GET/PUT /api/v1/admin/reasoning-recovery` interface are removed. Stored settings,
historical SQL and diagnostic records remain intact; ordinary Claude thinking
signature recovery is independent and remains available.

Cline and Command Code share the existing system-prompt insertion points.
Connection-test reasoning options require declared model support and a wire
path that retains the chosen effort; unsupported and `ultra` options remain
unavailable. The compact account filters retain multi-selection, tags, proxy,
plan and count controls; hiding additional filters never resets their values.

Official migration 242 removes platform CHECK constraints. Immutable downstream
251, 260 and 265 restore the quota constraint during a fresh installation, so
277 repeats the final removal after them. No historical checksum or existing
quota/route row is changed by 242 or 277.

## Upstream updates

Scheduled discovery may identify newer official releases. It cannot deploy
production. Each integration records its selected official commit, conflicts and
retained downstream contracts before a new immutable release is published.

`.downstream/upstream-risk.json` records the v0.2.14-to-v0.2.15 integration against
downstream commit `d469422e94b0655b22dd9d0a826decda61ffb91e`. The risk gate
recomputes those exact inputs. `initial_conflict_files` lists the 40
conflicts resolved during this merge; `merge_conflicts: []` records that none
remain. A `review_required` candidate needs a real completed review and the
`upstream-reviewed` label before promotion.

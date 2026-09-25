# HTExplicit Sub2API Downstream

This fork maintains the `codexrip` patch set over official Sub2API releases.

## Source baseline

- Integrated official baseline: `v0.2.8`, commit `fd80b08c90b55edcad5b00171b53f08721d30da1`.
- Retained compatibility decisions: [upstream review](.downstream/upstream-review-v0.2.8.md).
- The operator workspace `docs/sub2api.md` owns the production version and image digest. A source merge or Release does not establish deployment completion.

## Native domains and account operations

The seven first-party domains now run inside the host and use native Vue pages.
The official third-party plugin framework remains available. Domain settings,
startup data preservation and the legacy-host rollback boundary are described in
[native domains](.downstream/native-domains.md).

Account bulk operations remain HTTP 202 jobs with progress, cancellation and
failed-item retry. Both edit entries use the frozen selected IDs whenever there
is a selection; without a selection, filter-based updates retain their existing
semantics. A late select-all response cannot replace a newer manual selection.
Invalid nonempty ID lists cannot fall back to all filter results.

Folder/tag filters, explicit field clearing and untouched-field preservation
remain supported. Old jobs retain actor, encrypted payload, expiry and frozen
item targets. An old filter-only job without a saved target fails rather than
selecting a fresh set of accounts.

Codex routing applies only to OpenAI OAuth/setup-token accounts, excluding shadows.
Acquisition and stopping renewal use the existing persisted task experience.
Routing qualification, connection leases and the closed quality-run ledger keep
their existing contracts; a 292 header or a healthy deployment is not a quality
result. Proxy settings preserve their saved values and accept the existing input
formats. Draft proxy tests do not send OAuth credentials or model requests.

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
dialog keeps list position. Image Studio defaults off and lists no eligible key
until a generic image model source exists. Public model management remains
retired; catalog changes do not restore its routes or navigation.

Quota storage, aggregation, cleanup and resets follow official semantics. Missing
quota rows represent unlimited access; rows with three NULL limits are purged by
the official migration.
The three `238_*` migrations retain separate filenames and checksums.

## Release and production deployment

- `main` is admitted through the normal required PR checks.
- `.downstream/upstream-base` records the integrated official release.
- New releases use immutable `vX.Y.Z-codexrip.N` tags on `main`.
- Images use the tag without `v`: `ghcr.io/htexplicit/sub2api:X.Y.Z-codexrip.N`.
- Downstream Release authenticates to GHCR, verifies source/build materials and
  publishes the native host image digest and provenance. It reuses PR validation;
  first-party package signing and separate plugin release assets are retired.
- Production Deploy resolves the fixed digest through the existing restricted SSH
  updater. Ordinary updates use `operation=deploy-preserve`, preserving runtime
  settings and resources, naturally draining requests, and rebuilding only Sub2API.
- Ordinary updates do not run business backups, canaries, long observation or
  automatic rollback. A failure preserves the runtime state for diagnosis.
- SSH identity checks, fixed image validation, mutual exclusion and command error
  reporting remain required. Other services, networking, accounts, subscriptions
  and manual routing stay within their existing configuration; CPA stays stopped.
- Container/public health confirms availability, not actual model functionality.

## Upstream updates

Scheduled discovery may identify newer official releases. It cannot deploy
production. Each integration records its selected official commit, conflicts and
retained downstream contracts before a new immutable release is published.

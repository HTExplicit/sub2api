# HTExplicit Sub2API Downstream

This fork maintains the `codexrip` patch set over official Sub2API releases.

## Source baseline

- Integrated upstream: recovered `v0.2.6`, exact commit `49a39b6dc1abed30fd227611e8af1108bc427610`. The public tag was withdrawn when recovered; source is pinned to the commit, not a recreated official tag.
- Integration base: production `v0.2.5-codexrip.14`, commit `a2f13d34a41bb89225e5d6864d466b4a35d6448f`; embedded version is `0.2.6`.
- Conflict decisions and validation: [v0.2.6 review](.downstream/upstream-review-v0.2.6.md); exact-SHA evidence: [risk manifest](.downstream/upstream-risk.json).
- The operator workspace `docs/sub2api.md` owns the current production pointer. A source merge or Release alone does not establish deployment completion.
- The full recovered tree includes the official changes after v0.2.5 and all six Codex ticket patches. Ticket collection/injection is disabled in this deployment; the admin settings expose the proxy and live switch for later explicit activation.
- Tickets apply only to OpenAI OAuth/Setup Token accounts, excluding shadows. Cindy/API-key paths retain their existing identity, health, quota and sticky-session behavior.

## Official behavior and downstream contracts

OpenCode Zen/GO, site billing states, WebSocket execution scope and pooling,
Responses Lite namespaces, native Codex Images, model metadata/provider filters,
batch administration and Ollama asynchronous quota reset follow official behavior.

Cindy keeps its platform identity, catalog, health/budget handling and existing group
membership. Continuation, opaque lineage, refusal recovery and destination-specific
reasoning summaries remain supported. Account test model choices persist; API-key
reveal requires the configured password. The account test dialog keeps list position.
Image Studio and the Responses image bridge default off. Public model management
remains retired; catalog changes do not restore its routes or navigation.

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
  publishes an image digest and provenance. It reuses PR validation.
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

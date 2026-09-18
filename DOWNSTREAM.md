# HTExplicit Sub2API Downstream

This fork maintains the `codexrip` patch set over official Sub2API releases.

## Source baseline

- Official baseline: `v0.2.5`, peeled commit `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`.
- Version-only sync: `881f3202694c6bc932446931a30c27d9675178b9`; embedded version `0.2.5`.
- Integration starts at production `v0.2.4-codexrip.11`, commit `4c96fb375a68744ddf356ffb3f64afac5e21894b`.
- Conflict decisions and validation: [v0.2.5 review](.downstream/upstream-review-v0.2.5.md).
- The operator workspace `docs/sub2api.md` owns the current production pointer.
  A source merge or Release alone does not establish deployment completion.

### Recovered upstream v0.2.6

- The withdrawn upstream release is recovered from exact merge commit `49a39b6dc1abed30fd227611e8af1108bc427610`; the remote `v0.2.6` tag is not used as a source.
- The merge includes the official changes after v0.2.5 and the six Codex ticket patches. The ticket feature remains runtime-disabled in the published deployment until an administrator supplies a harvest proxy and enables it from the settings page.
- Ticket collection and injection apply only to OpenAI OAuth and Setup Token accounts. Cindy/API-key accounts retain their existing identity, health, quota and sticky-session behavior.
- The integration review and one-off exact-SHA risk evidence are recorded in `.downstream/upstream-review-v0.2.6.md` and `.downstream/upstream-risk.json`.

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

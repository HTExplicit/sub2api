# HTExplicit Sub2API Downstream

This fork maintains the `codexrip` patch set over official Sub2API releases.

## Source baseline

- Integrated upstream: recovered `v0.2.6`, exact commit `49a39b6dc1abed30fd227611e8af1108bc427610`. The public tag was withdrawn when recovered; source is pinned to the commit, not a recreated official tag.
- Integration base: production `v0.2.5-codexrip.14`, commit `a2f13d34a41bb89225e5d6864d466b4a35d6448f`; embedded version is `0.2.6`.
- Conflict decisions and validation: [v0.2.6 review](.downstream/upstream-review-v0.2.6.md); exact-SHA evidence: [risk manifest](.downstream/upstream-risk.json).
- The operator workspace `docs/sub2api.md` owns the current production pointer. A source merge or Release alone does not establish deployment completion.
- The full recovered tree includes the official changes after v0.2.5 and all six Codex ticket patches. Deployment preserves the existing live ticket switch and proxy; source defaults remain disabled.
- Tickets apply only to OpenAI OAuth/Setup Token accounts, excluding shadows. Cindy/API-key paths retain their existing identity, health, quota and sticky-session behavior.

## Account tests and ticket lifecycle

- Single and batch text tests accept an optional user prompt, up to 8192 Unicode characters; blank uses `hi`. The browser remembers text separately from media, per site and administrator. Batch prompts are held in the existing encrypted task payload, while scheduled tests retain their defaults.
- Initial 292 harvesting is manual, per account and model, with one model request per item, up to 100 accounts and five concurrent requests. Valid tickets are skipped unless explicitly refreshed. The existing task drawer provides progress, cancellation and failed-item retry.
- Successful account/model pairs renew once 60 seconds before expiry, then once 60 seconds after expiry if needed. Two failures stop renewal until another manual success. Startup imports only still-valid legacy tickets and never revives stopped enrollments.
- Migration `244_codex_ticket_lifecycle.sql` stores durable stages and execution leases. Ticket material remains solely in account Extra; ticket and lifecycle writes commit together. Account disablement, deletion or principal changes invalidate in-flight claims; token refresh preserves the principal.
- The ticket proxy editor accepts URLs, colon-separated fields and labeled fields in any order. Only authenticated administrator settings responses reveal the full proxy credentials; those responses prohibit caching. Explicit clearing also disables tickets.
- Draft connection tests send no OAuth credentials or model requests. Private certificates can be learned only during an unauthenticated ChatGPT preflight, remain bound to that proxy configuration generation and hostname, and must pass hostname/validity checks. Automatic renewal does not learn certificate changes; manual harvesting or testing the configured proxy can update its trust. Business transports and system roots are unchanged.
- OpenAI OAuth text tests apply the same account/model ticket policy as forwarding. Diagnostic events expose presence, length and a SHA-256 fingerprint, never the ticket itself. A successful connection or a 292 header does not establish model quality.

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

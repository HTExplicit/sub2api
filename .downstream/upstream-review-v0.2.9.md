# Upstream v0.2.9 integration and API context capacity

## Source

- Downstream base: `fb7467b7a2479cd9092fc4b1e659c6c8e9bba7b4` (`v0.2.8-codexrip.12`).
- Upstream: stable tag `v0.2.9`, tag object `8532ec28b56188d3c845ed97070e6ee5136bd9bd`, commit `4c00df2e0183e2c70b7fa8ba45914205e36aad0c`.
- Upstream delta: 70 commits, 117 paths; 61 overlap downstream changes and 37 match the existing critical-path classifier. Six actual conflict paths were resolved by semantic review. The complete classifier output remains in `upstream-risk.json`.
- There are no upstream schema, migration or dependency changes. Existing migration names and checksums are unchanged. The snapshot extension uses an optional JSON field, without SQL backfill.

## Integration decisions

- Adopt arbitrary-position allowlist globs and passthrough model discovery without hiding configured models. Capacity still resolves actual account/channel mapping targets, not public aliases.
- Adopt `thinking.type=disabled`, GPT-5-or-newer reasoning detection, Responses message item types, seeded tool input preservation and streamed text recovery. Keep explicit `none`, downstream `max` semantics, exact date aliases, opaque reasoning state and authoritative terminal-event requirements.
- Forward the upstream-supported `OpenAI-Beta` header with its legacy Responses token cleanup. Keep downstream session/thread/request headers and body-only installation identity.
- On a Codex WebSocket window rollover, remove the previous response ID and do not infer an anchor from the old window. Keep the existing owner, identity and verified-replay rules. Do not resurrect retired encrypted-lineage rewriting.
- Combine upstream client-disconnect 499 handling with downstream quality-request isolation, selection-scoped reporting and cooldown behavior. Antigravity keeps both upstream empty-stream errors and downstream adapter protocol errors.
- Keep `OpenAIQuotaWindows` as the source of primary/secondary quota deadlines, including future reset deadlines. Adopt scheduler snapshot auto-reset fields and retry/backoff improvements.
- Carry the account long-context pricing flag through the complete account-cost call chain. Adopt inherited image pricing, zero-cost Free Fast records and terminal-success alpha-search billing while preserving downstream reasoning-effort accounting.
- Adopt the admin/client bug fixes and Redis exec-list compose templates while retaining the Apple theme and complete admin visibility. Production deployment does not apply compose-template changes or rebuild Redis.
- Draft PR #208 and its production hold remain separate; this merge contains no commits from that branch.

## Capacity contract

- All account types and providers use `custom > applicable official API > source-bound upstream > registry > unknown`.
- Select one complete automatic evidence record. A winning official record never borrows omitted input/output limits from a relay or Codex subscription observation. A custom window overlays that record, retaining only its compatible independently declared I/O limits; unknown independent limits stay absent.
- The effective planning window is the explicit `max_context_window`, otherwise `context_window`, otherwise the independently declared input limit. A smaller explicit maximum remains authoritative. Group aggregation takes the minimum of the resolved real targets among active accounts, independent of the schedulable toggle.
- GPT primary records now contain API specifications and precise source links. Old Codex reference records are diagnostic only. Provider/product host constraints and finite identity aliases remain unchanged.
- `upstream_model_metadata.source_identity` binds observations and endpoint-derived registry enrichment to their normalized endpoint and protocol contract, including OpenAI Responses mode and escaped URL paths. Old unbound observations remain visible for diagnosis but do not become current evidence. Source changes and late asynchronous responses cannot rebind them. Conditional writes use the existing row revision gate with one bounded reread/retry only when the source and complete stored catalog (including an empty catalog) are unchanged; they never refetch upstream merely to retry persistence. Deduplication also checks the current snapshot.
- No new hard TTL, no invented default capacity, no SQL backfill and no changes to administrator overrides or account mappings.
- Admin rows, ordinary models and Codex manifests use the same resolver. Locally generated Codex descriptors contain no capacity defaults; real upstream fields are preserved when no effective evidence supersedes them. OpenCode configuration reads the existing manifest instead of maintaining a second numeric table; it emits a limit only when the client schema's required values are known.
- Runtime self-test contract is version 4 with `priority=custom,official,upstream,registry`; companion operational fixtures follow that exact line.

## Validation and release

- Validation is limited to the changed resolver, source lifecycle, projection, protocol and frontend behavior, plus normal required PR checks. Successful checks for the same code are reused; no real model requests or production migration exercises are part of this release.
- Target release: `v0.2.9-codexrip.1`, fixed downstream image digest, `deploy-preserve`, resource profile `preserve`, natural drain, only the Sub2API container recreated.
- The final source SHA, PR/check results, image digest and production outcome belong to the operational delivery evidence. Source integration alone does not claim a successful deployment or model-quality acceptance.

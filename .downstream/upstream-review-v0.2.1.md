# Upstream v0.2.1 integration review

> Document type: release integration evidence
> Status: candidate; production acceptance pending
> Last verified: 2026-09-05

The integration baseline is downstream main `5b8c1092a5c5c46cf0b49825dc957d537ada3de2`
and official `v0.2.1` (peeled commit `578785ee7`). The correct merge has 33 conflicted
paths; the earlier 87-path estimate used an obsolete local main and is not valid.
The machine-readable upstream risk report is recomputed against the actual production baseline.

## Merge decisions

- Generated Ent code was rebuilt from the merged schemas. Account taxonomy, strict Cindy
  identity and pinned Codex manifest fields coexist. Wire was regenerated.
- Conflicted source files were restored from Git's automatic merge tree and reviewed
  hunk by hunk. The temporary incomplete merge resolution was not released.
- Channel-mapped models feed capability selection, while client model names remain
  available for billing and output. Cindy compatibility aliases and opaque continuation
  affinity retain precedence; exact-account retry leases are released after reporting.
- The compact admin account DTO retains downstream taxonomy and Cindy identity/health
  fields but omits repeated groups/account_groups graphs. Full detail is loaded on demand.
- Upstream replay bodies use immutable shared ownership. Downstream continuation recovery
  and business prompts remain; invalid encrypted-content digests are recorded by existing
  bounded recovery and used to strip repeated rejected history.
- Direct upstream headers, Astra capability data, Ultrafast tiers, pinned manifests,
  reasoning pricing and image URL-to-base64 behavior are integrated.
- OAuth manifest caching is integrated, but account-health mutation remains in the
  downstream handler's shared scheduling/retry path. Legacy upstream tests superseded by
  the downstream ownership tests were not resurrected. This avoids duplicate cooldown
  and token-revocation writes.
- Authentication snapshots advance to v24 because v23 already existed downstream; stale
  Redis/L1 snapshots must not silently omit the new manifest configuration.

## Requested fixes

- Completed account jobs and cascading result items expire after 24 hours. In-flight
  tasks and existing encrypted-payload TTL are unchanged. No schema migration or VACUUM FULL.
- Task lists/drawers have no background polling. Explicit refresh uses cancellation and
  request sequencing; expired selections are cleared without repeatedly requesting 404s.
- TLS fingerprint TCP, CONNECT, SOCKS and handshake setup are bounded and cancellable.
  Successful connections clear setup deadlines. Certificate verification/profile bytes
  and ordinary OpenAI HTTP/2 behavior remain unchanged.
- Manual stable synchronization supports an exact tag and stops on existing candidate
  branches rather than overwriting manual resolutions. Reviewed promotion validates the
  exact head and all required checks, publishes an immutable tag and explicitly dispatches
  release/deploy workflows. Production approval, flags and resource preservation remain.

## Validation

Frontend typecheck and 69 focused frontend cases passed. Task cleanup SQL/cutoff cases,
selected manifest/capability integration cases and offline fingerprint deadline/HTTP/SOCKS
tests passed locally. Initial merge-signature and fixture mismatches were corrected and only
affected tests rerun. Existing CI/release hard gates and live platform acceptance remain
mandatory; no full local backend suite or production stress test was run.

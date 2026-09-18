# Recovered upstream v0.2.6 integration review

## Source and provenance

- Downstream base: `v0.2.5-codexrip.14`, commit `a2f13d34a41bb89225e5d6864d466b4a35d6448f`.
- Recovered upstream commit: `49a39b6dc1abed30fd227611e8af1108bc427610`.
- Upstream parents: `efe9aab1e4ec89a42ba45e8dac20e882c5409a6a`, `3c2f05c957b4b93866318ec8695fc5a28fff70eb`.
- Upstream tree: `61de86700f217d58c337998f3c51a89969493267`.
- The public `v0.2.6` tag/Release is withdrawn. This review uses the exact commit object; it does not use a mutable tag or `latest` image.
- Machine-readable evidence: `.downstream/upstream-risk.json`. It records the exact commit, tree, parents, tag-unavailable state, overlap counts and the original Git conflict list.

## Integration scope

The complete recovered merge tree is integrated. This includes the upstream changes after the official v0.2.5 baseline and the six Codex ticket patches: ticket harvesting, live management settings, ticket injection, export/edit redaction, compact-model gating and scheduler-neutral ticket writes.

Ticket runtime defaults remain safe: `enabled=false`, empty harvest proxy, `fail_closed=true`, 292-character state, one-hour TTL, ten-minute refresh window, and the upstream target models. No harvest proxy is written during this release and the management switch stays off.

## Conflict decisions

- Account DTOs retain downstream fields for provider identity, folders, tags, Cindy state, quotas, shadow accounts and parent metadata; upstream `codex_turn_tickets` status is added without returning private state.
- Locked account Extra merging retains downstream model-context capacity/override/metadata values and upstream current ticket entries. Non-object historical Extra values degrade ticket preservation without blocking account edits.
- Account Extra updates preserve downstream reasoning/fingerprint/quota protections and reject administrator-supplied ticket material.
- Wire generation retains downstream account jobs, Cindy health/balance, system prompts, remote skills, Image Studio, traffic telemetry and identity backfill while wiring the upstream SettingService, PluginManager and ticket harvester lifecycle.
- OpenAI OAuth/Setup Token scheduling combines official outbound-model/compact ticket gating with downstream runtime-breaker, probe-ownership, cooldown and continuation/sticky protections. Cindy/API-key paths do not enter the ticket predicate.
- Response affinity uses the downstream cancellation-independent Redis context and upstream owner-binding diagnostics.
- Frontend settings and account views retain downstream Cindy and permission behavior while adding ticket status, masked proxy settings and live-toggle coverage.
- Public model management remains retired; Image Studio and Responses image bridge remain off by default.

## Validation status

- Passed: Wire regeneration, focused backend ticket/account/DTO tests, focused frontend settings/account/dialog/manifest tests, frontend production build and `git diff --check`.
- Pending: full compile/vet and required PR checks.
- Not run: real upstream model calls, harvest-proxy calls, management-page activation, canary, long observation and production deployment.

## Release boundary

The candidate release is `v0.2.6-codexrip.1`. Production deployment uses the existing immutable-digest `deploy-preserve` path and changes only the `sub2api` container. The release does not claim that ticket quality improvements have been model-validated while the feature remains disabled.

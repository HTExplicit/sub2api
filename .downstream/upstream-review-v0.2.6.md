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
- Passed: full backend compile (`go test -run '^$' ./...`), `go vet ./...`, `go mod verify`, focused runtime/continuation/Cindy tests and risk/automation contract tests.
- Passed: US pre-deploy and post-deploy `resource-preflight` baselines; evidence `artifacts/evidence/us-runtime-audit-2026-09-18_140357_769694.log` and `artifacts/evidence/us-runtime-audit-2026-09-18_150841_483754.log` record the `.14` and `v0.2.6-codexrip.1` images, digest, healthy container, resource profile, guard hashes and unchanged companion services.
- Passed: applicable PR checks and main CI/security/Downstream Verify; PR and release trees match (`970fb93bb74577d46469d63b744a125dfa343b9c`). The ordinary-branch Upstream risk gate was skipped by its existing condition, not executed as a successful classification; the exact-SHA manifest is the review evidence.
- Passed: [Release](https://github.com/HTExplicit/sub2api/actions/runs/35358623663) and [deploy-preserve](https://github.com/HTExplicit/sub2api/actions/runs/35359519253). Runtime digest `sha256:fc8a1d7ec4dc66820ac2fbffde74f8c3109459e61c6f5c57b62c799e97afc6ca` and revision match the Release. Health passed after two startup 502 retries.
- Read-only admin verification: ticket disabled, harvest proxy absent, OpenAI OAuth and Setup Token account counts both zero. No real model quality result is claimed.
- Not run: real upstream model calls, harvest-proxy calls, management-page activation, canary, long observation and automatic rollback.

## Release boundary

Released and deployed: `v0.2.6-codexrip.1`, source `4bb9eabca65d17c9c392945b332e3914b2ff16c3`. Production deployment uses the existing immutable-digest `deploy-preserve` path and changes only the `sub2api` container. The release does not claim that ticket quality improvements have been model-validated while the feature remains disabled.

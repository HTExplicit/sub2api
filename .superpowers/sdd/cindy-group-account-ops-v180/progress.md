# SDD ledger — plan: D:/Data/codex_work/my_server/artifacts/tmp/sdd-plans/cindy-group-account-ops-v180.md

## Baseline

- Base: `69478fa0a9fa35662e4ab21462893dae1a442168` (`v0.1.179-codexrip.1`, `origin/main`).
- Production read-only audit on 2026-08-24: platform profile active; 27 strict Cindy accounts; 1 strict group; 27 capability records missing; 0 attached/active channels; all Cindy rollout flags off.
- Public international Cindy schema-v4 refresh: 22 models, 16078 bytes, SHA-256 `b2783df6c272fc9851c85f3ffe871c962a6ab701f7698be1893fbfd03a5f28d3`.
- Frontend baseline: 267 files / 1798 tests pass.
- Backend baseline: all packages except the monolithic `internal/service` package completed; its default full-package run hit the 10-minute timeout. The exact long-running test observed at a 30-second diagnostic timeout passes alone in 7.2 seconds. A `-parallel 4` package run passed in 195.3 seconds, confirming local parallel saturation rather than a deterministic failing assertion.
- User ruling: local verification is minimal and root-cause scoped; no long or unnecessary suites. Full matrices run in PR/release CI.

## Pre-flight interface scan

| Producer task | Consumer task | Shared interface/files | Finding |
|---|---|---|---|
| Task 1 catalog | Tasks 2, 4, 6 | `cindy_capability_catalog.go`, group candidates, scheduler, public DTOs | One exact 22-model source must feed every consumer; no duplicated model tables. |
| Task 1 catalog | Task 5 billing | pricing DTOs and exact model resolution | Billing must fail closed on incomplete exact pricing while public catalog remains inspectable. |
| Task 2 groups | Task 3 import | canonical identity and strict group validation | Import must consume the group contract; it cannot infer a target from legacy group names. |
| Task 2 groups | Task 6 UI | group/account filter parameter | Backend and UI move together to `group_id`; no runtime dual-parameter compatibility. |
| Task 2 channels | Task 7 acceptance | `channel_groups`, pricing/mapping platform keys | Channel attachment alone is insufficient; Cindy pricing/mapping rows and cache invalidation must be coherent. |
| Task 3 import | Task 4 terminal states | credential generation and transaction boundary | Import update/rotation must invalidate stale terminal evidence atomically. |
| Task 3 import | Task 5 duplicate inventory | normalized Cindy credential identity | Preview and inventory share one fingerprint normalizer; raw keys remain unobservable. |
| Task 4 health | Task 5 usage | failed attempt versus final owner | Terminal/failover attempts never bill; only the final successful owner reaches usage commit. |
| Tasks 2-5 backend | Task 6 UI | stable DTOs, business error codes, job kinds | UI starts only after backend contracts are reviewed. |
| Tasks 1-6 | Task 7 delivery | migrations, generated Ent/Wire, rollout profile | Regenerate once after schema/API stabilization; CI owns full-suite execution. |

## Task self-consistency scan

| Task | Code/tests alignment | Finding |
|---|---|---|
| 1 | 22 exact v4 models, public/live IDs, prices, protocol matrix | Consistent; special Search/Review services must leave the model list but keep their endpoint implementations. |
| 2 | strict identity, channel topology, group candidates/filter | Consistent after selecting one `group_id` contract and a deterministic no-ID channel migration. |
| 3 | preview and execution share one decision engine | Consistent; no extra preview-confirmation token is introduced. |
| 4 | first-signal terminal states and physical cleanup | Consistent; Spark-child RESTRICT and Redis/runtime cleanup are required parts of physical deletion. |
| 5 | final-success owner, reset watermark, duplicate inventory | Consistent; operational failed-attempt evidence is not a billing or usage row. |
| 6 | existing full account console and task drawer | Consistent; no reduced member table and no page-level mobile overflow. |
| 7 | minimal local tests plus remote full gates | Consistent with the user's latest explicit testing instruction. |

## Rulings

- Ruling: The approved user plan supersedes the older `cindy-sub2api-v179-slim` non-goals; import preview, first-signal terminal health, final-owner usage, and complete UI are now in scope. Cost if wrong: scope expands beyond the older release brief, but matches the newest explicit approval.
- Ruling: `cindy/auto-review` and `cindy/web-search` remain separate service endpoints but are not entries in the 22-model v4 catalog. Cost if wrong: clients that treated special services as models must use their dedicated endpoints.
- Ruling: Current v4 fields are authoritative for claimed Cindy pricing. Extra historical fields absent from v4 are not advertised as v4 facts; incomplete billable pricing fails closed. Cost if wrong: an upstream price dimension omitted from v4 remains unavailable until a new snapshot.
- Ruling: The account-list filter becomes `group_id` end to end. All repository-owned callers are updated in the same task; no `group` alias is retained. Cost if wrong: external callers using the undocumented old name must update.
- Ruling: Existing pool-mode status defaults are not broadened or re-hardcoded. Ordinary 429 follows the account-resolved configured list, then the current Retry-After/default cooldown. Cost if wrong: an operator expecting empty-list=no-retry may need a separate configuration migration.
- Ruling: Automatic Luna/Terra confirmation is removed from request-driven balance classification. Explicit administrator diagnostics may remain only as diagnostics and cannot delay or reverse first-signal terminal marking. Cost if wrong: keeping the diagnostic surface adds maintenance but preserves current operations.
- Ruling: Physical cleanup intentionally follows the approved cascade, including `usage_logs`; Spark children are deleted in dependency order rather than bypassing the FK. Cost if wrong: historical account-level usage is irrecoverable after the confirmed job, as explicitly approved.
- Ruling: Migration 236 may bind a strict Cindy group only through deterministic canonical channel criteria and must fail closed on ambiguity; it cannot hard-code production IDs or silently borrow OpenAI platform pricing. Cost if wrong: ambiguous installations require explicit administrator repair instead of automatic guessing.
- Ruling: Import preview is informational and shares the exact decision engine with job execution; it does not require a hash/token round trip. Cost if wrong: source data can change between separate preview and submit actions, but the submitted payload is still fully revalidated and avoids an unrequested gate.
- Ruling: After Task 4 fix round 5, the remaining load-bearing diagnostic-block defect becomes Task 4b. All terminal balance runtime blocks must be v3 episode-owned; no new recovery exception for legacy status-only blocks is allowed. Cost if wrong: explicit diagnostic code changes, but recovery becomes one coherent generation-safe contract.

## Task ledger

- Task 0: complete — prior conversation recovered, current runtime re-audited, official sources refreshed, isolated worktree created, and minimal-test policy recorded.
- Task 1: minor (deferred): the Go catalog table is not byte-for-byte generated from or cryptographically tied to the external 16,078-byte v4 response; final review must decide whether to add a checked-in canonical fixture/generator.
- Task 1: fix round 1/5 (1 addressed, 0 open; commits d58f66b..e103358).
- Task 1: complete (commits 69478fa..e103358, review clean; 1 deferred minor).
- Task 2: fix round 1/5 (7 addressed, 1 open plus 1 fix-diff gap; commits 02211e1..02fab8f).
- Task 2: fix round 2/5 (2 addressed, 1 new fixture gap; commits 02fab8f..908df68).
- Task 2: fix round 3/5 (1 addressed, 0 open; commits 908df68..130df0b).
- Task 2: complete (commits e103358..130df0b, review clean; PostgreSQL execution is a CI gate).
- Task 3: minor (deferred): preview business-message constants duplicate the account-job allowlist and could drift; final review must decide whether one shared safe-error catalog is warranted.
- Task 3: interrupted implementation recovered after external 429; draft completed in dd6f800.
- Task 3: fix round 1/5 (4 addressed, 0 open; commits dd6f800..6647309).
- Task 3: complete (commits 130df0b..6647309, review clean; live PostgreSQL concurrency is a CI gate; 1 deferred minor).
- Task 4: fix round 1/5 (5 addressed, 2 open plus 3 P1 gaps; commits 9e252c3..d3261a5).
- Task 4: fix round 2/5 (1 addressed, 2 open; commits d3261a5..650df7d).
- Task 4: fix round 3/5 (1 addressed, 2 open; commits 650df7d..22d98b4).
- Task 4: fix round 4/5 (1 addressed, 1 open; commits 22d98b4..dbac529).
- Task 4: fix round 5/5 (race addressed, 1 load-bearing diagnostic regression carried by ruling; commits dbac529..e134741).
- Task 4: complete (commits 6647309..e134741, 1 carried ruling to Task 4b; PostgreSQL/Redis/live are external gates).
- Task 4b: complete (commit 864e3e3d8; focused probe/recovery tests passed; no legacy diagnostic runtime block remains).
- Task 5: fix round 1/5 (initial review found async Gin/mutable captures, refusal/partial-result billing, missing inventory caller, and terminal-owner selection; commits f6c95b5..1620bd1).
- Task 5: fix round 2/5 (all strict OpenAI usage workers now snapshot detached request/account state; failed/partial Chat, Images, and WS turns are non-billable; commits 1620bd1..9799a91).
- Task 5: complete (commits 6647309..9799a91; focused handler/service/repository checks passed; PostgreSQL/Redis concurrency and production acceptance remain CI/deployment gates; global today-stats cache clear is safe but over-broad and deferred).
- Task 6: complete (working-tree implementation verified; commit pending; focused frontend typecheck, lint, and 13-file/44-test matrix passed; import token/fingerprint remains explicitly ruled out by the plan).
- Task 7: pending — integration review, PR, Release, and controlled production acceptance.

## Continuation 2026-08-24

- Namespace hotfix PR #93 was merged to `main` as `df4feba8a73b4dc8456df1784b72c9653ead7b34` and released as `v0.1.179-codexrip.2`.
- The hotfix was merged into this branch before Task 5 so the final release cannot regress namespaced tool-call continuations.

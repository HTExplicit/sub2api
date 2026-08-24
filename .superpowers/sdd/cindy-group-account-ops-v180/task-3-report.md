# Task 3 Report: Cindy Import Preview and Canonical Execution

## Status

Complete in the isolated `codex/cindy-group-ops-v180` worktree. The change is
backend-only and does not modify production, frontend code, migrations, or
generated artifacts.

## Result

- Added `POST /api/v1/admin/accounts/data/preview` as a read-only import preview.
- Added one canonical decision engine used by preview and account-job execution.
- Promotes only exact legacy `openai` API-key records whose normalized base URL
  is the Laxa root to persisted Cindy input identity.
- Requires one explicit materialized strict Cindy target group and discards
  legacy group names for Cindy items.
- Produces deterministic `create`, `update`, and `reject` decisions with stable,
  redacted business codes and messages.
- Rejects submitted credential duplicates, submitted/existing device conflicts,
  invalid device IDs, and invalid device sources without returning raw values.
- Preserves approved import business failures in account-job items while all
  unknown execution failures remain the generic `execution_failed` result.
- Keeps preview informational: there is deliberately no preview hash, token, or
  confirmation gate. Execution re-evaluates the submitted payload against
  current state.

## RED Evidence

1. Existing Cindy job test after introducing the explicit target-group contract:

   `go test ./internal/handler/admin -run '^(TestDataImport|TestAccountJobDataImport)' -count=1 -timeout 50s`

   Failed because the old job fixture omitted `target_group_id` and now correctly
   produced a failed item instead of bypassing canonical import decisions.

2. Target-group lookup error propagation:

   `go test ./internal/handler/admin -run '^TestDataImportDecisionPropagatesTargetGroupLookupFailure$' -count=1 -timeout 50s`

   Failed with `An error is expected but got nil`; the interrupted draft ignored
   every `GetGroup` error and misclassified infrastructure failure as an invalid
   target group.

3. Device-source preview parity and redaction:

   `go test ./internal/handler/admin -run '^TestDataImportPreviewRejectsWithStableRedactedBusinessError$' -count=1 -timeout 50s`

   Failed with expected `reject`, actual `create`; commit-time Cindy normalization
   rejected an invalid source that preview had accepted.

4. Route contract:

   `go test ./internal/server/routes -run '^TestAccountJobRoutesExposeSlimContractWithoutImportPreview$' -count=1 -timeout 50s`

   Failed because the stale test still required the preview route to be absent.

The interrupted draft's new decision tests were already present when this task
was resumed. Base `130df0b72fea9a27a6040b6463eff4049e5cf61a` contains neither the
preview decision engine nor those tests, which preserves the earlier structural
RED evidence without reverting or discarding the draft.

## GREEN Evidence

- Import preview, canonical decision, job execution, legacy import regressions,
  stale-preview re-evaluation, duplicate conflicts, and redaction:

  `go test ./internal/handler/admin -run '^(TestDataImport|TestImportData|TestAccountJob(DataImport|Import))' -count=1 -timeout 50s`

  Result: `ok github.com/Wei-Shaw/sub2api/internal/handler/admin`.

- Preview route registration:

  `go test ./internal/server/routes -run '^TestAccountJobRoutesExposeImportPreviewAndJobContract$' -count=1 -timeout 50s`

  Result: `ok github.com/Wei-Shaw/sub2api/internal/server/routes`.

- Strict Cindy transaction rollback on identity-binding failure:

  `go test ./internal/repository -run '^TestAccountJobCindyMutationRollsBackAccountAndGroupsWhenIdentityBindFails$' -count=1 -timeout 50s`

  Result: `ok github.com/Wei-Shaw/sub2api/internal/repository`.

- Cindy device normalization and account-job service regressions:

  `go test ./internal/service -run '^(TestNormalizeCindyDeviceIdentityExtra|TestMaskCindyDeviceID|TestAccountJob)' -count=1 -timeout 50s`

  Result: `ok github.com/Wei-Shaw/sub2api/internal/service`.

- Touched-package compile:

  `go test ./internal/handler/admin ./internal/server/routes ./internal/service ./internal/repository -run '^$' -count=1 -timeout 50s`

  Result: all four packages compiled successfully.

- Static analysis:

  `go vet ./internal/handler/admin ./internal/server/routes ./internal/service ./internal/repository`

  Result: exit 0 with no output.

All commands completed in less than 60 seconds.

## Files Changed

- `backend/internal/handler/admin/account_data.go`: request/preview DTOs and
  read-only preview handler.
- `backend/internal/handler/admin/account_data_import_decision.go`: canonical
  decision engine, strict group validation, conflict resolution, and redacted
  preview projection.
- `backend/internal/handler/admin/account_job_execution.go`: import jobs consume
  the canonical decision and bind strict Cindy items only to the explicit group.
- `backend/internal/handler/admin/account_job_handler.go` and
  `backend/internal/service/account_job.go`: allowlisted safe job failure
  normalization.
- `backend/internal/service/cindy_identity.go`: shared read-only device-source
  validator used by preview and commit normalization.
- `backend/internal/server/routes/admin.go`: preview route.
- Focused handler and route tests, plus updated import test stubs.

## Self-Review

- Exact Laxa root normalization accepts harmless case/trailing-slash variants
  and rejects paths, ports, queries, fragments, and userinfo; near matches remain
  OpenAI.
- Strict Cindy identity is still exactly `cindy/openai/cindy_laxa_v1`.
- Cindy source `groups` names are cleared before execution; only the explicit
  target strict-group ID reaches create/update input.
- Preview calls only account/group reads and performs no proxy, taxonomy,
  account, credential-identity, or health writes.
- Submitted credential/device conflicts reject every affected item in a stable
  way. Existing device ownership permits only the matching update target.
- Preview contains no credential fingerprint, raw credential, device value, or
  raw validation error. Job failures expose only allowlisted code/message pairs.
- Execution re-runs the canonical engine, so a stale create preview can become
  an update without any preview-confirmation token.
- Strict Cindy create/update remains inside the existing transaction runner;
  its rollback test passes.
- Existing non-Cindy synchronous import helpers and account-job kinds retain
  their prior behavior.

## Concerns and Deferred Checks

- No full package-wide suites were run, per the explicit minimal-test policy;
  PR CI owns the complete matrix.
- The repository rollback test uses the existing SQL transaction mock. No local
  PostgreSQL integration run was added for this handler-only task.
- Production runtime audit was not repeated for an undeployed local change;
  Task 0 already recorded the current production snapshot.
- Frontend integration is intentionally deferred to Task 6.

## Review Fix Round 1

The interrupted draft was retained and reviewed hunk by hunk. Four review defects
were closed without schema, migration, frontend, production, or release scope:

1. `AdminService.GetGroup` now hydrates `StrictCindyKnown` and `StrictCindy` from
   the production account repository's canonical aggregate classifier. That
   classifier evaluates the complete non-deleted membership, including disabled
   and unschedulable accounts; the handler no longer treats test-populated marker
   fields as authoritative.
2. Strict Cindy mutation now claims the stored device identity inside the same
   PostgreSQL transaction as the account/group/credential mutation. A
   transaction-scoped `pg_advisory_xact_lock` keyed by device identity serializes
   different credential keys, followed by a locked owner query. A conflict rolls
   back before credential binding or scheduler outbox commit.
3. Import decisions validate and match the transformed canonical account. Legacy
   group names are cleared before payload validation and never become import
   authority.
4. Cindy candidates reject missing, non-string, and blank `api_key` values with
   `cindy_import_api_key_invalid` / `Cindy API key is required`. Legacy promotion
   occurs only after that check succeeds.

### RED Evidence

The resumed worktree already contained the previous worker's untrusted fixes, so
the draft was not destructively reverted just to manufacture a failure. The base
comparison and review assertions identified these four failing behaviors:

- The base `GetGroup` path returned repository DTO marker fields without invoking
  the complete-membership classifier; the new hydration regression would retain a
  stale marker instead of the canonical result.
- The base Cindy mutation transaction had no device claim. The two-worker
  serialization regression would allow the second different-key transaction to
  bind and commit the same device.
- The base preview validated the original import item after clearing groups on
  the decision account; a blank legacy group entry therefore rejected an otherwise
  valid explicit-target Cindy item.
- The base preview promoted legacy identity before checking `api_key` and had no
  stable Cindy key error, so missing/non-string/blank keys lacked the required
  redacted business result.

### GREEN Evidence

Fresh focused verification after the fix round:

- `go test ./internal/handler/admin -run '^(TestDataImportDecision|TestDataImportPreview|TestAccountJobImport)' -count=1 -timeout 40s`
  -> `ok github.com/Wei-Shaw/sub2api/internal/handler/admin`.
- `go test ./internal/repository -run '^TestAccountJobCindyMutation' -count=1 -timeout 40s`
  -> `ok github.com/Wei-Shaw/sub2api/internal/repository`; the serialization case
  uses two different credential keys, one commit, one owner conflict, and one
  rollback under the same device lock domain.
- `go test -tags=unit ./internal/service -run '^TestAdminServiceGetGroupHydratesStrictCindyIdentityFromCompleteMembership$' -count=1 -timeout 40s`
  -> `ok github.com/Wei-Shaw/sub2api/internal/service`.
- `go test -tags=unit ./internal/handler/admin ./internal/repository ./internal/service -run '^$' -count=1 -timeout 45s`
  -> all three touched packages compiled successfully.
- `git diff --check` -> passed.

### Remaining Risk

The transaction proof is a focused SQL-mock serialization test, not a live
PostgreSQL concurrency run. The production mechanism is database-scoped and does
not use a process mutex; live database execution remains a CI/integration gate.
No schema or interface expansion was required.

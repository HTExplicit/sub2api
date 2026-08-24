# Task 5 Report

## Status

Implemented the final-success usage/statistics boundary and focused tests.

## Changes

- Added an `account_id` owner column to the usage-billing dedup ledger through
  migration `238_cindy_account_stats_reset_at.sql`. The atomic dedup claim now
  returns the first successful account owner; duplicate/out-of-order workers are
  rejected before billing and usage-log insertion.
- Failed OpenAI responses, refusal/cyber/failover attempts, and partial-error
  results no longer submit usage records from the gateway handler. Cyber events
  remain operational evidence only.
- Duplicate billing claims no longer write a second usage row. Successful commits
  invalidate the account usage cache and the admin today-stats snapshot cache.
- Added Cindy `cindy_account_stats_reset_at` watermark support. Single and batch
  account-window SQL applies `GREATEST(today_start, reset_at)` and leaves global
  historical usage and balances untouched.
- Added deterministic redacted duplicate-identity inventory grouped by the
  normalized Laxa credential fingerprint. The oldest non-terminal account is the
  proposed owner; other IDs are returned for manual review only.

## Verification

Focused checks passed:

```text
go test -tags=unit ./migrations -run TestMigration238AddsCindyAccountStatsResetWatermark -count=1
go test -tags=unit ./internal/repository -run 'TestUsageBillingApplyStoresFirstSuccessfulAccountOwner|TestCindyAccountStatsResetWatermarkAppearsInSingleAndBatchSQL|^$' -count=1
go test -tags=unit ./internal/service -run 'TestBuildCindyDuplicateIdentityInventory|TestGatewayServiceRecordUsage_BillingUsesDetachedContext|^$' -count=1
go test -tags=unit ./internal/handler/admin -run '^$' -count=1
go test -tags=unit ./cmd/server -run '^$' -count=1
git diff --check
```

## Concerns

- PostgreSQL migration execution, concurrent multi-worker behavior, and Redis/
  production cache invalidation remain integration/deployment gates.
- The existing generated Ent account type does not expose the new watermark;
  the stats path intentionally reads it in SQL so schema generation is not
  required for this narrow change.
- No full package suite or production traffic was run.

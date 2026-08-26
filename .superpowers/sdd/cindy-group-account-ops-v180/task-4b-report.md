# Task 4b Report

> Document type: task report
> Status: complete
> Last verified: 2026-08-24

## Changes

- Removed the explicit diagnostic exhaustion call to the legacy
  `blockCindyBalanceRuntime` path.
- After the lease-guarded database commit, diagnostic exhaustion now sends the
  exact-budget signal through the existing `CindyHealthCoordinator`. The
  coordinator validates the current credential generation and fingerprint,
  creates the v3 terminal episode, and owns the runtime block.
- Added a focused regression assertion that the diagnostic path creates an
  episode-owned block and never leaves the legacy fingerprint-only marker.

## Verification

- `go test -tags unit ./internal/service -run '^TestCindyBalanceProbe' -count=1`: passed.
- Focused recovery tests for exact recovery, concurrent replacement, and nil
  capture: passed.
- `go test -tags unit ./internal/service -run '^$' -count=1`: passed (compile-only).
- Git diff review: limited to the assigned service, test, and report paths.

## Remaining Risk

The coordinator is wired in production through `ProvideCindyHealthService`.
If a degraded test or deployment constructs the probe without a coordinator,
the database terminal state still commits but no local runtime block is added;
there is intentionally no legacy fallback.

An unrelated existing `TestAdminCindyBalanceRecoveryIgnoresLegacyPendingCleanupFailure`
failure remains in `cindy_balance_pending_test.go:236`; it reproduces without
the 4b changes and is outside this task's owned paths.

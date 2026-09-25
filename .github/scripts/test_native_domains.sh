#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"

# The same domain policies now compile into the host. Lifecycle concurrency is
# selected once by the workflow's dedicated race step; no package/signing process.
go -C backend test -p=1 -tags unit \
  ./internal/accounttools/... \
  ./internal/codexruntime/... \
  -skip '^TestNativeModuleLifecycleStartsOnceAndDrains$' -count=1

# Preserve configuration/ledger/storage boundaries without starting real IO.
go -C backend test -p=1 -tags unit ./internal/service ./internal/repository \
  -run '^(TestNativeCodex(ConfigGenerationIsAtomicAndIdempotent|StoredConfigDistinguishesMissingAndReadError|ClosedLedgerReadableWithoutRewritingGeneration|HostKeepsPrivateStateAndCredentialScope|ErrorDiagnosticsUseStoredAccountsAndRemovePrivateFields)|TestNativeRuntimeLease.*|TestNativePluginBoundaryProtectsRetiredRecordsAndAllowsThirdParties|TestNativeFeatureBootstrapPreservesEffectiveConfiguration|TestNativeSettingLoadFailureNeverReenablesSavedSwitches|TestImageTools.*|TestSwitchingImageStudio(OnStartsTheRuntime|OffStopsDetachedUpstreamIO)|TestTrafficObservation.*|TestAccountTrafficOutcomeRulesKeepCancellationAboveErrorsAndRequireWSTerminals|TestDecodeSwitchSettingsRejectsUnknownAndNonBooleanValues)$' \
  -count=1

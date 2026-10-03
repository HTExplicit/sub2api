#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"

# The same domain policies now compile into the host; no package/signing process.
go -C backend test -p=1 -tags unit \
  ./internal/accounttools/... \
  ./internal/codexruntime/... \
  -count=1

# Preserve configuration/storage boundaries without starting real IO.
go -C backend test -p=1 -tags unit ./internal/service ./internal/repository \
  -run '^(TestCodexRuntimeSettingIsOnePlainRowOverTheDeployDefault|TestPrepareOpenAICodexWireRequest.*|TestNativeCodexErrorDiagnosticsProjectEveryStoredAttempt|TestNativePluginBoundaryProtectsRetiredRecordsAndAllowsThirdParties|TestNativeFeatureBootstrapPreservesEffectiveConfiguration|TestNativeSettingLoadFailureNeverReenablesSavedSwitches|TestImageTools.*|TestSwitchingImageStudio(OnStartsTheRuntime|OffStopsDetachedUpstreamIO)|TestTrafficObservation.*|TestAccountTrafficOutcomeRulesKeepCancellationAboveErrorsAndRequireWSTerminals|TestDecodeSwitchSettingsRejectsUnknownAndNonBooleanValues)$' \
  -count=1

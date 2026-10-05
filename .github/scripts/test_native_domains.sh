#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"

# The Codex runtime domain packages.
go -C backend test -p=1 -tags unit \
  ./internal/codexruntime/... \
  -count=1

# Preserve the native configuration contracts without starting real IO.
go -C backend test -p=1 -tags unit ./internal/service \
  -run '^(TestCodexRuntimeSettingIsOnePlainRowOverTheDeployDefault|TestPrepareOpenAICodexWireRequest.*|TestNativeCodexErrorDiagnosticsProjectEveryStoredAttempt|TestNativeSettingLoadFailureNeverReenablesSavedSwitches|TestTrafficObservation.*|TestAccountTrafficOutcomeRulesKeepCancellationAboveErrorsAndRequireWSTerminals|TestDecodeSwitchSettingsRejectsUnknownAndNonBooleanValues)$' \
  -count=1

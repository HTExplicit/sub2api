#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

# Offline contracts only. The broker-controlled live entry point is excluded
# explicitly; this gate cannot consume account credentials or model requests.
go -C backend test -p=1 -tags unit ./internal/pkg/apicompat ./internal/repository ./internal/service ./internal/handler/admin \
  -run 'Test(OpenAIChatReasoning|OpenAIReasoning|OpenAIHTTPTerminal|GatewayCacheReasoningState|SchedulerCacheReasoningPolicy|ResponsesReasoningConfiguration)' -count=1
go -C backend test -p=1 -tags reasoning_fidelity,reasoning_replay_diagnostic ./internal/service \
  -run '^TestReasoningReplayDiagnostic(Control|ChatParse)$' -count=1

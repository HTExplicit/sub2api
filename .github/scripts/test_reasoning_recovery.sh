#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

# Offline contracts only; no account credentials or model calls.
go -C backend test -p=1 -tags unit ./internal/pkg/apicompat ./internal/repository ./internal/service ./internal/handler/admin \
  -run 'Test(OpenAIReasoning|OpenAIHTTPTerminal|GatewayCacheReasoningState|ResponsesReasoningConfiguration|ReasoningRecovery)' -count=1

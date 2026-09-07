#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root/backend"

# Offline policy, persistence, and wire-contract fixtures only; no model calls.
go test -p=1 -tags unit \
  ./internal/service ./internal/repository ./internal/handler ./internal/handler/admin ./cmd/server \
  -run 'Test.*(ModelContext|ContextCapacit|GroupModelCapacity|ConvertOpenAIModelListLiveCapacity|CodexCapacityProjection|SyncUpstreamModelCatalog.*Capacity|RunCodexContextContractVerification)' \
  -count=1

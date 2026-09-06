#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
# Build-only fixture assets let the production embed path participate in CI.
mkdir -p backend/internal/web/dist
if [[ ! -f backend/internal/web/dist/index.html ]]; then
  cp frontend/index.html backend/internal/web/dist/index.html
fi
if [[ ! -f backend/internal/web/dist/logo.svg ]]; then
  cp frontend/public/logo.svg backend/internal/web/dist/logo.svg
fi
if [[ ! -d backend/internal/web/dist/assets ]]; then
  mkdir -p backend/internal/web/dist/assets
  asset_hash=$(sha256sum backend/internal/web/testdata/agent_cache_fixture.js | cut -c1-8)
  cp backend/internal/web/testdata/agent_cache_fixture.js "backend/internal/web/dist/assets/agent-${asset_hash}.js"
fi
go -C backend test -p=1 -tags unit,embed ./internal/web -run 'Test(Agent|FrontendServer_Middleware|ServeEmbeddedFrontend|OverrideFilesNeverReceiveImmutableCacheHeaders)' -count=1
go -C backend test -p=1 ./internal/pkg/apicompat -run 'Test(Agent|Stream_|.*Chat.*Responses|.*Responses.*Chat)' -count=1
go -C backend test -p=1 -tags unit ./internal/service ./internal/handler -run 'Test(Agent|ForwardResponses_.*(Chat|Reasoning)|ForwardAsAnthropic_ForceChatCompletions|ForwardAsRawChatCompletions|OpenAIRawStreamTerminalState|.*NoAccount|.*SelectionFailure)' -count=1

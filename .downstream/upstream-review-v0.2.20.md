# Official v0.2.20 integration

- Official tag: `v0.2.20`, commit `dab829a4e9b7d1cc307189aea8b0319eb29e29d0`; downstream start: `8dea2b7681ea56e85fe818400e85a8a6e89904a7`.
- The approved integration follows the complete 459-file Git difference, including 162 downstream-overlap files. The machine risk report owns its exact inputs. [DOWNSTREAM](../DOWNSTREAM.md) owns current behavior; operator delivery evidence owns validation, release and actual production results.

## Initial conflicts and resolution boundaries

| Conflict files | Resolution |
| --- | --- |
| `.github/workflows/backend-ci.yml`, `.github/workflows/security-scan.yml` | Keep selective CI and required check names; apply official Node version in the shared action and receipt environment. Classify actual new inputs with exact test or build boundaries. |
| `backend/cmd/server/wire.go`, `backend/cmd/server/wire_gen.go` | Combine official digest/balance service cleanup with Pelican, jobs and identity services; regenerate Wire from the resolved providers. |
| `backend/go.mod` | Adopt official SDK/compression dependencies; keep independent pgx, Pelican and direct text dependencies. |
| `backend/internal/handler/openai_alpha_search.go`, `openai_chat_completions.go`, `openai_embeddings.go`, `openai_gateway_handler.go`, `openai_images.go` | Adopt official read-error classification, image-size routing, 413 overrides and transcript length handling; retain request-platform selection, successful-owner billing and asynchronous usage snapshots. |
| `backend/internal/handler/request_body_limit.go`, `request_body_limit_test.go`, `backend/internal/pkg/httputil/body.go` | Use one official budgeted reader/error model. Keep lenient JSON and previously tested lone-EOF complete-document compatibility; incomplete JSON, cancellation and timeout remain failures. Official content-free diagnostics replace duplicate legacy helpers. |
| `backend/internal/pkg/apicompat/responses_reasoning.go`, `responses_to_anthropic.go` | Add official reasoning fallback and accumulation fixes; retain raw reasoning configuration, exact ciphertext and independent completed-argument behavior. |
| `backend/internal/repository/gateway_cache.go`, `backend/internal/server/middleware/audit_log_test.go`, `backend/internal/server/routes/gateway.go` | Use official transcript-length cache and shared budget. Retain lenient JSON route handling and administrator raw audit assertions alongside official MCP audit cases. |
| `backend/internal/service/antigravity_gateway_service.go`, `antigravity_internal500_penalty.go` | Official finite drain, identity neutralization and model-scoped image penalty; retain observation semantics and existing test prompt/output contracts. |
| `backend/internal/service/api_key_auth_cache_impl.go`, `billing_service.go`, `gateway_usage_billing.go` | Official privacy snapshot, Terra and Image 2.6 changes; retain independent projections, model pricing and successful-owner accounting. |
| `backend/internal/service/openai_account_scheduler.go`, `openai_cyber_session_block.go`, `openai_gateway_passthrough.go`, `openai_gateway_request_body.go`, `openai_gateway_response_handling.go`, `openai_gateway_scheduling.go`, `openai_images.go`, `openai_ws_http_bridge.go` | Official routing, transcript and body changes; keep credential-source shadow, frozen identity policy, API-key namespaces, independent sessions, strict transport and authoritative terminal semantics. WS HTTP bridging joins the existing identity chain. |
| `frontend/src/components/icons/Icon.vue`, `frontend/src/components/layout/AppSidebar.vue` | Keep existing theme, Pelican and extension navigation; add official MCP icon and default-off visibility. |

These rows cover all 33 initial text conflicts. Clean merges are reviewed at the same affected boundaries; no second recovery, retry, identity or request-budget layer is introduced.

## Official additions and retained data

- Adopt official MCP/OAuth, administrator permission extensions, email blacklist, image-size routing, usage display, protocol fixes and dependencies. MCP and full-access compatibility mode remain opt-in; deployment changes no account mapping or production switch.
- Preserve the official `249_mcp_oauth.sql` beside historical `249_plugin_bundle_binding_intent.sql`, under their original complete filenames and checksums. The new migration adds MCP tables and indexes; historical migrations and diagnostic data remain unchanged.
- Antigravity tests retain administrator prompt validation, `Reply with OK.` default, existing custom/default output budgets, visible-text requirement and `OutputLimited` reporting. Do not replace these with a fixed 256-token budget.
- Request budgets/deadlines and post-disconnect billing limits are described once in [DOWNSTREAM](../DOWNSTREAM.md#management-mcp-and-request-boundaries). No production resource or body-limit adjustment accompanies the source update.
- Retired capacity overrides, dedicated ciphertext recovery and buffered consistency guards remain retired. Required checks stay intact and successful same-tree results are reused; no real model or payment call is required.

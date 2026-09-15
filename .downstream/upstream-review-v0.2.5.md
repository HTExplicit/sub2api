# Upstream v0.2.5 integration review

## Inputs

- ours: `origin/main` / `4c96fb375a68744ddf356ffb3f64afac5e21894b` (`v0.2.4-codexrip.11`)
- official release: peeled `v0.2.5` / `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`
- version sync: `881f3202694c6bc932446931a30c27d9675178b9`
- common ancestor: official `v0.2.4` / `5de5e2bed035d43591a2e10e51f420ef6a84eb98`

## Conflict inventory

The three-way merge reported 54 content conflicts: 37 backend and 17 frontend. Every path was resolved and staged:

### Backend (37)

- `backend/cmd/server/wire_gen.go`
- `backend/ent/schema/user_platform_quota.go`
- `backend/internal/handler/admin/account_data.go`
- `backend/internal/handler/admin/group_handler.go`
- `backend/internal/handler/admin/group_handler_platform_test.go`
- `backend/internal/handler/admin/setting_handler_partial_payload_test.go`
- `backend/internal/handler/admin/user_platform_quota_admin_test.go`
- `backend/internal/handler/gateway_handler.go`
- `backend/internal/handler/grok_media.go`
- `backend/internal/handler/openai_gateway_handler.go`
- `backend/internal/handler/setting_handler.go`
- `backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go`
- `backend/internal/pkg/httputil/body.go`
- `backend/internal/repository/account_repo_integration_test.go`
- `backend/internal/repository/scheduler_cache.go`
- `backend/internal/repository/user_platform_quota_repo.go`
- `backend/internal/server/api_contract_test.go`
- `backend/internal/server/routes/gateway.go`
- `backend/internal/service/account.go`
- `backend/internal/service/account_test_service.go`
- `backend/internal/service/composite_platform_test.go`
- `backend/internal/service/openai_codex_transform.go`
- `backend/internal/service/openai_gateway_chat_completions.go`
- `backend/internal/service/openai_gateway_forward.go`
- `backend/internal/service/openai_gateway_request_body.go`
- `backend/internal/service/openai_gateway_response_handling.go`
- `backend/internal/service/openai_gateway_scheduling.go`
- `backend/internal/service/openai_gateway_usage.go`
- `backend/internal/service/openai_gpt56_max_test.go`
- `backend/internal/service/openai_images_responses.go`
- `backend/internal/service/openai_responses_namespace.go`
- `backend/internal/service/openai_responses_namespace_test.go`
- `backend/internal/service/openai_ws_forwarder_ingress.go`
- `backend/internal/service/openai_ws_session_preemption_test.go`
- `backend/internal/service/scheduler_snapshot_service.go`
- `backend/internal/service/setting_public.go`
- `backend/internal/setup/setup.go`

### Frontend (17)

- `frontend/src/components/account/AccountUsageCell.vue`
- `frontend/src/components/account/CreateAccountModal.vue`
- `frontend/src/components/account/EditAccountModal.vue`
- `frontend/src/components/account/__tests__/ModelWhitelistSelector.spec.ts`
- `frontend/src/components/layout/AppSidebar.vue`
- `frontend/src/components/layout/__tests__/AppSidebar.spec.ts`
- `frontend/src/constants/__tests__/platforms.spec.ts`
- `frontend/src/constants/platforms.ts`
- `frontend/src/router/__tests__/feature-access.spec.ts`
- `frontend/src/router/index.ts`
- `frontend/src/stores/app.ts`
- `frontend/src/types/index.ts`
- `frontend/src/utils/featureFlags.ts`
- `frontend/src/views/admin/ChannelsView.vue`
- `frontend/src/views/admin/__tests__/AccountsView.lite.spec.ts`
- `frontend/src/views/admin/__tests__/SettingsView.spec.ts`
- `frontend/src/views/admin/__tests__/channelPlatformOptions.spec.ts`


## Resolution policy

Official behavior is authoritative for OpenCode Zen/GO, site-type billing states, Responses Lite and agent/system messages, WebSocket execution scope/pooling/preemption/retry, native Codex Images, visible model metadata/provider filtering, batch administration, and Ollama asynchronous quota reset.

Downstream contracts retained are Cindy identity/catalog/health and production group mapping; continuation, opaque lineage, refusal recovery and PNNL reasoning summaries; persisted account test models; password-gated API-key reveal; fixed-digest release/deploy protections; Image Studio and Responses image bridge default-off; and the retired public model management page.

## Quota and migrations

Official quota semantics are selected. `238_opencode_go_platform.sql` and `238_purge_unlimited_user_platform_quotas.sql` remain independent alongside `238_cindy_account_stats_reset_at.sql`; the platform check includes both `cindy` and `opencode_go`, while Cindy is not reintroduced as a composite/monitor provider. The runner keys migrations by complete filename and lexicographically orders them, so the three `238_*` files do not collide.

Production NULL-quota row count and post-deploy purge deletion count are **not yet measured** in this checkout; they must be captured by the deployment procedure before/after migration. No historical migration was renamed.

## Verification

- **Passed:** `go test ./migrations ./internal/pkg/apicompat -count=1`; backend package compile (`go test ./internal/service ./internal/handler ./internal/repository ./internal/setup -run '^$'`); targeted account model tests; frontend type-check and targeted Vitest suites (see handoff).
- **Passed:** `git diff --check`; no unresolved Git index conflicts.
- **Not completed:** full service suite still has downstream behavioral failures in Cindy WS turn-state and direct-image account-test fixtures; these require runtime fixture follow-up.
- **Not run:** production migration, image build/push, SSH deploy, post-deploy checks.
- **Unverified:** target GHCR digest, production NULL-row counts, deployed health and model functionality.

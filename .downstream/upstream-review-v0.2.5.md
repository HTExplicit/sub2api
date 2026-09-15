# Upstream v0.2.5 integration review

## Inputs

- ours: `origin/main` / `4c96fb375a68744ddf356ffb3f64afac5e21894b` (`v0.2.4-codexrip.11`)
- official release: peeled `v0.2.5` / `86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea`
- version sync: `881f3202694c6bc932446931a30c27d9675178b9`
- common ancestor: official `v0.2.4` / `5de5e2bed035d43591a2e10e51f420ef6a84eb98`

## Conflict inventory

The three-way merge reported 54 content conflicts: 37 backend and 17 frontend. Every path was resolved in the integration commit:

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

The fixed read-only production audit on 2026-09-15 returned **82 total rows, all 82
with three NULL limits**. The already applied MiniMax and Cindy checksums match
both the integration tree and the original production baseline. The two official
238 migrations are not yet applied. No historical migration was renamed or edited.

| Migration, in execution order | SHA256 of trimmed SQL | Pre-deploy state |
|---|---|---|
| `237_add_minimax_platform.sql` | `259520a95b7ce0c6989fabb928e80fc4eae787fdae0cb82d04dee7f71f6d975a` | Applied, checksum matches |
| `238_cindy_account_stats_reset_at.sql` | `15c06d93de465e6e115d235cf592e0de7da6f0dcfb493236b62427f086c6489c` | Applied, checksum matches |
| `238_opencode_go_platform.sql` | `69e8b061418372f636e04b289d794ffd74d5fd2cd63b37e3226897ea32ff787b` | Pending |
| `238_purge_unlimited_user_platform_quotas.sql` | `052756d1b1ac951f002034bf0434f8a6ea943ec0a2a34541ca4374ba6278cce7` | Pending |

The runner uses `sort.Strings`, a filename primary key and SHA256 after
`strings.TrimSpace`; it skips applied files only after verifying checksums.
The official purge is idempotent because it deletes only three-NULL rows. Its
production effect and post-deploy checksum verification remain pending.

## Decisions by conflict group

The path inventory above is exhaustive. Decisions apply to all listed paths in
each group, including their accompanying tests and generated code.

| Group | Final decision and preserved invariant |
|---|---|
| Schema, quota repository/admin, setup and API contracts | Official quota filtering, aggregation and reset; keep Cindy and OpenCode Go in the supported platform list; regenerate Ent/Wire from source. |
| Account DTO, group/platform validation, scheduler cache/snapshots | Add official OpenCode taxonomy and routing while preserving Cindy identity, health, budget exclusions and production membership. |
| Account test service and image test path | Official native OAuth Images and OpenCode protocol-specific endpoints; persisted test models and existing direct-image defaults remain. |
| Gateway/model handlers, gateway routes and Grok media | Official catalog metadata and provider projection; enforce group membership for pinned accounts, preserve model-retrieve metadata, maintain Grok media ownership and release admission slots on every failure. |
| OpenAI request/forward/response/usage and compatibility helpers | Official Lite declarations/history, system/agent restoration, usage and images; retain Cindy continuation, lineage and reasoning/refusal recovery. |
| WebSocket ingress, scheduling and preemption | Official execution scope, pool and preemption behavior; Cindy session compatibility is retained without replaying a failed stale lease. |
| Settings handlers, public settings and payload tests | Official site billing tri-state plus downstream switches; API-key reveal remains password-gated. |
| Frontend platform/types, account usage/create/edit and account tests | OpenCode Zen/GO and Cindy display; official quota semantics and persisted account model selection. |
| Router, sidebar, app store and feature flags | Site billing states respected; public model management stays retired; Image Studio and Responses image bridge remain off by default. |
| Channels, settings and platform/whitelist tests | Official provider/catalog options and site settings with Cindy catalog/account boundaries. |

## Verification

- **Passed locally:** migration/apicompat tests; backend compile; targeted quota,
  OpenCode, namespace, native Images, Cindy WS/continuation/reasoning, catalog and
  model-retrieve, bootstrap, request-body and Grok admission-release regressions.
- **Passed locally:** Ent/Wire generation, frontend type-check and targeted
  platform/account/sidebar/settings/feature-access suites; `git diff --check` and
  no unmerged index entries or conflict markers.
- **Passed for code commit `ebc1cd5db`:** CI lint, frontend, shell and security;
  Downstream frontend. The three latest unit failures were corrected and their
  affected packages passed targeted verification before this push.
- **Not completed:** final required backend CI, release build/push, deployment,
  post-deploy quota/health/digest/runtime checks. See [integration PR #167](https://github.com/HTExplicit/sub2api/pull/167)
  for the authoritative required-check status.
- **Not run:** additional local full/race suites, canaries, long observation and
  real model calls. Existing required CI suites run under branch protection.
- **Unverified:** target image digest and model functionality. Historical live
  continuation/cache problems are not declared fixed by passing unit or health checks.

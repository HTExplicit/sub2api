# Upstream v0.2.0 Overlap Review

> Review status: complete
> Reviewed: 2026-09-02
> Upstream base: `v0.1.185`
> Upstream tag: `v0.2.0` (`aa236488351eb71e120fc2b6fb32e36b0374c918`)
> Downstream input: `bf2c1891c6e0881e488095e47997012d32120ba5`
> Planned downstream release: `v0.2.0-codexrip.2`

The trusted risk classifier reports 148 upstream files, 75 downstream-overlap
files, and 59 critical upstream paths. The merge produced 16 textual conflicts.
All conflicts were resolved in the candidate and the committed risk manifest
therefore records `merge_conflicts: []`.

The candidate also contains one downstream-owned status fix outside the 75-file
overlap: the tokenless public GitHub status reader recognizes a matching open
`upstream-review-required` Issue when conflict sync has no candidate PR or
release, reports `review_required`, and exposes the Issue link in VersionBadge.

The reviewed Codex quota root fix from PR #113 is integrated without changing
the upstream overlap count. Transient `rate_limit_error` events with under-100
5h/7d observation headers retain bounded retry/short cooldown semantics; only
explicit hard quota evidence can terminate the overdraft cycle. The final
candidate also carries the context-specific scheduler bucket, monotonic usage
snapshot CAS, first-injection statistics boundary, and HTTP/SSE/WebSocket
classifier tests. Release-specific test selection includes the new classifier
and scheduler-context families.

## Resolution Invariants

- Group persistence is generated from the combined schema. Generated Ent files
  were not hand-maintained as a conflict resolution.
- Canonical Cindy identity, managed model/search behavior, continuation binding,
  final-wire cache-key normalization, and Business System Prompt ordering remain
  downstream-owned and enabled only by their existing runtime gates.
- Group Fast applies only to OpenAI/Composite groups, runs before the existing
  global Fast policy, and changes only `ActualCost` when Standard billing is
  selected. It does not bypass channel, long-context, Cindy, or response-model
  pricing.
- API-key auth snapshot v23 is a combined downstream schema. It invalidates both
  downstream v21 and upstream v22 entries instead of accepting either partial
  snapshot shape.
- Kimi native Responses is adopted without classifying Cindy as a CN provider.
  Zhipu remains on the Chat Completions bridge and DeepSeek-only client-tool
  adaptation remains DeepSeek-only.
- Downstream quota-overdraft scheduling, final-success-only usage ownership,
  account task/import behavior, dormant profit controls, and release automation
  remain intact.

## Textual Conflicts

| Path | Resolution |
|---|---|
| `backend/ent/group.go` | Regenerated from the combined Group schema. |
| `backend/ent/mutation.go` | Regenerated from the combined Group schema. |
| `backend/ent/runtime/runtime.go` | Regenerated from the combined Group schema. |
| `backend/internal/handler/admin/channel_handler_test.go` | Kept Cindy managed-catalog and Fable 5.1 cache-TTL tests. |
| `backend/internal/handler/dto/mappers.go` | Added Fast/over-limit projection; retained provider identity and dormant profit projection. |
| `backend/internal/handler/openai_chat_completions.go` | Added policy-deny errors around the existing Cindy routing and overdraft failover path. |
| `backend/internal/handler/openai_gateway_handler.go` | Added Automation bootstrap and policy-deny handling before the existing continuation validation chain. |
| `backend/internal/service/admin_group.go` | Sanitized Fast by logical platform while retaining effective-wire Live handling. |
| `backend/internal/service/api_key_auth_cache_impl.go` | Combined all fields under snapshot version 23. |
| `backend/internal/service/api_key_auth_cache_profit_test.go` | Retained dormant-profit assertions and updated the combined cache version. |
| `backend/internal/service/openai_fast_policy_test.go` | Kept the functional bulk settings stub and all upstream group-Fast cases. |
| `backend/internal/service/openai_gateway_chat_completions.go` | Kept prompt ownership/final Cindy normalization and synchronized the final tier/cache identity. |
| `backend/internal/service/openai_gateway_forward.go` | Adopted native-CN routing without weakening downstream Cindy or tool handling. |
| `backend/internal/service/openai_gateway_messages.go` | Kept prompt/final Cindy normalization and synchronized the final tier. |
| `backend/internal/service/openai_gateway_usage.go` | Reused the downstream account-aware cost pipeline for Standard-priced Fast. |
| `frontend/src/types/index.ts` | Added Fast/Reasoning types while retaining Cindy, account jobs/import, and overdraft types. |

## Reviewed Overlap Files

### Persistence And Schema

- [x] `backend/ent/group_create.go`
- [x] `backend/ent/group_update.go`
- [x] `backend/ent/group.go`
- [x] `backend/ent/group/group.go`
- [x] `backend/ent/group/where.go`
- [x] `backend/ent/migrate/schema.go`
- [x] `backend/ent/mutation.go`
- [x] `backend/ent/runtime/runtime.go`
- [x] `backend/ent/schema/group.go`
- [x] `backend/internal/domain/constants.go`

Review result: the generated field order contains downstream provider identity
plus official Force Fast, Free Fast, and over-limit policy fields. Full-filename
migration tracking allows the official `232_*`/`233_*` files to coexist with
the already released downstream files of the same numeric prefix.

### Handler And DTO Surface

- [x] `backend/internal/handler/admin/channel_handler_test.go`
- [x] `backend/internal/handler/admin/group_handler.go`
- [x] `backend/internal/handler/dto/mappers.go`
- [x] `backend/internal/handler/dto/types.go`
- [x] `backend/internal/handler/openai_chat_completions.go`
- [x] `backend/internal/handler/openai_gateway_handler.go`
- [x] `backend/internal/handler/ops_error_logger_test.go`
- [x] `backend/internal/handler/ops_error_logger.go`
- [x] `backend/internal/server/api_contract_test.go`

Review result: OpenAI and Anthropic policy-deny envelopes remain local business
limits, valid Codex Automation/Delegation bootstrap normalization precedes
call-output validation, and downstream Cindy, continuation, Cyber, retry, and
usage ownership code remains present.

### Repository And Scheduler Projection

- [x] `backend/internal/repository/api_key_repo.go`
- [x] `backend/internal/repository/channel_repo_account_stats_pricing.go`
- [x] `backend/internal/repository/channel_repo_pricing_time_test.go`
- [x] `backend/internal/repository/channel_repo_pricing.go`
- [x] `backend/internal/repository/fixtures_integration_test.go`
- [x] `backend/internal/repository/group_repo.go`
- [x] `backend/internal/repository/scheduler_cache_unit_test.go`
- [x] `backend/internal/repository/scheduler_cache.go`

Review result: every new Group field is selected, mapped, created, and updated;
strict Cindy materialization remains repository-backed; scheduler snapshots keep
provider identity and both OpenAI passthrough compatibility keys; split 1h cache
pricing is round-tripped by flat, interval, and account-stat repositories.

### Service, Gateway, Billing, And Transport

- [x] `backend/internal/service/account_test_service_cn_adaptive.go`
- [x] `backend/internal/service/account.go`
- [x] `backend/internal/service/admin_group_duplicate.go`
- [x] `backend/internal/service/admin_group.go`
- [x] `backend/internal/service/admin_service_composite_group_test.go`
- [x] `backend/internal/service/admin_service_group_test.go`
- [x] `backend/internal/service/admin_service.go`
- [x] `backend/internal/service/api_key_auth_cache_impl.go`
- [x] `backend/internal/service/api_key_auth_cache_profit_test.go`
- [x] `backend/internal/service/api_key_auth_cache.go`
- [x] `backend/internal/service/channel_service.go`
- [x] `backend/internal/service/group.go`
- [x] `backend/internal/service/model_pricing_resolver.go`
- [x] `backend/internal/service/openai_apikey_responses_probe_test.go`
- [x] `backend/internal/service/openai_apikey_responses_probe.go`
- [x] `backend/internal/service/openai_fast_policy_test.go`
- [x] `backend/internal/service/openai_gateway_chat_completions_raw.go`
- [x] `backend/internal/service/openai_gateway_chat_completions.go`
- [x] `backend/internal/service/openai_gateway_forward.go`
- [x] `backend/internal/service/openai_gateway_messages_chat_fallback.go`
- [x] `backend/internal/service/openai_gateway_messages.go`
- [x] `backend/internal/service/openai_gateway_passthrough.go`
- [x] `backend/internal/service/openai_gateway_record_usage_test.go`
- [x] `backend/internal/service/openai_gateway_request_body.go`
- [x] `backend/internal/service/openai_gateway_usage.go`
- [x] `backend/internal/service/openai_oauth_passthrough_test.go`
- [x] `backend/internal/service/openai_ws_forwarder_ingress.go`
- [x] `backend/internal/service/openai_ws_forwarder_payload.go`
- [x] `backend/internal/service/openai_ws_forwarder.go`
- [x] `backend/internal/service/openai_ws_v2_passthrough_adapter.go`

Review result: Kimi native Responses uses the new CN capability helpers;
DeepSeek-only tool adaptation remains scoped; Fast is applied across HTTP,
adapters, passthrough, and WebSocket before the global policy; final prompt/cache
normalization remains last; Free Fast reuses account-aware unified pricing; and
pre-terminal WebSocket closure is a failed turn rather than successful usage.

### Frontend And Documentation

- [x] `frontend/src/components/account/__tests__/CreateAccountModal.spec.ts`
- [x] `frontend/src/components/account/__tests__/EditAccountModal.spec.ts`
- [x] `frontend/src/components/account/AccountStatusIndicator.vue`
- [x] `frontend/src/components/account/AccountUsageCell.vue`
- [x] `frontend/src/components/account/CreateAccountModal.vue`
- [x] `frontend/src/components/account/EditAccountModal.vue`
- [x] `frontend/src/components/keys/UseKeyModal.vue`
- [x] `frontend/src/composables/useModelWhitelist.ts`
- [x] `frontend/src/i18n/locales/en/admin/overview.ts`
- [x] `frontend/src/i18n/locales/en/dashboard.ts`
- [x] `frontend/src/i18n/locales/zh/admin/overview.ts`
- [x] `frontend/src/i18n/locales/zh/dashboard.ts`
- [x] `frontend/src/types/index.ts`
- [x] `frontend/src/views/admin/ChannelsView.vue`
- [x] `frontend/src/views/admin/GroupsView.vue`
- [x] `README_CN.md`
- [x] `README_JA.md`
- [x] `README.md`

Review result: Fast controls are limited to OpenAI/Composite group forms and
default off; old Reasoning mappings remain compatible; Kimi adaptive Responses
configuration and Fable pricing UI are present; downstream Cindy, account task,
import, overdraft, and read-only compatibility surfaces remain present. The old
per-account Alpha Search and Prompt Cache Key controls are not restored.

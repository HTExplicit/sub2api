# Official v0.2.19 integration

- Official tag: `v0.2.19`, commit `dece26153621959ccd7635814f4b2d08304e681a`.
- Downstream starting commit: `6ac28b7ecf2e247b1dbe5985b6ad115016aa514f`.
- The operator approved the resolutions below before implementation. The risk
  report owns its input counts and unresolved-conflict status; this review
  records behavior choices. Source admission, release and US production delivery
  remain separate facts in the operator workspace.

## Initial text conflicts and selected resolutions

| Conflict files | Resolution |
| --- | --- |
| `backend/cmd/server/wire_gen.go`, `backend/internal/service/wire.go` | Wire official scoped administrator-key services and new dependencies. Remove retired capacity resolver/catalog refresh wiring; keep independent native services and Codex identity dependencies. |
| `backend/go.mod`, `backend/go.sum` | Use official v0.2.19 x/crypto, x/net, x/sync, x/term, x/text, x/sys and x/tools versions/checksums. Keep x/text as a direct dependency for the existing refusal normalizer; retain Chromium/Pelican, pgx and other independent dependencies. |
| `backend/internal/handler/admin/account_handler.go` | Adopt official Grok model diagnostics and administrator-key changes. Remove custom capacity create/update fields and read/preview endpoints. Preserve password-gated API-key wrapper/no-store, `view=plan`, actual effort rows, manual sync, picker source and independent account operations. |
| `backend/internal/repository/account_repo.go`, `backend/internal/repository/account_repo_opencode_go_usage_test.go`, `backend/internal/repository/account_repo_upstream_billing_probe_update_test.go` | Use official transaction scope and outbox logging/continue behavior for Create/GetByID/SetSchedulable; remove custom capacity patch/extra merging. Merge official Grok managed fields with the locked system-prompt binding and retained OpenCode/Ollama/probe state. Test fixtures follow the final columns without deleting those independent assertions. |
| `backend/internal/service/account_test_service.go` | Adopt official Gemini test default and new protocol behavior while retaining test plans, raw administrator errors, actual effort, fixed Codex devices/strict transport, system prompts and DeepSeek default effort. Remove capacity/Cindy-specific hooks and fixtures. |
| `backend/internal/service/admin_service.go` | Adopt official administrator-key and proxy input fields; remove `ModelContextOverrides`. Preserve unrelated native account inputs. |
| `backend/internal/service/api_key_auth_cache_impl.go` | Include the official image-count billing field and use snapshot version 29 so incompatible earlier cached projections reload. Preserve independent stored group/account fields. |
| `backend/internal/service/billing_service.go`, `backend/internal/service/pricing_service.go`, `backend/internal/service/deepseek_pricing_test.go` | DeepSeek pricing is entirely official, including continued Pro pricing, official fallback/force-official behavior and peak handling in the official unified token path. Remove downstream pricing registry/catalog forcing, alias/namespace price identity hooks and shared time-pricing layer. Generic override-file support remains. Adopt Terra and official unknown-GPT-6 prefix behavior; retain the independent GPT-5.6 named-family-before-numeric fix. |
| `backend/internal/service/gemini_messages_compat_service.go` | Use official same-account retry and explicit-cache behavior. Keep the existing post-conversion system prompt and DeepSeek effort application; do not create a second retry mechanism. |
| `backend/internal/service/openai_codex_transform.go` | Adopt official Terra/model-name normalization and dated-name behavior. Preserve independent requested-effort accounting and other actual wire-path behavior. |
| `backend/internal/service/openai_gateway_chat_completions.go`, `backend/internal/service/openai_gateway_messages.go` | Buffered reader, terminal/error handling, failed-response reconstruction and Grok model audit are official. Remove downstream raw terminal-payload retention, bare-error special handling and pending-signature branches. Keep independent identity, system prompts, effort and buffered completed-argument reconstruction. |
| `backend/internal/service/openai_images_responses.go` | Adopt official dedicated-image endpoint accounting (`ImageEndpoint`) and result shape while retaining the actual Codex transport/identity and independent diagnostics. |
| `backend/internal/service/setting_service.go` | Include official scoped administrator-key storage while retaining independent Codex fingerprint synchronization and native settings services. Remove custom capacity-catalog settings wiring. |
| `backend/internal/service/upstream_models.go`, `backend/internal/service/upstream_models_test.go` | Metadata structures, models.dev enrichment, persistence and Grok catalog observation follow official v0.2.19. Remove the downstream capacity/source-identity/revision system, background refresh and the entire Cindy registry enrichment path. Preserve picker `model_list_source`, existing Anthropic/Gemini pagination, structured sync errors, actual effort metadata and fixed-device strict sending. |
| `frontend/src/api/admin/accounts.ts` | Include official Grok diagnostics/types and retain test-plan/picker/actual-effort interfaces. Remove custom capacity types, form fields and APIs; backend plural filter APIs and independent account interfaces remain. |
| `frontend/src/components/account/EditAccountModal.vue`, `frontend/src/components/account/__tests__/EditAccountModal.grokUpstream.spec.ts` | Adopt official Grok diagnostics and refreshed catalogue semantics. Remove custom context controls; preserve picker confirmation, sync completion refresh, system prompts, API-key reveal and other independent account settings. |
| `frontend/src/components/account/ModelWhitelistSelector.vue`, `frontend/src/components/account/__tests__/ModelWhitelistSelector.spec.ts` | Preserve searchable picker, confirmation-based additions/removals and cancel-without-change. Official Grok configured fallback warns without opening the picker. No sync result silently rewrites the whitelist; account Save still persists it. |
| `frontend/src/composables/useModelWhitelist.ts` | Adopt official Gemini defaults and Terra/model presets while retaining the existing mapping and actual effort choices. Remove retired custom-capacity selector data without introducing a new catalog. |

These rows cover all 28 initial text-conflict files. The same decisions apply to
cleanly merged dependent paths, form types, deleted feature tests and generated
wiring; a clean textual merge does not preserve a retired subsystem.

## Capacity and manifest retirement

Model context capacity and client manifests return to official v0.2.19 across
administrative forms/settings, public model lists, group manifests and client
exports. Retire the custom override/evidence resolver, source priority and
source identity/revision semantics, group minimum projection, dedicated capacity
endpoints, automatic refresh and capacity self-test. Remove the separate Cindy
model-access request, cache and enrichment write path. Do not delete historical
SQL or clean stored account extras as part of this retirement.

Native metadata synchronization and models.dev enrichment remain. Codex
manifest synthesis/completion, configured/pinned discovery, ordinary cache and
account selection follow official behavior. Remove the downstream manifest
scheduling/failover retry adapter; retain the fixed credential-source devices and
strict transport at the actual fetch. Use Key keeps official catalog fetching,
remote/file mode and the over-1-MiB file switch; downloads use
`/v1/models?client_version=0.158.0`. OpenCode capacities use the official export,
without a Codex manifest fetch or a second resolver.

## Official additions and independent boundaries

- Adopt scoped administrator API keys, dedicated-image count billing (default
  off), GPT-6.1 Terra, Gemini defaults/retries, Grok model diagnostics/audit,
  DeepSeek cache-hit usage, pagination and upload fixes, and the other
  conflict-free official changes.
- Keep official `247_scoped_admin_api_keys.sql` and
  `248_group_image_count_billing.sql` unchanged alongside historical downstream
  migrations. Existing SQL filenames/checksums, persisted account settings and
  diagnostic ledgers are retained. No account mapping, production switch or
  credential rewrite accompanies source integration.
- Preserve independent system prompts, DeepSeek default reasoning effort,
  requested/forwarded effort behavior, API-key reveal, test plans, administrator
  visibility, account jobs/taxonomy, login protection, strict fixed-device Codex
  identity and Pelican workbench. Pelican uses its existing 64000 output budget
  or an explicitly smaller limit after removal of the capacity resolver.
- Official account transaction scope and transaction outbox logging replace the
  downstream immediate-error return. This does not add another transaction or
  recovery layer.
- Reuse normal required PR checks and the fixed-image release/deploy flow.
  Local checks target changed risks; successful checks on the same code tree are
  reused. No new fallback, retry, recovery switch or compatibility layer is
  introduced. Real model, payment and failed-login probes are not required.

A completed source review does not establish test, release or deployment
success. The operator production pointer and delivery evidence own the actual
results, admitted source, immutable image digest and running version.

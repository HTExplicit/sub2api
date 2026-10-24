# Official v0.2.18 integration

- Official tag: `v0.2.18`, commit `ac1b8f1271be0d674fa6f074403c1410f30a3a81`.
- Downstream starting commit: `48da9d2dd3c5cfceaaf5f8477556e32b83fd2852`.
- The operator explicitly selected the resolutions below before implementation.
  Machine-readable risk and final unresolved-conflict status belong to
  `upstream-risk.json`; source admission and production delivery are recorded
  separately in the operator workspace.

## Initial text conflicts and selected resolutions

| Conflict files | Resolution |
| --- | --- |
| `backend/internal/repository/scheduler_cache.go` | Use the official memo/revision algorithm, original key names and batch Pipeline. Remove downstream v2 isolation, legacy-key cleanup and extra batch transactions. Retain independent account metadata projections and the official delete-account transaction. No cache migration, Redis clearing or startup barrier is added. |
| `backend/internal/service/gateway_forward.go` | Ordinary Claude SSE handling uses the official 403/529 helper. Retire the downstream SSE classifier, prelude buffering and same-account thinking repair; retain official HTTP 400 thinking/tool repair and historical diagnostic readers. |
| `backend/internal/service/gateway_forward_as_chat_completions.go`, `backend/internal/service/gemini_chat_completions_compat_service.go` | Adopt official disconnect/usage accounting and stream readers. Retire the downstream streaming tool consistency guard and its dedicated error handling in the shared converter and all callers. Retain independent buffered argument reconstruction by actual call ID. |
| `backend/internal/service/openai_gateway_upstream_errors.go` | Remove the downstream special HTTP 400 upstream-error-wrapper classifier and all dependent calls/annotations. Use official error classification and capacity Retry-After waiting. |
| `backend/internal/service/openai_oauth_passthrough_test.go`, `backend/internal/service/openai_setup_token_compat_test.go` | Assertions use the final per-account CLI identity. Retain fixed randomly derived devices, independent conversations, turn state and strict transport; reuse the official normalization helper at the final outbound selector. |
| `frontend/package.json`, `frontend/pnpm-lock.yaml` | Use official v0.2.18 overrides, DOMPurify 3.4.17 and follow-redirects 1.16.1; remove redundant nanoid/Vue constraints. Preserve the independent xlsx 0.20.3 author-distribution dependency. |
| `frontend/src/components/account/AccountUsageCell.vue` | Use the official Gemini/Vertex usage region and service-account quota capability. Remove the former common quota bars and loading-with-old-values condition in that region. |
| `frontend/src/components/account/CreateAccountModal.vue`, `frontend/src/components/account/EditAccountModal.vue`, `frontend/src/components/admin/account/AccountActionMenu.vue` | Merge the imports used by official quota support and the independent model-sync/system-prompt features. Adopt official Vertex quota editing/reset behavior. |
| `frontend/src/views/admin/AccountsView.vue` | Platform/status return to official single-select localStorage persistence. Ignore old plural platform/status URL conditions; keep other filters and sensitive session state. Restore the official timer verbatim with `immediate: false`; it remains inactive, as explicitly selected after checking the library startup behavior. |

These rows cover all 14 initial conflict files. Clean textual merges do not
override these decisions: the final per-account identity, shared tool converter,
filter state and dependent error-classification calls follow the same contract.

## Official additions and retained boundaries

- Preserve official `246_enable_gemini_image_generation_groups.sql` unchanged
  alongside historical `246_plugin_extension_state.sql`. Existing Gemini and
  Antigravity groups are enabled for image generation. Composite groups retain
  their stored switch and reject Gemini image requests when it is off.
- Adopt the remaining conflict-free v0.2.18 changes, including stream billing,
  Vertex quota accounting, scheduling performance, Cloudflare 1010 handling,
  Gemini tool schemas, DeepSeek image payloads and image-storage download rules.
  Go, Docker and CI use official 1.27.3.
- Preserve independent system prompts, DeepSeek default effort, model capacity
  evidence and source binding, administrator visibility, account jobs/taxonomy,
  login protection, strict Codex transport and Pelican workbench behavior.
  Retired borrowing and recovery interfaces remain absent. Historical SQL names,
  checksums and diagnostic records remain intact.
- Use the existing required PR checks and fixed-image release/deploy flow. Local
  checks are limited to the changed risks; successful checks for the same code
  tree are reused. No new fallback, retry, recovery switch or compatibility layer
  is introduced. No real model calls or live payment tests are part of this
  integration.

Source integration does not establish release or deployment completion. The
operator production pointer and delivery evidence remain authoritative for the
running version, immutable image digest and actual verification results.

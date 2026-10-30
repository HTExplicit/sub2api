# Official v0.2.18 integration

Historical review; current contracts are in [DOWNSTREAM](../DOWNSTREAM.md).

- Official tag: `v0.2.18`, commit `ac1b8f1271be0d674fa6f074403c1410f30a3a81`; downstream start: `48da9d2dd3c5cfceaaf5f8477556e32b83fd2852`.
- The operator selected the resolutions below. Pinned risk inputs and unresolved-conflict status belong to the machine report; source admission and production delivery are separate facts.

## Initial text conflicts and selected resolutions

| Conflict files | Resolution |
| --- | --- |
| `backend/internal/repository/scheduler_cache.go` | Official memo/revision, keys and batch Pipeline; remove downstream isolation/cleanup/extra transactions. Keep independent projections; no Redis clearing or cache migration. |
| `backend/internal/service/gateway_forward.go` | Official Claude SSE 403/529; retire classifier, prelude buffering and SSE thinking repair. Keep HTTP 400 repair and historical diagnostic readers. |
| `backend/internal/service/gateway_forward_as_chat_completions.go`, `backend/internal/service/gemini_chat_completions_compat_service.go` | Official disconnect/usage readers; retire streaming tool guard in all callers. Keep buffered completed arguments by actual call ID. |
| `backend/internal/service/openai_gateway_upstream_errors.go` | Official classification and Retry-After; remove special HTTP 400 wrapper handling. |
| `backend/internal/service/openai_oauth_passthrough_test.go`, `backend/internal/service/openai_setup_token_compat_test.go` | Final official CLI normalization with retained fixed random device, independent conversations and strict transport. |
| `frontend/package.json`, `frontend/pnpm-lock.yaml` | Official security overrides; remove redundant nanoid/Vue constraints, retain xlsx 0.20.3. |
| `frontend/src/components/account/AccountUsageCell.vue` | Official Gemini/Vertex usage/quota layout; remove common quota bars and stale-value loading condition. |
| `frontend/src/components/account/CreateAccountModal.vue`, `frontend/src/components/account/EditAccountModal.vue`, `frontend/src/components/admin/account/AccountActionMenu.vue` | Official Vertex quota editing/reset plus independent sync/prompt imports. |
| `frontend/src/views/admin/AccountsView.vue` | Official platform/status single selection in localStorage; ignore old plural URL conditions, retain other state. Billing timer keeps `immediate: false` and remains inactive. |

All 14 initial conflict files are covered; dependent clean merges use the same decisions.

## Official additions and retained boundaries

- Preserve official `246_enable_gemini_image_generation_groups.sql` beside historical `246_plugin_extension_state.sql`, filenames/checksums unchanged. Existing Gemini/Antigravity groups enable images; Composite retains its switch and rejects Gemini images when off.
- Adopt stream billing, Vertex quotas, scheduling reuse, Cloudflare 1010, Gemini tool schemas, DeepSeek image payloads and public-only image downloads. Go/Docker/CI use official 1.27.3; frontend takes DOMPurify 3.4.17 and follow-redirects 1.16.1.
- Preserve independent prompts, effort, administrator visibility, account jobs, login protection, strict Codex identity and Pelican. The capacity resolver retained in this release was later retired. Borrowing/recovery interfaces stay absent; historical SQL and diagnostics remain.
- Use normal required PR checks, changed-risk local validation and same-tree result reuse. No real model or live payment tests were included. Source review does not prove release/deployment completion; delivery evidence owns those results.

# Official v0.2.19 integration

- Official tag: `v0.2.19`, commit `dece26153621959ccd7635814f4b2d08304e681a`; downstream start: `6ac28b7ecf2e247b1dbe5985b6ad115016aa514f`.
- The operator approved the decisions below. The machine report owns pinned risk inputs and unresolved-conflict status. [DOWNSTREAM](../DOWNSTREAM.md) owns current behavior; source admission, release and US production delivery are separate facts.

## Initial text conflicts and selected resolutions

| Conflict files | Resolution |
| --- | --- |
| `backend/cmd/server/wire_gen.go`, `backend/internal/service/wire.go` | Official scoped-key dependencies; remove capacity/refresh wiring, retain independent native/identity services. |
| `backend/go.mod`, `backend/go.sum` | Official dependency versions/checksums; keep direct x/text for refusal normalization and independent Chromium/Pelican/pgx dependencies. |
| `backend/internal/handler/admin/account_handler.go` | Official Grok/scoped-key behavior; remove capacity fields/APIs. Retain password-gated no-store key reveal, test plan/effort, sync and picker source. |
| `backend/internal/repository/account_repo.go`, `backend/internal/repository/account_repo_opencode_go_usage_test.go`, `backend/internal/repository/account_repo_upstream_billing_probe_update_test.go` | Official transaction scope and outbox log/continue. Remove capacity merging; retain locked prompt bindings and OpenCode/Ollama/probe fields/assertions. |
| `backend/internal/service/account_test_service.go` | Official Gemini defaults/protocols; keep test plans, raw errors, effort, strict devices, prompts and DeepSeek default effort. Remove capacity/Cindy hooks. |
| `backend/internal/service/admin_service.go` | Official scoped-key/proxy inputs; remove ModelContextOverrides, retain unrelated inputs. |
| `backend/internal/service/api_key_auth_cache_impl.go` | Official image-count field and snapshot version 29; retain independent projections. |
| `backend/internal/service/billing_service.go`, `backend/internal/service/pricing_service.go`, `backend/internal/service/deepseek_pricing_test.go` | Official DeepSeek pricing/peak/cache semantics and Terra/unknown-GPT-6 prefixes. Remove registry/catalog/identity/time-pricing hooks; retain override files and GPT-5.6 named-family precedence. |
| `backend/internal/service/gemini_messages_compat_service.go` | Official retries/explicit cache; retain post-conversion prompt and default effort without a second retry path. |
| `backend/internal/service/openai_codex_transform.go` | Official Terra/dated-name normalization; retain requested-effort accounting. |
| `backend/internal/service/openai_gateway_chat_completions.go`, `backend/internal/service/openai_gateway_messages.go` | Official buffered terminal/error reconstruction and Grok audit. Remove raw-terminal/bare-error/pending-signature branches; retain identity, prompts, effort and completed-argument reconstruction. |
| `backend/internal/service/openai_images_responses.go` | Official ImageEndpoint accounting/shape with actual strict identity/transport and independent diagnostics. |
| `backend/internal/service/setting_service.go` | Official scoped-key storage plus identity/native settings; remove capacity catalog wiring. |
| `backend/internal/service/upstream_models.go`, `backend/internal/service/upstream_models_test.go` | Official metadata/models.dev/Grok observation; remove custom capacity/source/revision/refresh and Cindy registry. Keep picker source, pagination, structured errors, effort and strict devices. |
| `frontend/src/api/admin/accounts.ts` | Official Grok types plus retained test plan/picker/effort; remove capacity types/forms/APIs, keep plural backend filter APIs. |
| `frontend/src/components/account/EditAccountModal.vue`, `frontend/src/components/account/__tests__/EditAccountModal.grokUpstream.spec.ts` | Official Grok/catalog behavior; remove capacity controls, retain picker confirmation, completion refresh, prompts/key reveal. |
| `frontend/src/components/account/ModelWhitelistSelector.vue`, `frontend/src/components/account/__tests__/ModelWhitelistSelector.spec.ts` | Search/confirm/cancel picker remains. Grok fallback warns without opening it; account Save still persists changes. |
| `frontend/src/composables/useModelWhitelist.ts` | Official Gemini/Terra presets with retained mapping/effort; remove capacity selector data. |

These rows cover all 28 initial conflict files. Dependent clean merges, generated wiring and feature-test removal follow the same decisions.

## Capacity and manifest retirement

Use official capacity defaults, metadata, manifests and exports. Remove the override/evidence resolver, priority/source/revision policy, group minimum projection, dedicated APIs, automatic refresh/self-test, Cindy registry/cache and manifest retry adapter. Do not clean historical SQL or stored account extras.

Keep native metadata/models.dev/Grok enrichment, configured/pinned discovery and strict fixed devices at actual directory fetches. Use Key retains official cache, remote/file mode and the over-1-MiB switch; downloads use `/v1/models?client_version=0.158.0`. OpenCode uses official export without a second resolver or Codex fetch.

## Official additions and independent boundaries

- Adopt scoped administrator keys, default-off image-count billing, Terra, Gemini defaults/retries, Grok diagnostics, DeepSeek cache-hit usage and conflict-free fixes.
- Keep `247_scoped_admin_api_keys.sql` and `248_group_image_count_billing.sql` unchanged beside same-number historical migrations. No account mapping, credential, production switch or diagnostic ledger rewrite accompanies integration.
- Preserve prompts, DeepSeek default effort, requested/forwarded effort, key reveal, test plans, full administrator visibility, account jobs/taxonomy, login protection, strict fixed-device identity and Pelican. Pelican retains its 64000 output budget or an explicitly smaller limit.
- Official account transactions/outbox logging replace immediate downstream errors without adding a recovery layer.
- Reuse required PR checks and passing same-tree results; local checks target changed risks. No new fallback/retry switch or real model/payment/failed-login probe is required. Review completion alone establishes no test, release or deployment success; delivery evidence owns actual results.

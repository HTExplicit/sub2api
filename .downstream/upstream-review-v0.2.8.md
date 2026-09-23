# Upstream v0.2.8 integration review

## Source and provenance

- Downstream base: production `v0.2.7-codexrip.4`, main commit `f78c2cf54c797a2ccb655848be5452c42a13adde`.
- Upstream: tag `v0.2.8` (tag object `d7a82d78ca51d42be41cb4daa3510ea401defe9f`, commit `fd80b08c90b55edcad5b00171b53f08721d30da1`); merge base is upstream `v0.2.7` commit `aea725f2ea644d5592d0bbb1d63b607efa7e200a`.
- The hourly sync stopped on text conflicts (issue #195). The merge was resolved by hand on `sync/upstream-0.2.8`: 57 files / 126 hunks (38 both-added, 88 both-modified). `.downstream/upstream-risk.json` is the unmodified `upstream_risk.py` output plus `initial_conflict_files`.

## Migrations

- Upstream adds `238b_content_moderation_engine_meta`, `239_channel_reasoning_effort_multipliers` and `240_affiliate_ledger_operation_id`. The runner applies every unapplied file by name, so production runs them after downstream `253`; they touch no table that downstream `239_codexrip…253` changes.
- 239 converts an explicitly configured `max_reasoning_effort_multiplier` into the per-effort `reasoning_effort_multipliers` map (channel pricing, account-stats pricing and group JSON pricing). Downstream billing reads the per-effort map.

## Upstream implementations adopted over downstream duplicates

- GPT-6 Sol/Luna pricing, detection and fallback prices (same values as downstream: 272k long-context surcharge, Fast x2, cache write 1.25x). Downstream `openai_gpt6_pricing.go` and the duplicate model-map keys, switch cases, `DefaultModels` entries, frontend preset buttons and pricing-file entries were removed. Codex-only parts stay: routing gates, pinned instruction templates and live capacity.
- Select keyboard focus / search highlight fix, ported into `backend/pkg/extensionapi/ui/components/Select.vue` because the host `Select.vue` re-exports it. Nested dialog scroll locking already reference-counts `body.modal-open` in the downstream dialog stack.
- OpenAI scheduling rate uses upstream OAuth-like weighting; gateway selection uses the upstream mixed-platform mapping filter and reports sticky-session hits.
- Response decompression moved into `doUpstreamRequest`; the downstream Codex SSE `Content-Type` restoration runs there as well.
- Gemini-compatible image models (`gemini-*-image`) on the Images endpoint are served by API-key accounts only. The downstream handler and scheduler image fences now admit them; strict Cindy groups keep rejecting unsupported endpoint/model pairs.

## Downstream behavior kept where upstream differs

1. Reasoning effort normalization and recording keep downstream semantics: `max` is preserved except for models without it (gpt-5.5/5.4 map `max` to `xhigh`). Upstream's "record the provider-normalized effort" was not adopted.
2. GPT-6 Sol lists Codex's `ultra` workflow in the Codex catalog (six levels), as the official catalog does; Luna lists five.
3. An explicit `reasoning.effort=none` is kept for every OpenAI-wire account, including compatible hosts (downstream since `72edee8fb`); upstream strips it for non-official base URLs.
4. Undocumented dated GPT-6 snapshots (for example `gpt-6-luna-2099-01-01`) pass through unchanged; upstream's generic prefix table would rewrite them to the base model.
5. A Responses stream needs an authoritative terminal event; `[DONE]` is only a transport marker (upstream treats `[DONE]` as terminal).
6. The admin test-model picker keeps the downstream rule: saved mapping names stay testable even when the provider catalog omits their target, and wildcard mappings select request IDs from the raw catalog (`FetchOpenAIAccountCatalogModels`). The projected list (`FetchOpenAIAccountModels`) keeps upstream semantics.
7. Group profit control stays removed (downstream `5f216d1d0`); GroupsView only gains upstream reasoning-multiplier validation.
8. User platform quotas cover every concrete platform, including Cindy.

## Fixes found during the merge

- OpenAI token billing with channel pricing dropped the reasoning effort, so configured reasoning multipliers never applied on that path (pre-existing downstream gap, exposed by an upstream test). The channel-pricing branch now passes the forwarded effort.
- Admin account updates again preserve `OpenAIAutoResetCreditStateExtraKey`, which the v0.2.7 merge had dropped.
- The locked probe-extra query returns 17 columns: downstream model-context and current-extra values plus upstream OpenCode-Go state.

## Release

- Host `0.2.8-codexrip.1`; `backend/cmd/server/VERSION` is `0.2.8` and `.downstream/upstream-base` is `v0.2.8`.
- The seven bundled plugins move to `0.2.10`, because every host release re-signs bundled packages. `requires.sub2api` is `>=0.2.7-codexrip.2 <0.2.9`: the plugins need no new host API, so the lower bound stays and the declared range still covers the 0.2.7 host.

## Validation status

- Local Windows, single process: `go build ./...`, `go vet -tags=unit` on the changed packages, and the unit suites of all backend packages.
- Windows-only failures that are identical on downstream main and pass on Linux CI: `TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort` (two `time.Now()` calls share one clock tick) and `TestGPT6InstructionsMatchPinnedCatalog` (`core.autocrlf` checks the embedded templates out with CRLF).

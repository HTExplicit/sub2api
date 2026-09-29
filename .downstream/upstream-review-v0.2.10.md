# Upstream v0.2.10 integration

## Source

- Downstream base: `a3bca48d645f3bcd49720c53e197a43a11282a5e` (`v0.2.9-codexrip.2`, `origin/main`).
- Upstream: stable tag `v0.2.10`, commit `2f3fed2fdb0787141294cec81487a5df30426f7f` (`upstream/main` at `a60a29549f488a854966aaec9541abbe006cac22`).
- Shared ancestor: `4c00df2e0183e2c70b7fa8ba45914205e36aad0c` (upstream `v0.2.9`), which is also the base of the previous integration.
- Upstream delta: 34 commits, 118 paths, +3759/-332. 64 paths overlap downstream changes; 17 of them conflicted textually.
- No upstream schema, migration or dependency changes. No new SQL migration is introduced by this integration.

## Integration decisions

Four high-impact areas were escalated to the owner before any code was written; the owner chose official semantics for all four.

- **Risk-control user allowlist (upstream #7679).** Adopted verbatim: the allowlisted platform users keep full upstream evidence while being excluded from automatic local penalties; default is off and the list is empty. The fork's downstream visibility rules (administrators see complete, unmasked information) are unaffected because the upstream change adds fields rather than hiding any.
- **Claude Sonnet 5.5 support (upstream #7683).** Adopted verbatim, including the upstream effort catalog and the single-pass tool-name rewrite in `gateway_tool_rewrite.go`. Downstream effort accounting, exact date aliases, explicit `none` and downstream `max` semantics remain in place and compose with the upstream catalog.
- **Billing and streamed usage normalization (upstream #7380/#7684).** Adopted: billed figures follow the upstream normalization for streamed Anthropic usage and chat-stream usage forwarding. The one-off value shift this causes in existing figures is accepted by the owner's decision to follow official semantics.
- **Client tabs for Claude Code-only groups (upstream #7678).** Adopted: unsupported clients are hidden for those groups. The fork's OpenCode tab was preserved and the guard composed with it.

## Deliberate downstream deviations kept

- **Capacity stays manifest-derived.** Upstream re-introduced a hard-coded `limit: { context: 1000000, output: 128000 }` for `claude-opus-5-5` and `claude-sonnet-5-5` in the OpenCode config. The fork removed exactly those constants in `862e84bbb` (v0.2.9 capacity unification) so that an OpenCode limit is emitted only from a resolver-tagged manifest row (`custom`, `official`, `upstream`, `registry`) and stays unknown when no evidence exists. The upstream constants are not restored; the upstream spec assertions that required them were rewritten to the fork's contract. The model entries, adaptive thinking options and variant sets are unchanged.
- **The OpenCode tab keeps fetching the Codex model manifest for OpenAI groups.** Upstream #7680 stopped showing and fetching the catalog for OpenAI groups. The fork derives OpenCode capacity from that manifest, so the catalog panel and fetch remain available for the `opencode` tab only. For the Codex CLI tabs on OpenAI groups the upstream behavior is kept exactly: the config emits no `model_catalog_json` reference and no catalog panel is rendered. This preserves the upstream intent while keeping the fork's capacity source.
- The fork's scheduler reporting (`ReportOpenAIAccountScheduleResultForSelection`, selective proxy-failure reporting, retry-exhausted cooldown) and its composite-route target-platform checks are retained alongside the upstream composite route resolution added in this release.
- The Apple-style console theme, the restored admin visibility and the native in-host capabilities are untouched by this integration.

## Conflict resolutions

- `backend/cmd/server/wire_gen.go`: arguments follow the fork's provider signatures plus the new `compositeRouteResolver` and `claudeResetCreditService` providers.
- `backend/internal/handler/openai_gateway_handler.go` (10 hunks): kept the fork's scheduler reporting, selective failure reporting and refusal-recovery cyber input handling; adopted upstream composite route resolution, the log-only cyber gate, the synchronous session-block write before the asynchronous record, and the body-aware blocked-session lookup. `wsRouteModel` (upstream) and `wsRoutingModel`/`wsForwardModel` (fork) coexist: routing data used for scheduler calls is the resolved upstream model (`wsRoutingModel = wsForwardModel`, identical to the old value outside composite groups). The fork's older asynchronous session-block write was removed, so allowlisted (log-only) users never get transcript blocks, as upstream intends.
- Settings union: `CyberPolicyUserAllowlist` was added alongside the fork's existing refusal and alpha-search keys in `settings_view.go`, `setting_parse.go`, `dto/settings.go`, `domain_constants.go` and `setting_handler_update.go`.
- `AccountUsageCell.vue`: adopted upstream's single stable `ClaudeResetCreditsCell` instance that survives the usage response, while keeping the fork's empty-usage text and always-available Grok quota probe.
- `EditAccountModal.vue`, `ModelWhitelistSelector.spec.ts`, `UseKeyModal.vue`: the fork's capacity-sync props were merged with upstream's `model-mappings` prop.

## Validation

- `GOOS=linux GOARCH=amd64 go build ./...` passes.
- `pnpm run typecheck` passes.
- `pnpm run check:i18n` passes.
- 174 focused frontend specs pass, covering every conflicted frontend file plus the new upstream `ClaudeResetCreditsCell` spec.
- `go test -tags=unit ./internal/handler/` passes, including upstream's composite WebSocket alias and cyber allowlist tests. The shared WebSocket harness counts rejected-turn upstream frames with the fork's `frameCount`.
- Not run: production migration rehearsal, real model calls, deploy, or canary. Source integration does not claim a successful deployment or model-quality acceptance.

## Release target

- `v0.2.10-codexrip.1`, fixed downstream image digest, `deploy-preserve`, resource profile `preserve`, natural drain, only the Sub2API container recreated. Final source SHA, PR and check results, image digest and production outcome belong to the operational delivery evidence.

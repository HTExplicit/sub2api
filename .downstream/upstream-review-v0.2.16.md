# Official v0.2.16 integration

- Official tag: `v0.2.16`, commit `01842576b3d5c1e2545b5a99ad555d36e28ed306`.
- Downstream starting commit: `157836bdc12424a71f1d0bd30a37fcca38842fc9`.
- The 17 initial conflicts cover gateway request/error handling, pricing, model
  discovery, dependency injection, effort metadata, and account UI/tests. Ent and
  Wire are regenerated from the combined schemas and provider sets. Existing
  runtime dependency pins and Go 1.27.2 remain.
- Haiku 5.5 registration, validation, bridge, sampling and tier pricing follow
  official behavior. Its downstream effort entry is adaptive with medium default
  effort. Official API capacity evidence is 1M/128K, verified on 2026-10-13 from
  the [model overview](https://platform.claude.com/docs/en/models/haiku-5-5/overview).
- Codex descriptors retain zero generated capacity defaults, including Sol's
  upstream 500k default. Account, group and client capacities still use the shared
  source-bound resolver. Sol subscription model-card pricing exemption uses the
  actual account and upstream model, without changing API-key/custom-interval
  pricing or downstream DeepSeek time-pricing application.
- Structured-input Chat fallback retains the official complete-body and safe
  retry guards, plus downstream prompt-echo restoration, raw account classifiers,
  request cancellation and session identity. Native Responses preserves downstream
  request-terminal metadata (`NextAccountStop`, no account-health penalty), which
  the inherited no-retry case verifies directly.
- Gemini explicit caching, Vertex authentication/model discovery, model-level
  cooldown scopes, group translations and UI fixes follow official behavior.
  Scheduler projections retain both downstream capabilities and the new cache
  authentication/TTL fields; catalog readers share bounded response handling and
  retain downstream Anthropic/Gemini pagination.
- Existing Codex identity and strict STATE borrowing, Pelican jobs, prompt binding,
  full administrator visibility, account/model mapping, capacity evidence and
  flat-console hooks remain. The model picker keeps its explicit confirmation and
  stale-response guard while showing the upstream sync error message.
- Official migrations `242_group_i18n`, `243_gemini_cached_contents`,
  `244_gemini_cached_contents_channel_usage` and
  `245_gemini_cached_contents_mutation_lease` are unchanged. Full filenames are
  migration identities; matching numeric prefixes do not replace historical SQL.
  Source VERSION is corrected from the official tag's stale 0.2.15 to 0.2.16.

Local validation uses synthetic upstreams, UI fixtures and disposable PostgreSQL
18.1. Normal required PR CI remains authoritative. Source validation, immutable
release and US production availability are recorded separately in the operator
workspace delivery evidence. No real model generation is part of this integration.

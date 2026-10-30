# Official v0.2.16 integration

Historical review; current contracts are in [DOWNSTREAM](../DOWNSTREAM.md).

- Official tag: `v0.2.16`, commit `01842576b3d5c1e2545b5a99ad555d36e28ed306`; downstream start: `157836bdc12424a71f1d0bd30a37fcca38842fc9`.
- The 17 initial conflicts covered gateway errors, pricing, discovery, wiring, effort and account UI. Ent/Wire outputs followed the combined schemas/providers.
- Adopt Haiku 5.5, Gemini explicit caching, Vertex authentication/discovery, model cooldown scopes and official UI fixes. Retain supported effort defaults, bounded catalog reads and Anthropic/Gemini pagination.
- Preserve structured-input fallback safety, cancellation, prompt-echo restoration and session identity. Model-scoped auth rules precede the unmarked count/probe fast path; scheduling-neutral administrator errors remain available.
- Preserve explicit model-sync confirmation/stale-response guards and independent native features. The capacity resolver, strict borrowing and downstream pricing mechanisms retained at that time were later retired; they are not current contracts.
- Official `242_group_i18n`, `243_gemini_cached_contents`, `244_gemini_cached_contents_channel_usage` and `245_gemini_cached_contents_mutation_lease` keep their full filenames and checksums alongside same-number downstream migrations. Source VERSION was corrected to 0.2.16.

Recorded validation used synthetic upstreams, UI fixtures and disposable PostgreSQL 18.1, plus normal required PR checks. No real model generation was included. Source admission, immutable release and US production availability are separate delivery facts.

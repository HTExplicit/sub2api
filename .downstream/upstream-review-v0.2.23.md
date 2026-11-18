# Official v0.2.23 integration

[The risk report](upstream-risk.json) records the exact Git inputs: the
`v0.2.22` baseline, official `v0.2.23` and the admitted downstream base.
The candidate has 20 overlapping paths and no text conflicts.

| Surface | Integration decision |
| --- | --- |
| Models and billing | Adopt GPT-6.2 Sol/Astra pricing, request-time multiplier selection and subscription eligibility. Keep independent 30m/12h OpenAI cache write accounting alongside official 5m/1h bridge accounting. |
| Streaming and protocol conversion | Adopt official first-output idle failover, data-driven idle timeout and usage merging. Keep final prompt handling and the Pelican observation boundary at existing entry points. |
| Channel prices | Preserve 12h prices and stored cards. Add the official nullable `ultrafast_multiplier` in the repository, API and editor. Keep both complete `252` migration filenames. |
| Model selection and client exports | Keep downstream model-sync confirmation and saved-model semantics. Adopt the official copy and whitelist updates. |

The [downstream contract](../DOWNSTREAM.md) owns current behavior. Focused
checks cover model pricing and eligibility, protocol usage and stream handling,
PostgreSQL channel price persistence, and the changed editor and key flows.

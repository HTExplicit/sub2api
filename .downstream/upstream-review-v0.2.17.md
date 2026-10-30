# Official v0.2.17 integration

Historical review; current contracts are in [DOWNSTREAM](../DOWNSTREAM.md).

- Official tag: `v0.2.17`, commit `060d0160417782cd962a0c7f460f6cd4565d4916`; downstream start: `4ebe80cf5c69408fbc6b43a86c730e44f2f03f66`.
- The pinned review covered 153 upstream paths and 21 initial conflicts. No SQL migration was added; historical filenames/checksums stayed fixed.
- Adopt Gemini 4 Argon, thinking summaries, Antigravity endpoint discovery, Vertex retry/auth handling, Responses timestamps, workspace-aware Codex imports and payment fixes.
- GPT-5.5 retirement follows resolved OAuth/setup-token/shadow targets; API keys and explicit replacement mappings remain supported. No production account mapping or default was rewritten.
- Preserve fixed credential-source devices, independent conversations, strict transport, login protection, prompt binding, administrator visibility and Pelican jobs. Historical capacity precedence and multi-select platform/status rules were subsequently replaced by official behavior.
- `gateway.min_output_tokens` defaults to 128, minimum 16. Optional Web Search emulation was not enabled in production.

Recorded validation used synthetic upstreams, local UI fixtures and normal required PR checks. No real model generation, live payment or failed-login probe was included. Source admission, immutable release and US deployment remained separate evidence.

# Official v0.2.15 integration

Historical review; current contracts are in [DOWNSTREAM](../DOWNSTREAM.md).

- Official tag: `v0.2.15`, commit `f2669c8cf62555cd92389b3f55920e9e6e7c6ff2`; downstream start: `d469422e94b0655b22dd9d0a826decda61ffb91e`.
- The 40 initial conflicts covered dependencies/CI, platform catalogs, account interfaces and OpenAI request/usage handling.
- Adopt Cline, Command Code, catalog-driven platforms, protocol conversion, discovery, TPS and asynchronous UI fixes. Each WS turn refreshes group pricing while retaining immutable usage snapshots.
- Adopt official encrypted-content recovery; retire downstream recovery state, admin page/API and special failovers while retaining stored settings and diagnostics. Preserve independent prompt/effort, identity, test-model and administrator-visibility contracts.
- Named standalone API-key tool outputs retain their namespace with official lazy request reconstruction.
- Official 242 and additive 277 remove both database platform CHECKs on upgrade and fresh install. Immutable 251/260/265 stay unchanged; 277 changes no rows.

Recorded validation used fake upstreams, UI fixtures and isolated PostgreSQL 18.1 for recovery, namespace, new-platform prompt/effort, mapped models and WS pricing. Normal required PR checks remained authoritative. Release and production results are separate delivery evidence.

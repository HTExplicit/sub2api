# Official v0.2.15 integration

- Official tag: `v0.2.15`, commit `f2669c8cf62555cd92389b3f55920e9e6e7c6ff2`.
- Downstream starting commit: `d469422e94b0655b22dd9d0a826decda61ffb91e`.
- The 40 initial conflicts cover dependency/CI compatibility, platform catalogs,
  tests, account interfaces and OpenAI request/usage handling. Existing Go
  1.27.2 and dependency security pins and narrow HTTP2 exceptions remain.
- Cline, Command Code, catalog-driven platforms, protocol conversion, model
  discovery, TPS and async UI fixes follow the official release. Each WS turn
  uses its refreshed group pricing while retaining immutable usage snapshots.
- OpenAI encrypted-content recovery and account retry rules follow official;
  downstream recovery state, its admin API/page and special failovers are retired.
  Historical settings and diagnostic data are retained. Claude recovery, Codex
  identity/borrowing, Pelican observation, prompt injection, capacity evidence,
  full administrator visibility and mapped test-model contracts remain.
- Named standalone API-key tool outputs retain their namespace; official lazy
  request reconstruction is used. Compact filters retain existing facets and
  state. New platforms take prompt/effort extensions only on supported paths.
- Official 242 and additive downstream 277 end both upgrade and fresh-install
  paths without the two database platform CHECK constraints. 251/260/265 stay
  byte-for-byte unchanged; 277 changes no rows. VERSION is 0.2.15.

Local validation uses fake upstreams, UI fixtures and an isolated PostgreSQL
18.1 database. It covers official recovery, namespace preservation, new-platform
prompt/effort support, mapped models, filter disclosure and WS turn pricing.
Required PR CI remains authoritative. Release and production status is recorded
only by the operator workspace's production pointer.

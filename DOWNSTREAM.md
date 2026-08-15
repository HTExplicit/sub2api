# HTExplicit Sub2API Downstream

This public fork carries the maintained `codexrip` patch set for Sub2API. It includes
OpenAI refusal recovery, API-key pool scheduling fixes, Cindy budget-exhaustion handling,
and the account management console extensions used by the downstream deployment.

## Current production baseline

- Official baseline: `v0.1.176` at `e803e3851c0a7e222cfadeafad7b8636ab959d11`.
- Downstream release: `v0.1.176-codexrip.1` at
  `4e590b3ad92674a938dfb88dc72772e670798aa2`.
- Production image digest:
  `sha256:09c42953a6e21d2d6eee23b2a0d86f631c8ea30f6b594e3e46444bf950ba48b5`.

## Release model

- `main` is the tested downstream production line.
- `.downstream/upstream-base` records the official stable release merged into `main`.
- New custom tags use `vX.Y.Z-codexrip.N`.
- Existing `vX.Y.Z-refusal-recovery.N` images are legacy inputs only. Production can
  move from that channel to `codexrip`, but cannot deploy a new legacy image or move back.
- Release images are published only as immutable version tags under `ghcr.io/htexplicit/sub2api`.
- Production deployment always resolves and pins the registry digest.

## Upstream updates

The scheduled upstream workflow checks the latest non-draft, non-prerelease release from
`Wei-Shaw/sub2api`. A clean merge opens a candidate pull request and builds a short-lived
OCI artifact. Merge conflicts create an issue and leave `main` unchanged. No upstream-sync
workflow can deploy production.

## OpenAI API-key pool scheduling

OpenAI API-key accounts use an in-process account/model cooldown rather than the Redis
half-open probe lease used by OAuth accounts. Same-account retries occur only when pool
mode is enabled and the response status is explicitly configured for that account. The
default configured statuses remain `401`, `403`, and `429`; `503` is not a global default.
OAuth and other breaker-managed account types continue to use the distributed circuit
breaker.

## Cindy budget exhaustion

Cindy accounts are identified strictly as OpenAI API-key accounts using
`https://api.laxarouter.ai`. HTTP `402`, structured `429` responses with
`error.type=budget_exceeded`, and a fallback where the type is missing and the parsed message
contains both normalized `ExceededBudget` and `over budget` mark the account as budget
exhausted. Ordinary or malformed `429` responses and non-Cindy accounts are not marked. A
matching request switches accounts immediately, and future scheduling excludes the marked
account without changing its administrator-managed enabled state.

The admin account test paths use the same classifier; closing the test dialog refreshes the
account list, Cindy aggregates, and deletion candidates. Recovery clears only the exhaustion
marker. Preview-and-confirm deletion is fingerprint protected and can permanently delete
only marked, enabled Cindy accounts. Non-Cindy, unmarked, and manually disabled accounts are
preserved. No balance amount is queried or displayed.

## Production deployment

The production workflow is manual and protected by the `production` environment. It accepts
only an existing custom release tag, resolves the public GHCR digest, and connects with a
restricted SSH key whose forced command can only invoke the Sub2API updater. Secrets and
server-specific identifiers are stored in the environment, not in this repository.

The workflow also requires three typed boolean rollout inputs, in dependency order: Cindy
balance detection, capability catalog, and Image Studio. Image Studio cannot be enabled while
the catalog is disabled. After resolution, the workflow sends exactly
`deploy <immutable-ref> cindy=<balance>,<catalog>,<image>` to the forced command. The host
updater persists those values in one fixed Compose override; changing only the tuple for the
same digest backs up and checksum-verifies the prior state, recreates only `sub2api`, and
restores the prior override and process flags if any gate fails.

During the guard-first migration window, the host also accepts the legacy workflow's exact
`deploy <immutable-ref>` command and maps it only to `cindy=true,false,false`. This workflow
never emits that legacy form; the compatibility prevents an outage between guard installation
and workflow merge without accepting optional shell arguments.

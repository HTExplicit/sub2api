# HTExplicit Sub2API Downstream

This public fork carries the maintained `codexrip` patch set for Sub2API. It includes
OpenAI refusal recovery and the account management console extensions used by the
downstream deployment.

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

## Production deployment

The production workflow is manual and protected by the `production` environment. It accepts
only an existing custom release tag, resolves the public GHCR digest, and connects with a
restricted SSH key whose forced command can only invoke the Sub2API updater. Secrets and
server-specific identifiers are stored in the environment, not in this repository.

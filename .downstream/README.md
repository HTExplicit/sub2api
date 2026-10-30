# Downstream release channel

`upstream-base` records the exact integrated official stable tag. The hourly
`Upstream Stable Sync` follows non-draft, non-prerelease Releases from
`Wei-Shaw/sub2api`; current source contracts are in [DOWNSTREAM.md](../DOWNSTREAM.md).

A conflict-free candidate is eligible for checked auto-merge only when its risk
report has zero downstream-overlap files and zero critical paths. Other
conflict-free candidates receive `upstream-review-required`; authorized review
records `upstream-reviewed`. Text conflicts stop before push and create a named
issue. Historical reviews and machine risk inputs keep their existing paths.

After admission, automation creates immutable `vX.Y.Z-codexrip.1` once.
Downstream Release builds/attests linux/amd64 and dispatches Production Deploy
with `release_tag` and `confirmation=DEPLOY`. The `production` Environment keeps
its configured approval; authorized delegated maintenance may complete it,
and the release workflow does not approve itself. Verification reuses admitted
results under [the repository rules](../AGENTS.md).

Sub2API stores no GitHub token. VersionBadge reads public metadata and links the
official Release, candidate PR, downstream Release and production run. Release
or merge success alone does not establish production completion.

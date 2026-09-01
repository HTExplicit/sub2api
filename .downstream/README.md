# Downstream release channel

`upstream-base` is the exact official stable tag merged into this fork. The
hourly `Upstream Stable Sync` workflow follows only non-draft,
non-prerelease GitHub Releases from `Wei-Shaw/sub2api`.

An upstream candidate is eligible for checked auto-merge only when the
machine-readable risk report records zero downstream-overlap files, zero
critical paths, and a conflict-free merge. Every other conflict-free candidate
is labeled `upstream-review-required`; the trusted risk gate requires a human
to add `upstream-reviewed`. Text conflicts stop before a branch is pushed and
create a named issue with the conflicted paths.

After a safe candidate merges, automation creates the immutable
`vX.Y.Z-codexrip.1` tag once. The normal Downstream Release workflow builds and
attests the linux/amd64 OCI image. A successful release dispatches Production
Deploy with `runtime=preserve`; that run waits at the GitHub `production`
Environment and is never approved by automation.

Sub2API does not store a GitHub token. The admin VersionBadge reads only public
repository metadata and links to the official Release, candidate PR,
downstream Release, and production workflow run.

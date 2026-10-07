#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORKFLOW="$ROOT/.github/workflows/production-deploy.yml"
DOWNSTREAM_RELEASE_WORKFLOW="$ROOT/.github/workflows/downstream-release.yml"
GENERIC_RELEASE_WORKFLOW="$ROOT/.github/workflows/release.yml"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

# Authentication, immutable source binding, and the protected deploy job remain.
grep -Fq 'packages: read' "$WORKFLOW" || fail 'resolve must request GHCR read permission'
grep -Fq 'docker/login-action@c94ce9fb468520275223c153574b00df6fe4bcc9' "$WORKFLOW" ||
  fail 'resolve must authenticate to GHCR'
grep -Fq 'run-name: Deploy ${{ inputs.release_tag }}' "$WORKFLOW" ||
  fail 'run title must expose the release tag for the status UI'
grep -Fq '      name: production' "$WORKFLOW" || fail 'deployment must use the production environment'
[[ "$(grep -c '^      [a-z_]*:$' "$WORKFLOW")" -eq 2 ]] || fail 'workflow must have only two inputs'
for input in release_tag confirmation; do
  grep -Fq "      ${input}:" "$WORKFLOW" || fail "missing input: $input"
done
if grep -Eq 'expected_current|runtime=|resource=|CINDY_|IMAGE_STUDIO|INTERRUPT_BUSINESS|rollback|reconcile-runtime|io.github.htexplicit' "$WORKFLOW"; then
  fail 'workflow still contains a retired deployment interface or image label gate'
fi
if grep -Fq 'io.github.htexplicit.' "$DOWNSTREAM_RELEASE_WORKFLOW"; then
  fail 'release still emits retired fork image labels'
fi
for label in source version revision created licenses; do
  grep -Fq "org.opencontainers.image.${label}=" "$DOWNSTREAM_RELEASE_WORKFLOW" ||
    fail "standard OCI label missing: $label"
done
grep -Fq "!contains(github.ref_name, '-codexrip.')" "$GENERIC_RELEASE_WORKFLOW" ||
  fail 'generic Release must skip downstream codexrip tags'

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
resolve_script="$tmpdir/resolve.sh"
awk '
  /^        id: resolve$/ { found_resolve = 1; next }
  found_resolve && /^        run: \|$/ { capture = 1; next }
  capture && /^  deploy:$/ { exit }
  capture { sub(/^          /, ""); print }
' "$WORKFLOW" >"$resolve_script"
[[ -s "$resolve_script" ]] || fail 'could not extract resolve step'
apply_script="$tmpdir/apply.sh"
awk '
  /^      - name: Apply immutable release image$/ { found_apply = 1; next }
  found_apply && /^        run: \|$/ { capture = 1; next }
  capture && /^      - name:/ { exit }
  capture { sub(/^          /, ""); print }
' "$WORKFLOW" >"$apply_script"
[[ -s "$apply_script" ]] || fail 'could not extract deploy step'
bash -n "$resolve_script"
bash -n "$apply_script"

mkdir -p "$tmpdir/bin"
cat >"$tmpdir/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
tag=$3
case "$tag" in
  v0.1.177-codexrip.6)
    digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    source=1111111111111111111111111111111111111111
    ;;
  v0.1.177-codexrip.7)
    digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    source=2222222222222222222222222222222222222222
    ;;
  *) exit 3 ;;
esac
if [[ " $* " == *' --json tagName,isDraft,isPrerelease '* ]]; then
  printf '%s\t%s\t%s\n' "$tag" "${MOCK_RELEASE_DRAFT:-false}" "${MOCK_RELEASE_PRERELEASE:-false}"
elif [[ " $* " == *' --json body '* ]]; then
  printf 'Image: `%s`\nSource: `%s`\n' \
    "${MOCK_BODY_IMAGE:-ghcr.io/htexplicit/sub2api:${tag#v}@${MOCK_BODY_DIGEST:-$digest}}" \
    "${MOCK_BODY_SOURCE:-$source}"
  if [[ "${MOCK_DUPLICATE_IMAGE:-false}" == true ]]; then
    printf 'Image: `ghcr.io/htexplicit/sub2api:%s@%s`\n' "${tag#v}" "$digest"
  fi
else
  # No asset inventory or retired Skill gate belongs to deployment resolution.
  exit 2
fi
EOF
cat >"$tmpdir/bin/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  fetch) exit 0 ;;
  merge-base) [[ "${MOCK_NON_ANCESTOR:-false}" == false ]] ;;
  rev-list)
    tag=${!#}
    case "$tag" in
      v0.1.177-codexrip.6) printf '%s\n' 1111111111111111111111111111111111111111 ;;
      v0.1.177-codexrip.7) printf '%s\n' 2222222222222222222222222222222222222222 ;;
      *) exit 3 ;;
    esac
    ;;
  *) exit 2 ;;
esac
EOF
cat >"$tmpdir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
image_ref=$4
case "$image_ref" in
  *:0.1.177-codexrip.6*)
    digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    revision=1111111111111111111111111111111111111111
    ;;
  *:0.1.177-codexrip.7*)
    digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    revision=2222222222222222222222222222222222222222
    ;;
  *) exit 3 ;;
esac
[[ "$image_ref" == *@"$digest" ]] || exit 3
[[ "$6" == '{{ index .Image.Config.Labels "org.opencontainers.image.revision" }}' ]] || exit 2
printf '%s\n' "${MOCK_IMAGE_REVISION:-$revision}"
EOF
cat >"$tmpdir/bin/ssh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'call\n' >>"${SSH_CALLS:?}"
printf '%s\n' "$@" >"${SSH_CAPTURE:?}"
EOF
chmod +x "$tmpdir/bin/gh" "$tmpdir/bin/git" "$tmpdir/bin/docker" "$tmpdir/bin/ssh"

run_resolve() {
  : >"$tmpdir/github-output"
  PATH="$tmpdir/bin:$PATH" \
    MOCK_RELEASE_DRAFT="${MOCK_RELEASE_DRAFT:-false}" \
    MOCK_RELEASE_PRERELEASE="${MOCK_RELEASE_PRERELEASE:-false}" \
    MOCK_BODY_DIGEST="${MOCK_BODY_DIGEST:-}" \
    MOCK_BODY_IMAGE="${MOCK_BODY_IMAGE:-}" \
    MOCK_BODY_SOURCE="${MOCK_BODY_SOURCE:-}" \
    MOCK_DUPLICATE_IMAGE="${MOCK_DUPLICATE_IMAGE:-false}" \
    MOCK_IMAGE_REVISION="${MOCK_IMAGE_REVISION:-}" \
    MOCK_NON_ANCESTOR="${MOCK_NON_ANCESTOR:-false}" \
    RELEASE_TAG="$1" CONFIRMATION="$2" GITHUB_OUTPUT="$tmpdir/github-output" \
    bash "$resolve_script" >"$tmpdir/resolve-output" 2>&1
}

assert_resolve_rejected() {
  if run_resolve "$1" "$2"; then fail "$3"; fi
  [[ ! -s "$tmpdir/github-output" ]] || fail 'rejected release emitted a deployable image'
}

# Both old and new immutable images use the same path, without fork labels.
for tag in v0.1.177-codexrip.6 v0.1.177-codexrip.7; do
  run_resolve "$tag" DEPLOY || { cat "$tmpdir/resolve-output" >&2; fail 'valid release was rejected'; }
  [[ "$(wc -l <"$tmpdir/github-output")" -eq 1 ]] || fail 'resolve must emit only image_ref'
  grep -Fq "image_ref=ghcr.io/htexplicit/sub2api:${tag#v}@sha256:" "$tmpdir/github-output" ||
    fail 'resolve did not emit the immutable release image'
done
for confirmation in ROLLBACK RECONCILE deploy ''; do
  assert_resolve_rejected v0.1.177-codexrip.7 "$confirmation" 'non-DEPLOY confirmation was accepted'
done
for tag in latest v0.1.177 v0.1.177-codexrip.0 'v0.1.177-codexrip.7;id'; do
  assert_resolve_rejected "$tag" DEPLOY 'invalid release tag was accepted'
done
MOCK_RELEASE_DRAFT=true
assert_resolve_rejected v0.1.177-codexrip.7 DEPLOY 'draft release was accepted'
unset MOCK_RELEASE_DRAFT
MOCK_RELEASE_PRERELEASE=true
assert_resolve_rejected v0.1.177-codexrip.7 DEPLOY 'prerelease was accepted'
unset MOCK_RELEASE_PRERELEASE
MOCK_BODY_DIGEST=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
assert_resolve_rejected v0.1.177-codexrip.7 DEPLOY 'unavailable release digest was accepted'
unset MOCK_BODY_DIGEST
MOCK_BODY_IMAGE=ghcr.io/htexplicit/sub2api:latest
assert_resolve_rejected v0.1.177-codexrip.7 DEPLOY 'wrong image tag was accepted'
unset MOCK_BODY_IMAGE
MOCK_BODY_SOURCE=4444444444444444444444444444444444444444
assert_resolve_rejected v0.1.177-codexrip.7 DEPLOY 'release source mismatch was accepted'
unset MOCK_BODY_SOURCE
MOCK_IMAGE_REVISION=4444444444444444444444444444444444444444
assert_resolve_rejected v0.1.177-codexrip.7 DEPLOY 'OCI revision mismatch was accepted'
unset MOCK_IMAGE_REVISION
MOCK_DUPLICATE_IMAGE=true
assert_resolve_rejected v0.1.177-codexrip.7 DEPLOY 'ambiguous release body was accepted'
unset MOCK_DUPLICATE_IMAGE
MOCK_NON_ANCESTOR=true
assert_resolve_rejected v0.1.177-codexrip.7 DEPLOY 'release outside main was accepted'
unset MOCK_NON_ANCESTOR

run_apply() {
  : >"$tmpdir/ssh-capture"
  : >"$tmpdir/ssh-calls"
  PATH="$tmpdir/bin:$PATH" SSH_CAPTURE="$tmpdir/ssh-capture" SSH_CALLS="$tmpdir/ssh-calls" \
    VPS_HOST=production.example.invalid VPS_PORT="${MOCK_VPS_PORT:-2222}" VPS_USER=deployer \
    IMAGE_REF="$1" bash "$apply_script" >"$tmpdir/apply-output" 2>&1
}

image_ref=ghcr.io/htexplicit/sub2api:0.1.177-codexrip.7@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
run_apply "$image_ref" || fail 'valid deploy did not execute'
actual=()
while IFS= read -r line; do actual+=("$line"); done <"$tmpdir/ssh-capture"
expected=(
  -F /dev/null
  -i "$HOME/.ssh/sub2api_deploy"
  -o BatchMode=yes
  -o IdentitiesOnly=yes
  -o ServerAliveInterval=30
  -o ServerAliveCountMax=10
  -o StrictHostKeyChecking=yes
  -o "UserKnownHostsFile=$HOME/.ssh/known_hosts"
  -p 2222
  deployer@production.example.invalid
  "deploy $image_ref"
)
[[ "$(wc -l <"$tmpdir/ssh-calls")" -eq 1 ]] || fail 'deploy must invoke SSH exactly once'
[[ "${#actual[@]}" -eq "${#expected[@]}" ]] || fail 'SSH argument count mismatch'
for ((i = 0; i < ${#expected[@]}; i++)); do
  [[ "${actual[$i]}" == "${expected[$i]}" ]] || fail "SSH argument $i mismatch"
done
for invalid in 'ghcr.io/htexplicit/sub2api:latest' "$image_ref runtime=preserve" "$image_ref;id"; do
  if run_apply "$invalid"; then fail 'invalid image reference was accepted'; fi
  [[ ! -s "$tmpdir/ssh-calls" && ! -s "$tmpdir/ssh-capture" ]] || fail 'rejected reference invoked SSH'
done
MOCK_VPS_PORT=not-a-port
if run_apply "$image_ref"; then fail 'invalid SSH port was accepted'; fi
[[ ! -s "$tmpdir/ssh-calls" && ! -s "$tmpdir/ssh-capture" ]] || fail 'rejected SSH port invoked SSH'
unset MOCK_VPS_PORT

printf 'PASS: single production deploy, immutable source binding, and pinned SSH command\n'

#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORKFLOW="$ROOT/.github/workflows/production-deploy.yml"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

grep -Fq 'inspect=$(docker buildx imagetools inspect "$image")' "$WORKFLOW" ||
  fail 'production digest resolution must consume the complete buildx output'
grep -Fq 'END {if (digest == "") exit 1; print digest}' "$WORKFLOW" ||
  fail 'production digest resolution must validate the parsed digest after EOF'
grep -Fq 'release_body=$(gh release view "$tag" --json body --jq .body)' "$WORKFLOW" ||
  fail 'production resolution must read the immutable image reference recorded in the Release body'
grep -Fq '[[ "$release_image_ref" == "${image}@${digest}" ]]' "$WORKFLOW" ||
  fail 'production resolution must bind the mutable registry tag to the Release digest'
grep -Fq 'image_revision=$(docker buildx imagetools inspect "${image}@${digest}" --format' "$WORKFLOW" ||
  fail 'production resolution must inspect source metadata from the immutable image digest'
grep -Fq '[[ "$image_revision" == "$tag_commit" ]]' "$WORKFLOW" ||
  fail 'production resolution must bind the immutable image revision to the release tag commit'
if grep -Fq '{print $2; exit}' "$WORKFLOW"; then
  fail 'production digest resolution closes the buildx pipe before EOF'
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
resolve_script="$tmpdir/resolve.sh"
awk '
  /^        id: resolve$/ { found_resolve = 1; next }
  found_resolve && /^        run: \|$/ { capture = 1; next }
  capture && /^  deploy:$/ { exit }
  capture { sub(/^          /, ""); print }
' "$WORKFLOW" >"$resolve_script"
[[ -s "$resolve_script" ]] || fail 'could not extract the resolve shell step'
apply_script="$tmpdir/apply.sh"
awk '
  /^      - name: Apply immutable release operation$/ { found_apply = 1; next }
  found_apply && /^        run: \|$/ { capture = 1; next }
  capture && /^      - name:/ { exit }
  capture { sub(/^          /, ""); print }
' "$WORKFLOW" >"$apply_script"
[[ -s "$apply_script" ]] || fail 'could not extract the immutable release operation shell step'

mkdir -p "$tmpdir/bin"
cat >"$tmpdir/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ " $* " == *' --json assets '* ]]; then
  printf '%s\n' 'seed-descriptor.json'
  for ((i = 0; i < 10000; i++)); do
    printf 'ordinary-release-asset-%05d-%064d.bin\n' "$i" "$i"
  done
elif [[ " $* " == *' --json tagName,isDraft,isPrerelease '* ]]; then
  printf '%s\tfalse\tfalse\n' "$3"
else
  exit 2
fi
EOF
cat >"$tmpdir/bin/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  fetch|merge-base) exit 0 ;;
  rev-list)
    tag=${!#}
    case "$tag" in
      v0.1.177-codexrip.6) printf '%s\n' 1111111111111111111111111111111111111111 ;;
      v0.1.177-codexrip.7) printf '%s\n' 2222222222222222222222222222222222222222 ;;
      v0.1.177-codexrip.8) printf '%s\n' 3333333333333333333333333333333333333333 ;;
      *) exit 3 ;;
    esac
    ;;
  *) exit 2 ;;
esac
EOF
cat >"$tmpdir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'Name: image\nDigest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n'
EOF
chmod +x "$tmpdir/bin/gh" "$tmpdir/bin/git" "$tmpdir/bin/docker"

set +e
PATH="$tmpdir/bin:$PATH" \
OPERATION=deploy \
RELEASE_TAG=v0.1.177-codexrip.7 \
EXPECTED_CURRENT_RELEASE_TAG= \
CONFIRMATION=DEPLOY \
CINDY_BALANCE_DETECTION=true \
CINDY_CAPABILITY_CATALOG=false \
IMAGE_STUDIO=false \
GITHUB_OUTPUT="$tmpdir/github-output" \
bash "$resolve_script" >"$tmpdir/resolve-output" 2>&1
resolve_status=$?
set -e
[[ "$resolve_status" -ne 0 ]] ||
  fail 'removed release asset was accepted when the producer received SIGPIPE'
grep -Fq 'contains a removed Skill-specific asset' "$tmpdir/resolve-output" ||
  fail 'removed release asset did not fail through the intended release gate'

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
  v0.1.177-codexrip.8)
    digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
    source=3333333333333333333333333333333333333333
    ;;
  *) exit 3 ;;
esac
if [[ "${MOCK_OVERRIDE_TAG:-}" == "$tag" ]]; then
  digest=${MOCK_BODY_DIGEST_OVERRIDE:-$digest}
  source=${MOCK_BODY_SOURCE_OVERRIDE:-$source}
fi
if [[ " $* " == *' --json assets '* ]]; then
  printf '%s\n' 'sub2api-release-checksums.txt'
elif [[ " $* " == *' --json tagName,isDraft,isPrerelease '* ]]; then
  draft=false
  prerelease=false
  if [[ "${MOCK_OVERRIDE_TAG:-}" == "$tag" ]]; then
    draft=${MOCK_RELEASE_DRAFT_OVERRIDE:-false}
    prerelease=${MOCK_RELEASE_PRERELEASE_OVERRIDE:-false}
  fi
  printf '%s\t%s\t%s\n' "$tag" "$draft" "$prerelease"
elif [[ " $* " == *' --json body '* ]]; then
  version=${tag#v}
  printf 'Image: `ghcr.io/htexplicit/sub2api:%s@%s`\nSource: `%s`\n' "$version" "$digest" "$source"
else
  exit 2
fi
EOF
cat >"$tmpdir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
image_ref=$4
case "$image_ref" in
  *:0.1.177-codexrip.6*)
    tag=v0.1.177-codexrip.6
    digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    revision=1111111111111111111111111111111111111111
    ;;
  *:0.1.177-codexrip.7*)
    tag=v0.1.177-codexrip.7
    digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    revision=2222222222222222222222222222222222222222
    ;;
  *:0.1.177-codexrip.8*)
    tag=v0.1.177-codexrip.8
    digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
    revision=3333333333333333333333333333333333333333
    ;;
  *) exit 3 ;;
esac
if [[ "${MOCK_OVERRIDE_TAG:-}" == "$tag" ]]; then
  digest=${MOCK_REGISTRY_DIGEST_OVERRIDE:-$digest}
  revision=${MOCK_IMAGE_REVISION_OVERRIDE:-$revision}
fi
if [[ " $* " == *' --format '* ]]; then
  printf '%s\n' "$revision"
else
  printf 'Name: image\nDigest: %s\n' "$digest"
fi
EOF
cat >"$tmpdir/bin/ssh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
: "${SSH_CAPTURE:?}"
: "${SSH_CALLS:?}"
printf 'call\n' >>"$SSH_CALLS"
printf '%s\n' "$@" >"$SSH_CAPTURE"
EOF
chmod +x "$tmpdir/bin/gh" "$tmpdir/bin/docker" "$tmpdir/bin/ssh"

run_resolve() {
  local operation=$1 tag=$2 expected_current=$3 confirmation=$4
  : >"$tmpdir/github-output"
  : >"$tmpdir/resolve-output"
  PATH="$tmpdir/bin:$PATH" \
    MOCK_OVERRIDE_TAG="${MOCK_OVERRIDE_TAG:-}" \
    MOCK_RELEASE_DRAFT_OVERRIDE="${MOCK_RELEASE_DRAFT_OVERRIDE:-}" \
    MOCK_RELEASE_PRERELEASE_OVERRIDE="${MOCK_RELEASE_PRERELEASE_OVERRIDE:-}" \
    MOCK_BODY_DIGEST_OVERRIDE="${MOCK_BODY_DIGEST_OVERRIDE:-}" \
    MOCK_BODY_SOURCE_OVERRIDE="${MOCK_BODY_SOURCE_OVERRIDE:-}" \
    MOCK_REGISTRY_DIGEST_OVERRIDE="${MOCK_REGISTRY_DIGEST_OVERRIDE:-}" \
    MOCK_IMAGE_REVISION_OVERRIDE="${MOCK_IMAGE_REVISION_OVERRIDE:-}" \
    OPERATION="$operation" \
    RELEASE_TAG="$tag" \
    EXPECTED_CURRENT_RELEASE_TAG="$expected_current" \
    CONFIRMATION="$confirmation" \
    CINDY_BALANCE_DETECTION=true \
    CINDY_CAPABILITY_CATALOG=false \
    IMAGE_STUDIO=false \
    GITHUB_OUTPUT="$tmpdir/github-output" \
    bash "$resolve_script" >"$tmpdir/resolve-output" 2>&1
}

run_apply() {
  local operation=$1 image_ref=$2 expected_current=$3 rollout=$4
  : >"$tmpdir/ssh-capture"
  : >"$tmpdir/ssh-calls"
  : >"$tmpdir/apply-output"
  HOME="$tmpdir/home" \
    PATH="$tmpdir/bin:$PATH" \
    SSH_CAPTURE="$tmpdir/ssh-capture" \
    SSH_CALLS="$tmpdir/ssh-calls" \
    VPS_HOST=production.example.invalid \
    VPS_PORT=2222 \
    VPS_USER=deployer \
    OPERATION="$operation" \
    IMAGE_REF="$image_ref" \
    EXPECTED_CURRENT_IMAGE_REF="$expected_current" \
    CINDY_ROLLOUT="$rollout" \
    bash "$apply_script" >"$tmpdir/apply-output" 2>&1
}

assert_ssh_invocation() {
  local expected_remote_command=$1 index
  local -a actual expected
  mapfile -t actual <"$tmpdir/ssh-capture"
  expected=(
    -F /dev/null
    -i "$tmpdir/home/.ssh/sub2api_deploy"
    -o BatchMode=yes
    -o IdentitiesOnly=yes
    -o StrictHostKeyChecking=yes
    -o "UserKnownHostsFile=$tmpdir/home/.ssh/known_hosts"
    -p 2222
    deployer@production.example.invalid
    "$expected_remote_command"
  )
  [[ "$(wc -l <"$tmpdir/ssh-calls")" -eq 1 ]] ||
    fail 'immutable release operation did not invoke ssh exactly once'
  [[ "${#actual[@]}" -eq "${#expected[@]}" ]] ||
    fail "ssh argv length mismatch: expected ${#expected[@]}, got ${#actual[@]}"
  for ((index = 0; index < ${#expected[@]}; index++)); do
    [[ "${actual[$index]}" == "${expected[$index]}" ]] ||
      fail "ssh argv[$index] mismatch: expected '${expected[$index]}', got '${actual[$index]}'"
  done
}

assert_ssh_not_invoked() {
  [[ ! -s "$tmpdir/ssh-calls" && ! -s "$tmpdir/ssh-capture" ]] ||
    fail 'rejected immutable release operation still invoked ssh'
}

rollback_target_ref=ghcr.io/htexplicit/sub2api:0.1.177-codexrip.6@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
rollback_current_ref=ghcr.io/htexplicit/sub2api:0.1.177-codexrip.7@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

run_resolve deploy v0.1.177-codexrip.7 '' DEPLOY ||
  fail 'valid deploy resolution did not execute successfully'
grep -Fq "image_ref=$rollback_current_ref" "$tmpdir/github-output" ||
  fail 'deploy did not emit the Release-bound immutable image'
grep -Fxq 'expected_current_image_ref=' "$tmpdir/github-output" ||
  fail 'deploy unexpectedly emitted an expected-current rollback image'

run_resolve rollback v0.1.177-codexrip.6 v0.1.177-codexrip.7 ROLLBACK ||
  fail 'valid rollback resolution did not execute successfully'
grep -Fq "image_ref=$rollback_target_ref" "$tmpdir/github-output" ||
  fail 'rollback did not emit the immutable target image'
grep -Fq "expected_current_image_ref=$rollback_current_ref" "$tmpdir/github-output" ||
  fail 'rollback did not emit the immutable expected-current image'

MOCK_OVERRIDE_TAG=v0.1.177-codexrip.7
MOCK_BODY_DIGEST_OVERRIDE=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
if run_resolve deploy v0.1.177-codexrip.7 '' DEPLOY; then
  fail 'registry digest was accepted when it did not match the Release body'
fi
unset MOCK_BODY_DIGEST_OVERRIDE
MOCK_BODY_SOURCE_OVERRIDE=4444444444444444444444444444444444444444
if run_resolve deploy v0.1.177-codexrip.7 '' DEPLOY; then
  fail 'Release source was accepted when it did not match the release tag commit'
fi
unset MOCK_BODY_SOURCE_OVERRIDE
MOCK_IMAGE_REVISION_OVERRIDE=4444444444444444444444444444444444444444
if run_resolve deploy v0.1.177-codexrip.7 '' DEPLOY; then
  fail 'immutable image was accepted when its OCI revision did not match the release tag commit'
fi
unset MOCK_IMAGE_REVISION_OVERRIDE
MOCK_RELEASE_DRAFT_OVERRIDE=true
if run_resolve deploy v0.1.177-codexrip.7 '' DEPLOY; then
  fail 'draft Release was accepted for production deploy'
fi
unset MOCK_RELEASE_DRAFT_OVERRIDE MOCK_OVERRIDE_TAG

MOCK_OVERRIDE_TAG=v0.1.177-codexrip.7
MOCK_BODY_DIGEST_OVERRIDE=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
if run_resolve rollback v0.1.177-codexrip.6 v0.1.177-codexrip.7 ROLLBACK; then
  fail 'rollback accepted an expected-current Release body with the wrong digest'
fi
unset MOCK_BODY_DIGEST_OVERRIDE
MOCK_BODY_SOURCE_OVERRIDE=4444444444444444444444444444444444444444
if run_resolve rollback v0.1.177-codexrip.6 v0.1.177-codexrip.7 ROLLBACK; then
  fail 'rollback accepted an expected-current Release body with the wrong source'
fi
unset MOCK_BODY_SOURCE_OVERRIDE
MOCK_IMAGE_REVISION_OVERRIDE=4444444444444444444444444444444444444444
if run_resolve rollback v0.1.177-codexrip.6 v0.1.177-codexrip.7 ROLLBACK; then
  fail 'rollback accepted an expected-current image with the wrong OCI revision'
fi
unset MOCK_IMAGE_REVISION_OVERRIDE MOCK_OVERRIDE_TAG
if run_resolve rollback v0.1.177-codexrip.8 v0.1.177-codexrip.7 ROLLBACK; then
  fail 'rollback target newer than expected-current was accepted'
fi

run_apply deploy "$rollback_current_ref" '' 'cindy=true,true,false' ||
  fail 'valid deploy operation did not execute successfully'
assert_ssh_invocation "deploy $rollback_current_ref cindy=true,true,false"

run_apply rollback "$rollback_target_ref" "$rollback_current_ref" 'cindy=true,false,false' ||
  fail 'valid rollback operation did not execute successfully'
assert_ssh_invocation "rollback $rollback_target_ref from=$rollback_current_ref cindy=true,false,false"

if run_apply deploy "$rollback_current_ref" "$rollback_target_ref" 'cindy=true,true,false'; then
  fail 'deploy accepted an unexpected expected-current image'
fi
assert_ssh_not_invoked
if run_apply rollback "$rollback_target_ref" "$rollback_target_ref" 'cindy=true,false,false'; then
  fail 'rollback accepted a target equal to expected-current'
fi
assert_ssh_not_invoked
if run_apply rollback "$rollback_target_ref" "$rollback_current_ref" 'cindy=true,false,true'; then
  fail 'rollback accepted Image Studio without the Cindy capability catalog'
fi
assert_ssh_not_invoked
if run_apply invalid "$rollback_current_ref" '' 'cindy=true,true,false'; then
  fail 'immutable release operation accepted an unknown operation'
fi
assert_ssh_not_invoked

inspect=$'Name: image\nDigest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nManifest: amd64\nManifest: attestation'
digest=$(awk '$1 == "Digest:" && digest == "" {digest=$2} END {if (digest == "") exit 1; print digest}' <<<"$inspect")
[[ "$digest" == 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' ]] ||
  fail 'digest parser did not preserve the first index digest'

for input in operation release_tag expected_current_release_tag confirmation \
  cindy_balance_detection cindy_capability_catalog image_studio; do
  grep -Fq "      ${input}:" "$WORKFLOW" || fail "missing typed workflow input: $input"
done
grep -Fq '          - deploy' "$WORKFLOW" || fail 'workflow operation is missing deploy'
grep -Fq '          - rollback' "$WORKFLOW" || fail 'workflow operation is missing rollback'
[[ "$(grep -c '        type: boolean' "$WORKFLOW")" -ge 3 ]] ||
  fail 'Cindy rollout inputs must be typed booleans'
grep -Fq '        default: true' "$WORKFLOW" ||
  fail 'the balance phase must default on'
[[ "$(grep -c '        default: false' "$WORKFLOW")" -ge 2 ]] ||
  fail 'catalog and image phases must default off'
grep -Fq 'Image Studio requires the Cindy capability catalog in this release' "$WORKFLOW" ||
  fail 'workflow must reject image rollout without the catalog'
grep -Fq 'cindy_rollout=cindy=${CINDY_BALANCE_DETECTION},${CINDY_CAPABILITY_CATALOG},${IMAGE_STUDIO}' "$WORKFLOW" ||
  fail 'resolve must emit a canonical Cindy rollout tuple'
grep -Fq 'expected_current_image_ref=${expected_current_image_ref}' "$WORKFLOW" ||
  fail 'resolve must publish the immutable expected-current rollback image'
grep -Fq '[[ "$CONFIRMATION" == DEPLOY ]]' "$WORKFLOW" ||
  fail 'deploy must require the exact DEPLOY confirmation'
grep -Fq '[[ "$CONFIRMATION" == ROLLBACK ]]' "$WORKFLOW" ||
  fail 'rollback must require the exact ROLLBACK confirmation'
grep -Fq 'resolve_release_image "$EXPECTED_CURRENT_RELEASE_TAG"' "$WORKFLOW" ||
  fail 'rollback must independently resolve the expected-current release image'
grep -Fq 'isDraft,isPrerelease' "$WORKFLOW" ||
  fail 'release state resolution must inspect draft and prerelease flags'
grep -Fq '\tfalse\tfalse' "$WORKFLOW" ||
  fail 'deploy and rollback releases must be published and non-prerelease'
grep -Fq 'version_strictly_less "$RELEASE_TAG" "$EXPECTED_CURRENT_RELEASE_TAG"' "$WORKFLOW" ||
  fail 'rollback must require the target release to be older than expected-current'
grep -Fq 'remote_command="deploy ${IMAGE_REF} ${CINDY_ROLLOUT}"' "$WORKFLOW" ||
  fail 'deploy must pass the canonical rollout tuple to the forced command'
grep -Fq 'remote_command="rollback ${IMAGE_REF} from=${EXPECTED_CURRENT_IMAGE_REF} ${CINDY_ROLLOUT}"' "$WORKFLOW" ||
  fail 'rollback must bind the target to the exact expected-current image'

validate_rollout() {
  local value=$1
  [[ "$value" =~ ^cindy=(true|false),(true|false),(true|false)$ ]] || return 1
  [[ "${BASH_REMATCH[3]}" != true || "${BASH_REMATCH[2]}" == true ]]
}

validate_rollout 'cindy=true,false,false' || fail 'balance-only rollout was rejected'
validate_rollout 'cindy=true,true,false' || fail 'catalog rollout was rejected'
validate_rollout 'cindy=true,true,true' || fail 'image rollout was rejected'
if validate_rollout 'cindy=true,false,true'; then
  fail 'image rollout without catalog was accepted'
fi
for invalid in \
  'cindy=1,false,false' \
  'cindy=true,false,false extra' \
  'cindy=true,false,false;id' \
  'cindy=true, true,false'
do
  if validate_rollout "$invalid"; then
    fail "malformed rollout tuple was accepted: $invalid"
  fi
done

version_strictly_less() {
  local target=$1 current=$2 first
  [[ "$target" != "$current" ]] || return 1
  first=$(printf '%s\n%s\n' "$target" "$current" | LC_ALL=C sort -V | head -n 1)
  [[ "$first" == "$target" ]]
}

version_strictly_less 'v0.1.177-codexrip.6' 'v0.1.177-codexrip.7' ||
  fail 'valid rollback order was rejected'
version_strictly_less 'v0.1.176-codexrip.16' 'v0.1.177-codexrip.1' ||
  fail 'valid cross-upstream rollback order was rejected'
for invalid_pair in \
  'v0.1.177-codexrip.7 v0.1.177-codexrip.7' \
  'v0.1.177-codexrip.8 v0.1.177-codexrip.7' \
  'v0.1.178-codexrip.1 v0.1.177-codexrip.9'; do
  read -r target current <<<"$invalid_pair"
  if version_strictly_less "$target" "$current"; then
    fail "unsafe rollback order was accepted: $invalid_pair"
  fi
done

printf 'PASS: production deploy, rollback, digest, and Cindy rollout resolution\n'

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
CINDY_HEALTH=true \
CINDY_CAPABILITY_CATALOG=false \
  CINDY_SEARCH=false \
  IMAGE_STUDIO=false \
  CINDY_RESPONSES_IMAGE_BRIDGE=false \
  OVERDRAFT=false \
  INTERRUPT_BUSINESS=false \
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
    platform_v1=false
    ;;
  *:0.1.177-codexrip.7*)
    tag=v0.1.177-codexrip.7
    digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    revision=2222222222222222222222222222222222222222
    platform_v1=true
    ;;
  *:0.1.177-codexrip.8*)
    tag=v0.1.177-codexrip.8
    digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
    revision=3333333333333333333333333333333333333333
    platform_v1=true
    ;;
  *) exit 3 ;;
esac
if [[ "${MOCK_OVERRIDE_TAG:-}" == "$tag" ]]; then
  digest=${MOCK_REGISTRY_DIGEST_OVERRIDE:-$digest}
  revision=${MOCK_IMAGE_REVISION_OVERRIDE:-$revision}
  platform_v1=${MOCK_IMAGE_PLATFORM_V1_OVERRIDE:-$platform_v1}
fi
if [[ " $* " == *'io.github.htexplicit.cindy-platform-v1'* ]]; then
  printf '%s\n' "$platform_v1"
elif [[ " $* " == *' --format '* ]]; then
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
  local operation=$1 tag=$2 expected_current=$3 confirmation=$4 interrupt=${5-false}
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
    MOCK_IMAGE_PLATFORM_V1_OVERRIDE="${MOCK_IMAGE_PLATFORM_V1_OVERRIDE:-}" \
    OPERATION="$operation" \
    RELEASE_TAG="$tag" \
    EXPECTED_CURRENT_RELEASE_TAG="$expected_current" \
    CONFIRMATION="$confirmation" \
    CINDY_HEALTH=true \
    CINDY_CAPABILITY_CATALOG=false \
    CINDY_SEARCH=false \
    IMAGE_STUDIO=false \
    CINDY_RESPONSES_IMAGE_BRIDGE=false \
    OVERDRAFT=false \
    INTERRUPT_BUSINESS="$interrupt" \
    GITHUB_OUTPUT="$tmpdir/github-output" \
    bash "$resolve_script" >"$tmpdir/resolve-output" 2>&1
}

run_apply() {
  local operation=$1 image_ref=$2 expected_current=$3 rollout=$4 overdraft=${5-false} runtime_spec=${6-runtime=explicit} maintenance_spec=${7-}
  : >"$tmpdir/ssh-capture"
  : >"$tmpdir/ssh-calls"
  : >"$tmpdir/apply-output"
  HOME="$tmpdir/home" \
    PATH="$tmpdir/bin:$PATH" \
    SSH_CAPTURE="$tmpdir/ssh-capture" \
    SSH_CALLS="$tmpdir/ssh-calls" \
    VPS_HOST=production.example.invalid \
    VPS_PORT="${MOCK_VPS_PORT:-2222}" \
    VPS_USER=deployer \
    OPERATION="$operation" \
    IMAGE_REF="$image_ref" \
    EXPECTED_CURRENT_IMAGE_REF="$expected_current" \
    CINDY_ROLLOUT="$rollout" \
    OVERDRAFT="$overdraft" \
    MAINTENANCE_SPEC="$maintenance_spec" \
    RUNTIME_SPEC="$runtime_spec" \
    bash "$apply_script" >"$tmpdir/apply-output" 2>&1
}

assert_ssh_invocation() {
  local expected_remote_command=$1 index line
  local -a actual expected
  actual=()
  while IFS= read -r line; do
    actual+=("$line")
  done <"$tmpdir/ssh-capture"
  expected=(
    -F /dev/null
    -i "$tmpdir/home/.ssh/sub2api_deploy"
    -o BatchMode=yes
    -o IdentitiesOnly=yes
    -o ServerAliveInterval=30
    -o ServerAliveCountMax=3
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

if ! run_resolve deploy v0.1.177-codexrip.7 '' DEPLOY; then
  cat "$tmpdir/resolve-output" >&2
  fail 'valid deploy resolution did not execute successfully'
fi
grep -Fq "image_ref=$rollback_current_ref" "$tmpdir/github-output" ||
  fail 'deploy did not emit the Release-bound immutable image'
grep -Fxq 'expected_current_image_ref=' "$tmpdir/github-output" ||
  fail 'deploy unexpectedly emitted an expected-current rollback image'

run_resolve deploy v0.1.177-codexrip.7 '' DEPLOY true ||
  fail 'deploy resolution rejected the explicit interruption mode'
grep -Fxq 'maintenance_spec=maintenance=interrupt' "$tmpdir/github-output" ||
  fail 'deploy resolution did not emit the explicit interruption spec'

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
MOCK_IMAGE_PLATFORM_V1_OVERRIDE=false
if run_resolve deploy v0.1.177-codexrip.7 '' DEPLOY; then
  fail 'immutable image without the Cindy platform-v1 capability label was accepted'
fi
unset MOCK_IMAGE_PLATFORM_V1_OVERRIDE
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
unset MOCK_IMAGE_REVISION_OVERRIDE
MOCK_IMAGE_PLATFORM_V1_OVERRIDE=false
if run_resolve rollback v0.1.177-codexrip.6 v0.1.177-codexrip.7 ROLLBACK; then
  fail 'rollback accepted an expected-current image without the platform-v1 capability label'
fi
unset MOCK_IMAGE_PLATFORM_V1_OVERRIDE MOCK_OVERRIDE_TAG
if run_resolve rollback v0.1.177-codexrip.8 v0.1.177-codexrip.7 ROLLBACK; then
  fail 'rollback target newer than expected-current was accepted'
fi
if run_resolve rollback v0.1.177-codexrip.6 v0.1.177-codexrip.7 ROLLBACK true; then
  fail 'rollback accepted the business interruption mode'
fi

run_apply deploy "$rollback_current_ref" '' 'cindy=true,true,true,false,false' ||
  fail 'valid deploy operation did not execute successfully'
assert_ssh_invocation "deploy $rollback_current_ref cindy=true,true,true,false,false overdraft=false"

run_apply rollback "$rollback_target_ref" "$rollback_current_ref" 'cindy=true,false,false,false,false' ||
  fail 'valid rollback operation did not execute successfully'
assert_ssh_invocation "rollback $rollback_target_ref from=$rollback_current_ref cindy=true,false,false,false,false overdraft=false"

run_apply deploy "$rollback_current_ref" '' 'cindy=true,true,true,false,false' true ||
  fail 'valid overdraft-enabled deploy operation did not execute successfully'
assert_ssh_invocation "deploy $rollback_current_ref cindy=true,true,true,false,false overdraft=true"

run_apply deploy "$rollback_current_ref" '' 'cindy=true,true,true,false,false' true runtime=explicit maintenance=interrupt ||
  fail 'valid maintenance-interrupt deploy operation did not execute successfully'
assert_ssh_invocation "deploy $rollback_current_ref cindy=true,true,true,false,false overdraft=true maintenance=interrupt"

run_apply deploy "$rollback_current_ref" '' '' '' runtime=preserve ||
  fail 'valid runtime-preserve deploy operation did not execute successfully'
assert_ssh_invocation "deploy $rollback_current_ref runtime=preserve"

if run_apply deploy "$rollback_current_ref" "$rollback_target_ref" 'cindy=true,true,true,false,false'; then
  fail 'deploy accepted an unexpected expected-current image'
fi
assert_ssh_not_invoked
if run_apply rollback "$rollback_target_ref" "$rollback_target_ref" 'cindy=true,false,false,false,false'; then
  fail 'rollback accepted a target equal to expected-current'
fi
assert_ssh_not_invoked
MOCK_VPS_PORT=not-a-port
if run_apply deploy "$rollback_current_ref" '' 'cindy=true,true,true,false,false'; then
  fail 'deploy accepted a malformed SSH port'
fi
unset MOCK_VPS_PORT
assert_ssh_not_invoked
if run_apply deploy 'ghcr.io/htexplicit/sub2api:latest' '' 'cindy=true,true,true,false,false'; then
  fail 'deploy accepted a mutable image reference'
fi
assert_ssh_not_invoked
if run_apply deploy "$rollback_current_ref" '' 'cindy=true,false,true'; then
  fail 'deploy accepted a malformed Cindy rollout'
fi
assert_ssh_not_invoked
if run_apply rollback "$rollback_target_ref" '' 'cindy=true,false,false,false,false'; then
  fail 'rollback accepted an empty expected-current image'
fi
assert_ssh_not_invoked
if run_apply invalid "$rollback_current_ref" '' 'cindy=true,true,true,false,false'; then
  fail 'immutable release operation accepted an unknown operation'
fi
assert_ssh_not_invoked
if run_apply deploy "$rollback_current_ref" '' 'cindy=true,true,true,false,false' 1; then
  fail 'immutable release operation accepted a non-boolean overdraft flag'
fi
assert_ssh_not_invoked

inspect=$'Name: image\nDigest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nManifest: amd64\nManifest: attestation'
digest=$(awk '$1 == "Digest:" && digest == "" {digest=$2} END {if (digest == "") exit 1; print digest}' <<<"$inspect")
[[ "$digest" == 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' ]] ||
  fail 'digest parser did not preserve the first index digest'

for input in operation release_tag expected_current_release_tag confirmation \
  cindy_health cindy_capability_catalog cindy_search image_studio \
  cindy_responses_image_bridge overdraft interrupt_business; do
  grep -Fq "      ${input}:" "$WORKFLOW" || fail "missing typed workflow input: $input"
done
grep -Fq '          - deploy' "$WORKFLOW" || fail 'workflow operation is missing deploy'
grep -Fq '          - deploy-preserve' "$WORKFLOW" || fail 'workflow operation is missing deploy-preserve'
grep -Fq '          - rollback' "$WORKFLOW" || fail 'workflow operation is missing rollback'
[[ "$(grep -c '        type: boolean' "$WORKFLOW")" -ge 5 ]] ||
  fail 'Cindy rollout inputs must be typed booleans'
[[ "$(grep -c '        default: false' "$WORKFLOW")" -ge 5 ]] ||
  fail 'all staged rollout phases must default off'
if grep -Fq 'requires the Cindy capability catalog' "$WORKFLOW"; then
  fail 'independent Search, Image Studio, or Responses-image flags still depend on the catalog'
fi
grep -Fq 'cindy_rollout=cindy=${CINDY_HEALTH},${CINDY_CAPABILITY_CATALOG},${CINDY_SEARCH},${IMAGE_STUDIO},${CINDY_RESPONSES_IMAGE_BRIDGE}' "$WORKFLOW" ||
  fail 'resolve must emit a canonical Cindy rollout tuple'
grep -Fq 'io.github.htexplicit.cindy-platform-v1' "$WORKFLOW" ||
  fail 'resolve must require the platform capability label from the immutable image'
grep -Fq 'expected_current_image_ref=${expected_current_image_ref}' "$WORKFLOW" ||
  fail 'resolve must publish the immutable expected-current rollback image'
grep -Fq '[[ "$CONFIRMATION" == DEPLOY ]]' "$WORKFLOW" ||
  fail 'deploy must require the exact DEPLOY confirmation'
grep -Fq '[[ "$CONFIRMATION" == ROLLBACK ]]' "$WORKFLOW" ||
  fail 'rollback must require the exact ROLLBACK confirmation'
grep -Fq 'resolve_release_image "$EXPECTED_CURRENT_RELEASE_TAG"' "$WORKFLOW" ||
  fail 'rollback must independently resolve the expected-current release image'
grep -Fq 'resolve_release_image "$RELEASE_TAG" rollback-target' "$WORKFLOW" ||
  fail 'rollback must resolve the target using the legacy-aware image role'
grep -Fq 'resolve_release_image "$EXPECTED_CURRENT_RELEASE_TAG" platform' "$WORKFLOW" ||
  fail 'rollback must require the expected-current image to be platform capable'
grep -Fq 'isDraft,isPrerelease' "$WORKFLOW" ||
  fail 'release state resolution must inspect draft and prerelease flags'
grep -Fq '\tfalse\tfalse' "$WORKFLOW" ||
  fail 'deploy and rollback releases must be published and non-prerelease'
grep -Fq 'version_strictly_less "$RELEASE_TAG" "$EXPECTED_CURRENT_RELEASE_TAG"' "$WORKFLOW" ||
  fail 'rollback must require the target release to be older than expected-current'
grep -Fq 'remote_command="deploy ${IMAGE_REF} ${CINDY_ROLLOUT} overdraft=${OVERDRAFT}"' "$WORKFLOW" ||
  fail 'deploy must pass the canonical rollout tuple to the forced command'
grep -Fq 'maintenance_spec=maintenance=interrupt' "$WORKFLOW" ||
  fail 'explicit business interruption must resolve to a fixed maintenance spec'
grep -Fq 'remote_command+=" $MAINTENANCE_SPEC"' "$WORKFLOW" ||
  fail 'explicit business interruption must be appended only after validation'
grep -Fq '[[ "$OPERATION" == deploy || -z "$MAINTENANCE_SPEC" ]]' "$WORKFLOW" ||
  fail 'rollback must reject the maintenance interrupt spec'
grep -Fq '[[ "$INTERRUPT_BUSINESS" == false ]]' "$WORKFLOW" ||
  fail 'preserve/rollback paths must reject business interruption'
grep -Fq 'remote_command="deploy ${IMAGE_REF} runtime=preserve"' "$WORKFLOW" ||
  fail 'automatic deploy must preserve the locked runtime tuple'
grep -Fq 'remote_command="rollback ${IMAGE_REF} from=${EXPECTED_CURRENT_IMAGE_REF} ${CINDY_ROLLOUT} overdraft=${OVERDRAFT}"' "$WORKFLOW" ||
  fail 'rollback must bind the target to the exact expected-current image'

validate_rollout() {
  local value=$1
  [[ "$value" =~ ^cindy=(true|false),(true|false),(true|false),(true|false),(true|false)$ ]]
}

validate_rollout 'cindy=false,false,false,false,false' || fail 'platform-only rollout was rejected'
validate_rollout 'cindy=false,true,false,false,false' || fail 'catalog rollout was rejected'
validate_rollout 'cindy=true,true,false,false,false' || fail 'health rollout was rejected'
validate_rollout 'cindy=true,true,true,false,false' || fail 'search rollout was rejected'
validate_rollout 'cindy=true,true,true,true,false' || fail 'Image Studio rollout was rejected'
validate_rollout 'cindy=true,true,true,true,true' || fail 'Responses-image rollout was rejected'
validate_rollout 'cindy=false,false,true,true,true' || fail 'independent feature rollout was rejected'
for invalid in \
  'cindy=1,false,false,false,false' \
  'cindy=true,false,false,false,false extra' \
  'cindy=true,false,false,false,false;id' \
  'cindy=true, true,false,false,false' \
  'cindy=true,false,false'
do
  if validate_rollout "$invalid"; then
    fail "malformed rollout tuple was accepted: $invalid"
  fi
done

grep -Fq 'io.github.htexplicit.cindy-platform-v1=true' "$DOWNSTREAM_RELEASE_WORKFLOW" ||
  fail 'downstream image must declare the platform capability label'
grep -Fq "!contains(github.ref_name, '-codexrip.')" "$GENERIC_RELEASE_WORKFLOW" ||
  fail 'generic Release workflow must skip downstream codexrip tags'

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

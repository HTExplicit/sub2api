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
if grep -Fq '{print $2; exit}' "$WORKFLOW"; then
  fail 'production digest resolution closes the buildx pipe before EOF'
fi

inspect=$'Name: image\nDigest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nManifest: amd64\nManifest: attestation'
digest=$(awk '$1 == "Digest:" && digest == "" {digest=$2} END {if (digest == "") exit 1; print digest}' <<<"$inspect")
[[ "$digest" == 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' ]] ||
  fail 'digest parser did not preserve the first index digest'

for input in cindy_balance_detection cindy_capability_catalog cindy_image_studio; do
  grep -Fq "      ${input}:" "$WORKFLOW" || fail "missing typed workflow input: $input"
done
[[ "$(grep -c '        type: boolean' "$WORKFLOW")" -ge 3 ]] ||
  fail 'Cindy rollout inputs must be typed booleans'
grep -Fq '        default: true' "$WORKFLOW" ||
  fail 'the balance phase must default on'
[[ "$(grep -c '        default: false' "$WORKFLOW")" -ge 2 ]] ||
  fail 'catalog and image phases must default off'
grep -Fq 'Cindy Image Studio requires the Cindy capability catalog' "$WORKFLOW" ||
  fail 'workflow must reject image rollout without the catalog'
grep -Fq 'cindy_rollout=cindy=${CINDY_BALANCE_DETECTION},${CINDY_CAPABILITY_CATALOG},${CINDY_IMAGE_STUDIO}' "$WORKFLOW" ||
  fail 'resolve must emit a canonical Cindy rollout tuple'
grep -Fq '"deploy ${IMAGE_REF} ${CINDY_ROLLOUT}"' "$WORKFLOW" ||
  fail 'deployment must pass the canonical rollout tuple to the forced command'

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

printf 'PASS: production deploy digest and Cindy rollout resolution\n'

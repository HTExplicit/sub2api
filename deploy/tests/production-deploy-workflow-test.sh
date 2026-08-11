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

printf 'PASS: production deploy digest resolution\n'

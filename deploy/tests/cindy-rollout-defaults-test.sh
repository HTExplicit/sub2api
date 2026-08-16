#!/bin/sh
set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

assert_line() {
  file=$1
  expected=$2
  if ! grep -Fqx -- "$expected" "$root_dir/$file"; then
    echo "missing rollout default in $file: $expected" >&2
    exit 1
  fi
}

assert_line "deploy/.env.example" "GATEWAY_CINDY_BALANCE_DETECTION_ENABLED=true"
assert_line "deploy/.env.example" "GATEWAY_CINDY_CAPABILITY_CATALOG_ENABLED=false"
assert_line "deploy/.env.example" "GATEWAY_IMAGE_STUDIO_ENABLED=false"
assert_line "deploy/.env.example" "# GATEWAY_CINDY_IMAGE_STUDIO_ENABLED="

for compose_file in \
  deploy/docker-compose.yml \
  deploy/docker-compose.local.yml \
  deploy/docker-compose.dev.yml \
  deploy/docker-compose.standalone.yml
do
  assert_line "$compose_file" '      - GATEWAY_CINDY_BALANCE_DETECTION_ENABLED=${GATEWAY_CINDY_BALANCE_DETECTION_ENABLED:-true}'
  assert_line "$compose_file" '      - GATEWAY_CINDY_CAPABILITY_CATALOG_ENABLED=${GATEWAY_CINDY_CAPABILITY_CATALOG_ENABLED:-false}'
  assert_line "$compose_file" '      - GATEWAY_IMAGE_STUDIO_ENABLED=${GATEWAY_IMAGE_STUDIO_ENABLED:-}'
  assert_line "$compose_file" '      - GATEWAY_CINDY_IMAGE_STUDIO_ENABLED=${GATEWAY_CINDY_IMAGE_STUDIO_ENABLED:-}'
done

echo "Cindy rollout defaults enable only the balance phase with the generic image flag off"

#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"
plugin_build_dir=$(mktemp -d)

for module in plugins/*; do
  [[ -f "$module/go.mod" ]] || continue
  module_name=$(basename "$module")
  (
    cd "$module"
    go test ./... -count=1
    go build -trimpath -o "$plugin_build_dir/$module_name" ./cmd/plugin
  )
done

cd backend
go run ./cmd/package-plugin -generate-key "$plugin_build_dir/publisher.key" > "$plugin_build_dir/publisher.json"
go run ./cmd/package-plugin \
  -source ../plugins/codex-runtime \
  -binary "$plugin_build_dir/codex-runtime" \
  -platform linux-amd64 \
  -tested-host-version 0.2.7 \
  -output "$plugin_build_dir/codex-runtime.s2plugin" \
  -bundle-lock "$plugin_build_dir/lock.json" \
  -signing-key-file "$plugin_build_dir/publisher.key" \
  -key-id codexrip-plugins-test

go run ./cmd/package-plugin \
  -source ../plugins/model-policy \
  -binary "$plugin_build_dir/model-policy" \
  -platform linux-amd64 \
  -tested-host-version 0.2.7 \
  -output "$plugin_build_dir/model-policy.s2plugin" \
  -bundle-lock "$plugin_build_dir/lock.json" \
  -default-enabled \
  -signing-key-file "$plugin_build_dir/publisher.key" \
  -key-id codexrip-plugins-test

export SUB2API_EXTENSION_TEST_BINARY="$plugin_build_dir/codex-runtime"
export SUB2API_MODEL_POLICY_TEST_BINARY="$plugin_build_dir/model-policy"
export SUB2API_EXTENSION_TEST_BUNDLE="$plugin_build_dir/lock.json"
export SUB2API_EXTENSION_TEST_PUBLIC_KEY
SUB2API_EXTENSION_TEST_PUBLIC_KEY=$(jq -r .public_key "$plugin_build_dir/publisher.json")
go test ./internal/service -run '^(TestExtensionRuntimeUsesOfficialProcessAndHostBroker|TestCatalogExtensionRuntimeUsesIndependentProcess|TestFirstPartyBundleContainsMatchingSignedDomainPackages)$' -count=1

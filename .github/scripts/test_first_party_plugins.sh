#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"
plugin_build_dir=$(mktemp -d)
node backend/pkg/extensionapi/ui/build.mjs

for module in plugins/*; do
  [[ -f "$module/go.mod" ]] || continue
  (
    cd "$module"
    go test ./... -count=1
  )
done

cd backend
go build -trimpath -o "$plugin_build_dir/package-plugin" ./cmd/package-plugin
"$plugin_build_dir/package-plugin" -build-binaries -bundle-source ../plugins/bundle.source.json -binary-dir "$plugin_build_dir/binaries" -platform linux-amd64
"$plugin_build_dir/package-plugin" -generate-key "$plugin_build_dir/publisher.key" > "$plugin_build_dir/publisher.json"
"$plugin_build_dir/package-plugin" \
  -bundle-source ../plugins/bundle.source.json \
  -binary-dir "$plugin_build_dir/binaries" \
  -platform linux-amd64 \
  -tested-host-version 0.2.8-codexrip.2 \
  -output "$plugin_build_dir/bundle" \
  -signing-key-file "$plugin_build_dir/publisher.key" \
  -key-id codexrip-plugins-test

export SUB2API_EXTENSION_TEST_BINARY="$plugin_build_dir/binaries/codex-runtime"
export SUB2API_EXTENSION_TEST_BUNDLE="$plugin_build_dir/bundle/lock.json"
export SUB2API_PROMPT_SKILLS_TEST_BINARY="$plugin_build_dir/binaries/prompt-skills"
export SUB2API_ACCOUNT_TOOLS_TEST_BINARY="$plugin_build_dir/binaries/account-tools"
export SUB2API_CINDY_PROVIDER_TEST_BINARY="$plugin_build_dir/binaries/cindy-provider"
export SUB2API_IMAGE_TOOLS_TEST_BINARY="$plugin_build_dir/binaries/image-tools"
export SUB2API_ADMIN_OBSERVABILITY_TEST_BINARY="$plugin_build_dir/binaries/admin-observability"
export SUB2API_EXTENSION_TEST_PUBLIC_KEY
SUB2API_EXTENSION_TEST_PUBLIC_KEY=$(jq -r .public_key "$plugin_build_dir/publisher.json")
go test ./internal/service -run '^(TestExtensionRuntimeUsesOfficialProcessAndHostBroker|TestPromptExtensionRuntimeUsesIndependentProcess|TestAccountToolsExtensionRuntimeUsesIndependentProcess|TestCindyProviderExtensionRuntimeUsesIndependentProcess|TestImageToolsExtensionRuntimeUsesIndependentProcess|TestObservabilityExtensionRuntimeUsesScopedMetricsBroker|TestFirstPartyBundleContainsMatchingSignedDomainPackages)$' -count=1

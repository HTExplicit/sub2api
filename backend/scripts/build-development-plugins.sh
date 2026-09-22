#!/usr/bin/env bash
set -euo pipefail

plugin_output=${1:?output directory required}
host_version=${2:?host version required}
runtime_platform=${3:?runtime platform required}
source_revision=${4:-}
[[ -z ${SUB2API_PLUGIN_SIGNING_KEY:-} ]] || { printf '%s\n' 'Development builds cannot use the release signing secret' >&2; exit 1; }
plugin_build=$(mktemp -d)
trap 'rm -rf "$plugin_build"' EXIT

go build -o "$plugin_build/package-plugin" ./cmd/package-plugin
"$plugin_build/package-plugin" -build-binaries -bundle-source ../plugins/bundle.source.json -binary-dir "$plugin_build/binaries" -platform "$runtime_platform"
"$plugin_build/package-plugin" -generate-key "$plugin_build/development.key" >"$plugin_build/public.json"
source_args=()
if [[ $source_revision =~ ^[a-f0-9]{40}$ ]]; then source_args=(-source-revision "$source_revision"); fi
"$plugin_build/package-plugin" -bundle-source ../plugins/bundle.source.json -binary-dir "$plugin_build/binaries" \
  -platform "$runtime_platform" -tested-host-version "$host_version" -output "$plugin_output" \
  -signing-key-file "$plugin_build/development.key" -key-id codexrip-development "${source_args[@]}"
"$plugin_build/package-plugin" -verify-bundle "$plugin_output/lock.json" -tested-host-version "$host_version" -platform "$runtime_platform" -development

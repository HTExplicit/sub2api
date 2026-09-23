package main

import (
	policy "github.com/HTExplicit/sub2api-plugins/promptskills/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var version = "0.2.10"

func main() {
	extensionv1.Serve(extensionv1.Definition{ID: "codexrip.prompt-skills", Version: version, Capabilities: []string{extensionv1.CapabilityRequest, extensionv1.CapabilityAdmin}}, policy.New())
}

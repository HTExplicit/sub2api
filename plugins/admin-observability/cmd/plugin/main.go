package main

import (
	"github.com/HTExplicit/sub2api-plugins/adminobservability/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var version = "0.2.11"

func main() {
	extensionv1.Serve(extensionv1.Definition{ID: "codexrip.admin-observability", Version: version, Capabilities: []string{extensionv1.CapabilityObservability, extensionv1.CapabilityAdmin, extensionv1.CapabilityUI}}, policy.New())
}

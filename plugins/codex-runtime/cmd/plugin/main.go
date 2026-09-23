package main

import (
	"github.com/HTExplicit/sub2api-plugins/codexruntime/internal/tickets"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var version = "0.2.8"

func main() {
	extensionv1.Serve(extensionv1.Definition{ID: "codexrip.codex-runtime", Version: version,
		Capabilities: []string{extensionv1.CapabilityAdmin, extensionv1.CapabilityScheduling, extensionv1.CapabilityJobs, extensionv1.CapabilityRequest, extensionv1.CapabilityCredentials, extensionv1.CapabilityRecovery}}, tickets.NewModule())
}

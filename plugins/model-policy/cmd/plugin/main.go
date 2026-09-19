package main

import (
	catalog "github.com/HTExplicit/sub2api-plugins/modelpolicy/catalog"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func main() {
	extensionv1.Serve(extensionv1.Definition{ID: "codexrip.model-policy", Version: "0.2.7", Capabilities: []string{extensionv1.CapabilityCatalog, extensionv1.CapabilityAdmin}}, catalog.New())
}

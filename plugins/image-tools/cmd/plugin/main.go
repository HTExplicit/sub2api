package main

import (
	"github.com/HTExplicit/sub2api-plugins/imagetools/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func main() {
	extensionv1.Serve(extensionv1.Definition{ID: "codexrip.image-tools", Version: "0.2.7", Capabilities: []string{extensionv1.CapabilityRequest, extensionv1.CapabilityAdmin}}, policy.New())
}

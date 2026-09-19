package main

import (
	policy "github.com/HTExplicit/sub2api-plugins/accounttools/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func main() {
	extensionv1.Serve(extensionv1.Definition{ID: "codexrip.account-tools", Version: "0.2.7", Capabilities: []string{extensionv1.CapabilityAdmin, extensionv1.CapabilityUI}}, policy.New())
}

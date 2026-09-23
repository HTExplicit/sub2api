package profile

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestIdentityPolicyPreservesDeterminismAndProtocolPairing(t *testing.T) {
	first := deriveCodexClientIdentity("1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55")
	if first != deriveCodexClientIdentity("1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55") || !first.valid() {
		t.Fatal("stable profile changed")
	}
	ua := first.UserAgent("0.150.0")
	if !strings.HasPrefix(ua, "codex-tui/0.150.0 ") || !strings.HasSuffix(ua, "(codex-tui; 0.150.0)") || codexSandboxForUserAgent(ua) != first.Sandbox {
		t.Fatal("inconsistent profile and agent")
	}
	bad := first
	bad.Terminal = "terminal\r\nx-header: value"
	if bad.valid() {
		t.Fatal("header injection accepted")
	}
	query, _ := json.Marshal(extensionv1.CodexIdentityQuery{Profile: extensionv1.CodexClientProfile(first), Version: "0.150.0\r\nx: value"})
	_, err := Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.identity.agent", Payload: query})
	if err == nil {
		t.Fatal("invalid version accepted")
	}
}

func TestIdentityCapturedDefaultPreservesExplicitLegacyProfiles(t *testing.T) {
	query, _ := json.Marshal(extensionv1.CodexIdentityQuery{Seed: "account-seed"})
	result, err := Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.identity.derive", Payload: query})
	if err != nil {
		t.Fatal(err)
	}
	var value extensionv1.CodexIdentityResult
	if json.Unmarshal(result.Payload, &value) != nil || value.Profile.Version != 2 || value.Profile.Source != "reference_derived_windows_cli" {
		t.Fatal("new account did not receive explicit captured-reference profile")
	}
	profile := codexClientIdentity(value.Profile)
	if !profile.valid() || !strings.HasPrefix(profile.UserAgent("0.145.0"), "codex_exec/0.156.0 ") {
		t.Fatal("captured reference version changed")
	}
	legacy := deriveCodexClientIdentity("old-seed")
	query, _ = json.Marshal(extensionv1.CodexIdentityQuery{Seed: "old-seed", Profile: extensionv1.CodexClientProfile(legacy), Version: "0.156.0"})
	result, err = Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.identity.plan", Payload: query})
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(result.Payload, &value) != nil || value.Profile.Version != 1 || value.Profile.OSType != legacy.OSType || !strings.HasPrefix(value.UserAgent, "codex-tui/0.156.0 ") {
		t.Fatal("legacy profile was silently migrated")
	}
}

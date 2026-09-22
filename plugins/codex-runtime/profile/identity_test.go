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

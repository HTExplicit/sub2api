package profile

import (
	"errors"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func TestIdentityProfilePreservesDeterminismAndProtocolPairing(t *testing.T) {
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
	if _, err := UserAgent(extensionv1.CodexClientProfile(first), "0.150.0\r\nx: value"); !errors.Is(err, ErrInvalidVersion) {
		t.Fatal("invalid version accepted")
	}
	if _, err := UserAgent(extensionv1.CodexClientProfile(bad), "0.150.0"); err == nil {
		t.Fatal("invalid profile accepted")
	}
	typed := Derive("1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55")
	if got, err := UserAgent(typed, "0.150.0"); err != nil || got != ua || !Valid(typed) || SandboxForUserAgent(ua) != first.Sandbox {
		t.Fatal("typed calls disagree with the profile")
	}
}

package service

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fingerprintViewTestPolicy(t *testing.T, enabled bool) {
	t.Helper()
	fingerprintPolicyTestState(t)
	previous := codexForceCLI.Load()
	t.Cleanup(func() { codexForceCLI.Store(previous) })
	codexForceCLI.Store(false)
	publishedCodexFingerprintPolicy.Store(fingerprintTestPolicy(enabled, "0.162.0"))
}

func fingerprintViewStoredAccount(mode string) *Account {
	account := fingerprintTestAccount(mode)
	device := codexClientIdentity{Version: 1, OSType: "Windows", OSVersion: "10.0.26100", Arch: "x86_64", Terminal: "vscode/1.104.0", Sandbox: "windows_sandbox"}
	account.Extra[CodexClientIdentityExtraKey] = codexClientIdentityExtraValue(device, time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC))
	return account
}

func TestCodexFingerprintAccountViewReadsStoredAndDerivedIdentitiesWithoutWriting(t *testing.T) {
	fingerprintViewTestPolicy(t, true)
	for _, stored := range []bool{true, false} {
		account := fingerprintViewStoredAccount("device")
		if !stored {
			delete(account.Extra, CodexClientIdentityExtraKey)
		}
		before, err := json.Marshal(account.Extra)
		require.NoError(t, err)
		view := DescribeCodexFingerprintAccount(account, account)
		require.NotNil(t, view.DeviceIdentity)
		require.Equal(t, stored, view.Identity.IdentityPersisted)
		require.Equal(t, view.DeviceIdentity.OSType, *view.EffectiveDevice.OSType)
		require.Equal(t, view.DeviceIdentity.Arch, *view.EffectiveDevice.Arch)
		require.Equal(t, view.DeviceIdentity.Terminal, *view.EffectiveDevice.Terminal)
		if stored {
			require.Equal(t, "2026-10-08T08:00:00Z", view.DeviceIdentity.GeneratedAt)
		} else {
			require.Empty(t, view.DeviceIdentity.GeneratedAt, "a read must not manufacture a generation time")
		}
		after, err := json.Marshal(account.Extra)
		require.NoError(t, err)
		require.JSONEq(t, string(before), string(after))
	}
}

func TestCodexFingerprintAccountViewMissingIdentityAndMasterOff(t *testing.T) {
	fingerprintViewTestPolicy(t, true)
	account := fingerprintTestAccount("full")
	delete(account.Extra, codexFingerprintSeedExtraKey)
	view := DescribeCodexFingerprintAccount(account, account)
	require.Nil(t, view.DeviceIdentity)
	require.False(t, view.Identity.IdentityPersisted)
	require.Equal(t, "seed_missing", view.Identity.FingerprintReason)
	require.Equal(t, "passthrough", view.IdentifierPolicy.InstallationID.Behavior)
	require.Nil(t, view.IdentifierPolicy.InstallationID.Value)
	account = fingerprintViewStoredAccount("full")
	publishedCodexFingerprintPolicy.Store(fingerprintTestPolicy(false, "0.162.0"))
	view = DescribeCodexFingerprintAccount(account, account)
	require.NotNil(t, view.DeviceIdentity, "disabled simulation must keep the saved identity visible")
	require.Nil(t, view.EffectiveDevice, "protocol fallback is not the real client device")
	require.Equal(t, "protocol_fallback", view.Identity.IdentitySource)
	require.Equal(t, "simulation_disabled", view.Identity.FingerprintReason)
	require.Equal(t, "passthrough", view.IdentifierPolicy.SessionID.Behavior)
	require.Nil(t, view.IdentifierPolicy.ThreadID.Value)
}

func TestCodexFingerprintAccountViewCustomUAAndUnknownDeviceFields(t *testing.T) {
	fingerprintViewTestPolicy(t, true)
	account := fingerprintViewStoredAccount("device")
	account.Credentials = map[string]any{"user_agent": "codex-tui/0.150.0 (Mac OS X 14.0; arm64) iTerm.app/3.5.15 (codex-tui; 0.150.0)"}
	view := DescribeCodexFingerprintAccount(account, account)
	require.Equal(t, "override_ua", view.Identity.IdentitySource)
	require.Equal(t, "Windows", view.DeviceIdentity.OSType)
	require.Equal(t, "Mac OS X", *view.EffectiveDevice.OSType)
	require.Equal(t, "14.0", *view.EffectiveDevice.OSVersion)
	require.Equal(t, "arm64", *view.EffectiveDevice.Arch)
	require.Equal(t, "iTerm.app/3.5.15", *view.EffectiveDevice.Terminal)
	require.Equal(t, "seatbelt", *view.EffectiveDevice.PlatformSandbox)
	require.Contains(t, view.Identity.UserAgent, "0.162.0")
	account.Credentials["user_agent"] = "codex-tui/0.150.0"
	view = DescribeCodexFingerprintAccount(account, account)
	require.Equal(t, "codex-tui/0.162.0", view.Identity.UserAgent)
	require.NotNil(t, view.EffectiveDevice)
	require.Nil(t, view.EffectiveDevice.OSType)
	require.Nil(t, view.EffectiveDevice.Terminal)
	codexForceCLI.Store(true)
	view = DescribeCodexFingerprintAccount(account, account)
	require.Equal(t, "account", view.Identity.IdentitySource)
	require.Equal(t, "Windows", *view.EffectiveDevice.OSType)
}

func TestCodexFingerprintAccountViewPoliciesMatchSharedForwardingAndParentSource(t *testing.T) {
	fingerprintViewTestPolicy(t, true)
	parent := fingerprintViewStoredAccount("device")
	parent.Extra["openai_device_id"] = "43ef16f3-2c23-4ca0-b67d-7c3f0189aca4"
	child := fingerprintTestAccount("device")
	child.ID = 84
	child.ParentAccountID = &parent.ID
	for _, mode := range []string{"off", "device", "session", "full"} {
		t.Run(mode, func(t *testing.T) {
			child.Extra[codexFingerprintModeExtraKey] = mode
			view := DescribeCodexFingerprintAccount(child, parent)
			require.Equal(t, parent.ID, view.Identity.IdentityAccountID)
			require.Equal(t, "Windows", view.DeviceIdentity.OSType)
			require.Equal(t, mode, view.Mode)
			headers := http.Header{"Session-Id": {"real-session"}, "Thread-Id": {"real-thread"}, "X-Codex-Window-Id": {"real-thread:9"}}
			ids := resolveCodexFingerprintIDsWithSource(child, parent, headers, currentCodexFingerprintPolicy())
			if mode == "off" {
				require.Nil(t, ids)
				require.Equal(t, "passthrough", view.IdentifierPolicy.InstallationID.Behavior)
				require.Nil(t, view.IdentifierPolicy.InstallationID.Value)
				return
			}
			require.Equal(t, ids.installationID, *view.IdentifierPolicy.InstallationID.Value)
			require.Equal(t, "43ef16f3-2c23-4ca0-b67d-7c3f0189aca4", *view.IdentifierPolicy.InstallationID.Value)
			if mode == "device" {
				require.Equal(t, "passthrough", view.IdentifierPolicy.SessionID.Behavior)
				return
			}
			require.Equal(t, ids.sessionID, *view.IdentifierPolicy.SessionID.Value)
			require.Nil(t, view.IdentifierPolicy.WindowID.Value, "the real request window index is not known by this endpoint")
			if mode == "session" {
				require.Equal(t, "request_derived", view.IdentifierPolicy.ThreadID.Behavior)
				require.Nil(t, view.IdentifierPolicy.ThreadID.Value, "do not display the empty-request thread fallback")
				require.Equal(t, "mapped_parent_thread", view.IdentifierPolicy.ParentThreadID.Rule)
			} else {
				require.Equal(t, ids.threadID, *view.IdentifierPolicy.ThreadID.Value)
				require.Equal(t, "remove_parent_self_reference", view.IdentifierPolicy.ParentThreadID.Rule)
			}
		})
	}
}

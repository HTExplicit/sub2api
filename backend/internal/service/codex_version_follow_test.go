package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codextransport"
	"github.com/stretchr/testify/require"
)

func TestCodexVersionFollowsStableWithoutProfileGate(t *testing.T) {
	fingerprintPolicyTestState(t)
	for _, test := range []struct{ name, manual, synced, syncEnabled, version, source string }{
		{"synced", "", "0.163.0", "true", "0.163.0", "synced"},
		{"future release", "", "0.164.0", "true", "0.164.0", "synced"},
		{"manual", "0.165.0", "0.163.0", "true", "0.165.0", "manual"},
		{"sync off retains discovery", "", "0.163.0", "false", "0.163.0", "synced"},
		{"default", "", "", "true", codexCLIVersion, "builtin"},
		{"minimum", "0.100.0", "", "false", codexCLIVersion, "builtin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := NewSettingService(&codexFingerprintSettingsRepo{values: map[string]string{
				SettingKeyOpenAICodexClientVersion: test.manual, SettingKeyOpenAICodexClientVersionSynced: test.synced,
				SettingKeyOpenAICodexVersionAutoSyncEnabled: test.syncEnabled,
			}}, nil)
			view, err := svc.GetCodexFingerprintSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, test.version, view.EffectiveVersion)
			require.Equal(t, test.source, view.VersionSource)
			require.False(t, view.VersionPendingValidation)
			require.Equal(t, codextransport.LatestVersion(), view.VerifiedVersion)
			publishCodexFingerprintSettings(view)
			identity := resolveCodexOutboundIdentity("")
			require.Equal(t, test.version, identity.version)
			require.Contains(t, identity.userAgent, "/"+test.version+" ")
			for _, plane := range []string{"http", "ws"} {
				binding, err := codextransport.Select(identity.userAgent, "test", plane)
				require.NoError(t, err)
				require.Equal(t, test.version, binding.ClientVersion)
			}
		})
	}
}

func TestCodexVersionSavePublishesNewIdentityWithoutChangingActiveAttempt(t *testing.T) {
	fingerprintPolicyTestState(t)
	repo := &codexFingerprintSettingsRepo{values: map[string]string{SettingKeyOpenAICodexClientVersionSynced: "0.163.0"}}
	svc := NewSettingService(repo, nil)
	require.NoError(t, svc.LoadCodexFingerprintSettings(context.Background()))
	frozen := currentCodexFingerprintPolicy()
	view, err := svc.UpdateCodexFingerprintSettings(context.Background(), CodexFingerprintSettings{Enabled: true, ClientVersion: "0.164.0", VersionAutoSyncEnabled: true})
	require.NoError(t, err)
	require.Equal(t, "0.164.0", view.EffectiveVersion)
	require.Equal(t, "0.163.0", resolveCodexOutboundIdentity("", frozen).version)
	require.Equal(t, "0.164.0", resolveCodexOutboundIdentity("").version)
	require.NotEqual(t, frozen.revision, currentCodexFingerprintPolicy().revision)
	headers := http.Header{}
	ApplyCodexCanonicalAuthIdentity(headers)
	require.Contains(t, headers.Get("User-Agent"), "/0.164.0 ")
}

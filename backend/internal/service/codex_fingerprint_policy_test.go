package service

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexFingerprintSettingsRepo struct {
	SettingRepository
	values map[string]string
	fail   error
	writes int
}

func (r *codexFingerprintSettingsRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return maps.Clone(r.values), nil
}
func (r *codexFingerprintSettingsRepo) SetMultiple(_ context.Context, updates map[string]string) error {
	if r.fail != nil {
		return r.fail
	}
	r.writes++
	for key, value := range updates {
		r.values[key] = value
	}
	return nil
}

func fingerprintPolicyTestState(t *testing.T) {
	t.Helper()
	previous := publishedCodexFingerprintPolicy.Load()
	t.Cleanup(func() { publishedCodexFingerprintPolicy.Store(previous) })
	publishedCodexFingerprintPolicy.Store(nil)
}

func fingerprintTestPolicy(enabled bool, version string) *codexFingerprintPolicy {
	ua := buildCodexCLIUserAgent(version)
	return &codexFingerprintPolicy{enabled: enabled, userAgent: ua, revision: codexFingerprintPolicyRevision(enabled, ua)}
}

func fingerprintTestAccount(mode string) *Account {
	return &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		codexFingerprintModeExtraKey: mode, codexFingerprintSeedExtraKey: "ac6a7159-d158-41e8-bd52-3e66fd5c6515",
	}}
}

func TestCodexFingerprintSettingsPreserveStorageAndPublishOnlyAfterSave(t *testing.T) {
	fingerprintPolicyTestState(t)
	repo := &codexFingerprintSettingsRepo{values: map[string]string{SettingKeyOpenAICodexClientVersionSynced: "0.161.0"}}
	svc := NewSettingService(repo, &config.Config{})
	require.NoError(t, svc.LoadCodexFingerprintSettings(context.Background()))
	before := currentCodexFingerprintPolicy()
	account := fingerprintTestAccount("session")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	frozen := stageCodexFingerprintPolicy(c, account)
	sent := CodexFingerprintSettings{Enabled: false, ClientVersion: "0.162.0", VersionAutoSyncEnabled: false}
	repo.fail = errors.New("storage unavailable")
	_, err := svc.UpdateCodexFingerprintSettings(context.Background(), sent)
	require.Error(t, err)
	require.Same(t, before, currentCodexFingerprintPolicy())
	require.Zero(t, repo.writes)
	repo.fail = nil
	view, err := svc.UpdateCodexFingerprintSettings(context.Background(), sent)
	require.NoError(t, err)
	require.False(t, view.Enabled)
	require.Equal(t, "0.162.0", view.EffectiveVersion)
	require.Equal(t, "0.161.0", repo.values[SettingKeyOpenAICodexClientVersionSynced])
	require.False(t, currentCodexFingerprintPolicy().enabled)
	require.Same(t, frozen, codexFingerprintPolicyForContext(c, account), "an active attempt keeps its policy")
	other := fingerprintTestAccount("device")
	other.ID = 43
	require.False(t, codexFingerprintPolicyForContext(c, other).enabled, "a new account attempt cannot inherit stale policy")
}

func TestCodexFingerprintSettingsInitialFlagAndValidation(t *testing.T) {
	fingerprintPolicyTestState(t)
	repo := &codexFingerprintSettingsRepo{values: map[string]string{}}
	cfg := &config.Config{}
	cfg.Gateway.DisableCodexIdentityEnforcement = true
	view, err := NewSettingService(repo, cfg).GetCodexFingerprintSettings(context.Background())
	require.NoError(t, err)
	require.False(t, view.Enabled)
	for _, raw := range []string{`null`, `[]`, `{}`, `{"enabled":null,"user_agent":"","client_version":"","version_auto_sync_enabled":true}`, `{"enabled":true,"user_agent":"bad\r\nUA","client_version":"","version_auto_sync_enabled":true}`, `{"enabled":true,"user_agent":"","client_version":"bad","version_auto_sync_enabled":true}`} {
		_, err := DecodeCodexFingerprintSettings([]byte(raw))
		require.Error(t, err)
	}
	_, err = DecodeCodexFingerprintSettings([]byte(`{"enabled":true,"user_agent":"","client_version":"0.161.0","version_auto_sync_enabled":true}`))
	require.NoError(t, err)
	for _, raw := range []string{`{"mode":"invented"}`, `{"mode":"device","codex_fingerprint_seed":"injected"}`, `{"mode":null}`} {
		_, err := DecodeCodexFingerprintMode([]byte(raw))
		require.Error(t, err)
	}
}

func TestCodexFingerprintLogicalTurnReferencesAndPermissions(t *testing.T) {
	policy := fingerprintTestPolicy(true, "0.161.0")
	for _, mode := range []string{"device", "session", "full"} {
		t.Run(mode, func(t *testing.T) {
			account := fingerprintTestAccount(mode)
			headers := http.Header{"Session-Id": {"client-session"}, "Thread-Id": {"child-thread"}, "X-Codex-Window-Id": {"child-thread:8"}, "X-Codex-Parent-Thread-Id": {"parent-thread"}}
			metadata := map[string]any{"installation_id": "client-device", "session_id": "client-session", "thread_id": "child-thread", "parent_thread_id": "parent-thread", "parent_turn_id": "parent-turn", "root_turn_id": "root-turn", "turn_id": "real-turn", "turn_started_at_unix_ms": float64(12345), "window_id": "child-thread:8", "window_number": float64(8), "sandbox": "none", "sandbox_mode": "danger-full-access"}
			raw, err := json.Marshal(metadata)
			require.NoError(t, err)
			headers.Set("x-codex-turn-metadata", string(raw))
			body := map[string]any{"prompt_cache_key": "custom-affinity", "client_metadata": map[string]any{"session_id": "client-session", "thread_id": "child-thread", "turn_id": "real-turn", "parent_turn_id": "parent-turn", "root_turn_id": "root-turn", "x-codex-parent-thread-id": "parent-thread", "x-codex-window-id": "child-thread:8", "x-codex-turn-metadata": string(raw)}}
			ids := resolveCodexFingerprintIDsFromRequest(account, headers, policy)
			ids.alignSandboxWithUserAgent("codex-tui/0.161.0 (Ubuntu 24.4.0; x86_64) xterm-256color")
			require.True(t, applyCodexFingerprintClientMetadata(body, ids))
			applyCodexFingerprintHeaders(headers, ids)
			wire, err := json.Marshal(body)
			require.NoError(t, err)
			require.Equal(t, "custom-affinity", gjson.GetBytes(wire, "prompt_cache_key").String())
			require.Equal(t, "real-turn", gjson.GetBytes(wire, "client_metadata.turn_id").String())
			var embedded map[string]any
			require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(wire, "client_metadata.x-codex-turn-metadata").String()), &embedded))
			require.Equal(t, "real-turn", embedded["turn_id"])
			require.Equal(t, "parent-turn", embedded["parent_turn_id"])
			require.Equal(t, "root-turn", embedded["root_turn_id"])
			require.Equal(t, float64(12345), embedded["turn_started_at_unix_ms"])
			require.Equal(t, "none", embedded["sandbox"])
			require.Equal(t, float64(8), embedded["window_number"])
			if mode == "device" {
				require.Equal(t, "child-thread", embedded["thread_id"])
			} else {
				require.Equal(t, headers.Get("thread-id"), embedded["thread_id"])
				require.Equal(t, headers.Get("x-codex-window-id"), embedded["window_id"])
				require.Contains(t, embedded["window_id"], ":8")
				if mode == "full" {
					require.NotContains(t, embedded, "parent_thread_id")
					require.Empty(t, headers.Get("x-codex-parent-thread-id"))
				} else {
					require.Equal(t, ids.mapThread("parent-thread"), embedded["parent_thread_id"])
				}
			}
		})
	}
}

func TestCodexFingerprintMasterOffKeepsClientAndPreventsWSReuse(t *testing.T) {
	fingerprintPolicyTestState(t)
	account := fingerprintTestAccount("full")
	on, off := fingerprintTestPolicy(true, "0.161.0"), fingerprintTestPolicy(false, "0.162.0")
	publishedCodexFingerprintPolicy.Store(off)
	client := http.Header{"User-Agent": {"codex-tui/0.150.0 (Ubuntu 24.4.0; x86_64) xterm-256color"}, "Originator": {"codex-tui"}, "Version": {"0.150.0"}, "Session-Id": {"client-session"}, "Thread-Id": {"client-thread"}}
	raw := []byte(`{"client_metadata":{"session_id":"client-session","thread_id":"client-thread","turn_id":"real-turn"},"prompt_cache_key":"custom"}`)
	result, changed, err := applyCodexAccountIdentityClientMetadataRaw(raw, account, 7, off)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, raw, result)
	require.Nil(t, resolveCodexFingerprintIDsFromRequest(account, client, off))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header = client
	stageCodexFingerprintPolicy(c, account)
	upstream := http.Header{"User-Agent": {codexCLIUserAgent}, "Originator": {"codex_cli_rs"}, "Version": {codexCLIVersion}, "Session-Id": {"scoped-session"}}
	svc := &OpenAIGatewayService{}
	require.NoError(t, svc.finalizeCodexOutboundHeaders(context.Background(), c, account, upstream, "gpt-6-astra", ""))
	for _, name := range []string{"user-agent", "originator", "version", "session-id", "thread-id"} {
		require.Equal(t, client.Get(name), upstream.Get(name))
	}
	require.NotEqual(t, normalizeOpenAIWSHandshakeCompatibility(account, client, on), normalizeOpenAIWSHandshakeCompatibility(account, client, off))
}

func TestCodexFingerprintTestEffortsExcludeUltraFromEverySource(t *testing.T) {
	for _, typ := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, model := range []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol", "upstream-only-model"} {
			t.Run(typ+"/"+model, func(t *testing.T) {
				account := &Account{Platform: PlatformOpenAI, Type: typ, Credentials: map[string]any{"model_mapping": map[string]any{"alias": model}}}
				supported := true
				account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{model: {Reasoning: &supported, SupportedReasoningLevels: []string{"high", "ultra"}, DefaultReasoningLevel: "ultra"}}})
				levels, defaultEffort := AccountTestReasoningOptions(account, "alias")
				require.Equal(t, []string{"high"}, levels)
				require.Empty(t, defaultEffort)
				require.Error(t, ValidateAccountTestReasoning(account, "alias", "default", "ultra"))
				require.NoError(t, ValidateAccountTestReasoning(account, "alias", "default", "high"))
			})
		}
	}
}

func TestCodexFingerprintAccountModeEpochAndGeneralEditPreservation(t *testing.T) {
	fingerprintPolicyTestState(t)
	account := fingerprintTestAccount("full")
	previous, existed := codexFingerprintAccountEpochs.Load(account.ID)
	t.Cleanup(func() {
		if existed {
			codexFingerprintAccountEpochs.Store(account.ID, previous)
		} else {
			codexFingerprintAccountEpochs.Delete(account.ID)
		}
	})
	before := currentCodexFingerprintPolicyForAccount(account)
	other := fingerprintTestAccount("device")
	other.ID = 99
	unaffected := currentCodexFingerprintPolicyForAccount(other)
	notifyCodexFingerprintAccountChanged(account.ID)
	require.NotEqual(t, before.revision, currentCodexFingerprintPolicyForAccount(account).revision)
	require.Equal(t, unaffected.revision, currentCodexFingerprintPolicyForAccount(other).revision)
	seed := account.Extra[codexFingerprintSeedExtraKey]
	updated := prepareCodexFingerprintExtraForUpdate(account, map[string]any{"unrelated": true})
	require.Equal(t, "full", updated[codexFingerprintModeExtraKey])
	require.Equal(t, seed, updated[codexFingerprintSeedExtraKey])
	require.Equal(t, true, updated["unrelated"])
}

func TestCodexFingerprintSandboxAlignmentPreservesActualPolicy(t *testing.T) {
	for _, backend := range []string{"none", "external", "custom-sandbox", ""} {
		metadata := map[string]any{"sandbox": backend, "sandbox_mode": "read-only"}
		require.False(t, alignCodexSandboxMetadata(metadata, "seatbelt"))
		require.Equal(t, backend, metadata["sandbox"])
	}
	metadata := map[string]any{"sandbox": "windows_mxc", "sandbox_mode": "workspace-write"}
	require.False(t, alignCodexSandboxMetadata(metadata, "windows_sandbox"))
	metadata = map[string]any{"sandbox": "seccomp", "sandbox_mode": "workspace-write", "thread_id": "original", "turn_id": "original-turn"}
	require.True(t, alignCodexSandboxMetadata(metadata, "seatbelt"))
	require.Equal(t, "workspace-write", metadata["sandbox_mode"])
	require.Equal(t, "original", metadata["thread_id"])
	require.Equal(t, "original-turn", metadata["turn_id"])
}

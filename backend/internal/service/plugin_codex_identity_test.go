package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type partialCodexIdentityFailure struct{}

func (partialCodexIdentityFailure) InvokeOperation(_ context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Operation == "codex.identity.available" {
		return extensionv1.Result{Payload: []byte(`{"valid":true}`)}, nil
	}
	return extensionv1.Result{}, ErrExtensionOperationUnavailable
}

func TestCodexIdentityPlanFailureCannotSendCanonicalFallback(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: partialCodexIdentityFailure{}})
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "fixture-refresh"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"}}
	headers := http.Header{"Originator": []string{"fixture-original"}}
	err := enforceCodexIdentityHeadersForAccountContext(context.Background(), headers, account, "")
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	require.Equal(t, "fixture-original", headers.Get("Originator"))
	req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"input":"fixture"}`))
	require.NoError(t, err)
	_, err = prepareCodexTransport(req, account)
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	client := &identityRefreshingOAuthClientStub{}
	_, err = NewOpenAIOAuthService(nil, client).RefreshAccountToken(context.Background(), account)
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	require.Empty(t, client.userAgent)
}

func TestCodexIdentityDisabledKeepsDataAndFailureBlocksHTTPAndRefresh(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "fixture-refresh"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"}}
	account.Extra = ensureCodexClientIdentityExtra(account.Platform, account.Type, account.Extra, time.Now())
	stored := account.Extra[CodexClientIdentityExtraKey]
	require.NotNil(t, stored)
	require.Equal(t, codexFingerprintDevice, account.GetCodexFingerprintMode())
	processExtensionOperations.Store(nil)
	_, found := account.CodexClientIdentity()
	require.False(t, found)
	require.Equal(t, stored, account.Extra[CodexClientIdentityExtraKey])
	updated := prepareCodexFingerprintExtraForUpdate(account, map[string]any{"operator_field": "changed"})
	require.Equal(t, stored, updated[CodexClientIdentityExtraKey], "editing a disabled account must retain its stored domain data")
	require.Equal(t, codexFingerprintOff, account.GetCodexFingerprintMode())
	account.Extra[codexFingerprintModeExtraKey] = "session"
	require.Equal(t, codexFingerprintSession, account.GetCodexFingerprintMode(), "upstream explicit policy survives domain disable")
	req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"input":"fixture"}`))
	require.NoError(t, err)
	wire, err := prepareCodexTransport(req, account)
	require.NoError(t, err)
	require.Same(t, req, wire)
	processExtensionOperations.Store(&extensionOperationProvider{invoker: failedPromptProcess{}})
	_, err = prepareCodexTransport(req, account)
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "policy failure cannot silently send canonical identity")
	client := &identityRefreshingOAuthClientStub{}
	_, err = NewOpenAIOAuthService(nil, client).RefreshAccountToken(context.Background(), account)
	require.ErrorIs(t, err, ErrExtensionOperationUnavailable)
	require.Empty(t, client.userAgent)
}

func TestCodexProfileMetadataDoesNotBroadenAccountBinding(t *testing.T) {
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		return promptPolicyFixture{}.InvokeOperation(context.Background(), "", "", in)
	})
	registry := manager.extensions.Load()
	registry.installations[1].Bindings = []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: PlatformOpenAI, AccountType: AccountTypeSetupToken, Enabled: true}}
	registry.installations[1].Manifest.Operations = map[string][]string{extensionv1.CapabilityRequest: {"codex.identity.available", "codex.identity.derive", "codex.identity.validate"}}
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{codexFingerprintSeedExtraKey: "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"}}
	_, found := account.CodexClientIdentity()
	require.False(t, found)
	account.Type = AccountTypeSetupToken
	_, found = account.CodexClientIdentity()
	require.True(t, found)
}

func TestCodexIdentitySnapshotReportsTheSelectedFingerprintSource(t *testing.T) {
	SetCodexForceCLIEnabled(true)
	t.Cleanup(func() { SetCodexForceCLIEnabled(false) })
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Extra: map[string]any{codexFingerprintSeedExtraKey: "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"}}
	snapshot := resolveCodexIdentitySnapshotContext(context.Background(), account, account, "")
	require.Equal(t, "account", snapshot.IdentitySource)
	snapshot = resolveCodexIdentitySnapshotContext(context.Background(), account, account, "codex_cli_rs/0.144.0 (Windows 10.0.19045; x86_64) unknown")
	require.Equal(t, "override_ua", snapshot.IdentitySource)
}

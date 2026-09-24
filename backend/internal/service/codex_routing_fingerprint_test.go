package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func TestCodexFingerprintShowsCookieDigestsAndOriginalVerificationTime(t *testing.T) {
	account := ticketTestAccount(7)
	manager := nativeTicketTestRuntime(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, nil)
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	manager.repo = store
	service := &OpenAIGatewayService{nativeCodexRuntime: manager, accountRepo: &routingAccountRepositoryFixture{account: account}}
	scope, err := service.PrepareCodexRoutingScope(context.Background(), 7, "http")
	require.NoError(t, err)
	verified := time.Now().UTC().Add(-20 * time.Second).Truncate(time.Second)
	expires := verified.Add(time.Minute)
	qualification := &extensionv1.CodexRoutingQualification{Scope: scope, Model: "gpt-6-astra", VerifiedAt: verified, ExpiresAt: expires, Bundle: extensionv1.CodexRoutingBundleRef{Key: "bundle.fingerprint", Revision: 1, ExpiresAt: expires}}
	raw, err := json.Marshal(map[string]any{"schema": 2, "identity": CodexTicketAccountIdentity(account), "phase": "ready", "enrolled": true, "qualification": qualification})
	require.NoError(t, err)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: "tickets", Key: "7." + codexRoutingDigest("gpt-6-astra"), Value: raw})
	require.NoError(t, err)
	request, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, err)
	request.Header.Set("Cookie", "__cflb=first-secret-cookie; auth_token=private-session")
	service.observeCodexWire(context.Background(), account, request, &http.Response{StatusCode: http.StatusOK}, qualification)
	view, err := service.CodexFingerprint(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, view.Models, 1)
	require.NotNil(t, view.Models[0].VerifiedAt)
	require.True(t, view.Models[0].VerifiedAt.Equal(verified))
	require.Equal(t, []string{"__cflb"}, view.Observed.CookieNames)
	require.Len(t, view.Observed.CookieVersions, 1)
	first := view.Observed.CookieVersions["__cflb"]
	require.Len(t, first, 16)
	raw, err = json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "first-secret-cookie")
	require.NotContains(t, string(raw), "private-session")
	before := len(store.values)
	_, err = service.CodexFingerprint(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, store.values, before, "read-only fingerprint GET wrote state")
	request.Header.Set("Cookie", "__cflb=second-secret-cookie")
	service.observeCodexWire(context.Background(), account, request, &http.Response{StatusCode: http.StatusOK}, qualification)
	view, err = service.CodexFingerprint(context.Background(), 7)
	require.NoError(t, err)
	require.NotEqual(t, first, view.Observed.CookieVersions["__cflb"])
	require.True(t, view.Models[0].VerifiedAt.Equal(verified), "GET replaced the original verification timestamp")
	require.False(t, resolveCodexIdentitySnapshotContext(context.Background(), account, account, "").UAOverridePresent)
	require.True(t, resolveCodexIdentitySnapshotContext(context.Background(), account, account, "explicit UA override").UAOverridePresent)
}

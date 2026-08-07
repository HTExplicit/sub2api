package service

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBusinessSystemPromptRemoteSkillUsesFixedBodyAcrossAdapters(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 11, Enabled: true, Body: embeddedBusinessSystemPrompt,
		CompositionMode: BusinessSystemPromptCompositionRemoteSkill,
		BundleID:        BusinessSystemPromptRemoteSkillBundleID,
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := &Account{Platform: PlatformOpenAI}
	responsesBody, application, err := gateway.applyBusinessSystemPromptForRequest(
		c,
		[]byte(`{"model":"gpt-5","input":[{"role":"user","content":"audit this REST API OAuth flow"}]}`),
		account,
		BusinessSystemPromptProtocolResponses,
		false,
	)
	require.NoError(t, err)
	require.True(t, application.Applied)
	require.Equal(t, BusinessSystemPromptRemoteSkillBundleID, application.BundleID)
	require.Empty(t, application.BundleManifestSHA256)
	require.Empty(t, application.RouteIDs)
	require.NotEmpty(t, application.BaseSHA256)
	require.NotEmpty(t, application.EffectiveSHA256)
	require.Equal(t, application.BaseSHA256, application.EffectiveSHA256)
	require.Equal(t, embeddedBusinessSystemPrompt, application.ServerInstructions)
	require.NotContains(t, application.ServerInstructions, "moxinggang.com")
	require.NotContains(t, application.ServerInstructions, `C:\Users\Administrator`)
	require.Equal(t, application.ServerInstructions, gjson.GetBytes(responsesBody, "instructions").String())

	// A transformed fallback body can contain different task words, but the
	// request-scoped fixed prompt must remain byte-for-byte identical.
	chatBody, fallbackApplication, err := gateway.applyBusinessSystemPromptForRequest(
		c,
		[]byte(`{"model":"gpt-5","messages":[{"role":"user","content":"malware reverse engineering"}]}`),
		account,
		BusinessSystemPromptProtocolChat,
		false,
	)
	require.NoError(t, err)
	require.Equal(t, application.EffectiveSHA256, fallbackApplication.EffectiveSHA256)
	require.Equal(t, embeddedBusinessSystemPrompt, fallbackApplication.ServerInstructions)
	require.True(t, chatBodyHasSystemPrompt(chatBody, application.ServerInstructions))

	cacheKey := appendBusinessSystemPromptApplicationToCacheKey("client-key", application)
	require.Contains(t, cacheKey, application.EffectiveSHA256)
	require.Equal(t, cacheKey, appendBusinessSystemPromptApplicationToCacheKey(cacheKey, application))
}

func TestBusinessSystemPromptOfflineBundleMissingIsDegradedWhileDisabled(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv(BusinessSystemPromptBundlePathEnv, missing)
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 4, Enabled: false, Body: embeddedBusinessSystemPrompt,
		CompositionMode:      BusinessSystemPromptCompositionOfflineBundle,
		BundleID:             BusinessSystemPromptSeedBundleID,
		BundleManifestSHA256: strings.Repeat("a", 64),
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	snapshot, ok := policy.CurrentSnapshot()
	require.True(t, ok)
	require.True(t, snapshot.Degraded)

	body := []byte(`{"instructions":"client"}`)
	got, application, err := (&OpenAIGatewayService{businessPromptService: policy}).applyBusinessSystemPrompt(
		body, &Account{Platform: PlatformOpenAI}, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	require.False(t, application.Applied)
	require.Equal(t, body, got)
}

func TestBusinessSystemPromptOfflineBundleMissingIsUnavailableWhenEnabled(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv(BusinessSystemPromptBundlePathEnv, missing)
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 4, Enabled: true, Body: embeddedBusinessSystemPrompt,
		CompositionMode:      BusinessSystemPromptCompositionOfflineBundle,
		BundleID:             BusinessSystemPromptSeedBundleID,
		BundleManifestSHA256: strings.Repeat("a", 64),
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	err := policy.Initialize(context.Background())
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
}

func TestBusinessSystemPromptRequestTextExtractionUsesLatestUserContent(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		body     string
		want     string
	}{
		{
			name:     "responses array",
			protocol: BusinessSystemPromptProtocolResponses,
			body:     `{"input":[{"role":"user","content":[{"type":"input_text","text":"old"}]},{"role":"assistant","content":"answer"},{"role":"user","content":"latest api audit"}]}`,
			want:     "latest api audit",
		},
		{
			name:     "responses string",
			protocol: BusinessSystemPromptProtocolResponses,
			body:     `{"input":"reverse this binary"}`,
			want:     "reverse this binary",
		},
		{
			name:     "chat parts",
			protocol: BusinessSystemPromptProtocolChat,
			body:     `{"messages":[{"role":"user","content":[{"type":"text","text":"inspect JWT auth"}]}]}`,
			want:     "inspect JWT auth",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, businessSystemPromptRequestTextFromJSON([]byte(tt.body), tt.protocol))
		})
	}
}

func TestBusinessSystemPromptWSTurnsReuseFixedRemoteSkillBody(t *testing.T) {
	policy := NewBusinessSystemPromptService(&fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 8, Enabled: true, Body: embeddedBusinessSystemPrompt,
		CompositionMode: BusinessSystemPromptCompositionRemoteSkill,
		BundleID:        BusinessSystemPromptRemoteSkillBundleID,
	}}, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	account := &Account{Platform: PlatformOpenAI}

	beginBusinessSystemPromptRequestTurn(c)
	_, first, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","input":[{"role":"user","content":"audit OAuth API"}]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Empty(t, first.RouteIDs)
	require.Equal(t, embeddedBusinessSystemPrompt, first.ServerInstructions)

	beginBusinessSystemPromptRequestTurn(c)
	_, continued, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","previous_response_id":"resp_route_1","input":[]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, first.EffectiveSHA256, continued.EffectiveSHA256)
	require.Equal(t, embeddedBusinessSystemPrompt, continued.ServerInstructions)

	beginBusinessSystemPromptRequestTurn(c)
	_, next, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","previous_response_id":"resp_route_1","input":[{"role":"user","content":"analyze this malware"}]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, first.EffectiveSHA256, next.EffectiveSHA256)
	require.Equal(t, embeddedBusinessSystemPrompt, next.ServerInstructions)
}

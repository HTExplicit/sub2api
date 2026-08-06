package service

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type fakeBusinessSystemPromptRouteBus struct {
	metadata map[string]BusinessSystemPromptBundleMetadata
}

func (b *fakeBusinessSystemPromptRouteBus) Publish(context.Context, int64) error         { return nil }
func (b *fakeBusinessSystemPromptRouteBus) Subscribe(context.Context, func(int64)) error { return nil }
func (b *fakeBusinessSystemPromptRouteBus) StoreBusinessSystemPromptRouteMetadata(_ context.Context, responseID string, metadata BusinessSystemPromptBundleMetadata, _ time.Duration) error {
	if b.metadata == nil {
		b.metadata = make(map[string]BusinessSystemPromptBundleMetadata)
	}
	b.metadata[responseID] = metadata
	return nil
}
func (b *fakeBusinessSystemPromptRouteBus) LoadBusinessSystemPromptRouteMetadata(_ context.Context, responseID string) (BusinessSystemPromptBundleMetadata, bool, error) {
	metadata, ok := b.metadata[responseID]
	return metadata, ok, nil
}

func TestBusinessSystemPromptOfflineBundleCompilesOnceAcrossAdapters(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	t.Setenv(BusinessSystemPromptBundlePathEnv, root)

	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 11, Enabled: true, Body: embeddedBusinessSystemPrompt,
		CompositionMode: BusinessSystemPromptCompositionOfflineBundle,
		BundleID:        bundle.Manifest.BundleID, BundleManifestSHA256: bundle.ManifestSHA256,
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
	require.Equal(t, bundle.Manifest.BundleID, application.BundleID)
	require.Equal(t, bundle.ManifestSHA256, application.BundleManifestSHA256)
	require.Equal(t, []string{"api-security"}, application.RouteIDs)
	require.NotEmpty(t, application.BaseSHA256)
	require.NotEmpty(t, application.EffectiveSHA256)
	require.NotEqual(t, application.BaseSHA256, application.EffectiveSHA256)
	require.NotContains(t, application.ServerInstructions, "moxinggang.com")
	require.NotContains(t, application.ServerInstructions, `C:\Users\Administrator`)
	require.Equal(t, application.ServerInstructions, gjson.GetBytes(responsesBody, "instructions").String())

	// A transformed fallback body can contain different task words, but the
	// request-scoped compilation decision must remain byte-for-byte identical.
	chatBody, fallbackApplication, err := gateway.applyBusinessSystemPromptForRequest(
		c,
		[]byte(`{"model":"gpt-5","messages":[{"role":"user","content":"malware reverse engineering"}]}`),
		account,
		BusinessSystemPromptProtocolChat,
		false,
	)
	require.NoError(t, err)
	require.Equal(t, application.EffectiveSHA256, fallbackApplication.EffectiveSHA256)
	require.Equal(t, application.RouteIDs, fallbackApplication.RouteIDs)
	require.True(t, chatBodyHasSystemPrompt(chatBody, application.ServerInstructions))

	cacheKey := appendBusinessSystemPromptApplicationToCacheKey("client-key", application)
	require.Contains(t, cacheKey, application.BundleManifestSHA256)
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

func TestBusinessSystemPromptWSTurnsCompileOnceAndContinuationInheritsRoute(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	t.Setenv(BusinessSystemPromptBundlePathEnv, root)
	bus := &fakeBusinessSystemPromptRouteBus{}
	policy := NewBusinessSystemPromptService(&fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 8, Enabled: true, Body: embeddedBusinessSystemPrompt,
		CompositionMode: BusinessSystemPromptCompositionOfflineBundle,
		BundleID:        bundle.Manifest.BundleID, BundleManifestSHA256: bundle.ManifestSHA256,
	}}, bus)
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
	require.Equal(t, []string{"api-security"}, first.RouteIDs)
	gateway.rewriteBusinessSystemPromptJSONForRequest(c,
		[]byte(`{"type":"response.completed","response":{"id":"resp_route_1"}}`),
		BusinessSystemPromptProtocolResponses)
	require.Contains(t, bus.metadata, "resp_route_1")

	beginBusinessSystemPromptRequestTurn(c)
	_, continued, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","previous_response_id":"resp_route_1","input":[]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, first.RouteIDs, continued.RouteIDs)

	beginBusinessSystemPromptRequestTurn(c)
	_, next, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","previous_response_id":"resp_route_1","input":[{"role":"user","content":"analyze this malware"}]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, []string{"malware-analysis"}, next.RouteIDs)
}

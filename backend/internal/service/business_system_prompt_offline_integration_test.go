package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBusinessSystemPromptHybridCodexUsesFixedBodyAcrossAdapters(t *testing.T) {
	policy := newGatewayHybridBusinessSystemPromptPolicyWithBody(t, embeddedBusinessSystemPrompt, 11)
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("originator", "codex-tui")
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
	require.NotEmpty(t, application.BundleManifestSHA256)
	require.Equal(t, int64(11), application.BundleRevision)
	require.NotEmpty(t, application.BaseSHA256)
	require.NotEmpty(t, application.EffectiveSHA256)
	require.Equal(t, application.BaseSHA256, application.EffectiveSHA256)
	sourceRoot := RemoteSkillMoxinggangRoot
	expectedPrompt := applyRemoteSkillRoot(embeddedBusinessSystemPrompt, sourceRoot)
	require.Equal(t, expectedPrompt, application.ServerInstructions)
	require.Contains(t, application.ServerInstructions, sourceRoot)
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
	require.Equal(t, expectedPrompt, fallbackApplication.ServerInstructions)
	require.True(t, chatBodyHasSystemPrompt(chatBody, application.ServerInstructions))

	cacheKey := appendBusinessSystemPromptApplicationToCacheKey("client-key", application)
	require.Contains(t, cacheKey, application.EffectiveSHA256)
	require.Equal(t, cacheKey, appendBusinessSystemPromptApplicationToCacheKey(cacheKey, application))
}

func TestBusinessSystemPromptWSHybridCodexTurnsReuseFixedBody(t *testing.T) {
	policy := newGatewayHybridBusinessSystemPromptPolicyWithBody(t, embeddedBusinessSystemPrompt, 8)
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	expectedPrompt := applyRemoteSkillRoot(
		embeddedBusinessSystemPrompt,
		RemoteSkillMoxinggangRoot,
	)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	c.Request.Header.Set("originator", "codex-tui")
	account := &Account{Platform: PlatformOpenAI}

	beginBusinessSystemPromptRequestTurn(c)
	_, first, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","input":[{"role":"user","content":"audit OAuth API"}]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, expectedPrompt, first.ServerInstructions)

	beginBusinessSystemPromptRequestTurn(c)
	_, continued, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","previous_response_id":"resp_route_1","input":[]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, first.EffectiveSHA256, continued.EffectiveSHA256)
	require.Equal(t, expectedPrompt, continued.ServerInstructions)

	beginBusinessSystemPromptRequestTurn(c)
	_, next, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","previous_response_id":"resp_route_1","input":[{"role":"user","content":"analyze this malware"}]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, first.EffectiveSHA256, next.EffectiveSHA256)
	require.Equal(t, expectedPrompt, next.ServerInstructions)
}

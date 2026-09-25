package service

import (
	"context"
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
	require.Len(t, application.RulesPlan.Placements, 1)
	placement := application.RulesPlan.Placements[0]
	require.Equal(t, "instructions", placement.Carrier)
	require.True(t, placement.PreserveEcho)
	require.True(t, application.PreserveInstructionsEcho)
	require.NotEmpty(t, application.EffectiveSHA256)
	capture, err := buildRemoteSkillPromptCapture([]byte(embeddedBusinessSystemPrompt))
	require.NoError(t, err)
	expectedPrompt := string(capture.EffectiveBody)
	publication, err := policy.registry.ActivePublication(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(11), publication.Revision)
	require.Equal(t, publication.Prompt.ID, publication.Version.PromptVersionID)
	require.NotEmpty(t, publication.Version.EffectiveTreeSHA256)
	require.Equal(t, capture.RawSHA256, publication.Prompt.RawSHA256)
	require.Equal(t, capture.EffectiveSHA256, publication.Prompt.EffectiveSHA256)
	require.NotEqual(t, publication.Prompt.RawSHA256, publication.Prompt.EffectiveSHA256)
	require.Equal(t, expectedPrompt, publication.EffectivePromptBody)
	require.Equal(t, capture.EffectiveSHA256, placement.SHA256)
	require.Equal(t, expectedPrompt, placement.Body)
	require.Contains(t, placement.Body, RemoteSkillPublicRoot)
	require.NotContains(t, placement.Body, `C:\Users\Administrator`)
	require.Equal(t, placement.Body, gjson.GetBytes(responsesBody, "instructions").String())
	snapshot, ok := policy.CurrentSnapshot()
	require.True(t, ok)
	require.Equal(t, snapshot.RulePolicy.Rules[0].TemplateID, placement.TemplateID)
	require.Equal(t, snapshot.RulePolicy.Rules[0].VersionID, placement.VersionID)

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
	require.Len(t, fallbackApplication.RulesPlan.Placements, 1)
	fallbackPlacement := fallbackApplication.RulesPlan.Placements[0]
	require.Equal(t, placement.SHA256, fallbackPlacement.SHA256)
	require.Equal(t, placement.TemplateID, fallbackPlacement.TemplateID)
	require.Equal(t, placement.VersionID, fallbackPlacement.VersionID)
	require.Equal(t, expectedPrompt, fallbackPlacement.Body)
	require.Equal(t, "messages", fallbackPlacement.Carrier)
	require.Equal(t, "system", fallbackPlacement.Role)
	require.True(t, fallbackPlacement.PreserveEcho)
	require.True(t, chatBodyHasSystemPrompt(chatBody, placement.Body))
	require.NotEqual(t, application.EffectiveSHA256, fallbackApplication.EffectiveSHA256, "different final protocol carriers have separate plan identities")

	cacheKey := deriveBusinessSystemPromptCacheKey(c, "client-key", application)
	require.Regexp(t, `^[0-9a-f]{64}$`, cacheKey)
	require.Equal(t, cacheKey, deriveBusinessSystemPromptCacheKey(c, cacheKey, application))
}

func TestBusinessSystemPromptWSHybridCodexTurnsReuseFixedBody(t *testing.T) {
	policy := newGatewayHybridBusinessSystemPromptPolicyWithBody(t, embeddedBusinessSystemPrompt, 8)
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	capture, err := buildRemoteSkillPromptCapture([]byte(embeddedBusinessSystemPrompt))
	require.NoError(t, err)
	expectedPrompt := string(capture.EffectiveBody)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	c.Request.Header.Set("originator", "codex-tui")
	account := &Account{Platform: PlatformOpenAI}

	beginBusinessSystemPromptRequestTurn(c)
	firstBody, first, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","input":[{"role":"user","content":"audit OAuth API"}]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Len(t, first.RulesPlan.Placements, 1)
	require.Equal(t, expectedPrompt, first.RulesPlan.Placements[0].Body)
	require.Equal(t, expectedPrompt, gjson.GetBytes(firstBody, "instructions").String())

	beginBusinessSystemPromptRequestTurn(c)
	continuedBody, continued, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","previous_response_id":"resp_route_1","input":[]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, first.EffectiveSHA256, continued.EffectiveSHA256)
	require.Equal(t, expectedPrompt, continued.RulesPlan.Placements[0].Body)
	require.Equal(t, expectedPrompt, gjson.GetBytes(continuedBody, "instructions").String())
	require.Equal(t, "resp_route_1", gjson.GetBytes(continuedBody, "previous_response_id").String())
	require.Empty(t, gjson.GetBytes(continuedBody, "input").Array())

	beginBusinessSystemPromptRequestTurn(c)
	nextBody, next, err := gateway.applyBusinessSystemPromptForRequest(c,
		[]byte(`{"type":"response.create","previous_response_id":"resp_route_1","input":[{"role":"user","content":"analyze this malware"}]}`),
		account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, first.EffectiveSHA256, next.EffectiveSHA256)
	require.Equal(t, expectedPrompt, next.RulesPlan.Placements[0].Body)
	require.Equal(t, expectedPrompt, gjson.GetBytes(nextBody, "instructions").String())
	require.Equal(t, "analyze this malware", gjson.GetBytes(nextBody, "input.0.content").String())
}

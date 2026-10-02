package service

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountTestReasoningValidatedAgainstMappedModel(t *testing.T) {
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"model_mapping": map[string]any{"friendly": "gpt-6-astra"}}}
	levels, _ := AccountTestReasoningOptions(a, "friendly")
	require.Contains(t, levels, "ultra")
	require.NoError(t, ValidateAccountTestReasoning(a, "friendly", "default", "ultra"))
	require.Error(t, ValidateAccountTestReasoning(a, "friendly", "compact", "ultra"))
	require.Error(t, ValidateAccountTestReasoning(a, "friendly", "default", "invented"))
	require.Error(t, ValidateAccountTestReasoning(a, "gpt-image-2", "default", "high"))
	require.NoError(t, ValidateAccountTestReasoning(a, "unknown-model", "default", ""))
}

func TestAccountTestReasoningFinalPayload(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(accountTestReasoningContextKey, "xhigh")
	responses := createOpenAITestPayload("gpt-6-astra", true, "test")
	applyAccountTestReasoning(c, responses, false)
	require.Equal(t, map[string]any{"effort": "xhigh"}, responses["reasoning"])
	require.NotContains(t, responses, "reasoning_effort")
	chat := createOpenAIChatCompletionsTestPayload("gpt-6-astra", "test")
	applyAccountTestReasoning(c, chat, true)
	require.Equal(t, "xhigh", chat["reasoning_effort"])
	require.NotContains(t, chat, "reasoning")
	c.Set(accountTestReasoningContextKey, "")
	defaultPayload := createOpenAITestPayload("gpt-6-astra", true, "test")
	applyAccountTestReasoning(c, defaultPayload, false)
	require.NotContains(t, defaultPayload, "reasoning")
}

func TestAccountTestReasoningDoesNotAdvertiseUnsupportedProtocol(t *testing.T) {
	// Anthropic accounts transmit output_config.effort; see the Claude tests below.
	for _, platform := range []string{PlatformGemini, PlatformDeepseek} {
		account := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAnthropic}}
		supported := true
		account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"test-model": {Reasoning: &supported, SupportedReasoningLevels: []string{"high"}}}})
		levels, _ := AccountTestReasoningOptions(account, "test-model")
		require.Empty(t, levels)
		require.Error(t, ValidateAccountTestReasoning(account, "test-model", "default", "high"))
		require.NoError(t, ValidateAccountTestReasoning(account, "test-model", "default", ""))
	}
}

func TestAccountTestReasoningClaudeReadsTestedUpstreamModel(t *testing.T) {
	allLevels := []string{"low", "medium", "high", "xhigh", "max"}
	apiKey := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"friendly": "claude-opus-4-5-20251101"}}}
	levels, defaultLevel := AccountTestReasoningOptions(apiKey, "friendly")
	require.Equal(t, []string{"low", "medium", "high"}, levels, "levels of the mapped upstream model")
	require.Equal(t, "high", defaultLevel)
	disabled := false
	apiKey.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"claude-opus-4-5-20251101": {Reasoning: &disabled}}})
	levels, _ = AccountTestReasoningOptions(apiKey, "friendly")
	require.Empty(t, levels, "an explicit upstream reasoning=false rules the model out")

	oauth := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	levels, defaultLevel = AccountTestReasoningOptions(oauth, "claude-opus-5-5")
	require.Equal(t, allLevels, levels)
	require.Equal(t, "medium", defaultLevel)
	for _, model := range []string{"claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001"} {
		levels, _ = AccountTestReasoningOptions(oauth, model)
		require.Empty(t, levels, model)
	}

	bedrock := &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"aws_region": "us-east-1"}}
	levels, _ = AccountTestReasoningOptions(bedrock, "claude-opus-4-7")
	require.Empty(t, levels, "Bedrock request preparation removes output_config for this model")
	levels, defaultLevel = AccountTestReasoningOptions(bedrock, "claude-sonnet-5-5")
	require.Equal(t, allLevels, levels)
	require.Equal(t, "high", defaultLevel)
}

func TestAccountTestReasoningGrokFollowsForwardingEffortRules(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"agents": "grok-4.20-multi-agent"}}}
	levels, _ := AccountTestReasoningOptions(account, "grok-4.7")
	require.Equal(t, []string{"low", "medium", "high", "xhigh"}, levels)
	levels, _ = AccountTestReasoningOptions(account, "agents")
	require.Equal(t, []string{"low", "medium", "high"}, levels, "an alias is judged by the canonical ID forwarding sends")
	for _, model := range []string{"grok-4.20-0309-non-reasoning", "grok-composer-2.5-fast", "grok-imagine-image"} {
		levels, _ = AccountTestReasoningOptions(account, model)
		require.Empty(t, levels, "forwarding removes the effort for %s", model)
	}
}

func TestAccountTestClaudeBodyWritesEffortLikeProduction(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	payload, err := createTestPayload("claude-opus-4-7", "test")
	require.NoError(t, err)
	built, err := json.Marshal(payload)
	require.NoError(t, err)
	body, err := accountTestClaudeBody(c, payload, "claude-opus-4-7")
	require.NoError(t, err)
	require.Equal(t, built, body, "without a selected effort the payload is unchanged")

	c.Set(accountTestReasoningContextKey, "xhigh")
	body, err = accountTestClaudeBody(c, payload, "claude-opus-4-7")
	require.NoError(t, err)
	require.Equal(t, "xhigh", gjson.GetBytes(body, "output_config.effort").String())
	require.JSONEq(t, `{"type":"adaptive"}`, gjson.GetBytes(body, "thinking").Raw)
	require.False(t, gjson.GetBytes(body, "temperature").Exists())
	require.Equal(t, int64(64000), gjson.GetBytes(body, "max_tokens").Int())

	c.Set(accountTestReasoningContextKey, "high")
	payload, err = createTestPayload("claude-opus-4-5-20251101", "test")
	require.NoError(t, err)
	body, err = accountTestClaudeBody(c, payload, "claude-opus-4-5-20251101")
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(body, "output_config.effort").String())
	require.False(t, gjson.GetBytes(body, "thinking").Exists(), "Opus 4.5 has no adaptive thinking")

	c.Set(accountTestReasoningContextKey, "max")
	for _, model := range []string{"claude-opus-5-5", "claude-sonnet-5-5"} {
		payload, err = createTestPayload(model, "test")
		require.NoError(t, err)
		_, err = accountTestClaudeBody(c, payload, model)
		require.NoError(t, err, model)
	}
	payload["tool_choice"] = map[string]any{"type": "any"}
	_, err = accountTestClaudeBody(c, payload, "claude-sonnet-5-5")
	require.Error(t, err, "the 5.5 request validation runs on the test body")
}

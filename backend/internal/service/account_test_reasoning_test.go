package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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

//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type reasoningTestRepo struct {
	AccountRepository
	account *Account
}

func (r *reasoningTestRepo) GetByID(context.Context, int64) (*Account, error)         { return r.account, nil }
func (r *reasoningTestRepo) UpdateExtra(context.Context, int64, map[string]any) error { return nil }

func TestAccountTestReasoningSurvivesMappingAndWireCompression(t *testing.T) {
	a := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "test-access", "model_mapping": map[string]any{"friendly": "gpt-6-astra"}}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, "data: {\"type\":\"response.completed\"}\n\n")}}
	svc := &AccountTestService{accountRepo: &reasoningTestRepo{account: a}, httpUpstream: upstream, cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICodexRequestZstd: true}}}
	c, rec := newTestContext()
	require.NoError(t, svc.TestAccountConnection(c, a.ID, "friendly", "test", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "ultra"}))
	require.Len(t, upstream.requests, 1)
	raw, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	body := zstdDecodeForTest(t, raw)
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
	require.Equal(t, "ultra", gjson.GetBytes(body, "reasoning.effort").String())
	require.Contains(t, rec.Body.String(), `"effective_reasoning_effort":"ultra"`)
	c, _ = newTestContext()
	require.Error(t, svc.TestAccountConnection(c, a.ID, "friendly", "test", AccountTestModeCompact, AccountTestOptions{ReasoningEffort: "ultra"}))
	require.Len(t, upstream.requests, 1, "unsupported mode must fail before any upstream request")
}

func TestAccountTestReasoningOpenCodeGoSerializesNativeProtocol(t *testing.T) {
	for _, protocol := range []string{APIProtocolResponses, APIProtocolChatCompletions} {
		t.Run(protocol, func(t *testing.T) {
			account := openCodeGoTestAccount(44)
			account.Credentials["api_protocol"] = protocol
			supported := true
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"test-model": {Reasoning: &supported, SupportedReasoningLevels: []string{"high"}}}})
			response := adaptiveCNChatTestResponse()
			if protocol == APIProtocolResponses {
				response = adaptiveCNResponsesTestResponse()
			}
			svc, upstream := adaptiveCNAccountTestService(account, response)
			c, recorded := newTestContext()
			require.NoError(t, svc.TestAccountConnection(c, account.ID, "test-model", "test", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "high"}))
			require.Len(t, upstream.requests, 1)
			body, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			field := "reasoning_effort"
			if protocol == APIProtocolResponses {
				field = "reasoning.effort"
			}
			require.Equal(t, "high", gjson.GetBytes(body, field).String())
			require.Contains(t, recorded.Body.String(), `"effective_reasoning_effort":"high"`)
		})
	}
}

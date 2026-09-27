package admin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountResponseCodexTicketsDoesNotUseLegacySettings(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{"legacy-model"}}
	h := &AccountHandler{cfg: cfg}
	for _, kind := range []string{service.AccountTypeOAuth, service.AccountTypeSetupToken} {
		account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: kind}
		require.Empty(t, h.accountResponseFromService(account).CodexTurnTickets)
		require.Empty(t, h.accountListResponseFromService(account).CodexTurnTickets)
	}
}

func TestCodexTicketJobResultKeepsRuntimeOutcome(t *testing.T) {
	observation := &extensionv1.CodexRoutingObservation{Stage: "acquire", Code: "routing_upstream", HTTPStatus: 429, UpstreamErrorCode: "rate_limit_exceeded", UpstreamErrorMessage: "Rate limit reached", RequestID: "req_fixture", UpstreamBody: `{"error":{"code":"rate_limit_exceeded"}}`}
	result := service.CodexTicketResult{Code: "routing_upstream", HTTPStatus: 429, Message: service.CodexTicketFailure("routing_upstream").Message, Observation: observation,
		Facts: []extensionv1.DisplayFact{{Label: map[string]string{"en": "Upstream status"}, Value: "429"}, {Label: map[string]string{"en": "Upstream response body"}, Value: observation.UpstreamBody}}}
	failure := codexTicketJobFailure(11, result.Code, result.Detail(), codexTicketJobMetadata(7, "gpt-6-astra", result), false)
	require.Equal(t, service.AccountJobItemStatusFailed, failure.Status)
	require.Equal(t, "routing_upstream", failure.ErrorCode, "the real routing code is kept")
	require.Equal(t, "Rate limit reached (code=rate_limit_exceeded)", failure.ErrorMessage, "the verbatim upstream text is the item error")
	require.NoError(t, service.ValidateAccountJobMetadata(failure.Metadata), "the job runtime accepts the complete outcome")
	var stored map[string]any
	require.NoError(t, json.Unmarshal(failure.Metadata, &stored))
	require.Equal(t, "routing_upstream", stored["code"])
	require.EqualValues(t, 429, stored["http_status"])
	require.Contains(t, stored["message"], result.Message)
	require.Contains(t, stored["message"], "Rate limit reached")
	require.Len(t, stored["facts"], 2)
	require.Contains(t, string(failure.Metadata), "req_fixture")
	require.Equal(t, 1, strings.Count(string(failure.Metadata), `rate_limit_exceeded\"}}`), "the upstream body is stored once")
	require.NotContains(t, stored["message"], observation.UpstreamBody, "message never falls back to the body")
}

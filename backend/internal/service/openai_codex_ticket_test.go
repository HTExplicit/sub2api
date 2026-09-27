package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/codexruntime/tickets"
	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func fakeCodexTicketState(n int) string {
	if n < 6 {
		return strings.Repeat("A", n)
	}
	return "gAAAAA" + strings.Repeat("B", n-6)
}
func ticketTestAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "tok", "chatgpt_account_id": "acc-1"}}
}

func ticketTestService(t *testing.T, cfg config.OpenAICodexTicketConfig, upstream HTTPUpstream) *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: cfg}}, httpUpstream: upstream, nativeCodexRuntime: nativeTicketTestRuntime(t, cfg, nil)}
}

func setTicketTestProjection(account *Account, model string, expiry time.Time) {
	projection := NativeCodexAccountProjection{Identity: CodexTicketAccountIdentity(account), Scheduling: map[string]extensionv1.SchedulingConstraint{model: {Model: model, Effect: "allow", Until: &expiry, Reason: "routing_verified"}}}
	raw, _ := json.Marshal(projection)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	account.Extra = map[string]any{NativeCodexAccountProjectionKey: map[string]any{codexRuntimePluginKey: value}}
}

func TestCodexTicketFacadeUsesNativeWithoutLegacyFallback(t *testing.T) {
	cfg := config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}
	account := ticketTestAccount(41)
	account.Extra = map[string]any{openAICodexTicketExtraKey("gpt-6-astra"): map[string]any{"state": fakeCodexTicketState(292), "expires_at": time.Now().Add(time.Hour)}}
	var observed extensionv1.SchedulingRequest
	manager := nativeTicketTestRuntime(t, cfg, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		require.Equal(t, extensionv1.CapabilityRequest, in.Capability)
		require.Equal(t, "inject", in.Operation)
		require.NoError(t, json.Unmarshal(in.Payload, &observed))
		raw, _ := json.Marshal(map[string]any{"headers": map[string]string{openAICodexTurnStateHeader: "plugin-owned-ticket"}})
		return extensionv1.Result{Payload: raw}, nil
	})
	service := &OpenAIGatewayService{nativeCodexRuntime: manager}
	headers := http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"caller-ticket"}}
	require.NoError(t, service.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers))
	require.Equal(t, "plugin-owned-ticket", headers.Get(openAICodexTurnStateHeader))
	require.Equal(t, account.ID, observed.Account.ID)
	require.Equal(t, CodexTicketAccountIdentity(account), observed.Account.Identity)
	require.Equal(t, "gpt-6-astra", observed.Model)
	manager.current().cancel()
	require.ErrorIs(t, service.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", http.Header{}), ErrOpenAICodexTicketUnavailable)
	require.True(t, service.openAICodexTicketBlocksAccount(account, "gpt-6-astra"), "legacy Extra cannot bypass a stopped native runtime")
	service.nativeCodexRuntime = nativeTicketTestRuntime(t, config.OpenAICodexTicketConfig{Enabled: false}, func(extensionv1.Invocation) (extensionv1.Result, error) {
		return extensionv1.Result{Payload: json.RawMessage(`{"headers":{}}`)}, nil
	})

	headers = http.Header{}
	require.NoError(t, service.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers))
	require.Empty(t, headers)
	require.False(t, service.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
}
func TestCodexTicketNativePolicyFailureDoesNotFallBack(t *testing.T) {
	manager := nativeTicketTestRuntime(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, func(extensionv1.Invocation) (extensionv1.Result, error) {
		return extensionv1.Result{}, errors.New("offline fixture")
	})
	service := &OpenAIGatewayService{nativeCodexRuntime: manager}
	require.ErrorIs(t, service.applyOpenAICodexTicket(context.Background(), ticketTestAccount(41), "gpt-6-astra", http.Header{}), ErrOpenAICodexTicketUnavailable)
	// A runtime refusal keeps its own result code and reason.
	service.nativeCodexRuntime = nativeTicketTestRuntime(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, func(extensionv1.Invocation) (extensionv1.Result, error) {
		return extensionv1.Result{Code: "ticket_missing", Message: "no route qualification is recorded (routing phase stopped; last result routing_upstream)"}, nil
	})
	err := service.applyOpenAICodexTicket(context.Background(), ticketTestAccount(41), "gpt-6-astra", http.Header{})
	require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
	require.ErrorIs(t, err, ErrNativeCodexRuntimeUnavailable)
	var refusal *NativeCodexResultError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "ticket_missing", refusal.Code)
	require.Contains(t, err.Error(), "last result routing_upstream")
}
func TestCodexTicketInvokeFailureUsesTheJobContext(t *testing.T) {
	running := context.Background()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.Equal(t, "ticket_plugin_unavailable", codexTicketInvokeFailure(running, tickets.ErrStopping).Code, "a runtime reload is not a job cancellation")
	require.Equal(t, "ticket_plugin_unavailable", codexTicketInvokeFailure(running, context.Canceled).Code)
	require.Equal(t, "ticket_canceled", codexTicketInvokeFailure(canceled, context.Canceled).Code)
	require.Equal(t, "ticket_timeout", codexTicketInvokeFailure(running, context.DeadlineExceeded).Code)
}

func TestOpenAICodexTicketStatusesUsesSanitizedProjection(t *testing.T) {
	account := ticketTestAccount(41)
	now := time.Now()
	setTicketTestProjection(account, "gpt-6-astra", now.Add(time.Hour))
	cfg := config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{"gpt-6-astra", "gpt-5.6-sol"}}
	statuses := OpenAICodexTicketStatuses(account, cfg, now)
	require.Len(t, statuses, 2)
	require.True(t, statuses[0].Ready)
	require.EqualValues(t, 3600, statuses[0].RemainingSeconds)
	require.True(t, statuses[1].Blocked)
	account.Credentials["chatgpt_account_id"] = "changed"
	require.True(t, OpenAICodexTicketStatuses(account, cfg, now)[0].Blocked)
	cfg.Enabled = false
	require.Empty(t, OpenAICodexTicketStatuses(account, cfg, now))
	// With the switch off, recorded history stays visible with its real code,
	// HTTP status, response model and original message; nothing is ready or blocked.
	account = ticketTestAccount(41)
	setTicketTestProjection(account, "gpt-6-astra", now.Add(time.Hour))
	plugins, ok := account.Extra[NativeCodexAccountProjectionKey].(map[string]any)
	require.True(t, ok)
	projection, ok := plugins[codexRuntimePluginKey].(map[string]any)
	require.True(t, ok)
	projection["observations"] = map[string]any{"gpt-6-astra": map[string]any{"key": "gpt-6-astra", "kind": "codex_routing", "state": "retry", "code": "routing_persist", "http_status": 503, "response_model": "gpt-6-luna", "message": "save cookie clock: database is locked"}}
	statuses = OpenAICodexTicketStatuses(account, cfg, now)
	require.Len(t, statuses, 1)
	require.False(t, statuses[0].Ready)
	require.False(t, statuses[0].Blocked)
	require.Equal(t, "retry", statuses[0].RenewalState)
	require.NotNil(t, statuses[0].LastResult)
	require.Equal(t, "routing_persist", statuses[0].LastResult.Code, "a code outside the old catalog is not rewritten")
	require.Equal(t, 503, statuses[0].LastResult.HTTPStatus)
	require.Equal(t, "gpt-6-luna", statuses[0].LastResult.ResponseModel)
	require.Equal(t, CodexTicketFailure("routing_persist").Message, statuses[0].LastResult.Message, "the short catalog sentence")
	require.Equal(t, "save cookie clock: database is locked", statuses[0].LastResult.Error, "the raw text")
}
func TestExtractOpenAICodexTicketModel(t *testing.T) {
	require.Equal(t, "gpt-6-astra", extractOpenAICodexTicketModel([]byte(`{"model":" gpt-6-astra "}`)))
}
func TestOpenAICodexTicketGate_CompactRequestUsesForwardOutboundModel(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{"gpt-6-astra"}}, nil)
	svc.cfg.Gateway.OpenAICompactModel = "gpt-5.5"
	account := ticketTestAccount(41)
	require.Equal(t, "gpt-6-astra", svc.openAICodexTicketOutboundModel(account, "gpt-6-astra", false))
	require.Equal(t, "gpt-5.5", svc.openAICodexTicketOutboundModel(account, "gpt-6-astra", true))
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", false))
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", true))
}

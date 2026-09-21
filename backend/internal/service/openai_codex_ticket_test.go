package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
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

// Host tests stub only the process boundary. Ticket validation, acquisition and
// renewal are tested against the independent module in plugins/codex-runtime.
type ticketExtensionTestConn struct {
	grpc.ClientConnInterface
	invoke func(extensionv1.Invocation) (extensionv1.Result, error)
}

func (c *ticketExtensionTestConn) Invoke(_ context.Context, _ string, args, reply any, _ ...grpc.CallOption) error {
	var request extensionv1.Invocation
	if err := json.Unmarshal(args.(*wrapperspb.BytesValue).Value, &request); err != nil {
		return err
	}
	result, err := c.invoke(request)
	if err != nil {
		return err
	}
	reply.(*wrapperspb.BytesValue).Value, err = json.Marshal(result)
	return err
}
func ticketTestManager(t *testing.T, cfg config.OpenAICodexTicketConfig, invoke func(extensionv1.Invocation) (extensionv1.Result, error)) *PluginManager {
	t.Helper()
	if len(cfg.Models) == 0 {
		cfg.Models = []string{"gpt-6-astra", "gpt-5.6-sol"}
	}
	installation := &PluginInstallation{ID: 1, PluginKey: codexRuntimePluginKey}
	for _, capability := range []string{extensionv1.CapabilityScheduling, extensionv1.CapabilityRequest, extensionv1.CapabilityAdmin} {
		for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
			installation.Bindings = append(installation.Bindings, PluginBinding{Capability: capability, Platform: PlatformOpenAI, AccountType: kind, Enabled: true, RolloutPercent: 100})
		}
	}
	if invoke == nil {
		invoke = func(extensionv1.Invocation) (extensionv1.Result, error) {
			return extensionv1.Result{Code: "ticket_missing"}, nil
		}
	}
	runtime := &pluginRuntime{installation: installation, client: &hcplugin.Client{}, extension: extensionv1.NewClient(&ticketExtensionTestConn{invoke: invoke}), done: make(chan struct{})}
	raw, _ := json.Marshal(map[string]any{"enabled": cfg.Enabled, "fail_closed": cfg.FailClosed, "models": cfg.Models, "proxy_url": cfg.HarvestProxyURL})
	snapshot := json.RawMessage(raw)
	runtime.configSnapshot.Store(&snapshot)
	rules := []extensionv1.SchedulingRule{}
	if cfg.Enabled && cfg.FailClosed {
		rules = append(rules, extensionv1.SchedulingRule{Models: cfg.Models, Default: "deny", Reason: "ticket_missing", ExcludeShadows: true})
	}
	runtime.scheduling.Store(&rules)
	manager := &PluginManager{}
	manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{1: installation}, runtimes: map[int64]*pluginRuntime{1: runtime}})
	return manager
}
func ticketTestService(t *testing.T, cfg config.OpenAICodexTicketConfig, upstream HTTPUpstream) *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: cfg}}, httpUpstream: upstream, pluginManager: ticketTestManager(t, cfg, nil)}
}
func setTicketTestProjection(account *Account, model string, expiry time.Time) {
	projection := PluginAccountProjection{Identity: CodexTicketAccountIdentity(account), Scheduling: map[string]extensionv1.SchedulingConstraint{model: {Model: model, Effect: "allow", Until: &expiry, Reason: "ticket_ready"}}}
	raw, _ := json.Marshal(projection)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	account.Extra = map[string]any{PluginAccountProjectionKey: map[string]any{codexRuntimePluginKey: value}}
}
func TestCodexTicketFacadeUsesPluginWithoutLegacyFallback(t *testing.T) {
	cfg := config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}
	account := ticketTestAccount(41)
	account.Extra = map[string]any{openAICodexTicketExtraKey("gpt-6-astra"): map[string]any{"state": fakeCodexTicketState(292), "expires_at": time.Now().Add(time.Hour)}}
	var observed extensionv1.SchedulingRequest
	manager := ticketTestManager(t, cfg, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		require.Equal(t, extensionv1.CapabilityRequest, in.Capability)
		require.Equal(t, "inject", in.Operation)
		require.NoError(t, json.Unmarshal(in.Payload, &observed))
		raw, _ := json.Marshal(map[string]any{"headers": map[string]string{openAICodexTurnStateHeader: "plugin-owned-ticket"}})
		return extensionv1.Result{Payload: raw}, nil
	})
	service := &OpenAIGatewayService{pluginManager: manager}
	headers := http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"caller-ticket"}}
	require.NoError(t, service.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers))
	require.Equal(t, "plugin-owned-ticket", headers.Get(openAICodexTurnStateHeader))
	require.Equal(t, account.ID, observed.Account.ID)
	require.Equal(t, CodexTicketAccountIdentity(account), observed.Account.Identity)
	require.Equal(t, "gpt-6-astra", observed.Model)
	registry := manager.extensions.Load()
	registry.runtimes = map[int64]*pluginRuntime{}
	manager.extensions.Store(registry)
	require.ErrorIs(t, service.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", http.Header{}), ErrOpenAICodexTicketUnavailable)
	require.True(t, service.openAICodexTicketBlocksAccount(account, "gpt-6-astra"), "legacy Extra must not bypass an unavailable plugin")
	for i := range registry.installations[1].Bindings {
		registry.installations[1].Bindings[i].Enabled = false
	}
	manager.extensions.Store(registry)
	headers = http.Header{}
	require.NoError(t, service.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers))
	require.Empty(t, headers)
	require.False(t, service.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
}
func TestCodexTicketPluginRPCFailureDoesNotFallBack(t *testing.T) {
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, func(extensionv1.Invocation) (extensionv1.Result, error) {
		return extensionv1.Result{}, errors.New("offline fixture")
	})
	service := &OpenAIGatewayService{pluginManager: manager}
	require.ErrorIs(t, service.applyOpenAICodexTicket(context.Background(), ticketTestAccount(41), "gpt-6-astra", http.Header{}), ErrOpenAICodexTicketUnavailable)
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

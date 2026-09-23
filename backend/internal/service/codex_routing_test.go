package service

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type routingMemoryStore struct {
	PluginRepository
	mu     sync.Mutex
	values map[string]extensionv1.StateResult
	next   map[string]time.Time
}

func (store *routingMemoryStore) ReadExtensionState(_ context.Context, _ string, req extensionv1.StateRequest) (extensionv1.StateResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.values[req.Namespace+":"+req.Key], nil
}
func (store *routingMemoryStore) CompareSwapExtensionState(_ context.Context, _ string, req extensionv1.StateRequest) (extensionv1.StateResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.values == nil {
		store.values = map[string]extensionv1.StateResult{}
	}
	key := req.Namespace + ":" + req.Key
	old := store.values[key]
	old.Applied = false
	if old.Revision == req.ExpectedRevision {
		old = extensionv1.StateResult{Found: true, Applied: true, Revision: old.Revision + 1, Value: append(json.RawMessage(nil), req.Value...)}
		store.values[key] = old
		if store.next == nil {
			store.next = map[string]time.Time{}
		}
		if req.NextAt != nil {
			store.next[key] = *req.NextAt
		} else {
			delete(store.next, key)
		}
	}
	return old, nil
}
func (store *routingMemoryStore) DueExtensionStates(_ context.Context, _ string, request extensionv1.DueStateRequest) ([]extensionv1.DueState, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []extensionv1.DueState
	for key, at := range store.next {
		if strings.HasPrefix(key, request.Namespace+":") && !time.Now().Before(at) {
			record := store.values[key]
			out = append(out, extensionv1.DueState{Key: strings.TrimPrefix(key, request.Namespace+":"), Revision: record.Revision, Value: record.Value})
			if len(out) >= request.Limit {
				break
			}
		}
	}
	return out, nil
}
func (*routingMemoryStore) AcquireExtensionLease(context.Context, string, extensionv1.LeaseRequest) (extensionv1.LeaseResult, error) {
	return extensionv1.LeaseResult{Acquired: true, Generation: 1}, nil
}
func (*routingMemoryStore) ReleaseExtensionLease(context.Context, string, extensionv1.LeaseRequest) (extensionv1.LeaseResult, error) {
	return extensionv1.LeaseResult{}, nil
}

type routingHostDirectoryFixture struct {
	PluginAccountDirectory
	account  extensionv1.Account
	scope    extensionv1.CodexRoutingScope
	response string
	requests int
	onSend   func()
}

type routingAccountRepositoryFixture struct {
	AccountRepository
	account *Account
}

func (r *routingAccountRepositoryFixture) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestCodexRoutingValidationDoesNotEnrollOrChangeStoppedState(t *testing.T) {
	host, directory, store := routingHostFixture()
	installation := host.installation
	installation.ID, installation.State, installation.Version = 1, PluginStateEnabled, "0.2.7"
	installation.Manifest.Requires.ExtensionAPI = 1
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityCredentials, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100}}
	store.PluginRepository = &pluginTokenRepository{installation: installation}
	manager := NewPluginManager(store, nil, nil, PluginHostInfo{}, newFakePluginKVStore())
	manager.accountDirectory = directory
	manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{1: installation}, runtimes: map[int64]*pluginRuntime{}})
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	service := &OpenAIGatewayService{pluginManager: manager, accountRepo: &routingAccountRepositoryFixture{account: account}}
	legacyKey := "tickets:7." + codexRoutingDigest("gpt-6-astra")
	stopped := json.RawMessage(`{"schema":2,"phase":"stopped","enrolled":true,"failures":2}`)
	store.values[legacyKey] = extensionv1.StateResult{Found: true, Revision: 8, Value: stopped}
	request := CodexRoutingValidationRequest{ValidationID: "e753ac46-3a37-4b7b-ab74-c6fdb987ccec", Model: "gpt-6-astra", Transport: "http"}
	result, err := service.ValidateCodexRouting(context.Background(), 7, request)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.False(t, result.Enrolled)
	require.Equal(t, 2, result.BudgetUsed)
	require.Equal(t, 2, directory.requests)
	require.Equal(t, stopped, store.values[legacyKey].Value)
	require.EqualValues(t, 8, store.values[legacyKey].Revision)
	result, err = service.ValidateCodexRouting(context.Background(), 7, request)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, 2, directory.requests)
	require.Equal(t, 2, result.BudgetUsed)
}

func (d *routingHostDirectoryFixture) ReadExtensionAccount(context.Context, int64) (*extensionv1.Account, error) {
	copy := d.account
	return &copy, nil
}
func (d *routingHostDirectoryFixture) ListExtensionAccounts(context.Context, extensionv1.AccountQuery) ([]extensionv1.Account, error) {
	return []extensionv1.Account{d.account}, nil
}
func (d *routingHostDirectoryFixture) ResolveExtensionIdentity(context.Context, extensionv1.AccountQuery) (*extensionv1.OutboundIdentity, error) {
	panic("routing probes must not expose credentials")
}
func (d *routingHostDirectoryFixture) PrepareCodexRoutingScope(context.Context, int64, string) (extensionv1.CodexRoutingScope, error) {
	return d.scope, nil
}
func (d *routingHostDirectoryFixture) ExecuteCodexRoutingProbe(_ context.Context, q extensionv1.CodexRoutingQuery, cookies string, _ time.Time) (*http.Response, extensionv1.CodexRoutingScope, error) {
	d.requests++
	if d.onSend != nil {
		d.onSend()
	}
	headers := http.Header{"Content-Type": []string{"text/event-stream"}, "Set-Cookie": []string{"__cflb=synthetic-route; Path=/; Secure; Max-Age=100", "__oailb=synthetic-lb; Path=/; Secure; Max-Age=100"}}
	scope := d.scope
	if q.Stage == "verify" {
		if cookies == "" {
			return nil, scope, errCodexRoutingUnavailable
		}
		scope.ConnectionLeaseID = "actual-connection"
		scope.RouteEvidence = "connection"
	}
	return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(d.response))}, scope, nil
}

func routingHostFixture() (*pluginExtensionHost, *routingHostDirectoryFixture, *routingMemoryStore) {
	directory := &routingHostDirectoryFixture{account: extensionv1.Account{ID: 7, Identity: "owner", Platform: PlatformOpenAI, Type: AccountTypeOAuth}, scope: extensionv1.CodexRoutingScope{AccountID: 7, Identity: "owner", RouteHash: "route", ProfileHash: "profile", Transport: "http"}, response: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}\n\n"}
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	installation := &PluginInstallation{PluginKey: codexRuntimePluginKey, Manifest: PluginManifest{Capabilities: []PluginCapability{{ID: extensionv1.CapabilityCredentials, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}}}}
	return &pluginExtensionHost{key: codexRuntimePluginKey, state: store, directory: directory, installation: installation}, directory, store
}

func TestCodexRoutingCookieScopeAgeDeletionAndColdCandidate(t *testing.T) {
	now := time.Now().UTC()
	target, _ := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	headers := http.Header{"Set-Cookie": []string{"__cflb=a; Path=/; Secure; Max-Age=60", "__oailb=b; Domain=attacker.test; Path=/", "auth_token=secret; Path=/", "__oailb=c; Path=/elsewhere"}}
	changes := parseCodexRoutingCookies(headers, target, now)
	require.Len(t, changes, 1)
	for _, malformed := range []string{"__cflb=x; Domain=bad domain; Path=/", "__cflb=x; Path=\"/broken; Secure"} {
		require.Empty(t, parseCodexRoutingCookies(http.Header{"Set-Cookie": []string{malformed}}, target, now), "malformed scope cannot become a host-only cookie")
	}
	clock := codexRoutingCookieClock{}
	clock.apply(changes, now)
	clock.apply(parseCodexRoutingCookies(headers, target, now.Add(20*time.Second)), now.Add(20*time.Second))
	require.Equal(t, now, clock.Cookies["__cflb"].FirstSeen)
	require.Empty(t, clock.selected(nil, nil, now), "cold candidate must not inherit another response")
	clock.apply(parseCodexRoutingCookies(http.Header{"Set-Cookie": []string{"__cflb=; Max-Age=0; Path=/"}}, target, now.Add(30*time.Second)), now.Add(30*time.Second))
	clock.apply(changes, now.Add(40*time.Second))
	require.Empty(t, clock.live(now.Add(40*time.Second)), "old response resurrected a tombstone")
	clock.apply(parseCodexRoutingCookies(headers, target, now.Add(5*time.Minute)), now.Add(5*time.Minute))
	require.Empty(t, clock.live(now.Add(5*time.Minute)), "same value renewed after TTL")
}

func TestCodexRoutingCompletionRequiresOriginalModelAndDelimitedTerminal(t *testing.T) {
	for _, test := range []struct {
		name, body string
		ok         bool
	}{
		{"complete", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}\n\n", true},
		{"model_mismatch", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-luna\"}}\n\n", false},
		{"missing_model", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", false},
		{"truncated", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}", false},
		{"done_only", "data: [DONE]\n\n", false},
		{"error", "data: {\"type\":\"error\",\"error\":{\"code\":\"server_is_overloaded\"}}\n\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var o extensionv1.CodexRoutingObservation
			err := readCodexRoutingCompletion(strings.NewReader(test.body), "text/event-stream", "gpt-6-astra", &o)
			require.Equal(t, test.ok, err == nil)
			require.Equal(t, test.ok, o.ModelMatched)
		})
	}
	var untyped extensionv1.CodexRoutingObservation
	stream := `event: response.completed
data: {"type":"response.completed","response":{"status":"completed","model":"gpt-6-astra"}}

`
	require.NoError(t, readCodexRoutingCompletion(strings.NewReader(stream), "", "gpt-6-astra", &untyped), "ChatGPT streams arrive without Content-Type")
}

func TestCodexRoutingHostSeparatesCandidateQualifiedAndPrivateMaterial(t *testing.T) {
	host, directory, _ := routingHostFixture()
	call := func(query extensionv1.CodexRoutingQuery) extensionv1.CodexRoutingProbeResult {
		raw, _ := json.Marshal(query)
		result, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingProbe, Payload: raw})
		require.NoError(t, err)
		require.NotContains(t, string(result.Payload), "synthetic-route")
		require.NotContains(t, string(result.Payload), "synthetic-lb")
		var value extensionv1.CodexRoutingProbeResult
		require.NoError(t, json.Unmarshal(result.Payload, &value))
		return value
	}
	query := extensionv1.CodexRoutingQuery{AccountID: 7, Model: "gpt-6-astra", Transport: "http", OperationID: "one", Stage: "acquire"}
	candidate := call(query)
	require.True(t, candidate.Valid)
	require.Empty(t, candidate.Scope.ConnectionLeaseID)
	query.Stage, query.Bundle = "verify", candidate.Bundle
	verified := call(query)
	require.True(t, verified.Valid)
	require.NotEmpty(t, verified.Scope.ConnectionLeaseID)
	require.Equal(t, 2, directory.requests)
	duplicate := call(query)
	require.False(t, duplicate.Valid)
	require.Equal(t, 2, directory.requests)
	raw, _ := json.Marshal(extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: candidate.Bundle.Key})
	_, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostStateRead, Payload: raw})
	require.Error(t, err)
}

func TestCodexRoutingHostRejectsMismatchedModelAndChangedOwner(t *testing.T) {
	for _, changeOwner := range []bool{false, true} {
		host, directory, _ := routingHostFixture()
		if changeOwner {
			directory.onSend = func() { directory.scope.Identity = "new-owner" }
		} else {
			directory.response = strings.ReplaceAll(directory.response, "gpt-6-astra", "gpt-6-luna")
		}
		raw, _ := json.Marshal(extensionv1.CodexRoutingQuery{AccountID: 7, Model: "gpt-6-astra", Transport: "http", OperationID: "one", Stage: "acquire"})
		result, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingProbe, Payload: raw})
		require.NoError(t, err)
		var value extensionv1.CodexRoutingProbeResult
		require.NoError(t, json.Unmarshal(result.Payload, &value))
		if changeOwner {
			require.False(t, value.Valid)
			require.Nil(t, value.Bundle)
		} else {
			require.True(t, value.Valid)
			require.NotNil(t, value.Bundle)
			require.False(t, value.Observation.ModelMatched)
			require.Empty(t, value.Scope.ConnectionLeaseID)
			query := extensionv1.CodexRoutingQuery{AccountID: 7, Model: "gpt-6-astra", Transport: "http", OperationID: "one", Stage: "verify", Bundle: value.Bundle}
			raw, _ = json.Marshal(query)
			result, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingProbe, Payload: raw})
			require.NoError(t, err)
			var verified extensionv1.CodexRoutingProbeResult
			require.NoError(t, json.Unmarshal(result.Payload, &verified))
			require.False(t, verified.Valid)
			require.Nil(t, verified.Bundle)
		}
	}
}

func TestCodexRoutingValidationBudgetSevenStagesAndOneOwner(t *testing.T) {
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	ctx := context.Background()
	run := "22b6424b-eeb4-4a34-80f4-5f014e04e804"
	for _, model := range codexValidationModels {
		for _, stage := range []string{"acquire", "verify"} {
			bound := context.WithValue(ctx, codexValidationContextKey{}, codexValidationContext{ID: run, AccountID: 7, Model: model, Transport: "http"})
			query := extensionv1.CodexRoutingQuery{AccountID: 7, Model: model, Transport: "http", Stage: stage}
			require.NoError(t, consumeCodexValidationBudget(bound, store, codexRuntimePluginKey, query))
			require.Error(t, consumeCodexValidationBudget(bound, store, codexRuntimePluginKey, query))
		}
	}
	bound := context.WithValue(ctx, codexValidationContextKey{}, codexValidationContext{ID: run, AccountID: 7, Model: "gpt-6-astra", Transport: "ws"})
	query := extensionv1.CodexRoutingQuery{AccountID: 7, Model: "gpt-6-astra", Transport: "ws", Stage: "verify"}
	require.NoError(t, consumeCodexValidationBudget(bound, store, codexRuntimePluginKey, query))
	require.Error(t, consumeCodexValidationBudget(bound, store, codexRuntimePluginKey, query))
	bound = context.WithValue(ctx, codexValidationContextKey{}, codexValidationContext{ID: run, AccountID: 8, Model: "gpt-6-astra", Transport: "http"})
	query.AccountID, query.Transport = 8, "http"
	require.Error(t, consumeCodexValidationBudget(bound, store, codexRuntimePluginKey, query))
	record, _ := store.ReadExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "validation." + run})
	var budget codexValidationBudget
	require.NoError(t, json.Unmarshal(record.Value, &budget))
	require.Equal(t, 7, budget.Used)
}

func TestCodexRoutingTurnStateCannotCrossLogicalTurn(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("session-id", "session")
	s := &OpenAIGatewayService{}
	account := &Account{ID: 7}
	stageCodexRoutingTurn(c, []byte(`{"client_metadata":{"turn_id":"turn-one"}}`))
	s.noteOpenAICodexTurnStateProvenance(c, account)
	headers := http.Header{}
	headers.Set(openAICodexTurnStateHeader, "opaque")
	s.guardOpenAICodexTurnStateEcho(c, account, headers)
	require.Equal(t, "opaque", headers.Get(openAICodexTurnStateHeader))
	stageCodexRoutingTurn(c, []byte(`{"client_metadata":{"turn_id":"turn-two"}}`))
	s.guardOpenAICodexTurnStateEcho(c, account, headers)
	require.Empty(t, headers.Get(openAICodexTurnStateHeader))
}

func TestCodexRoutingGenericCookieHeadersStayProtected(t *testing.T) {
	for _, name := range []string{"Cookie", "Set-Cookie"} {
		manager := ticketTestManager(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, func(extensionv1.Invocation) (extensionv1.Result, error) {
			raw, _ := json.Marshal(extensionv1.CodexRoutingInjection{Headers: map[string]string{name: "__cflb=not-host-owned"}})
			return extensionv1.Result{Payload: raw}, nil
		})
		headers := http.Header{"X-Original": []string{"preserve"}}
		err := manager.ApplyRequestHeaders(context.Background(), ticketTestAccount(7), "gpt-6-astra", headers)
		require.Error(t, err)
		require.Equal(t, http.Header{"X-Original": []string{"preserve"}}, headers)
	}
}

func TestCodexRoutingInfrastructureCookieLayerIsConnectionScoped(t *testing.T) {
	scope := extensionv1.CodexRoutingScope{AccountID: 7, Identity: "owner", ProfileHash: "profile", RouteHash: "route", ConnectionLeaseID: "infra-test-connection"}
	expires := time.Now().Add(time.Minute)
	observeCodexInfrastructureCookies(scope, http.Header{"Set-Cookie": []string{"__cf_bm=infra; Path=/; Secure", "__oailb=recovery; Path=/; Secure", "session_token=secret; Path=/; Secure"}}, expires)
	request, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", nil)
	request.Header.Set("Cookie", "__oailb=host-owned")
	applyCodexInfrastructureCookies(request, scope, expires)
	require.Contains(t, request.Header.Get("Cookie"), "__cf_bm=infra")
	require.Contains(t, request.Header.Get("Cookie"), "__oailb=host-owned")
	require.NotContains(t, request.Header.Get("Cookie"), "session_token")
	scope.ConnectionLeaseID = "other-connection"
	next, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", nil)
	applyCodexInfrastructureCookies(next, scope, expires)
	require.Empty(t, next.Header.Get("Cookie"))
}

func TestCodexRoutingDeviceChangeInvalidatesQualifiedScopeBeforeSend(t *testing.T) {
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "owner"}, Extra: map[string]any{"openai_device_id": "device-before", codexFingerprintSeedExtraKey: testCodexFingerprintSeed}}
	service := &OpenAIGatewayService{accountRepo: &routingAccountRepositoryFixture{account: account}}
	before, err := service.PrepareCodexRoutingScope(context.Background(), 7, "http")
	require.NoError(t, err)
	account.Extra["openai_device_id"] = "device-after"
	after, err := service.PrepareCodexRoutingScope(context.Background(), 7, "http")
	require.NoError(t, err)
	require.Equal(t, before.Identity, after.Identity)
	require.NotEqual(t, before.ProfileHash, after.ProfileHash)
	require.False(t, before.SameOwner(after))
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	expiry := time.Now().Add(time.Minute)
	raw, _ := json.Marshal(codexRoutingPrivateBundle{Schema: 2, Scope: before, Status: "qualified", Model: "gpt-6-astra", ExpiresAt: expiry})
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "bundle.device-test", Value: raw})
	require.NoError(t, err)
	_, err = readCodexRoutingBundle(context.Background(), store, codexRuntimePluginKey, extensionv1.CodexRoutingBundleRef{Key: "bundle.device-test", Revision: 1, ExpiresAt: expiry}, after, true)
	require.Error(t, err)
}

func TestCodexRoutingAnotherModelClockDoesNotBlockOwnCookieRefresh(t *testing.T) {
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	installation := &PluginInstallation{PluginKey: codexRuntimePluginKey}
	service := &OpenAIGatewayService{pluginManager: &PluginManager{repo: store}}
	scope := extensionv1.CodexRoutingScope{AccountID: 7, Identity: "owner", ProfileHash: "profile", RouteHash: "route", ConnectionLeaseID: "connection-astra", Transport: "http"}
	now := time.Now().Add(-time.Second).UTC()
	expiry := now.Add(time.Minute)
	first := []codexRoutingCookie{{Name: "__cflb", Value: "astra-route", Domain: "chatgpt.com", Path: "/", FirstSeen: now, ExpiresAt: expiry}, {Name: "__oailb", Value: "astra-lb", Domain: "chatgpt.com", Path: "/", FirstSeen: now, ExpiresAt: expiry}}
	clock := codexRoutingCookieClock{Scope: scope}
	for _, cookie := range first {
		clock.apply([]codexRoutingCookieChange{{Cookie: cookie}}, now)
	}
	clockKey := "clock." + codexRoutingScopeKey(scope)
	raw, _ := json.Marshal(clock)
	_, err := store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey, Value: raw})
	require.NoError(t, err)
	bundle := codexRoutingPrivateBundle{Schema: 2, Scope: scope, Cookies: first, ClockKey: clockKey, ClockRevision: 1, Status: "qualified", Model: "gpt-6-astra", ExpiresAt: expiry}
	raw, _ = json.Marshal(bundle)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "bundle.astra", Value: raw})
	require.NoError(t, err)
	// Sol changes the shared clock without revoking Astra's immutable pair.
	second := first[0]
	second.Value = "sol-route"
	clock.apply([]codexRoutingCookieChange{{Cookie: second}}, now)
	raw, _ = json.Marshal(clock)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey, ExpectedRevision: 1, Value: raw})
	require.NoError(t, err)
	q := &extensionv1.CodexRoutingQualification{Scope: scope, Model: "gpt-6-astra", VerifiedAt: now, ExpiresAt: expiry, Bundle: extensionv1.CodexRoutingBundleRef{Key: "bundle.astra", Revision: 1, ExpiresAt: expiry}}
	next := service.refreshObservedCodexCookies(context.Background(), installation, q, http.Header{"Set-Cookie": []string{"__oailb=astra-refreshed; Path=/; Secure; Max-Age=100"}}, extensionv1.CodexRoutingObservation{ObservedAt: time.Now().UTC(), Completed: true, ModelMatched: true, ResponseModel: "gpt-6-astra"})
	require.NotNil(t, next)
	require.Equal(t, q.ExpiresAt, next.ExpiresAt)
	updated, err := readCodexRoutingBundle(context.Background(), store, codexRuntimePluginKey, next.Bundle, scope, true)
	require.NoError(t, err)
	header, _ := codexRoutingCookieHeader(updated.Cookies, time.Now())
	require.Contains(t, header, "__cflb=astra-route")
	require.Contains(t, header, "__oailb=astra-refreshed")
	require.NotContains(t, header, "sol-route")
}

func TestCodexRoutingObservesOrdinaryHTTPAndNativeFramesWithoutCookies(t *testing.T) {
	account := ticketTestAccount(7)
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{Enabled: false}, nil)
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	manager.repo = store
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Proto: "HTTP/2.0", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}}
	service := &OpenAIGatewayService{httpUpstream: upstream, pluginManager: manager}
	request, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-5.5","input":"private-prompt","client_metadata":{"session_id":"session"}}`))
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("User-Agent", "actual-wire-agent")
	request.Header.Set("session-id", "session")
	response, err := service.doOpenAICodexUpstream(request, account, "")
	require.NoError(t, err)
	_ = response.Body.Close()
	record, err := store.ReadExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "wire.7"})
	require.NoError(t, err)
	var observation CodexWireFingerprint
	require.NoError(t, json.Unmarshal(record.Value, &observation))
	require.Equal(t, "http", observation.Transport)
	require.Equal(t, "actual-wire-agent", observation.UserAgent)
	require.Equal(t, "match", observation.IdentityFields["session"].Consistency)
	require.NotContains(t, string(record.Value), "private-token")
	require.NotContains(t, string(record.Value), "private-prompt")
	frames := newStagedPassthroughConn()
	observed := &codexObservedNativeFrameConn{FrameConn: frames, observe: func(ctx context.Context, body []byte) {
		service.observeNativeCodexWS(ctx, account, request.Header, http.Header{"Sec-Websocket-Extensions": []string{"permessage-deflate"}}, body, "native-socket")
	}}
	require.NoError(t, observed.WriteFrame(context.Background(), websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5.5","client_metadata":{"session_id":"session"}}`)))
	record, err = store.ReadExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "wire.7"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(record.Value, &observation))
	require.Equal(t, "ws", observation.Transport)
	require.Equal(t, "ws", observation.Ingress)
	require.Equal(t, "unknown", observation.JA3)
	require.Empty(t, observation.TLSVersion)
	require.Equal(t, "match", observation.IdentityFields["session"].Consistency)
}

func TestCodexRoutingUnchangedCookiesDoNotGrowAndExpiryRedactsValues(t *testing.T) {
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	scope := extensionv1.CodexRoutingScope{AccountID: 7, Identity: "owner", ProfileHash: "profile", RouteHash: "route", ConnectionLeaseID: "connection", Transport: "http"}
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	cookie := codexRoutingCookie{Name: "__cflb", Value: "temporary-private-value", Domain: "chatgpt.com", Path: "/", FirstSeen: now, ExpiresAt: expires}
	clock := codexRoutingCookieClock{Scope: scope, Tombstones: map[string]time.Time{"other-name": now}}
	clock.apply([]codexRoutingCookieChange{{Cookie: cookie}}, now)
	clockKey := "clock." + codexRoutingScopeKey(scope)
	bundleKey := fixedCodexRoutingBundleKey(context.Background(), extensionv1.CodexRoutingQuery{AccountID: 7, Model: "gpt-6-astra"}, "live")
	raw, _ := json.Marshal(clock)
	_, err := store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey, Value: raw, NextAt: &expires})
	require.NoError(t, err)
	bundle := codexRoutingPrivateBundle{Schema: 2, Scope: scope, Cookies: []codexRoutingCookie{cookie}, ClockKey: clockKey, ClockRevision: 1, Status: "qualified", Model: "gpt-6-astra", ExpiresAt: expires, Observation: extensionv1.CodexRoutingObservation{Code: "routing_verified", HTTPStatus: 200}}
	raw, _ = json.Marshal(bundle)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundleKey, Value: raw, NextAt: &expires})
	require.NoError(t, err)
	q := &extensionv1.CodexRoutingQualification{Scope: scope, Model: "gpt-6-astra", VerifiedAt: now, ExpiresAt: expires, Bundle: extensionv1.CodexRoutingBundleRef{Key: bundleKey, Revision: 1, ExpiresAt: expires}}
	service := &OpenAIGatewayService{pluginManager: &PluginManager{repo: store}}
	before := len(store.values)
	updated := service.refreshObservedCodexCookies(context.Background(), &PluginInstallation{PluginKey: codexRuntimePluginKey}, q, http.Header{"Set-Cookie": []string{"__cflb=temporary-private-value; Path=/; Secure; Max-Age=100"}}, extensionv1.CodexRoutingObservation{ObservedAt: now})
	require.Nil(t, updated)
	require.Len(t, store.values, before)
	require.EqualValues(t, 1, store.values[codexRoutingPrivateNamespace+":"+clockKey].Revision)
	past := now.Add(-time.Second)
	bundle.ExpiresAt = past
	bundle.Cookies[0].ExpiresAt = past
	raw, _ = json.Marshal(bundle)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundleKey, ExpectedRevision: 1, Value: raw, NextAt: &past})
	require.NoError(t, err)
	clock.Cookies["__cflb"] = bundle.Cookies[0]
	raw, _ = json.Marshal(clock)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey, ExpectedRevision: 1, Value: raw, NextAt: &past})
	require.NoError(t, err)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "spent.keep", Value: json.RawMessage(`{"spent":true}`)})
	require.NoError(t, err)
	host := &pluginExtensionHost{key: codexRuntimePluginKey, state: store}
	_, err = host.redactExpiredCodexRoutingMaterial(context.Background())
	require.NoError(t, err)
	require.Len(t, store.values, before+1)
	require.NotContains(t, string(store.values[codexRoutingPrivateNamespace+":"+bundleKey].Value), "temporary-private-value")
	require.Contains(t, string(store.values[codexRoutingPrivateNamespace+":"+bundleKey].Value), "routing_verified")
	require.Contains(t, string(store.values[codexRoutingPrivateNamespace+":"+clockKey].Value), "other-name")
	require.Equal(t, json.RawMessage(`{"spent":true}`), store.values[codexRoutingPrivateNamespace+":spent.keep"].Value)
}

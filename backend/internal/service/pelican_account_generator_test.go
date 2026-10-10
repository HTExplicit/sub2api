//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type pelicanGeneratorAccountRepo struct {
	AccountRepository
	accounts map[int64]*Account
	reads    []int64
}

func (r *pelicanGeneratorAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.reads = append(r.reads, id)
	if account := r.accounts[id]; account != nil {
		return account, nil
	}
	return nil, errors.New("missing local test account")
}

type pelicanGeneratorUpstream struct {
	status      int
	body        string
	contentType string
	requests    []*http.Request
	bodies      [][]byte
	accountIDs  []int64
	proxies     []string
	profiles    []*tlsfingerprint.Profile
	beforeSend  func(*http.Request)
}

func (s *pelicanGeneratorUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxy, id, concurrency, nil)
}

func (s *pelicanGeneratorUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if s.beforeSend != nil {
		s.beforeSend(req)
	}
	prepared, finish, err := PreparePelicanHTTPRequest(req, id, concurrency, "http")
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(prepared.Body)
	if err != nil {
		return finish(nil, err)
	}
	prepared.Body = io.NopCloser(bytes.NewReader(body))
	s.requests = append(s.requests, prepared)
	s.bodies = append(s.bodies, body)
	s.accountIDs = append(s.accountIDs, id)
	s.proxies = append(s.proxies, proxy)
	s.profiles = append(s.profiles, profile)
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	contentType := s.contentType
	if contentType == "" {
		contentType = "text/event-stream"
	}
	return finish(&http.Response{StatusCode: status, Proto: "HTTP/1.1", Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(s.body))}, nil)
}

func TestPelicanAccountGeneratorQueuePreservesSharedFirstOutputPolicy(t *testing.T) {
	clock := time.Now().UTC()
	state := newPelicanExecutionState(context.Background(), newPelicanExecutionCoordinator(1), 10*time.Minute, func() time.Time { return clock })
	defer state.finish()
	ctx := context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
	account := &Account{ID: 70, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "local-key", "base_url": "http://fake.example"}}
	upstream := &pelicanGeneratorUpstream{body: pelicanResponsesSSE("gpt-6.1-sol", "completed", "</html>")}
	svc, _ := newPelicanGeneratorForTest(account, upstream)
	svc.openaiGatewayService.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 20
	var policy *pelicanFirstOutputPolicy
	upstream.beforeSend = func(req *http.Request) {
		policy, _ = req.Context().Value(pelicanFirstOutputPolicyContextKey{}).(*pelicanFirstOutputPolicy)
		require.NotNil(t, policy)
		require.Equal(t, 20*time.Second, policy.timeout)
		require.Nil(t, policy.guard, "account/global admission precedes the normal header guard")
		require.True(t, state.generationAt.IsZero())
		clock = clock.Add(8 * time.Minute)
		require.NoError(t, req.Context().Err())
	}
	result, err := svc.GeneratePelican(ctx, account.ID, "gpt-6.1-sol", "high")
	require.NoError(t, err)
	require.Equal(t, "complete", result.Status, result.Error)
	require.Equal(t, clock, policy.startedAt)
	require.Equal(t, clock, state.generationAt)
}

func newPelicanGeneratorForTest(account *Account, upstream *pelicanGeneratorUpstream) (*AccountTestService, *pelicanGeneratorAccountRepo) {
	repo := &pelicanGeneratorAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{AllowInsecureHTTP: true}}, Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &AccountTestService{accountRepo: repo, cfg: cfg, httpUpstream: upstream}
	svc.openaiGatewayService = &OpenAIGatewayService{accountRepo: repo, cfg: cfg, httpUpstream: upstream}
	svc.pelicanGatewayService = &GatewayService{accountRepo: repo, cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}}
	svc.pelicanGeminiService = &GeminiMessagesCompatService{accountRepo: repo, cfg: cfg, httpUpstream: upstream}
	svc.antigravityGatewayService = &AntigravityGatewayService{accountRepo: repo, httpUpstream: upstream, settingService: &SettingService{cfg: cfg}}
	return svc, repo
}

func pelicanResponsesSSE(model, status, suffix string) string {
	completed := map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_local", "model": model, "status": status, "output": []any{}, "usage": map[string]any{"input_tokens": 4, "output_tokens": 8}}}
	if status == "incomplete" {
		completed["type"] = "response.incomplete"
		completed["response"].(map[string]any)["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	raw, _ := json.Marshal(completed)
	return "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html><svg></svg>" + suffix + "\"}\n\ndata: " + string(raw) + "\n\n"
}

func TestPelicanAccountGeneratorSharedOpenAISettings(t *testing.T) {
	for _, target := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		t.Run(target, func(t *testing.T) {
			proxyID := int64(37)
			account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Status: StatusError, Schedulable: false,
				ProxyID: &proxyID, Proxy: &Proxy{Protocol: "socks5", Host: "127.0.0.1", Port: 19090},
				Credentials: map[string]any{"api_key": "local-key", "base_url": "http://fake.example", "model_mapping": map[string]any{"public-model": target},
					credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"X-Account-Identity": "saved-identity"}}}
			upstream := &pelicanGeneratorUpstream{body: pelicanResponsesSSE(target, "completed", "</html>")}
			svc, repo := newPelicanGeneratorForTest(account, upstream)
			prompts := NewSystemPromptService(nil, nil, nil)
			prompts.publish(SystemPromptConfig{Enabled: true, DefaultPromptID: "site", Prompts: []SystemPrompt{{ID: "site", Body: "saved site instructions", Position: SystemPromptPositionPrepend, Role: SystemPromptRoleDeveloper}}})
			svc.openaiGatewayService.systemPrompts = prompts
			result, err := svc.GeneratePelican(context.Background(), account.ID, "public-model", "")
			require.NoError(t, err)
			require.Equal(t, "complete", result.Status)
			require.Equal(t, "<html><svg></svg></html>", result.RawAnswer)
			require.Equal(t, upstream.body, result.RawResponse)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, []int64{account.ID}, repo.reads)
			require.Equal(t, []int64{account.ID}, upstream.accountIDs)
			require.Equal(t, "socks5://127.0.0.1:19090", upstream.proxies[0])
			require.Equal(t, []string{"saved-identity"}, upstream.requests[0].Header[resolveWireCasing("x-account-identity")])
			require.Equal(t, target, gjson.GetBytes(upstream.bodies[0], "model").String())
			require.False(t, gjson.GetBytes(upstream.bodies[0], "reasoning.effort").Exists(), "omitted effort preserves the model default")
			require.Contains(t, string(upstream.bodies[0]), CodexGatewayBorrowPelicanPrompt)
			require.Contains(t, string(upstream.bodies[0]), "saved site instructions")
			require.Greater(t, gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Int(), int64(1024))
			require.True(t, IsAccountObservation(upstream.requests[0].Context()))
		})
	}
}

func TestPelicanAccountGeneratorKeepsBorrowPreparationCause(t *testing.T) {
	account := borrowCoreAccount(2)
	upstream := &pelicanGeneratorUpstream{}
	svc, _ := newPelicanGeneratorForTest(account, upstream)
	shots := 0
	borrow := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		shots++
		state := "first"
		if req.Header.Get("X-Codex-Turn-State") != "" {
			state = "changed"
		}
		return borrowCoreResponse("gpt-6-astra", "OK", state), nil
	}, account)
	borrowCoreCandidate(borrow, time.Now().Add(codexGatewayBorrowTTL))
	svc.openaiGatewayService.SetCodexGatewayBorrowService(borrow)
	result, err := svc.GeneratePelican(context.Background(), account.ID, "gpt-6-astra", "low")
	require.Error(t, err)
	require.True(t, IsCodexGatewayBorrowRequestFailure(err))
	require.Contains(t, result.Error, "target_validation/target_state_changed")
	require.Contains(t, result.Error, "CODEX_GATEWAY_BORROW_UNAVAILABLE")
	require.Equal(t, "failed", result.Status)
	require.Empty(t, result.RawAnswer)
	require.Empty(t, upstream.requests, "a rejected qualification never reaches generation")
	require.Equal(t, 2, shots)
}

func TestPelicanAccountGeneratorHTTPFailureKeepsSelectedAccountAndRawBody(t *testing.T) {
	raw := `{"error":{"type":"rate_limit_error","message":"` + strings.Repeat("complete original detail ", 2000) + `"}}`
	account := &Account{ID: 28, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "local-key", "base_url": "http://fake.example"}}
	upstream := &pelicanGeneratorUpstream{status: 429, contentType: "application/json", body: raw}
	svc, repo := newPelicanGeneratorForTest(account, upstream)
	repo.accounts[29] = &Account{ID: 29, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	result, _ := svc.GeneratePelican(context.Background(), account.ID, "gpt-6.1-sol", "high")
	require.Equal(t, "failed", result.Status)
	require.Equal(t, raw, result.RawResponse)
	require.Equal(t, "API returned 429: "+raw, result.Error)
	require.Equal(t, []int64{account.ID}, repo.reads)
	require.Equal(t, []int64{account.ID}, upstream.accountIDs)
}

func TestPelicanAccountGeneratorTruncatedWorkIsIncomplete(t *testing.T) {
	account := &Account{ID: 31, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "local-key", "base_url": "http://fake.example"}}
	upstream := &pelicanGeneratorUpstream{body: pelicanResponsesSSE("gpt-6.1-sol", "incomplete", "")}
	svc, _ := newPelicanGeneratorForTest(account, upstream)
	result, _ := svc.GeneratePelican(context.Background(), account.ID, "gpt-6.1-sol", "high")
	require.Equal(t, "incomplete", result.Status)
	require.Equal(t, "<html><svg></svg>", result.RawAnswer)
	require.Equal(t, upstream.body, result.RawResponse)
	require.Contains(t, result.Error, "output limit reached")
	require.Len(t, upstream.requests, 1)
}

func TestPelicanAccountGeneratorNativeTextDispatch(t *testing.T) {
	claudeSSE := "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"<html>work</html>\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	chatSSE := "data: {\"model\":\"text-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<html>work</html>\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n"
	geminiSSE := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"<html>work</html>\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1}}\n\n"
	for _, scenario := range []struct{ name, platform, kind, model, protocol, path, response string }{
		{"cn-chat", PlatformDeepseek, AccountTypeAPIKey, "text-model", APIProtocolChatCompletions, "/chat/completions", chatSSE},
		{"cn-anthropic", PlatformMiniMax, AccountTypeAPIKey, "text-model", APIProtocolAnthropic, "/messages", claudeSSE},
		{"opencode", PlatformOpenCodeGo, AccountTypeAPIKey, "text-model", APIProtocolChatCompletions, "/chat/completions", chatSSE},
		{"anthropic", PlatformAnthropic, AccountTypeAPIKey, "text-model", "", "/messages", claudeSSE},
		{"gemini", PlatformGemini, AccountTypeAPIKey, "gemini-3.1-pro", "", ":streamGenerateContent", geminiSSE},
		{"antigravity-upstream", PlatformAntigravity, AccountTypeUpstream, "gemini-text-model", "", "/messages", claudeSSE},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			account := &Account{ID: 44, Platform: scenario.platform, Type: scenario.kind, Credentials: map[string]any{"api_key": "local-key", "base_url": "http://fake.example", "api_protocol": scenario.protocol}}
			upstream := &pelicanGeneratorUpstream{body: scenario.response}
			svc, _ := newPelicanGeneratorForTest(account, upstream)
			result, err := svc.GeneratePelican(context.Background(), account.ID, scenario.model, "")
			require.NoError(t, err)
			require.Equal(t, "complete", result.Status, result.Error)
			require.Equal(t, "<html>work</html>", result.RawAnswer)
			require.Len(t, upstream.requests, 1)
			require.Contains(t, upstream.requests[0].URL.Path, scenario.path)
			require.Contains(t, string(upstream.bodies[0]), CodexGatewayBorrowPelicanPrompt)
		})
	}
}

func TestPelicanParsersKeepJSONDataURLsAndPrettyWSFrames(t *testing.T) {
	jsonReply := `{"model":"text-model","choices":[{"index":0,"message":{"content":"<img src='data:image/png;base64,x'>"},"finish_reason":"stop"}]}`
	text, limited, _, err := parsePelicanTextResponse("chat", jsonReply)
	require.NoError(t, err)
	require.False(t, limited)
	require.Equal(t, "<img src='data:image/png;base64,x'>", text)
	frames := "{\n\"type\":\"response.output_text.delta\",\n\"delta\":\"<html>whole</html>\"\n}\n" + `{"type":"response.completed","response":{"status":"completed","model":"gpt-6-astra","output":[]}}` + "\n"
	text, limited, model, err := parsePelicanWSFrames(frames)
	require.NoError(t, err)
	require.False(t, limited)
	require.Equal(t, "gpt-6-astra", model)
	require.Equal(t, "<html>whole</html>", text)
}

func TestPelicanHTTPRequestOrdinaryAndPreparationPurposeAreNoops(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://fake.example/responses", strings.NewReader(`{}`))
	require.NoError(t, err)
	prepared, finish, err := PreparePelicanHTTPRequest(req, 17, 1, "http")
	require.NoError(t, err)
	require.Same(t, req, prepared)
	resp := &http.Response{}
	actual, err := finish(resp, nil)
	require.NoError(t, err)
	require.Same(t, resp, actual)
	capture := &pelicanGenerationCapture{account: &Account{ID: 17}}
	state := newPelicanExecutionState(context.Background(), newPelicanExecutionCoordinator(1), time.Minute, time.Now)
	defer state.finish()
	ctx := context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
	ctx = context.WithValue(ctx, pelicanGenerationContextKey{}, capture)
	ctx = WithCodexGatewayBorrowObservation(ctx)
	req = req.WithContext(ctx)
	prepared, _, err = PreparePelicanHTTPRequest(req, 17, 1, "http")
	require.NoError(t, err)
	require.Same(t, req, prepared)
	require.True(t, state.generationAt.IsZero(), "borrow preparation must keep its own clock")
	require.Empty(t, capture.attempts)
}

func TestPelicanFirstOutputPolicyStartsAfterQueueAndPreservesNilReplay(t *testing.T) {
	now := time.Now().UTC()
	clock := now
	state := newPelicanExecutionState(context.Background(), newPelicanExecutionCoordinator(1), 10*time.Minute, func() time.Time { return clock })
	defer state.finish()
	capture := &pelicanGenerationCapture{account: &Account{ID: 67, Platform: PlatformOpenAI}, fallbackModel: "gpt-6.1-sol-high", fallbackEffort: "medium"}
	ctx := context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
	ctx = context.WithValue(ctx, pelicanGenerationContextKey{}, capture)
	var armedDeadline time.Time
	policy := &pelicanFirstOutputPolicy{timeout: 30 * time.Second, newGuard: func(ctx context.Context, release context.CancelFunc, deadline time.Time) (context.Context, *openAIFirstOutputHeaderGuard) {
		armedDeadline = deadline
		return newOpenAIFirstOutputHeaderGuard(ctx, release, time.Now().Add(time.Hour))
	}}
	ctx = context.WithValue(ctx, pelicanFirstOutputPolicyContextKey{}, policy)
	BeginPelicanPreparation(ctx)
	clock = clock.Add(8 * time.Minute) // A modeled queue, never a wall-clock sleep.
	require.True(t, state.generationAt.IsZero())
	require.Nil(t, policy.guard, "queued work must not arm the upstream header guard")
	body := `{"model":"gpt-6.1-sol","reasoning":{"effort":"high"},"input":"fixed","stream":true}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://fake.example/responses", strings.NewReader(body))
	require.NoError(t, err)
	req.GetBody = nil // The normal reasoning-recovery dispatch contract.
	prepared, finish, err := PreparePelicanHTTPRequest(req, 67, 1, "http")
	require.NoError(t, err)
	defer policy.guard.close()
	require.Equal(t, clock.Add(policy.timeout), armedDeadline)
	require.Equal(t, clock, PelicanFirstOutputStart(prepared.Context(), now))
	require.Nil(t, prepared.GetBody, "observation must not enable transport replay")
	actualBody, err := io.ReadAll(prepared.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(actualBody))
	require.Equal(t, "gpt-6.1-sol", capture.attempts[0].model)
	require.Equal(t, "high", capture.attempts[0].effort)
	_, err = finish(nil, nil)
	require.NoError(t, err)
	require.False(t, policy.stopHeaderWait(), "header completion is safe to query again by Forward")
	ordinaryPolicy := &pelicanFirstOutputPolicy{timeout: time.Minute}
	ordinaryCtx := context.WithValue(context.Background(), pelicanFirstOutputPolicyContextKey{}, ordinaryPolicy)
	req = req.WithContext(ordinaryCtx)
	prepared, _, err = PreparePelicanHTTPRequest(req, 67, 1, "http")
	require.NoError(t, err)
	require.Same(t, req, prepared)
	require.Nil(t, ordinaryPolicy.guard, "ordinary requests keep their existing guard owner")
}

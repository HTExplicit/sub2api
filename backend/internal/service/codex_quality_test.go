package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func qualityStateFixture(t *testing.T, limit int) (*nativeCodexMemoryStore, codexQualityRun) {
	t.Helper()
	store := &nativeCodexMemoryStore{}
	run := codexQualityRun{RunID: uuid.NewString(), ActorID: 9, APIKeyID: 101, AccountID: 16380, GroupID: 53, ProxyID: 34, OwnerIdentity: "owner", Model: "gpt-6-astra", ReasoningEffort: "high", PromptSHA256: codexQualityHash("question"), MaxSends: limit, Status: "open"}
	_, digest, err := newCodexQualityGrant(run.RunID)
	require.NoError(t, err)
	run, err = issueCodexQualityGrant(context.Background(), store, run, digest, time.Now().Add(time.Hour))
	require.NoError(t, err)
	return store, run
}

func TestCodexQualityBudgetReplayAndConcurrentCAS(t *testing.T) {
	store, run := qualityStateFixture(t, 8)
	ctx := context.Background()
	first := CodexQualityAttempt{Stage: "business", TrialID: uuid.NewString(), AccountID: run.AccountID}
	require.NoError(t, reserveCodexQualityAttempt(ctx, store, run.RunID, run.GrantDigest, first))
	require.ErrorIs(t, reserveCodexQualityAttempt(ctx, store, run.RunID, run.GrantDigest, first), ErrCodexQualitySpent)
	var successes atomic.Int32
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			attempt := CodexQualityAttempt{Stage: "business", TrialID: uuid.NewString(), AccountID: run.AccountID}
			if reserveCodexQualityAttempt(ctx, store, run.RunID, run.GrantDigest, attempt) == nil {
				successes.Add(1)
			}
		}()
	}
	workers.Wait()
	saved, _, err := readCodexQualityRun(ctx, store, run.RunID)
	require.NoError(t, err)
	require.Equal(t, int(successes.Load())+1, saved.UsedSends)
	require.LessOrEqual(t, saved.UsedSends, 8)
	require.Equal(t, saved.UsedSends, len(saved.Attempts))
	for _, a := range saved.Attempts {
		require.Equal(t, "unknown", a.State)
	}
}

func TestCodexQualityGrantReissueKeepsBudgetAndBinding(t *testing.T) {
	store, run := qualityStateFixture(t, 3)
	ctx := context.Background()
	for _, trial := range []string{"one", "two", "three"} {
		require.NoError(t, reserveCodexQualityAttempt(ctx, store, run.RunID, run.GrantDigest, CodexQualityAttempt{Stage: codexQualityStage, TrialID: trial, AccountID: run.AccountID}))
	}
	_, digest, err := newCodexQualityGrant(run.RunID)
	require.NoError(t, err)
	saved, err := issueCodexQualityGrant(ctx, store, run, digest, time.Now().Add(2*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 3, saved.UsedSends)
	require.False(t, codexQualityGrantMatches(saved, run.GrantDigest))
	require.True(t, codexQualityGrantMatches(saved, digest))
	require.ErrorIs(t, reserveCodexQualityAttempt(ctx, store, run.RunID, digest, CodexQualityAttempt{Stage: codexQualityStage, TrialID: "four", AccountID: run.AccountID}), ErrCodexQualitySpent)
	for _, mutate := range []func(*codexQualityRun){func(r *codexQualityRun) { r.ActorID++ }, func(r *codexQualityRun) { r.APIKeyID++ }, func(r *codexQualityRun) { r.AccountID++ }, func(r *codexQualityRun) { r.GroupID++ }, func(r *codexQualityRun) { r.ProxyID++ }, func(r *codexQualityRun) { r.PromptSHA256 = codexQualityHash("changed") }, func(r *codexQualityRun) { r.MaxSends++ }, func(r *codexQualityRun) { r.OwnerIdentity = "different" }, func(r *codexQualityRun) { r.Model = "gpt-6-sol" }, func(r *codexQualityRun) { r.ReasoningEffort = "xhigh" }} {
		other := run
		mutate(&other)
		_, err := issueCodexQualityGrant(ctx, store, other, digest, time.Now().Add(time.Hour))
		require.ErrorIs(t, err, ErrCodexQualityUnavailable)
	}
	_, err = mutateCodexQualityRun(ctx, store, run.RunID, func(r *codexQualityRun) error { r.Status = "closed"; return nil })
	require.NoError(t, err)
	_, err = issueCodexQualityGrant(ctx, store, run, digest, time.Now().Add(time.Hour))
	require.ErrorIs(t, err, ErrCodexQualityUnavailable)
}

func TestCodexQualityRawObserverDoesNotRewriteOrInventUsage(t *testing.T) {
	t.Run("event_header_disagrees_with_body", func(t *testing.T) {
		raw := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"metadata\":{\"headers\":{\"openai-model\":\"gpt-6-luna\",\"x-openai-model\":[\"gpt-6-luna\"]}}}}\n\n"
		var observed CodexQualityAttempt
		body := &codexQualityObservedBody{ReadCloser: io.NopCloser(strings.NewReader(raw)), sse: true, attempt: CodexQualityAttempt{HTTPStatus: 200}, finish: func(a CodexQualityAttempt) { observed = a }}
		recordCodexQualityHeaderModel(&body.attempt, "gpt-6-astra")
		wire, err := io.ReadAll(body)
		require.NoError(t, err)
		require.Equal(t, raw, string(wire))
		require.Equal(t, []string{"gpt-6-astra"}, observed.ResponseModels)
		require.Equal(t, []string{"gpt-6-astra", "gpt-6-luna"}, observed.HeaderModels)
		require.Equal(t, "conflicting_models", *observed.HeaderModel)
	})
	raw := "data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-6-luna\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"29\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-luna\",\"usage\":{\"output_tokens\":0}}}\n\n"
	var final CodexQualityAttempt
	finishes := 0
	body := &codexQualityObservedBody{ReadCloser: io.NopCloser(strings.NewReader(raw)), sse: true, attempt: CodexQualityAttempt{HTTPStatus: 200}, finish: func(a CodexQualityAttempt) { final = a; finishes++ }}
	forwarded, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, raw, string(forwarded))
	require.Equal(t, 1, finishes)
	require.True(t, final.Completed)
	require.Equal(t, []string{"gpt-6-luna"}, final.ResponseModels)
	require.Equal(t, "gpt-6-luna", *final.CreatedModel)
	require.Equal(t, "gpt-6-luna", *final.TerminalModel)
	require.Nil(t, final.ReasoningTokens)
	require.Nil(t, final.InputTokens)
	require.NotNil(t, final.OutputTokens)
	require.Zero(t, *final.OutputTokens)
	require.Equal(t, strings.Repeat("m", 256)+"…(truncated, 300 bytes)", qualityRecordedModel(strings.Repeat("m", 300)), "one declared name is bounded and marked")
	failedEvent := `{"type":"response.failed","response":{"model":"o-internal/Model X","error":{"code":"server_error","message":"upstream said no"}}}`
	var failed CodexQualityAttempt
	odd := &codexQualityObservedBody{ReadCloser: io.NopCloser(strings.NewReader("data: " + failedEvent + "\n\n")), sse: true, attempt: CodexQualityAttempt{HTTPStatus: 200}, finish: func(a CodexQualityAttempt) { failed = a }}
	_, err = io.ReadAll(odd)
	require.NoError(t, err)
	require.Equal(t, []string{"o-internal/Model X"}, failed.ResponseModels, "declared models are recorded verbatim")
	require.Equal(t, "upstream_error", failed.ErrorCode)
	require.Equal(t, "server_error", failed.UpstreamErrorCode)
	require.Equal(t, "upstream said no", failed.UpstreamErrorMessage)
	require.Equal(t, failedEvent, failed.UpstreamBody)
}

func TestCodexQualityStrictJSONAndHeaderScope(t *testing.T) {
	require.True(t, validCodexQualityJSON([]byte(`{"model":"gpt-6-astra","input":"question","reasoning":{"effort":"high"}}`)))
	for _, body := range []string{`{"input":"question","input":"changed"}`, `{"reasoning":{"effort":"high","effort":"low"}}`, `{} {}`} {
		require.False(t, validCodexQualityJSON([]byte(body)))
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	StageCodexQualityHeaders(c)
	require.NoError(t, (&OpenAIGatewayService{}).AdmitCodexQualityRequest(c, nil, nil), "ordinary requests are unchanged")
	c.Request.Header.Set(CodexQualityGrantHeader, "invalid-secret")
	c.Request.Header.Set(CodexQualityTrialHeader, uuid.NewString())
	StageCodexQualityHeaders(c)
	require.Empty(t, c.Request.Header.Get(CodexQualityGrantHeader))
	require.Empty(t, c.Request.Header.Get(CodexQualityTrialHeader))
	require.ErrorIs(t, (&OpenAIGatewayService{}).AdmitCodexQualityRequest(c, nil, nil), ErrCodexQualityUnavailable)
	require.False(t, IsCodexQualityRequest(c.Request.Context()))
}

func TestCodexQualityLocalBudgetErrorIsNotAccountFailure(t *testing.T) {
	ctx := context.WithValue(context.Background(), codexQualityExecutionKey{}, &codexQualityExecution{})
	service := &OpenAIGatewayService{}
	for _, err := range []error{ErrCodexQualitySpent, ErrCodexQualityUnavailable} {
		require.ErrorIs(t, service.handleOpenAIUpstreamTransportError(ctx, nil, nil, err, false), err)
	}
}

func TestCodexQualityUUIDCaseCannotCreateAnotherBudget(t *testing.T) {
	store, run := qualityStateFixture(t, 3)
	ctx := context.Background()
	trial := "a1b2c3d4-1111-4111-8111-a1b2c3d4e5f6"
	canonical, ok := canonicalCodexQualityID(strings.ToUpper(trial))
	require.True(t, ok)
	require.Equal(t, trial, canonical)
	first := CodexQualityAttempt{Stage: "business", TrialID: trial, AccountID: run.AccountID}
	require.NoError(t, reserveCodexQualityAttempt(ctx, store, run.RunID, run.GrantDigest, first))
	first.TrialID = canonical
	require.ErrorIs(t, reserveCodexQualityAttempt(ctx, store, strings.ToUpper(run.RunID), run.GrantDigest, first), ErrCodexQualitySpent)
	other := run
	other.RunID = strings.ToUpper(run.RunID)
	saved, err := issueCodexQualityGrant(ctx, store, other, codexQualityHash("new grant"), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, run.RunID, saved.RunID)
	require.Equal(t, 1, saved.UsedSends)
}

type qualityHealthRepository struct {
	AccountRepository
	errorWrites, cooldownWrites int
}

func (r *qualityHealthRepository) SetError(context.Context, int64, string) error {
	r.errorWrites++
	return nil
}
func (r *qualityHealthRepository) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.cooldownWrites++
	return nil
}

type quality403Counter struct{ resets int }

func (*quality403Counter) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	return 1, nil
}
func (c *quality403Counter) ResetOpenAI403Count(context.Context, int64) error { c.resets++; return nil }

func TestCodexQualityHTTPFailuresAndSuccessDoNotChangeOrdinaryHealth(t *testing.T) {
	repo := &qualityHealthRepository{}
	counter := &quality403Counter{}
	rateLimit := &RateLimitService{accountRepo: repo, openAI403CounterCache: counter}
	s := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rateLimit}
	proxy := int64(34)
	account := &Account{ID: 16380, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, ProxyID: &proxy}
	ctx := context.WithValue(context.Background(), codexQualityExecutionKey{}, &codexQualityExecution{})
	for _, status := range []int{401, 429, 529} {
		require.False(t, s.handleOpenAIAccountUpstreamError(ctx, account, status, nil, []byte(`{"error":{"code":"account_deactivated","message":"account deactivated"}}`), "gpt-6-astra"))
	}
	transportErr := errors.New("connection refused")
	require.ErrorIs(t, s.handleOpenAIUpstreamTransportError(ctx, nil, account, transportErr, false), transportErr)
	require.False(t, rateLimit.HandleStreamTimeout(ctx, account, "gpt-6-astra"))
	s.observeOpenAIUsageAccountHealth(context.Background(), &OpenAIRecordUsageInput{Account: account, CodexQuality: true})
	require.Zero(t, repo.errorWrites)
	require.Zero(t, repo.cooldownWrites)
	require.Zero(t, counter.resets)
	_, blocked := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.False(t, blocked)
	// The ordinary control retains both its permanent 401 observation and its
	// successful-request reset; the diagnostic guard is not a global disable.
	require.True(t, s.handleOpenAIAccountUpstreamError(context.Background(), account, 401, nil, []byte(`{"error":{"code":"account_deactivated","message":"account deactivated"}}`), "gpt-6-astra"))
	require.Equal(t, 1, repo.errorWrites)
	s.observeOpenAIUsageAccountHealth(context.Background(), &OpenAIRecordUsageInput{Account: account})
	require.Equal(t, 1, counter.resets)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	apiAccount := &Account{ID: 16463, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, ProxyID: &proxy}
	_ = s.handleOpenAIUpstreamTransportError(WithOpenAIOfficialHTTPFailover(context.Background()), c, apiAccount, transportErr, false)
	require.Equal(t, 1, repo.cooldownWrites)
	// Proxy disconnect/success isolation is also independent of account health.
	circuit := s.getOpenAIProxyStreamCircuit()
	circuit.entries[proxy] = openAIProxyStreamCircuitEntry{failureCount: 1, blockedUntil: time.Now().Add(time.Minute)}
	before := circuit.entries[proxy]
	s.recordOpenAIProxyStreamDisconnect(account, transportErr, "fixture", ctx)
	s.clearOpenAIProxyStreamDisconnect(account, ctx)
	require.Equal(t, before, circuit.entries[proxy])
}

type qualityBreakerReadCache struct {
	GatewayCache
	reads   int
	blocked bool
}

func (c *qualityBreakerReadCache) CodexQualityRuntimeBlocked(context.Context, int64, []string) (bool, error) {
	c.reads++
	return c.blocked, nil
}
func (*qualityBreakerReadCache) AllowOpenAIRuntimeBreakerProbe(context.Context, int64, string, string, time.Duration) (bool, error) {
	panic("diagnostic must not claim a probe")
}

func TestCodexQualityHealthReadDoesNotClaimOrClear(t *testing.T) {
	cache := &qualityBreakerReadCache{}
	s := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 16380, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	expired := time.Now().Add(-time.Minute)
	s.openaiAccountRuntimeBlockUntil.Store(account.ID, expired)
	state := s.getOpenAIAccountModelTransientState()
	key := openAIAccountModelKey{AccountID: account.ID, Model: "gpt-6-astra"}
	entry := openAIAccountModelTransientEntry{blockUntil: expired, failureStreak: 2, lastFailure: expired, lastTouched: expired}
	state.entries[key] = entry
	require.False(t, s.codexQualityHealthBlocked(context.Background(), account, "gpt-6-astra"))
	value, exists := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, exists)
	require.Equal(t, expired, value)
	require.Equal(t, entry, state.entries[key])
	require.Equal(t, 1, cache.reads)
	cache.blocked = true
	require.True(t, s.codexQualityHealthBlocked(context.Background(), account, "gpt-6-astra"), "ordinary half-open marker stays unavailable to diagnostics")
}

func TestCodexQualityKeyRevalidationRejectsExpiredQuotaAndDrift(t *testing.T) {
	group := int64(53)
	key := APIKey{ID: 101, UserID: 9, GroupID: &group, Status: StatusAPIKeyActive}
	require.True(t, codexQualityKeyUsable(&key, 9, 101, 53))
	for _, change := range []func(*APIKey){func(k *APIKey) { k.Status = StatusAPIKeyDisabled }, func(k *APIKey) { t := time.Now().Add(-time.Second); k.ExpiresAt = &t }, func(k *APIKey) { k.Quota, k.QuotaUsed = 1, 1 }, func(k *APIKey) { k.UserID++ }, func(k *APIKey) { group := int64(54); k.GroupID = &group }} {
		other := key
		change(&other)
		require.False(t, codexQualityKeyUsable(&other, 9, 101, 53))
		store, run := qualityStateFixture(t, 3)
		rt := &codexQualityRuntime{store: store, installation: &NativeCodexMetadata{}}
		e := &codexQualityExecution{runtime: rt, runID: run.RunID, grantDigest: run.GrantDigest, accountID: run.AccountID, trialID: uuid.NewString(), keyLookup: func(context.Context, int64) (*APIKey, error) { return &other, nil }}
		ctx := context.WithValue(context.Background(), codexQualityExecutionKey{}, e)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6-astra","reasoning":{"effort":"high"}}`))
		require.NoError(t, err)
		require.ErrorIs(t, reserveCodexQualitySend(req, &Account{ID: run.AccountID}), ErrCodexQualityUnavailable)
		saved, _, err := readCodexQualityRun(context.Background(), store, run.RunID)
		require.NoError(t, err)
		require.Zero(t, saved.UsedSends, "revoked key must fail before the budget is spent")
	}
}

// qualityCreateFixture is a manually stopped Pro OAuth account in group 53
// behind proxy 34, with an administrator key of that group.
func qualityCreateFixture() (*OpenAIGatewayService, *Account, *nativeCodexMemoryStore, *APIKey) {
	group, proxy := int64(53), int64(34)
	account := &Account{ID: 16380, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: false, Concurrency: 1, GroupIDs: []int64{group}, ProxyID: &proxy, Credentials: map[string]any{"plan_type": "pro", "chatgpt_account_id": "quality-account"}}
	store := &nativeCodexMemoryStore{}
	s := &OpenAIGatewayService{nativeCodexRuntime: &NativeCodexRuntime{repo: store}, accountRepo: &codexAccountRepositoryFixture{account: account}}
	return s, account, store, &APIKey{ID: 101, UserID: 9, GroupID: &group, Status: StatusAPIKeyActive}
}

func TestCodexQualityCreateBindsModelAndReasoningEffort(t *testing.T) {
	ctx := context.Background()
	s, account, store, key := qualityCreateFixture()
	request := func(model, effort string) CodexQualityCreateRequest {
		return CodexQualityCreateRequest{RunID: uuid.NewString(), APIKeyID: key.ID, PromptSHA256: codexQualityHash("question"), Model: model, ReasoningEffort: effort, MaxSends: 4, TTLSeconds: 600}
	}
	for _, test := range []struct {
		name, model, effort, reason string
		mapping                     map[string]any
		group                       *Group
	}{
		{name: "model_required", effort: "high", reason: "model is required"},
		{name: "effort_required", model: "gpt-6-sol", reason: "reasoning_effort is required"},
		{name: "effort_outside_model_levels", model: "gpt-6-sol", effort: "minimal", reason: `reasoning_effort "minimal" is not supported for model gpt-6-sol on account 16380 (supported: low, medium, high, xhigh, max)`},
		{name: "effort_outside_openai_set", model: "gpt-4.1", effort: "max", reason: `reasoning_effort "max" is not supported for model gpt-4.1 on account 16380 (supported: none, minimal, low, medium, high, xhigh)`},
		{name: "model_not_served", model: "gpt-6-sol", effort: "high", reason: "account 16380 is not eligible for gpt-6-sol: model_not_supported", mapping: map[string]any{"gpt-6-astra": "gpt-6-astra"}},
		{name: "model_mapped", model: "gpt-6-sol", effort: "high", reason: "account 16380 sends gpt-6-sol upstream as gpt-6-luna", mapping: map[string]any{"gpt-6-sol": "gpt-6-luna"}},
		{name: "effort_capped_by_group", model: "gpt-6-sol", effort: "xhigh", reason: "the reasoning-effort policy of group 53 sends xhigh as high", group: &Group{ID: 53, Platform: PlatformOpenAI, MaxReasoningEffort: "high"}},
		{name: "effort_denied_by_group", model: "gpt-6-sol", effort: "xhigh", reason: `the reasoning-effort policy of group 53 rejects xhigh: reasoning effort "xhigh" exceeds this group's limit of "high"`, group: &Group{ID: 53, Platform: PlatformOpenAI, MaxReasoningEffort: "high", MaxReasoningEffortOverLimit: ReasoningEffortOverLimitDeny}},
	} {
		t.Run(test.name, func(t *testing.T) {
			delete(account.Credentials, "model_mapping")
			if test.mapping != nil {
				account.Credentials["model_mapping"] = test.mapping
			}
			key.Group = test.group
			rejected := request(test.model, test.effort)
			_, err := s.CreateCodexQualityRun(ctx, key.UserID, account.ID, key, rejected)
			require.ErrorIs(t, err, ErrCodexQualityUnavailable)
			require.ErrorContains(t, err, test.reason)
			stored, _, err := readCodexQualityRun(ctx, store, rejected.RunID)
			require.NoError(t, err)
			require.Empty(t, stored.RunID, "a rejected binding stores nothing")
		})
	}
	delete(account.Credentials, "model_mapping")
	// A group policy that leaves the bound effort unchanged does not block it.
	key.Group = &Group{ID: 53, Platform: PlatformOpenAI, MaxReasoningEffort: "xhigh"}
	accepted := request("gpt-6-sol", "xhigh")
	created, err := s.CreateCodexQualityRun(ctx, key.UserID, account.ID, key, accepted)
	require.NoError(t, err)
	require.NotEmpty(t, created.Grant)
	require.Equal(t, "gpt-6-sol", created.Model)
	require.Equal(t, "xhigh", created.ReasoningEffort)
	require.Nil(t, created.RouteGeneration)
	require.Nil(t, created.RouteExpiresAt)
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(store.values[codexPrivateStateNamespace+":"+codexQualityKey(accepted.RunID)].Value, &members))
	require.JSONEq(t, `"gpt-6-sol"`, string(members["model"]))
	require.JSONEq(t, `"xhigh"`, string(members["reasoning_effort"]))
	owner, err := json.Marshal(CodexCredentialOwnerIdentity(account))
	require.NoError(t, err)
	require.JSONEq(t, string(owner), string(members["owner_identity"]))
	for _, name := range []string{"scope", "qualification", "route_generation", "route_runtime_generation"} {
		require.NotContains(t, members, name)
	}
	// The same run id resumes only with the same binding; a new grant never
	// grows the budget.
	for _, other := range []CodexQualityCreateRequest{{Model: "gpt-6-astra", ReasoningEffort: "xhigh"}, {Model: "gpt-6-sol", ReasoningEffort: "high"}} {
		changed := accepted
		changed.Model, changed.ReasoningEffort = other.Model, other.ReasoningEffort
		_, err = s.CreateCodexQualityRun(ctx, key.UserID, account.ID, key, changed)
		require.ErrorContains(t, err, "exists with other parameters")
	}
	resumed, err := s.CreateCodexQualityRun(ctx, key.UserID, account.ID, key, accepted)
	require.NoError(t, err)
	require.NotEqual(t, created.Grant, resumed.Grant)
	require.Equal(t, 4, resumed.MaxSends)
}

// A record written by the retired route-qualified diagnostics: no model or
// effort of its own, a routing scope, a Cookie route qualification and probe
// attempts that name their connection.
const qualityLegacyRecord = `{"run_id":"138bf94a-1bea-4aeb-9e7a-378e9c394bc9","actor_id":9,"api_key_id":101,"account_id":7,"group_id":53,"proxy_id":34,` +
	`"scope":{"account_id":7,"identity":"legacy-owner","profile_hash":"legacy-profile","route_hash":"legacy-route","route_evidence":"connection_required","transport":"http","account_proxy_id":34},` +
	`"prompt_sha256":"5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8","grant_digest":"0b3a8f9e1c2d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7","status":"open","max_sends":60,"used_sends":3,"expires_at":"2099-01-01T00:00:00Z",` +
	`"route_generation":2,"route_runtime_generation":5,` +
	`"qualification":{"scope":{"account_id":7,"identity":"legacy-owner","profile_hash":"legacy-profile","route_hash":"legacy-route","route_evidence":"connection","connection_lease_id":"legacy-lease","transport":"http"},"bundle":{"key":"bundle.quality.legacy.live","revision":4,"expires_at":"2026-09-23T09:02:00Z","connection_lease_id":"legacy-lease"},"model":"gpt-6-astra","verified_at":"2026-09-23T09:00:00Z","expires_at":"2026-09-23T09:02:00Z"},` +
	`"attempts":[` +
	`{"stage":"acquire","operation_id":"5d0c4f5e-2b58-4d27-9a43-8a0fd4d2f101","account_id":7,"state":"complete","reserved_at":"2026-09-23T08:59:00Z","request_model":"gpt-6-astra","reasoning_effort":"high","response_models":["gpt-6-astra"],"created_model":null,"terminal_model":"gpt-6-astra","header_model":null,"header_models":[],"completed":true,"reasoning_tokens":null,"input_tokens":null,"output_tokens":null},` +
	`{"stage":"verify","operation_id":"5d0c4f5e-2b58-4d27-9a43-8a0fd4d2f101","account_id":7,"state":"complete","reserved_at":"2026-09-23T08:59:30Z","request_model":"gpt-6-astra","reasoning_effort":"high","response_models":["gpt-6-astra"],"created_model":null,"terminal_model":"gpt-6-astra","header_model":null,"header_models":[],"completed":true,"reasoning_tokens":null,"input_tokens":null,"output_tokens":null,"connection_fingerprint":"aaaaaaaaaaaaaaaa"},` +
	`{"stage":"business","trial_id":"9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d","account_id":7,"state":"complete","reserved_at":"2026-09-23T09:00:10Z","request_model":"gpt-6-astra","reasoning_effort":"high","response_models":["gpt-6-astra"],"created_model":"gpt-6-astra","terminal_model":"gpt-6-astra","header_model":"gpt-6-astra","header_models":["gpt-6-astra"],"completed":true,"reasoning_tokens":12,"input_tokens":30,"output_tokens":40,"connection_fingerprint":"aaaaaaaaaaaaaaaa","qualification_fingerprint":"bbbbbbbbbbbbbbbb"}]}`

func TestCodexQualityLegacyLedgerReadsAsAstraHighAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := &nativeCodexMemoryStore{values: map[string]extensionv1.StateResult{}}
	account := codexOAuthTestAccount(7)
	s := &OpenAIGatewayService{nativeCodexRuntime: &NativeCodexRuntime{repo: store}, accountRepo: &codexAccountRepositoryFixture{account: account}}
	runID := "138bf94a-1bea-4aeb-9e7a-378e9c394bc9"
	key := codexPrivateStateNamespace + ":" + codexQualityKey(runID)
	require.True(t, json.Valid([]byte(qualityLegacyRecord)))
	store.values[key] = extensionv1.StateResult{Found: true, Revision: 11, Value: json.RawMessage(qualityLegacyRecord)}

	view, err := s.ReadCodexQualityRun(ctx, 10, 7, runID)
	require.NoError(t, err, "any administrator reads a legacy record")
	require.Equal(t, codexQualityLegacyModel, view.Model)
	require.Equal(t, codexQualityLegacyEffort, view.ReasoningEffort)
	require.Equal(t, "open", view.Status)
	require.Equal(t, 3, view.UsedSends)
	require.Len(t, view.Attempts, 3)
	require.Equal(t, "verify", view.Attempts[1].Stage)
	require.Equal(t, "bbbbbbbbbbbbbbbb", view.Attempts[2].QualificationFingerprint)
	require.NotNil(t, view.RouteGeneration)
	require.EqualValues(t, 2, *view.RouteGeneration)
	require.NotNil(t, view.RouteExpiresAt)
	require.Equal(t, "2026-09-23T09:02:00Z", view.RouteExpiresAt.UTC().Format(time.RFC3339))
	require.Equal(t, codexQualityHash("legacy-lease")[:16], view.ConnectionFingerprint)
	require.True(t, bytes.Equal([]byte(qualityLegacyRecord), store.values[key].Value), "reading keeps the stored bytes")
	require.EqualValues(t, 11, store.values[key].Revision)

	// An unexpired legacy run takes no new grant and no send.
	run, _, err := readCodexQualityRun(ctx, store, runID)
	require.NoError(t, err)
	require.True(t, run.legacy())
	require.False(t, codexQualityActive(run, time.Now()))
	_, err = issueCodexQualityGrant(ctx, store, codexQualityRun{RunID: runID, ActorID: 9, APIKeyID: 101, AccountID: 7, GroupID: 53, ProxyID: 34, OwnerIdentity: "legacy-owner", Model: codexQualityLegacyModel, ReasoningEffort: codexQualityLegacyEffort, PromptSHA256: run.PromptSHA256, MaxSends: 60}, codexQualityHash("new grant"), time.Now().Add(time.Hour))
	require.ErrorContains(t, err, "retired route-qualified diagnostics")
	require.ErrorIs(t, reserveCodexQualityAttempt(ctx, store, runID, "", CodexQualityAttempt{Stage: codexQualityStage, TrialID: uuid.NewString(), AccountID: 7}), ErrCodexQualityUnavailable)
	require.EqualValues(t, 11, store.values[key].Revision, "refusals never rewrite a legacy record")

	// Closing rewrites the record; every legacy-only member survives.
	closed, err := s.CloseCodexQualityRun(ctx, 9, 7, runID)
	require.NoError(t, err)
	require.Equal(t, "closed", closed.Status)
	var before, after map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(qualityLegacyRecord), &before))
	require.NoError(t, json.Unmarshal(store.values[key].Value, &after))
	for _, name := range []string{"scope", "qualification", "route_generation", "route_runtime_generation", "attempts", "prompt_sha256", "max_sends", "used_sends"} {
		require.JSONEq(t, string(before[name]), string(after[name]), name)
	}
	require.NotContains(t, after, "model", "a rewritten legacy record does not gain a binding")
	require.JSONEq(t, `"closed"`, string(after["status"]))
	require.JSONEq(t, `""`, string(after["grant_digest"]))
}

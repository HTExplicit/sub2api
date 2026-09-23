package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func qualityStateFixture(t *testing.T, limit int) (*routingMemoryStore, codexQualityRun) {
	t.Helper()
	store := &routingMemoryStore{}
	run := codexQualityRun{RunID: uuid.NewString(), ActorID: 9, APIKeyID: 101, AccountID: 16380, GroupID: 53, ProxyID: 34, Scope: extensionv1.CodexRoutingScope{AccountID: 16380, Identity: "owner", ProfileHash: "profile", RouteHash: "route", Transport: "http"}, PromptSHA256: codexQualityHash("question"), MaxSends: limit, Status: "open"}
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

func TestCodexQualityGrantReissueKeepsBudgetAndScope(t *testing.T) {
	store, run := qualityStateFixture(t, 3)
	ctx := context.Background()
	for _, stage := range []string{"acquire", "verify", "business"} {
		require.NoError(t, reserveCodexQualityAttempt(ctx, store, run.RunID, run.GrantDigest, CodexQualityAttempt{Stage: stage, TrialID: "one", OperationID: "route", AccountID: run.AccountID}))
	}
	_, digest, err := newCodexQualityGrant(run.RunID)
	require.NoError(t, err)
	saved, err := issueCodexQualityGrant(ctx, store, run, digest, time.Now().Add(2*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 3, saved.UsedSends)
	require.False(t, codexQualityGrantMatches(saved, run.GrantDigest))
	require.True(t, codexQualityGrantMatches(saved, digest))
	require.ErrorIs(t, reserveCodexQualityAttempt(ctx, store, run.RunID, digest, CodexQualityAttempt{Stage: "business", TrialID: "two", AccountID: run.AccountID}), ErrCodexQualitySpent)
	for _, mutate := range []func(*codexQualityRun){func(r *codexQualityRun) { r.ActorID++ }, func(r *codexQualityRun) { r.APIKeyID++ }, func(r *codexQualityRun) { r.AccountID++ }, func(r *codexQualityRun) { r.GroupID++ }, func(r *codexQualityRun) { r.ProxyID++ }, func(r *codexQualityRun) { r.PromptSHA256 = codexQualityHash("changed") }, func(r *codexQualityRun) { r.MaxSends++ }, func(r *codexQualityRun) { r.Scope.Identity = "different" }} {
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
}

func TestCodexQualityPrivateKeysAndHeaderScope(t *testing.T) {
	require.True(t, validCodexQualityJSON([]byte(`{"model":"gpt-6-astra","input":"question","reasoning":{"effort":"high"}}`)))
	for _, body := range []string{`{"input":"question","input":"changed"}`, `{"reasoning":{"effort":"high","effort":"low"}}`, `{} {}`} {
		require.False(t, validCodexQualityJSON([]byte(body)))
	}
	ctx := context.WithValue(context.Background(), codexQualityExecutionKey{}, &codexQualityExecution{runID: uuid.NewString()})
	query := extensionv1.CodexRoutingQuery{AccountID: 16380, Model: codexQualityModel}
	privateKey := fixedCodexRoutingBundleKey(ctx, query, "candidate")
	require.LessOrEqual(t, len(privateKey), 80)
	require.NotEqual(t, privateKey, fixedCodexRoutingBundleKey(context.Background(), query, "candidate"))
	require.NotEqual(t, codexQualityClockKey(ctx, "clock.ordinary"), "clock.ordinary")
	require.Equal(t, "clock.ordinary", codexQualityClockKey(context.Background(), "clock.ordinary"))
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
		require.False(t, s.handleOpenAIAccountUpstreamError(ctx, account, status, nil, []byte(`{"error":{"code":"account_deactivated","message":"account deactivated"}}`), codexQualityModel))
	}
	transportErr := errors.New("connection refused")
	require.ErrorIs(t, s.handleOpenAIUpstreamTransportError(ctx, nil, account, transportErr, false), transportErr)
	require.False(t, rateLimit.HandleStreamTimeout(ctx, account, codexQualityModel))
	s.observeOpenAIUsageAccountHealth(context.Background(), &OpenAIRecordUsageInput{Account: account, CodexQuality: true})
	require.Zero(t, repo.errorWrites)
	require.Zero(t, repo.cooldownWrites)
	require.Zero(t, counter.resets)
	_, blocked := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.False(t, blocked)
	// The ordinary control retains both its permanent 401 observation and its
	// successful-request reset; the diagnostic guard is not a global disable.
	require.True(t, s.handleOpenAIAccountUpstreamError(context.Background(), account, 401, nil, []byte(`{"error":{"code":"account_deactivated","message":"account deactivated"}}`), codexQualityModel))
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
	key := openAIAccountModelKey{AccountID: account.ID, Model: codexQualityModel}
	entry := openAIAccountModelTransientEntry{blockUntil: expired, failureStreak: 2, lastFailure: expired, lastTouched: expired}
	state.entries[key] = entry
	require.False(t, s.codexQualityHealthBlocked(context.Background(), account))
	value, exists := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, exists)
	require.Equal(t, expired, value)
	require.Equal(t, entry, state.entries[key])
	require.Equal(t, 1, cache.reads)
	cache.blocked = true
	require.True(t, s.codexQualityHealthBlocked(context.Background(), account), "ordinary half-open marker stays unavailable to diagnostics")
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
		rt := &codexQualityRuntime{store: store, installation: &PluginInstallation{}}
		e := &codexQualityExecution{runtime: rt, runID: run.RunID, grantDigest: run.GrantDigest, accountID: run.AccountID, stage: "acquire", operationID: uuid.NewString(), keyLookup: func(context.Context, int64) (*APIKey, error) { return &other, nil }}
		ctx := context.WithValue(context.Background(), codexQualityExecutionKey{}, e)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6-astra"}`))
		require.NoError(t, err)
		require.ErrorIs(t, reserveCodexQualitySend(req, &Account{ID: run.AccountID}, nil), ErrCodexQualityUnavailable)
		saved, _, err := readCodexQualityRun(context.Background(), store, run.RunID)
		require.NoError(t, err)
		require.Zero(t, saved.UsedSends, "revoked key must fail before the probe budget is spent")
	}
}

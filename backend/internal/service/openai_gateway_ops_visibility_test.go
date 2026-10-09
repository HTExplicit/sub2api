//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func opsVisibilityTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func opsVisibilityTestEvents(t *testing.T, c *gin.Context) []*OpsUpstreamErrorEvent {
	t.Helper()
	value, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := value.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	return events
}

func opsVisibilityTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.LogUpstreamErrorBody = true
	cfg.Gateway.LogUpstreamErrorBodyMaxBytes = 2048
	return cfg
}

// OPS-8: a budget_exceeded 429 is recorded like any other upstream failover
// attempt, and the account error keeps the structured type and code.
func TestOpenAIBudgetExceededHTTPFailoverRecordsUpstreamAttempt(t *testing.T) {
	const body = `{"error":{"message":"ExceededBudget: key over budget","type":"budget_exceeded","param":null,"code":"429"}}`
	cfg := opsVisibilityTestConfig()
	repo := &budgetExceededAccountRepoStub{}
	svc := &OpenAIGatewayService{cfg: cfg, rateLimitService: NewRateLimitService(repo, nil, cfg, nil, nil)}
	c := opsVisibilityTestContext()

	failoverErr, ok := svc.handleOpenAIBudgetExceededHTTPFailover(context.Background(), c, newBudgetRelayAccount(97101, false),
		http.StatusTooManyRequests, http.Header{"X-Request-Id": []string{"req_budget"}}, []byte(body))

	require.True(t, ok)
	require.Equal(t, NextAccountRetry, failoverErr.NextAccountAction)
	events := opsVisibilityTestEvents(t, c)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, openAIBudgetExceededKind, events[0].Reason)
	require.Equal(t, http.StatusTooManyRequests, events[0].UpstreamStatusCode)
	require.Equal(t, "req_budget", events[0].UpstreamRequestID)
	require.Equal(t, "ExceededBudget: key over budget", events[0].Message)
	require.Equal(t, body, events[0].Detail)
	require.Equal(t, "ExceededBudget: key over budget (type=budget_exceeded, code=429)", repo.lastErrorMsg)
}

type lastUpstreamErrorAccountRepo struct {
	stubOpenAIAccountRepo
	mu      sync.Mutex
	updates []map[string]any
}

func (r *lastUpstreamErrorAccountRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, updates)
	return nil
}

// OPS-9: the upstream 401/403/429 handled only by the request failover state is
// kept verbatim in extra.last_upstream_error; no account state changes.
func TestOpenAIAPIKeyAuthAndQuotaErrorsRecordLastUpstreamError(t *testing.T) {
	for index, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			body := `{"error":{"message":"Request blocked by the relay policy","type":"requests","code":"relay_blocked"}}`
			repo := &lastUpstreamErrorAccountRepo{}
			svc := &OpenAIGatewayService{accountRepo: repo}
			account := &Account{ID: int64(97201 + index), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
			key := openAILastUpstreamErrorKey{accountID: account.ID, status: statusCode}
			openAILastUpstreamErrorWritten.Delete(key)

			require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, statusCode, http.Header{}, []byte(body)))
			require.Eventually(t, func() bool {
				_, written := openAILastUpstreamErrorWritten.Load(key)
				return written
			}, 5*time.Second, 5*time.Millisecond, "the background write completes")
			// Same account and status inside the window: skipped whatever the body.
			require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, statusCode, http.Header{}, []byte(`{"error":{"message":"different text"}}`)))
			_, inflight := openAILastUpstreamErrorInflight.Load(key)
			require.False(t, inflight)

			repo.mu.Lock()
			defer repo.mu.Unlock()
			require.Len(t, repo.updates, 1, "one write per account and status inside the window")
			record, ok := repo.updates[0][OpenAILastUpstreamErrorExtraKey].(map[string]any)
			require.True(t, ok)
			require.Equal(t, statusCode, record["status"])
			require.Equal(t, body, record["message"])
			require.Equal(t, "gateway", record["source"])
			_, err := time.Parse(time.RFC3339, record["at"].(string))
			require.NoError(t, err)
			require.Equal(t, StatusActive, account.Status)
			require.True(t, account.Schedulable)
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

// OPS-5: an upstream error chunk inside a Chat Completions stream is recorded
// with its own message and payload instead of a truncation description.
func TestForwardAsRawChatCompletionsRecordsUpstreamErrorChunk(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		message string
		kind    string
	}{
		// The chunk ends this request: no other account is tried.
		{"request_scoped", `{"error":{"message":"upstream model crashed","type":"server_error","code":"internal"}}`, "upstream model crashed", "stream_error"},
		// Nothing reached the client and the chunk moves the request on.
		{"switches_account", `{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded"}}`, "Rate limit reached", "failover"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := opsVisibilityTestContext()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_chunk"}},
				Body:       io.NopCloser(strings.NewReader("data: " + tc.payload + "\n\n")),
			}}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.LogUpstreamErrorBody = true
			cfg.Gateway.LogUpstreamErrorBodyMaxBytes = 2048
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"stream":true}`)

			_, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")

			require.Error(t, err)
			events := opsVisibilityTestEvents(t, c)
			require.Len(t, events, 1, "the error chunk is not re-described as a truncated stream")
			require.Equal(t, tc.kind, events[0].Kind)
			require.Equal(t, tc.message, events[0].Message)
			require.Equal(t, tc.payload, events[0].Detail)
			require.Equal(t, "req_chunk", events[0].UpstreamRequestID)
			message, _ := c.Get(OpsUpstreamErrorMessageKey)
			require.Equal(t, tc.message, message)
		})
	}
}

// OPS-6: the upstream's own model_not_supported text reaches the attempt.
func TestOpenAIModelNotSupportedAttemptKeepsUpstreamText(t *testing.T) {
	c := opsVisibilityTestContext()
	payload := []byte(`{"error":{"type":"model_not_supported","code":"400","message":"The model 'gpt-x' is not supported for this key"}}`)
	svc := &OpenAIGatewayService{cfg: opsVisibilityTestConfig()}

	svc.recordOpenAIModelNotSupportedAttempt(c, &Account{ID: 7, Name: "relay", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, http.Header{}, payload, false)

	events := opsVisibilityTestEvents(t, c)
	require.Len(t, events, 1)
	require.Equal(t, string(openAIModelNotSupportedReason), events[0].Reason)
	require.Equal(t, "The model 'gpt-x' is not supported for this key", events[0].Message)
	require.Equal(t, string(payload), events[0].Detail)
}

// OPS-7: a refusal rewrite or prompt retry records the matched keyword and the
// original leading text for administrators.
func TestOpenAIRefusalRecoveryRecordsKeywordAndOriginalText(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"I cannot"}, "继续当前任务")
	require.NoError(t, err)
	body := []byte(`{"id":"resp_1","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"I cannot help with that request."}]}]}`)
	_, matched, evidence, err := rewriteOpenAIResponsesJSONWithEvidence(body, matcher)
	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, "I cannot", evidence.Keyword)
	require.Equal(t, "I cannot help with that request.", evidence.Text)

	c := opsVisibilityTestContext()
	recordOpenAIRefusalRecovery(context.Background(), c, &Account{ID: 8, Platform: PlatformOpenAI}, "http", false, false, openAIRefusalActionPromptRetry, evidence)
	// A handled refusal is not upstream error context: a request that then
	// succeeds leaves no Ops row, and nothing classifies on it.
	_, recordedAsUpstream := c.Get(OpsUpstreamErrorsKey)
	require.False(t, recordedAsUpstream)
	events := OpsRefusalRecoveryEvents(c)
	require.Len(t, events, 1)
	require.Equal(t, "refusal_recovery", events[0].Kind)
	require.Equal(t, openAIRefusalActionPromptRetry, events[0].Reason)
	require.Contains(t, events[0].Message, `"I cannot"`)
	require.Equal(t, "I cannot help with that request.", events[0].Detail)

	// WebSocket refusals are logged only.
	wsContext := opsVisibilityTestContext()
	recordOpenAIRefusalRecovery(context.Background(), wsContext, &Account{ID: 8, Platform: PlatformOpenAI}, "websocket", true, false, openAIRefusalActionRewritten, evidence)
	_, recordedAsUpstream = wsContext.Get(OpsUpstreamErrorsKey)
	require.False(t, recordedAsUpstream)
	require.Empty(t, OpsRefusalRecoveryEvents(wsContext))
}

// OPS-11: image transport failures keep the upstream endpoint on the attempt.
func TestOpenAIImagesTransportErrorKeepsUpstreamURL(t *testing.T) {
	c := opsVisibilityTestContext()
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	err := svc.handleOpenAIImagesUpstreamTransportError(context.Background(), c, account, errors.New("dial tcp: connection refused"),
		"https://api.example.test/v1/images/generations?api-version=1")

	require.Error(t, err)
	events := opsVisibilityTestEvents(t, c)
	require.Len(t, events, 1)
	require.Equal(t, "request_error", events[0].Kind)
	require.Equal(t, "https://api.example.test/v1/images/generations", events[0].UpstreamURL)
}

// C-13: the model's complete words behind an image failure reach Ops and the
// connection test; the client keeps the bounded text it always received.
func TestOpenAIImagesModelTextKeepsFullTextForAdministrators(t *testing.T) {
	text := "Blocked by our content policy. " + strings.Repeat("详细原因 ", 300)
	body := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + text + "\"}\n\n")

	require.Equal(t, strings.TrimSpace(text), extractOpenAIImagesModelText(body))
	upstreamErr := openAIImagesTextFallbackError(body)
	require.NotNil(t, upstreamErr)
	require.Equal(t, "content_policy_violation", upstreamErr.Code)
	// The client text is computed exactly as before: trim, cut at 600 bytes,
	// trim again.
	legacy := strings.TrimSpace(text)[:600]
	require.Equal(t, sanitizeUpstreamErrorMessage(strings.TrimSpace(legacy)), upstreamErr.Message)
	require.Equal(t, strings.TrimSpace(text), upstreamErr.ModelText)
	require.Equal(t, strings.TrimSpace(text), upstreamErr.opsMessage())

	// Streamed text keeps its raw first 600 bytes, leading whitespace included.
	streamed := "\n\n   " + text
	streamErr := openAIImagesTextFallbackErrorForWindow(openAIImagesModelTextClientWindow(streamed), streamed)
	require.NotNil(t, streamErr)
	require.Equal(t, sanitizeUpstreamErrorMessage(strings.TrimSpace(streamed[:600])), streamErr.Message)
	require.Equal(t, strings.TrimSpace(streamed), streamErr.ModelText)
}

// OPS-10: the reasoning-recovery detail is bounded by its encoded size, so the
// Ops queue never shrinks it to an object without its fields.

// OPS-4: a delivered cyber-policy block keeps only its dedicated row; other
// delivered failures are marked as client-visible.
func TestOpenAIWSDeliveredFailureSkipsCyberPolicy(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"cyber_policy","message":"blocked"}}`),
		[]byte(`{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"blocked"}}}`),
	} {
		c := opsVisibilityTestContext()
		markOpenAIWSDeliveredFailure(c, payload)
		require.Empty(t, GetOpsStreamErrors(c))
	}
	c := opsVisibilityTestContext()
	markOpenAIWSDeliveredFailure(c, []byte(`{"type":"error","error":{"type":"server_error","code":"server_error","message":"boom"}}`))
	require.Len(t, GetOpsStreamErrors(c), 1)
}

// A budget terminal counts as a failover only while the request can still move
// to another account.
func TestOpenAIBudgetExceededTerminalKindFollowsReplayability(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","message":"ExceededBudget: key over budget"}}}`)
	cfg := opsVisibilityTestConfig()
	svc := &OpenAIGatewayService{cfg: cfg, rateLimitService: NewRateLimitService(&budgetExceededAccountRepoStub{}, nil, cfg, nil, nil)}
	for _, tc := range []struct {
		name    string
		started bool
		kind    string
	}{{"before_output", false, "failover"}, {"after_output", true, "stream_error"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := opsVisibilityTestContext()
			if tc.started {
				c.Writer.WriteHeaderNow()
			}
			_, ok := svc.openAIBudgetExceededHTTPResponseTerminalFailover(context.Background(), c, newBudgetRelayAccount(97301, false), http.StatusOK, http.Header{}, payload)
			require.True(t, ok)
			events := opsVisibilityTestEvents(t, c)
			require.Len(t, events, 1)
			require.Equal(t, tc.kind, events[0].Kind)
		})
	}
}

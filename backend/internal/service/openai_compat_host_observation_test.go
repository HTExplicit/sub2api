//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type compatObservationUpstream struct {
	httpUpstreamRecorder
	onSend func(*http.Request)
}

func (u *compatObservationUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	if u.onSend != nil {
		u.onSend(req)
	}
	return u.httpUpstreamRecorder.Do(req, proxyURL, accountID, concurrency)
}

func compatObservationContext(t *testing.T, protocol string, stream bool) (*gin.Context, []byte, *observer.ObservedLogs) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	path := "/v1/chat/completions"
	if protocol == "messages" {
		path = "/v1/messages"
	}
	body := []byte(fmt.Sprintf(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"fixture"}],"stream":%t}`, stream))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	core, logs := observer.New(zap.WarnLevel)
	c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), zap.New(core)))
	return c, body, logs
}

func compatObservationResponse(withUsage, failed bool) *http.Response {
	usage := ""
	if withUsage {
		usage = `,"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}`
	}
	terminal := `data: {"type":"response.completed","response":{"id":"resp_observation","model":"gpt-5.4","status":"completed","output":[{"id":"msg_observation","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fixture response"}]}]` + usage + "}}\n\n"
	if failed {
		terminal = "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_observation\",\"model\":\"gpt-5.4\",\"status\":\"failed\",\"error\":{\"code\":\"context_length_exceeded\",\"message\":\"fixture context limit\"}}}\n\n"
	}
	// A duplicate terminal must not duplicate host-side observation.
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"fixture-observation"},
	}, Body: io.NopCloser(strings.NewReader(terminal + terminal))}
}

func forwardCompatObservation(svc *OpenAIGatewayService, protocol string, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	if protocol == "messages" {
		return svc.ForwardAsAnthropic(c.Request.Context(), c, account, body, "", "")
	}
	return svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body, "", "")
}

// These tests are intentionally not parallel: the production sampler and
// notifier are process-wide. Restore their controls without starting workers.
func observeCompatMissingUsage(t *testing.T) uint64 {
	t.Helper()
	previousLast := openAIMissingUsageSampler.lastLog.Swap(0)
	previousSuppressed := openAIMissingUsageSampler.suppressed.Swap(0)
	t.Cleanup(func() {
		openAIMissingUsageSampler.lastLog.Store(previousLast)
		openAIMissingUsageSampler.suppressed.Store(previousSuppressed)
	})
	return openAIMissingUsageSampler.total.Load()
}

func captureCompatQuotaNotifications(t *testing.T) *OpenAIQuotaAutoResetService {
	t.Helper()
	capture := &OpenAIQuotaAutoResetService{ctx: context.Background(), queue: make(chan int64, 4)}
	openAIAutoResetNotifierRegistry.Lock()
	previous := openAIAutoResetNotifierRegistry.service
	openAIAutoResetNotifierRegistry.service = capture
	openAIAutoResetNotifierRegistry.Unlock()
	t.Cleanup(func() {
		openAIAutoResetNotifierRegistry.Lock()
		openAIAutoResetNotifierRegistry.service = previous
		openAIAutoResetNotifierRegistry.Unlock()
	})
	return capture
}

func compatObservationShadow() (*Account, *stubOpenAIAccountRepo, int64) {
	parentID := int64(92801)
	parent := Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "fixture-parent-token", "chatgpt_account_id": "fixture-parent"}}
	shadow := &Account{ID: 92802, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark}
	return shadow, &stubOpenAIAccountRepo{accounts: []Account{parent}}, parentID
}

func TestCompatHostObservations_CCFinalModelAtDispatch(t *testing.T) {
	c, body, _ := compatObservationContext(t, "chat", false)
	body, err := sjson.SetBytes(body, "model", "client-alias")
	require.NoError(t, err)
	account := &Account{ID: 92803, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "fixture-token", "chatgpt_account_id": "fixture-account",
			"model_mapping": map[string]any{"client-alias": "gpt-5.4"}}}
	c.Set(OpsUpstreamModelKey, "stale-previous-attempt")
	observed := ""
	upstream := &compatObservationUpstream{httpUpstreamRecorder: httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"fixture stops after dispatch"}}`)),
	}}, onSend: func(*http.Request) { observed = c.GetString(OpsUpstreamModelKey) }}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	_, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body, "", "")
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	wireModel := gjson.GetBytes(upstream.lastBody, "model").String()
	require.NotEqual(t, "client-alias", wireModel)
	require.NotEmpty(t, wireModel)
	require.Equal(t, wireModel, observed, "final model must be recorded before sending, even on upstream failure")
}

func TestCompatHostObservations_MissingUsageWarnsOnce(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", protocol, stream), func(t *testing.T) {
				before := observeCompatMissingUsage(t)
				c, body, logs := compatObservationContext(t, protocol, stream)
				upstream := &httpUpstreamRecorder{resp: compatObservationResponse(false, false)}
				svc := newOpenAIRejectedFieldTestService(upstream)
				result, err := forwardCompatObservation(svc, protocol, c, newOpenAIRejectedFieldTestAccount(), body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Zero(t, result.Usage.InputTokens)
				require.Zero(t, result.Usage.OutputTokens)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, uint64(1), openAIMissingUsageSampler.total.Load()-before, "sampling suppression must not hide duplicate calls")
				warnings := logs.FilterMessage("openai_usage.success_missing_usage").All()
				require.Len(t, warnings, 1)
				require.Equal(t, "response.completed", warnings[0].ContextMap()["terminal_event"])
			})
		}
	}
}

func TestCompatHostObservations_ShadowSuccessNotifiesParent(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			capture := captureCompatQuotaNotifications(t)
			before := observeCompatMissingUsage(t)
			c, body, logs := compatObservationContext(t, protocol, protocol == "chat")
			shadow, repo, parentID := compatObservationShadow()
			upstream := &httpUpstreamRecorder{resp: compatObservationResponse(true, false)}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: repo}
			result, err := forwardCompatObservation(svc, protocol, c, shadow, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 4, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
			require.Len(t, upstream.requests, 1, "notification must not query quota or retry upstream")
			require.Equal(t, before, openAIMissingUsageSampler.total.Load(), "real usage must not be counted as missing")
			require.Empty(t, logs.FilterMessage("openai_usage.success_missing_usage").All())
			require.Len(t, capture.queue, 1)
			require.Equal(t, parentID, <-capture.queue)
			require.Empty(t, shadow.Credentials)
		})
	}
}

func TestCompatHostObservations_RecoveryLogsOnlyFinalSuccess(t *testing.T) {
	cache := newOpenAIChatReplayTestCache()
	completed, err := sjson.DeleteBytes(openAIChatReplayTestPayload(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]`), "response.usage")
	require.NoError(t, err)
	failed := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}}}\n\n"
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		openAIChatReplayTestResponse(openAIChatReplayTestSSE(openAIChatReplayTestOutput, false), http.StatusOK),
		openAIChatReplayTestResponse(failed, http.StatusOK),
		openAIChatReplayTestResponse("data: "+string(completed)+"\n\n", http.StatusOK),
	}}
	svc := newOpenAIRejectedFieldTestService(upstream)
	svc.cache = cache
	account := newOpenAIRejectedFieldTestAccount()
	first := openAIChatReplayTestBody(t, false, false, false)
	c, _ := openAIChatReplayTestContext(t, first)
	_, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, first, "", "")
	require.NoError(t, err)
	second := openAIChatReplayTestBody(t, true, false, false)
	c, _ = openAIChatReplayTestContext(t, second)
	core, logs := observer.New(zap.WarnLevel)
	c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), zap.New(core)))
	before := observeCompatMissingUsage(t)
	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account, second, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 3, "only the existing priming, rejected replay and bounded recovery may send")
	require.Len(t, cache.rejected, 1)
	require.Equal(t, uint64(1), openAIMissingUsageSampler.total.Load()-before)
	require.Len(t, logs.FilterMessage("openai_usage.success_missing_usage").All(), 1)
}

func TestCompatHostObservations_FailedAttemptsEmitNoSuccessSignals(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", protocol, stream), func(t *testing.T) {
				capture := captureCompatQuotaNotifications(t)
				before := observeCompatMissingUsage(t)
				c, body, logs := compatObservationContext(t, protocol, stream)
				shadow, repo, _ := compatObservationShadow()
				upstream := &httpUpstreamRecorder{resp: compatObservationResponse(false, true)}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: repo}
				_, err := forwardCompatObservation(svc, protocol, c, shadow, body)
				require.Error(t, err)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, before, openAIMissingUsageSampler.total.Load())
				require.Empty(t, logs.FilterMessage("openai_usage.success_missing_usage").All())
				require.Empty(t, capture.queue)
			})
		}
	}
}

func TestCompatHostObservations_IncompleteDoesNotCountAsCompleted(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", protocol, stream), func(t *testing.T) {
				before := observeCompatMissingUsage(t)
				c, body, logs := compatObservationContext(t, protocol, stream)
				payload := "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_incomplete\",\"model\":\"gpt-5.4\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"partial fixture\"}]}]}}\n\n"
				upstream := &httpUpstreamRecorder{resp: openAIChatReplayTestResponse(payload, http.StatusOK)}
				svc := newOpenAIRejectedFieldTestService(upstream)
				result, err := forwardCompatObservation(svc, protocol, c, newOpenAIRejectedFieldTestAccount(), body)
				require.NoError(t, err, "the existing bounded-output response contract must remain unchanged")
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, before, openAIMissingUsageSampler.total.Load())
				require.Empty(t, logs.FilterMessage("openai_usage.success_missing_usage").All())
			})
		}
	}
}

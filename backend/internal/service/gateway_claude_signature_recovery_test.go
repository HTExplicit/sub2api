package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const claudeRecoveryTestBody = `{"model":"claude-sonnet-5-5","stream":true,"thinking":{"type":"between_tools"},"output_config":{"effort":"high"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"old reasoning","signature":"private-signature-value"},{"type":"text","text":"old answer"}]},{"role":"user","content":"continue"}]}`
const claudeRecoverySignatureError = `{"type":"error","error":{"message":"Invalid signature in thinking block"}}`

type claudeRecoverySequenceUpstream struct {
	responses  []*http.Response
	requests   []*http.Request
	bodies     [][]byte
	accountIDs []int64
	proxyURLs  []string
	beforeSend func(int)
}

func (u *claudeRecoverySequenceUpstream) Do(req *http.Request, proxy string, id int64, _ int) (*http.Response, error) {
	if u.beforeSend != nil {
		u.beforeSend(len(u.requests))
	}
	u.requests = append(u.requests, req)
	u.accountIDs = append(u.accountIDs, id)
	u.proxyURLs = append(u.proxyURLs, proxy)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.bodies = append(u.bodies, body)
	if len(u.responses) == 0 {
		return nil, errors.New("unexpected additional request")
	}
	response := u.responses[0]
	u.responses = u.responses[1:]
	return response, nil
}

type claudeRecoveryCloseObservedBody struct {
	io.ReadCloser
	closed bool
}

func (body *claudeRecoveryCloseObservedBody) Close() error {
	body.closed = true
	return body.ReadCloser.Close()
}

func (u *claudeRecoverySequenceUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func claudeRecoveryResponse(status int, body, id string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{id}}, Body: io.NopCloser(strings.NewReader(body))}
}

func claudeRecoverySuccessStream() string {
	return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"final-message\",\"model\":\"claude-sonnet-5-5\",\"usage\":{\"input_tokens\":7}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
}

func claudeRecoveryService(upstream *claudeRecoverySequenceUpstream, enabled bool) *GatewayService {
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	settings := fmt.Sprintf(`{"enabled":true,"thinking_signature_enabled":true,"apikey_signature_enabled":%t}`, enabled)
	return &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream,
		rateLimitService: &RateLimitService{}, deferredService: &DeferredService{},
		settingService: NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{SettingKeyRectifierSettings: settings}}, cfg)}
}

func TestClaudeSignatureRecoveryForwardHTTPAndSSE(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		prelude string
	}{
		{"http_400", 400, ""},
		{"sse_before_message", 200, ""},
		{"sse_after_ping", 200, "event: ping\ndata: {\"type\":\"ping\"}\n\n"},
		{"sse_after_held_message_start", 200, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"rejected-message\",\"model\":\"other-model\",\"usage\":{\"input_tokens\":900}}}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			firstBody := claudeRecoverySignatureError
			if tc.status == 200 {
				firstBody = tc.prelude + "event: error\ndata: " + firstBody + "\n\n"
			}
			upstream := &claudeRecoverySequenceUpstream{responses: []*http.Response{
				claudeRecoveryResponse(tc.status, firstBody, "initial-rid"), claudeRecoveryResponse(200, claudeRecoverySuccessStream(), "repaired-rid"),
			}}
			initialBody := &claudeRecoveryCloseObservedBody{ReadCloser: upstream.responses[0].Body}
			upstream.responses[0].Body = initialBody
			upstream.beforeSend = func(index int) {
				if index == 1 {
					require.True(t, initialBody.closed, "the rejected response must release its connection before repair sends")
				}
			}
			account := newAnthropicAPIKeyAccountForTest()
			account.Extra = nil
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(claudeRecoveryTestBody)), PlatformAnthropic)
			require.NoError(t, err)
			acceptedCalls := 0
			parsed.OnUpstreamAccepted = func() { acceptedCalls++ }
			result, err := claudeRecoveryService(upstream, true).Forward(context.Background(), c, account, parsed)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 2)
			require.Equal(t, []int64{account.ID, account.ID}, upstream.accountIDs)
			require.Equal(t, upstream.requests[0].URL, upstream.requests[1].URL)
			require.Equal(t, upstream.requests[0].Header.Get("X-Api-Key"), upstream.requests[1].Header.Get("X-Api-Key"))
			require.Equal(t, 1, acceptedCalls)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.Equal(t, "repaired-rid", result.RequestID)
			require.False(t, result.UpstreamResponseModelConflict)
			require.NotContains(t, rec.Body.String(), "rejected-message")
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: message_start"))
			require.NotContains(t, string(upstream.bodies[1]), "private-signature-value")
			require.Equal(t, "between_tools", gjson.GetBytes(upstream.bodies[1], "thinking.type").String())
			require.Equal(t, "high", gjson.GetBytes(upstream.bodies[1], "output_config.effort").String())
			require.True(t, bytes.Equal(parsed.Body.Bytes(), upstream.bodies[1]))
			events, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			trigger := events.([]*OpsUpstreamErrorEvent)[0]
			require.NotNil(t, trigger.SignatureRecovery)
			require.Equal(t, "accepted", trigger.SignatureRecovery.Outcome)
			require.Equal(t, 1, trigger.SignatureRecovery.Attempts)
			require.Equal(t, 1, trigger.SignatureRecovery.Inbound.TotalCount)
			diagnosticJSON, err := json.Marshal(trigger.SignatureRecovery)
			require.NoError(t, err)
			require.NotContains(t, string(diagnosticJSON), "private-signature-value")
			require.NotContains(t, string(diagnosticJSON), "old reasoning")
		})
	}
}

func TestClaudeSignatureRecoveryDoesNotReplaySSEAgainOrAfterOutput(t *testing.T) {
	for _, prefix := range []string{"", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"} {
		t.Run(fmt.Sprintf("output_%t", prefix != ""), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			errStream := prefix + "event: error\ndata: " + claudeRecoverySignatureError + "\n\n"
			upstream := &claudeRecoverySequenceUpstream{responses: []*http.Response{claudeRecoveryResponse(200, errStream, "one"), claudeRecoveryResponse(200, errStream, "two")}}
			account := newAnthropicAPIKeyAccountForTest()
			account.Extra = nil
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(claudeRecoveryTestBody)), PlatformAnthropic)
			require.NoError(t, err)
			_, err = claudeRecoveryService(upstream, true).Forward(context.Background(), c, account, parsed)
			require.Error(t, err)
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, 400, failure.StatusCode)
			require.False(t, failure.ShouldRetryNextAccount())
			require.False(t, failure.ShouldReportAccountScheduleFailure())
			wantRequests := 2
			if prefix != "" {
				wantRequests = 1
			}
			require.Len(t, upstream.requests, wantRequests)
			events, _ := c.Get(OpsUpstreamErrorsKey)
			last := events.([]*OpsUpstreamErrorEvent)[wantRequests-1]
			wantReason := "attempt_limit"
			if prefix != "" {
				wantReason = "output_started"
			}
			require.Equal(t, wantReason, last.SignatureRecovery.Reason)
		})
	}
}

func TestClaudeSignatureRecoveryDisabledAndPassthrough(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough_%t", passthrough), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			upstream := &claudeRecoverySequenceUpstream{responses: []*http.Response{claudeRecoveryResponse(400, claudeRecoverySignatureError, "original")}}
			account := newAnthropicAPIKeyAccountForTest()
			if !passthrough {
				account.Extra = nil
			}
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(claudeRecoveryTestBody)), PlatformAnthropic)
			require.NoError(t, err)
			_, err = claudeRecoveryService(upstream, passthrough).Forward(context.Background(), c, account, parsed)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1)
			require.Contains(t, string(upstream.bodies[0]), "private-signature-value")
		})
	}
}

func TestClaudeSignatureRecoveryDecisionAndFailureEvidence(t *testing.T) {
	for _, reason := range []string{"build_failed", "transport_failed", "empty_response", "budget_exhausted", "context_canceled", "body_unchanged", "output_started", "attempt_limit"} {
		t.Run(reason, func(t *testing.T) {
			state := newClaudeSignatureRecoveryState([]byte(claudeRecoveryTestBody))
			diag := state.diagnostic("http_400", false)
			ctx := context.Background()
			body := []byte(claudeRecoveryTestBody)
			sent := 0
			clock := time.Unix(0, 0)
			state.now = func() time.Time { return clock }
			if reason == "context_canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if reason == "body_unchanged" {
				diag.Source = "sse_error"
				body = []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
			}
			if reason == "output_started" {
				diag.MessageStarted = true
			}
			if reason == "attempt_limit" {
				state.used = true
			}
			_, _, _ = state.run(ctx, diag, body, "claude-sonnet-5-5", func(body []byte) (*http.Request, []byte, error) {
				if reason == "build_failed" {
					return nil, nil, errors.New("build detail")
				}
				if reason == "budget_exhausted" {
					clock = clock.Add(11 * time.Second)
				}
				req, err := http.NewRequest(http.MethodPost, "https://example.invalid/v1/messages", bytes.NewReader(body))
				return req, body, err
			}, func(*http.Request) (*http.Response, error) {
				sent++
				if reason == "transport_failed" {
					return nil, errors.New("transport detail")
				}
				return nil, nil
			})
			require.Equal(t, reason, diag.Reason)
			require.Equal(t, sent, diag.Attempts)
			if reason == "transport_failed" || reason == "empty_response" {
				require.Equal(t, 1, sent)
			} else {
				require.Zero(t, sent)
			}
		})
	}
}

func TestClaudeSignatureRecoveryFingerprintAndSanitizer(t *testing.T) {
	blocks := make([]string, 20)
	for i := range blocks {
		blocks[i] = fmt.Sprintf(`{"type":"thinking","signature":"secret-%d"}`, i)
	}
	body := []byte(`{"messages":[{"role":"assistant","content":[` + strings.Join(blocks, ",") + `]}]}`)
	state := newClaudeSignatureRecoveryState(body)
	state.observeFirstWire(body)
	state.observeFirstWire([]byte(`{}`))
	diag := state.diagnostic("sse_error", false)
	require.Equal(t, 20, diag.Inbound.TotalCount)
	require.Equal(t, 4, diag.Inbound.OmittedCount)
	require.Len(t, diag.Inbound.Signatures, 16)
	require.Equal(t, diag.Inbound, diag.FirstWire)
	require.Equal(t, "/messages/0/content/0/signature", diag.Inbound.Signatures[0].Path)
	diag.Attempts = 100
	diag.ElapsedMs = -1
	diag.Reason = "private text"
	diag.FirstWire.Signatures[0].Path = "/messages/private-text"
	safe := sanitizeClaudeSignatureRecoveryDiagnostic(diag)
	require.Equal(t, 2, safe.Attempts)
	require.Zero(t, safe.ElapsedMs)
	require.Empty(t, safe.Reason)
	require.Len(t, safe.FirstWire.Signatures, 15)
	safe.Inbound.Signatures[0].Length = 0
	require.NotZero(t, diag.Inbound.Signatures[0].Length)
	encoded, err := json.Marshal(safe)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-")
	require.NotContains(t, string(encoded), "private text")
}

func TestClaudeSignatureRecoverySharedHTTPAndSSELimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	errStream := "event: error\ndata: " + claudeRecoverySignatureError + "\n\n"
	upstream := &claudeRecoverySequenceUpstream{responses: []*http.Response{
		claudeRecoveryResponse(400, claudeRecoverySignatureError, "http-rejection"),
		claudeRecoveryResponse(200, errStream, "sse-rejection"),
	}}
	account := newAnthropicAPIKeyAccountForTest()
	account.Extra = nil
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(claudeRecoveryTestBody)), PlatformAnthropic)
	require.NoError(t, err)
	_, err = claudeRecoveryService(upstream, true).Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	require.Len(t, upstream.requests, 2)
	events, _ := c.Get(OpsUpstreamErrorsKey)
	attempts := events.([]*OpsUpstreamErrorEvent)
	require.Equal(t, "attempt_limit", attempts[len(attempts)-1].SignatureRecovery.Reason)
}

func TestClaudeSignatureRecoveryFailureStopsWithoutHealthPenalty(t *testing.T) {
	for _, streamFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_failure_%t", streamFailure), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			serviceError := `{"type":"error","error":{"type":"service_error","message":"temporarily unavailable"}}`
			repairedStatus := 503
			repairedBody := serviceError
			if streamFailure {
				repairedStatus = 200
				repairedBody = "event: error\ndata: " + serviceError + "\n\n"
			}
			upstream := &claudeRecoverySequenceUpstream{responses: []*http.Response{
				claudeRecoveryResponse(200, "event: error\ndata: "+claudeRecoverySignatureError+"\n\n", "initial"),
				claudeRecoveryResponse(repairedStatus, repairedBody, "retry"),
			}}
			account := newAnthropicAPIKeyAccountForTest()
			account.Extra = nil
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(claudeRecoveryTestBody)), PlatformAnthropic)
			require.NoError(t, err)
			_, err = claudeRecoveryService(upstream, true).Forward(context.Background(), c, account, parsed)
			require.Error(t, err)
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.False(t, failure.ShouldRetryNextAccount())
			require.False(t, failure.ShouldReportAccountScheduleFailure())
			require.Equal(t, serviceError, string(failure.ResponseBody))
			require.Len(t, upstream.requests, 2)
		})
	}
}

func TestClaudeSignatureRecoveryExplicitRequestPatternMatchesPrelude(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// This is an existing HTTP rectifier match without a "thinking" word; it
	// must agree with the prelude handler when the provider declares HTTP 400.
	first := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"discard-me\"}}\n\nevent: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"signature is invalid\"}}\n\n"
	upstream := &claudeRecoverySequenceUpstream{responses: []*http.Response{claudeRecoveryResponse(200, first, "first"), claudeRecoveryResponse(200, claudeRecoverySuccessStream(), "second")}}
	account := newAnthropicAPIKeyAccountForTest()
	account.Extra = nil
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(claudeRecoveryTestBody)), PlatformAnthropic)
	require.NoError(t, err)
	result, err := claudeRecoveryService(upstream, true).Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.NotContains(t, rec.Body.String(), "discard-me")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: message_start"))
}

func TestClaudeSignatureRecoverySlowInitialResponseDoesNotSpendDecisionBudget(t *testing.T) {
	clock := time.Unix(0, 0)
	state := newClaudeSignatureRecoveryState([]byte(claudeRecoveryTestBody))
	state.now = func() time.Time { return clock }
	state.observeFirstWire([]byte(claudeRecoveryTestBody))
	// Twenty seconds pass while waiting for the first upstream rejection.
	clock = clock.Add(20 * time.Second)
	diag := state.diagnostic("http_400", false)
	sent := 0
	resp, _, err := state.run(context.Background(), diag, []byte(claudeRecoveryTestBody), "claude-sonnet-5-5",
		func(body []byte) (*http.Request, []byte, error) {
			req, err := http.NewRequest(http.MethodPost, "https://example.invalid/v1/messages", bytes.NewReader(body))
			return req, body, err
		},
		func(*http.Request) (*http.Response, error) {
			sent++
			clock = clock.Add(time.Second)
			return claudeRecoveryResponse(200, claudeRecoverySuccessStream(), "fresh"), nil
		})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 1, sent)
	require.Equal(t, "accepted", diag.Outcome)
	require.Equal(t, int64(1000), diag.ElapsedMs)
}

func TestClaudeSignatureRecoveryIncompleteRepairedStreamRecordsActualAttempt(t *testing.T) {
	for _, initialHTTP := range []bool{false, true} {
		t.Run(fmt.Sprintf("initial_http_%t", initialHTTP), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			firstStatus := 200
			firstBody := "event: error\ndata: " + claudeRecoverySignatureError + "\n\n"
			if initialHTTP {
				firstStatus = 400
				firstBody = claudeRecoverySignatureError
			}
			incomplete := strings.Split(claudeRecoverySuccessStream(), "event: message_stop")[0]
			upstream := &claudeRecoverySequenceUpstream{responses: []*http.Response{claudeRecoveryResponse(firstStatus, firstBody, "initial"), claudeRecoveryResponse(200, incomplete, "incomplete-retry")}}
			account := newAnthropicAPIKeyAccountForTest()
			account.Extra = nil
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(claudeRecoveryTestBody)), PlatformAnthropic)
			require.NoError(t, err)
			_, err = claudeRecoveryService(upstream, true).Forward(context.Background(), c, account, parsed)
			require.Error(t, err)
			require.Contains(t, err.Error(), "missing terminal")
			events, _ := c.Get(OpsUpstreamErrorsKey)
			attempts := events.([]*OpsUpstreamErrorEvent)
			require.Equal(t, "response_read_failed", attempts[0].SignatureRecovery.Outcome)
			require.Equal(t, "stream_incomplete", attempts[0].SignatureRecovery.Reason)
			last := attempts[len(attempts)-1]
			require.Equal(t, "stream_failure", last.Kind)
			require.Equal(t, 502, last.UpstreamStatusCode)
			require.Equal(t, 200, last.UpstreamHTTPStatusCode)
			require.Equal(t, "incomplete-retry", last.UpstreamRequestID)
			require.Len(t, upstream.requests, 2)
		})
	}
}

func TestClaudeSignatureRecoveryHTTPToolOnlyKeepsExistingSecondStage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"model":"claude-opus-5-5","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tool1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool1","content":"result"}]}]}`)
	errBody := `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in tool_use block"}}`
	upstream := &claudeRecoverySequenceUpstream{responses: []*http.Response{
		claudeRecoveryResponse(400, errBody, "original"), claudeRecoveryResponse(400, errBody, "first-repair"),
		claudeRecoveryResponse(200, `{"type":"message","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":7,"output_tokens":3}}`, "tool-repair"),
	}}
	account := newAnthropicAPIKeyAccountForTest()
	account.Extra = nil
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	result, err := claudeRecoveryService(upstream, true).Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, gjson.GetBytes(upstream.bodies[0], "messages").Raw, gjson.GetBytes(upstream.bodies[1], "messages").Raw)
	require.NotContains(t, string(upstream.bodies[2]), `"type":"tool_use"`)
	require.NotContains(t, string(upstream.bodies[2]), `"type":"tool_result"`)
	events, _ := c.Get(OpsUpstreamErrorsKey)
	first := events.([]*OpsUpstreamErrorEvent)[0]
	require.Equal(t, 2, first.SignatureRecovery.Attempts)
	require.Equal(t, "accepted", first.SignatureRecovery.Outcome)
}

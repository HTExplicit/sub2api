package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const claudePreludeSignatureError = `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`

func claudePreludeFrame(event, data string) string {
	return "event: " + event + "\ndata: " + data + "\n\n"
}

func claudePreludeStart(id string, inputTokens int) string {
	return claudePreludeFrame("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":%q,"model":"claude-sonnet-5-5","usage":{"input_tokens":%d}}}`, id, inputTokens))
}

func claudePreludeTestContext() (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return recorder, c
}

type claudePreludeFlushObserver struct {
	gin.ResponseWriter
	onFlush func()
}

func (w *claudePreludeFlushObserver) Flush() {
	w.ResponseWriter.Flush()
	w.onFlush()
}

func runClaudePreludeTestStream(t *testing.T, svc *GatewayService, c *gin.Context, stream string, recoverPrelude bool) (*streamingResult, error) {
	t.Helper()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(stream))}
	defer resp.Body.Close()
	return svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformAnthropic}, time.Now(), "claude-sonnet-5-5", "claude-sonnet-5-5", false, recoverPrelude)
}

func TestClaudePrelude_SignatureRejectionDiscardsStartButKeepsAttemptUsage(t *testing.T) {
	svc := newStreamingResponseTestGatewayService()
	recorder, c := claudePreludeTestContext()
	stream := claudePreludeStart("failed-message", 123) + claudePreludeFrame("ping", `{"type":"ping"}`) + ": keepalive\n\n" + claudePreludeFrame("error", claudePreludeSignatureError)

	result, err := runClaudePreludeTestStream(t, svc, c, stream, true)
	var sseErr *sseStreamErrorEventError
	require.True(t, errors.As(err, &sseErr))
	require.False(t, sseErr.MessageStarted, "ping and comments do not start this message")
	require.NotNil(t, result)
	require.Equal(t, 123, result.usage.InputTokens, "discarding a client prelude must not discard this attempt's observed metering")
	require.NotContains(t, recorder.Body.String(), "failed-message")
	require.Contains(t, recorder.Body.String(), "event: ping")
	require.Contains(t, recorder.Body.String(), ": keepalive")

	// A successful repaired attempt contributes its own usage and one message ID.
	beginUpstreamResponseModelObservation(c)
	success := claudePreludeStart("repaired-message", 7) + claudePreludeFrame("message_delta", `{"type":"message_delta","usage":{"output_tokens":3}}`) + claudePreludeFrame("message_stop", `{"type":"message_stop"}`)
	result, err = runClaudePreludeTestStream(t, svc, c, success, true)
	require.NoError(t, err)
	require.Equal(t, 7, result.usage.InputTokens)
	require.Equal(t, 3, result.usage.OutputTokens)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_start"))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
	require.Contains(t, recorder.Body.String(), "repaired-message")
}

func TestClaudePrelude_PingIsFlushedBeforeStartCommit(t *testing.T) {
	svc := newStreamingResponseTestGatewayService()
	recorder, c := claudePreludeTestContext()
	beforeCommit := ""
	c.Writer = &claudePreludeFlushObserver{ResponseWriter: c.Writer, onFlush: func() {
		if beforeCommit == "" {
			beforeCommit = recorder.Body.String()
		}
	}}
	stream := claudePreludeStart("message", 5) + claudePreludeFrame("ping", `{"type":"ping"}`) + claudePreludeFrame("message_stop", `{"type":"message_stop"}`)
	_, err := runClaudePreludeTestStream(t, svc, c, stream, true)
	require.NoError(t, err)
	require.Contains(t, beforeCommit, "event: ping")
	require.NotContains(t, beforeCommit, "event: message_start")
}

func TestClaudePrelude_CommitsStartBeforeContentAndPreservesSignatureDelta(t *testing.T) {
	svc := newStreamingResponseTestGatewayService()
	recorder, c := claudePreludeTestContext()
	start := claudePreludeStart("message", 5)
	contentStart := claudePreludeFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
	thinkingDelta := claudePreludeFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"A thought"}}`)
	signatureDelta := claudePreludeFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque+/=fragment"}}`)
	contentStop := claudePreludeFrame("content_block_stop", `{"type":"content_block_stop","index":0}`)
	stop := claudePreludeFrame("message_stop", `{"type":"message_stop"}`)

	_, err := runClaudePreludeTestStream(t, svc, c, start+contentStart+thinkingDelta+signatureDelta+contentStop+stop, true)
	require.NoError(t, err)
	require.Equal(t, start+contentStart+thinkingDelta+signatureDelta+contentStop+stop, recorder.Body.String(), "buffering must preserve all event order and signature bytes")
}

func TestClaudePrelude_ContentOutputPreventsSignatureRecovery(t *testing.T) {
	svc := newStreamingResponseTestGatewayService()
	recorder, c := claudePreludeTestContext()
	stream := claudePreludeStart("message", 5) + claudePreludeFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) + claudePreludeFrame("error", claudePreludeSignatureError)
	result, err := runClaudePreludeTestStream(t, svc, c, stream, true)
	var sseErr *sseStreamErrorEventError
	require.True(t, errors.As(err, &sseErr))
	require.True(t, sseErr.MessageStarted)
	require.Equal(t, 5, result.usage.InputTokens)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_start"))
	require.Contains(t, recorder.Body.String(), "event: content_block_start")
}

func TestClaudePrelude_OversizedStartCommitsAndPreventsRecovery(t *testing.T) {
	svc := newStreamingResponseTestGatewayService()
	recorder, c := claudePreludeTestContext()
	start := claudePreludeFrame("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":%q,"usage":{"input_tokens":5}}}`, strings.Repeat("x", claudeMessageStartPreludeLimit)))
	_, err := runClaudePreludeTestStream(t, svc, c, start+claudePreludeFrame("error", claudePreludeSignatureError), true)
	var sseErr *sseStreamErrorEventError
	require.True(t, errors.As(err, &sseErr))
	require.True(t, sseErr.MessageStarted)
	require.Equal(t, start, recorder.Body.String())
}

func TestClaudePrelude_NonSignatureRejectionCommitsStart(t *testing.T) {
	for _, errorJSON := range []string{
		`{"type":"error","error":{"type":"service_error","message":"Service temporarily unavailable"}}`,
		`{"type":"error","error":{"type":"service_error","message":"Invalid signature in thinking block"}}`,
		`{"type":"error","error":{"type":"permission_error","message":"Invalid signature in thinking block"}}`,
	} {
		t.Run(errorJSON, func(t *testing.T) {
			svc := newStreamingResponseTestGatewayService()
			recorder, c := claudePreludeTestContext()
			start := claudePreludeStart("message", 5)
			result, err := runClaudePreludeTestStream(t, svc, c, start+claudePreludeFrame("error", errorJSON), true)
			var sseErr *sseStreamErrorEventError
			require.True(t, errors.As(err, &sseErr))
			require.True(t, sseErr.MessageStarted)
			require.Equal(t, 5, result.usage.InputTokens)
			require.Equal(t, start, recorder.Body.String())
		})
	}
}

func TestClaudePrelude_EOFCommitsStartAndEndsWithSingleError(t *testing.T) {
	svc := newStreamingResponseTestGatewayService()
	recorder, c := claudePreludeTestContext()
	start := claudePreludeStart("message", 5)
	result, err := runClaudePreludeTestStream(t, svc, c, start, true)
	require.ErrorContains(t, err, "missing terminal event")
	require.Equal(t, 5, result.usage.InputTokens)
	require.True(t, strings.HasPrefix(recorder.Body.String(), start))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
	require.True(t, IsResponseCommitted(c), "the handler must not append a second terminal after the prelude EOF error")
}

func TestClaudePrelude_DisabledRecoveryDoesNotHoldStart(t *testing.T) {
	svc := newStreamingResponseTestGatewayService()
	recorder, c := claudePreludeTestContext()
	start := claudePreludeStart("message", 5)
	_, err := runClaudePreludeTestStream(t, svc, c, start+claudePreludeFrame("error", claudePreludeSignatureError), false)
	var sseErr *sseStreamErrorEventError
	require.True(t, errors.As(err, &sseErr))
	require.True(t, sseErr.MessageStarted)
	require.Equal(t, start, recorder.Body.String())
}

func TestClaudePrelude_RepairTimeoutSkipsHealthPenaltyAndNormalTimeoutKeepsIt(t *testing.T) {
	for _, suppressPenalty := range []bool{false, true} {
		t.Run(fmt.Sprintf("suppress_%t", suppressPenalty), func(t *testing.T) {
			svc := newStreamingResponseTestGatewayService()
			svc.cfg.Gateway.StreamDataIntervalTimeout = 1
			repo := &gatewayForwardErrorPolicyRepoStub{}
			settings := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
				SettingKeyStreamTimeoutSettings: `{"enabled":true,"action":"temp_unsched","threshold_count":1,"threshold_window_minutes":1,"temp_unsched_minutes":1}`,
			}}, svc.cfg)
			svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
			svc.rateLimitService.SetSettingService(settings)
			recorder, c := claudePreludeTestContext()
			pr, pw := io.Pipe()
			defer pr.Close()
			defer pw.Close()
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

			_, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false, false, suppressPenalty)
			require.ErrorContains(t, err, "stream data interval timeout")
			if suppressPenalty {
				require.Zero(t, repo.tempCalls, "a failed signature repair must not park the account")
			} else {
				require.Equal(t, 1, repo.tempCalls, "normal streaming timeout keeps its existing health action")
			}
			require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
			require.Contains(t, recorder.Body.String(), `"type":"stream_timeout"`)
		})
	}
}

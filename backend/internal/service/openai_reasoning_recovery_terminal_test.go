//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type reasoningRecoverySeededNegativeCache struct {
	*openAIChatReplayTestCache
	entry OpenAIRejectedReasoning
	gets  int
	puts  int
}

func (s *reasoningRecoverySeededNegativeCache) GetOpenAIRejectedReasoning(_ context.Context, scope OpenAIReasoningCacheScope, hashes []string) (map[string]OpenAIRejectedReasoning, error) {
	s.gets++
	result := make(map[string]OpenAIRejectedReasoning)
	if scope.ScopeHash != "" {
		for _, hash := range hashes {
			if hash == openAIReasoningDigest([]byte("opaque-old")) {
				result[hash] = s.entry
			}
		}
	}
	return result, nil
}

func (s *reasoningRecoverySeededNegativeCache) PutOpenAIRejectedReasoning(context.Context, OpenAIReasoningCacheScope, []string) error {
	s.puts++
	return nil
}

func lastReasoningRecoveryDiagnostic(t *testing.T, c *gin.Context) *OpsUpstreamErrorEvent {
	t.Helper()
	value, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	var last *OpsUpstreamErrorEvent
	for _, event := range value.([]*OpsUpstreamErrorEvent) {
		if event.ContinuationDiagnostic != nil {
			last = event
		}
	}
	require.NotNil(t, last)
	return last
}

func TestOpenAIReasoningRecoveryTerminalsPreserveClassificationAndDiagnostic(t *testing.T) {
	const signature = `{"code":"thinking_signature_invalid","param":"input[1].encrypted_content","message":"private-signature-detail-8162"}`
	const wrongTarget = `{"code":"thinking_signature_invalid","param":"input[2].encrypted_content","message":"private-signature-detail-8162"}`
	const validation = `{"type":"invalid_request_error","code":"missing_required_parameter","param":"input[3].call_id","message":"private-validation-detail-7514"}`
	for _, passthrough := range []bool{false, true} {
		for _, transport := range []string{"http", "stream", "buffered_sse"} {
			for _, behavior := range []string{"recovery_then_validation", "recovery_then_signature", "unrecoverable_signature", "cache_skip_then_validation"} {
				name := fmt.Sprintf("passthrough_%t/%s/%s", passthrough, transport, behavior)
				t.Run(name, func(t *testing.T) {
					input := reasoningRecoveryFixture
					if transport == "stream" {
						input = strings.Replace(input, `"store":false`, `"store":false,"stream":true`, 1)
					}
					payload, classification := validation, "request_validation"
					if behavior == "unrecoverable_signature" {
						payload, classification = wrongTarget, "thinking_signature_invalid"
					} else if behavior == "recovery_then_signature" {
						payload, classification = signature, "thinking_signature_invalid"
					}
					status, contentType, terminal := http.StatusBadRequest, "application/json", `{"error":`+payload+`}`
					if transport != "http" {
						status, contentType = http.StatusOK, "text/event-stream"
						terminal = "data: " + `{"type":"response.failed","response":{"status":"failed","error":` + payload + `}}` + "\n\n"
					}
					responses := []*http.Response{{
						StatusCode: status,
						Header:     http.Header{"Content-Type": []string{contentType}, "X-Request-Id": []string{"request-final-safe-id"}},
						Body:       io.NopCloser(strings.NewReader(terminal)),
					}}
					retried := strings.HasPrefix(behavior, "recovery_then_")
					if retried {
						responses = append([]*http.Response{newJSONResponse(http.StatusBadRequest, `{"error":`+signature+`}`)}, responses...)
					}
					upstream := &httpUpstreamRecorder{responses: responses}
					svc := newOpenAIImageGenerationControlTestService(upstream)
					repo := &openAIPassthroughFailoverRepo{}
					svc.rateLimitService = &RateLimitService{accountRepo: repo, cfg: svc.cfg}
					c, _ := newOpenAIImageGenerationControlTestContext(false, "test-client")
					getAPIKeyFromContext(c).UserID = 77
					account := newOpenAIImageGenerationControlTestAccount()
					account.Extra = map[string]any{"openai_passthrough": passthrough, "responses_api_supported": true, "pool_mode_enabled": true}
					var negative *reasoningRecoverySeededNegativeCache
					if behavior == "cache_skip_then_validation" {
						negative = &reasoningRecoverySeededNegativeCache{
							openAIChatReplayTestCache: newOpenAIChatReplayTestCache(),
							entry:                     OpenAIRejectedReasoning{RejectedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)},
						}
						negative.GatewayCache = &stubGatewayCache{}
						svc.cache = negative
					}
					result, err := svc.Forward(context.Background(), c, account, []byte(input))
					require.Error(t, err)
					require.Nil(t, result)
					var failure *UpstreamFailoverError
					if retried {
						var stopped *OpenAIReasoningRecoveryTerminalError
						require.ErrorAs(t, err, &stopped)
						require.False(t, errors.As(err, &failure), "spent recovery must not reenter failover")
						failure = stopped.Failure
						require.Len(t, upstream.requests, 2, "no third POST")
					} else {
						require.ErrorAs(t, err, &failure)
						require.Len(t, upstream.requests, 1)
					}
					require.Equal(t, status, failure.StatusCode, "retain actual HTTP status separately from client semantics")
					require.Equal(t, http.StatusBadRequest, failure.ClientStatusCode)
					require.Equal(t, classification == "request_validation", failure.IsOpenAIRequestRejected())
					require.Equal(t, classification != "request_validation", failure.IsOpenAIContinuationStateUnavailable())
					require.False(t, failure.ShouldRetryNextAccount())
					require.False(t, failure.RetryableOnSameAccount)
					require.True(t, failure.SuppressAccountHealthPenalty)
					require.False(t, failure.ShouldReportAccountScheduleFailure())
					require.Empty(t, repo.rateLimitCalls)
					require.Empty(t, repo.overloadCalls)
					for _, req := range upstream.requests {
						require.Nil(t, req.GetBody)
					}
					for _, path := range []string{"reasoning", "input.0", "input.1.id", "input.1.phase", "input.1.summary", "input.1.unknown", "input.2", "input.3"} {
						require.JSONEq(t, gjson.Get(input, path).Raw, gjson.GetBytes(upstream.lastBody, path).Raw, path)
					}
					event := lastReasoningRecoveryDiagnostic(t, c)
					diagnostic := event.ContinuationDiagnostic
					require.Equal(t, classification, diagnostic.Classification)
					require.Equal(t, status, event.UpstreamStatusCode)
					require.Equal(t, "request-final-safe-id", event.UpstreamRequestID)
					require.Equal(t, "frozen_request", diagnostic.Wire.BodySource)
					require.False(t, diagnostic.Wire.InspectionLimited)
					require.Equal(t, len(upstream.lastBody), diagnostic.Wire.BodyBytes)
					require.Equal(t, 1, diagnostic.Incoming.History.Encrypted)
					require.NotNil(t, diagnostic.Recovery)
					require.Equal(t, retried, diagnostic.Recovery.RetryAttempted)
					if retried {
						require.Equal(t, "budget_exhausted", diagnostic.Recovery.Disposition)
						require.Zero(t, diagnostic.Wire.History.Encrypted)
					} else {
						require.Equal(t, "not_attempted", diagnostic.Recovery.Disposition)
						if negative != nil {
							require.Equal(t, 1, diagnostic.Recovery.CacheSkippedItems)
							require.Equal(t, "not_signature_rejection", diagnostic.Recovery.NotAttemptedReason)
							require.Zero(t, diagnostic.Wire.History.Encrypted)
							require.Equal(t, 1, negative.gets)
							require.Zero(t, negative.puts, "a negative cache hit must not refresh rejection memory")
						} else {
							require.Equal(t, "target_not_reasoning_ciphertext", diagnostic.Recovery.NotAttemptedReason)
							require.Equal(t, 1, diagnostic.Wire.History.Encrypted)
						}
					}
					encoded, encodeErr := json.Marshal(diagnostic)
					require.NoError(t, encodeErr)
					for _, secret := range []string{"opaque-old", "visible summary", "private-signature-detail-8162", "private-validation-detail-7514", "source-token"} {
						require.NotContains(t, string(encoded), secret)
						require.NotContains(t, err.Error(), secret)
					}
				})
			}
		}
	}
}

func TestOpenAIReasoningRecoveryFrozenDiagnosticDoesNotReadGetBody(t *testing.T) {
	state, req, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	state.BindDiagnosticRequest([]byte(reasoningRecoveryFixture), req)
	require.Nil(t, req.GetBody)
	// An unrelated caller's prepared body cannot replace the frozen wire proof.
	diagnostic := buildOpenAIContinuationDiagnostic(state.c, []byte(reasoningRecoveryFixture), req, []byte(`{"input":[]}`), []byte(`{"error":{"code":"thinking_signature_invalid"}}`), "thinking_signature_invalid")
	require.Equal(t, "frozen_request", diagnostic.Wire.BodySource)
	require.False(t, diagnostic.Wire.InspectionLimited)
	require.Equal(t, len(reasoningRecoveryFixture), diagnostic.Wire.BodyBytes)
	require.Equal(t, 1, diagnostic.Wire.History.Encrypted)
	require.Equal(t, 1, diagnostic.Wire.History.Calls)
	require.Equal(t, 1, diagnostic.Wire.History.Outputs)
	require.Nil(t, req.GetBody, "diagnostics must not re-enable automatic POST replay")
	stripped, retry := state.TryRecover(http.StatusBadRequest, nil, []byte(`{"error":{"code":"thinking_signature_invalid"}}`), false)
	require.True(t, retry)
	changedSource := req.Clone(context.Background())
	changedSource.Header.Set("Authorization", "Bearer different-source")
	_, _, err := state.PrepareRequest(changedSource, stripped, "")
	var stopped *OpenAIReasoningRecoveryTerminalError
	require.ErrorAs(t, err, &stopped)
	blocked := lastReasoningRecoveryDiagnostic(t, state.c).ContinuationDiagnostic
	require.False(t, blocked.Recovery.RetryAttempted, "a prepared but blocked retry was never dispatched")
	require.Equal(t, "source_changed", blocked.Recovery.NotAttemptedReason)
	require.Equal(t, 1, blocked.Wire.History.Encrypted, "the last sent snapshot is still the original attempt")
}

func TestOpenAIReasoningRecoveryNotAttemptedDiagnosticReasons(t *testing.T) {
	for _, reason := range []string{"disabled", "request_cancelled", "semantic_output_committed", "server_held_context", "no_reasoning_ciphertext", "invalid_tool_history"} {
		t.Run(reason, func(t *testing.T) {
			body := reasoningRecoveryFixture
			switch reason {
			case "server_held_context":
				body = strings.Replace(body, `"store":false`, `"store":false,"previous_response_id":"private-upstream-reference"`, 1)
			case "no_reasoning_ciphertext":
				body = strings.Replace(body, `"encrypted_content":"opaque-old",`, "", 1)
			case "invalid_tool_history":
				body = strings.Replace(body, `"type":"function_call_output","call_id":"call_one"`, `"type":"function_call_output","call_id":"missing"`, 1)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			state, req, _ := newReasoningRecoveryTestState(t, ctx, body)
			state.BindDiagnosticRequest([]byte(body), req)
			state.ObserveResponse(&http.Response{StatusCode: http.StatusBadRequest})
			if reason == "disabled" {
				state.enabled = false
			} else if reason == "request_cancelled" {
				cancel()
			}
			payload := []byte(`{"error":{"code":"thinking_signature_invalid","message":"private-rejection-detail"}}`)
			_, retry := state.TryRecover(http.StatusBadRequest, nil, payload, reason == "semantic_output_committed")
			require.False(t, retry)
			err := state.StopError(NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, payload))
			require.Error(t, err)
			diagnostic := lastReasoningRecoveryDiagnostic(t, state.c).ContinuationDiagnostic
			require.Equal(t, "thinking_signature_invalid", diagnostic.Classification)
			require.Equal(t, "not_attempted", diagnostic.Recovery.Disposition)
			require.Equal(t, reason, diagnostic.Recovery.NotAttemptedReason)
			require.False(t, diagnostic.Recovery.RetryAttempted)
			require.Nil(t, req.GetBody)
		})
	}
}

func TestOpenAIContinuationDiagnosticRecoveryEnumSanitization(t *testing.T) {
	diagnostic := &OpenAIContinuationDiagnostic{Recovery: &openAIContinuationRecoveryShape{
		CacheSkippedItems: -1, Disposition: "private-disposition-52913", NotAttemptedReason: "private-reason-27184",
	}}
	safe := sanitizeOpenAIContinuationDiagnostic(diagnostic)
	require.NotNil(t, safe)
	require.NotNil(t, safe.Recovery)
	require.Zero(t, safe.Recovery.CacheSkippedItems)
	require.Equal(t, "unknown", safe.Recovery.Disposition)
	require.Empty(t, safe.Recovery.NotAttemptedReason)
	encoded, err := json.Marshal(safe)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
}

type reasoningRecoveryTerminalWriteFailure struct{ gin.ResponseWriter }

func (w reasoningRecoveryTerminalWriteFailure) Write(body []byte) (int, error) {
	if strings.Contains(string(body), "response.failed") {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(body)
}

func TestOpenAIReasoningRecoveryTerminalDeliveryDistinguishesInterruptedStream(t *testing.T) {
	const rejection = `{"error":{"code":"invalid_encrypted_content","param":"input[1].encrypted_content"}}`
	const delta = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible answer\"}\n\n"
	const failed = "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}}}\n\n"
	for _, passthrough := range []bool{false, true} {
		for _, ending := range []string{"failed_terminal", "interrupted_after_delta", "terminal_write_failed"} {
			t.Run(fmt.Sprintf("passthrough_%t/%s", passthrough, ending), func(t *testing.T) {
				responseBody := delta
				if ending != "interrupted_after_delta" {
					responseBody += failed
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					newJSONResponse(http.StatusBadRequest, rejection),
					{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(responseBody))},
				}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				c, recorder := newOpenAIImageGenerationControlTestContext(false, "test-client")
				if ending == "terminal_write_failed" {
					c.Writer = reasoningRecoveryTerminalWriteFailure{c.Writer}
				}
				account := newOpenAIImageGenerationControlTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough, "responses_api_supported": true}
				input := strings.Replace(reasoningRecoveryFixture, `"store":false`, `"store":false,"stream":true`, 1)
				_, err := svc.Forward(context.Background(), c, account, []byte(input))
				var terminal *OpenAIReasoningRecoveryTerminalError
				require.ErrorAs(t, err, &terminal)
				require.Len(t, upstream.requests, 2)
				require.Equal(t, ending == "failed_terminal", terminal.FailureTerminalForwarded)
				require.Contains(t, recorder.Body.String(), "visible answer")
				if ending == "interrupted_after_delta" {
					require.NotContains(t, recorder.Body.String(), "response.failed", "the handler must still synthesize the missing terminal")
				} else if ending == "failed_terminal" {
					require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"response.failed"`))
				}
				var retryable *UpstreamFailoverError
				require.False(t, errors.As(err, &retryable))
			})
		}
	}
}

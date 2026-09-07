package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAIHTTPTerminalCompletedFixture = `{"type":"response.completed","response":{"id":"resp_terminal","status":"completed","model":"gpt-6-astra","reasoning":{"effort":"max","mode":"standard","context":"all_turns","extension":{"integer":9007199254740993}},"output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":9,"output_tokens":3,"input_tokens_details":{"cached_tokens":4}}}}`

func runOpenAIHTTPTerminalHandler(mode string, c *gin.Context, reader io.ReadCloser, contentType string, accountOverride ...*Account) (*OpenAIUsage, error) {
	svc := &OpenAIGatewayService{
		cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector(),
	}
	if mode == "native_async" {
		svc.cfg.Gateway.StreamDataIntervalTimeout = 30
	}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	if len(accountOverride) > 0 {
		account = accountOverride[0]
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: reader}
	switch mode {
	case "native", "native_async":
		result, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
		if result == nil {
			return nil, err
		}
		return result.usage, err
	case "passthrough":
		result, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
		if result == nil {
			return nil, err
		}
		return result.usage, err
	case "buffered":
		result, err := svc.handleNonStreamingResponse(c.Request.Context(), resp, c, account, "gpt-6-astra", "gpt-6-astra")
		if result == nil {
			return nil, err
		}
		return result.usage, err
	default:
		result, err := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, account, "gpt-6-astra", "gpt-6-astra")
		if result == nil {
			return nil, err
		}
		return result.usage, err
	}
}

func TestOpenAIHTTPTerminalDoesNotWaitForEOF(t *testing.T) {
	for _, mode := range []string{"native", "native_async", "passthrough", "buffered", "passthrough_buffered"} {
		for _, status := range []string{"completed", "incomplete"} {
			t.Run(mode+"/"+status, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				terminal := strings.ReplaceAll(openAIHTTPTerminalCompletedFixture, "completed", status)
				reader := newOpenAICompatBlockingReadCloser([]byte("event: response." + status + "\ndata: " + terminal + "\n\n"))
				defer reader.Close()
				type outcome struct {
					usage *OpenAIUsage
					err   error
				}
				done := make(chan outcome, 1)
				go func() {
					usage, err := runOpenAIHTTPTerminalHandler(mode, c, reader, "text/event-stream")
					done <- outcome{usage, err}
				}()
				var got outcome
				select {
				case got = <-done:
				case <-time.After(3 * time.Second):
					_ = reader.Close()
					<-done
					t.Fatal("complete HTTP terminal waited for upstream EOF")
				}
				if status == "completed" {
					require.NoError(t, got.err)
				} else {
					require.ErrorContains(t, got.err, "incomplete")
				}
				require.NotNil(t, got.usage)
				require.Equal(t, 9, got.usage.InputTokens)
				require.Equal(t, 3, got.usage.OutputTokens)
				require.Equal(t, 4, got.usage.CacheReadInputTokens)
				select {
				case <-reader.closed:
				default:
					t.Fatal("handler did not close its HTTP response body")
				}
				require.Contains(t, rec.Body.String(), `"mode":"standard"`)
				require.Contains(t, rec.Body.String(), `9007199254740993`)
				if strings.Contains(mode, "buffered") {
					require.Equal(t, status, gjson.Get(rec.Body.String(), "status").String())
				} else {
					require.True(t, strings.HasSuffix(rec.Body.String(), "\n\n"))
				}
			})
		}
	}
}

func TestOpenAIHTTPTerminalNamedAndConcatenatedFrames(t *testing.T) {
	for _, mode := range []string{"native", "passthrough", "buffered", "passthrough_buffered"} {
		for _, named := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "concatenated", true: "event_only"}[named], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				payload := `{"type":"response.in_progress","response":{"usage":{"input_tokens":100}}}` + openAIHTTPTerminalCompletedFixture
				if named {
					payload = strings.Replace(openAIHTTPTerminalCompletedFixture, `"type":"response.completed",`, "", 1)
				}
				body := "event: response.completed\ndata: " + payload + "\n\n"
				usage, err := runOpenAIHTTPTerminalHandler(mode, c, io.NopCloser(strings.NewReader(body)), "")
				require.NoError(t, err)
				require.Equal(t, 9, usage.InputTokens, "authoritative usage supersedes progress snapshots")
				require.Contains(t, rec.Body.String(), `resp_terminal`)
			})
		}
	}

	payload := []byte(`{"type":"error","error":{"code":"invalid_request_error"}}`)
	require.False(t, openAIHTTPAuthoritativeTerminalEvent(payload, "response.completed"))
	typ, _, ok := extractOpenAISSETerminalEvent("event: response.completed\ndata: " + string(payload) + "\n\n")
	require.True(t, ok)
	require.Equal(t, "error", typ, "payload type must not be overwritten by an inherited event name")
}

func TestOpenAIHTTPTerminalFailedKeepsAuthoritativeUsage(t *testing.T) {
	for _, mode := range []string{"native", "native_async", "passthrough"} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
				"data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request_error\",\"message\":\"rejected\"}}\n\n" +
				"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"status\":\"failed\",\"error\":{\"code\":\"invalid_request_error\",\"message\":\"rejected\"},\"usage\":{\"input_tokens\":9,\"output_tokens\":3}}}\n\n"
			reader := newOpenAICompatBlockingReadCloser([]byte(body))
			defer reader.Close()
			done := make(chan error, 1)
			var usage *OpenAIUsage
			go func() {
				var err error
				usage, err = runOpenAIHTTPTerminalHandler(mode, c, reader, "text/event-stream")
				done <- err
			}()
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(3 * time.Second):
				_ = reader.Close()
				<-done
				t.Fatal("failed terminal waited for EOF")
			}
			require.Equal(t, 9, usage.InputTokens)
			require.Equal(t, 3, usage.OutputTokens)
			require.Contains(t, rec.Body.String(), "response.failed")
			require.NotContains(t, rec.Body.String(), "response.completed")
		})
	}
}

func TestOpenAIHTTPTerminalMissingOrMalformedNeverSucceeds(t *testing.T) {
	for _, mode := range []string{"native", "passthrough", "buffered", "passthrough_buffered"} {
		for _, body := range []string{
			"data: [DONE]\n\n",
			"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"x\\\":\"}\n\n" + "data: [DONE]\n\n",
			"data: {\"type\":\"response.completed\"} trailing\n\n",
		} {
			t.Run(mode, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				_, err := runOpenAIHTTPTerminalHandler(mode, c, io.NopCloser(strings.NewReader(body)), "text/event-stream")
				require.Error(t, err)
			})
		}
	}
}

func TestOpenAIHTTPTerminalRecoverySignalBeforeOutputOnly(t *testing.T) {
	for _, mode := range []string{"native", "passthrough", "buffered", "passthrough_buffered"} {
		for _, prefix := range []string{"", "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"visible reasoning\"}\n\n"} {
			t.Run(mode+map[bool]string{true: "/preoutput", false: "/postoutput"}[prefix == ""], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				state := &openAIReasoningRecoveryState{ctx: context.Background(), enabled: true, wire: []byte(`{"input":[{"type":"reasoning","summary":[],"encrypted_content":"cipher_fixture"}]}`)}
				c.Set(openAIReasoningRecoveryContextKey, state)
				bare := `{"type":"error","error":{"code":"invalid_encrypted_content","param":"input[0].encrypted_content"}}`
				failed := `{"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_encrypted_content","param":"input[0].encrypted_content"},"usage":{"input_tokens":9,"output_tokens":3}}}`
				body := prefix + "data: " + bare + "\n\ndata: " + failed + "\n\n"
				usage, err := runOpenAIHTTPTerminalHandler(mode, c, io.NopCloser(strings.NewReader(body)), "text/event-stream")
				require.Error(t, err)
				var signal *openAIReasoningRecoverySignalError
				wantSignal := prefix == "" || strings.Contains(mode, "buffered")
				require.Equal(t, wantSignal, errors.As(err, &signal))
				if wantSignal {
					require.Equal(t, "response.failed", gjson.GetBytes(signal.payload, "type").String())
					require.Equal(t, int64(9), gjson.GetBytes(signal.payload, "response.usage.input_tokens").Int())
					require.Empty(t, rec.Body.String())
				} else {
					require.Equal(t, 9, usage.InputTokens)
					require.Contains(t, rec.Body.String(), "visible reasoning")
				}
				require.False(t, state.retryUsed, "parsing a candidate must not consume the retry")
			})
		}
	}
}

func TestOpenAIHTTPTerminalBufferedBodyLimitAndCancellation(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{UpstreamResponseReadMaxBytes: 32}}}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 33)))}
	_, err := svc.readOpenAIResponsesHTTPBody(context.Background(), resp, nil)
	require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
	resp.Body = &openAIResponseFlushReadError{err: context.Canceled, sent: true}
	_, err = svc.readOpenAIResponsesHTTPBody(context.Background(), resp, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestOpenAIHTTPTerminalPreambleAndKeepaliveDoNotCommitRecovery(t *testing.T) {
	for _, mode := range []string{"native", "native_async", "passthrough"} {
		for _, platform := range []string{PlatformOpenAI, PlatformCindy} {
			t.Run(mode+"/"+platform, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				// Only the neutral comment has actually been sent. Old response IDs
				// and attempt-specific headers must remain private when recovering.
				n, err := c.Writer.Write([]byte(":\n\n"))
				require.NoError(t, err)
				recordOpenAIStreamKeepaliveBytes(c, n)
				state := &openAIReasoningRecoveryState{ctx: context.Background(), enabled: true, wire: []byte(`{"input":[{"type":"reasoning","summary":[],"encrypted_content":"cipher_fixture"}]}`)}
				c.Set(openAIReasoningRecoveryContextKey, state)
				body := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"old_attempt\"}}\n\n" +
					"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"thinking_signature_invalid\",\"param\":\"input[0].encrypted_content\"}}}\n\n"
				_, err = runOpenAIHTTPTerminalHandler(mode, c, io.NopCloser(strings.NewReader(body)), "text/event-stream", &Account{ID: 1, Platform: platform, Type: AccountTypeAPIKey})
				var signal *openAIReasoningRecoverySignalError
				require.ErrorAs(t, err, &signal)
				require.Equal(t, ":\n\n", rec.Body.String())
				require.False(t, state.retryUsed)
			})
		}
	}
}

func TestOpenAIHTTPTerminalRecoveryDisabledOrCanceledDoesNotRotate(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if !disabled {
			cancel()
		}
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Set(openAIReasoningRecoveryContextKey, &openAIReasoningRecoveryState{ctx: ctx, enabled: !disabled, wire: []byte(`{"input":[{"type":"reasoning","summary":[],"encrypted_content":"cipher_fixture"}]}`)})
		payload := []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"thinking_signature_invalid","param":"input[0].encrypted_content"}}}`)
		err := openAIHTTPReasoningRejectionBeforeOutput(c, payload, false)
		var signal *openAIReasoningRecoverySignalError
		require.False(t, errors.As(err, &signal))
		var terminal *UpstreamFailoverError
		require.ErrorAs(t, err, &terminal)
		require.False(t, terminal.ShouldRetryNextAccount())
		require.False(t, terminal.ShouldReportAccountScheduleFailure())
		require.Empty(t, terminal.ResponseBody)
	}
}

func TestOpenAIHTTPTerminalReadErrorCannotDispatchUnfinishedFrame(t *testing.T) {
	for _, mode := range []string{"native", "native_async", "passthrough", "buffered", "passthrough_buffered"} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			reader := &openAIResponseFlushReadError{payload: []byte("data: " + openAIHTTPTerminalCompletedFixture + "\n"), err: io.ErrUnexpectedEOF}
			_, err := runOpenAIHTTPTerminalHandler(mode, c, reader, "text/event-stream")
			require.Error(t, err)
		})
	}
}

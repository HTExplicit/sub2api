//go:build unit

package service

import (
	"context"
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

func assertReasoningRecoveryUsageEvidence(t *testing.T, c *gin.Context, input int64, output *int64) {
	t.Helper()
	value, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	found := false
	for _, event := range value.([]*OpsUpstreamErrorEvent) {
		if event.Kind != "reasoning_recovery" || event.Message != "retry_without_encrypted_content" {
			continue
		}
		found = true
		detail := gjson.Parse(event.Detail)
		require.Equal(t, "available", detail.Get("usage_status").String())
		require.Equal(t, input, detail.Get("usage.input_tokens").Int())
		if output == nil {
			require.False(t, detail.Get("usage.output_tokens").Exists(), "missing output usage must remain unavailable, not zero")
		} else {
			require.Equal(t, *output, detail.Get("usage.output_tokens").Int())
		}
	}
	require.True(t, found)
}

func TestOpenAIReasoningRecoveryLegacyDoneAndEarlierUsage(t *testing.T) {
	for _, mode := range []string{"native", "native_async", "passthrough", "buffered", "passthrough_buffered"} {
		for _, terminal := range []string{"response.failed", "response.done"} {
			t.Run(mode+"/"+terminal, func(t *testing.T) {
				state, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
				progress := `{"type":"response.in_progress","response":{"usage":{"input_tokens":3}}}`
				failed := fmt.Sprintf(`{"type":%q,"response":{"status":"failed","error":{"code":"thinking_signature_invalid","param":"input[1].encrypted_content"}}}`, terminal)
				_, err := runOpenAIHTTPTerminalHandler(mode, state.c, io.NopCloser(strings.NewReader("data: "+progress+"\n\ndata: "+failed+"\n\n")), "text/event-stream")
				var signal *openAIReasoningRecoverySignalError
				require.ErrorAs(t, err, &signal)
				_, recovered := state.TryRecoverError(err)
				require.True(t, recovered)
				assertReasoningRecoveryUsageEvidence(t, state.c, 3, nil)
			})
		}
	}
}

func TestOpenAIChatReasoningReplayWaitsForAuthoritativeRejection(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, terminal := range []string{"response.failed", "response.done", "response.completed", "eof"} {
			t.Run(fmt.Sprintf("stream_%t/%s", stream, terminal), func(t *testing.T) {
				cache := newOpenAIChatReplayTestCache()
				account := newOpenAIRejectedFieldTestAccount()
				progress := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_rejected\"}}\n\n" +
					"data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":3}}}\n\n"
				bare := "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}}\n\n"
				completed := "data: " + string(openAIChatReplayTestPayload(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]`)) + "\n\n"
				firstFailure := progress + bare
				switch terminal {
				case "response.completed":
					firstFailure += completed
				case "response.failed", "response.done":
					firstFailure += fmt.Sprintf("data: {\"type\":%q,\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}}}\n\n", terminal)
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					openAIChatReplayTestResponse(openAIChatReplayTestSSE(openAIChatReplayTestOutput, stream), http.StatusOK),
					openAIChatReplayTestResponse(firstFailure, http.StatusOK),
					openAIChatReplayTestResponse(completed, http.StatusOK),
				}}
				svc := newOpenAIRejectedFieldTestService(upstream)
				svc.cache = cache
				first := openAIChatReplayTestBody(t, false, stream, false)
				c, _ := openAIChatReplayTestContext(t, first)
				_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, first, "", "")
				require.NoError(t, err)
				second := openAIChatReplayTestBody(t, true, stream, false)
				c, writer := openAIChatReplayTestContext(t, second)
				result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, second, "", "")
				require.NoError(t, err)
				require.Equal(t, 4, result.Usage.InputTokens)
				require.Equal(t, 6, result.Usage.OutputTokens)
				if terminal == "response.completed" {
					require.Len(t, upstream.requests, 2)
					require.Empty(t, cache.rejected)
				} else {
					require.NotContains(t, writer.Body.String(), "resp_rejected")
					require.Len(t, upstream.requests, 3)
					require.Len(t, cache.rejected, 1)
					assertReasoningRecoveryUsageEvidence(t, c, 3, nil)
				}
			})
		}
	}
}

type reasoningPartialWriter struct{ gin.ResponseWriter }

func (w reasoningPartialWriter) Write(p []byte) (int, error) {
	n, _ := w.ResponseWriter.Write(p[:min(len(p), 4)])
	return n, io.ErrClosedPipe
}

func TestOpenAIChatReasoningReplayPartialWriteCannotRecover(t *testing.T) {
	state, _, writer := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	state.c.Writer = reasoningPartialWriter{state.c.Writer}
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"visible\"}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}}}\n\n"
	svc := newOpenAIRejectedFieldTestService(&httpUpstreamRecorder{})
	_, err := svc.handleChatStreamingResponse(openAIChatReplayTestResponse(body, 200), state.c, state.account, "gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol", time.Now(), 32)
	require.Error(t, err)
	require.NotEmpty(t, writer.Body.String())
	var signal *openAIReasoningRecoverySignalError
	require.False(t, errors.As(err, &signal))
	_, recovered := state.TryRecoverError(err)
	require.False(t, recovered)
}

//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentRawChatRequiresSemanticTermination(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		wrote        bool
	}{
		{"sentinel_only", "data: [DONE]\n\n", false},
		{"usage_only", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n", false},
		{"error_frame", "data: {\"error\":{\"type\":\"server_error\",\"message\":\"fixture\"}}\n\ndata: [DONE]\n\n", false},
		{"malformed_frame", "data: {broken\n\ndata: [DONE]\n\n", false},
		{"truncated_with_sentinel", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\ndata: [DONE]\n\n", true},
		{"unclosed_tool_arguments", "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_fixture\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\"}}]}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			body := []byte(`{"model":"fixture","messages":[{"role":"user","content":"fixture"}],"stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.stream))}}
			s := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			_, err := s.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
			require.Error(t, err)
			require.NotContains(t, rec.Body.String(), "data: [DONE]")
			var failover *UpstreamFailoverError
			require.Equal(t, !tc.wrote, errors.As(err, &failover))
			if !tc.wrote {
				require.Empty(t, rec.Body.String())
			}
		})
	}
}

func TestAgentRawUpstreamTimeoutIsNotClientCancellation(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"fixture","messages":[{"role":"user","content":"fixture"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &openAIChatStreamReadErrorCloser{
		payload: []byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"),
		err:     context.DeadlineExceeded,
	}}}
	s := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	result, err := s.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
	require.Error(t, err)
	require.NotNil(t, result)
	require.False(t, result.ClientDisconnect)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
}

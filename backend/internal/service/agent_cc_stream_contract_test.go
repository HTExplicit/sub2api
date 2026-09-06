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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentCCStreamRequiresSemanticTermination(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"normal", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", true},
		{"finish_without_sentinel", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n", true},
		{"eof_before_finish", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n", false},
		{"sentinel_without_finish", "data: [DONE]\n\n", false},
		{"error_frame", "data: {\"error\":{\"type\":\"server_error\",\"message\":\"fixture failure\"}}\n\ndata: [DONE]\n\n", false},
		{"malformed_frame", "data: {broken\n\ndata: [DONE]\n\n", false},
		{"html", "<!doctype html><title>not an API</title>", false},
		{"usage_only", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":0}}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}
			s := &OpenAIGatewayService{}
			state := s.scanCCStream(c, resp, "fixture", "fixture", time.Now(), func(*apicompat.ChatCompletionsChunk) {})
			if tc.valid {
				require.NoError(t, state.Err)
			} else {
				require.Error(t, state.Err)
			}
		})
	}
}

func TestAgentResponsesBridgeDoesNotCompleteBrokenStreams(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wrote      bool
	}{
		{"before_output", "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n", false},
		{"after_output", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			body := []byte(`{"model":"fixture","input":"fixture","stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.body))}}
			s := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			_, err := s.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
			require.Error(t, err)
			require.NotContains(t, rec.Body.String(), "event: response.completed")
			var failover *UpstreamFailoverError
			if tc.wrote {
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
				require.False(t, errors.As(err, &failover), "a delivered terminal must not remain replayable")
			} else {
				require.Empty(t, rec.Body.String(), "metadata-only prefixes must remain buffered")
				require.True(t, errors.As(err, &failover))
			}
		})
	}
}

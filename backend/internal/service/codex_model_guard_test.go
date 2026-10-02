package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// A guarded response only completes with a delimited terminal event that
// declares the requested model.
func TestCodexModelGuardRequiresOriginalModelAndDelimitedTerminal(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	read := func(contentType, body string) codexResponseCompletion {
		header := http.Header{}
		if contentType != "" {
			header.Set("Content-Type", contentType)
		}
		response := &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}
		guard := svc.newCodexModelGuardBody(httptest.NewRequest(http.MethodPost, "/", nil), response, "gpt-6-astra")
		_, _ = io.ReadAll(guard)
		require.NoError(t, guard.Close())
		return guard.completion
	}
	declared := func(completion codexResponseCompletion) bool {
		return completion.Completed && !completion.Failed && !completion.Mismatch && completion.Model == "gpt-6-astra"
	}
	for _, test := range []struct {
		name, body string
		ok         bool
	}{
		{"complete", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}\n\n", true},
		{"model_mismatch", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-luna\"}}\n\n", false},
		{"missing_model", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", false},
		{"truncated", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}", false},
		{"done_only", "data: [DONE]\n\n", false},
		{"error", "data: {\"type\":\"error\",\"error\":{\"code\":\"server_is_overloaded\"}}\n\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.ok, declared(read("text/event-stream", test.body)))
		})
	}
	stream := `event: response.completed
data: {"type":"response.completed","response":{"status":"completed","model":"gpt-6-astra"}}

`
	require.True(t, declared(read("", stream)), "ChatGPT streams arrive without Content-Type")
}

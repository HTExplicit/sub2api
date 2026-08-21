//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newCindyBalanceProbeResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCindyBalanceProbeModelRecognizesExactExhaustion(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		status      int
		body        string
	}{
		{name: "http 429", contentType: "application/json", status: http.StatusTooManyRequests, body: exactCindyBudgetExceededBody},
		{name: "response failed SSE", contentType: "text/event-stream", status: http.StatusOK, body: "data: " + `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}` + "\n\n"},
		{name: "error SSE", contentType: "text/event-stream", status: http.StatusOK, body: "data: " + `{"type":"error","error":{"type":"budget_exceeded","code":"429"}}` + "\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: newCindyBalanceProbeResponse(tc.status, tc.contentType, tc.body)}
			gateway := &OpenAIGatewayService{httpUpstream: upstream}

			require.Equal(t, cindyBalanceProbeExhausted, gateway.probeCindyBalanceModel(
				context.Background(), newFirstClassCindyRateLimitAccount(8551, true), cindyBalanceProbeModels[0],
			))
			require.Len(t, upstream.bodies, 1)
		})
	}
}

func TestExactCindyBudgetSignalDelegatesToHealthCoordinator(t *testing.T) {
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimit := NewRateLimitService(repo, nil, nil, nil, nil)
	upstream := &httpUpstreamRecorder{resp: newCindyBalanceProbeResponse(http.StatusOK, "application/json", `{}`)}
	gateway := &OpenAIGatewayService{rateLimitService: rateLimit, httpUpstream: upstream}
	health := &cindyHealthCoordinatorRecorder{}
	gateway.SetCindyHealthCoordinator(health)
	rateLimit.SetAccountRuntimeBlocker(gateway)
	account := newFirstClassCindyRateLimitAccount(8556, true)

	require.True(t, gateway.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusTooManyRequests, http.Header{},
		[]byte(exactCindyBudgetExceededBody), "openai/gpt-5.6-sol",
	))
	require.Zero(t, repo.markCalls)
	require.Nil(t, account.CindyBalanceInsufficientAt)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	require.Empty(t, upstream.bodies, "the gateway must delegate instead of probing inline")
	require.Equal(t, []CindyHealthSignal{CindyHealthSignalExactBudget}, health.signals)
	require.Equal(t, []int64{account.ID}, health.accounts)
}

func TestCindyBalanceProbeModelRejectsTerminalShapesOutsideHTTP200(t *testing.T) {
	body := "data: " + `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}` + "\n\n"
	gateway := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{resp: newCindyBalanceProbeResponse(
		http.StatusCreated, "text/event-stream", body,
	)}}

	require.Equal(t, cindyBalanceProbeOther, gateway.probeCindyBalanceModel(
		context.Background(), newFirstClassCindyRateLimitAccount(8554, true), cindyBalanceProbeModels[0],
	))
}

func TestCindyBalanceProbeModelRequiresProtocolValidCompletedResponse(t *testing.T) {
	validResponse := `{"id":"resp_probe","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}`
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		want        cindyBalanceProbeOutcome
	}{
		{name: "valid JSON", contentType: "application/json", body: validResponse, want: cindyBalanceProbeSuccess},
		{name: "valid SSE", contentType: "text/event-stream", body: "data: " + `{"type":"response.completed","response":` + validResponse + `}` + "\n\n", want: cindyBalanceProbeSuccess},
		{name: "empty", contentType: "application/json", want: cindyBalanceProbeOther},
		{name: "HTML", contentType: "text/html", body: "<html>ok</html>", want: cindyBalanceProbeOther},
		{name: "empty object", contentType: "application/json", body: `{}`, want: cindyBalanceProbeOther},
		{name: "completed without usage", contentType: "application/json", body: `{"id":"resp_probe","object":"response","status":"completed","output":[{"type":"message"}]}`, want: cindyBalanceProbeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{resp: newCindyBalanceProbeResponse(
				http.StatusOK, tc.contentType, tc.body,
			)}}
			require.Equal(t, tc.want, gateway.probeCindyBalanceModel(
				context.Background(), newFirstClassCindyRateLimitAccount(8552, true), cindyBalanceProbeModels[0],
			))
		})
	}
}

func TestCindyBalanceProbeModelRejectsConflictingOrDuplicateSSETerminals(t *testing.T) {
	completed := `{"type":"response.completed","response":{"id":"resp_probe","object":"response","status":"completed","output":[{"type":"message"}],"usage":{"input_tokens":1,"output_tokens":1}}}`
	exhausted := `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`
	for _, events := range [][]string{{exhausted, completed}, {completed, exhausted}, {exhausted, exhausted}} {
		var body strings.Builder
		for _, event := range events {
			body.WriteString("data: ")
			body.WriteString(event)
			body.WriteString("\n\n")
		}
		gateway := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{resp: newCindyBalanceProbeResponse(
			http.StatusOK, "text/event-stream", body.String(),
		)}}
		require.Equal(t, cindyBalanceProbeOther, gateway.probeCindyBalanceModel(
			context.Background(), newFirstClassCindyRateLimitAccount(8557, true), cindyBalanceProbeModels[0],
		))
	}
}

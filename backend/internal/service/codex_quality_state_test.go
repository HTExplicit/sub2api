package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexQualityOAuthStateOwnsSubjectSessionAndTurn(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := codexQualityAccount()
	c, _ := newTurnStateTestContext(t, 11, "fixture-session")
	stageCodexRoutingTurn(c, []byte(`{"client_metadata":{"turn_id":"one"}}`))
	ctx := context.Background()
	svc.bindOpenAICompatSessionTurnState(ctx, c, account, "cache", "first")
	svc.bindOpenAICompatSessionTurnState(ctx, c, account, "cache", "later")
	require.Equal(t, "first", svc.getOpenAICompatSessionTurnState(ctx, c, account, "cache"))
	require.Empty(t, svc.getOpenAICompatSessionTurnState(ctx, c, account, "other-cache"))
	stageCodexRoutingTurn(c, []byte(`{"client_metadata":{"turn_id":"two"}}`))
	require.Empty(t, svc.getOpenAICompatSessionTurnState(ctx, c, account, "cache"))
	stageCodexRoutingTurn(c, []byte(`{"client_metadata":{"turn_id":"one"}}`))
	oldWSScope := openAIWSTurnStateScope(c, account, "ws-session")
	account.Credentials["chatgpt_user_id"] = "new-owner"
	require.Empty(t, svc.getOpenAICompatSessionTurnState(ctx, c, account, "cache"), "same numeric account id is not proof of the credential owner")
	require.NotEqual(t, oldWSScope, openAIWSTurnStateScope(c, account, "ws-session"))
	c.Request.Header.Del(openAIWSTurnMetadataHeader)
	stageCodexRoutingTurn(c, nil)
	svc.bindOpenAICompatSessionTurnState(ctx, c, account, "cache", "unproven")
	require.Empty(t, svc.getOpenAICompatSessionTurnState(ctx, c, account, "cache"))

	apiKey := &Account{ID: 72, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	svc.bindOpenAICompatSessionTurnState(ctx, c, apiKey, "cache", "api-state")
	svc.bindOpenAICompatSessionResponseID(ctx, c, apiKey, "cache", "resp_api")
	stageCodexRoutingTurn(c, []byte(`{"client_metadata":{"turn_id":"another-turn"}}`))
	require.Equal(t, "api-state", svc.getOpenAICompatSessionTurnState(ctx, c, apiKey, "cache"))
	require.Equal(t, "resp_api", svc.getOpenAICompatSessionResponseID(ctx, c, apiKey, "cache"))
}

func TestCodexQualityCommittedStateRetainsFirstAndRejectsChangedSubject(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := codexQualityAccount()
	c, _ := newTurnStateTestContext(t, 11, "fixture-session")
	first := http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"first"}}
	later := http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"later"}}
	svc.noteStagedOpenAICodexTurnStateCommitted(c, account, first)
	prepared := svc.codexTurnStateResponseHeaders(c, account, later)
	require.Equal(t, "first", prepared.Get(openAICodexTurnStateHeader))
	require.Equal(t, "later", later.Get(openAICodexTurnStateHeader), "retain the untouched upstream evidence")
	svc.noteStagedOpenAICodexTurnStateCommitted(c, account, later)
	svc.noteStagedOpenAICodexTurnStateCommitted(c, account, nil)
	keep := first.Clone()
	svc.guardOpenAICodexTurnStateEcho(c, account, keep)
	require.Equal(t, "first", keep.Get(openAICodexTurnStateHeader))
	svc.guardOpenAICodexTurnStateEcho(c, account, later)
	require.Empty(t, later.Get(openAICodexTurnStateHeader))
	account.Credentials["chatgpt_account_id"] = "another-workspace"
	svc.guardOpenAICodexTurnStateEcho(c, account, keep)
	require.Empty(t, keep.Get(openAICodexTurnStateHeader))
}

func TestCodexQualityWSTurnCannotFallBackToHandshake(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := codexQualityAccount()
	c, _ := newTurnStateTestContext(t, 11, "fixture-session")
	c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"turn_id":"old-handshake"}`)
	stageCodexRoutingWSTurn(c, []byte(`{"client_metadata":{"turn_id":"tool-turn"}}`))
	svc.noteStagedOpenAICodexTurnStateCommitted(c, account, http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"tool-state"}})
	stageCodexRoutingWSTurn(c, []byte(`{"client_metadata":{"turn_id":"tool-turn"},"input":[{"type":"function_call_output","call_id":"call-one","output":"ok"}]}`))
	state := http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"tool-state"}}
	svc.guardOpenAICodexTurnStateEcho(c, account, state)
	require.Equal(t, "tool-state", state.Get(openAICodexTurnStateHeader))
	stageCodexRoutingWSTurn(c, []byte(`{"input":[{"type":"message","role":"user","content":"new user turn"}]}`))
	require.Empty(t, codexRoutingTurnID(c))
	require.Empty(t, openAIWSTurnStateScope(c, account, "fixture-session"))
	svc.guardOpenAICodexTurnStateEcho(c, account, state)
	require.Empty(t, state.Get(openAICodexTurnStateHeader))
}

func TestCodexQualityMessagesStateCommitsOnlyCompletedDownstream(t *testing.T) {
	for _, test := range []struct {
		name, status                  string
		stream, writeFails, wantState bool
	}{
		{"buffered_success", "completed", false, false, true},
		{"stream_success", "completed", true, false, true},
		{"buffered_failed", "failed", false, false, false},
		{"stream_failed", "failed", true, false, false},
		{"buffered_incomplete", "incomplete", false, false, false},
		{"stream_incomplete", "incomplete", true, false, false},
		{"buffered_write_failure", "completed", false, true, false},
		{"stream_write_failure", "completed", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := newTurnStateTestContext(t, 11, "fixture-session")
			c.Request.URL.Path = "/v1/messages"
			if test.writeFails {
				c.Writer = &failingGinWriter{ResponseWriter: c.Writer, failAfter: 0}
			}
			terminal := `{"type":"response.` + test.status + `","response":{"id":"resp_quality","object":"response","model":"gpt-6-astra","status":"` + test.status + `","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"pong"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`
			if test.status == "failed" {
				terminal = `{"type":"response.failed","response":{"id":"resp_quality","status":"failed","model":"gpt-6-astra","error":{"type":"invalid_request_error","code":"invalid_request_error","message":"fixture failure"}}}`
			}
			payload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_quality\",\"model\":\"gpt-6-astra\",\"status\":\"in_progress\"}}\n\n"
			if test.status != "failed" {
				payload += "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"pong\"}\n\n"
			}
			payload += "data: " + terminal + "\n\n"
			headers := http.Header{"Content-Type": []string{"text/event-stream"}}
			headers.Set(openAICodexTurnStateHeader, "header-is-not-a-commit")
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(payload))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := codexQualityAccount()
			stream := "false"
			if test.stream {
				stream = "true"
			}
			body := []byte(`{"model":"gpt-6-astra","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":` + stream + `}`)
			_, _ = svc.ForwardAsAnthropic(withCodexTransportFixture(context.Background(), false), c, account, body, "fixed-cache", "")
			require.Len(t, upstream.requests, 1)
			stored := svc.getOpenAICompatSessionTurnState(context.Background(), c, account, "fixed-cache")
			require.Equal(t, test.wantState, stored != "", "only a complete response successfully written downstream may save STATE")
		})
	}
}

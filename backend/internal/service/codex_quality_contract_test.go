package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func codexQualityAccount() *Account {
	return &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1, RateMultiplier: f64p(1),
		Credentials: map[string]any{"access_token": "fixture-token", "chatgpt_account_id": "fixture-workspace", "chatgpt_user_id": "fixture-owner"}}
}

func TestCodexQualityProbeMatchesBusinessHeadersAndPreservesEffort(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := codexQualityAccount()
	for _, effort := range []string{"", "high"} {
		t.Run("effort_"+effort, func(t *testing.T) {
			ctx := withCodexTransportFixture(context.Background(), false)
			probe, err := svc.buildCodexRoutingProbe(ctx, account, "gpt-6-astra", "fixture-token", "", effort)
			require.NoError(t, err)
			probeBody, err := io.ReadAll(probe.Body)
			require.NoError(t, err)
			require.Equal(t, effort, gjson.GetBytes(probeBody, "reasoning.effort").String())
			require.Equal(t, effort != "", gjson.GetBytes(probeBody, "reasoning.effort").Exists())
			require.Equal(t, "ping", gjson.GetBytes(probeBody, "input.0.content.0.text").String())
			require.Empty(t, probe.Header.Get(responsesLiteHeader), "a lightweight probe does not impersonate the Lite contract")

			body := []byte(`{"model":"gpt-6-astra","stream":true,"input":"business input","reasoning":{"effort":"high"}}`)
			if effort == "" {
				body = []byte(`{"model":"gpt-6-astra","stream":true,"input":"business input"}`)
			}
			c, _ := newTurnStateTestContext(t, 11, "fixture-session")
			business, err := svc.buildUpstreamRequest(ctx, c, account, body, "fixture-token", true, "fixture-session", true)
			require.NoError(t, err)
			for _, name := range []string{"User-Agent", "originator", "version", "chatgpt-account-id", "chatgpt-user-id", openAICodexRoutingHintHeader} {
				require.Equal(t, probe.Header.Get(name), business.Header.Get(name), name)
			}
			require.Equal(t, "model=gpt-6-astra", business.Header.Get(openAICodexRoutingHintHeader))
			businessBody, err := io.ReadAll(business.Body)
			require.NoError(t, err)
			require.Equal(t, effort, gjson.GetBytes(businessBody, "reasoning.effort").String())
			require.Equal(t, effort != "", gjson.GetBytes(businessBody, "reasoning.effort").Exists())
		})
	}
	_, err := svc.buildCodexRoutingProbe(context.Background(), account, "gpt-6-astra", "fixture-token", "", "unrecognized")
	require.Error(t, err)
}

func TestCodexQualityLitePreservesInputAndOmittedInstructions(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "managed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6-astra","stream":true,"reasoning":{"effort":"high","context":"all_turns"},"parallel_tool_calls":false,"tool_choice":"auto","input":[{"type":"additional_tools","role":"developer","tools":[]},{"type":"message","role":"developer","content":[{"type":"input_text","text":"preserve this unique base prompt"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.156.0")
			c.Request.Header.Set(responsesLiteHeader, "true")
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_lite\",\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")),
			}}
			account := codexQualityAccount()
			account.Extra = map[string]any{"openai_passthrough": passthrough}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			_, err := svc.Forward(withCodexTransportFixture(context.Background(), false), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, upstream.lastReq)
			require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists(), "Lite already carries its developer base prompt in input")
			require.False(t, gjson.GetBytes(upstream.lastBody, "tools").Exists())
			require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(upstream.lastBody, "input").Raw)
			require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
		})
	}
}

type codexQualityLeaseDirectory struct {
	*routingHostDirectoryFixture
	live   bool
	checks int
}

func (d *codexQualityLeaseDirectory) CheckCodexRoutingLease(_ context.Context, scope extensionv1.CodexRoutingScope, deadline time.Time) error {
	d.checks++
	if !d.live || scope.ConnectionLeaseID != "actual-connection" || !time.Now().Before(deadline) {
		return ErrCodexConnectionLeaseExpired
	}
	return nil
}

func TestCodexQualityHostCheckRejectsLostLeaseWithoutIO(t *testing.T) {
	host, base, _ := routingHostFixture()
	directory := &codexQualityLeaseDirectory{routingHostDirectoryFixture: base, live: true}
	host.directory = directory
	call := func(operation extensionv1.HostOperation, query extensionv1.CodexRoutingQuery) extensionv1.CodexRoutingProbeResult {
		raw, err := json.Marshal(query)
		require.NoError(t, err)
		result, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: operation, Payload: raw})
		require.NoError(t, err)
		var value extensionv1.CodexRoutingProbeResult
		require.NoError(t, json.Unmarshal(result.Payload, &value))
		return value
	}
	query := extensionv1.CodexRoutingQuery{AccountID: 7, Model: "gpt-6-astra", Transport: "http", OperationID: "local-lease", Stage: "acquire"}
	candidate := call(extensionv1.HostCodexRoutingProbe, query)
	require.True(t, candidate.Valid)
	query.Stage, query.Bundle = "verify", candidate.Bundle
	verified := call(extensionv1.HostCodexRoutingProbe, query)
	require.True(t, verified.Valid)
	query.Bundle, query.Scope = verified.Bundle, &verified.Scope
	require.True(t, call(extensionv1.HostCodexRoutingCheck, query).Valid)
	directory.live = false // restart/lost physical connection with a still-live bundle
	checked := call(extensionv1.HostCodexRoutingCheck, query)
	require.False(t, checked.Valid)
	require.Equal(t, "routing_connection_expired", checked.Observation.Code)
	require.Equal(t, 2, directory.requests, "a local qualification check must never probe or redial")
	directory.live = true
	changed := *verified.Bundle
	changed.ExpiresAt = changed.ExpiresAt.Add(time.Second)
	query.Bundle = &changed
	require.False(t, call(extensionv1.HostCodexRoutingCheck, query).Valid, "reference expiry must match the persisted bundle")
	query.Bundle = verified.Bundle
	changed = *verified.Bundle
	changed.ConnectionLeaseID = "another-connection"
	query.Bundle = &changed
	require.False(t, call(extensionv1.HostCodexRoutingCheck, query).Valid)
	require.Equal(t, 2, directory.requests)
}

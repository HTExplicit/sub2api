package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func codexQualityAccount() *Account {
	return &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1, RateMultiplier: f64p(1),
		Credentials: map[string]any{"access_token": "fixture-token", "chatgpt_account_id": "fixture-workspace", "chatgpt_user_id": "fixture-owner"}}
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

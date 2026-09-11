package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const compatibleSummaryRequest = `{
	"model":"gpt-6-astra-project",
	"input":[
		{"type":"additional_tools","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}}]}]},
		{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":"detailed"}
	],
	"reasoning":{"effort":"xhigh","summary":"detailed","context":"all_turns"},
	"text":{"verbosity":"high"},
	"stream":true,
	"extension":{"precise_integer":9007199254740993,"summary":"detailed"}
}`

func compatibleSummaryWantAuto(body string) string {
	return strings.Replace(body, `"effort":"xhigh","summary":"detailed"`, `"effort":"xhigh","summary":"auto"`, 1)
}

func TestNormalizeOpenAICompatibleResponsesReasoningSummary(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	compatEndpoint := "https://ai-incubator-api.pnnl.gov/v1/responses"
	want := compatibleSummaryWantAuto(compatibleSummaryRequest)

	for _, targetURL := range []string{compatEndpoint, "https://AI-INCUBATOR-API.PNNL.GOV:443/v1/responses", "https://gateway.example.com/v1/responses"} {
		got, err := normalizeOpenAICompatibleResponsesReasoningSummary(account, targetURL, []byte(compatibleSummaryRequest))
		require.NoError(t, err)
		require.Equal(t, want, string(got), "only the request reasoning.summary may change")
	}

	concise := strings.Replace(compatibleSummaryRequest, `"summary":"detailed"`, `"summary":"concise"`, 1)
	got, err := normalizeOpenAICompatibleResponsesReasoningSummary(account, compatEndpoint, []byte(concise))
	require.NoError(t, err)
	require.Equal(t, want, string(got))

	for _, tc := range []struct {
		name    string
		account *Account
		url     string
		body    string
	}{
		{"missing account", nil, compatEndpoint, compatibleSummaryRequest},
		{"OAuth", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, compatEndpoint, compatibleSummaryRequest},
		{"Cindy", &Account{Platform: PlatformCindy, Type: AccountTypeAPIKey}, compatEndpoint, compatibleSummaryRequest},
		{"official host", account, "https://api.openai.com/v1/responses", compatibleSummaryRequest},
		{"other port", account, "https://ai-incubator-api.pnnl.gov:8443/v1/responses", compatibleSummaryRequest},
		{"HTTP", account, "http://ai-incubator-api.pnnl.gov/v1/responses", compatibleSummaryRequest},
		{"compact", account, compatEndpoint + "/compact", compatibleSummaryRequest},
		{"passthrough extra", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{openai_compat.ExtraKeyReasoningSummaryMode: string(openai_compat.ReasoningSummaryModePassthrough)}}, compatEndpoint, compatibleSummaryRequest},
		{"auto", account, compatEndpoint, want},
		{"missing summary", account, compatEndpoint, `{"model":"gpt-6-astra-project","reasoning":{"effort":"xhigh"}}`},
		{"non-string summary", account, compatEndpoint, `{"model":"gpt-6-astra-project","reasoning":{"summary":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeOpenAICompatibleResponsesReasoningSummary(tc.account, tc.url, []byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, tc.body, string(got), "non-target requests must remain byte-for-byte unchanged")
		})
	}
}

func TestNormalizeOpenAICompatibleResponsesReasoningSummaryModels(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, model := range []string{
		"gpt-6-astra", "gpt-6-astra-project", "gpt-5.5", "gpt-5.5-project",
		"gpt-5.6", "gpt-5.6-sol-project", "gpt-5.6-terra-project", "gpt-5.6-luna", "gpt-5.6-luna-project",
	} {
		t.Run(model, func(t *testing.T) {
			body := strings.Replace(compatibleSummaryRequest, "gpt-6-astra-project", model, 1)
			want := compatibleSummaryWantAuto(body)
			got, err := normalizeOpenAICompatibleResponsesReasoningSummary(account, "https://ai-incubator-api.pnnl.gov/v1/responses", []byte(body))
			require.NoError(t, err)
			require.Equal(t, want, string(got))
		})
	}
}

func TestNormalizeOpenAICompatibleResponsesReasoningSummaryOmit(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{openai_compat.ExtraKeyReasoningSummaryMode: string(openai_compat.ReasoningSummaryModeOmit)},
	}
	got, err := normalizeOpenAICompatibleResponsesReasoningSummary(account, "https://gateway.example.com/v1/responses", []byte(compatibleSummaryRequest))
	require.NoError(t, err)
	require.Equal(t, "xhigh", gjson.GetBytes(got, "reasoning.effort").String())
	require.Equal(t, "all_turns", gjson.GetBytes(got, "reasoning.context").String())
	require.False(t, gjson.GetBytes(got, "reasoning.summary").Exists())
	require.Equal(t, "detailed", gjson.GetBytes(got, "extension.summary").String())
	require.Equal(t, "high", gjson.GetBytes(got, "text.verbosity").String())

	onlySummary := []byte(`{"model":"gpt-5.5","reasoning":{"summary":"detailed"}}`)
	got, err = normalizeOpenAICompatibleResponsesReasoningSummary(account, "https://gateway.example.com/v1/responses", onlySummary)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(got, "reasoning").Exists())
	require.Equal(t, "gpt-5.5", gjson.GetBytes(got, "model").String())
}

func TestOpenAICompatibleResponsesReasoningSummaryHTTPBuilders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	want := compatibleSummaryWantAuto(compatibleSummaryRequest)

	t.Run("compatible clamps", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://ai-incubator-api.pnnl.gov/v1",
			},
		}
		assertCompatibleSummaryBuilder(t, svc, account, "https://ai-incubator-api.pnnl.gov/v1/responses", want)
	})

	t.Run("official passthrough", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://api.openai.com/v1",
			},
		}
		assertCompatibleSummaryBuilder(t, svc, account, "https://api.openai.com/v1/responses", compatibleSummaryRequest)
	})

	t.Run("passthrough extra", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://ai-incubator-api.pnnl.gov/v1",
			},
			Extra: map[string]any{openai_compat.ExtraKeyReasoningSummaryMode: string(openai_compat.ReasoningSummaryModePassthrough)},
		}
		assertCompatibleSummaryBuilder(t, svc, account, "https://ai-incubator-api.pnnl.gov/v1/responses", compatibleSummaryRequest)
	})
}

func assertCompatibleSummaryBuilder(t *testing.T, svc *OpenAIGatewayService, account *Account, wantURL, wantBody string) {
	t.Helper()
	for _, passthrough := range []bool{false, true} {
		name := "ordinary"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/responses", bytes.NewBufferString(compatibleSummaryRequest))
			var req *http.Request
			var err error
			if passthrough {
				req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, []byte(compatibleSummaryRequest), "test-token")
			} else {
				req, err = svc.buildUpstreamRequest(context.Background(), c, account, []byte(compatibleSummaryRequest), "test-token", true, "", false)
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = req.Body.Close() })
			require.Equal(t, wantURL, req.URL.String())
			wireBody, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, wantBody, string(wireBody))
			require.Equal(t, int64(len(wireBody)), req.ContentLength)
			snapshot, err := req.GetBody()
			require.NoError(t, err)
			defer func() { _ = snapshot.Close() }()
			frozenBody, err := io.ReadAll(snapshot)
			require.NoError(t, err)
			require.Equal(t, wireBody, frozenBody, "request snapshots must contain the same normalized wire body")
		})
	}
}

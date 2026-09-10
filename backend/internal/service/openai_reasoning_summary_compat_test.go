package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const pnnlSummaryRequest = `{
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

func TestNormalizePNNLResponsesReasoningSummary(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	endpoint := "https://ai-incubator-api.pnnl.gov/v1/responses"
	want := strings.Replace(pnnlSummaryRequest, `"effort":"xhigh","summary":"detailed"`, `"effort":"xhigh","summary":"auto"`, 1)

	for _, targetURL := range []string{endpoint, "https://AI-INCUBATOR-API.PNNL.GOV:443/v1/responses"} {
		got, err := normalizePNNLResponsesReasoningSummary(account, targetURL, []byte(pnnlSummaryRequest))
		require.NoError(t, err)
		require.Equal(t, want, string(got), "only the request reasoning.summary may change")
	}

	for _, tc := range []struct {
		name    string
		account *Account
		url     string
		body    string
	}{
		{"missing account", nil, endpoint, pnnlSummaryRequest},
		{"OAuth", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, endpoint, pnnlSummaryRequest},
		{"Cindy", &Account{Platform: PlatformCindy, Type: AccountTypeAPIKey}, endpoint, pnnlSummaryRequest},
		{"other host", account, "https://api.openai.com/v1/responses", pnnlSummaryRequest},
		{"host suffix", account, "https://ai-incubator-api.pnnl.gov.example.com/v1/responses", pnnlSummaryRequest},
		{"other port", account, "https://ai-incubator-api.pnnl.gov:8443/v1/responses", pnnlSummaryRequest},
		{"HTTP", account, "http://ai-incubator-api.pnnl.gov/v1/responses", pnnlSummaryRequest},
		{"compact", account, endpoint + "/compact", pnnlSummaryRequest},
		{"other model", account, endpoint, strings.Replace(pnnlSummaryRequest, "gpt-6-astra-project", "gpt-5.6-sol-project", 1)},
		{"unmapped model", account, endpoint, strings.Replace(pnnlSummaryRequest, "gpt-6-astra-project", "gpt-6-astra", 1)},
		{"auto", account, endpoint, want},
		{"missing summary", account, endpoint, `{"model":"gpt-6-astra-project","reasoning":{"effort":"xhigh"}}`},
		{"non-string summary", account, endpoint, `{"model":"gpt-6-astra-project","reasoning":{"summary":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizePNNLResponsesReasoningSummary(tc.account, tc.url, []byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, tc.body, string(got), "non-target requests must remain byte-for-byte unchanged")
		})
	}
}

func TestPNNLResponsesReasoningSummaryHTTPBuilders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://ai-incubator-api.pnnl.gov/v1",
		},
	}
	want := strings.Replace(pnnlSummaryRequest, `"effort":"xhigh","summary":"detailed"`, `"effort":"xhigh","summary":"auto"`, 1)
	for _, passthrough := range []bool{false, true} {
		name := "ordinary"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/responses", bytes.NewBufferString(pnnlSummaryRequest))
			var req *http.Request
			var err error
			if passthrough {
				req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, []byte(pnnlSummaryRequest), "test-token")
			} else {
				req, err = svc.buildUpstreamRequest(context.Background(), c, account, []byte(pnnlSummaryRequest), "test-token", true, "", false)
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = req.Body.Close() })
			require.Equal(t, "https://ai-incubator-api.pnnl.gov/v1/responses", req.URL.String())
			wireBody, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, want, string(wireBody))
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

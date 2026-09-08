//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardResponsesOllamaClampPreservesContinuationPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-flash","input":"next","previous_response_id":"resp_native_reference","max_output_tokens":256000,"stream":false}`)
	for _, tc := range []struct {
		name       string
		platform   string
		previousID string
	}{
		{"openai_native_reference", PlatformOpenAI, "resp_native_reference"},
		{"existing_cn_stateless_adapter", PlatformDeepseek, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := ollamaUpstreamTestAccount(tc.platform, 391)
			account.Credentials["api_protocol"] = APIProtocolResponses
			account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
			upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

			_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), account, body)
			require.Error(t, err)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "https://ollama.com/v1/responses", upstream.lastReq.URL.String())
			require.Equal(t, int64(65535), gjson.GetBytes(upstream.lastBody, "max_output_tokens").Int())
			require.Equal(t, tc.previousID, gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
			require.Equal(t, "deepseek-v4-flash", gjson.GetBytes(upstream.lastBody, "model").String())
		})
	}
}

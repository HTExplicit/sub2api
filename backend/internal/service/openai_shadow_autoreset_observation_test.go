//go:build unit

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
)

func TestOpenAIShadowSuccessAutoResetHonorsObservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []struct {
		name, path, body string
		passthrough      bool
	}{
		{"responses", "/v1/responses", `{"model":"gpt-5.5","input":"hello","stream":false}`, false},
		{"passthrough", "/v1/responses", `{"model":"gpt-5.5","input":"hello","stream":false}`, true},
		{"chat", "/v1/chat/completions", `{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":false}`, false},
		{"messages", "/v1/messages", `{"model":"gpt-5.5","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`, false},
	} {
		for _, observation := range []bool{false, true} {
			mode := "business"
			if observation {
				mode = "observation"
			}
			t.Run(endpoint.name+"/"+mode, func(t *testing.T) {
				notifier := NewOpenAIQuotaAutoResetService(nil, nil, nil, nil, nil, nil, nil)
				openAIAutoResetNotifierRegistry.Lock()
				previous := openAIAutoResetNotifierRegistry.service
				openAIAutoResetNotifierRegistry.service = notifier
				openAIAutoResetNotifierRegistry.Unlock()
				defer func() {
					openAIAutoResetNotifierRegistry.Lock()
					openAIAutoResetNotifierRegistry.service = previous
					openAIAutoResetNotifierRegistry.Unlock()
					notifier.cancel()
				}()

				parent := &Account{ID: 901, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
					Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-parent"}}
				shadow := &Account{ID: 902, ParentAccountID: &parent.ID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
					Status: StatusActive, Schedulable: true, Concurrency: 1,
					Extra: map[string]any{"openai_passthrough": endpoint.passthrough, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff}}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(codexCompletedSSE(`{"id":"resp_shadow_success","status":"completed","model":"gpt-5.5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"shadow success"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`))),
				}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream,
					accountRepo: &stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent, shadow.ID: shadow}}}
				ctx := context.Background()
				if observation {
					ctx = WithAccountObservation(ctx)
				}
				body := []byte(endpoint.body)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, endpoint.path, bytes.NewReader(body)).WithContext(ctx)
				var result *OpenAIForwardResult
				var err error
				switch endpoint.name {
				case "chat":
					result, err = svc.ForwardAsChatCompletions(ctx, c, shadow, body, "", "")
				case "messages":
					result, err = svc.ForwardAsAnthropic(ctx, c, shadow, body, "", "")
				default:
					result, err = svc.Forward(ctx, c, shadow, body)
					require.Equal(t, endpoint.passthrough, c.GetBool("openai_passthrough"))
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Contains(t, recorder.Body.String(), "shadow success")
				require.Len(t, upstream.requests, 1)
				if observation {
					require.Empty(t, notifier.queue)
				} else {
					require.Len(t, notifier.queue, 1)
					require.Equal(t, parent.ID, <-notifier.queue)
				}
			})
		}
	}
}

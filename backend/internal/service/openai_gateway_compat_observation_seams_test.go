package service

import (
	"context"
	"errors"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCompatStreamAccountObservationDoesNotWriteHealth(t *testing.T) {
	for _, endpoint := range []struct {
		name string
		path string
		body []byte
		send func(*OpenAIGatewayService, context.Context, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{"chat", "/v1/chat/completions", []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"stream":true}`),
			func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-5.5")
			}},
		{"messages", "/v1/messages", []byte(`{"model":"gpt-5.5","max_tokens":1024,"messages":[{"role":"user","content":"hi"}],"stream":true}`),
			func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.ForwardAsAnthropic(ctx, c, account, body, "", "gpt-5.5")
			}},
	} {
		for _, outputFirst := range []bool{false, true} {
			phase := "before_output"
			if outputFirst {
				phase = "after_output"
			}
			t.Run(endpoint.name+"/"+phase, func(t *testing.T) {
				svc, repo, account := newCompatStreamSideEffectsService(compatStreamSideEffectsSSE(outputFirst, compatStreamAuthErrorEvent))
				ctx := WithAccountObservation(context.Background())
				c, rec := newCompatStreamContext(endpoint.path, endpoint.body)
				c.Request = c.Request.WithContext(ctx)
				_, err := endpoint.send(svc, ctx, c, account, endpoint.body)
				require.Error(t, err)
				var failoverErr *UpstreamFailoverError
				require.Equal(t, !outputFirst, errors.As(err, &failoverErr), "observation retains official output/failover behavior")
				if outputFirst {
					require.Contains(t, rec.Body.String(), "partial")
				}
				require.Empty(t, repo.errorCalls)
				require.Empty(t, repo.rateLimitCalls)
			})
		}
	}
}

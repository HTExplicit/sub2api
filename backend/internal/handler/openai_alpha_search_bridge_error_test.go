package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAlphaSearchBridgeFailoverExhaustionReturnsSafeRetryableError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)

	failoverErr := service.NewOpenAIAlphaSearchBridgeUnavailableError(
		http.StatusBadRequest,
		nil,
		[]byte(`{"error":{"message":"upstream tool detail must-not-leak"}}`),
	)
	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failoverErr, false)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.JSONEq(t, `{
		"error": {
			"type": "server_error",
			"code": "web_search_unavailable",
			"message": "Web search is temporarily unavailable",
			"retryable": true
		}
	}`, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
}

func TestResolveOpenAIAlphaSearchUpstreamEndpointDistinguishesDirectAndBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)
	account := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}

	require.Equal(t, EndpointAlphaSearch, resolveOpenAIAlphaSearchUpstreamEndpoint(c, account, &service.OpenAIForwardResult{}))
	require.Equal(t, EndpointResponses, resolveOpenAIAlphaSearchUpstreamEndpoint(c, account, &service.OpenAIForwardResult{
		UpstreamEndpoint: EndpointResponses,
	}))

	service.SetActualOpenAIUpstreamEndpoint(c, EndpointResponses)
	service.SetActualOpenAIUpstreamEndpoint(c, "")
	setActualUpstreamEndpoint(c, "")
	require.Equal(t, EndpointAlphaSearch, resolveOpenAIAlphaSearchUpstreamEndpoint(c, account, nil))
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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

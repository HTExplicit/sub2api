package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cindyBalancePassthroughRuleRepo struct {
	rules []*model.ErrorPassthroughRule
}

func (r *cindyBalancePassthroughRuleRepo) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return r.rules, nil
}

func (r *cindyBalancePassthroughRuleRepo) GetByID(context.Context, int64) (*model.ErrorPassthroughRule, error) {
	return nil, nil
}

func (r *cindyBalancePassthroughRuleRepo) Create(_ context.Context, rule *model.ErrorPassthroughRule) (*model.ErrorPassthroughRule, error) {
	return rule, nil
}

func (r *cindyBalancePassthroughRuleRepo) Update(_ context.Context, rule *model.ErrorPassthroughRule) (*model.ErrorPassthroughRule, error) {
	return rule, nil
}

func (r *cindyBalancePassthroughRuleRepo) Delete(context.Context, int64) error { return nil }

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

func TestCindyBalanceFailoverBypassesErrorPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)
	ruleService := service.NewErrorPassthroughService(&cindyBalancePassthroughRuleRepo{rules: []*model.ErrorPassthroughRule{
		{
			ID:              1,
			Name:            "would expose raw budget body",
			Enabled:         true,
			Priority:        1,
			ErrorCodes:      []int{http.StatusTooManyRequests},
			Keywords:        []string{"budget_exceeded"},
			MatchMode:       model.MatchModeAny,
			Platforms:       []string{model.PlatformOpenAI},
			PassthroughCode: true,
			PassthroughBody: true,
		},
	}}, nil)
	handler := &OpenAIGatewayHandler{errorPassthroughService: ruleService}
	failoverErr := &service.UpstreamFailoverError{
		StatusCode:               http.StatusTooManyRequests,
		ResponseBody:             []byte(`{"error":{"type":"budget_exceeded","code":"429","message":"fixture-account"}}`),
		CindyBalanceInsufficient: true,
	}

	handler.handleFailoverExhausted(c, failoverErr, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Upstream rate limit exceeded")
	require.NotContains(t, recorder.Body.String(), "budget_exceeded")
	require.NotContains(t, recorder.Body.String(), "fixture-account")
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

package service

import (
	"bytes"
	"context"
	"errors"
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

func firstClassCindyAlphaSearchAccount(id int64) *Account {
	return &Account{
		ID:              id,
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Status:          StatusActive,
		Schedulable:     true,
		Concurrency:     1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.laxarouter.ai",
		},
	}
}

func alphaSearchHTTPResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func cindyAlphaSearchMessagesSuccessResponse() *http.Response {
	return alphaSearchHTTPResponse(http.StatusOK, "application/json", `{
		"id":"msg-search-1",
		"type":"message",
		"model":"cindy/web-search",
		"content":[
			{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://example.com/news","title":"Example News"}]},
			{"type":"text","text":"fallback result","citations":[{"type":"web_search_result_location","url":"https://example.com/news","title":"Example News"}]}
		],
		"usage":{"input_tokens":11,"output_tokens":4,"server_tool_use":{"web_search_requests":1}}
	}`)
}

func newCindyAlphaSearchServiceContext(t *testing.T, upstream HTTPUpstream) (*OpenAIGatewayService, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}, c, recorder
}

func TestForwardAlphaSearchCindyUsesPublicResponsesModelBeforeHiddenFallback(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		alphaSearchHTTPResponse(http.StatusOK, "text/event-stream", "event: response.output_item.done\n"+
			`data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_1","status":"completed"}}`+"\n\n"+
			"event: response.output_text.delta\n"+
			`data: {"type":"response.output_text.delta","delta":"native result"}`+"\n\n"),
	}}
	service, c, recorder := newCindyAlphaSearchServiceContext(t, upstream)
	body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)

	result, err := service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61001), body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/responses", result.UpstreamEndpoint)
	require.Equal(t, "openai/gpt-5.6-luna", result.UpstreamModel)
	require.Equal(t, []string{"/v1/responses"}, []string{upstream.requests[0].URL.Path})
	require.Equal(t, "openai/gpt-5.6-luna", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Equal(t, "web_search", gjson.GetBytes(upstream.bodies[0], "tools.0.type").String())
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"output":"native result"}`, recorder.Body.String())
}

func TestForwardAlphaSearchCindyFallsBackToHiddenMessagesOnlyForUnsupportedOrUnprovenResponses(t *testing.T) {
	tests := []struct {
		name          string
		firstResponse func() *http.Response
	}{
		{
			name: "structured tool unsupported",
			firstResponse: func() *http.Response {
				return alphaSearchHTTPResponse(http.StatusBadRequest, "application/json", `{"error":{"type":"invalid_request_error","code":"unsupported_tool","param":"tools[0]","message":"web_search tool is unsupported"}}`)
			},
		},
		{
			name: "successful response without search evidence",
			firstResponse: func() *http.Response {
				return alphaSearchHTTPResponse(http.StatusOK, "text/event-stream", "event: response.output_text.delta\n"+
					`data: {"type":"response.output_text.delta","delta":"plain answer"}`+"\n\n"+
					"event: response.completed\n"+
					`data: {"type":"response.completed","response":{"status":"completed","output":[]}}`+"\n\n")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{test.firstResponse(), cindyAlphaSearchMessagesSuccessResponse()}}
			service, c, recorder := newCindyAlphaSearchServiceContext(t, upstream)
			body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)

			result, err := service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61002), body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "/v1/messages", result.UpstreamEndpoint)
			require.Equal(t, "gpt-5.6-luna", result.Model)
			require.Equal(t, "openai/gpt-5.6-luna", result.BillingModel)
			require.Len(t, upstream.requests, 2)
			require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
			require.Equal(t, "/v1/messages", upstream.requests[1].URL.Path)
			require.Equal(t, "cindy/web-search", gjson.GetBytes(upstream.bodies[1], "model").String())
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), "fallback result")
		})
	}
}

func TestForwardAlphaSearchCindyEndpointErrorsDoNotSwitchProtocol(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				alphaSearchHTTPResponse(status, "application/json", `{"error":{"type":"not_found_error","message":"responses unavailable"}}`),
				cindyAlphaSearchMessagesSuccessResponse(),
			}}
			service, c, _ := newCindyAlphaSearchServiceContext(t, upstream)
			body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)

			_, _ = service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61008), body)

			require.Len(t, upstream.requests, 1)
			require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
		})
	}
}

func TestForwardAlphaSearchCindyCompatibilityAliasUsesManagedLunaTarget(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		alphaSearchHTTPResponse(http.StatusOK, "text/event-stream", "event: response.output_item.done\n"+
			`data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_alias","status":"completed"}}`+"\n\n"),
	}}
	service, c, _ := newCindyAlphaSearchServiceContext(t, upstream)
	body := []byte(`{"model":"gpt-5.4-mini","commands":{"search_query":[{"q":"news"}]}}`)

	result, err := service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61009), body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4-mini", result.Model)
	require.Equal(t, "openai/gpt-5.6-luna", result.UpstreamModel)
	require.Equal(t, "openai/gpt-5.6-luna", gjson.GetBytes(upstream.bodies[0], "model").String())
}

func TestForwardAlphaSearchCindyOperationalResponsesFailuresNeverSwitchProtocol(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "server error", status: http.StatusInternalServerError, body: `{"error":{"type":"server_error","message":"temporary"}}`},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":{"type":"permission_error","message":"denied"}}`},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"error":{"type":"rate_limit_error","message":"slow down"}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				alphaSearchHTTPResponse(test.status, "application/json", test.body),
				cindyAlphaSearchMessagesSuccessResponse(),
			}}
			service, c, _ := newCindyAlphaSearchServiceContext(t, upstream)
			body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)

			result, err := service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61003), body)

			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.False(t, failoverErr.IsOpenAIAlphaSearchBridgeUnavailable())
			require.False(t, failoverErr.SuppressAccountHealthPenalty)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
		})
	}

	t.Run("transport error", func(t *testing.T) {
		upstream := &httpUpstreamRecorder{err: errors.New("upstream connection reset")}
		service, c, _ := newCindyAlphaSearchServiceContext(t, upstream)
		body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)

		result, err := service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61005), body)

		require.Nil(t, result)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.False(t, failoverErr.IsOpenAIAlphaSearchBridgeUnavailable())
		require.False(t, failoverErr.SuppressAccountHealthPenalty)
		require.Len(t, upstream.requests, 1)
		require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
	})
}

func TestForwardAlphaSearchCindyGenericBadRequestDoesNotSwitchProtocol(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		alphaSearchHTTPResponse(http.StatusBadRequest, "application/json", `{"error":{"type":"invalid_request_error","message":"invalid search context size"}}`),
		cindyAlphaSearchMessagesSuccessResponse(),
	}}
	service, c, recorder := newCindyAlphaSearchServiceContext(t, upstream)
	body := []byte(`{"model":"gpt-5.6-luna","settings":{"search_context_size":"invalid"}}`)

	result, err := service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61006), body)

	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
}

func TestForwardAlphaSearchCindyInvalidMessagesFallbackUsesNormalFailurePath(t *testing.T) {
	responsesWithoutEvidence := alphaSearchHTTPResponse(http.StatusOK, "text/event-stream", "event: response.completed\n"+
		`data: {"type":"response.completed","response":{"status":"completed","output":[]}}`+"\n\n")
	messagesWithoutEvidence := alphaSearchHTTPResponse(http.StatusOK, "application/json", `{
		"id":"msg-empty",
		"model":"cindy/web-search",
		"content":[{"type":"text","text":"plain answer"}],
		"usage":{"server_tool_use":{"web_search_requests":1}}
	}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{responsesWithoutEvidence, messagesWithoutEvidence}}
	service, c, _ := newCindyAlphaSearchServiceContext(t, upstream)
	body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)

	result, err := service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61004), body)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.False(t, failoverErr.SuppressAccountHealthPenalty)
	require.Len(t, upstream.requests, 2)
}

func TestForwardAlphaSearchCindyMessagesFallbackHTTPFailureClassification(t *testing.T) {
	tests := []struct {
		name           string
		status         int
		body           string
		wantCapability bool
		wantSuppressed bool
	}{
		{name: "operational failure", status: http.StatusInternalServerError, body: `{"error":{"type":"server_error","message":"temporary"}}`},
		{name: "capability unsupported", status: http.StatusNotFound, body: `{"error":{"type":"not_found_error","message":"not found"}}`, wantCapability: true, wantSuppressed: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				alphaSearchHTTPResponse(http.StatusBadRequest, "application/json", `{"error":{"type":"invalid_request_error","code":"unsupported_tool","message":"web_search tool is unsupported"}}`),
				alphaSearchHTTPResponse(test.status, "application/json", test.body),
			}}
			service, c, _ := newCindyAlphaSearchServiceContext(t, upstream)
			body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)

			result, err := service.ForwardAlphaSearch(context.Background(), c, firstClassCindyAlphaSearchAccount(61007), body)

			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, test.wantCapability, failoverErr.IsOpenAIAlphaSearchBridgeUnavailable())
			require.Equal(t, test.wantSuppressed, failoverErr.SuppressAccountHealthPenalty)
			require.Len(t, upstream.requests, 2)
			require.Equal(t, "/v1/messages", upstream.requests[1].URL.Path)
		})
	}
}

func TestOpenAIAlphaSearchResponsesEvidenceRejectsNonTerminalSearchStates(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "searching only",
			body: "event: response.web_search_call.searching\n" +
				`data: {"type":"response.web_search_call.searching","item_id":"ws_1"}` + "\n\n",
		},
		{
			name: "response failed",
			body: "event: response.failed\n" +
				`data: {"type":"response.failed","response":{"status":"failed","output":[{"type":"web_search_call","status":"completed"}]}}` + "\n\n",
		},
		{
			name: "response in progress",
			body: "event: response.in_progress\n" +
				`data: {"type":"response.in_progress","response":{"status":"in_progress","output":[{"type":"web_search_call","status":"in_progress"}]}}` + "\n\n",
		},
		{
			name: "plain text completed",
			body: "event: response.output_text.delta\n" +
				`data: {"type":"response.output_text.delta","delta":"plain"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"type":"response.completed","response":{"status":"completed","output":[]}}` + "\n\n",
		},
		{
			name: "json failed response with completed child",
			body: `{"object":"response","status":"failed","output":[{"type":"web_search_call","status":"completed"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, hasEvidence, err := openAICindyAlphaSearchResponseFromResponsesSSE([]byte(test.body))
			require.NoError(t, err)
			require.False(t, hasEvidence)
		})
	}
}

func TestOpenAIAlphaSearchResponsesEvidenceRequiresCompletedCallOrValidCitation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "completed call",
			body: "event: response.output_item.done\n" +
				`data: {"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed"}}` + "\n\n",
		},
		{
			name: "valid citation",
			body: "event: response.output_text.annotation.added\n" +
				`data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://example.com/source"}}` + "\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, hasEvidence, err := openAICindyAlphaSearchResponseFromResponsesSSE([]byte(test.body))
			require.NoError(t, err)
			require.True(t, hasEvidence)
		})
	}

	_, hasEvidence, err := openAICindyAlphaSearchResponseFromResponsesSSE([]byte("event: response.output_text.annotation.added\n" +
		`data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"javascript:alert(1)"}}` + "\n\n"))
	require.NoError(t, err)
	require.False(t, hasEvidence)
}

func TestOpenAIAlphaSearchLegacyResponsesEvidenceSemanticsRemainUnchanged(t *testing.T) {
	body := []byte("event: response.web_search_call.searching\n" +
		`data: {"type":"response.web_search_call.searching","item_id":"ws_legacy"}` + "\n\n")

	_, legacyEvidence, err := openAIAlphaSearchResponseFromResponsesSSE(body)
	require.NoError(t, err)
	require.True(t, legacyEvidence)

	_, cindyEvidence, err := openAICindyAlphaSearchResponseFromResponsesSSE(body)
	require.NoError(t, err)
	require.False(t, cindyEvidence)
}

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newOpenAIRefusalRecoveryPipelineService(t *testing.T, cyber, rewrite bool) *OpenAIGatewayService {
	t.Helper()
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot", "I'm unable"}, "继续当前任务")
	require.NoError(t, err)
	settingService := &SettingService{}
	settingService.openAIRefusalRecoveryCache.Store(&cachedOpenAIRefusalRecoveryRuntime{
		runtime: OpenAIRefusalRecoveryRuntime{
			Enabled:       true,
			CyberFailover: cyber,
			Rewrite:       rewrite,
			Matcher:       matcher,
		},
		expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	return &OpenAIGatewayService{
		cfg:            &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		settingService: settingService,
	}
}

func newOpenAIRefusalRecoveryTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

func TestOpenAINonStreamingPassthroughRewritesRefusalAndPreservesUsage(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_rewrite","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help with that."}]}],"usage":{"input_tokens":9,"output_tokens":6,"total_tokens":15}}`,
		)),
	}

	result, err := svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, "gpt-5.4", "")

	require.NoError(t, err)
	require.Equal(t, "resp_rewrite", result.responseID)
	require.Equal(t, 9, result.usage.InputTokens)
	require.Equal(t, 6, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I cannot")
}

func TestOpenAINonStreamingPassthroughSSEToJSONRewritesRefusal(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	upstream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","response_id":"resp_sse_json","item_id":"msg_1","output_index":0,"content_index":0,"delta":"I cannot help."}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_sse_json","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help."}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstream)),
	}

	result, err := svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, "gpt-5.4", "")

	require.NoError(t, err)
	require.Equal(t, "resp_sse_json", result.responseID)
	require.Equal(t, 5, result.usage.InputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I cannot")
}

func TestOpenAIHTTPPassthroughCyberPolicyReturnsFailoverBeforeWriting(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, true, false)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"X-Request-Id": []string{"req_cyber"}}}
	body := []byte(`{"error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":7,"output_tokens":0,"total_tokens":7}}`)

	err := svc.handleErrorResponsePassthrough(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, nil, body)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.ClientStatusCode)
	require.Empty(t, recorder.Body.String())
	require.False(t, c.Writer.Written())
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, 7, mark.UpstreamInTok)
}

func TestOpenAIHTTPPassthroughNonCyberErrorDoesNotTriggerRecoveryFailover(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, true, false)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}}
	body := []byte(`{"error":{"code":"content_policy","message":"blocked"}}`)

	err := svc.handleErrorResponsePassthrough(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, nil, body)

	require.Error(t, err)
	require.False(t, IsOpenAIRefusalRecoveryFailover(err))
	require.True(t, recorder.Body.Len() > 0 || c.Writer.Written())
	require.Nil(t, GetOpsCyberPolicy(c))
}

func TestOpenAIStreamingPassthroughCyberPolicyReturnsFailoverBeforeSemanticOutput(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, true, false)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	upstream := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_cyber"}}` + "\n\n" +
		"event: response.failed\n" +
		`data: {"type":"response.failed","response":{"id":"resp_cyber","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":11,"output_tokens":1,"total_tokens":12}}}` + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

	_, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "", "")

	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Empty(t, recorder.Body.String())
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, 11, mark.UpstreamInTok)
}

func TestOpenAIStreamingPassthroughRewritesRefusalAcrossDeltas(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	upstream := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_stream","object":"response","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","response_id":"resp_stream","item_id":"msg_1","output_index":0,"content_index":0,"delta":"I ca"}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","response_id":"resp_stream","item_id":"msg_1","output_index":0,"content_index":0,"delta":"nnot help."}`,
		``,
		`event: response.output_text.done`,
		`data: {"type":"response.output_text.done","response_id":"resp_stream","item_id":"msg_1","output_index":0,"content_index":0,"text":"I cannot help."}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_stream","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help."}]}],"usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}}`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

	result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "", "")

	require.NoError(t, err)
	require.Equal(t, 4, result.usage.InputTokens)
	require.Equal(t, 3, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I cannot")
	require.Contains(t, recorder.Body.String(), `"id":"resp_stream"`)
	require.Contains(t, recorder.Body.String(), `"total_tokens":7`)
}

func TestOpenAINonStreamingTranslatedResponseRewritesRefusalAndPreservesUsage(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_translated","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I'm unable to help."}]}],"usage":{"input_tokens":8,"output_tokens":4,"total_tokens":12}}`,
		)),
	}

	result, err := svc.handleNonStreamingResponse(context.Background(), resp, c, &Account{Type: AccountTypeOAuth}, "gpt-5.4", "gpt-5.4")

	require.NoError(t, err)
	require.Equal(t, "resp_translated", result.responseID)
	require.Equal(t, 8, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I'm unable")
}

func TestOpenAINonStreamingTranslatedSSEToJSONRewritesRefusal(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	upstream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","response_id":"resp_translated_sse_json","item_id":"msg_1","output_index":0,"content_index":0,"delta":"I'm unable to help."}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_translated_sse_json","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I'm unable to help."}]}],"usage":{"input_tokens":7,"output_tokens":4,"total_tokens":11}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstream)),
	}

	result, err := svc.handleNonStreamingResponse(context.Background(), resp, c, &Account{Type: AccountTypeOAuth}, "gpt-5.4", "gpt-5.4")

	require.NoError(t, err)
	require.Equal(t, "resp_translated_sse_json", result.responseID)
	require.Equal(t, 7, result.usage.InputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I'm unable")
}

func TestOpenAIStreamingTranslatedResponseCyberPolicyReturnsFailoverBeforeSemanticOutput(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, true, false)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	upstream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_translated_cyber"}}`,
		``,
		`data: {"type":"response.failed","response":{"id":"resp_translated_cyber","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":13,"output_tokens":1,"total_tokens":14}}}`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

	_, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-5.4", "gpt-5.4")

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.IsOpenAIRefusalRecovery())
	require.Empty(t, recorder.Body.String())
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, 13, mark.UpstreamInTok)
}

func TestOpenAIStreamingTranslatedResponseRewritesRefusalAcrossDeltas(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	upstream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_translated_stream","object":"response","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		``,
		`data: {"type":"response.output_text.delta","response_id":"resp_translated_stream","item_id":"msg_1","output_index":0,"content_index":0,"delta":"I'm un"}`,
		``,
		`data: {"type":"response.output_text.delta","response_id":"resp_translated_stream","item_id":"msg_1","output_index":0,"content_index":0,"delta":"able to help."}`,
		``,
		`data: {"type":"response.output_text.done","response_id":"resp_translated_stream","item_id":"msg_1","output_index":0,"content_index":0,"text":"I'm unable to help."}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_translated_stream","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I'm unable to help."}]}],"usage":{"input_tokens":6,"output_tokens":4,"total_tokens":10}}}`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-5.4", "gpt-5.4")

	require.NoError(t, err)
	require.Equal(t, 6, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I'm unable")
	require.Contains(t, recorder.Body.String(), `"id":"resp_translated_stream"`)
	require.Contains(t, recorder.Body.String(), `"total_tokens":10`)
}

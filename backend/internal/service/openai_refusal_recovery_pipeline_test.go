package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIRefusalFlushRecorder struct {
	mu      sync.Mutex
	header  http.Header
	body    bytes.Buffer
	flushed chan struct{}
}

func newOpenAIRefusalFlushRecorder() *openAIRefusalFlushRecorder {
	return &openAIRefusalFlushRecorder{
		header:  make(http.Header),
		flushed: make(chan struct{}, 8),
	}
}

func (r *openAIRefusalFlushRecorder) Header() http.Header {
	return r.header
}

func (r *openAIRefusalFlushRecorder) WriteHeader(_ int) {}

func (r *openAIRefusalFlushRecorder) Write(payload []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(payload)
}

func (r *openAIRefusalFlushRecorder) Flush() {
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}

func (r *openAIRefusalFlushRecorder) BodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func newOpenAIRefusalRecoveryPipelineService(t *testing.T, cyber, rewrite bool) *OpenAIGatewayService {
	t.Helper()
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot", "I'm unable", "不能"}, "继续当前任务")
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

func TestOpenAINonStreamingPassthroughRewritesStructuredRefusal(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_structured","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"I cannot help with that."}]}],"usage":{"input_tokens":9,"output_tokens":6,"total_tokens":15}}`,
		)),
	}

	result, err := svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, "gpt-5.4", "")

	require.NoError(t, err)
	require.Equal(t, "resp_structured", result.responseID)
	require.Equal(t, 9, result.usage.InputTokens)
	require.Equal(t, 6, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I cannot")
	require.NotContains(t, recorder.Body.String(), `"type":"refusal"`)
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

func TestOpenAIHTTPPassthroughCyberPolicyWritesReplacementWhenRewriteEnabled(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, true, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	requestBody := []byte(`{"model":"gpt-5.6-sol","input":"hello","stream":true}`)
	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"X-Request-Id": []string{"req_cyber"}}}
	body := []byte(`{"error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":7,"output_tokens":0,"total_tokens":7}}`)

	err := svc.handleErrorResponsePassthrough(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, requestBody, body)

	require.Error(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, IsResponseCommitted(c))
	stream := recorder.Body.String()
	require.Contains(t, stream, "继续当前任务")
	require.Contains(t, stream, `"type":"response.completed"`)
	require.Contains(t, stream, `"total_tokens":7`)
	require.NotContains(t, stream, "response.failed")
	require.NotContains(t, stream, "cyber_policy")
	require.NotContains(t, stream, "blocked")
}

func TestOpenAIHTTPCyberPolicyWritesReplacementWhenRewriteEnabled(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, true, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	requestBody := []byte(`{"model":"gpt-5.6-sol","input":"hello","stream":true}`)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"X-Request-Id": []string{"req_cyber"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":7,"output_tokens":0,"total_tokens":7}}`)),
	}

	result, err := svc.handleErrorResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, requestBody)

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, IsResponseCommitted(c))
	stream := recorder.Body.String()
	require.Contains(t, stream, "继续当前任务")
	require.Contains(t, stream, `"type":"response.completed"`)
	require.Contains(t, stream, `"total_tokens":7`)
	require.NotContains(t, stream, "response.failed")
	require.NotContains(t, stream, "cyber_policy")
	require.NotContains(t, stream, "blocked")
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

func TestOpenAIStreamingCyberPolicyReplacementCompletesWithoutFailover(t *testing.T) {
	tests := []struct {
		name string
		run  func(*OpenAIGatewayService, context.Context, *http.Response, *gin.Context, *Account) (*OpenAIUsage, error)
	}{
		{
			name: "translated",
			run: func(svc *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*OpenAIUsage, error) {
				result, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				if result == nil {
					return nil, err
				}
				return result.usage, err
			},
		},
		{
			name: "passthrough",
			run: func(svc *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*OpenAIUsage, error) {
				result, err := svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				if result == nil {
					return nil, err
				}
				return result.usage, err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newOpenAIRefusalRecoveryPipelineService(t, true, true)
			c, recorder := newOpenAIRefusalRecoveryTestContext()
			account := &Account{ID: 1, Platform: PlatformOpenAI}
			setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(`{"model":"gpt-5.6-sol","input":"hello","stream":true}`))
			upstream := strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp_cyber_replaced","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`,
				``,
				`data: {"type":"response.failed","response":{"id":"resp_cyber_replaced","status":"failed","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":11,"output_tokens":0,"total_tokens":11}}}`,
				``,
			}, "\n")
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

			usage, err := tc.run(svc, context.Background(), resp, c, account)

			require.NoError(t, err)
			require.NotNil(t, usage)
			require.Equal(t, 11, usage.InputTokens)
			body := recorder.Body.String()
			require.Contains(t, body, "继续当前任务")
			require.Contains(t, body, `"type":"response.completed"`)
			require.NotContains(t, body, "response.failed")
			require.NotContains(t, body, "cyber_policy")
			require.NotContains(t, body, "blocked")
			mark := GetOpsCyberPolicy(c)
			require.NotNil(t, mark)
			require.Equal(t, 11, mark.UpstreamInTok)
		})
	}
}

func TestOpenAIStreamingCyberPolicyReplacementAfterReasoningUsesMessageID(t *testing.T) {
	tests := []struct {
		name string
		run  func(*OpenAIGatewayService, context.Context, *http.Response, *gin.Context, *Account) (*OpenAIUsage, error)
	}{
		{
			name: "translated",
			run: func(svc *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*OpenAIUsage, error) {
				result, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				if result == nil {
					return nil, err
				}
				return result.usage, err
			},
		},
		{
			name: "passthrough",
			run: func(svc *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*OpenAIUsage, error) {
				result, err := svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				if result == nil {
					return nil, err
				}
				return result.usage, err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newOpenAIRefusalRecoveryPipelineService(t, true, true)
			c, recorder := newOpenAIRefusalRecoveryTestContext()
			account := &Account{ID: 1, Platform: PlatformOpenAI}
			setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(
				`{"model":"gpt-5.6-sol","input":"hello","stream":true,"tools":[{"type":"function","name":"test_tool"}]}`,
			))
			require.False(t, openAIRefusalEarlyStreamEligible(c))
			upstream := strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp_cyber_reasoning_pipeline","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`,
				``,
				`data: {"type":"response.output_item.added","response_id":"resp_cyber_reasoning_pipeline","output_index":0,"item":{"id":"rs_cyber_reasoning_pipeline","type":"reasoning","status":"in_progress","summary":[]}}`,
				``,
				`data: {"type":"response.reasoning_summary_text.delta","response_id":"resp_cyber_reasoning_pipeline","item_id":"rs_cyber_reasoning_pipeline","output_index":0,"summary_index":0,"delta":"Reasoning summary"}`,
				``,
				`data: {"type":"response.failed","response":{"id":"resp_cyber_reasoning_pipeline","status":"failed","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":11,"output_tokens":1,"total_tokens":12}}}`,
				``,
			}, "\n")
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(upstream)),
			}

			usage, err := tc.run(svc, context.Background(), resp, c, account)

			require.NoError(t, err)
			require.NotNil(t, usage)
			require.Equal(t, 11, usage.InputTokens)
			body := recorder.Body.String()
			require.Contains(t, body, "继续当前任务")
			require.Contains(t, body, `"id":"msg_refusal_recovery_`)
			require.NotContains(t, body, `"id":"rs_cyber_reasoning_pipeline"`)
			require.NotContains(t, body, `"item_id":"rs_cyber_reasoning_pipeline"`)
			require.NotContains(t, body, "response.failed")
			require.NotContains(t, body, "cyber_policy")
		})
	}
}

func TestOpenAIStreamingCyberPolicyReplacementCompletesEarlyRewrite(t *testing.T) {
	tests := []struct {
		name string
		run  func(*OpenAIGatewayService, context.Context, *http.Response, *gin.Context, *Account) (*OpenAIUsage, error)
	}{
		{
			name: "translated",
			run: func(svc *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*OpenAIUsage, error) {
				result, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				if result == nil {
					return nil, err
				}
				return result.usage, err
			},
		},
		{
			name: "passthrough",
			run: func(svc *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*OpenAIUsage, error) {
				result, err := svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				if result == nil {
					return nil, err
				}
				return result.usage, err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newOpenAIRefusalRecoveryPipelineService(t, true, true)
			c, recorder := newOpenAIRefusalRecoveryTestContext()
			account := &Account{ID: 1, Platform: PlatformOpenAI}
			setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(`{"model":"gpt-5.6-sol","input":"hello","stream":true}`))
			upstream := strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp_early_cyber_replaced","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`,
				``,
				`data: {"type":"response.output_text.delta","response_id":"resp_early_cyber_replaced","item_id":"msg_early_cyber_replaced","output_index":0,"content_index":0,"delta":"I cannot help."}`,
				``,
				`data: {"type":"response.failed","response":{"id":"resp_early_cyber_replaced","status":"failed","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":12,"output_tokens":1,"total_tokens":13}}}`,
				``,
			}, "\n")
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

			usage, err := tc.run(svc, context.Background(), resp, c, account)

			require.NoError(t, err)
			require.NotNil(t, usage)
			require.Equal(t, 12, usage.InputTokens)
			body := recorder.Body.String()
			require.Contains(t, body, "继续当前任务")
			require.Contains(t, body, `"type":"response.completed"`)
			require.NotContains(t, body, "I cannot")
			require.NotContains(t, body, "response.failed")
			require.NotContains(t, body, "cyber_policy")
			require.NotContains(t, body, "blocked")
		})
	}
}

func TestOpenAIStreamingPassthroughRewritesRefusalAcrossDeltas(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	account := &Account{ID: 1, Platform: PlatformOpenAI}
	setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`))
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

	result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "", "")

	require.NoError(t, err)
	require.Equal(t, 4, result.usage.InputTokens)
	require.Equal(t, 3, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I cannot")
	require.Contains(t, recorder.Body.String(), `"id":"resp_stream"`)
	require.Contains(t, recorder.Body.String(), `"total_tokens":7`)
}

func TestOpenAIStreamingPassthroughRewritesNonEarlyEmptyTerminalFromCompletedMessage(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	account := &Account{ID: 1, Platform: PlatformOpenAI}
	setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(
		`{"model":"gpt-5.6-sol","input":"test","stream":true,"tools":[{"type":"function","name":"test_tool"}]}`,
	))
	require.False(t, openAIRefusalEarlyStreamEligible(c))

	upstream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_non_early_pipeline","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`,
		``,
		`data: {"type":"response.output_item.added","response_id":"resp_non_early_pipeline","output_index":0,"item":{"id":"msg_non_early_pipeline","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		``,
		`data: {"type":"response.output_text.delta","response_id":"resp_non_early_pipeline","item_id":"msg_non_early_pipeline","output_index":0,"content_index":0,"delta":"不能完成测试请求。"}`,
		``,
		`data: {"type":"response.output_text.done","response_id":"resp_non_early_pipeline","item_id":"msg_non_early_pipeline","output_index":0,"content_index":0,"text":"不能完成测试请求。"}`,
		``,
		`data: {"type":"response.output_item.done","response_id":"resp_non_early_pipeline","output_index":0,"item":{"id":"msg_non_early_pipeline","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"不能完成测试请求。"}]}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_non_early_pipeline","object":"response","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":3,"total_tokens":11}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstream)),
	}

	result, err := svc.handleStreamingResponsePassthrough(
		context.Background(),
		resp,
		c,
		account,
		time.Now(),
		"gpt-5.6-sol",
		"gpt-5.6-sol",
	)

	require.NoError(t, err)
	require.Equal(t, 8, result.usage.InputTokens)
	require.Equal(t, 3, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.Contains(t, recorder.Body.String(), `"id":"resp_non_early_pipeline"`)
	require.Contains(t, recorder.Body.String(), `"total_tokens":11`)
	require.NotContains(t, recorder.Body.String(), "不能完成测试请求")
}

func TestOpenAIStreamingPassthroughRewritesNonEarlyEmptyTerminalAfterReasoning(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	account := &Account{ID: 1, Platform: PlatformOpenAI}
	setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(
		`{"model":"gpt-5.6-sol","input":"test","stream":true,"tools":[{"type":"function","name":"test_tool"}]}`,
	))
	require.False(t, openAIRefusalEarlyStreamEligible(c))

	upstream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_reasoning_pipeline","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`,
		``,
		`data: {"type":"response.output_item.added","response_id":"resp_reasoning_pipeline","output_index":0,"item":{"id":"rs_reasoning_pipeline","type":"reasoning","status":"in_progress","summary":[]}}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","response_id":"resp_reasoning_pipeline","item_id":"rs_reasoning_pipeline","output_index":0,"summary_index":0,"delta":"Reasoning summary"}`,
		``,
		`data: {"type":"response.output_item.done","response_id":"resp_reasoning_pipeline","output_index":0,"item":{"id":"rs_reasoning_pipeline","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"Reasoning summary"}]}}`,
		``,
		`data: {"type":"response.output_item.added","response_id":"resp_reasoning_pipeline","output_index":1,"item":{"id":"msg_reasoning_pipeline","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		``,
		`data: {"type":"response.output_text.delta","response_id":"resp_reasoning_pipeline","item_id":"msg_reasoning_pipeline","output_index":1,"content_index":0,"delta":"不能完成测试请求。"}`,
		``,
		`data: {"type":"response.output_text.done","response_id":"resp_reasoning_pipeline","item_id":"msg_reasoning_pipeline","output_index":1,"content_index":0,"text":"不能完成测试请求。"}`,
		``,
		`data: {"type":"response.output_item.done","response_id":"resp_reasoning_pipeline","output_index":1,"item":{"id":"msg_reasoning_pipeline","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"不能完成测试请求。"}]}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_reasoning_pipeline","object":"response","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":4,"total_tokens":12}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstream)),
	}

	result, err := svc.handleStreamingResponsePassthrough(
		context.Background(),
		resp,
		c,
		account,
		time.Now(),
		"gpt-5.6-sol",
		"gpt-5.6-sol",
	)

	require.NoError(t, err)
	require.Equal(t, 8, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.Contains(t, recorder.Body.String(), `"id":"resp_reasoning_pipeline"`)
	require.Contains(t, recorder.Body.String(), `"total_tokens":12`)
	require.NotContains(t, recorder.Body.String(), "不能完成测试请求")
}

func TestOpenAIStreamingPassthroughRewritesStructuredRefusalAcrossDeltas(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	upstream := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_structured_stream","object":"response","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		``,
		`event: response.refusal.delta`,
		`data: {"type":"response.refusal.delta","response_id":"resp_structured_stream","item_id":"msg_1","output_index":0,"content_index":0,"delta":"I ca"}`,
		``,
		`event: response.refusal.delta`,
		`data: {"type":"response.refusal.delta","response_id":"resp_structured_stream","item_id":"msg_1","output_index":0,"content_index":0,"delta":"nnot help."}`,
		``,
		`event: response.refusal.done`,
		`data: {"type":"response.refusal.done","response_id":"resp_structured_stream","item_id":"msg_1","output_index":0,"content_index":0,"refusal":"I cannot help."}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_structured_stream","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"I cannot help."}]}],"usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}}`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

	result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "", "")

	require.NoError(t, err)
	require.Equal(t, 4, result.usage.InputTokens)
	require.Equal(t, 3, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I cannot")
	require.NotContains(t, recorder.Body.String(), "response.refusal")
	require.Contains(t, recorder.Body.String(), `"id":"resp_structured_stream"`)
	require.Contains(t, recorder.Body.String(), `"total_tokens":7`)
}

func TestOpenAIStreamingPassthroughRewritesRefusalInSecondParagraph(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	text := "可以协助分析你自有或明确授权的应用，例如会员鉴权安全测试、逆向协议、漏洞复现和修复建议。\n\n但不能帮助绕过第三方付费会员、伪造订阅状态或破解授权。若是授权测试，请提供 APK/安装包、源码或测试环境，以及具体测试目标。"
	upstream := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_second_paragraph_stream","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`,
		``,
		`event: response.refusal.delta`,
		`data: {"type":"response.refusal.delta","response_id":"resp_second_paragraph_stream","item_id":"msg_1","output_index":0,"content_index":0,"delta":"可以协助分析你自有或明确授权的应用，例如会员鉴权安全测试、逆向协议、漏洞复现和修复建议。\n\n"}`,
		``,
		`event: response.refusal.delta`,
		`data: {"type":"response.refusal.delta","response_id":"resp_second_paragraph_stream","item_id":"msg_1","output_index":0,"content_index":0,"delta":"但不能帮助绕过第三方付费会员、伪造订阅状态或破解授权。若是授权测试，请提供 APK/安装包、源码或测试环境，以及具体测试目标。"}`,
		``,
		`event: response.refusal.done`,
		`data: {"type":"response.refusal.done","response_id":"resp_second_paragraph_stream","item_id":"msg_1","output_index":0,"content_index":0,"refusal":` + fmt.Sprintf("%q", text) + `}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_second_paragraph_stream","object":"response","model":"gpt-5.6-sol","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":` + fmt.Sprintf("%q", text) + `}]}],"usage":{"input_tokens":12,"output_tokens":42,"total_tokens":54}}}`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

	result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "", "")

	require.NoError(t, err)
	require.Equal(t, 12, result.usage.InputTokens)
	require.Equal(t, 42, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "伪造订阅状态")
	require.NotContains(t, recorder.Body.String(), "response.refusal")
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
	account := &Account{ID: 1, Platform: PlatformOpenAI}
	setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`))
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

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.4", "gpt-5.4")

	require.NoError(t, err)
	require.Equal(t, 6, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I'm unable")
	require.Contains(t, recorder.Body.String(), `"id":"resp_translated_stream"`)
	require.Contains(t, recorder.Body.String(), `"total_tokens":10`)
}

func TestOpenAIStreamingTranslatedEarlyRewriteErrorStillCollectsTerminalUsage(t *testing.T) {
	svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
	c, recorder := newOpenAIRefusalRecoveryTestContext()
	account := &Account{ID: 1, Platform: PlatformOpenAI}
	setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`))
	upstream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_mismatch","object":"response","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		``,
		`data: {"type":"response.output_text.delta","response_id":"resp_mismatch","item_id":"msg_early","output_index":0,"content_index":0,"delta":"I cannot help."}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_mismatch","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_terminal","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help."}]}],"usage":{"input_tokens":9,"output_tokens":4,"total_tokens":13}}}`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstream))}

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.4", "gpt-5.4")

	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "继续当前任务")
	require.NotContains(t, recorder.Body.String(), "I cannot")
}

func TestOpenAIStreamingHandlersFlushEarlyReplacementBeforeTerminal(t *testing.T) {
	type handlerResult struct {
		usage *OpenAIUsage
		err   error
	}
	tests := []struct {
		name string
		run  func(*OpenAIGatewayService, context.Context, *http.Response, *gin.Context, *Account) (*OpenAIUsage, error)
	}{
		{
			name: "translated",
			run: func(svc *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*OpenAIUsage, error) {
				result, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-5.4", "gpt-5.4")
				if result == nil {
					return nil, err
				}
				return result.usage, err
			},
		},
		{
			name: "passthrough",
			run: func(svc *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) (*OpenAIUsage, error) {
				result, err := svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-5.4", "gpt-5.4")
				if result == nil {
					return nil, err
				}
				return result.usage, err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newOpenAIRefusalRecoveryPipelineService(t, false, true)
			recorder := newOpenAIRefusalFlushRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			account := &Account{ID: 1, Platform: PlatformOpenAI}
			setOpenAIRefusalEarlyStreamEligibility(c, account, []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`))
			reader, writer := io.Pipe()
			t.Cleanup(func() {
				_ = writer.Close()
				_ = reader.Close()
			})
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}
			resultCh := make(chan handlerResult, 1)
			go func() {
				usage, err := tc.run(svc, context.Background(), resp, c, account)
				resultCh <- handlerResult{usage: usage, err: err}
			}()

			prefix := strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp_flush","object":"response","model":"gpt-5.4","status":"in_progress","output":[]}}`,
				``,
				`data: {"type":"response.output_text.delta","response_id":"resp_flush","item_id":"msg_flush","output_index":0,"content_index":0,"delta":"I cannot help."}`,
				``,
			}, "\n")
			_, err := io.WriteString(writer, prefix)
			require.NoError(t, err)

			select {
			case <-recorder.flushed:
			case result := <-resultCh:
				t.Fatalf("handler returned before terminal event: %v", result.err)
			case <-time.After(5 * time.Second):
				t.Fatal("replacement was not flushed before terminal event")
			}
			earlyBody := recorder.BodyString()
			require.Contains(t, earlyBody, "继续当前任务")
			require.Contains(t, earlyBody, `"type":"response.output_text.delta"`)
			require.NotContains(t, earlyBody, "response.completed")
			require.NotContains(t, earlyBody, "I cannot")

			terminal := strings.Join([]string{
				`data: {"type":"response.output_text.done","response_id":"resp_flush","item_id":"msg_flush","output_index":0,"content_index":0,"text":"I cannot help."}`,
				``,
				`data: {"type":"response.output_item.done","response_id":"resp_flush","output_index":0,"item":{"id":"msg_flush","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help."}]}}`,
				``,
				`data: {"type":"response.completed","response":{"id":"resp_flush","object":"response","model":"gpt-5.4","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
				``,
			}, "\n")
			_, err = io.WriteString(writer, terminal)
			require.NoError(t, err)
			require.NoError(t, writer.Close())

			select {
			case result := <-resultCh:
				require.NoError(t, result.err)
				require.NotNil(t, result.usage)
				require.Equal(t, 10, result.usage.InputTokens)
				require.Equal(t, 5, result.usage.OutputTokens)
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not finish after terminal event")
			}
			finalBody := recorder.BodyString()
			require.Contains(t, finalBody, `"type":"response.completed"`)
			require.Contains(t, finalBody, `"total_tokens":15`)
			require.NotContains(t, finalBody, "I cannot")
		})
	}
}

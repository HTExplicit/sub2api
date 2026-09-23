package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexQualityObservationClassifiesStreamFailures(t *testing.T) {
	for _, test := range []struct{ name, payload, code string }{
		{"capacity", `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"private upstream text"}}}`, "routing_capacity"},
		{"policy", `{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"private upstream text"}}}`, "routing_policy"},
		{"incomplete", `{"type":"response.incomplete","response":{"model":"gpt-6-astra","status":"incomplete"}}`, "routing_incomplete"},
		{"cancelled", `{"type":"response.cancelled","response":{"status":"cancelled"}}`, "routing_cancelled"},
		{"mismatch", `{"type":"response.completed","response":{"model":"gpt-6-luna","status":"completed"}}`, "routing_model_mismatch"},
		{"no_prose_inference", `{"type":"error","error":{"code":"other","message":"server_is_overloaded private upstream text"}}`, "routing_upstream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var observation extensionv1.CodexRoutingObservation
			err := readCodexRoutingCompletion(strings.NewReader("data: "+test.payload+"\n\n"), "text/event-stream", "gpt-6-astra", &observation)
			require.Error(t, err)
			require.Equal(t, test.code, observation.Code)
			encoded, err := json.Marshal(observation)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private upstream text")
		})
	}
	var cancelled extensionv1.CodexRoutingObservation
	require.Error(t, readCodexRoutingCompletion(passthroughErrReadCloser{err: context.Canceled}, "text/event-stream", "gpt-6-astra", &cancelled))
	require.Equal(t, "routing_cancelled", cancelled.Code)
}

func TestCodexQualityBusinessObservationUsesExistingLimits(t *testing.T) {
	frame := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"output\":[{\"text\":\"" + strings.Repeat("x", 180<<10) + "\"}]}}\r\n\r\n"
	for _, limit := range []int{256 << 10, 64 << 10} {
		cfg := &config.Config{}
		cfg.Gateway.MaxLineSize = limit
		svc := &OpenAIGatewayService{cfg: cfg}
		response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(frame))}
		body := svc.newCodexRoutingObservedBody(httptest.NewRequest(http.MethodPost, "/", nil), response, "gpt-6-astra")
		var completed codexRoutingCompletion
		body.finish = func(value codexRoutingCompletion) { completed = value }
		_, err := io.Copy(io.Discard, body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		require.Equal(t, limit > len(frame), completed.Completed && !completed.Failed)
	}
	var probe extensionv1.CodexRoutingObservation
	require.Error(t, readCodexRoutingCompletion(strings.NewReader(frame), "text/event-stream", "gpt-6-astra", &probe), "lightweight probes retain their independent total budget")
	require.Equal(t, "routing_incomplete", probe.Code)
}

func TestCodexQualityCancellationObservesClientWithoutCancellingDrain(t *testing.T) {
	client, cancel := context.WithCancel(t.Context())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(client)
	wire := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithoutCancel(client))
	wire = withCodexRoutingDownstreamContext(wire, c)
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}\n\n"))}
	body := (&OpenAIGatewayService{cfg: &config.Config{}}).newCodexRoutingObservedBody(wire, response, "gpt-6-astra")
	var completed codexRoutingCompletion
	body.finish = func(value codexRoutingCompletion) { completed = value }
	cancel()
	require.NoError(t, wire.Context().Err(), "cancellation evidence must not stop the existing usage drain")
	_, err := io.Copy(io.Discard, body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, "routing_cancelled", completed.observationCode("gpt-6-astra", http.StatusOK))
}

func TestCodexQualityMismatchStopsBeforeFirstOutput(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	for _, contentType := range []string{"text/event-stream", "application/json"} {
		payload := `{"type":"response.created","response":{"model":"gpt-6-luna","status":"in_progress"}}`
		if contentType == "text/event-stream" {
			payload = "data: " + payload + "\n\n"
		} else {
			payload = `{"object":"response","model":"gpt-6-luna","status":"completed"}`
		}
		response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(payload))}
		body := svc.newCodexRoutingObservedBody(httptest.NewRequest(http.MethodPost, "/", nil), response, "gpt-6-astra")
		var completed codexRoutingCompletion
		body.finish = func(value codexRoutingCompletion) { completed = value }
		data, err := io.ReadAll(body)
		require.ErrorIs(t, err, ErrCodexRoutingModelMismatch)
		if contentType == "text/event-stream" {
			require.Empty(t, data)
		}
		require.Equal(t, "routing_model_mismatch", completed.observationCode("gpt-6-astra", http.StatusOK))
		require.NoError(t, body.Close())
	}
	// The official header is independently authoritative, even when a later
	// body would have echoed the requested name. No response bytes escape.
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Openai-Model": []string{"gpt-6-luna"}}, Body: io.NopCloser(strings.NewReader("unread"))}
	body := svc.newCodexRoutingObservedBody(httptest.NewRequest(http.MethodPost, "/", nil), response, "gpt-6-astra")
	data, err := io.ReadAll(body)
	require.ErrorIs(t, err, ErrCodexRoutingModelMismatch)
	require.Empty(t, data)
	require.NoError(t, body.Close())
}

type codexQualitySignalWriter struct {
	gin.ResponseWriter
	written chan struct{}
	once    sync.Once
}

func (w *codexQualitySignalWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err == nil && n > 0 {
		w.once.Do(func() { close(w.written) })
	}
	return n, err
}

type codexQualityLaterMismatchReader struct {
	first *strings.Reader
	later *strings.Reader
	gate  <-chan struct{}
	ctx   context.Context
}

func (r *codexQualityLaterMismatchReader) Read(data []byte) (int, error) {
	if r.first.Len() > 0 {
		return r.first.Read(data)
	}
	select {
	case <-r.gate:
		return r.later.Read(data)
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}
func (r *codexQualityLaterMismatchReader) Close() error { return nil }

func TestCodexQualityMismatchAfterOutputDoesNotReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	gate := make(chan struct{})
	c.Writer = &codexQualitySignalWriter{ResponseWriter: c.Writer, written: gate}
	reader := &codexQualityLaterMismatchReader{
		first: strings.NewReader("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_quality\",\"model\":\"gpt-6-astra\",\"status\":\"in_progress\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"first-visible\"}\n\n"),
		later: strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-luna\",\"status\":\"completed\"}}\n\n"),
		gate:  gate, ctx: ctx,
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}
	response.Body = svc.newCodexRoutingObservedBody(c.Request, response, "gpt-6-astra")
	_, err := svc.handleStreamingResponse(ctx, response, c, codexQualityAccount(), time.Now(), "gpt-6-astra", "gpt-6-astra")
	require.ErrorIs(t, err, ErrCodexRoutingModelMismatch)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Contains(t, recorder.Body.String(), "first-visible")
	require.NotContains(t, recorder.Body.String(), "gpt-6-luna")
}

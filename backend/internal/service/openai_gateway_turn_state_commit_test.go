package service

import (
	"context"
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

func newOpenAITurnStateCommitContext(t *testing.T, writer gin.ResponseWriter) (*gin.Context, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if writer == nil {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		writer = c.Writer
	}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session_id", "sess-http-commit")
	c.Set("api_key", &APIKey{ID: 801})
	return c, openAICodexTurnStateSeed(c)
}

func seedOpenAITurnStateOrigin(svc *OpenAIGatewayService, seed string, accountID int64) {
	svc.openaiCodexTurnStateOrigins.Store(seed, openAICodexTurnStateOrigin{
		accountID: accountID,
		expiresAt: time.Now().Add(time.Minute),
	})
}

func requireOpenAITurnStateOrigin(t *testing.T, svc *OpenAIGatewayService, seed string, accountID int64) {
	t.Helper()
	raw, ok := svc.openaiCodexTurnStateOrigins.Load(seed)
	require.True(t, ok)
	origin, ok := raw.(openAICodexTurnStateOrigin)
	require.True(t, ok)
	require.Equal(t, accountID, origin.accountID)
}

func openAITurnStateStreamingResponse(state string) *http.Response {
	header := http.Header{"Content-Type": []string{"text/event-stream"}}
	if state != "" {
		header.Set(openAICodexTurnStateHeader, state)
	}
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_commit","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		"",
	}, "\n")
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func openAITurnStateJSONResponse(state string) *http.Response {
	header := http.Header{"Content-Type": []string{"application/json"}}
	if state != "" {
		header.Set(openAICodexTurnStateHeader, state)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_commit","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}
}

func TestOpenAIStreamingTurnStateCommitsOnlyAfterDownstreamWrite(t *testing.T) {
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{},
		toolCorrector: NewCodexToolCorrector(),
	}
	accountB := &Account{ID: 202, Platform: PlatformOpenAI}

	failedRecorder := httptest.NewRecorder()
	failedBase, _ := gin.CreateTestContext(failedRecorder)
	failedWriter := &failingGinWriter{ResponseWriter: failedBase.Writer, failAfter: 0}
	failedContext, seed := newOpenAITurnStateCommitContext(t, failedWriter)
	seedOpenAITurnStateOrigin(svc, seed, 101)

	_, err := svc.handleStreamingResponse(
		context.Background(), openAITurnStateStreamingResponse("state-B"), failedContext,
		accountB, time.Now(), "gpt-5.6-luna", "gpt-5.6-luna",
	)
	require.NoError(t, err, "the stream still drains the upstream after a client disconnect")
	requireOpenAITurnStateOrigin(t, svc, seed, 101)

	successContext, successSeed := newOpenAITurnStateCommitContext(t, nil)
	require.Equal(t, seed, successSeed)
	_, err = svc.handleStreamingResponse(
		context.Background(), openAITurnStateStreamingResponse("state-B"), successContext,
		accountB, time.Now(), "gpt-5.6-luna", "gpt-5.6-luna",
	)
	require.NoError(t, err)
	require.Equal(t, "state-B", successContext.Writer.Header().Get(openAICodexTurnStateHeader))
	requireOpenAITurnStateOrigin(t, svc, seed, accountB.ID)

	noStateContext, noStateSeed := newOpenAITurnStateCommitContext(t, nil)
	require.Equal(t, seed, noStateSeed)
	_, err = svc.handleStreamingResponse(
		context.Background(), openAITurnStateStreamingResponse(""), noStateContext,
		accountB, time.Now(), "gpt-5.6-luna", "gpt-5.6-luna",
	)
	require.NoError(t, err)
	require.Empty(t, noStateContext.Writer.Header().Get(openAICodexTurnStateHeader))
	_, ok := svc.openaiCodexTurnStateOrigins.Load(seed)
	require.False(t, ok)
}

func TestOpenAINonStreamingTurnStateCommitsOnlyAfterDownstreamWrite(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	accountB := &Account{ID: 302, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	failedRecorder := httptest.NewRecorder()
	failedBase, _ := gin.CreateTestContext(failedRecorder)
	failedWriter := &failingGinWriter{ResponseWriter: failedBase.Writer, failAfter: 0}
	failedContext, seed := newOpenAITurnStateCommitContext(t, failedWriter)
	seedOpenAITurnStateOrigin(svc, seed, 301)

	_, err := svc.handleNonStreamingResponse(
		context.Background(), openAITurnStateJSONResponse("state-B"), failedContext,
		accountB, "gpt-5.6-luna", "gpt-5.6-luna",
	)
	require.ErrorContains(t, err, "write downstream OpenAI response")
	requireOpenAITurnStateOrigin(t, svc, seed, 301)

	successContext, successSeed := newOpenAITurnStateCommitContext(t, nil)
	require.Equal(t, seed, successSeed)
	_, err = svc.handleNonStreamingResponse(
		context.Background(), openAITurnStateJSONResponse("state-B"), successContext,
		accountB, "gpt-5.6-luna", "gpt-5.6-luna",
	)
	require.NoError(t, err)
	require.Equal(t, "state-B", successContext.Writer.Header().Get(openAICodexTurnStateHeader))
	requireOpenAITurnStateOrigin(t, svc, seed, accountB.ID)

	noStateContext, noStateSeed := newOpenAITurnStateCommitContext(t, nil)
	require.Equal(t, seed, noStateSeed)
	_, err = svc.handleNonStreamingResponse(
		context.Background(), openAITurnStateJSONResponse(""), noStateContext,
		accountB, "gpt-5.6-luna", "gpt-5.6-luna",
	)
	require.NoError(t, err)
	require.Empty(t, noStateContext.Writer.Header().Get(openAICodexTurnStateHeader))
	_, ok := svc.openaiCodexTurnStateOrigins.Load(seed)
	require.False(t, ok)
}

func TestOpenAIPassthroughTurnStateUsesTheSameDownstreamCommitBoundary(t *testing.T) {
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{},
		toolCorrector: NewCodexToolCorrector(),
	}
	accountB := &Account{ID: 402, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	t.Run("streaming write failure keeps the previous owner", func(t *testing.T) {
		failedRecorder := httptest.NewRecorder()
		failedBase, _ := gin.CreateTestContext(failedRecorder)
		failedWriter := &failingGinWriter{ResponseWriter: failedBase.Writer, failAfter: 0}
		failedContext, seed := newOpenAITurnStateCommitContext(t, failedWriter)
		seedOpenAITurnStateOrigin(svc, seed, 401)

		_, err := svc.handleStreamingResponsePassthrough(
			context.Background(), openAITurnStateStreamingResponse("state-B"), failedContext,
			accountB, time.Now(), "gpt-5.6-luna", "gpt-5.6-luna",
		)
		require.NoError(t, err)
		requireOpenAITurnStateOrigin(t, svc, seed, 401)

		successContext, successSeed := newOpenAITurnStateCommitContext(t, nil)
		require.Equal(t, seed, successSeed)
		_, err = svc.handleStreamingResponsePassthrough(
			context.Background(), openAITurnStateStreamingResponse("state-B"), successContext,
			accountB, time.Now(), "gpt-5.6-luna", "gpt-5.6-luna",
		)
		require.NoError(t, err)
		requireOpenAITurnStateOrigin(t, svc, seed, accountB.ID)
	})

	t.Run("unary write failure keeps the previous owner", func(t *testing.T) {
		failedRecorder := httptest.NewRecorder()
		failedBase, _ := gin.CreateTestContext(failedRecorder)
		failedWriter := &failingGinWriter{ResponseWriter: failedBase.Writer, failAfter: 0}
		failedContext, seed := newOpenAITurnStateCommitContext(t, failedWriter)
		seedOpenAITurnStateOrigin(svc, seed, 403)

		_, err := svc.handleNonStreamingResponsePassthrough(
			context.Background(), openAITurnStateJSONResponse("state-B"), failedContext,
			"gpt-5.6-luna", "gpt-5.6-luna", accountB,
		)
		require.ErrorContains(t, err, "write downstream OpenAI passthrough response")
		requireOpenAITurnStateOrigin(t, svc, seed, 403)

		successContext, successSeed := newOpenAITurnStateCommitContext(t, nil)
		require.Equal(t, seed, successSeed)
		_, err = svc.handleNonStreamingResponsePassthrough(
			context.Background(), openAITurnStateJSONResponse("state-B"), successContext,
			"gpt-5.6-luna", "gpt-5.6-luna", accountB,
		)
		require.NoError(t, err)
		requireOpenAITurnStateOrigin(t, svc, seed, accountB.ID)
	})
}

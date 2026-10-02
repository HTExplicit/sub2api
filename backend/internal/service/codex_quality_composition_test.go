package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type codexQualityPacketReader struct {
	*strings.Reader
	reads int
}

func (r *codexQualityPacketReader) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}
func (*codexQualityPacketReader) Close() error { return nil }

func TestCodexQualityComposedRawObservationPrecedesGuard(t *testing.T) {
	for _, headerMismatch := range []bool{false, true} {
		name := "created_and_event_header"
		if headerMismatch {
			name = "http_header_before_body"
		}
		t.Run(name, func(t *testing.T) {
			store, run := qualityStateFixture(t, 3)
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			rt := &codexQualityRuntime{s: svc, store: store, installation: &NativeCodexMetadata{}}
			group := run.GroupID
			key := &APIKey{ID: run.APIKeyID, UserID: run.ActorID, GroupID: &group, Status: StatusAPIKeyActive}
			e := &codexQualityExecution{runtime: rt, runID: run.RunID, grantDigest: run.GrantDigest, trialID: uuid.NewString(),
				accountID: run.AccountID, requestModel: run.Model, effort: run.ReasoningEffort,
				keyLookup: func(context.Context, int64) (*APIKey, error) { return key, nil },
			}
			ctx := context.WithValue(context.Background(), codexQualityExecutionKey{}, e)
			// The already-admitted synthetic send reserves once, exactly as the
			// ordinary send path does before installing both readers.
			require.NoError(t, reserveCodexQualityAttempt(rt.ctx(ctx), store, run.RunID, run.GrantDigest, e.attempt()))
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
			require.NoError(t, err)
			raw := "data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-6-luna\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"headers\":{\"X-OpenAI-Model\":\"gpt-6-luna\"},\"output\":[{\"text\":\"private response text\"}]}}\n\n"
			source := &codexQualityPacketReader{Reader: strings.NewReader(raw)}
			headerModel := run.Model
			if headerMismatch {
				headerModel = "gpt-6-luna"
			}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
				"Content-Type": []string{"text/event-stream"}, "openai-model": []string{headerModel},
			}, Body: source}
			svc.observeCodexQualityBusinessResponse(request, response, nil)
			forwarded, err := io.ReadAll(response.Body)
			require.ErrorIs(t, err, ErrCodexModelMismatch)
			require.Empty(t, forwarded, "no mismatched frame may reach the downstream consumer")
			require.NoError(t, response.Body.Close())
			saved, _, err := readCodexQualityRun(context.Background(), store, run.RunID)
			require.NoError(t, err)
			require.Equal(t, 1, saved.UsedSends)
			require.Len(t, saved.Attempts, 1)
			attempt := saved.Attempts[0]
			require.Contains(t, attempt.HeaderModels, headerModel)
			if headerMismatch {
				require.Zero(t, source.reads)
				require.Nil(t, attempt.CreatedModel, "an unread body is unknown")
				require.Nil(t, attempt.TerminalModel)
			} else {
				require.Equal(t, 1, source.reads)
				require.Equal(t, "gpt-6-luna", *attempt.CreatedModel)
				require.Equal(t, run.Model, *attempt.TerminalModel)
				require.Contains(t, attempt.HeaderModels, "gpt-6-luna")
				require.True(t, attempt.Completed, "raw upstream completion is distinct from the guard outcome")
			}
			encoded, err := json.Marshal(saved)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private response text")
			require.ErrorIs(t, reserveCodexQualityAttempt(rt.ctx(ctx), store, run.RunID, run.GrantDigest, e.attempt()), ErrCodexQualitySpent)
		})
	}
}

func TestCodexQualityComposedBusinessLimits(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	svc.cfg.Gateway.MaxLineSize = 256 << 10
	svc.cfg.Gateway.UpstreamResponseReadMaxBytes = 256 << 10
	request := httptest.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	terminal := `{"type":"response.completed","response":{"status":"completed","model":"gpt-6-astra","output":[{"text":"` + strings.Repeat("x", 180<<10) + `"}]}}`
	for _, contentType := range []string{"text/event-stream", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			raw := terminal
			if contentType == "text/event-stream" {
				raw = "data: " + terminal + "\r\n\r\n"
			}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(raw))}
			body := svc.newCodexQualityObservedBody(response, CodexQualityAttempt{Stage: codexQualityStage, HTTPStatus: http.StatusOK})
			var observed CodexQualityAttempt
			body.finish = func(value CodexQualityAttempt) { observed = value }
			response.Body = body
			reader := svc.newCodexModelGuardBody(request, response, "gpt-6-astra")
			forwarded, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			require.Equal(t, raw, string(forwarded))
			require.True(t, observed.Completed, "a 180 KiB frame stays within the ordinary business limits")
		})
	}
}

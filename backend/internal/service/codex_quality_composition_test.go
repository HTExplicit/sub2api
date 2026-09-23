package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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
			rt := &codexQualityRuntime{s: svc, store: store, installation: &PluginInstallation{PluginKey: codexRuntimePluginKey}}
			scope := run.Scope
			scope.ConnectionLeaseID = "fixture-private-connection"
			deadline := time.Now().Add(time.Minute)
			q := &extensionv1.CodexRoutingQualification{Scope: scope, Model: codexQualityModel,
				Bundle:     extensionv1.CodexRoutingBundleRef{Key: "bundle.quality.fixture", Revision: 1, ConnectionLeaseID: scope.ConnectionLeaseID, ExpiresAt: deadline},
				VerifiedAt: time.Now().Add(-time.Second), ExpiresAt: deadline,
			}
			_, err := mutateCodexQualityRun(context.Background(), store, run.RunID, func(current *codexQualityRun) error {
				current.Qualification = q
				return nil
			})
			require.NoError(t, err)
			group := run.GroupID
			key := &APIKey{ID: run.APIKeyID, UserID: run.ActorID, GroupID: &group, Status: StatusAPIKeyActive}
			e := &codexQualityExecution{runtime: rt, runID: run.RunID, grantDigest: run.GrantDigest, trialID: uuid.NewString(), stage: "business",
				accountID: run.AccountID, requestModel: codexQualityModel, effort: codexQualityEffort, qualification: q,
				keyLookup: func(context.Context, int64) (*APIKey, error) { return key, nil },
			}
			ctx := context.WithValue(context.Background(), codexQualityExecutionKey{}, e)
			// The already-admitted synthetic send reserves once, exactly as the
			// production private transport does before installing both readers.
			require.NoError(t, reserveCodexQualityAttempt(rt.ctx(ctx), store, run.RunID, run.GrantDigest, e.attempt()))
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
			require.NoError(t, err)
			raw := "data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-6-luna\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"headers\":{\"X-OpenAI-Model\":\"gpt-6-luna\"},\"output\":[{\"text\":\"private response text\"}]}}\n\n"
			source := &codexQualityPacketReader{Reader: strings.NewReader(raw)}
			headerModel := codexQualityModel
			if headerMismatch {
				headerModel = "gpt-6-luna"
			}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
				"Content-Type": []string{"text/event-stream"}, "openai-model": []string{headerModel},
			}, Body: source}
			svc.observeCodexQualityBusinessResponse(request, response, nil, q)
			forwarded, err := io.ReadAll(response.Body)
			require.ErrorIs(t, err, ErrCodexRoutingModelMismatch)
			require.Empty(t, forwarded, "no mismatched frame may reach the downstream consumer")
			require.NoError(t, response.Body.Close())
			saved, _, err := readCodexQualityRun(context.Background(), store, run.RunID)
			require.NoError(t, err)
			require.Equal(t, 1, saved.UsedSends)
			require.Len(t, saved.Attempts, 1)
			require.Nil(t, saved.Qualification, "a raw matching terminal must not preserve a route rejected for an earlier/header model")
			attempt := saved.Attempts[0]
			require.Contains(t, attempt.HeaderModels, headerModel)
			if headerMismatch {
				require.Zero(t, source.reads)
				require.Nil(t, attempt.CreatedModel, "an unread body is unknown")
				require.Nil(t, attempt.TerminalModel)
			} else {
				require.Equal(t, 1, source.reads)
				require.Equal(t, "gpt-6-luna", *attempt.CreatedModel)
				require.Equal(t, codexQualityModel, *attempt.TerminalModel)
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

func TestCodexQualityComposedHighProbeAndBusinessLimits(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	svc.cfg.Gateway.MaxLineSize = 256 << 10
	svc.cfg.Gateway.UpstreamResponseReadMaxBytes = 256 << 10
	run := codexQualityRun{RunID: uuid.NewString(), AccountID: 71}
	query := codexQualityRenewQuery(run, uuid.NewString())
	ctx := withCodexTransportFixture(context.Background(), false)
	request, err := svc.buildCodexRoutingProbe(ctx, codexQualityAccount(), query.Model, "fixture-token", "", query.ReasoningEffort)
	require.NoError(t, err)
	wire, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Equal(t, codexQualityEffort, gjson.GetBytes(wire, "reasoning.effort").String())
	require.Equal(t, "ping", gjson.GetBytes(wire, "input.0.content.0.text").String(), "renewal remains a protocol probe")
	require.NoError(t, request.Body.Close())

	terminal := `{"type":"response.completed","response":{"status":"completed","model":"gpt-6-astra","output":[{"text":"` + strings.Repeat("x", 180<<10) + `"}]}}`
	for _, test := range []struct{ stage, contentType string }{
		{"business", "text/event-stream"}, {"business", "application/json"},
		{"acquire", "text/event-stream"}, {"verify", "text/event-stream"},
	} {
		t.Run(test.stage+"/"+test.contentType, func(t *testing.T) {
			raw := terminal
			if test.contentType == "text/event-stream" {
				raw = "data: " + terminal + "\r\n\r\n"
			}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{test.contentType}}, Body: io.NopCloser(strings.NewReader(raw))}
			body := svc.newCodexQualityObservedBody(response, test.stage, CodexQualityAttempt{Stage: test.stage, HTTPStatus: http.StatusOK})
			var observed CodexQualityAttempt
			body.finish = func(value CodexQualityAttempt) { observed = value }
			var reader io.ReadCloser = body
			if test.stage == "business" {
				response.Body = body
				reader = svc.newCodexRoutingObservedBody(request, response, codexQualityModel)
			}
			forwarded, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			require.Equal(t, raw, string(forwarded))
			require.Equal(t, test.stage == "business", observed.Completed)
		})
	}
}

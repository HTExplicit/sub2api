//go:build unit

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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const qualityWireFixtureBody = `{"model":"gpt-6-sol","reasoning":{"effort":"xhigh"},"input":"question","stream":true,"store":false}`

// qualityWireUpstream is the account's ordinary transport (HTTPUpstream.Do);
// a send that needed any other transport would panic on the nil embedding.
type qualityWireUpstream struct {
	HTTPUpstream
	reply     string
	calls     int
	request   *http.Request
	body      []byte
	accountID int64
	proxyURL  string
}

func (u *qualityWireUpstream) Do(req *http.Request, proxyURL string, accountID int64, _ int) (*http.Response, error) {
	u.calls++
	u.request, u.accountID, u.proxyURL = req, accountID, proxyURL
	defer func() { _ = req.Body.Close() }()
	var err error
	u.body, err = io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	reply := u.reply
	if reply == "" {
		reply = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-sol\"}}\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(reply))}, nil
}

type qualityWireSource struct {
	reader   io.Reader
	readErr  error
	closeErr error
	read     int
	closed   int
}

func (s *qualityWireSource) Read(p []byte) (int, error) {
	n, err := s.reader.Read(p)
	s.read += n
	if err == io.EOF && s.readErr != nil {
		return n, s.readErr
	}
	return n, err
}

func (s *qualityWireSource) Close() error {
	s.closed++
	return s.closeErr
}

// Use the real ordinary send path and durable reservation boundary. Only the
// database and network ports are in-memory fixtures; no model call is possible.
func qualityWireServiceFixture(t *testing.T, compressed, recovery bool) (*OpenAIGatewayService, *Account, *nativeCodexMemoryStore, codexQualityRun, *qualityWireUpstream, context.Context, *http.Request) {
	t.Helper()
	group, proxy := int64(53), int64(34)
	account := &Account{ID: 16380, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Schedulable: false, Concurrency: 1, GroupIDs: []int64{group}, ProxyID: &proxy,
		Credentials: map[string]any{"plan_type": "pro", "chatgpt_account_id": "quality-wire-fixture"}}
	if !recovery {
		account.Extra = map[string]any{OpenAIReasoningSignatureRecoveryEnabledExtraKey: false}
	}
	store := &nativeCodexMemoryStore{}
	upstream := &qualityWireUpstream{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, nativeCodexRuntime: &NativeCodexRuntime{repo: store}, httpUpstream: upstream,
		accountRepo: &codexAccountRepositoryFixture{account: account}}
	ctx := withCodexTransportFixture(context.Background(), compressed)
	run := codexQualityRun{RunID: uuid.NewString(), ActorID: 9, APIKeyID: 101, AccountID: account.ID, GroupID: group, ProxyID: proxy,
		OwnerIdentity: CodexCredentialOwnerIdentity(account), Model: "gpt-6-sol", ReasoningEffort: "xhigh",
		PromptSHA256: codexQualityHash("question"), MaxSends: 60, Status: "open"}
	_, digest, err := newCodexQualityGrant(run.RunID)
	require.NoError(t, err)
	run, err = issueCodexQualityGrant(ctx, store, run, digest, time.Now().Add(time.Hour))
	require.NoError(t, err)
	runtime, err := svc.codexQualityRuntime()
	require.NoError(t, err)
	key := &APIKey{ID: run.APIKeyID, UserID: run.ActorID, GroupID: &group, Status: StatusAPIKeyActive}
	execution := &codexQualityExecution{runtime: runtime, runID: run.RunID, grantDigest: run.GrantDigest, trialID: uuid.NewString(),
		accountID: account.ID, keyLookup: func(context.Context, int64) (*APIKey, error) { return key, nil }}
	ctx = context.WithValue(ctx, codexQualityExecutionKey{}, execution)
	_, err = runtime.account(ctx, run)
	require.NoError(t, err, "the fixture must pass account checks before exercising wire inspection")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Set("api_key", key)
	state := svc.newOpenAIReasoningRecoveryState(ctx, c, account, "synthetic-token")
	require.Equal(t, recovery, state.enabled, "missing account setting must use the real enabled-by-default recovery policy")
	t.Cleanup(state.Close)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(qualityWireFixtureBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req = withCodexExpectedModel(req, "gpt-6-sol")
	req, _, err = state.PrepareRequest(req, []byte(qualityWireFixtureBody), "")
	require.NoError(t, err)
	return svc, account, store, run, upstream, ctx, req
}

func TestCodexQualityWireRecoveryPreservesBodyAndReplayPolicy(t *testing.T) {
	for _, test := range []struct {
		name                 string
		compressed, recovery bool
	}{
		{"plain_nonreplayable", false, true}, {"zstd_nonreplayable", true, true},
		{"plain_existing_snapshot", false, false}, {"zstd_existing_snapshot", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, account, store, run, upstream, ctx, req := qualityWireServiceFixture(t, test.compressed, test.recovery)
			require.Equal(t, test.recovery, req.GetBody == nil)
			source := &qualityWireSource{reader: req.Body}
			req.Body = source
			expected := []byte(qualityWireFixtureBody)
			if test.compressed {
				var err error
				expected, err = compressCodexRequestBodyZstd(expected)
				require.NoError(t, err)
			}
			response, err := svc.doOpenAICodexUpstream(req, account, "socks5://quality-proxy:1080")
			require.NoError(t, err)
			require.NotNil(t, response)
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, 1, upstream.calls, "one send on the account's ordinary transport")
			require.Equal(t, account.ID, upstream.accountID)
			require.Equal(t, "socks5://quality-proxy:1080", upstream.proxyURL, "the account's ordinary proxy is used")
			require.Empty(t, upstream.request.Header.Get("Cookie"), "a diagnosis carries no routing cookies")
			require.Equal(t, expected, upstream.body, "the actual transport receives the exact final wire bytes")
			require.Equal(t, int64(len(expected)), upstream.request.ContentLength)
			require.Equal(t, test.recovery, upstream.request.GetBody == nil, "inspection must not enable transparent replay")
			require.Equal(t, test.recovery, HTTPUpstreamRedirectsDisabled(upstream.request.Context()))
			if !test.compressed || test.recovery {
				require.Equal(t, 1, source.closed, "the consumed source keeps its close obligation")
			}
			if test.compressed {
				require.Equal(t, "zstd", upstream.request.Header.Get("Content-Encoding"))
			}
			saved, _, err := readCodexQualityRun(ctx, store, run.RunID)
			require.NoError(t, err)
			require.Equal(t, 1, saved.UsedSends)
			require.Len(t, saved.Attempts, 1)
			attempt := saved.Attempts[0]
			require.Equal(t, codexQualityStage, attempt.Stage)
			require.Equal(t, "gpt-6-sol", attempt.RequestModel)
			require.Equal(t, "xhigh", attempt.ReasoningEffort)
			require.Equal(t, "complete", attempt.State, "the raw observation records the completed send")
			require.Equal(t, "gpt-6-sol", *attempt.TerminalModel)
			require.False(t, account.Schedulable)
		})
	}
}

func TestCodexQualityOrdinarySendRecordsRawEvidenceBeforeGuard(t *testing.T) {
	svc, account, store, run, upstream, ctx, req := qualityWireServiceFixture(t, false, true)
	upstream.reply = "data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-6-luna\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-luna\"}}\n\n"
	response, err := svc.doOpenAICodexUpstream(req, account, "")
	require.NoError(t, err)
	forwarded, err := io.ReadAll(response.Body)
	require.ErrorIs(t, err, ErrCodexModelMismatch)
	require.Empty(t, forwarded, "the guard stops a mismatched model before the first downstream byte")
	require.NoError(t, response.Body.Close())
	saved, _, err := readCodexQualityRun(ctx, store, run.RunID)
	require.NoError(t, err)
	require.Equal(t, 1, saved.UsedSends, "the spent send stays counted")
	require.Equal(t, "gpt-6-luna", *saved.Attempts[0].CreatedModel, "the raw observer saw the frame the guard rejected")
	// The same trial is never sent again on the ordinary path.
	retry, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(qualityWireFixtureBody))
	require.NoError(t, err)
	_, err = svc.doOpenAICodexUpstream(retry, account, "")
	require.ErrorIs(t, err, ErrCodexQualitySpent)
	require.Equal(t, 1, upstream.calls)
}

func TestCodexQualityWireFailuresDoNotReserveOrSend(t *testing.T) {
	for _, test := range []struct {
		name, body, encoding string
		readErr, closeErr    error
		snapshot             bool
		getBodyErr           error
	}{
		{name: "read_error", body: qualityWireFixtureBody[:20], readErr: io.ErrUnexpectedEOF},
		{name: "close_error", body: qualityWireFixtureBody, closeErr: errors.New("synthetic close failure")},
		{name: "wire_limit", body: strings.Repeat("x", (2<<20)+2)},
		{name: "invalid_json", body: qualityWireFixtureBody[:20]},
		{name: "invalid_zstd", body: "invalid zstd frame", encoding: "zstd"},
		{name: "decoded_limit", body: `{"model":"gpt-6-sol","reasoning":{"effort":"xhigh"},"input":"` + strings.Repeat("x", 1<<20) + `"}`, encoding: "compress"},
		{name: "unbound_model", body: `{"model":"gpt-6-astra","reasoning":{"effort":"xhigh"},"input":"question","stream":true,"store":false}`},
		{name: "unbound_effort", body: `{"model":"gpt-6-sol","reasoning":{"effort":"high"},"input":"question","stream":true,"store":false}`},
		{name: "snapshot_read_error", body: qualityWireFixtureBody[:20], readErr: io.ErrUnexpectedEOF, snapshot: true},
		{name: "snapshot_close_error", body: qualityWireFixtureBody, closeErr: errors.New("synthetic close failure"), snapshot: true},
		{name: "snapshot_wire_limit", body: strings.Repeat("x", (2<<20)+2), snapshot: true},
		{name: "snapshot_getbody_error", body: qualityWireFixtureBody, getBodyErr: io.ErrUnexpectedEOF, snapshot: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, account, store, run, upstream, ctx, req := qualityWireServiceFixture(t, false, !test.snapshot)
			body := []byte(test.body)
			encoding := test.encoding
			if encoding == "compress" {
				var err error
				body, err = compressCodexRequestBodyZstd(body)
				require.NoError(t, err)
				encoding = "zstd"
			}
			source := &qualityWireSource{reader: bytes.NewReader(body), readErr: test.readErr, closeErr: test.closeErr}
			if test.snapshot {
				req.GetBody = func() (io.ReadCloser, error) { return source, test.getBodyErr }
			} else {
				req.Body, req.ContentLength = source, int64(len(body))
			}
			req.Header.Set("Content-Encoding", encoding)
			_, revision, err := readCodexQualityRun(ctx, store, run.RunID)
			require.NoError(t, err)
			response, err := svc.doOpenAICodexUpstream(req, account, "")
			require.ErrorIs(t, err, ErrCodexQualityUnavailable)
			require.Nil(t, response)
			require.Zero(t, upstream.calls)
			require.Equal(t, !test.snapshot, req.GetBody == nil)
			require.Equal(t, 1, source.closed)
			require.LessOrEqual(t, source.read, (2<<20)+1)
			saved, savedRevision, err := readCodexQualityRun(ctx, store, run.RunID)
			require.NoError(t, err)
			require.Equal(t, revision, savedRevision)
			require.Zero(t, saved.UsedSends)
			require.Empty(t, saved.Attempts)
			require.False(t, account.Schedulable)
		})
	}
}

func TestCodexQualityWireFailedSnapshotCannotReplayPrefix(t *testing.T) {
	ctx := WithHTTPUpstreamRedirectsDisabled(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(qualityWireFixtureBody))
	require.NoError(t, err)
	req.GetBody = nil
	source := &qualityWireSource{reader: strings.NewReader(qualityWireFixtureBody[:20]), readErr: io.ErrUnexpectedEOF}
	req.Body = source
	length := req.ContentLength
	_, _, err = codexQualityWireFields(req)
	require.ErrorIs(t, err, ErrCodexQualityUnavailable)
	remaining, err := io.ReadAll(req.Body)
	require.ErrorIs(t, err, ErrCodexQualityUnavailable)
	require.Empty(t, remaining)
	require.NoError(t, req.Body.Close())
	require.Equal(t, 1, source.closed, "snapshot failure closes the original body once")
	require.Equal(t, length, req.ContentLength)
	require.Nil(t, req.GetBody)
	require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
}

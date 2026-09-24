//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const qualityWireFixtureBody = `{"model":"gpt-6-astra","reasoning":{"effort":"high"},"input":"question","stream":true,"store":false}`

type qualityWireUpstream struct {
	HTTPUpstream
	accountID int64
	scope     extensionv1.CodexRoutingScope
	calls     int
	request   *http.Request
	body      []byte
}

func (u *qualityWireUpstream) HasCodexQualityConnection(id string, accountID int64, scope string) bool {
	return id == u.scope.ConnectionLeaseID && accountID == u.accountID && scope == codexRoutingScopeKey(u.scope)
}

func (u *qualityWireUpstream) DoWithCodexConnectionLease(req *http.Request, _ string, accountID int64, scope, id string, _ time.Time, _ *tlsfingerprint.Profile) (*http.Response, string, error) {
	if !u.HasCodexQualityConnection(id, accountID, scope) {
		return nil, "", ErrCodexConnectionLeaseExpired
	}
	u.calls++
	u.request = req
	defer func() { _ = req.Body.Close() }()
	var err error
	u.body, err = io.ReadAll(req.Body)
	if err != nil {
		return nil, "", err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}\n\n"))}, id, nil
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

// Use the real private transport and durable reservation boundary. Only the
// database and network ports are in-memory fixtures; no model call is possible.
func qualityWireServiceFixture(t *testing.T, compressed, recovery bool) (*OpenAIGatewayService, *Account, *routingMemoryStore, codexQualityRun, *qualityWireUpstream, context.Context, *http.Request) {
	t.Helper()
	group, proxy := int64(53), int64(34)
	account := &Account{ID: 16380, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Schedulable: false, Concurrency: 1, GroupIDs: []int64{group}, ProxyID: &proxy,
		Credentials: map[string]any{"plan_type": "pro", "chatgpt_account_id": "quality-wire-fixture"}}
	if !recovery {
		account.Extra = map[string]any{OpenAIReasoningSignatureRecoveryEnabledExtraKey: false}
	}
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{codexQualityModel}}, nil)
	installation := manager.extensions.Load().installations[1]
	installation.State = PluginStateEnabled
	host, directory, store := routingHostFixture()
	store.PluginRepository = &pluginTokenRepository{installation: installation}
	manager.repo = store
	upstream := &qualityWireUpstream{accountID: account.ID}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, pluginManager: manager, httpUpstream: upstream,
		accountRepo: &routingAccountRepositoryFixture{account: account}}
	ctx := withCodexTransportFixture(context.Background(), compressed)
	scope, err := svc.PrepareCodexRoutingScope(ctx, account.ID, "http")
	require.NoError(t, err)
	directory.scope, directory.account = scope, *extensionAccount(account)
	host.installation = installation
	installation.Manifest.Capabilities = []PluginCapability{{ID: extensionv1.CapabilityCredentials, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}}
	now := time.Now().UTC()
	expiry := now.Add(time.Minute)
	leaseScope := scope
	leaseScope.ConnectionLeaseID, leaseScope.RouteEvidence = "quality-wire-connection", "connection"
	upstream.scope = leaseScope
	cookie := codexRoutingCookie{Name: "__cflb", Value: "quality-wire-route", Domain: "chatgpt.com", Path: "/", FirstSeen: now, ExpiresAt: expiry}
	clock := codexRoutingCookieClock{Scope: scope}
	clock.apply([]codexRoutingCookieChange{{Cookie: cookie}}, now)
	clockKey := "clock.quality.wire-fixture"
	put := func(key string, value any) {
		raw, marshalErr := json.Marshal(value)
		require.NoError(t, marshalErr)
		_, writeErr := store.CompareSwapExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, Value: raw})
		require.NoError(t, writeErr)
	}
	put(clockKey, clock)
	put("bundle.quality.wire-fixture", codexRoutingPrivateBundle{Schema: extensionv1.CodexRoutingSchema, Scope: leaseScope,
		Cookies: []codexRoutingCookie{cookie}, ClockKey: clockKey, ClockRevision: 1, Status: "qualified", Model: codexQualityModel, ExpiresAt: expiry})
	qualification := &extensionv1.CodexRoutingQualification{Scope: leaseScope, Model: codexQualityModel, VerifiedAt: now, ExpiresAt: expiry,
		Bundle: extensionv1.CodexRoutingBundleRef{Key: "bundle.quality.wire-fixture", Revision: 1, ExpiresAt: expiry, ConnectionLeaseID: leaseScope.ConnectionLeaseID}}
	run := codexQualityRun{RunID: uuid.NewString(), ActorID: 9, APIKeyID: 101, AccountID: account.ID, GroupID: group, ProxyID: proxy,
		Scope: scope, PromptSHA256: codexQualityHash("question"), MaxSends: 60, Status: "open", Qualification: qualification,
		RouteGeneration: 1, RouteRuntimeGeneration: installation.RuntimeGeneration}
	_, digest, err := newCodexQualityGrant(run.RunID)
	require.NoError(t, err)
	run, err = issueCodexQualityGrant(ctx, store, run, digest, now.Add(time.Hour))
	require.NoError(t, err)
	runtime := &codexQualityRuntime{s: svc, store: store, installation: installation, host: host}
	key := &APIKey{ID: run.APIKeyID, UserID: run.ActorID, GroupID: &group, Status: StatusAPIKeyActive}
	execution := &codexQualityExecution{runtime: runtime, runID: run.RunID, grantDigest: run.GrantDigest, trialID: uuid.NewString(),
		stage: "business", accountID: account.ID, qualification: qualification,
		keyLookup: func(context.Context, int64) (*APIKey, error) { return key, nil }}
	ctx = context.WithValue(ctx, codexQualityExecutionKey{}, execution)
	_, err = runtime.account(ctx, run)
	require.NoError(t, err, "the fixture must pass account checks before exercising wire inspection")
	_, err = runtime.qualification(ctx, run)
	require.NoError(t, err, "the fixture must have a live private route before exercising wire inspection")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Set("api_key", key)
	state := svc.newOpenAIReasoningRecoveryState(ctx, c, account, "synthetic-token")
	require.Equal(t, recovery, state.enabled, "missing account setting must use the real enabled-by-default recovery policy")
	t.Cleanup(state.Close)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(qualityWireFixtureBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req = withCodexRoutingModel(req, codexQualityModel)
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
			response, err := svc.doOpenAICodexUpstream(req, account, "")
			require.NoError(t, err)
			require.NotNil(t, response)
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, 1, upstream.calls)
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
			require.Equal(t, codexQualityModel, saved.Attempts[0].RequestModel)
			require.Equal(t, codexQualityEffort, saved.Attempts[0].ReasoningEffort)
			require.False(t, account.Schedulable)
		})
	}
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
		{name: "decoded_limit", body: `{"model":"gpt-6-astra","reasoning":{"effort":"high"},"input":"` + strings.Repeat("x", 1<<20) + `"}`, encoding: "compress"},
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

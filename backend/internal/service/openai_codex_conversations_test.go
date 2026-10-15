package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codextransport"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexConversationsPreserveRootAndChildGraph(t *testing.T) {
	policy := fingerprintTestPolicy(true, "0.162.1")
	for _, mode := range []string{"device", "session", "full"} {
		t.Run(mode, func(t *testing.T) {
			account := fingerprintTestAccount(mode)
			root, child, other := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
			var installation string
			for _, thread := range []string{root, child, other} {
				session := root
				if thread == other {
					session = other
				}
				metadata := map[string]any{"session_id": session, "thread_id": thread, "window_id": thread + ":3",
					"turn_id": "real-turn", "sandbox": "windows_mxc", "sandbox_mode": "workspace-write", "unknown_future": "preserve"}
				if thread == child {
					metadata["parent_thread_id"] = root
					metadata["subagent_kind"] = "worker"
				}
				embedded, err := json.Marshal(metadata)
				require.NoError(t, err)
				body := map[string]any{"prompt_cache_key": session, "client_metadata": map[string]any{
					"session_id": session, "thread_id": thread, "x-codex-window-id": thread + ":3", "x-codex-turn-metadata": string(embedded)}}
				headers := http.Header{"Session-Id": {session}, "Thread-Id": {thread}, "X-Codex-Window-Id": {thread + ":3"}}
				applyCodexAccountIdentityClientMetadataMap(body, account, 9, policy)
				applyCodexAccountIdentityHeaders(headers, account, 9, policy)
				ids := resolveCodexFingerprintIDsFromRequest(account, headers, policy)
				require.NotNil(t, ids)
				require.True(t, applyCodexFingerprintClientMetadata(body, ids))
				require.NoError(t, ids.err)
				applyCodexFingerprintHeaders(headers, ids)
				flat, ok := body["client_metadata"].(map[string]any)
				require.True(t, ok)
				canonical, _, err := canonicalCodexMetadata(flat)
				require.NoError(t, err)
				mappedThread := scopeCodexAccountIdentityValue(account, 9, thread)
				require.Equal(t, mappedThread, headers.Get("thread-id"))
				require.Equal(t, uuid.Version(7), uuid.MustParse(mappedThread).Version())
				require.Equal(t, scopeCodexAccountIdentityValue(account, 9, session), body["prompt_cache_key"])
				require.Equal(t, "windows_mxc", canonical["sandbox"])
				require.Equal(t, "preserve", canonical["unknown_future"])
				if thread == child {
					require.NotEqual(t, headers.Get("session-id"), mappedThread)
					require.Equal(t, scopeCodexAccountIdentityValue(account, 9, root), canonical["parent_thread_id"])
					require.Equal(t, "worker", canonical["subagent_kind"])
				} else {
					require.Equal(t, headers.Get("session-id"), mappedThread)
				}
				if installation == "" {
					installation = ids.installationID
				} else {
					require.Equal(t, installation, ids.installationID)
				}
			}
		})
	}
}

func TestCodexConversationsCanonicalPrecedenceAndInvalidMetadata(t *testing.T) {
	policy := fingerprintTestPolicy(true, "0.162.1")
	account := fingerprintTestAccount("device")
	root := uuid.Must(uuid.NewV7()).String()
	canonical, err := json.Marshal(map[string]any{"session_id": root, "thread_id": root, "window_id": root + ":0"})
	require.NoError(t, err)
	flat := map[string]any{"session_id": "stale-flat", "thread_id": "stale-flat", "x-codex-turn-metadata": string(canonical)}
	conflicts, err := normalizeCodexMetadataProjection(flat)
	require.NoError(t, err)
	require.Contains(t, conflicts, "session_id")
	require.Equal(t, root, flat["session_id"])
	for _, raw := range []string{`{"client_metadata":{"x-codex-turn-metadata":"{invalid"}}`, `{"client_metadata":[]}`} {
		_, _, err := applyCodexAccountIdentityClientMetadataRaw([]byte(raw), account, 1, policy)
		require.Error(t, err)
		var clientErr *UpstreamFailoverError
		require.ErrorAs(t, err, &clientErr)
		require.Equal(t, 400, clientErr.ClientStatusCode)
	}
}

func TestCodexConversationsSyntheticTestUsesOneRootAcrossRetry(t *testing.T) {
	fingerprintPolicyTestState(t)
	publishedCodexFingerprintPolicy.Store(fingerprintTestPolicy(true, "0.162.1"))
	account := fingerprintTestAccount("full")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	var first *CodexWireIdentity
	for range 2 {
		body := createOpenAITestPayload("gpt-5.4", true)
		headers, err := projectCodexAccountTest(c, account, account, body)
		require.NoError(t, err)
		wire := codexWireIdentity(body, headers)
		require.True(t, wire.Consistent)
		require.NotEmpty(t, wire.InstallationID)
		require.Equal(t, wire.SessionID, wire.ThreadID)
		if first == nil {
			first = wire
		} else {
			require.Equal(t, first, wire)
		}
	}
}

func TestCodexConversationsWSChecksPreservedHandshakeIdentity(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, mode := range []string{"device", "off"} {
			account := fingerprintTestAccount(mode)
			policy := fingerprintTestPolicy(enabled, "0.162.1")
			a := http.Header{"Session-Id": {"root-a"}, "Thread-Id": {"root-a"}, "X-Codex-Window-Id": {"root-a:0"}}
			b := http.Header{"Session-Id": {"root-b"}, "Thread-Id": {"root-b"}, "X-Codex-Window-Id": {"root-b:0"}}
			require.NotEqual(t, normalizeOpenAIWSHandshakeCompatibility(account, a, policy), normalizeOpenAIWSHandshakeCompatibility(account, b, policy))
			require.Equal(t, normalizeOpenAIWSHandshakeCompatibility(account, a, policy), normalizeOpenAIWSHandshakeCompatibility(account, a.Clone(), policy))
		}
	}
}

func TestCodexStrictNetworkFailureCannotEnterGatewayRetry(t *testing.T) {
	original := &codextransport.Failure{Err: io.ErrUnexpectedEOF}
	result := wrapOpenAIWSFallback("read_upstream", original)
	var stop *UpstreamFailoverError
	require.True(t, errors.As(result, &stop))
	require.Equal(t, NextAccountStop, stop.NextAccountAction)
	_, retry := classifyOpenAIWSReconnectReason(result)
	require.False(t, retry)
	_, retry = classifyOpenAIWSReconnectReason(original)
	require.False(t, retry)
}

func TestCodexRequestWithoutIdentityGetsDistinctStableSyntheticRoot(t *testing.T) {
	fingerprintPolicyTestState(t)
	publishedCodexFingerprintPolicy.Store(fingerprintTestPolicy(true, "0.162.1"))
	account := fingerprintTestAccount("device")
	gateway := &OpenAIGatewayService{}
	var previous, installation string
	for range 2 {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		first := gateway.resolveStagedCodexFingerprintIDs(c, account, c.Request.Header)
		again := gateway.resolveStagedCodexFingerprintIDs(c, account, c.Request.Header)
		require.Equal(t, first.sessionID, again.sessionID)
		require.Equal(t, first.sessionID, first.threadID)
		require.Equal(t, uuid.Version(7), uuid.MustParse(first.threadID).Version())
		if previous != "" {
			require.NotEqual(t, previous, first.threadID)
			require.Equal(t, installation, first.installationID)
		}
		previous, installation = first.threadID, first.installationID
		root := uuid.Must(uuid.NewV7()).String()
		body := map[string]any{"prompt_cache_key": "explicit-cache", "client_metadata": map[string]any{"session_id": root}}
		require.True(t, applyCodexFingerprintClientMetadata(body, first))
		require.NoError(t, first.err)
		require.Equal(t, root, first.sessionID)
		require.Equal(t, root, first.threadID)
		require.Equal(t, "explicit-cache", body["prompt_cache_key"])
	}
}
func TestCodexRawMetadataPreservesUnknownLargeNumbers(t *testing.T) {
	account := fingerprintTestAccount("device")
	policy := fingerprintTestPolicy(true, "0.162.1")
	body := []byte(`{"client_metadata":{"session_id":"root","unknown_counter":9007199254740993,"x-codex-turn-metadata":"{\"unknown_counter\":9007199254740993}"},"input":[]}`)
	scoped, _, err := applyCodexAccountIdentityClientMetadataRaw(body, account, 7, policy)
	require.NoError(t, err)
	ids := resolveCodexFingerprintIDsFromRequest(account, nil, policy)
	wire, _, err := applyCodexFingerprintClientMetadataRaw(scoped, ids)
	require.NoError(t, err)
	require.Equal(t, "9007199254740993", gjson.GetBytes(wire, "client_metadata.unknown_counter").Raw)
	require.Contains(t, gjson.GetBytes(wire, "client_metadata.x-codex-turn-metadata").String(), "9007199254740993")
}

func TestCodexLegacyWindowRetainsTheThreadMappingAndIndex(t *testing.T) {
	account := fingerprintTestAccount("device")
	policy := fingerprintTestPolicy(true, "0.162.1")
	for _, raw := range []string{"legacy-thread", "project:legacy", uuid.NewString(), uuid.Must(uuid.NewV7()).String()} {
		metadata := map[string]any{"thread_id": raw, "window_id": raw + ":19"}
		require.True(t, applyCodexAccountIdentityFields(metadata, account, 9, policy))
		thread, ok := metadata["thread_id"].(string)
		require.True(t, ok)
		require.Equal(t, thread+":19", metadata["window_id"])
	}
}

func TestCodexWSLeaseCannotCrossBodyDeviceIdentity(t *testing.T) {
	account := fingerprintTestAccount("off")
	first := openAIWSAcquireRequest{Account: account, Headers: http.Header{"Session-Id": {"root"}, "Thread-Id": {"root"}}, FingerprintPolicy: fingerprintTestPolicy(true, "0.162.1")}
	first.BodyIdentityKey = codexWSBodyIdentityKey([]byte(`{"client_metadata":{"x-codex-installation-id":"device-a","session_id":"root","thread_id":"root"}}`))
	conn := &openAIWSConn{handshakeCompatibility: normalizeOpenAIWSAcquireCompatibility(first)}
	require.True(t, conn.matchesHandshakeCompatibility(normalizeOpenAIWSAcquireCompatibility(first)))
	next := cloneOpenAIWSAcquireRequest(first)
	next.BodyIdentityKey = codexWSBodyIdentityKey([]byte(`{"client_metadata":{"x-codex-installation-id":"device-b","session_id":"root","thread_id":"root"}}`))
	require.NotEqual(t, first.BodyIdentityKey, next.BodyIdentityKey)
	require.False(t, conn.matchesHandshakeCompatibility(normalizeOpenAIWSAcquireCompatibility(next)))
}

func TestCodexStrictStreamInterruptionCannotBecomeReplay(t *testing.T) {
	binding, err := codextransport.Select(codexCLIUserAgent, "stream-test", "http")
	require.NoError(t, err)
	for _, passthrough := range []bool{false, true} {
		for _, truncated := range []bool{false, true} {
			name := "native"
			if passthrough {
				name = "passthrough"
			}
			if truncated {
				name += "/truncated"
			} else {
				name += "/missing-terminal"
			}
			t.Run(name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				request := c.Request.WithContext(codextransport.WithBinding(context.Background(), binding))
				var body io.Reader = strings.NewReader("")
				if truncated {
					body = &prefixThenErrorReader{err: io.ErrUnexpectedEOF}
				}
				response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(body), Request: request}
				s := &OpenAIGatewayService{cfg: &config.Config{}}
				account := fingerprintTestAccount("device")
				if passthrough {
					_, err = s.handleStreamingResponsePassthrough(context.Background(), response, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				} else {
					_, err = s.handleStreamingResponse(context.Background(), response, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				}
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, GatewayFailureScopeRequest, failure.Scope)
				require.Equal(t, NextAccountStop, failure.NextAccountAction)
				require.True(t, failure.SuppressAccountHealthPenalty)
				require.Equal(t, "codex_transport_failed", failure.ClientErrorCode)
			})
		}
	}
}

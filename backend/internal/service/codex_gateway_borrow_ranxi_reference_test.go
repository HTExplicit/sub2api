package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Both implementations consume identical responses. The reference functions
// are extracted from the pinned Git commit, not restated as expected results.
func TestCodexGatewayBorrowRanxiReference(t *testing.T) {
	t.Run("business STATE provenance", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			owner int64
			ttl   time.Duration
		}{
			{"unknown", 0, 0}, {"same account", 2, time.Minute}, {"foreign account", 1, time.Minute}, {"expired foreign record", 1, -time.Minute},
		} {
			t.Run(tc.name, func(t *testing.T) {
				gateway := &OpenAIGatewayService{}
				c, _ := newTurnStateTestContext(t, 7, "native-session")
				if tc.owner != 0 {
					gateway.openaiCodexTurnStateOrigins.Store(openAICodexTurnStateSeed(c), openAICodexTurnStateOrigin{accountID: tc.owner, expiresAt: time.Now().Add(tc.ttl)})
				}
				h := http.Header{"X-Codex-Turn-State": {"business-state"}}
				original := h.Clone()
				gateway.ranxiRefGuardOpenAICodexTurnStateEcho(c, &Account{ID: 2}, original)
				gateway.guardOpenAICodexTurnStateEcho(c, &Account{ID: 2}, h)
				require.Equal(t, original, h)
			})
		}
	})
	t.Run("source payload", func(t *testing.T) {
		req, err := borrowSourceObservationRequest(context.Background(), http.Header{})
		require.NoError(t, err)
		original := ranxiRefCreatePelicanOpenAIPayload("gpt-6-astra", true, "Reply with OK only.", "medium")
		want, err := json.Marshal(original)
		require.NoError(t, err)
		require.Equal(t, string(want), string(borrowCoreBody(t, req)))
	})
	for _, tc := range []struct {
		name, first, second, firstBody, secondBody, route string
		status                                            int
	}{
		{name: "same STATE", first: "mint", second: "mint"},
		{name: "omitted second STATE", first: "mint"},
		{name: "new second STATE", first: "mint", second: "replacement"},
		{name: "missing first STATE"},
		{name: "route replaced", first: "mint", route: "__oailb=replaced; Path=/; Secure"},
		{name: "route deleted", first: "mint", route: "__oailb=; Max-Age=0; Path=/; Secure"},
		{name: "stream failed", first: "mint", secondBody: "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n"},
		{name: "unterminated completion", first: "mint", secondBody: strings.TrimSuffix(borrowCoreSSE("gpt-6-astra", "OK"), "\n\n")},
		{name: "HTTP failure", first: "mint", status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type sent struct {
				Body    string
				Header  http.Header
				Close   bool
				Profile HTTPUpstreamProfile
			}
			runs := make([][]sent, 2)
			sender := func(run int) borrowCoreProbe {
				return func(req *http.Request, proxy string, id int64, n int, p *tlsfingerprint.Profile) (*http.Response, error) {
					require.Equal(t, "synthetic-exit", proxy)
					require.Equal(t, int64(2), id)
					header := req.Header.Clone()
					_, err := uuid.Parse(header.Get("session_id"))
					require.NoError(t, err)
					header.Del("session_id") // Each original shot deliberately generates a UUID.
					runs[run] = append(runs[run], sent{string(borrowCoreBody(t, req)), header, req.Close, HTTPUpstreamProfileFromContext(req.Context())})
					state, body := tc.first, tc.firstBody
					if len(runs[run]) == 2 {
						state, body = tc.second, tc.secondBody
					}
					resp := borrowCoreResponse("gpt-6-astra", "OK", state, "__cflb=target-affinity; Path=/; Secure")
					if body != "" {
						resp.Body = io.NopCloser(strings.NewReader(body))
					}
					if tc.route != "" {
						resp.Header.Add("Set-Cookie", tc.route)
					}
					if tc.status != 0 {
						resp.StatusCode = tc.status
					}
					return resp, nil
				}
			}
			headers := http.Header{}
			for k, v := range map[string]string{"Authorization": "Bearer target-only", "ChatGPT-Account-ID": "target-only", "User-Agent": "current-client", "Originator": "Codex Desktop", "Version": "0.162.0-alpha.17.2", "session-id": "native-session", "thread-id": "native-thread", "x-codex-window-id": "native-window", "X-Codex-Turn-State": "business-state", "X-Codex-Turn-Metadata": "business-metadata", "Cookie": "__oailb=borrowed; business=untouched", "Content-Encoding": "zstd"} {
				headers.Set(k, v)
			}
			originalHeaders := headers.Clone()
			req, _ := http.NewRequest(http.MethodPost, chatgptCodexURL, nil)
			req.Header = headers
			reference := ranxiRefProbeOpenAICodexStateRoute(context.Background(), sender(0), req, "synthetic-exit", 2, 4, nil)
			service := newBorrowCoreTest(t, sender(1), borrowCoreAccount(1), borrowCoreAccount(2))
			candidate := &codexGatewayBorrowCandidate{sourceID: 1, cookie: http.Cookie{Name: "__oailb", Value: "borrowed"}, expires: time.Now().Add(time.Minute)}
			local := service.probeTarget(context.Background(), req, borrowCoreAccount(2), "gpt-6-astra", "synthetic-exit", nil, candidate)
			require.Equal(t, reference.Verdict == ranxiRefOpenAICodexStateHealthy, local.Success)
			require.Equal(t, reference.NewTicket, local.NewTicket)
			require.Equal(t, runs[0], runs[1], "both implementations must send the same two-shot protocol")
			require.Equal(t, originalHeaders, req.Header, "probes must not overwrite business history or cookies")
		})
	}
	t.Run("cookie scope and lifetime", func(t *testing.T) {
		now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
		for _, cookies := range [][]string{
			{"__oailb=route; Secure; Path=/; Max-Age=600"},
			{"__oailb=route; Secure; Path=/; Max-Age=30"},
			{"__oailb=session; Secure; Path=/"},
			{"__oailb=route; Secure; Path=/other; Max-Age=60"},
			{"__oailb=route; Secure; Domain=other.example; Max-Age=60"},
			{"__oailb=route; Path=/; Max-Age=60"},
			{"__oailb=route; Secure; Path=/; Max-Age=60", "__oailb=; Secure; Path=/; Max-Age=0"},
		} {
			h := http.Header{}
			for _, c := range cookies {
				h.Add("Set-Cookie", c)
			}
			response := &http.Response{Header: h}
			ref := ranxiRefCodexGatewayRouteFromResponse(response, "/backend-api/codex/responses", now)
			local := borrowCandidateFromCookies(response.Cookies(), "/backend-api/codex/responses", now)
			usable := ref != nil && ref.cookie.Value != "" && now.Before(ref.expires)
			require.Equal(t, usable, local != nil, cookies)
			if usable {
				require.Equal(t, ref.cookie, local.cookie)
				require.Equal(t, ref.expires, local.expires)
			}
		}
	})
}

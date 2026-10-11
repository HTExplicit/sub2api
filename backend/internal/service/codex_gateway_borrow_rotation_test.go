package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type borrowNodeProxies struct {
	ProxyRepository
	rows map[int64]Proxy
}

func (p *borrowNodeProxies) GetByID(_ context.Context, id int64) (*Proxy, error) {
	v, ok := p.rows[id]
	if !ok {
		return nil, ErrProxyNotFound
	}
	return &v, nil
}
func (p *borrowNodeProxies) ListByIDs(_ context.Context, ids []int64) ([]Proxy, error) {
	out := []Proxy{}
	for _, id := range ids {
		if p, ok := p.rows[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func borrowNodeCookie(host string) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"host":"`+host+`"}`)) + ".signature"
}

func nodeFixture(t *testing.T, limit int, decide func(int64, string) bool) (*CodexGatewayBorrowService, *[]string, *borrowNodeProxies) {
	t.Helper()
	var calls []string
	var mu sync.Mutex
	proxies := &borrowNodeProxies{rows: map[int64]Proxy{}}
	for i := int64(10); i <= 13; i++ {
		proxies.rows[i] = Proxy{ID: i, Name: fmt.Sprintf("node-%d", i), Host: fmt.Sprintf("node-%d.invalid", i), Port: 8080, Protocol: "http", Status: StatusActive}
	}
	s := newBorrowCoreTest(t, func(req *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		var pid int64
		for k, p := range proxies.rows {
			if p.URL() == proxy {
				pid = k
			}
		}
		require.NotZero(t, pid, "must select a configured node, never the account proxy")
		require.Equal(t, HTTPUpstreamProfileOpenAIHarvest, HTTPUpstreamProfileFromContext(req.Context()))
		body := borrowCoreBody(t, req)
		model := gjson.GetBytes(body, "model").String()
		calls = append(calls, fmt.Sprintf("%d:%d:%s", pid, id, model))
		host := fmt.Sprintf("chat.gateway.unified-%d.api.openai.com", pid)
		if id == 1 {
			return borrowCoreResponse("gpt-6-astra", "OK", "source-state", "__oailb="+borrowNodeCookie(host)+"; Path=/; Secure; Max-Age=230"), nil
		}
		require.Equal(t, int64(2), id)
		require.Equal(t, "Bearer synthetic-token", req.Header.Get("Authorization"))
		c, err := req.Cookie("__oailb")
		require.NoError(t, err)
		require.Equal(t, borrowNodeCookie(host), c.Value)
		state := "first-state"
		if req.Header.Get("X-Codex-Turn-State") != "" {
			state = ""
			if !decide(pid, model) {
				state = "different-state"
			}
		}
		return borrowCoreResponse(model, "OK", state), nil
	}, borrowCoreAccount(1), borrowCoreAccount(2), borrowCoreAccount(3))
	s.proxies = proxies
	s.publishConfig(CodexGatewayBorrowConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{2}, Models: []string{"gpt-6-astra", "gpt-6.1-sol"}, RotateNodes: true, NodeProxyIDs: []int64{10, 11, 12, 13}, MaxNodeAttempts: limit}, false)
	return s, &calls, proxies
}

func nodeRequest(t *testing.T, s *CodexGatewayBorrowService, model string) (*Account, *http.Request) {
	t.Helper()
	a, r, _, err := s.accountTemplate(t.Context(), 2, model)
	require.NoError(t, err)
	r.Body = io.NopCloser(strings.NewReader("business body remains unread"))
	return a, r
}

func TestCodexBorrowNodeSelectionPinsBusinessAndCoolsFailure(t *testing.T) {
	s, calls, _ := nodeFixture(t, 3, func(id int64, _ string) bool { return id == 11 })
	a, req := nodeRequest(t, s, "gpt-6-astra")
	wire, app, err := s.Apply(req, a, "gpt-6-astra", "account-original", nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(11), app.Verification.ProxyID)
	require.Equal(t, []string{"10:1:gpt-6-astra", "10:2:gpt-6-astra", "10:2:gpt-6-astra", "11:1:gpt-6-astra", "11:2:gpt-6-astra", "11:2:gpt-6-astra"}, *calls)
	data, err := io.ReadAll(wire.Body)
	require.NoError(t, err)
	require.Equal(t, "business body remains unread", string(data))
	require.Empty(t, req.Header.Get("Cookie"))
	require.Nil(t, s.accounts.(*borrowCoreAccounts).rows[1].ProxyID)
	require.Nil(t, s.accounts.(*borrowCoreAccounts).rows[2].ProxyID)
	_, app, err = s.Apply(req, a, "gpt-6-astra", "account-original", nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(11), app.Verification.ProxyID)
	require.Len(t, *calls, 6)
	rows := s.Status().Targets
	require.True(t, rows[0].CacheValid)
	cache := s.rotation.cache("gpt-6-astra")
	require.True(t, time.Now().Before(cache.blockedUntil(2, borrowHash("http://node-10.invalid:8080"), "", time.Now())))
	_, other, err := s.Apply(req, borrowCoreAccount(3), "gpt-6-astra", "ordinary-original", nil, false)
	require.NoError(t, err)
	require.Nil(t, other)
	require.Len(t, *calls, 6)
}

func TestCodexBorrowNodeModelsHaveIndependentVerdicts(t *testing.T) {
	s, calls, _ := nodeFixture(t, 1, func(_ int64, model string) bool { return model == "gpt-6.1-sol" })
	a, req := nodeRequest(t, s, "gpt-6-astra")
	_, _, err := s.Apply(req, a, "gpt-6-astra", "account-original", nil, false)
	require.Error(t, err)
	a, req = nodeRequest(t, s, "gpt-6.1-sol")
	_, app, err := s.Apply(req, a, "gpt-6.1-sol", "account-original", nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(10), app.Verification.ProxyID)
	require.Equal(t, []string{"10:1:gpt-6-astra", "10:2:gpt-6-astra", "10:2:gpt-6-astra", "10:2:gpt-6.1-sol", "10:2:gpt-6.1-sol"}, *calls)
}

func TestCodexBorrowNodeBoundedSearchAndRevisionPreservesCooldown(t *testing.T) {
	s, calls, _ := nodeFixture(t, 2, func(id int64, _ string) bool { return id == 12 })
	a, req := nodeRequest(t, s, "gpt-6-astra")
	_, _, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.ErrorContains(t, err, "node_candidates_exhausted")
	require.Len(t, *calls, 6)
	_, _, err = s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.ErrorContains(t, err, "node_search_cooling")
	require.Len(t, *calls, 6)
	s.publishConfig(s.ConfigSnapshot(), false) // Same config load cannot reset cooling.
	cache := s.rotation.cache("gpt-6-astra")
	before := cache.blockedUntil(2, borrowHash("http://node-10.invalid:8080"), "", time.Now())
	cache.reject(2, borrowHash("http://node-10.invalid:8080"), "", time.Now().Add(2*time.Hour), time.Now())
	require.Equal(t, before, cache.blockedUntil(2, borrowHash("http://node-10.invalid:8080"), "", time.Now()))
}

func TestCodexBorrowNodeDispatchRejectsEditedProxyAndNeverReplaysStream(t *testing.T) {
	s, calls, proxies := nodeFixture(t, 3, func(int64, string) bool { return true })
	a, req := nodeRequest(t, s, "gpt-6-astra")
	wire, app, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.NoError(t, err)
	p := proxies.rows[10]
	p.Host = "edited.invalid"
	proxies.rows[10] = p
	_, _, err = s.Apply(wire, a, "gpt-6-astra", "original", nil, true)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowChanged)
	require.Len(t, *calls, 3)
	p.Host = "node-10.invalid"
	proxies.rows[10] = p
	resp, err := trackCodexBorrowNodeHTTP(wire, &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\"}\n\n"))}, nil)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, *calls, 3, "incomplete generation must never be replayed")
	require.True(t, time.Now().Before(s.rotation.cache("gpt-6-astra").blockedUntil(2, app.node.node.identity, "", time.Now())))
	require.False(t, s.Status().Targets[0].CacheValid)
}

func TestCodexBorrowNodeActualSenderUsesSelectedProxyAndBusinessState(t *testing.T) {
	s, calls, _ := nodeFixture(t, 3, func(id int64, _ string) bool { return id == 11 })
	s.gateway.httpUpstream = s.upstream
	a, req := nodeRequest(t, s, "gpt-6-astra")
	req.Header.Set("X-Codex-Turn-State", "business-state")
	req.Body = io.NopCloser(strings.NewReader(`{"model":"gpt-6-astra","stream":true,"store":false,"input":[],"reasoning":{"effort":"xhigh"},"service_tier":"default"}`))
	prepared, _, err := s.gateway.prepareCodexGatewayBorrowHTTP(t.Context(), req, a, "gpt-6-astra", "saved-account-proxy")
	require.NoError(t, err)
	resp, err := s.gateway.doOpenAICodexUpstream(prepared, a, "saved-account-proxy", "gpt-6-astra")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "business-state", req.Header.Get("X-Codex-Turn-State"))
	require.Len(t, *calls, 7)
	require.Equal(t, "11:2:gpt-6-astra", (*calls)[6])
	require.True(t, s.Status().Targets[0].CacheValid)
	usage := s.Status().RecentUsage
	require.Len(t, usage, 1)
	require.Equal(t, int64(11), usage[0].ProxyID)
	require.True(t, usage[0].Applied)
}

func TestCodexBorrowNodeBusinessReplacementRemainsVisibleAndCoolsGateway(t *testing.T) {
	s, calls, _ := nodeFixture(t, 3, func(int64, string) bool { return true })
	a, req := nodeRequest(t, s, "gpt-6-astra")
	wire, app, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.NoError(t, err)
	wire.Header.Set("X-Codex-Turn-State", "old-business-state")
	resp, err := trackCodexBorrowNodeHTTP(wire, borrowCoreResponse("gpt-6-astra", "OK", "new-business-state"), nil)
	require.NoError(t, err)
	require.Equal(t, "new-business-state", resp.Header.Get("X-Codex-Turn-State"))
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, *calls, 3)
	require.Greater(t, time.Until(s.rotation.cache("gpt-6-astra").blockedUntil(2, app.node.node.identity, app.node.gateway, time.Now())), 59*time.Minute)
	require.False(t, s.Status().Targets[0].CacheValid)
	require.Equal(t, "business_state_changed", s.Status().Targets[0].Reason)
}

func TestCodexBorrowNodeStatusUsesEachModelsRoute(t *testing.T) {
	s, _, _ := nodeFixture(t, 3, func(id int64, model string) bool { return model == "gpt-6-astra" || id == 11 })
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		a, req := nodeRequest(t, s, model)
		_, _, err := s.Apply(req, a, model, "original", nil, false)
		require.NoError(t, err)
	}
	status := s.Status()
	require.True(t, status.Targets[0].CacheValid)
	require.True(t, status.Targets[1].CacheValid)
	require.Equal(t, int64(10), status.Targets[0].ProxyID)
	require.Equal(t, int64(11), status.Targets[1].ProxyID)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	_, _, err := s.Apply(req.WithContext(ctx), a, "gpt-6-astra", "original", nil, false)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, s.Status().Targets[0].CacheValid, "a cancelled caller must not cool a valid node")
}

func TestCodexBorrowNodeCoolingHostSkippedAcrossSettingsRevision(t *testing.T) {
	s, calls, proxies := nodeFixture(t, 1, func(id int64, _ string) bool { return id == 12 })
	a, req := nodeRequest(t, s, "gpt-6-astra")
	_, _, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.Error(t, err)
	require.Len(t, *calls, 3)
	cache := s.rotation.cache("gpt-6-astra")
	alias := proxies.rows[11]
	cache.observe(borrowHash(alias.URL()), "chat.gateway.unified-10.api.openai.com", time.Now())
	cfg := s.ConfigSnapshot()
	cfg.MaxNodeAttempts = 3
	s.publishConfig(cfg, false)
	_, app, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(12), app.Verification.ProxyID)
	require.Equal(t, []string{"10:1:gpt-6-astra", "10:2:gpt-6-astra", "10:2:gpt-6-astra", "12:1:gpt-6-astra", "12:2:gpt-6-astra", "12:2:gpt-6-astra"}, *calls)
}

func TestCodexBorrowNodeTransientFailureDoesNotCoolGatewayForAnHour(t *testing.T) {
	s, calls, proxies := nodeFixture(t, 3, func(int64, string) bool { return true })
	base := s.upstream
	badProxy := proxies.rows[10]
	s.upstream = borrowCoreProbe(func(req *http.Request, p string, id int64, n int, profile *tlsfingerprint.Profile) (*http.Response, error) {
		resp, err := base.DoWithTLS(req, p, id, n, profile)
		if id == 2 && p == badProxy.URL() && req.Header.Get("X-Codex-Turn-State") != "" {
			resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\"}\n\n"))
		}
		return resp, err
	})
	a, req := nodeRequest(t, s, "gpt-6-astra")
	_, app, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(11), app.Verification.ProxyID)
	require.Len(t, *calls, 6)
	cache := s.rotation.cache("gpt-6-astra")
	bad := proxies.rows[10]
	require.True(t, cache.blockedUntil(2, borrowHash(bad.URL()), "", time.Now()).IsZero())
	require.True(t, cache.blockedUntil(2, "another-node", "chat.gateway.unified-10.api.openai.com", time.Now()).IsZero())
}

func TestCodexBorrowNodeSingleTransientFailureAllowsNextIndependentPair(t *testing.T) {
	s, calls, proxies := nodeFixture(t, 1, func(int64, string) bool { return true })
	cfg := s.ConfigSnapshot()
	cfg.NodeProxyIDs = []int64{10}
	s.publishConfig(cfg, false)
	base := s.upstream
	interrupted := false
	badProxy := proxies.rows[10]
	s.upstream = borrowCoreProbe(func(req *http.Request, p string, id int64, n int, profile *tlsfingerprint.Profile) (*http.Response, error) {
		resp, err := base.DoWithTLS(req, p, id, n, profile)
		if !interrupted && id == 2 && p == badProxy.URL() && req.Header.Get("X-Codex-Turn-State") != "" {
			interrupted = true
			resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\"}\n\n"))
		}
		return resp, err
	})
	a, req := nodeRequest(t, s, "gpt-6-astra")
	_, _, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.Error(t, err)
	require.Len(t, *calls, 3)
	_, app, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.NoError(t, err)
	require.True(t, app.Verification.Success)
	require.Len(t, *calls, 5, "the next request needs both fresh probe shots, with no source or business replay")
}

func TestCodexBorrowNodeSameCookieAcrossNodesCannotExtendActiveLease(t *testing.T) {
	r := &codexBorrowRotation{}
	first := time.Now().Add(20 * time.Second)
	require.Equal(t, first, r.cookieExpiry("same-routing-value", first))
	require.Equal(t, first, r.cookieExpiry("same-routing-value", time.Now().Add(230*time.Second)))
	other := time.Now().Add(230 * time.Second)
	require.Equal(t, other, r.cookieExpiry("different-routing-value", other))
}

func TestCodexBorrowNodeSourceCoolingIsSharedButTargetVerdictsAreNot(t *testing.T) {
	s, calls, proxies := nodeFixture(t, 3, func(int64, string) bool { return true })
	p := proxies.rows[10]
	s.rotation.sourceCache.rejectSource(1, borrowHash(p.URL()), time.Now())
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		a, req := nodeRequest(t, s, model)
		_, app, err := s.Apply(req, a, model, "original", nil, false)
		require.NoError(t, err)
		require.Equal(t, int64(11), app.Verification.ProxyID)
	}
	require.Equal(t, []string{"11:1:gpt-6-astra", "11:2:gpt-6-astra", "11:2:gpt-6-astra", "11:2:gpt-6.1-sol", "11:2:gpt-6.1-sol"}, *calls)
}

func TestCodexBorrowNodeCompactJSONDoesNotBecomeAnSSEFailure(t *testing.T) {
	s, _, _ := nodeFixture(t, 3, func(int64, string) bool { return true })
	a, req := nodeRequest(t, s, "gpt-6-astra")
	wire, _, err := s.Apply(req, a, "gpt-6-astra", "original", nil, false)
	require.NoError(t, err)
	wire.URL.Path = "/backend-api/codex/responses/lite"
	wire.Header.Set("X-Codex-Turn-State", "full-response-state")
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Codex-Turn-State": {"compact-state"}}, Body: io.NopCloser(strings.NewReader(`{"type":"compaction","output":[]}`))}
	response, err = trackCodexBorrowNodeHTTP(wire, response, nil)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "compact-state", response.Header.Get("X-Codex-Turn-State"))
	require.True(t, s.Status().Targets[0].CacheValid)
}

func TestCodexBorrowNodeDirectRequiresExplicitSelection(t *testing.T) {
	var calls int
	s := newBorrowCoreTest(t, func(req *http.Request, p string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		require.Empty(t, p, "explicit direct node must not use the saved account proxy")
		calls++
		if id == 1 {
			return borrowCoreResponse("gpt-6-astra", "OK", "source", "__oailb="+borrowNodeCookie("chat.gateway.unified-99.api.openai.com")+"; Path=/; Secure; Max-Age=230"), nil
		}
		state := "first"
		if req.Header.Get("X-Codex-Turn-State") != "" {
			state = ""
		}
		return borrowCoreResponse("gpt-6-astra", "OK", state), nil
	}, borrowCoreAccount(1), borrowCoreAccount(2))
	s.proxies = &borrowNodeProxies{rows: map[int64]Proxy{}}
	cfg := s.ConfigSnapshot()
	cfg.TargetAccountIDs = []int64{2}
	cfg.RotateNodes = true
	cfg.NodeProxyIDs = []int64{0}
	_, err := normalizeCodexGatewayBorrowConfig(cfg)
	require.NoError(t, err)
	s.publishConfig(cfg, false)
	a, req := nodeRequest(t, s, "gpt-6-astra")
	wire, app, err := s.Apply(req, a, "gpt-6-astra", "saved-proxy", nil, false)
	require.NoError(t, err)
	require.NotEmpty(t, app.Verification.NodeIdentity)
	require.Zero(t, app.Verification.ProxyID)
	require.Len(t, s.Status().Nodes, 2)
	require.Equal(t, 3, calls)
	_, _, err = s.Apply(wire, a, "gpt-6-astra", "saved-proxy", nil, true)
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	cfg.NodeProxyIDs = []int64{99}
	s.publishConfig(cfg, false)
	_, _, err = s.Apply(wire, a, "gpt-6-astra", "saved-proxy", nil, true)
	require.Error(t, err)
	require.Equal(t, 3, calls, "removing direct cannot silently fall back to it")
}

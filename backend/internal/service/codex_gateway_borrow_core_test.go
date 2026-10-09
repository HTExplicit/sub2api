package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Embedding nil ports makes any health, quota, recovery or account write panic.
// Every request in these tests terminates at the synthetic HTTPUpstream below.
type borrowCoreAccounts struct {
	AccountRepository
	rows map[int64]*Account
}

func (r *borrowCoreAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.rows[id], nil
}

type borrowCoreSettings struct {
	SettingRepository
	raw    string
	writes int
}

func (s *borrowCoreSettings) GetValue(context.Context, string) (string, error) {
	if s.raw == "" {
		return "", ErrSettingNotFound
	}
	return s.raw, nil
}
func (s *borrowCoreSettings) Set(_ context.Context, _, value string) error {
	s.raw, s.writes = value, s.writes+1
	return nil
}

type borrowCoreProbe func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)

func (f borrowCoreProbe) Do(r *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	return f(r, proxy, id, concurrency, nil)
}
func (f borrowCoreProbe) DoWithTLS(r *http.Request, proxy string, id int64, concurrency int, p *tlsfingerprint.Profile) (*http.Response, error) {
	return f(r, proxy, id, concurrency, p)
}

func borrowCoreAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 4,
		Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-chat-id"}, Extra: map[string]any{}}
}

func newBorrowCoreTest(t *testing.T, upstream borrowCoreProbe, rows ...*Account) *CodexGatewayBorrowService {
	t.Helper()
	repo := &borrowCoreAccounts{rows: map[int64]*Account{}}
	for _, row := range rows {
		repo.rows[row.ID] = row
	}
	gateway := &OpenAIGatewayService{accountRepo: repo}
	s := NewCodexGatewayBorrowService(nil, repo, gateway, upstream, nil)
	s.publishConfig(CodexGatewayBorrowConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{2, 3}, Models: []string{"gpt-6-astra", "gpt-6.1-sol"}}, false)
	t.Cleanup(s.Stop)
	return s
}

func borrowCoreCandidate(s *CodexGatewayBorrowService, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.candidate = &codexGatewayBorrowCandidate{cookie: http.Cookie{Name: "__oailb", Value: "synthetic-borrowed-cookie", Secure: true, Path: "/"}, expires: expires, sourceID: 1}
}

func borrowCoreSSE(model, text string) string {
	output := []map[string]any{}
	if text != "" {
		output = append(output, map[string]any{"type": "message", "content": []map[string]any{{"type": "output_text", "text": text}}})
	}
	raw, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "model": model, "output": output}})
	return "data: " + string(raw) + "\n\n"
}

func borrowCoreResponse(model, text, state string, cookies ...string) *http.Response {
	h := make(http.Header)
	if state != "" {
		h.Set("X-Codex-Turn-State", state)
	}
	for _, cookie := range cookies {
		h.Add("Set-Cookie", cookie)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(borrowCoreSSE(model, text)))}
}

func borrowCoreBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	data, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	if req.Header.Get("Content-Encoding") == "zstd" {
		decoder, err := zstd.NewReader(nil)
		require.NoError(t, err)
		defer decoder.Close()
		data, err = decoder.DecodeAll(data, nil)
		require.NoError(t, err)
	}
	return data
}

func TestCodexGatewayBorrowConfigDefaultsAndLoadNeverProbe(t *testing.T) {
	var calls atomic.Int32
	probe := borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not call upstream")
	})
	store := &borrowCoreSettings{}
	s, err := ProvideCodexGatewayBorrowService(store, nil, nil, probe, nil)
	require.NoError(t, err)
	t.Cleanup(s.Stop)
	require.Equal(t, DefaultCodexGatewayBorrowConfig(), s.ConfigSnapshot())
	require.Zero(t, store.writes)
	require.Zero(t, calls.Load())
	store.raw = `{"enabled":true,"source_account_ids":[1],"target_account_ids":[2],"models":["gpt-6.1-sol"]}`
	cfg, err := s.GetConfig(context.Background())
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.Zero(t, calls.Load())
	for _, raw := range []string{`null`, `{"enabled":true}`, `{"source_account_ids":[1],"target_account_ids":[1]}`, `{"models":["gpt-6-luna"]}`, `{"models":["gpt-6-astra","gpt-6-astra"]}`, `{"unknown":true}`, `{} {}`} {
		_, err := DecodeCodexGatewayBorrowConfig([]byte(raw))
		require.Error(t, err, raw)
	}
	_, err = DecodeCodexGatewayBorrowConfig([]byte(`{"enabled":false}`))
	require.NoError(t, err)
}

func TestCodexGatewayBorrowCookieScopeAndExpiry(t *testing.T) {
	now := time.Now()
	valid := http.Cookie{Name: "__oailb", Value: "route", Secure: true, Path: "/backend-api/codex"}
	for _, tc := range []struct {
		name   string
		change func(*http.Cookie)
		ttl    time.Duration
		valid  bool
	}{
		{"session", func(*http.Cookie) {}, 230 * time.Second, true},
		{"actual max age", func(c *http.Cookie) { c.MaxAge = 30 }, 30 * time.Second, true},
		{"local cap", func(c *http.Cookie) { c.MaxAge = 1000000 }, 230 * time.Second, true},
		{"actual expires", func(c *http.Cookie) { c.Expires = now.Add(20 * time.Second) }, 20 * time.Second, true},
		{"default path", func(c *http.Cookie) { c.Path = "" }, 230 * time.Second, true},
		{"foreign domain", func(c *http.Cookie) { c.Domain = "example.invalid" }, 0, false},
		{"wrong path", func(c *http.Cookie) { c.Path = "/backend-api/codex/responses-other" }, 0, false},
		{"not secure", func(c *http.Cookie) { c.Secure = false }, 0, false},
		{"deleted", func(c *http.Cookie) { c.MaxAge = -1 }, 0, false},
		{"expired", func(c *http.Cookie) { c.Expires = now.Add(-time.Second) }, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cookie := valid
			tc.change(&cookie)
			candidate := borrowCandidateFromCookies([]*http.Cookie{&cookie}, "/backend-api/codex/responses", now)
			if !tc.valid {
				require.Nil(t, candidate)
				return
			}
			require.NotNil(t, candidate)
			require.Equal(t, now.Add(tc.ttl), candidate.expires)
		})
	}
	deleted := valid
	deleted.MaxAge = -1
	require.Nil(t, borrowCandidateFromCookies([]*http.Cookie{&valid, &deleted}, "/backend-api/codex/responses", now))
}

func TestCodexGatewayBorrowSourceUsesOwnExitAndNeverExtendsSameCookie(t *testing.T) {
	proxyID := int64(11)
	source := borrowCoreAccount(1)
	source.ProxyID, source.Proxy = &proxyID, &Proxy{Protocol: "http", Host: "source.invalid", Port: 8080}
	var calls int
	s := newBorrowCoreTest(t, func(req *http.Request, proxy string, id int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		require.Equal(t, int64(1), id)
		require.Equal(t, source.Proxy.URL(), proxy)
		require.Nil(t, profile)
		require.Equal(t, HTTPUpstreamProfileCodexBorrowSource, HTTPUpstreamProfileFromContext(req.Context()))
		require.True(t, IsCodexGatewayBorrowObservation(req.Context()))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		deadline, ok := req.Context().Deadline()
		require.True(t, ok)
		require.InDelta(t, 90, time.Until(deadline).Seconds(), 2)
		body := borrowCoreBody(t, req)
		require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
		require.Equal(t, "medium", gjson.GetBytes(body, "reasoning.effort").String())
		require.Equal(t, "Reply with OK only.", gjson.GetBytes(body, "input.0.content.0.text").String())
		require.Empty(t, req.Header.Get("Cookie"))
		require.Empty(t, req.Header.Get("X-Codex-Turn-State"))
		return borrowCoreResponse("gpt-6-astra", "OK", "source-state", "__oailb=synthetic-borrowed-cookie; Secure; Path=/; Max-Age=230", "__cflb=source-cookie; Secure; Path=/"), nil
	}, source)
	expires := time.Now().Add(50 * time.Second)
	borrowCoreCandidate(s, expires)
	require.NoError(t, s.prepareSource(context.Background(), s.revision, s.revisionCtx))
	require.Equal(t, 1, calls)
	require.Equal(t, expires, s.candidate.expires)
	borrowCoreCandidate(s, time.Now().Add(150*time.Second))
	require.NoError(t, s.prepareSource(context.Background(), s.revision, s.revisionCtx))
	require.Equal(t, 1, calls, "fresh candidate skips a new source invocation")
}

func TestCodexGatewayBorrowTargetCacheModelAndFingerprintIsolation(t *testing.T) {
	var calls int
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(req *http.Request, proxy string, id int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		require.Equal(t, int64(2), id)
		require.Equal(t, "target-exit", proxy)
		require.Nil(t, profile)
		require.Equal(t, HTTPUpstreamProfileCodexBorrowTarget, HTTPUpstreamProfileFromContext(req.Context()))
		require.True(t, IsCodexGatewayBorrowObservation(req.Context()))
		require.True(t, req.Close)
		deadline, ok := req.Context().Deadline()
		require.True(t, ok)
		require.InDelta(t, 45, time.Until(deadline).Seconds(), 2)
		body := borrowCoreBody(t, req)
		model := gjson.GetBytes(body, "model").String()
		require.Equal(t, "Reply with OK.", gjson.GetBytes(body, "input.0.content.0.text").String())
		require.False(t, gjson.GetBytes(body, "reasoning.effort").Exists())
		borrowed, err := req.Cookie("__oailb")
		require.NoError(t, err)
		require.Equal(t, "synthetic-borrowed-cookie", borrowed.Value)
		if req.Header.Get("X-Codex-Turn-State") == "" {
			require.NotContains(t, req.Header.Get("Cookie"), "source-cookie")
			return borrowCoreResponse(model, "", "minted-state", "__cflb=target-cookie; Secure; Path=/"), nil
		}
		require.Equal(t, "minted-state", req.Header.Get("X-Codex-Turn-State"))
		require.Contains(t, req.Header.Get("Cookie"), "__cflb=target-cookie")
		// Completed with no visible text is valid for the STATE criterion.
		return borrowCoreResponse(model, "", ""), nil
	}, a)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, template, _, err := s.accountTemplate(context.Background(), 2, "gpt-6-astra")
	require.NoError(t, err)
	template.Header.Set("Cookie", "__oailb=old; __cflb=business-cookie; other=keep")
	template.Header.Set("X-Codex-Turn-State", "business-state")
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		wire, app, err := s.Apply(template, a, model, "target-exit", nil, false)
		require.NoError(t, err)
		require.True(t, app.Applied)
		require.Equal(t, "business-state", wire.Header.Get("X-Codex-Turn-State"))
		require.Contains(t, wire.Header.Get("Cookie"), "__cflb=business-cookie")
		require.Contains(t, wire.Header.Get("Cookie"), "other=keep")
		require.NotContains(t, wire.Header.Get("Cookie"), "minted-state")
		require.Contains(t, template.Header.Get("Cookie"), "__oailb=old", "input request is not modified")
	}
	require.Equal(t, 4, calls)
	_, app, err := s.Apply(template, a, "gpt-6.1-sol", "target-exit", nil, false)
	require.NoError(t, err)
	require.True(t, app.Applied, "hot cache is read before global target lock")
	for _, header := range []string{"Authorization", "ChatGPT-Account-ID", "User-Agent", "Originator", "Version", "X-Codex-Turn-State"} {
		req := template.Clone(template.Context())
		req.Header.Set(header, req.Header.Get(header)+"-changed")
		_, _, err := s.Apply(req, a, "gpt-6.1-sol", "target-exit", nil, true)
		require.Error(t, err, header)
	}
	_, _, err = s.Apply(template, a, "gpt-6.1-sol", "other-exit", nil, true)
	require.Error(t, err)
	_, _, err = s.Apply(template, a, "gpt-6.1-sol", "target-exit", &tlsfingerprint.Profile{Name: "changed"}, true)
	require.Error(t, err)
	require.Equal(t, 4, calls, "cached-only mismatches do not validate")
}

func TestCodexGatewayBorrowTargetChangedStateRouteAndFailureCooldown(t *testing.T) {
	for _, mode := range []string{"state", "cookie", "missing", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			var calls int
			a := borrowCoreAccount(2)
			s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				calls++
				borrowCoreBody(t, req)
				if mode == "missing" {
					return borrowCoreResponse("gpt-6.1-sol", "", ""), nil
				}
				if mode == "cookie" {
					return borrowCoreResponse("gpt-6.1-sol", "", "first", "__oailb=changed; Secure; Path=/"), nil
				}
				if mode == "incomplete" {
					return &http.Response{StatusCode: 200, Header: http.Header{"X-Codex-Turn-State": []string{"first"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
				}
				state := "first"
				if calls == 2 {
					state = "changed"
				}
				return borrowCoreResponse("gpt-6.1-sol", "", state), nil
			}, a)
			borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
			_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
			require.NoError(t, err)
			_, _, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
			require.Error(t, err)
			initial := calls
			_, _, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
			require.Error(t, err)
			require.Equal(t, initial, calls, "failure stays in 15-second cooldown")
			status := s.Status()
			require.False(t, status.Targets[1].CacheValid)
			require.NotNil(t, status.Targets[1].RetryAfter)
		})
	}
}

func TestCodexGatewayBorrowSourceSingleFlightCooldownAndRevisionCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"full synthetic source failure"}}`))}, nil
	}, borrowCoreAccount(1))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.prepareSource(context.Background(), s.revision, s.revisionCtx) }()
	}
	<-started
	close(release)
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
	err := s.prepareSource(context.Background(), s.revision, s.revisionCtx)
	require.ErrorContains(t, err, "full synthetic source failure")
	require.Equal(t, int32(1), calls.Load())
	started, release = make(chan struct{}), make(chan struct{})
	calls.Store(0)
	s2 := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		close(started)
		<-release
		return borrowCoreResponse("gpt-6-astra", "OK", "", "__oailb=late; Secure; Path=/"), nil
	}, borrowCoreAccount(1))
	done := make(chan error, 1)
	rev, revCtx := s2.revision, s2.revisionCtx
	go func() { done <- s2.prepareSource(context.Background(), rev, revCtx) }()
	<-started
	s2.publishConfig(DefaultCodexGatewayBorrowConfig(), false)
	close(release)
	require.Error(t, <-done)
	require.Nil(t, s2.Status().Candidate)
	require.Empty(t, s2.Status().Sources)
}

type borrowCoreUntouchedBody struct{ reads int }

func (b *borrowCoreUntouchedBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("business body must not be inspected")
}
func (*borrowCoreUntouchedBody) Close() error { return nil }

func TestCodexGatewayBorrowGatePreserves100MiBBodyAndOtherRoutes(t *testing.T) {
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		t.Fatal("must not probe")
		return nil, nil
	}, a)
	body := &borrowCoreUntouchedBody{}
	req, err := http.NewRequest(http.MethodPost, chatgptCodexURL, body)
	require.NoError(t, err)
	req.ContentLength = 100 << 20
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	key := borrowTargetFingerprint(req, a, "gpt-6.1-sol", "", nil, s.candidate.cookie.Value)
	s.targets[codexGatewayBorrowTargetKey{2, "gpt-6.1-sol"}] = codexGatewayBorrowTargetCheck{policyRevision: currentCodexFingerprintPolicyForAccount(a).revision, key: key, cookieKey: borrowHash(s.candidate.cookie.Value), expires: s.candidate.expires, result: CodexGatewayBorrowVerification{Success: true}}
	wire, app, err := s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	require.True(t, app.Applied)
	require.Same(t, body, wire.Body)
	require.Equal(t, int64(100<<20), wire.ContentLength)
	require.Zero(t, body.reads)
	for _, url := range []string{"https://chatgpt.com/backend-api/codex/responses/compact", "https://chatgpt.com/backend-api/codex/models", "https://api.openai.com/v1/responses", "https://chatgpt.com:443/backend-api/codex/responses", "https://other.invalid/backend-api/codex/responses"} {
		other, _ := http.NewRequest(http.MethodPost, url, body)
		result, app, err := s.Apply(other, a, "gpt-6.1-sol", "", nil, false)
		require.NoError(t, err)
		require.Same(t, other, result)
		require.Nil(t, app)
	}
	result, app, err := s.Apply(req, a, "gpt-6-luna", "", nil, false)
	require.NoError(t, err)
	require.Same(t, req, result)
	require.Nil(t, app)
	require.Zero(t, body.reads)
}

func TestCodexGatewayBorrowPelicanCachedOnlyPausedTargetAndFullOutput(t *testing.T) {
	var calls int
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		body := borrowCoreBody(t, req)
		if gjson.GetBytes(body, "input.0.content.0.text").String() == CodexGatewayBorrowPelicanPrompt {
			require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
			return borrowCoreResponse("gpt-6.1-sol", "<html><svg>鹈鹕骑自行车</svg></html>", ""), nil
		}
		state := "first"
		if calls == 2 {
			state = ""
		}
		return borrowCoreResponse("gpt-6.1-sol", "", state), nil
	}, a)
	result, err := s.GeneratePelican(context.Background(), 2, "gpt-6.1-sol", "high")
	require.NoError(t, err)
	require.Equal(t, "skipped", result.Status)
	require.Zero(t, calls)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	_, _, err = s.Apply(req, a, "gpt-6.1-sol", "", nil, false)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	a.Status, a.Schedulable = "paused", false
	result, err = s.GeneratePelican(context.Background(), 2, "gpt-6.1-sol", "")
	require.NoError(t, err)
	require.Equal(t, "complete", result.Status)
	require.Equal(t, "<html><svg>鹈鹕骑自行车</svg></html>", result.RawAnswer)
	require.Contains(t, result.RawResponse, "response.completed")
	require.Equal(t, 3, calls)
	require.Equal(t, "paused", a.Status)
	require.False(t, a.Schedulable)
	result, err = s.GeneratePelican(context.Background(), 2, "gpt-6-astra", "high")
	require.NoError(t, err)
	require.Equal(t, "skipped", result.Status)
	require.Equal(t, 3, calls)
}

func TestCodexGatewayBorrowSSERequiresTerminalAndPreservesErrors(t *testing.T) {
	for _, stream := range []string{"data: [DONE]\n\n", `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n", `data: {"type":"response.failed","response":{"error":{"code":"synthetic_error","message":"full upstream message"}}}` + "\n\n", "data: {bad json}\n\n"} {
		got := parseCodexGatewayBorrowSSE([]byte(stream), "gpt-6-astra", codexGatewayBorrowProbeMaxBody)
		require.False(t, got.complete)
		if strings.Contains(stream, "full upstream message") {
			require.Contains(t, got.errorText, "full upstream message")
		}
	}
	createdOnly := `data: {"type":"response.created","response":{"model":"gpt-6-astra"}}` + "\n\n" + `data: {"type":"response.completed","response":{"status":"completed","output":[]}}` + "\n\n"
	got := parseCodexGatewayBorrowSSE([]byte(createdOnly), "gpt-6-astra", codexGatewayBorrowProbeMaxBody)
	require.True(t, got.complete)
	require.Empty(t, got.model, "source requires a final model, not earlier advertised identity")
	got = parseCodexGatewayBorrowSSE([]byte(borrowCoreSSE("gpt-6.1-sol", "answer")), "gpt-6-astra", codexGatewayBorrowProbeMaxBody)
	require.True(t, got.complete)
	require.True(t, got.modelMismatch)
	require.Equal(t, "answer", got.answer)
}

func TestCodexGatewayBorrowQualificationFailuresRemainTyped(t *testing.T) {
	a := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		return nil, errors.New("synthetic source connection refused")
	}, borrowCoreAccount(1), a)
	_, req, _, err := s.accountTemplate(context.Background(), 2, "gpt-6-astra")
	require.NoError(t, err)
	_, _, err = s.Apply(req, a, "gpt-6-astra", "", nil, false)
	require.True(t, IsCodexGatewayBorrowFailure(err))
	require.ErrorContains(t, err, "synthetic source connection refused")
	_, _, err = s.Apply(req, a, "gpt-6-astra", "", nil, true)
	require.True(t, IsCodexGatewayBorrowFailure(err))
	require.ErrorIs(t, err, ErrCodexGatewayBorrowUnavailable)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, _, err = s.Apply(req, a, "gpt-6-astra", "", nil, false)
	require.True(t, IsCodexGatewayBorrowFailure(err))
	require.ErrorIs(t, err, ErrCodexGatewayBorrowUnavailable)
	require.False(t, IsCodexGatewayBorrowFailure(errors.New("ordinary business failure")))
}

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func cacheIdentityHybridApplication() BusinessSystemPromptApplication {
	return BusinessSystemPromptApplication{
		Applied: true, Revision: 51, SHA256: strings.Repeat("a", 64),
		CompositionMode: BusinessSystemPromptCompositionCodexSkillHybrid,
		BundleID:        "codexrip-reverse-skill", BundleRevision: 12,
		BundleEffectiveTreeSHA256: strings.Repeat("b", 64),
		BaseSHA256:                strings.Repeat("c", 64), EffectiveSHA256: strings.Repeat("d", 64),
		BundlePromptEffectiveSHA256: strings.Repeat("e", 64),
	}
}

func TestBusinessSystemPromptCacheIdentity364Regression(t *testing.T) {
	seed := "00000000-0000-0000-0000-000000000000"
	application := cacheIdentityHybridApplication()
	// Independent encoding of the previously emitted production-sized key.
	legacy := seed + ":business-system-prompt:51:codexrip-reverse-skill:" +
		strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64) + ":" + strings.Repeat("d", 64) +
		":bundle-revision:12:" + strings.Repeat("e", 64)
	require.Len(t, legacy, 364)
	digest := sha256.Sum256([]byte(legacy))
	want := hex.EncodeToString(digest[:])
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", nil)
	require.Equal(t, want, deriveBusinessSystemPromptCacheKey(c, seed, application))
	require.Equal(t, want, deriveBusinessSystemPromptCacheKey(c, want, application))
	// The existing independent Cindy policy gives exactly the same wire key.
	SetCindyManagedCompatibility(c, true)
	account := businessSystemPromptAPIKeyAccount(true)
	account.Platform, account.WirePlatform, account.ProviderProfile = PlatformCindy, WirePlatformOpenAI, ProviderProfileCindyLaxaV1
	account.Credentials["base_url"] = "https://api.laxarouter.ai"
	oldBody := []byte(fmt.Sprintf(`{"prompt_cache_key":%q}`, legacy))
	normalized, changed, err := normalizeCindyManagedPromptCacheKey(oldBody, c, account)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, want, gjson.GetBytes(normalized, "prompt_cache_key").String())
}

func TestBusinessSystemPromptCacheIdentityChangesWithNamespace(t *testing.T) {
	base := cacheIdentityHybridApplication()
	want := deriveBusinessSystemPromptCacheKey(nil, "seed", base)
	for name, mutate := range map[string]func(*BusinessSystemPromptApplication){
		"revision":        func(a *BusinessSystemPromptApplication) { a.Revision++ },
		"bundle":          func(a *BusinessSystemPromptApplication) { a.BundleID += "-new" },
		"tree":            func(a *BusinessSystemPromptApplication) { a.BundleEffectiveTreeSHA256 = strings.Repeat("f", 64) },
		"base":            func(a *BusinessSystemPromptApplication) { a.BaseSHA256 = strings.Repeat("f", 64) },
		"effective":       func(a *BusinessSystemPromptApplication) { a.EffectiveSHA256 = strings.Repeat("f", 64) },
		"bundle revision": func(a *BusinessSystemPromptApplication) { a.BundleRevision++ },
		"bundle prompt":   func(a *BusinessSystemPromptApplication) { a.BundlePromptEffectiveSHA256 = strings.Repeat("f", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			require.NotEqual(t, want, deriveBusinessSystemPromptCacheKey(nil, "seed", changed))
		})
	}
	require.NotEqual(t, want, deriveBusinessSystemPromptCacheKey(nil, "other-seed", base))
	ordinary := BusinessSystemPromptApplication{Applied: true, Revision: 1, SHA256: strings.Repeat("a", 64)}
	first := deriveBusinessSystemPromptCacheKey(nil, "seed", ordinary)
	ordinary.SHA256 = strings.Repeat("b", 64)
	require.NotEqual(t, first, deriveBusinessSystemPromptCacheKey(nil, "seed", ordinary))
}

func TestBusinessSystemPromptCacheIdentityPreservesAbsentAndUnappliedFields(t *testing.T) {
	for _, body := range []string{
		`{"input":[],"unknown":9007199254740993}`,
		`{"prompt_cache_key":null,"input":[]}`,
		`{"prompt_cache_key":42,"input":[]}`,
		`{"prompt_cache_key":"","input":[]}`,
		`{"prompt_cache_key":"  ","input":[]}`,
	} {
		updated, err := rewriteBusinessSystemPromptCacheKey(nil, []byte(body), cacheIdentityHybridApplication())
		require.NoError(t, err)
		require.Equal(t, body, string(updated))
	}
	for _, seed := range []string{" seed ", strings.Repeat("k", 365), strings.Repeat("哈", 65)} {
		body := []byte(fmt.Sprintf(`{"prompt_cache_key":%q,"unknown":9007199254740993}`, seed))
		updated, err := rewriteBusinessSystemPromptCacheKey(nil, body, BusinessSystemPromptApplication{})
		require.NoError(t, err)
		require.Equal(t, body, updated)
	}
}

func TestBusinessSystemPromptCacheIdentityTreatsClientKeysAsOpaque(t *testing.T) {
	application := cacheIdentityHybridApplication()
	for _, seed := range []string{strings.Repeat("a", 64), strings.Repeat("哈", 80), "client" + businessSystemPromptCacheNamespace(application)} {
		c, _ := newBusinessSystemPromptGinContext("/v1/responses", nil)
		body := []byte(fmt.Sprintf(`{"prompt_cache_key":%q,"input":[{"type":"reasoning","id":"rs_test","encrypted_content":"opaque","phase":"analysis"}],"extra":9007199254740993}`, seed))
		updated, err := rewriteBusinessSystemPromptCacheKey(c, body, application)
		require.NoError(t, err)
		key := gjson.GetBytes(updated, "prompt_cache_key").String()
		require.Regexp(t, `^[0-9a-f]{64}$`, key)
		require.NotEqual(t, seed, key)
		require.Equal(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(updated, "input").Raw)
		require.Equal(t, "9007199254740993", gjson.GetBytes(updated, "extra").Raw)
		retry, err := rewriteBusinessSystemPromptCacheKey(c, updated, application)
		require.NoError(t, err)
		require.Equal(t, updated, retry)
	}
}

func TestBusinessSystemPromptCacheIdentityFrozenRetryAndFallback(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "server"}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	svc := &OpenAIGatewayService{businessPromptService: policy}
	account := businessSystemPromptAPIKeyAccount(true)
	original := []byte(`{"instructions":"client","prompt_cache_key":"seed","input":[]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", original)
	body, application, err := svc.applyBusinessSystemPromptForRequest(c, original, account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	body, err = rewriteBusinessSystemPromptCacheKey(c, body, application)
	require.NoError(t, err)
	want := gjson.GetBytes(body, "prompt_cache_key").String()
	store.loaded = BusinessSystemPromptSnapshot{Revision: 2, Enabled: true, Body: "new-server"}
	require.NoError(t, policy.Reload(context.Background()))
	for _, retryBody := range [][]byte{original, body} {
		account.ID++ // Failover must not create a new business-prompt identity.
		retried, frozen, err := svc.applyBusinessSystemPromptForRequest(c, retryBody, account, BusinessSystemPromptProtocolResponses, false)
		require.NoError(t, err)
		retried, err = rewriteBusinessSystemPromptCacheKey(c, retried, frozen)
		require.NoError(t, err)
		require.Equal(t, want, gjson.GetBytes(retried, "prompt_cache_key").String())
		require.Equal(t, int64(1), frozen.Revision)
	}
	fallback := []byte(fmt.Sprintf(`{"prompt_cache_key":%q,"messages":[{"role":"user","content":"continue"}]}`, want))
	fallback, frozen, err := svc.applyBusinessSystemPromptForRequest(c, fallback, account, BusinessSystemPromptProtocolChat, false)
	require.NoError(t, err)
	fallback, err = rewriteBusinessSystemPromptCacheKey(c, fallback, frozen)
	require.NoError(t, err)
	require.Equal(t, want, gjson.GetBytes(fallback, "prompt_cache_key").String())
	fresh, _ := newBusinessSystemPromptGinContext("/v1/responses", original)
	freshBody, freshApplication, err := svc.applyBusinessSystemPromptForRequest(fresh, original, account, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	freshBody, err = rewriteBusinessSystemPromptCacheKey(fresh, freshBody, freshApplication)
	require.NoError(t, err)
	require.NotEqual(t, want, gjson.GetBytes(freshBody, "prompt_cache_key").String())
}

func TestBusinessSystemPromptCacheIdentityWSTurnStateAndHeaderFallback(t *testing.T) {
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", nil)
	application := cacheIdentityHybridApplication()
	beginBusinessSystemPromptRequestTurn(c)
	wire := deriveBusinessSystemPromptCacheKey(c, "seed", application)
	require.Equal(t, wire, businessSystemPromptUpstreamCacheKey(c, []byte(`{}`), "seed", application))
	require.Equal(t, wire, businessSystemPromptUpstreamCacheKey(c, []byte(fmt.Sprintf(`{"prompt_cache_key":%q}`, wire)), "seed", application))
	resolution := resolveOpenAIWSSessionHeaders(c, wire)
	require.Equal(t, wire, resolution.SessionID)
	c.Request.Header.Set("session_id", "explicit-session")
	c.Request.Header.Set("conversation_id", "explicit-conversation")
	resolution = resolveOpenAIWSSessionHeaders(c, wire)
	require.Equal(t, "explicit-session", resolution.SessionID)
	require.Equal(t, "explicit-conversation", resolution.ConversationID)
	beginBusinessSystemPromptRequestTurn(c)
	require.Equal(t, wire, deriveBusinessSystemPromptCacheKey(c, "seed", application))
	beginBusinessSystemPromptRequestTurn(c)
	// A new client turn may legitimately supply the previous output as its
	// own opaque seed. Old request state must not treat it as already derived.
	require.NotEqual(t, wire, deriveBusinessSystemPromptCacheKey(c, wire, application))
}

// No networking: a strict in-process upstream validates the actual request
// built by each adapter before returning a protocol-appropriate completion.
type businessCacheStrictUpstream struct {
	httpUpstreamRecorder
	rejected int
}

func (u *businessCacheStrictUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	if len(u.responses) == 0 {
		switch {
		case strings.HasSuffix(req.URL.Path, "/chat/completions"):
			u.resp = &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chat_test","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))}
		case gjson.GetBytes(readCacheStrictBody(req), "stream").Bool():
			u.resp = openAICompatSSECompletedResponse("resp_cache_test", "gpt-5.4")
		default:
			u.resp = &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"resp_cache_test","object":"response","status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))}
		}
	}
	resp, err := u.httpUpstreamRecorder.Do(req, proxy, accountID, concurrency)
	if utf8.RuneCountInString(gjson.GetBytes(u.lastBody, "prompt_cache_key").String()) > 64 {
		u.rejected++
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"prompt_cache_key exceeds 64"}}`))}, nil
	}
	return resp, err
}

func readCacheStrictBody(req *http.Request) []byte {
	if req.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

func (u *businessCacheStrictUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func TestBusinessSystemPromptCacheIdentityStrictHTTPAdapters(t *testing.T) {
	seed := "00000000-0000-0000-0000-000000000000"
	responses := []byte(fmt.Sprintf(`{"model":"gpt-5.4","stream":false,"instructions":"client","prompt_cache_key":%q,"input":[{"role":"user","content":"hello"}]}`, seed))
	chat := []byte(fmt.Sprintf(`{"model":"gpt-5.4","stream":false,"prompt_cache_key":%q,"messages":[{"role":"user","content":"hello"}]}`, seed))
	messages := []byte(`{"model":"gpt-5.4","max_tokens":16,"stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	for _, mode := range []string{"responses", "hybrid responses", "passthrough", "compact", "chat bridge", "messages bridge", "raw chat", "responses fallback", "messages fallback"} {
		t.Run(mode, func(t *testing.T) {
			path, body := "/v1/responses", responses
			if mode == "compact" {
				path += "/compact"
			}
			if mode == "chat bridge" || mode == "raw chat" {
				path, body = "/v1/chat/completions", chat
			}
			if strings.HasPrefix(mode, "messages") {
				path, body = "/v1/messages", messages
			}
			c, _ := newBusinessSystemPromptGinContext(path, body)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			upstream := &businessCacheStrictUpstream{}
			svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, true)}
			if mode == "hybrid responses" {
				svc.businessPromptService = newGatewayHybridBusinessSystemPromptPolicyWithBody(t, embeddedBusinessSystemPrompt, 51)
			}
			account := businessSystemPromptAPIKeyAccount(true)
			var err error
			switch mode {
			case "responses", "hybrid responses", "compact":
				_, err = svc.Forward(context.Background(), c, account, body)
			case "passthrough":
				_, err = svc.forwardOpenAIPassthrough(context.Background(), c, account, body, body, "gpt-5.4", false, false, time.Now())
			case "chat bridge":
				_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, seed, "")
			case "messages bridge":
				_, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, seed, "")
			case "raw chat":
				_, err = svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
			case "responses fallback":
				_, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, body, false)
			case "messages fallback":
				_, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, account, body, "")
			}
			require.NoError(t, err)
			require.Zero(t, upstream.rejected)
			require.Len(t, upstream.bodies, 1)
			key := gjson.GetBytes(upstream.lastBody, "prompt_cache_key")
			if mode == "responses fallback" || mode == "messages fallback" {
				require.False(t, key.Exists(), "existing conversion intentionally omits cache key")
			} else {
				require.Regexp(t, `^[0-9a-f]{64}$`, key.String())
			}
		})
	}
}

func TestBusinessSystemPromptCacheIdentityMessagesContinuationUsesSource(t *testing.T) {
	upstream := &businessCacheStrictUpstream{}
	svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false)}
	account := businessSystemPromptAPIKeyAccount(true)
	seed := "source-session-for-business-cache"
	for turn := 1; turn <= 2; turn++ {
		body := []byte(fmt.Sprintf(`{"model":"gpt-5.3-codex","max_tokens":16,"messages":[{"role":"user","content":"turn %d"}],"stream":false}`, turn))
		c, _ := newBusinessSystemPromptGinContext("/v1/messages", body)
		upstream.responses = []*http.Response{openAICompatSSECompletedResponse(fmt.Sprintf("resp_%d", turn), "gpt-5.3-codex")}
		result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, seed, "")
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("resp_%d", turn), result.ResponseID)
		if turn == 2 {
			require.Equal(t, "resp_1", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
		}
		require.Equal(t, result.ResponseID, svc.getOpenAICompatSessionResponseID(context.Background(), c, account, seed))
		require.NotEqual(t, seed, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	}
	require.Equal(t, gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String(), gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String())
}

func TestBusinessSystemPromptCacheIdentityMessagesRecursiveRetryUsesSource(t *testing.T) {
	for _, unsupported := range []bool{false, true} {
		t.Run(fmt.Sprintf("unsupported=%v", unsupported), func(t *testing.T) {
			upstream := &businessCacheStrictUpstream{}
			svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false)}
			account := businessSystemPromptAPIKeyAccount(true)
			seed := "stable-retry-source"
			body := []byte(`{"model":"gpt-5.5","max_tokens":16,"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"ok"},{"role":"user","content":"continue"}],"stream":false}`)
			c, _ := newBusinessSystemPromptGinContext("/v1/messages", body)
			svc.bindOpenAICompatSessionResponseID(context.Background(), c, account, seed, "resp_missing")
			message := "Previous response with id 'resp_missing' not found."
			if unsupported {
				message = "previous_response_id is only supported on Responses WebSocket v2"
			}
			upstream.responses = []*http.Response{
				{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":%q}}`, message)))},
				openAICompatSSECompletedResponse("resp_retried", "gpt-5.5"),
			}
			result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, seed, "")
			require.NoError(t, err)
			require.Equal(t, "resp_retried", result.ResponseID)
			require.Len(t, upstream.bodies, 2)
			require.False(t, gjson.GetBytes(upstream.bodies[1], "previous_response_id").Exists())
			require.Equal(t, gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String(), gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String())
			require.Equal(t, unsupported, svc.isOpenAICompatSessionContinuationDisabled(context.Background(), c, account, seed))
			require.Equal(t, 1, strings.Count(string(upstream.bodies[1]), "business-server"))
		})
	}
}

func TestBusinessSystemPromptCacheIdentityUnappliedGatewayKeepsClientKey(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 1, Enabled: false, Body: "server"}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	upstream := &businessCacheStrictUpstream{}
	svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
	seed := strings.Repeat("k", 365)
	body := []byte(fmt.Sprintf(`{"model":"gpt-5.4","prompt_cache_key":%q,"input":[],"stream":false}`, seed))
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	_, err := svc.Forward(context.Background(), c, businessSystemPromptAPIKeyAccount(true), body)
	require.Error(t, err) // Invalid client keys remain the upstream's decision.
	require.Equal(t, 1, upstream.rejected)
	require.Equal(t, seed, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
}

func TestBusinessSystemPromptCacheIdentityPassthroughHeaderPriority(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false)}
			account := &Account{ID: 62, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}}
			body := []byte(`{"model":"gpt-5.4","instructions":"client","prompt_cache_key":"source","input":[]}`)
			c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
			if explicit {
				c.Request.Header.Set("session_id", "explicit-session")
				c.Request.Header.Set("conversation_id", "explicit-conversation")
			}
			body, application, err := svc.applyBusinessSystemPromptForRequest(c, body, account, BusinessSystemPromptProtocolResponses, false)
			require.NoError(t, err)
			body, err = rewriteBusinessSystemPromptCacheKey(c, body, application)
			require.NoError(t, err)
			wire := gjson.GetBytes(body, "prompt_cache_key").String()
			for attempt := 0; attempt < 2; attempt++ {
				req, err := svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "test-token")
				require.NoError(t, err)
				session, conversation := wire, wire
				if explicit {
					session, conversation = "explicit-session", "explicit-conversation"
				}
				require.Equal(t, isolateOpenAIUpstreamSessionID(getAPIKeyIDFromContext(c), codexAccountIdentitySource(c, account), session), req.Header.Get("session_id"))
				require.Equal(t, isolateOpenAIUpstreamSessionID(getAPIKeyIDFromContext(c), codexAccountIdentitySource(c, account), conversation), req.Header.Get("conversation_id"))
				sent := readCacheStrictBody(req)
				require.Equal(t, wire, gjson.GetBytes(sent, "prompt_cache_key").String())
			}
		})
	}
}

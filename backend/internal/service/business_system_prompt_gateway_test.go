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
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newGatewayBusinessSystemPromptPolicy(t *testing.T, expose, compact bool) *BusinessSystemPromptService {
	t.Helper()
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Enabled:            true,
		ExposeServerPrompt: expose,
		CompactEnabled:     compact,
		TemplateID:         10,
		VersionID:          20,
		TemplateVersion:    3,
		Revision:           7,
		Body:               "business-server",
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	return policy
}

func newGatewayHybridBusinessSystemPromptPolicy(t *testing.T) *BusinessSystemPromptService {
	return newGatewayHybridBusinessSystemPromptPolicyWithBody(t, embeddedBusinessSystemPrompt, 7)
}

func newGatewayHybridBusinessSystemPromptPolicyWithBody(t *testing.T, body string, revision int64) *BusinessSystemPromptService {
	t.Helper()
	registryFiles := NewRemoteSkillRegistryFilesystem(t.TempDir())
	active, err := registryFiles.LoadSeed(context.Background())
	require.NoError(t, err)
	require.Equal(t, body, active.Prompt.RawBody)
	active.Version.ID = 31
	active.Version.PromptVersionID = 41
	active.Prompt.ID = 41
	registryStore := &fakeRemoteSkillRegistryStore{
		snapshot: RemoteSkillRegistrySnapshot{
			Revision: 11, Active: &active.Version, ActivePrompt: &active.Prompt,
		},
		detail: RemoteSkillBundleVersionDetail{
			RemoteSkillBundleVersion: active.Version,
			Prompt:                   active.Prompt,
			FileChanges:              active.FileChanges,
		},
	}
	registry := NewRemoteSkillRegistryService(registryStore, nil, registryFiles, &fakeRemoteSkillCandidateSource{})
	require.NoError(t, registry.Initialize(context.Background()))

	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Enabled: true, TemplateID: 10, VersionID: 20, TemplateVersion: 3, Revision: revision,
		Body:            body,
		CompositionMode: BusinessSystemPromptCompositionCodexSkillHybrid,
		BundleID:        BusinessSystemPromptRemoteSkillBundleID,
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	policy.SetRemoteSkillRegistryService(registry)
	require.NoError(t, policy.Initialize(context.Background()))
	return policy
}

func TestBusinessSystemPromptHybridUsesSamePairedPublicationForOfficialCodexAndCompatibleClients(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := newGatewayHybridBusinessSystemPromptPolicy(t)
	svc := &OpenAIGatewayService{businessPromptService: policy}
	account := businessSystemPromptAPIKeyAccount(true)
	body := []byte(`{"model":"gpt-5.4","input":"请审计这个接口安全和鉴权流程"}`)

	official, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	official.Request.Header.Set("originator", "codex-tui")
	officialBody, officialApplication, err := svc.applyBusinessSystemPromptForRequest(
		official, body, account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	officialInstructions := gjson.GetBytes(officialBody, "instructions").String()
	require.Contains(t, officialInstructions, RemoteSkillPublicRoot)
	require.Equal(t, 1, strings.Count(officialInstructions, RemoteSkillPublicRoot+"/RULES.md"))
	require.Equal(t, 1, strings.Count(officialInstructions, RemoteSkillPublicRoot+"/README_AI.md"))
	require.Equal(t, 1, strings.Count(officialInstructions, RemoteSkillPublicRoot+"/SKILL.md"))
	require.Equal(t, int64(11), officialApplication.BundleRevision)

	compatible, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	compatible.Request.Header.Set("User-Agent", "compatible-client/1.0")
	compatibleBody, compatibleApplication, err := svc.applyBusinessSystemPromptForRequest(
		compatible, body, account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	require.Equal(t, officialInstructions, gjson.GetBytes(compatibleBody, "instructions").String())
	require.Equal(t, int64(11), compatibleApplication.BundleRevision)

	retried, retriedApplication, err := svc.applyBusinessSystemPromptForRequest(
		compatible, compatibleBody, account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	require.Equal(t, compatibleBody, retried)
	require.Equal(t, compatibleApplication.EffectiveSHA256, retriedApplication.EffectiveSHA256)
}

func TestBusinessSystemPromptHybridPreviewMatchesAppliedBytes(t *testing.T) {
	policy := newGatewayHybridBusinessSystemPromptPolicy(t)
	current, ok := policy.CurrentSnapshot()
	require.True(t, ok)

	for _, clientMode := range []string{"codex", "openai_compatible"} {
		t.Run(clientMode, func(t *testing.T) {
			preview, err := policy.PrepareBusinessSystemPromptPreviewSnapshotForClient(
				current,
				"请审计这个接口安全和鉴权流程",
				clientMode,
			)
			require.NoError(t, err)

			updated, application, err := ApplyBusinessSystemPromptToJSON(
				[]byte(`{"model":"gpt-5.4","input":"preview"}`),
				preview,
				BusinessSystemPromptTarget{Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses},
			)
			require.NoError(t, err)
			require.Equal(t, preview.Body, application.ServerInstructions)
			require.Equal(t, preview.Body, gjson.GetBytes(updated, "instructions").String())
			require.Equal(t, len([]byte(preview.Body)), application.EffectiveByteLength)
			require.Equal(t, hashBusinessSystemPromptBundleBytes([]byte(preview.Body)), application.EffectiveSHA256)

			require.Contains(t, preview.Body, RemoteSkillPublicRoot)
		})
	}
}

func TestBusinessSystemPromptHybridFailsClosedWhenRegistryHasNoPairedPublication(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Enabled: true, TemplateID: 10, VersionID: 20, TemplateVersion: 3, Revision: 7,
		Body: embeddedBusinessSystemPrompt, CompositionMode: BusinessSystemPromptCompositionCodexSkillHybrid,
		BundleID: BusinessSystemPromptRemoteSkillBundleID,
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))

	_, err := policy.PrepareBusinessSystemPromptPreviewSnapshotForClient(store.loaded, "security review", "openai_compatible")
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
}

func TestBusinessSystemPromptHybridKeepsPublishedSourceRootAfterRegistryReloadFailure(t *testing.T) {
	policy := newGatewayHybridBusinessSystemPromptPolicyWithBody(t, embeddedBusinessSystemPrompt, 7)
	registry := policy.registry
	t.Cleanup(registry.Stop)
	store, ok := registry.store.(*fakeRemoteSkillRegistryStore)
	require.True(t, ok)
	store.loadErr = errors.New("remote unavailable")
	require.Error(t, registry.Reload(context.Background()))

	current, ok := policy.CurrentSnapshot()
	require.True(t, ok)
	preview, err := policy.PrepareBusinessSystemPromptPreviewSnapshotForClient(current, "security review", "codex")
	require.NoError(t, err)
	require.Contains(t, preview.Body, RemoteSkillPublicRoot)
	require.True(t, preview.Degraded)
	require.NotContains(t, preview.Body, "registry unavailable")
}

func businessSystemPromptTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	return cfg
}

func businessSystemPromptErrorUpstream() *httpUpstreamRecorder {
	return &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"x-request-id": []string{"business-prompt-test"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"capture complete"}}`)),
	}}
}

func businessSystemPromptAPIKeyAccount(responses bool) *Account {
	return &Account{
		ID: 61, Name: "openai-compatible", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"openai_responses_supported": responses},
	}
}

func newBusinessSystemPromptGinContext(path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func TestBusinessSystemPromptNativeResponsesAppliesForAPIKeyAndOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, account := range map[string]*Account{
		"api key compatible": businessSystemPromptAPIKeyAccount(true),
		"oauth": {
			ID: 62, Name: "openai-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Status: StatusActive, Schedulable: true, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-test", "chatgpt_account_id": "acct-test"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":" client ","prompt_cache_key":"cache","input":[{"role":"user","content":"hello"}]}`)
			c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
			upstream := businessSystemPromptErrorUpstream()
			svc := &OpenAIGatewayService{
				cfg: businessSystemPromptTestConfig(), httpUpstream: upstream,
				businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false),
			}

			result, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Nil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "client\n\nbusiness-server", gjson.GetBytes(upstream.lastBody, "instructions").String())
			require.Contains(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String(), ":business-system-prompt:7:")
			require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
		})
	}
}

func TestBusinessSystemPromptAPIKeyPromptCacheKeyIsNormalizedAfterPromptRewrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"client","prompt_cache_key":"` + strings.Repeat("k", 363) + `","input":[]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	upstream := businessSystemPromptErrorUpstream()
	settings := &SettingService{}
	settings.openAIRefusalRecoveryCache.Store(&cachedOpenAIRefusalRecoveryRuntime{
		runtime:   OpenAIRefusalRecoveryRuntime{APIKeyPromptCacheKeyNormalization: true},
		expiresAt: time.Now().Add(time.Minute).UnixNano(),
	})
	account := businessSystemPromptAPIKeyAccount(true)
	account.Extra["openai_prompt_cache_key_mode"] = OpenAIPromptCacheKeyModeSHA25664
	svc := &OpenAIGatewayService{
		cfg:                   businessSystemPromptTestConfig(),
		httpUpstream:          upstream,
		businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false),
		settingService:        settings,
	}

	result, err := svc.Forward(context.Background(), c, account, body)

	require.Error(t, err)
	require.Nil(t, result)
	require.Regexp(t, `^[0-9a-f]{64}$`, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
}

func TestBusinessSystemPromptPassthroughPromptCacheKeyIsNormalizedAfterPromptRewrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"client","prompt_cache_key":"` + strings.Repeat("p", 363) + `","input":[]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	upstream := businessSystemPromptErrorUpstream()
	settings := &SettingService{}
	settings.openAIRefusalRecoveryCache.Store(&cachedOpenAIRefusalRecoveryRuntime{
		runtime:   OpenAIRefusalRecoveryRuntime{APIKeyPromptCacheKeyNormalization: true},
		expiresAt: time.Now().Add(time.Minute).UnixNano(),
	})
	account := businessSystemPromptAPIKeyAccount(true)
	account.Extra["openai_prompt_cache_key_mode"] = OpenAIPromptCacheKeyModeSHA25664
	svc := &OpenAIGatewayService{
		cfg:                   businessSystemPromptTestConfig(),
		httpUpstream:          upstream,
		businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false),
		settingService:        settings,
	}

	result, err := svc.forwardOpenAIPassthrough(
		context.Background(), c, account, body, body, "gpt-5.4", false, nil, false, time.Now(),
	)

	require.Error(t, err)
	require.Nil(t, result)
	require.Regexp(t, `^[0-9a-f]{64}$`, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
}

func TestBusinessSystemPromptWSV2PromptCacheKeyIsNormalizedAfterPromptRewrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	received := make(chan []byte, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				t.Errorf("close websocket: %v", closeErr)
			}
		}()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read websocket request: %v", err)
			return
		}
		received <- payload
		err = conn.WriteJSON(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_cache_key", "model": "gpt-5.4", "status": "completed", "output": []any{},
				"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
			},
		})
		if err != nil {
			t.Errorf("write websocket response: %v", err)
		}
	}))
	defer wsServer.Close()

	body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":"client","prompt_cache_key":"` + strings.Repeat("w", 363) + `","input":[]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
	SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")
	settings := &SettingService{}
	settings.openAIRefusalRecoveryCache.Store(&cachedOpenAIRefusalRecoveryRuntime{
		runtime:   OpenAIRefusalRecoveryRuntime{APIKeyPromptCacheKeyNormalization: true},
		expiresAt: time.Now().Add(time.Minute).UnixNano(),
	})
	cfg := businessSystemPromptTestConfig()
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	account := businessSystemPromptAPIKeyAccount(true)
	account.Credentials["base_url"] = wsServer.URL
	account.Extra["responses_websockets_v2_enabled"] = true
	account.Extra["openai_prompt_cache_key_mode"] = OpenAIPromptCacheKeyModeSHA25664
	svc := &OpenAIGatewayService{
		cfg: cfg, businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false),
		settingService: settings, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(),
	}

	result, err := svc.Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	payload := <-received
	require.Regexp(t, `^[0-9a-f]{64}$`, gjson.GetBytes(payload, "prompt_cache_key").String())
}

func TestBusinessSystemPromptUpstreamErrorIsSanitizedBeforeInspection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", nil)
	svc := &OpenAIGatewayService{businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false)}
	application := BusinessSystemPromptApplication{
		Applied:            true,
		Carrier:            BusinessSystemPromptCarrierInstructions,
		ClientInstructions: "client",
		ServerInstructions: "business-server",
	}
	c.Set(businessSystemPromptRequestApplicationKey+":"+BusinessSystemPromptProtocolResponses, businessSystemPromptRequestState{
		application: application,
	})
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"message":"invalid request","response":{"instructions":"client\n\nbusiness-server"}}}`,
		)),
	}

	body, message := svc.readOpenAIUpstreamError(resp, c)
	require.Equal(t, "invalid request", message)
	require.Equal(t, "client", gjson.GetBytes(body, "error.response.instructions").String())
	require.NotContains(t, string(body), "business-server")
	rewound, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, body, rewound)
}

func TestBusinessSystemPromptChatAndMessagesBridgeMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := newGatewayBusinessSystemPromptPolicy(t, false, false)

	t.Run("chat to responses", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","messages":[{"role":"system","content":"client-system"},{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/chat/completions", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.ForwardAsChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(true), body, "cache", "gpt-5.4")
		require.Error(t, err)
		require.Equal(t, "business-server", gjson.GetBytes(upstream.lastBody, "instructions").String())
		require.Contains(t, string(upstream.lastBody), "client-system")
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
	})

	t.Run("raw chat", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","messages":[{"role":"developer","content":"client-developer"},{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/chat/completions", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.forwardAsRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, "")
		require.Error(t, err)
		require.Equal(t, "developer", gjson.GetBytes(upstream.lastBody, "messages.0.role").String())
		require.Equal(t, "system", gjson.GetBytes(upstream.lastBody, "messages.1.role").String())
		require.Equal(t, "business-server", gjson.GetBytes(upstream.lastBody, "messages.1.content").String())
		require.Equal(t, "user", gjson.GetBytes(upstream.lastBody, "messages.2.role").String())
	})

	t.Run("responses to chat fallback", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","instructions":"client-responses","input":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, false)
		require.Error(t, err)
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
		require.True(t, chatBodyHasSystemPrompt(upstream.lastBody, "business-server"))
	})

	t.Run("responses compact fallback honors compact switch", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","instructions":"client-responses","input":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/responses/compact", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, true)}
		_, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, true)
		require.Error(t, err)
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
		require.True(t, chatBodyHasSystemPrompt(upstream.lastBody, "business-server"))
	})

	t.Run("responses compact fallback skips business prompt when compact is disabled", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","instructions":"client-responses","input":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/responses/compact", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, true)
		require.Error(t, err)
		require.NotContains(t, string(upstream.lastBody), "business-server")
		require.False(t, chatBodyHasSystemPrompt(upstream.lastBody, "business-server"))
	})

	t.Run("messages to responses", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","max_tokens":32,"system":"client-messages","messages":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/messages", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.ForwardAsAnthropic(context.Background(), c, businessSystemPromptAPIKeyAccount(true), body, "cache", "gpt-5.4")
		require.Error(t, err)
		require.Equal(t, "business-server", gjson.GetBytes(upstream.lastBody, "instructions").String())
		require.Contains(t, string(upstream.lastBody), "client-messages")
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
	})

	t.Run("messages to chat fallback", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","max_tokens":32,"system":"client-messages","messages":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/messages", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, "gpt-5.4")
		require.Error(t, err)
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
		require.True(t, chatBodyHasSystemPrompt(upstream.lastBody, "business-server"))
	})
}

func chatBodyHasSystemPrompt(body []byte, prompt string) bool {
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if message.Get("role").String() == "system" && message.Get("content").String() == prompt {
			return true
		}
	}
	return false
}

func TestBusinessSystemPromptPassthroughCannotSatisfyOAuthInstructionsPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.1-codex","stream":false,"instructions":" ","input":[{"role":"user","content":"hello"}]}`)
	c, recorder := newBusinessSystemPromptGinContext("/v1/responses", body)
	upstream := businessSystemPromptErrorUpstream()
	svc := &OpenAIGatewayService{
		cfg: businessSystemPromptTestConfig(), httpUpstream: upstream,
		businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false),
	}
	account := &Account{
		ID: 63, Name: "passthrough-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Concurrency: 1, Credentials: map[string]any{"access_token": "oauth-test", "chatgpt_account_id": "acct-test"},
	}

	result, err := svc.forwardOpenAIPassthrough(
		context.Background(), c, account, body, body, "gpt-5.1-codex", false, nil, false, time.Now(),
	)
	require.Error(t, err)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "non-empty instructions")
}

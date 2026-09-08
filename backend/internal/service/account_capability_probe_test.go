package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type capabilityProbeFakeUpstream struct {
	requests []*http.Request
	bodies   [][]byte
	proxy    string
	profile  *tlsfingerprint.Profile
	do       func(*http.Request, []byte, int) (*http.Response, error)
}

func (u *capabilityProbeFakeUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxy, accountID, concurrency, nil)
}

func (u *capabilityProbeFakeUpstream) DoWithTLS(req *http.Request, proxy string, _ int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	u.requests = append(u.requests, req)
	u.bodies = append(u.bodies, body)
	u.proxy, u.profile = proxy, profile
	return u.do(req, body, len(u.requests))
}

func capabilityProbeTestService(upstream *capabilityProbeFakeUpstream) *AccountCapabilityProbeService {
	return NewAccountCapabilityProbeService(&AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}},
	})
}

func capabilityProbeTestAccount() *Account {
	return &Account{
		ID: 42, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Concurrency: 3,
		Status: StatusActive, Schedulable: false,
		Credentials: map[string]any{"api_key": "test-secret-do-not-persist", "base_url": "https://relay.example.test/prefix/v1", "model_mapping": map[string]any{"PublicModel": "Unwanted-VIP"}},
		Extra:       map[string]any{"private_flag": true},
	}
}

func capabilityProbeResponse(code int, body string, streaming bool) *http.Response {
	contentType := "application/json"
	if streaming {
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func capabilityProbeTextResponse(protocol string) string {
	switch protocol {
	case AccountCapabilityProtocolResponses:
		return `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}`
	case AccountCapabilityProtocolChatCompletions:
		return `{"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
	default:
		return `{"type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`
	}
}

func TestAccountCapabilityProbePreservesExactTargetAndAccountState(t *testing.T) {
	account := capabilityProbeTestAccount()
	account.Credentials[credKeyHeaderOverrideEnabled] = true
	account.Credentials[credKeyHeaderOverrides] = map[string]any{"x-relay-zone": "zone-2"}
	proxyID := int64(9)
	account.ProxyID = &proxyID
	account.Proxy = &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example.test", Port: 8080}
	before, err := json.Marshal(account)
	require.NoError(t, err)
	upstream := &capabilityProbeFakeUpstream{do: func(req *http.Request, body []byte, count int) (*http.Response, error) {
		require.Equal(t, 1, count)
		require.Equal(t, "PublicModel", gjson.GetBytes(body, "model").String(), "the existing PublicModel -> Unwanted-VIP mapping must never run")
		require.Equal(t, "https://relay.example.test/prefix/v1/responses", req.URL.String())
		require.Equal(t, "Bearer test-secret-do-not-persist", req.Header.Get("Authorization"))
		require.Equal(t, "zone-2", getHeaderRaw(req.Header, "x-relay-zone"))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		require.Nil(t, req.GetBody, "the transport must not replay an inference request")
		deadline, exists := req.Context().Deadline()
		require.True(t, exists)
		require.LessOrEqual(t, time.Until(deadline), 60*time.Second)
		require.Equal(t, int64(256), gjson.GetBytes(body, "max_output_tokens").Int())
		require.False(t, gjson.GetBytes(body, "reasoning").Exists())
		require.False(t, gjson.GetBytes(body, "tools").Exists())
		return capabilityProbeResponse(200, capabilityProbeTextResponse(AccountCapabilityProtocolResponses), false), nil
	}}
	result := capabilityProbeTestService(upstream).Probe(context.Background(), account, "PublicModel", AccountCapabilityProtocolResponses, "")
	require.Equal(t, "alive", result.Status)
	require.Equal(t, AccountCapabilityProfileText, result.Profile)
	require.Equal(t, 1, result.RequestCount)
	require.Len(t, result.Attempts, 1)
	require.Equal(t, "http://proxy.example.test:8080", upstream.proxy)
	require.False(t, result.AccountFailure)
	require.False(t, result.Streaming, "a JSON reply must not be advertised as a streaming proof")
	after, err := json.Marshal(account)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	require.False(t, account.headerOverrideCacheReady)
	wire, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "test-secret")
	require.NotContains(t, string(wire), "Unwanted-VIP")
	require.NotContains(t, string(wire), "relay.example")
	require.NotContains(t, string(wire), `"text":"OK"`)
}

func TestAccountCapabilityProbeRequiresSemanticOutputAndAuthoritativeCompletion(t *testing.T) {
	tests := []struct {
		name, protocol, body, status, classification string
		streaming                                    bool
	}{
		{"responses JSON", "responses", capabilityProbeTextResponse("responses"), "alive", "text_completed", false},
		{"chat JSON", "chat_completions", capabilityProbeTextResponse("chat_completions"), "alive", "text_completed", false},
		{"messages JSON", "messages", capabilityProbeTextResponse("messages"), "alive", "text_completed", false},
		{"responses stream", "responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"output_tokens\":1}}}\n\n", "alive", "text_completed", true},
		{"chat stream finish", "chat_completions", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n", "alive", "text_completed", true},
		{"message stream terminal", "messages", "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\nevent: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {}\n\n", "alive", "text_completed", true},
		{"responses EOF with text", "responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n", "uncertain", "incomplete_response", true},
		{"responses DONE only", "responses", "data: [DONE]\n\n", "uncertain", "incomplete_response", true},
		{"chat DONE not finish", "chat_completions", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n", "uncertain", "incomplete_response", true},
		{"messages DONE not stop", "messages", "data: [DONE]\n\n", "uncertain", "incomplete_response", true},
		{"empty completed", "responses", `{"status":"completed","output":[]}`, "failed", "no_semantic_output", false},
		{"reasoning only", "responses", `{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"private reasoning"}]}]}`, "failed", "no_semantic_output", false},
		{"chat reasoning only", "chat_completions", `{"choices":[{"message":{"reasoning_content":"private reasoning"},"finish_reason":"stop"}]}`, "failed", "no_semantic_output", false},
		{"messages thinking only", "messages", `{"type":"message","role":"assistant","content":[{"type":"thinking","thinking":"private reasoning","signature":"private signature"}],"stop_reason":"end_turn"}`, "failed", "no_semantic_output", false},
		{"usage only", "chat_completions", `{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1}}`, "uncertain", "incomplete_response", false},
		{"responses no status", "responses", `{"output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`, "uncertain", "incomplete_response", false},
		{"responses budget incomplete", "responses", `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`, "failed", "output_budget_exhausted", false},
		{"chat budget incomplete", "chat_completions", `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, "failed", "output_budget_exhausted", false},
		{"message budget incomplete", "messages", `{"type":"message","role":"assistant","content":[{"type":"text","text":"partial"}],"stop_reason":"max_tokens"}`, "failed", "output_budget_exhausted", false},
		{"semantic200error", "responses", "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_api_key\",\"message\":\"test-secret sensitive-provider-error\"}}\n\n", "failed", "request_rejected", true},
		{"invalid JSON", "responses", `{broken`, "uncertain", "invalid_response", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := &capabilityProbeFakeUpstream{do: func(*http.Request, []byte, int) (*http.Response, error) {
				return capabilityProbeResponse(200, test.body, test.streaming), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), capabilityProbeTestAccount(), "Exact/Case-ssvip", test.protocol, "text")
			require.Equal(t, test.status, result.Status)
			require.Equal(t, test.classification, result.Classification)
			require.Equal(t, 1, result.RequestCount)
			require.Len(t, upstream.requests, 1)
			require.False(t, result.AccountFailure)
			wire, err := json.Marshal(result)
			require.NoError(t, err)
			for _, secret := range []string{"test-secret", "sensitive-provider-error", "private reasoning", "private signature"} {
				require.NotContains(t, string(wire), secret)
			}
		})
	}
}

func TestAccountCapabilityProbeErrorClassificationIsAccountScopedOnlyWithEvidence(t *testing.T) {
	tests := []struct {
		name, body, classification, code string
		httpStatus                       int
		accountFailure                   bool
	}{
		{"invalid key", `{"error":{"code":"invalid_api_key","message":"credential-detail-secret"}}`, "credential_invalid", "invalid_api_key", 401, true},
		{"account banned", `{"error":{"type":"account_banned","message":"credential-detail-secret"}}`, "account_disabled", "account_banned", 403, true},
		{"bare401", `{"error":{"message":"credential-detail-secret"}}`, "authentication_failed", "", 401, false},
		{"generic auth401", `{"error":{"type":"authentication_error"}}`, "authentication_failed", "authentication_error", 401, false},
		{"bare403", `{"message":"credential-detail-secret"}`, "permission_denied", "", 403, false},
		{"model400", `{"error":{"code":"model_not_found"}}`, "model_unavailable", "model_not_found", 400, false},
		{"quota", `{"error":{"code":"insufficient_quota"}}`, "quota_exhausted", "insufficient_quota", 429, false},
		{"rate", `{"error":{"type":"rate_limit_error"}}`, "rate_limited", "rate_limit_error", 429, false},
		{"outage", `{"error":{"message":"credential-detail-secret"}}`, "upstream_unavailable", "", 503, false},
		{"redirect", `{"location":"credential-detail-secret"}`, "redirect_blocked", "", 302, false},
		{"unsafe error code", `{"error":{"code":"sk-private-credential-detail-secret"}}`, "request_rejected", "", 400, false},
		{"invalid key wrong HTTP", `{"error":{"code":"invalid_api_key"}}`, "upstream_unavailable", "invalid_api_key", 502, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := capabilityProbeTestAccount()
			upstream := &capabilityProbeFakeUpstream{do: func(*http.Request, []byte, int) (*http.Response, error) {
				return capabilityProbeResponse(test.httpStatus, test.body, false), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), account, "OneModel", "responses", "text")
			require.Equal(t, test.classification, result.Classification)
			require.Equal(t, test.code, result.ErrorCode)
			require.Equal(t, test.httpStatus, result.HTTPStatus)
			require.Equal(t, test.accountFailure, result.AccountFailure)
			require.Equal(t, 1, result.RequestCount)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, StatusActive, account.Status)
			require.False(t, account.Schedulable)
			require.Empty(t, account.ErrorMessage)
			require.Nil(t, account.RateLimitedAt)
			wire, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(wire), "credential-detail-secret")
		})
	}
}

func TestAccountCapabilityProbeUnsupportedAndCanceledSendNothing(t *testing.T) {
	tests := []struct {
		name, model, protocol, profile string
		prepare                        func(*Account)
		cancel                         bool
	}{
		{name: "unknown protocol", model: "Exact", protocol: "guess_endpoint"},
		{name: "unknown profile", model: "Exact", protocol: "responses", profile: "retry_until_green"},
		{name: "empty model", protocol: "responses"},
		{name: "whitespace model is not rewritten", model: " Exact ", protocol: "responses"},
		{name: "OAuth never refreshed", model: "Exact", protocol: "responses", prepare: func(a *Account) { a.Type = AccountTypeOAuth }},
		{name: "missing key", model: "Exact", protocol: "responses", prepare: func(a *Account) { delete(a.Credentials, "api_key") }},
		{name: "canceled before send", model: "Exact", protocol: "responses", cancel: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := capabilityProbeTestAccount()
			if test.prepare != nil {
				test.prepare(account)
			}
			upstream := &capabilityProbeFakeUpstream{do: func(*http.Request, []byte, int) (*http.Response, error) {
				t.Fatal("unexpected request")
				return nil, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancel {
				cancel()
			}
			result := capabilityProbeTestService(upstream).Probe(ctx, account, test.model, test.protocol, test.profile)
			require.NotEqual(t, "alive", result.Status)
			require.Zero(t, result.RequestCount)
			require.Empty(t, upstream.requests)
			require.False(t, result.AccountFailure)
		})
	}
}

func TestAccountCapabilityProbeTransportFailureNeverRetriesOrExposesURL(t *testing.T) {
	upstream := &capabilityProbeFakeUpstream{do: func(*http.Request, []byte, int) (*http.Response, error) {
		return nil, errors.New("failed https://host.example?api_key=private-secret")
	}}
	result := capabilityProbeTestService(upstream).Probe(context.Background(), capabilityProbeTestAccount(), "OneModel", "responses", "text")
	require.Equal(t, "uncertain", result.Status)
	require.Equal(t, "network_error", result.Classification)
	require.Equal(t, 1, result.RequestCount)
	require.Len(t, upstream.requests, 1)
	wire, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "private-secret")
	require.NotContains(t, string(wire), "host.example")
}

func TestAccountCapabilityProbeNativeDeepseekUsesConfiguredWireURL(t *testing.T) {
	for _, test := range []struct{ protocol, base, path string }{
		{"responses", "https://deepseek-relay.example.test", "/responses"},
		{"chat_completions", "https://deepseek-relay.example.test/v1", "/v1/chat/completions"},
		{"messages", "https://deepseek-relay.example.test/anthropic/v1", "/anthropic/v1/v1/messages"},
	} {
		t.Run(test.protocol, func(t *testing.T) {
			account := capabilityProbeTestAccount()
			account.Platform = PlatformDeepseek
			account.Credentials["base_url"] = test.base
			account.Credentials["api_protocol"] = test.protocol
			if test.protocol == "messages" {
				account.Credentials["api_protocol"] = APIProtocolAnthropic
			}
			upstream := &capabilityProbeFakeUpstream{do: func(req *http.Request, body []byte, _ int) (*http.Response, error) {
				require.Equal(t, test.path, req.URL.Path)
				require.Equal(t, "deepseek-relay.example.test", req.URL.Host)
				require.Equal(t, "Exact/DeepSeek-V4", gjson.GetBytes(body, "model").String())
				if test.protocol == "messages" {
					require.Equal(t, "test-secret-do-not-persist", req.Header.Get("X-API-Key"))
				}
				return capabilityProbeResponse(200, capabilityProbeTextResponse(test.protocol), false), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), account, "Exact/DeepSeek-V4", test.protocol, "text")
			require.Equal(t, "alive", result.Status)
		})
	}
}

func TestAccountCapabilityProbeAnthropicMatchesRuntimeURL(t *testing.T) {
	for _, base := range []string{"https://relay.example.test/prefix", "https://relay.example.test/prefix/v1"} {
		t.Run(base, func(t *testing.T) {
			account := capabilityProbeTestAccount()
			account.Platform = PlatformAnthropic
			account.Credentials["base_url"] = base
			account.Extra[anthropicAPIKeyAuthSchemeExtraKey] = AnthropicAPIKeyAuthSchemeAuthorizationBearer
			upstream := &capabilityProbeFakeUpstream{do: func(req *http.Request, body []byte, count int) (*http.Response, error) {
				require.Equal(t, 1, count)
				require.Equal(t, base+"/v1/messages?beta=true", req.URL.String())
				require.Equal(t, "Bearer test-secret-do-not-persist", req.Header.Get("Authorization"))
				require.Equal(t, "Exact/Claude", gjson.GetBytes(body, "model").String())
				return capabilityProbeResponse(200, capabilityProbeTextResponse("messages"), false), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), account, "Exact/Claude", "messages", "text")
			require.Equal(t, "alive", result.Status)
		})
	}
}

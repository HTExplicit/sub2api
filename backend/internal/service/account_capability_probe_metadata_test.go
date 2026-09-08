package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountCapabilityMetadataProbePreservesWireAndIsNotGenerationProof(t *testing.T) {
	cases := []struct {
		name, platform, protocol, baseURL, targetURL, authHeader, authValue, bodyKey string
		bearer                                                                       bool
	}{
		{
			name: "responses preserves versioned relay", platform: PlatformOpenAI,
			protocol: AccountCapabilityProtocolResponsesInputTokens,
			baseURL:  "https://relay.example.test/prefix/v1", targetURL: "https://relay.example.test/prefix/v1/responses/input_tokens",
			authHeader: "Authorization", authValue: "Bearer test-secret-do-not-persist", bodyKey: "input",
		},
		{
			name: "messages native key", platform: PlatformAnthropic,
			protocol: AccountCapabilityProtocolMessagesCountTokens,
			baseURL:  "https://relay.example.test/prefix", targetURL: "https://relay.example.test/prefix/v1/messages/count_tokens?beta=true",
			authHeader: "X-API-Key", authValue: "test-secret-do-not-persist", bodyKey: "messages",
		},
		{
			name: "messages configured bearer", platform: PlatformAnthropic,
			protocol: AccountCapabilityProtocolMessagesCountTokens,
			baseURL:  "https://relay.example.test/prefix", targetURL: "https://relay.example.test/prefix/v1/messages/count_tokens?beta=true",
			authHeader: "Authorization", authValue: "Bearer test-secret-do-not-persist", bodyKey: "messages", bearer: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := capabilityProbeTestAccount()
			account.Platform = tc.platform
			account.Credentials["base_url"] = tc.baseURL
			account.Credentials[credKeyHeaderOverrideEnabled] = true
			account.Credentials[credKeyHeaderOverrides] = map[string]any{"x-relay-zone": "zone-2", "accept": "application/vnd.example.count+json"}
			if tc.bearer {
				account.Extra[anthropicAPIKeyAuthSchemeExtraKey] = AnthropicAPIKeyAuthSchemeAuthorizationBearer
			}
			proxyID := int64(9)
			account.ProxyID = &proxyID
			account.Proxy = &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example.test", Port: 8080}
			before, err := json.Marshal(account)
			require.NoError(t, err)
			upstream := &capabilityProbeFakeUpstream{do: func(req *http.Request, body []byte, count int) (*http.Response, error) {
				require.Equal(t, 1, count)
				require.Equal(t, http.MethodPost, req.Method)
				require.Equal(t, tc.targetURL, req.URL.String())
				require.Equal(t, tc.authValue, req.Header.Get(tc.authHeader))
				require.Equal(t, "zone-2", getHeaderRaw(req.Header, "x-relay-zone"))
				require.Equal(t, "application/vnd.example.count+json", getHeaderRaw(req.Header, "accept"))
				require.Equal(t, "application/json", req.Header.Get("Content-Type"))
				require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
				require.Nil(t, req.GetBody)
				deadline, ok := req.Context().Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), 60*time.Second)
				var payload map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(body, &payload))
				require.Len(t, payload, 2, "count requests contain only model and the short input")
				require.Equal(t, "PublicModel", gjson.GetBytes(body, "model").String(), "the account's private alias must not run")
				require.Equal(t, accountCapabilityTextPrompt, gjson.GetBytes(body, tc.bodyKey+".0.content").String())
				return capabilityProbeResponse(http.StatusOK, `{"input_tokens":0}`, false), nil
			}}
			svc := capabilityProbeTestService(upstream)
			svc.accountTests.tlsFPProfileService = &TLSFingerprintProfileService{}
			result := svc.Probe(context.Background(), account, "PublicModel", tc.protocol, "")
			require.Equal(t, "available", result.Status)
			require.Equal(t, "metadata_available", result.Classification)
			require.Contains(t, result.Reason, "generation was not tested")
			require.Equal(t, tc.protocol, result.Protocol)
			require.Equal(t, AccountCapabilityProfileText, result.Profile)
			require.Equal(t, "PublicModel", result.UpstreamModel)
			require.Equal(t, 1, result.RequestCount)
			require.Len(t, result.Attempts, 1)
			require.Equal(t, "available", result.Attempts[0].Status)
			require.False(t, result.Streaming)
			require.False(t, result.AccountFailure)
			require.NotNil(t, result.Usage.InputTokens)
			require.Zero(t, *result.Usage.InputTokens, "zero is valid, not an absent observation")
			require.Nil(t, result.Usage.OutputTokens)
			require.Nil(t, result.Usage.TotalTokens)
			require.Equal(t, "http://proxy.example.test:8080", upstream.proxy)
			require.Nil(t, upstream.profile, "ordinary API-key accounts do not enable TLS fingerprinting")
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			require.False(t, account.headerOverrideCacheReady)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			for _, hidden := range []string{"test-secret", "relay.example", "proxy.example", "Unwanted-VIP", accountCapabilityTextPrompt} {
				require.NotContains(t, string(encoded), hidden)
			}
		})
	}
}

func TestAccountCapabilityMetadataInputTokensRejectsInvalidOrErrorEnvelope(t *testing.T) {
	cases := []struct {
		name, body string
		valid      bool
		errorBody  bool
		tokens     int64
	}{
		{name: "zero", body: `{"input_tokens":0}`, valid: true},
		{name: "positive", body: `{"object":"response.input_tokens","input_tokens":19}`, valid: true, tokens: 19},
		{name: "maximum int64", body: `{"input_tokens":9223372036854775807}`, valid: true, tokens: 9223372036854775807},
		{name: "null error is no error", body: `{"input_tokens":2,"error":null}`, valid: true, tokens: 2},
		{name: "missing", body: `{}`},
		{name: "null", body: `{"input_tokens":null}`},
		{name: "negative", body: `{"input_tokens":-1}`},
		{name: "string", body: `{"input_tokens":"2"}`},
		{name: "boolean", body: `{"input_tokens":true}`},
		{name: "decimal", body: `{"input_tokens":1.0}`},
		{name: "fraction", body: `{"input_tokens":1.5}`},
		{name: "exponent", body: `{"input_tokens":1e2}`},
		{name: "overflow", body: `{"input_tokens":9223372036854775808}`},
		{name: "array", body: `[{"input_tokens":1}]`},
		{name: "trailing object", body: `{"input_tokens":1}{}`},
		{name: "truncated", body: `{"input_tokens":1`},
		{name: "error object", body: `{"input_tokens":1,"error":{"code":"invalid_api_key","message":"secret upstream error"}}`, errorBody: true},
		{name: "empty error object", body: `{"input_tokens":1,"error":{}}`, errorBody: true},
		{name: "top error type", body: `{"input_tokens":1,"type":"error"}`, errorBody: true},
		{name: "failed status", body: `{"input_tokens":1,"status":"failed"}`, errorBody: true},
		{name: "false success", body: `{"input_tokens":1,"success":false}`, errorBody: true},
		{name: "false ok", body: `{"input_tokens":1,"ok":false}`, errorBody: true},
		{name: "error code", body: `{"input_tokens":1,"code":"model_not_found"}`, errorBody: true},
		{name: "nested unclassified error", body: `{"input_tokens":0,"response":{"error":{"message":"secret upstream failure"}}}`, errorBody: true},
		{name: "nested failed status", body: `{"input_tokens":0,"response":{"status":"failed"}}`, errorBody: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, valid, errorBody := accountCapabilityMetadataInputTokens([]byte(tc.body))
			require.Equal(t, tc.valid, valid)
			require.Equal(t, tc.errorBody, errorBody)
			if tc.valid {
				require.Equal(t, tc.tokens, tokens)
			}
		})
	}
}

func TestAccountCapabilityMetadataProbeFailuresDoNotRetryOrChangeAccount(t *testing.T) {
	cases := []struct {
		name, body, status, classification string
		httpStatus                         int
		err                                error
		accountFailure                     bool
	}{
		{name: "metadata endpoint unsupported", httpStatus: 404, body: `{"error":{"type":"not_found_error","message":"secret"}}`, status: "unsupported", classification: "protocol_unsupported"},
		{name: "no redirect following", httpStatus: 302, body: ``, status: "failed", classification: "redirect_blocked"},
		{name: "limited", httpStatus: 429, body: `{"error":{"type":"rate_limit_error","message":"secret"}}`, status: "failed", classification: "rate_limited"},
		{name: "explicit credential failure is only evidence", httpStatus: 401, body: `{"error":{"code":"invalid_api_key","message":"secret"}}`, status: "failed", classification: "credential_invalid", accountFailure: true},
		{name: "ambiguous forbidden does not condemn account", httpStatus: 403, body: `{"error":{"message":"secret"}}`, status: "failed", classification: "permission_denied"},
		{name: "error in HTTP 200", httpStatus: 200, body: `{"input_tokens":1,"error":{"code":"invalid_api_key","message":"secret"}}`, status: "failed", classification: "request_rejected"},
		{name: "missing token count", httpStatus: 200, body: `{"usage":{"input_tokens":1}}`, status: "uncertain", classification: "invalid_response"},
		{name: "oversized body", httpStatus: 200, body: `{"input_tokens":1,"padding":"` + strings.Repeat("x", accountCapabilityBodyLimit) + `"}`, status: "uncertain", classification: "invalid_response"},
		{name: "network failure", err: errors.New("https://relay.example.test?api_key=secret"), status: "uncertain", classification: "network_error"},
		{name: "timeout", err: context.DeadlineExceeded, status: "uncertain", classification: "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := capabilityProbeTestAccount()
			before, err := json.Marshal(account)
			require.NoError(t, err)
			upstream := &capabilityProbeFakeUpstream{do: func(_ *http.Request, _ []byte, count int) (*http.Response, error) {
				require.Equal(t, 1, count)
				if tc.err != nil {
					return nil, tc.err
				}
				return capabilityProbeResponse(tc.httpStatus, tc.body, false), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), account, "PublicModel", AccountCapabilityProtocolResponsesInputTokens, AccountCapabilityProfileText)
			require.Equal(t, tc.status, result.Status)
			require.Equal(t, tc.classification, result.Classification)
			require.Equal(t, tc.accountFailure, result.AccountFailure)
			require.Equal(t, 1, result.RequestCount)
			require.Len(t, upstream.requests, 1)
			require.Len(t, result.Attempts, 1)
			require.Nil(t, result.Usage.InputTokens)
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "secret")
			require.NotContains(t, string(encoded), "relay.example")
		})
	}
}

func TestAccountCapabilityMetadataProbeRejectsToolsAndCanceledBeforeSending(t *testing.T) {
	for _, protocol := range []string{AccountCapabilityProtocolResponsesInputTokens, AccountCapabilityProtocolMessagesCountTokens} {
		t.Run(protocol, func(t *testing.T) {
			upstream := &capabilityProbeFakeUpstream{do: func(_ *http.Request, _ []byte, _ int) (*http.Response, error) {
				t.Fatal("invalid profile or canceled metadata must not send a request")
				return nil, nil
			}}
			svc := capabilityProbeTestService(upstream)
			result := svc.Probe(context.Background(), capabilityProbeTestAccount(), "PublicModel", protocol, AccountCapabilityProfileToolRoundtrip)
			require.Equal(t, "unsupported", result.Status)
			require.Equal(t, "unsupported_profile", result.Classification)
			require.Zero(t, result.RequestCount)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result = svc.Probe(ctx, capabilityProbeTestAccount(), "PublicModel", protocol, AccountCapabilityProfileText)
			require.Equal(t, "canceled", result.Status)
			require.Zero(t, result.RequestCount)
			require.Empty(t, upstream.requests)
		})
	}
}

func TestAccountCapabilityMetadataProbeMatchesRuntimeCountEndpoint(t *testing.T) {
	cases := []struct {
		name, platform, protocol, baseURL, wantURL string
		adaptive                                   bool
	}{
		{name: "DeepSeek count is not platform inference path", platform: PlatformDeepseek, protocol: AccountCapabilityProtocolResponsesInputTokens, baseURL: "https://relay.example.test", wantURL: "https://relay.example.test/v1/responses/input_tokens"},
		{name: "Anthropic count keeps existing duplicate version behavior", platform: PlatformAnthropic, protocol: AccountCapabilityProtocolMessagesCountTokens, baseURL: "https://relay.example.test/v1", wantURL: "https://relay.example.test/v1/v1/messages/count_tokens?beta=true"},
		{name: "adaptive count uses runtime base and host", platform: PlatformDeepseek, protocol: AccountCapabilityProtocolResponsesInputTokens, baseURL: "https://relay.example.test", wantURL: "https://count.example.test/v1/responses/input_tokens", adaptive: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := capabilityProbeTestAccount()
			account.Platform = tc.platform
			account.Credentials["base_url"] = tc.baseURL
			if tc.adaptive {
				account.Credentials["api_protocol"] = APIProtocolAdaptive
				account.Credentials["api_base_urls"] = map[string]any{
					APIProtocolResponses:       "https://inference.example.test/v1",
					APIProtocolChatCompletions: "https://count.example.test/v1",
				}
			}
			upstream := &capabilityProbeFakeUpstream{do: func(req *http.Request, _ []byte, count int) (*http.Response, error) {
				require.Equal(t, tc.wantURL, req.URL.String())
				require.Equal(t, req.URL.Host, req.Host)
				require.Equal(t, "application/json", req.Header.Get("Accept"))
				require.Equal(t, 1, count)
				return capabilityProbeResponse(http.StatusOK, `{"input_tokens":1}`, false), nil
			}}
			result := capabilityProbeTestService(upstream).Probe(context.Background(), account, "Vendor/Exact-MiXeD-VIP", tc.protocol, AccountCapabilityProfileText)
			require.Equal(t, "available", result.Status)
			require.Equal(t, "Vendor/Exact-MiXeD-VIP", result.UpstreamModel)
		})
	}
}

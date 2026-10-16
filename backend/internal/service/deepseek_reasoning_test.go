package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type deepSeekSettingsRepo struct {
	SettingRepository
	raw               string
	readErr, writeErr error
}

func (r *deepSeekSettingsRepo) GetValue(context.Context, string) (string, error) {
	return r.raw, r.readErr
}
func (r *deepSeekSettingsRepo) Set(_ context.Context, key, value string) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	r.raw = value
	return nil
}

func enabledDeepSeekReasoning() *DeepSeekReasoningService {
	s := NewDeepSeekReasoningService(nil)
	s.publish(DeepSeekReasoningConfig{Enabled: true, Effort: "max"})
	return s
}

func TestDeepSeekDefaultSettings(t *testing.T) {
	repo := &deepSeekSettingsRepo{}
	s := NewDeepSeekReasoningService(repo)
	config, err := s.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, DefaultDeepSeekReasoningConfig(), config)
	require.Error(t, s.Save(context.Background(), DeepSeekReasoningConfig{Enabled: true, Effort: "medium"}))
	require.Empty(t, repo.raw)
	want := DeepSeekReasoningConfig{Enabled: true, Effort: "max"}
	require.NoError(t, s.Save(context.Background(), want))
	restarted := NewDeepSeekReasoningService(repo)
	config, err = restarted.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, config)
	repo.writeErr = errors.New("store unavailable")
	require.Error(t, s.Save(context.Background(), DeepSeekReasoningConfig{Effort: "low"}))
	require.Equal(t, want, s.current())
	repo.readErr = errors.New("store unavailable")
	_, err = s.Get(context.Background())
	require.Error(t, err)
	require.Equal(t, want, s.current(), "refresh failure keeps last good settings")
	firstFailure := NewDeepSeekReasoningService(repo)
	_, err = firstFailure.Get(context.Background())
	require.Error(t, err)
	require.False(t, firstFailure.current().Enabled)
}

func TestDeepSeekDefaultEligibilityAndPolicy(t *testing.T) {
	config := DeepSeekReasoningConfig{Enabled: true, Effort: "max"}
	for _, tc := range []struct {
		name, fields string
		changed      bool
	}{
		{"missing", ``, true},
		{"null", `,"reasoning":null`, true},
		{"empty_effort", `,"reasoning":{"effort":""}`, true},
		{"null_effort", `,"reasoning":{"effort":null}`, true},
		{"enabled_only", `,"thinking":{"type":"enabled"}`, true},
		{"explicit", `,"reasoning":{"effort":"high"}`, false},
		{"none", `,"reasoning":{"effort":"none"}`, false},
		{"disabled", `,"thinking":{"type":"disabled"}`, false},
		{"unknown", `,"reasoning_effort":"future"`, false},
		{"invalid_effort", `,"output_config":{"effort":10}`, false},
		{"invalid_parent", `,"reasoning":[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"deepseek-flash","input":"hi"` + tc.fields + `}`)
			out, changed, err := applyDeepSeekDefault(body, nil, "deepseek-flash", APIProtocolResponses, config, nil)
			require.NoError(t, err)
			require.Equal(t, tc.changed, changed)
			if changed {
				require.Equal(t, "max", gjson.GetBytes(out, "reasoning.effort").String())
			} else {
				require.Equal(t, string(body), string(out))
			}
		})
	}
	body := []byte(`{"model":"alias","input":"hi","reasoning":{"summary":"auto","future":9007199254740993}}`)
	for _, tc := range []struct {
		model            string
		enabled, changed bool
	}{
		{"deepseek-flash", true, true}, {"provider/DeepSeek-V4-Pro", true, true},
		{"deepseek-reasoner", true, false}, {"gpt-6-astra", true, false}, {"deepseek-flash", false, false},
	} {
		config.Enabled = tc.enabled
		out, changed, err := applyDeepSeekDefault(body, nil, tc.model, APIProtocolResponses, config, nil)
		require.NoError(t, err)
		require.Equal(t, tc.changed, changed, tc.model)
		require.Equal(t, "9007199254740993", gjson.GetBytes(out, "reasoning.future").Raw)
	}
	config.Enabled = true
	group := &Group{MaxReasoningEffort: "high", MaxReasoningEffortOverLimit: "downgrade"}
	out, _, err := applyDeepSeekDefault(body, nil, "deepseek-flash", APIProtocolResponses, config, group)
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(out, "reasoning.effort").String())
	group.MaxReasoningEffortOverLimit = "deny"
	_, _, err = applyDeepSeekDefault(body, nil, "deepseek-flash", APIProtocolResponses, config, group)
	require.True(t, IsReasoningEffortPolicyDenied(err))
	group = &Group{ReasoningEffortMappings: []ReasoningEffortMapping{{From: "max", To: "high"}, {From: "high", To: "low"}}}
	out, changed, err := applyDeepSeekDefault(body, nil, "deepseek-flash", APIProtocolResponses, config, group)
	require.NoError(t, err)
	require.True(t, changed)
	out, changed, err = applyDeepSeekDefault(out, nil, "deepseek-flash", APIProtocolResponses, config, group)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "high", gjson.GetBytes(out, "reasoning.effort").String(), "mapping must run once")
}

func TestDeepSeekDefaultSnapshotFailoverAndIntegrity(t *testing.T) {
	s := enabledDeepSeekReasoning()
	body := []byte(`{"model":"alias","input":"hi","reasoning":{"summary":"auto"}}`)
	c := newDeepSeekChatFallbackContext(t, body)
	account := openaiMappedDeepSeekResponsesImageAccount()
	account.Credentials["model_mapping"] = map[string]any{"alias": "deepseek-flash"}
	out, changed, replay, err := s.prepare(c, account, body, resolveMappedUpstreamModel(account, body, ""), APIProtocolResponses)
	require.NoError(t, err)
	require.True(t, changed)
	s.publish(DeepSeekReasoningConfig{Enabled: true, Effort: "low"})
	retried, _, _, err := s.prepare(c, account, body, "deepseek-flash", APIProtocolResponses)
	require.NoError(t, err)
	require.JSONEq(t, string(out), string(retried))
	other, changed, _, err := s.prepare(c, account, body, "gpt-6-astra", APIProtocolResponses)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(other))
	field, err := compareRequestIntegrity(account, requestIntegrityOptions{UpstreamModel: "deepseek-flash", EffortPolicy: replay}, body, out)
	require.NoError(t, err)
	require.Empty(t, field)
	require.Nil(t, requestedEffortForPersistence(nil, optionalTrimmedStringPtr("max"), true))
	require.Equal(t, "max", *requestedEffortForPersistence(nil, optionalTrimmedStringPtr("max"), false))
	c2 := newDeepSeekChatFallbackContext(t, body)
	_, _, _, err = s.prepare(c2, account, body, "deepseek-flash", APIProtocolResponses)
	require.NoError(t, err)
	require.Equal(t, "low", s.requestConfig(c2).Effort)
}

// Exercise real dispatch and conversion, capture the body at the HTTP boundary,
// and supply a completed synthetic response. No provider or model is contacted.
func TestDeepSeekDefaultForwardProtocols(t *testing.T) {
	for _, inbound := range []string{APIProtocolResponses, APIProtocolChatCompletions, APIProtocolAnthropic} {
		for _, outbound := range []string{APIProtocolResponses, APIProtocolChatCompletions, APIProtocolAnthropic} {
			t.Run(inbound+"_to_"+outbound, func(t *testing.T) {
				body := []byte(`{"model":"alias","input":"hi"}`)
				if inbound != APIProtocolResponses {
					body = []byte(`{"model":"alias","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
				}
				c := newDeepSeekChatFallbackContext(t, body)
				account := &Account{ID: 18, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "api_protocol": outbound, "model_mapping": map[string]any{"alias": "deepseek-flash"}}, Status: StatusActive, Schedulable: true}
				response := `{"id":"resp_test","object":"response","status":"completed","model":"deepseek-flash","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`
				contentType := "application/json"
				if outbound == APIProtocolChatCompletions {
					response = deepSeekChatFallbackOKBody
				}
				if outbound == APIProtocolAnthropic {
					contentType = "text/event-stream"
					response = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-flash\",\"content\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				if outbound == APIProtocolResponses && inbound != APIProtocolResponses {
					response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
					contentType = "text/event-stream"
				}
				if outbound == APIProtocolAnthropic && inbound == APIProtocolAnthropic {
					response = `{"id":"msg_test","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`
					contentType = "application/json"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := &OpenAIGatewayService{cfg: deepSeekChatFallbackTestConfig(), httpUpstream: upstream, deepseekReasoning: enabledDeepSeekReasoning()}
				var result *OpenAIForwardResult
				var err error
				switch inbound {
				case APIProtocolResponses:
					result, err = svc.Forward(context.Background(), c, account, body)
				case APIProtocolChatCompletions:
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				case APIProtocolAnthropic:
					result, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				path := "reasoning.effort"
				if outbound == APIProtocolChatCompletions {
					path = "reasoning_effort"
				}
				if outbound == APIProtocolAnthropic {
					path = "output_config.effort"
				}
				require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, path).String(), string(upstream.lastBody))
				require.True(t, result.ReasoningEffortDefaulted)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "max", *result.ReasoningEffort)
			})
		}
	}
}

func TestDeepSeekDefaultDenyBeforeDispatch(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","input":"hi"}`)
	c := newDeepSeekChatFallbackContext(t, body)
	c.Set("api_key", &APIKey{Group: &Group{MaxReasoningEffort: "high", MaxReasoningEffortOverLimit: "deny"}})
	_, _, _, err := enabledDeepSeekReasoning().prepare(c, nil, body, "deepseek-flash", APIProtocolResponses)
	require.True(t, IsReasoningEffortPolicyDenied(err))
	require.Equal(t, 403, c.Writer.Status())
}

func TestDeepSeekDefaultDeclaredCapabilities(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	yes := true
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"relay/deepseek-future": {ID: "relay/deepseek-future", Reasoning: &yes, SupportedReasoningLevels: []string{"low", "max"}},
	}})
	require.True(t, deepSeekSupportsDefaultEffort(account, "relay/deepseek-future", "max"))
	require.False(t, deepSeekSupportsDefaultEffort(account, "relay/deepseek-future", "high"))
	require.False(t, deepSeekSupportsDefaultEffort(account, "deepseek-future", "max"))
	body := []byte(`{"model":"gpt-5.4-none","input":"hi"}`)
	_, changed, err := applyDeepSeekDefault(body, account, "deepseek-flash", APIProtocolResponses, DeepSeekReasoningConfig{Enabled: true, Effort: "max"}, nil)
	require.NoError(t, err)
	require.False(t, changed)
}

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
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func promptV2GatewayFixture(t *testing.T, platform, content, format string) (*BusinessSystemPromptService, *gin.Context, extensionv1.PromptRule) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rule := extensionv1.PromptRule{ID: "prompt", Name: "Prompt", Enabled: true, TemplateID: 1, VersionID: 2, Order: 100, Role: "auto", Platforms: []string{platform}, Position: "control_append", ModelMatch: "upstream", Models: []string{}}
	if format == extensionv1.PromptContentAnthropicSystemBlocks {
		rule.AccountTypes = []string{AccountTypeOAuth, AccountTypeSetupToken}
		rule.RequestProfiles = []string{"generic-mimic"}
		rule.ExcludeModelContains = []string{"fable"}
	}
	hash, _, err := extensionv1.ValidateTextDocument(content, extensionv1.PromptRulesMaxBytes)
	require.NoError(t, err)
	resolved := extensionv1.ResolvedPromptRule{Rule: rule, Body: content, SHA256: hash, ContentFormat: format}
	if format != "" {
		resolved.StructuredContent = json.RawMessage(content)
	}
	snapshot := BusinessSystemPromptSnapshot{Enabled: true, Revision: 9, RulePolicy: &extensionv1.PromptRulePolicy{Version: 2, Rules: []extensionv1.PromptRule{rule}, DefaultRuleIDs: []string{rule.ID}}, ResolvedRules: []extensionv1.ResolvedPromptRule{resolved}}
	prompts := NewBusinessSystemPromptService(nil, nil)
	prompts.snapshot.Store(&snapshot)
	businessSystemPromptRequestSet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestSnapshotKey, ""), snapshot)
	return prompts, c, rule
}

func TestPromptV2GatewayMessagesFinalBuilders(t *testing.T) {
	for _, path := range []string{"native", "passthrough", "count", "passthrough-count"} {
		t.Run(path, func(t *testing.T) {
			prompts, c, _ := promptV2GatewayFixture(t, PlatformAnthropic, "site-prompt-v2", "")
			svc := &GatewayService{businessPromptService: prompts, cfg: &config.Config{}}
			account := &Account{ID: 11, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
			clean := []byte(`{"model":"claude-sonnet-4-6","system":[{"type":"text","text":"client","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"fixture"}}]}]}`)
			for range 2 {
				var req *http.Request
				var err error
				switch path {
				case "native":
					req, _, err = svc.buildUpstreamRequest(context.Background(), c, account, clean, "fixture", "apikey", "claude-sonnet-4-6", false, false)
				case "passthrough":
					req, _, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, clean, "fixture")
				case "count":
					req, _, err = svc.buildCountTokensRequest(context.Background(), c, account, clean, "fixture", "apikey", "claude-sonnet-4-6", false)
				default:
					req, err = svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, clean, "fixture")
				}
				require.NoError(t, err)
				wire, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				require.Equal(t, 1, strings.Count(string(wire), "site-prompt-v2"))
				require.Equal(t, "client", gjson.GetBytes(wire, "system.0.text").String())
				require.Equal(t, "1h", gjson.GetBytes(wire, "system.0.cache_control.ttl").String())
				require.Equal(t, "site-prompt-v2", gjson.GetBytes(wire, "system.1.text").String())
				require.Equal(t, "image", gjson.GetBytes(wire, "messages.0.content.1.type").String())
			}
			require.NotContains(t, string(clean), "site-prompt-v2")
		})
	}
}

type promptV2CaptureUpstream struct{ bodies [][]byte }

func (u *promptV2CaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.bodies = append(u.bodies, body)
	return nil, errors.New("offline capture complete")
}

func (u *promptV2CaptureUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

func TestPromptV2GatewayGeminiFinalRequests(t *testing.T) {
	for _, ingress := range []string{"messages", "chat", "gemini", "countTokens"} {
		t.Run(ingress, func(t *testing.T) {
			prompts, c, _ := promptV2GatewayFixture(t, PlatformGemini, "site-prompt-v2", "")
			upstream := &promptV2CaptureUpstream{}
			svc := &GeminiMessagesCompatService{businessPromptService: prompts, cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 12, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}}
			body := []byte(`{"model":"gemini-2.5-flash","system":"client","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`)
			var err error
			switch ingress {
			case "messages":
				_, err = svc.Forward(context.Background(), c, account, body)
			case "chat":
				body = []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"system","content":"client"},{"role":"user","content":"hello"}]}`)
				_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body)
			default:
				body = []byte(`{"systemInstruction":{"parts":[{"text":"client"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
				action := "generateContent"
				if ingress == "countTokens" {
					action = "countTokens"
				}
				_, err = svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", action, false, body)
			}
			if ingress != "countTokens" {
				require.Error(t, err)
			}
			require.Len(t, upstream.bodies, 1)
			wire := upstream.bodies[0]
			require.Equal(t, 1, strings.Count(string(wire), "site-prompt-v2"))
			if ingress == "countTokens" {
				require.False(t, gjson.GetBytes(wire, "contents").Exists())
				require.False(t, gjson.GetBytes(wire, "systemInstruction").Exists())
				require.Equal(t, "models/gemini-2.5-flash", gjson.GetBytes(wire, "generateContentRequest.model").String())
				require.Greater(t, estimateGeminiCountTokens(wire), estimateGeminiCountTokens(body))
				wire = []byte(gjson.GetBytes(wire, "generateContentRequest").Raw)
			}
			require.Equal(t, "site-prompt-v2", gjson.GetBytes(wire, "systemInstruction.parts.1.text").String())
			require.Equal(t, "hello", gjson.GetBytes(wire, "contents.0.parts.0.text").String())
			require.NotContains(t, string(body), "site-prompt-v2")
		})
	}
}

func TestPromptV2GatewayGeminiEnvelopesKeepCleanRetrySource(t *testing.T) {
	for _, platform := range []string{PlatformGemini, PlatformAntigravity} {
		t.Run(platform, func(t *testing.T) {
			prompts, c, _ := promptV2GatewayFixture(t, platform, "site-prompt-v2", "")
			account := &Account{ID: 13, Platform: platform, Type: AccountTypeOAuth}
			clean := []byte(`{"model":"gemini-2.5-flash","project":"fixture","request":{"systemInstruction":{"parts":[{"text":"base identity"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}}`)
			svc := &AntigravityGatewayService{businessPromptService: prompts}
			for range 2 {
				var req *http.Request
				var err error
				if platform == PlatformAntigravity {
					req, err = svc.buildPromptedAPIRequest(context.Background(), c, account, "", "streamGenerateContent", "fixture", clean)
				} else {
					req, err = http.NewRequest(http.MethodPost, "https://example.test/v1internal:generateContent", bytes.NewReader(clean))
					require.NoError(t, err)
					_, err = applyGeminiPromptToHTTPRequest(prompts, c, account, req, "gemini-2.5-flash")
				}
				require.NoError(t, err)
				wire, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				require.Equal(t, 1, strings.Count(string(wire), "site-prompt-v2"))
				require.False(t, gjson.GetBytes(wire, "systemInstruction").Exists())
				require.Equal(t, "site-prompt-v2", gjson.GetBytes(wire, "request.systemInstruction.parts.1.text").String())
				require.Equal(t, "fixture", gjson.GetBytes(wire, "project").String())
			}
			require.NotContains(t, string(clean), "site-prompt-v2")
		})
	}
}

type promptV2BindingRepository struct {
	AccountRepository
	account *Account
	calls   int
}

func (r *promptV2BindingRepository) GetByID(context.Context, int64) (*Account, error) {
	r.calls++
	return r.account, nil
}

func TestPromptV2GatewayClaudeStructuredSourceUsesOneAttempt(t *testing.T) {
	content := `{"blocks":[{"text":"{billing_header}"},{"text":"custom {fp} {claude_code_expansion_prompt}","cache_control":{"type":"ephemeral","ttl":"1h"}},{"text":"{claude_code_system_prompt}"},{"text":"disabled","enabled":false}],"expansion_prompt":"literal {cc_version}"}`
	prompts, c, _ := promptV2GatewayFixture(t, PlatformAnthropic, content, extensionv1.PromptContentAnthropicSystemBlocks)
	account := &Account{ID: 14, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	repo := &promptV2BindingRepository{account: account}
	prompts.SetAccountRepository(repo)
	svc := &GatewayService{businessPromptService: prompts, cfg: &config.Config{}}
	clean := []byte(`{"model":"claude-sonnet-4-6","system":"client instructions","messages":[{"role":"user","content":"real original customer message"}]}`)
	base, err := svc.prepareClaudeCodeOAuthMimicryToBody(context.Background(), c, account, clean, "client instructions", "claude-sonnet-4-6")
	require.NoError(t, err)
	require.Empty(t, gjson.GetBytes(base, "system").Array())
	require.Contains(t, gjson.GetBytes(base, "messages.0.content.0.text").String(), "client instructions")
	// Account changes after base preparation belong to the next attempt. They
	// must not remove the structured source after its default base was omitted.
	off := *account
	off.Extra = map[string]any{PromptAccountBindingExtraKey: extensionv1.PromptAccountBinding{Mode: "off"}}
	repo.account = &off
	_, wire, err := svc.buildUpstreamRequest(context.Background(), c, account, base, "fixture", "oauth", "claude-sonnet-4-6", false, true)
	require.NoError(t, err)
	require.Equal(t, 1, repo.calls)
	require.Len(t, gjson.GetBytes(wire, "system").Array(), 3)
	require.Equal(t, "custom "+computeClaudeCodeFingerprint(clean, claude.EffectiveCLIVersion())+" literal {cc_version}", gjson.GetBytes(wire, "system.1.text").String())
	require.Equal(t, "1h", gjson.GetBytes(wire, "system.1.cache_control.ttl").String())
	require.Equal(t, claudeCodeSystemPrompt, gjson.GetBytes(wire, "system.2.text").String())
	require.NotContains(t, string(wire), "disabled")
	expansionPrefix := strings.SplitN(claudeCodeSystemPromptExpansion, "\n", 2)[0]
	for _, block := range gjson.GetBytes(wire, "system").Array() {
		require.NotContains(t, block.Get("text").String(), expansionPrefix)
	}
	// A new attempt rechecks the binding using the original clean client body.
	base, err = svc.prepareClaudeCodeOAuthMimicryToBody(context.Background(), c, account, clean, "client instructions", "claude-sonnet-4-6")
	require.NoError(t, err)
	_, wire, err = svc.buildUpstreamRequest(context.Background(), c, account, base, "fixture", "oauth", "claude-sonnet-4-6", false, true)
	require.NoError(t, err)
	require.Equal(t, 2, repo.calls)
	expansionCount := 0
	for _, block := range gjson.GetBytes(wire, "system").Array() {
		text := block.Get("text").String()
		if strings.HasPrefix(text, expansionPrefix) {
			expansionCount++
			require.Equal(t, claudeCodeSystemPromptExpansion, text)
		}
		require.NotContains(t, text, "custom ")
	}
	require.Equal(t, 1, expansionCount, "a new disabled attempt restores the default expansion exactly once")
}

func TestPromptV2GatewayClaudeStructuredSourceExcludesFable(t *testing.T) {
	content := `[{"text":"custom must not apply"}]`
	prompts, c, _ := promptV2GatewayFixture(t, PlatformAnthropic, content, extensionv1.PromptContentAnthropicSystemBlocks)
	account := &Account{ID: 15, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	svc := &GatewayService{businessPromptService: prompts, cfg: &config.Config{}}
	clean := []byte(`{"model":"claude-fable-5","system":"client","messages":[{"role":"user","content":"hello"}]}`)
	base, err := svc.prepareClaudeCodeOAuthMimicryToBody(context.Background(), c, account, clean, "client", "claude-fable-5")
	require.NoError(t, err)
	_, wire, err := svc.buildUpstreamRequest(context.Background(), c, account, base, "fixture", "oauth", "claude-fable-5", false, true)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(wire, "system").Array(), 2)
	require.NotContains(t, string(wire), "custom must not apply")
	require.NotContains(t, string(wire), claudeCodeSystemPromptExpansion)
}

func TestPromptV2GatewayFinalCachePreparationMatchesPreviewAndPlan(t *testing.T) {
	resetGatewayForwardingSettingsCacheForTest(t)
	settings := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{SettingKeyEnableAnthropicCacheTTL1hInjection: "true"}}, &config.Config{})
	content := `[{"text":"{billing_header}"},{"text":"one","cache_control":true},{"text":"two","cache_control":true},{"text":"three","cache_control":true},{"text":"four","cache_control":true},{"text":"five","cache_control":true}]`
	prompts, c, _ := promptV2GatewayFixture(t, PlatformAnthropic, content, extensionv1.PromptContentAnthropicSystemBlocks)
	prompts.previewSettings = settings
	account := &Account{ID: 16, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	svc := &GatewayService{businessPromptService: prompts, settingService: settings, cfg: &config.Config{}}
	clean := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)
	base, err := svc.prepareClaudeCodeOAuthMimicryToBody(context.Background(), c, account, clean, nil, "claude-sonnet-4-6")
	require.NoError(t, err)
	_, wire, err := svc.buildUpstreamRequest(context.Background(), c, account, base, "fixture", "oauth", "claude-sonnet-4-6", false, true)
	require.NoError(t, err)
	application, ok := businessSystemPromptApplicationFromRequest(c, "messages")
	require.True(t, ok)
	require.NoError(t, validateBusinessSystemPromptFinal(c, wire, "messages"))
	require.Equal(t, "1h", gjson.GetBytes(wire, "system.1.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(wire, "system.5.cache_control").Exists())
	require.JSONEq(t, gjson.GetBytes(wire, "system").Raw, string(application.RulesPlan.Placements[0].StructuredContent))

	// The preview starts from the same prepared body and the same frozen
	// source expansion, then shares the final cache/billing normalization.
	snapshot, err := prompts.snapshotForRequest(c)
	require.NoError(t, err)
	target := promptSendTarget(c, account, base, "messages", false, "claude-sonnet-4-6")
	snapshot, target, err = prompts.prepareAnthropicPromptSend(c, account, base, snapshot, target)
	require.NoError(t, err)
	preview, previewApplication, err := ApplyBusinessSystemPromptToJSONContext(context.Background(), base, snapshot, target)
	require.NoError(t, err)
	preview, previewApplication, err = FinalizePromptMessageApplication(preview, previewApplication, account, settings, claude.DefaultUserAgent(), c)
	require.NoError(t, err)
	require.JSONEq(t, gjson.GetBytes(wire, "system").Raw, gjson.GetBytes(preview, "system").Raw)
	require.Equal(t, application.RulesPlan.SHA256, previewApplication.RulesPlan.SHA256)
}

func TestPromptV2GatewayDisabledRulesAvoidAccountRepository(t *testing.T) {
	prompts, c, _ := promptV2GatewayFixture(t, PlatformAnthropic, "disabled prompt", "")
	snapshot, err := prompts.snapshotForRequest(c)
	require.NoError(t, err)
	snapshot.Enabled = false
	businessSystemPromptRequestSet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestSnapshotKey, ""), snapshot)
	repo := &promptV2BindingRepository{}
	prompts.SetAccountRepository(repo)
	account := &Account{ID: 17, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)
	out, application, err := prompts.ApplyForSend(c, account, body, "messages", false)
	require.NoError(t, err)
	require.False(t, application.Applied)
	require.Equal(t, body, out)
	require.Zero(t, repo.calls)
}

func TestPromptV2GatewayActualResponseEchoBoundaries(t *testing.T) {
	for _, protocol := range []string{"messages", "gemini"} {
		for _, stream := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "-json", true: "-sse"}[stream], func(t *testing.T) {
				platform := PlatformAnthropic
				clean := []byte(`{"model":"claude-sonnet-4-6","system":"client","messages":[{"role":"user","content":"hello"}]}`)
				carrier := "system"
				if protocol == "gemini" {
					platform, carrier = PlatformGemini, "systemInstruction"
					clean = []byte(`{"systemInstruction":{"parts":[{"text":"client"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
				}
				prompts, initialContext, _ := promptV2GatewayFixture(t, platform, "site-prompt-v2", "")
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request, c.Keys = initialContext.Request, initialContext.Keys
				account := &Account{ID: 18, Platform: platform, Type: AccountTypeAPIKey}
				wire, _, err := prompts.ApplyForSendModel(c, account, clean, protocol, false, "gemini-2.5-flash")
				require.NoError(t, err)
				response := map[string]any{
					carrier: json.RawMessage(gjson.GetBytes(wire, carrier).Raw),
					"type":  "message", "role": "assistant", "model": "claude-sonnet-4-6", "stop_reason": "end_turn",
					"content": []map[string]string{{"type": "text", "text": "site-prompt-v2"}},
					"usage":   map[string]int{"input_tokens": 5, "output_tokens": 2},
				}
				if protocol == "gemini" {
					delete(response, "content")
					response["candidates"] = []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]string{"text": "site-prompt-v2"}}}, "finishReason": "STOP"}}
				}
				encoded, err := json.Marshal(response)
				require.NoError(t, err)
				payload, contentType := string(encoded), "application/json"
				if stream {
					payload, contentType = "data: "+payload+"\n\n", "text/event-stream"
					if protocol == "messages" {
						payload += "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
				}
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(payload))}
				if protocol == "messages" {
					gateway := &GatewayService{cfg: &config.Config{}}
					if stream {
						_, err = gateway.handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), resp, c, account, time.Now(), "claude-sonnet-4-6")
					} else {
						_, err = gateway.handleNonStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), resp, c, account)
					}
				} else {
					gateway := &GeminiMessagesCompatService{cfg: &config.Config{}}
					if stream {
						_, err = gateway.handleNativeStreamingResponse(c, resp, time.Now(), false, account, "fixture")
					} else {
						_, err = gateway.handleNativeNonStreamingResponse(c, resp, false, account, "fixture")
					}
				}
				require.NoError(t, err)
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "site-prompt-v2"), "only the model's generated text remains")
				require.Contains(t, recorder.Body.String(), "client")
			})
		}
	}
}

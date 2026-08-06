package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBusinessSystemPromptSeedHasCapturedBytes(t *testing.T) {
	seed := embeddedBusinessSystemPrompt
	require.Equal(t, 7098, len([]byte(seed)))
	require.Equal(t, 6970, len([]rune(seed)))
	require.False(t, strings.HasSuffix(seed, "\n"))
	digest := sha256.Sum256([]byte(seed))
	require.Equal(t, "C2F0269BAFFA6A0EB1C9A9E15DF815A6582AE6A615BC51D64B7CC5342B5EFCB8", strings.ToUpper(hex.EncodeToString(digest[:])))
}

func TestValidateBusinessSystemPromptBodyPreservesWhitespace(t *testing.T) {
	body := "  first\nsecond  "
	hash, length, err := ValidateBusinessSystemPromptBody(body)
	require.NoError(t, err)
	require.Equal(t, len([]byte(body)), length)
	digest := sha256.Sum256([]byte(body))
	require.Equal(t, hex.EncodeToString(digest[:]), hash)
}

func TestValidateBusinessSystemPromptBodyRejectsInvalidContent(t *testing.T) {
	for name, body := range map[string]string{
		"empty":   " \n\t",
		"nul":     "valid\x00body",
		"invalid": string([]byte{0xff, 0xfe}),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ValidateBusinessSystemPromptBody(body)
			require.Error(t, err)
		})
	}
}

func TestValidateBusinessSystemPromptBodyEnforcesUTF8ByteLimit(t *testing.T) {
	exact := strings.Repeat("a", BusinessSystemPromptMaxBytes)
	_, byteLength, err := ValidateBusinessSystemPromptBody(exact)
	require.NoError(t, err)
	require.Equal(t, BusinessSystemPromptMaxBytes, byteLength)

	_, _, err = ValidateBusinessSystemPromptBody(exact + "a")
	require.ErrorIs(t, err, ErrBusinessSystemPromptInvalid)
}

func TestMergeBusinessSystemPromptInstructions(t *testing.T) {
	require.Equal(t, "client\n\nserver", MergeBusinessSystemPromptInstructions(" client ", " server "))
	require.Equal(t, "server", MergeBusinessSystemPromptInstructions(" ", " server "))
	require.Equal(t, "client", MergeBusinessSystemPromptInstructions(" client ", " "))
}

func TestApplyBusinessSystemPromptToResponsesUsesNativeInstructions(t *testing.T) {
	snapshot := BusinessSystemPromptSnapshot{Enabled: true, Body: "server", Revision: 7}
	body, application, err := ApplyBusinessSystemPromptToJSON(
		[]byte(`{"model":"gpt-5","instructions":" client ","input":[{"role":"user","content":"hi"}]}`),
		snapshot,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses},
	)
	require.NoError(t, err)
	require.True(t, application.Applied)
	require.Equal(t, "client\n\nserver", gjson.GetBytes(body, "instructions").String())
	require.Equal(t, "client", application.ClientInstructions)
	require.Equal(t, "server", application.ServerInstructions)
}

func TestApplyBusinessSystemPromptToChatInsertsSystemAfterExistingControlMessages(t *testing.T) {
	snapshot := BusinessSystemPromptSnapshot{Enabled: true, Body: "server"}
	body, application, err := ApplyBusinessSystemPromptToJSON(
		[]byte(`{"model":"gpt-5","messages":[{"role":"system","content":"old"},{"role":"developer","content":"dev"},{"role":"user","content":"hi"}]}`),
		snapshot,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolChat},
	)
	require.NoError(t, err)
	require.True(t, application.Applied)
	var decoded struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Equal(t, []string{"system", "developer", "system", "user"}, []string{
		decoded.Messages[0].Role, decoded.Messages[1].Role, decoded.Messages[2].Role, decoded.Messages[3].Role,
	})
	require.Equal(t, "server", decoded.Messages[2].Content)
}

func TestBusinessSystemPromptScopeAndCompactSwitch(t *testing.T) {
	snapshot := BusinessSystemPromptSnapshot{Enabled: true, Body: "server"}
	for name, target := range map[string]BusinessSystemPromptTarget{
		"grok excluded": {Platform: PlatformGrok, Protocol: BusinessSystemPromptProtocolResponses},
		"compact off":   {Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses, Compact: true},
	} {
		t.Run(name, func(t *testing.T) {
			if target.Compact {
				snapshot.CompactEnabled = false
			}
			body := []byte(`{"model":"gpt-5","instructions":"client"}`)
			got, application, err := ApplyBusinessSystemPromptToJSON(body, snapshot, target)
			require.NoError(t, err)
			require.False(t, application.Applied)
			require.True(t, bytes.Equal(body, got))
		})
	}
}

func TestDisabledBusinessSystemPromptReturnsSnapshotMetadataWithoutChangingBody(t *testing.T) {
	snapshot := BusinessSystemPromptSnapshot{
		Enabled: false, ExposeServerPrompt: true, CompactEnabled: true,
		TemplateID: 2, VersionID: 3, TemplateVersion: 4, Revision: 5, SHA256: "ABCDEF",
	}
	body := []byte(`{"instructions":"client"}`)
	got, application, err := ApplyBusinessSystemPromptToJSON(body, snapshot, BusinessSystemPromptTarget{
		Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses,
	})
	require.NoError(t, err)
	require.Equal(t, body, got)
	require.False(t, application.Applied)
	require.Equal(t, int64(5), application.Revision)
	require.Equal(t, "abcdef", application.SHA256)
	require.True(t, application.ExposeServerPrompt)
}

func TestRewriteBusinessSystemPromptResponseJSONRestoresClientInstructions(t *testing.T) {
	application := BusinessSystemPromptApplication{
		Applied:            true,
		Carrier:            BusinessSystemPromptCarrierInstructions,
		ClientInstructions: "client",
		ServerInstructions: "server",
	}
	body := []byte(`{"id":"resp_1","instructions":"client\n\nserver","output":[]}`)
	rewritten, err := RewriteBusinessSystemPromptResponseJSON(body, application, false)
	require.NoError(t, err)
	require.Equal(t, "client", gjson.GetBytes(rewritten, "instructions").String())
}

func TestRewriteBusinessSystemPromptResponseJSONDeletesServerOnlyInstructions(t *testing.T) {
	application := BusinessSystemPromptApplication{
		Applied:            true,
		Carrier:            BusinessSystemPromptCarrierInstructions,
		ServerInstructions: "server",
	}
	body := []byte(`{"type":"response.completed","response":{"id":"resp_1","instructions":"server"}}`)
	rewritten, err := RewriteBusinessSystemPromptResponseJSON(body, application, false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(rewritten, "response.instructions").Exists())
}

func TestRewriteBusinessSystemPromptResponseJSONRestoresStructuredErrorInstructions(t *testing.T) {
	application := BusinessSystemPromptApplication{
		Applied: true, Carrier: BusinessSystemPromptCarrierInstructions,
		ClientInstructions: "client", ServerInstructions: "server",
	}
	body := []byte(`{"error":{"response":{"instructions":"client\n\nserver"},"message":"unchanged"}}`)
	rewritten, err := RewriteBusinessSystemPromptResponseJSON(body, application, false)
	require.NoError(t, err)
	require.Equal(t, "client", gjson.GetBytes(rewritten, "error.response.instructions").String())
	require.Equal(t, "unchanged", gjson.GetBytes(rewritten, "error.message").String())
}

func TestRewriteBusinessSystemPromptResponseJSONDoesNotScrubUnexpectedText(t *testing.T) {
	application := BusinessSystemPromptApplication{
		Applied:            true,
		Carrier:            BusinessSystemPromptCarrierInstructions,
		ServerInstructions: "server",
	}
	body := []byte(`{"instructions":"upstream changed this","output":[{"content":[{"text":"server"}]}]}`)
	rewritten, err := RewriteBusinessSystemPromptResponseJSON(body, application, false)
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(rewritten))
}

func TestRewriteBusinessSystemPromptSSEPreservesFraming(t *testing.T) {
	application := BusinessSystemPromptApplication{
		Applied:            true,
		Carrier:            BusinessSystemPromptCarrierInstructions,
		ClientInstructions: "client",
		ServerInstructions: "server",
	}
	input := []byte("event: response.created\ndata: \t{\"type\":\"response.created\",\"response\":{\"instructions\":\"client\\n\\nserver\"}}\n\ndata: \t[DONE]  \n\n")
	want := "event: response.created\ndata: \t{\"type\":\"response.created\",\"response\":{\"instructions\":\"client\"}}\n\ndata: \t[DONE]  \n\n"
	rewritten, err := RewriteBusinessSystemPromptSSE(input, application, false)
	require.NoError(t, err)
	require.Equal(t, want, string(rewritten))
}

func TestRewriteBusinessSystemPromptResponseHonorsExposeSwitch(t *testing.T) {
	application := BusinessSystemPromptApplication{
		Applied:            true,
		Carrier:            BusinessSystemPromptCarrierInstructions,
		ServerInstructions: "server",
	}
	body := []byte(`{"instructions":"server"}`)
	rewritten, err := RewriteBusinessSystemPromptResponseJSON(body, application, true)
	require.NoError(t, err)
	require.True(t, bytes.Equal(body, rewritten))
}

func TestBusinessSystemPromptApplicationCapturesExposeDecision(t *testing.T) {
	snapshot := BusinessSystemPromptSnapshot{
		Enabled:            true,
		ExposeServerPrompt: true,
		Body:               "server",
		Revision:           8,
	}
	_, application, err := ApplyBusinessSystemPromptToJSON(
		[]byte(`{"instructions":"client"}`),
		snapshot,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses},
	)
	require.NoError(t, err)
	require.True(t, application.ExposeServerPrompt)
	require.Equal(t, int64(8), application.Revision)
}

func TestBusinessSystemPromptCacheKeyIncludesAppliedRevisionAndHash(t *testing.T) {
	application := BusinessSystemPromptApplication{Applied: true, Revision: 9, SHA256: "abc123"}
	require.Equal(t, "client-key:business-system-prompt:9:abc123", appendBusinessSystemPromptApplicationToCacheKey(" client-key ", application))
	require.Equal(t, "client-key:business-system-prompt:9:abc123", appendBusinessSystemPromptApplicationToCacheKey("client-key:business-system-prompt:9:abc123", application))
	require.Empty(t, appendBusinessSystemPromptApplicationToCacheKey("", application))

	body, err := rewriteBusinessSystemPromptCacheKey(
		[]byte(`{"prompt_cache_key":"client-key","input":[]}`),
		application,
	)
	require.NoError(t, err)
	require.Equal(t, "client-key:business-system-prompt:9:abc123", gjson.GetBytes(body, "prompt_cache_key").String())

	withoutKey := []byte(`{"input":[]}`)
	unchanged, err := rewriteBusinessSystemPromptCacheKey(withoutKey, application)
	require.NoError(t, err)
	require.Equal(t, withoutKey, unchanged)
}

func TestBusinessSystemPromptRequestDecisionIsFrozenAcrossRetry(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 1, Enabled: false, Body: "server",
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := &Account{Platform: PlatformOpenAI}
	first, application, err := gateway.applyBusinessSystemPromptForRequest(
		c, []byte(`{"instructions":"client"}`), account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	require.False(t, application.Applied)
	require.Equal(t, "client", gjson.GetBytes(first, "instructions").String())

	store.loaded = BusinessSystemPromptSnapshot{Revision: 2, Enabled: true, Body: "new-server"}
	require.NoError(t, policy.Reload(context.Background()))
	retry, retryApplication, err := gateway.applyBusinessSystemPromptForRequest(
		c, []byte(`{"instructions":"changed-client"}`), account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	require.False(t, retryApplication.Applied)
	require.Equal(t, int64(1), retryApplication.Revision)
	require.Equal(t, "changed-client", gjson.GetBytes(retry, "instructions").String())

	newRequest, _ := gin.CreateTestContext(httptest.NewRecorder())
	fresh, freshApplication, err := gateway.applyBusinessSystemPromptForRequest(
		newRequest, []byte(`{"instructions":"client"}`), account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	require.True(t, freshApplication.Applied)
	require.Equal(t, "client\n\nnew-server", gjson.GetBytes(fresh, "instructions").String())
}

func TestBusinessSystemPromptRequestDoesNotDuplicateAfterCacheRewrite(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 9, Enabled: true, Body: "server",
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := &Account{Platform: PlatformOpenAI}
	body, application, err := gateway.applyBusinessSystemPromptForRequest(
		c, []byte(`{"instructions":"client","prompt_cache_key":"key"}`), account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	body, err = rewriteBusinessSystemPromptCacheKey(body, application)
	require.NoError(t, err)

	retry, retryApplication, err := gateway.applyBusinessSystemPromptForRequest(
		c, body, account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	retry, err = rewriteBusinessSystemPromptCacheKey(retry, retryApplication)
	require.NoError(t, err)
	require.Equal(t, "client\n\nserver", gjson.GetBytes(retry, "instructions").String())
	require.Equal(t, 1, strings.Count(gjson.GetBytes(retry, "prompt_cache_key").String(), ":business-system-prompt:"))
}

func TestBusinessSystemPromptRetryPreservesRawSnapshotWhitespace(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 4, Enabled: true, Body: "  server  ",
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := &Account{Platform: PlatformOpenAI}
	_, _, err := gateway.applyBusinessSystemPromptForRequest(
		c, []byte(`{"instructions":"client"}`), account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)

	retry, application, err := gateway.applyBusinessSystemPromptForRequest(
		c, []byte(`{"instructions":"changed-client"}`), account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	require.Equal(t, "changed-client\n\nserver", gjson.GetBytes(retry, "instructions").String())
	require.Equal(t, int64(4), application.Revision)
}

func TestBusinessSystemPromptSnapshotIsFrozenAcrossAdapterFallback(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 4, Enabled: true, Body: "old-server",
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := &Account{Platform: PlatformOpenAI}
	_, firstApplication, err := gateway.applyBusinessSystemPromptForRequest(
		c, []byte(`{"instructions":"client"}`), account, BusinessSystemPromptProtocolResponses, false,
	)
	require.NoError(t, err)
	require.Equal(t, int64(4), firstApplication.Revision)

	store.loaded = BusinessSystemPromptSnapshot{Revision: 5, Enabled: true, Body: "new-server"}
	require.NoError(t, policy.Reload(context.Background()))
	fallback, fallbackApplication, err := gateway.applyBusinessSystemPromptForRequest(
		c, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), account, BusinessSystemPromptProtocolChat, false,
	)
	require.NoError(t, err)
	require.Equal(t, int64(4), fallbackApplication.Revision)
	require.True(t, chatBodyHasSystemPrompt(fallback, "old-server"))
	require.NotContains(t, string(fallback), "new-server")
}

func TestBusinessSystemPromptResponseUsesRequestScopedExposeDecision(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 1, Enabled: true, ExposeServerPrompt: true, Body: "server",
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(businessSystemPromptRequestApplicationKey+":"+BusinessSystemPromptProtocolResponses, businessSystemPromptRequestState{
		application: BusinessSystemPromptApplication{
			Applied: true, Carrier: BusinessSystemPromptCarrierInstructions,
			ServerInstructions: "server", ExposeServerPrompt: false,
		},
	})

	rewritten := gateway.rewriteBusinessSystemPromptJSONForRequest(
		c,
		[]byte(`{"response":{"instructions":"server"}}`),
		BusinessSystemPromptProtocolResponses,
	)
	require.False(t, gjson.GetBytes(rewritten, "response.instructions").Exists())
}

func TestOpenAIGatewayBusinessSystemPromptHelperScopesAndMerges(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "server"}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	openaiBody, app, err := gateway.applyBusinessSystemPrompt([]byte(`{"instructions":"client"}`), &Account{Platform: PlatformOpenAI}, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, "client\n\nserver", gjson.GetBytes(openaiBody, "instructions").String())
	require.True(t, app.Applied)

	grokBody, app, err := gateway.applyBusinessSystemPrompt([]byte(`{"instructions":"client"}`), &Account{Platform: PlatformGrok}, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.False(t, app.Applied)
	require.JSONEq(t, `{"instructions":"client"}`, string(grokBody))
}

func TestBusinessSystemPromptRequestStateNeverCrossesIntoGrok(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 1, Enabled: true, Body: "server",
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, application, err := gateway.applyBusinessSystemPromptForRequest(
		c,
		[]byte(`{"instructions":"client"}`),
		&Account{Platform: PlatformOpenAI},
		BusinessSystemPromptProtocolResponses,
		false,
	)
	require.NoError(t, err)
	require.True(t, application.Applied)

	grokBody, grokApplication, err := gateway.applyBusinessSystemPromptForRequest(
		c,
		[]byte(`{"instructions":"grok-client"}`),
		&Account{Platform: PlatformGrok},
		BusinessSystemPromptProtocolResponses,
		false,
	)
	require.NoError(t, err)
	require.False(t, grokApplication.Applied)
	require.JSONEq(t, `{"instructions":"grok-client"}`, string(grokBody))
}

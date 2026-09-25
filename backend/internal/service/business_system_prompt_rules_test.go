package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	promptpolicy "github.com/Wei-Shaw/sub2api/internal/promptskills/policy"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func promptRulesFixture(t *testing.T, protocol string, placements ...extensionv1.PromptRulePlacement) BusinessSystemPromptApplication {
	t.Helper()
	for i := range placements {
		placements[i].Protocol = protocol
	}
	return BusinessSystemPromptApplication{Applied: true, Revision: 1, Carrier: "rule_plan", RulesPlan: &extensionv1.PromptRulesPlan{SHA256: "plan", Placements: placements}}
}

func TestPromptRulesApplyPreservesHistoryAndDistinctPositions(t *testing.T) {
	input := []byte(`{"instructions":"client","input":[{"role":"developer","content":"client control"},{"role":"user","content":[{"type":"input_text","text":"site"}]},{"type":"function_call_output","call_id":"c1","output":"value"}],"previous_response_id":"resp_existing"}`)
	plan := promptRulesFixture(t, "responses",
		extensionv1.PromptRulePlacement{RuleID: "head", Carrier: "input", Role: "system", Position: "conversation_head", Body: "head"},
		extensionv1.PromptRulePlacement{RuleID: "control", Carrier: "input", Role: "developer", Position: "control_append", Body: "control"},
		extensionv1.PromptRulePlacement{RuleID: "tail", Carrier: "input", Role: "system", Position: "conversation_tail", Body: "tail"},
		extensionv1.PromptRulePlacement{RuleID: "prefix", Carrier: "instructions", Position: "control_prepend", Body: "before"},
		extensionv1.PromptRulePlacement{RuleID: "suffix", Carrier: "instructions", Position: "control_append", Body: "after"},
	)
	output, application, err := applyPromptRules(input, plan)
	require.NoError(t, err)
	require.Equal(t, "before\n\nclient\n\nafter", gjson.GetBytes(output, "instructions").String())
	require.Equal(t, "head", gjson.GetBytes(output, "input.0.content").String())
	require.Equal(t, "control", gjson.GetBytes(output, "input.2.content").String())
	require.Equal(t, "tail", gjson.GetBytes(output, "input.5.content").String())
	require.Equal(t, "resp_existing", gjson.GetBytes(output, "previous_response_id").String())
	proofs := promptRulesUndo(input, output, application)
	clean, err := restorePromptRules(output, proofs)
	require.NoError(t, err)
	require.JSONEq(t, string(input), string(clean))
	// A serializer may reorder object keys but cannot change control text.
	var reencoded any
	require.NoError(t, json.Unmarshal(output, &reencoded))
	encoded, _ := json.Marshal(reencoded)
	clean, err = restorePromptRules(encoded, proofs)
	require.NoError(t, err)
	require.JSONEq(t, string(input), string(clean))
	altered, _ := sjson.SetBytes(output, "input.3.content.0.text", "changed")
	_, err = restorePromptRules(altered, proofs)
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
}

func TestPromptRulesWireValidationAndEcho(t *testing.T) {
	input := []byte(`{"instructions":"client","input":"hello"}`)
	plan := promptRulesFixture(t, "responses", extensionv1.PromptRulePlacement{RuleID: "r", Carrier: "instructions", Position: "control_prepend", Body: "site"})
	output, application, err := applyPromptRules(input, plan)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, "responses"), cacheBusinessSystemPromptState(input, output, BusinessSystemPromptSnapshot{Revision: 1}, BusinessSystemPromptTarget{Protocol: "responses"}, application))
	require.NoError(t, validateBusinessSystemPromptFinal(c, output, "responses"))
	bad, _ := sjson.SetBytes(output, "instructions", "client")
	require.Error(t, validateBusinessSystemPromptFinal(c, bad, "responses"))
	echo, err := RewriteBusinessSystemPromptResponseJSON([]byte(`{"response":{"instructions":"site\n\nclient"}}`), application, false)
	require.NoError(t, err)
	require.Equal(t, "client", gjson.GetBytes(echo, "response.instructions").String())
}

func TestPromptRulesMixedPublicPrivateEchoPreservesClient(t *testing.T) {
	input := []byte(`{"instructions":"client","input":[{"role":"system","content":"private-site"},{"role":"user","content":"hello"}]}`)
	plan := promptRulesFixture(t, "responses",
		extensionv1.PromptRulePlacement{RuleID: "private", Carrier: "instructions", Position: "control_prepend", Body: "private-site"},
		extensionv1.PromptRulePlacement{RuleID: "public", Carrier: "instructions", Position: "control_append", Body: "public-site", PreserveEcho: true},
		extensionv1.PromptRulePlacement{RuleID: "message", Carrier: "input", Role: "system", Position: "conversation_tail", Body: "private-site"})
	output, application, err := applyPromptRules(input, plan)
	require.NoError(t, err)
	echo, err := RewriteBusinessSystemPromptResponseJSON(output, application, false)
	require.NoError(t, err)
	require.Equal(t, "client\n\npublic-site", gjson.GetBytes(echo, "instructions").String())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, "responses"), cacheBusinessSystemPromptState(input, output, BusinessSystemPromptSnapshot{Revision: 1}, BusinessSystemPromptTarget{Protocol: "responses"}, application))
	echo = rewritePromptRulesStructuredEcho(c, echo, "responses")
	require.JSONEq(t, gjson.GetBytes(input, "input").Raw, gjson.GetBytes(echo, "input").Raw)
	require.Contains(t, string(echo), "private-site", "the customer's identical message stays")
}

func TestPromptRulesPlannerRejectsOAuthSystemBeforeMutation(t *testing.T) {
	rule := extensionv1.PromptRule{ID: "r", Name: "r", Enabled: true, TemplateID: 1, VersionID: 1, Role: "system", Platforms: []string{"openai"}, Position: "control_append"}
	hash, _, _ := extensionv1.ValidateTextDocument("site", 100)
	snapshot := BusinessSystemPromptSnapshot{Enabled: true, RulePolicy: &extensionv1.PromptRulePolicy{Version: 2, Rules: []extensionv1.PromptRule{rule}, DefaultRuleIDs: []string{"r"}}, ResolvedRules: []extensionv1.ResolvedPromptRule{{Rule: rule, Body: "site", SHA256: hash}}}
	_, err := promptpolicy.Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", AccountType: "oauth", Protocol: "responses"}, true)
	require.ErrorIs(t, err, promptpolicy.ErrPromptDeliveryUnsupported)
}

func TestPromptRulesLegacyHybridAndMaximumCompiledBody(t *testing.T) {
	require.Equal(t, BusinessSystemPromptBundleMaxBytes, extensionv1.PromptRulesMaxBytes)
	t.Run("existing_paired_fixture", func(t *testing.T) {
		service := newGatewayHybridBusinessSystemPromptPolicy(t)
		snapshot, ok := service.CurrentSnapshot()
		require.True(t, ok)
		require.Len(t, snapshot.ResolvedRules, 1)
		old := snapshot.ResolvedRules[0]
		snapshot.RulePolicy = &extensionv1.PromptRulePolicy{Version: 2, DefaultRuleIDs: []string{"legacy-default"}, Rules: []extensionv1.PromptRule{{ID: "legacy-default", Name: "Legacy default", Enabled: true, TemplateID: snapshot.TemplateID, VersionID: snapshot.VersionID, Role: "auto", Platforms: []string{"openai", "cindy"}, Position: "control_append"}}}
		require.NoError(t, service.preparePromptRulesSnapshot(&snapshot))
		require.Len(t, snapshot.ResolvedRules, 1)
		require.Equal(t, old.Body, snapshot.ResolvedRules[0].Body)
		require.Equal(t, old.SHA256, snapshot.ResolvedRules[0].SHA256)
	})
	t.Run("full_256_KiB_compiled_publication", func(t *testing.T) {
		body := strings.Repeat("x", BusinessSystemPromptBundleMaxBytes)
		hash := hashBusinessSystemPromptBundleBytes([]byte(body))
		registry := &RemoteSkillRegistryService{}
		require.NoError(t, registry.installPublication(RemoteSkillPublication{Revision: 1, CandidateID: 1, EffectiveTreeSHA256: strings.Repeat("a", 64), EffectivePromptSHA256: hash, EffectivePromptBody: body, Files: map[string][]byte{"SKILL.md": []byte("fixture")}, Prompt: RemoteSkillPromptVersion{ID: 1, EffectiveSHA256: hash}}))
		baseHash, baseSize, err := ValidateBusinessSystemPromptBody("base")
		require.NoError(t, err)
		store := &fakeBusinessSystemPromptStore{detail: BusinessSystemPromptTemplateDetail{Versions: []BusinessSystemPromptVersion{{ID: 2, TemplateID: 1, Body: "base", SHA256: baseHash, ByteLength: baseSize, CompositionMode: BusinessSystemPromptCompositionCodexSkillHybrid, BundleID: BusinessSystemPromptRemoteSkillBundleID}}}}
		service := NewBusinessSystemPromptService(store, nil)
		service.registry = registry
		snapshot := BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, TemplateID: 1, VersionID: 2, RulePolicy: &extensionv1.PromptRulePolicy{Version: 2, DefaultRuleIDs: []string{"legacy-default"}, Rules: []extensionv1.PromptRule{{ID: "legacy-default", Name: "Legacy", Enabled: true, TemplateID: 1, VersionID: 2, Role: "auto", Platforms: []string{"openai", "cindy"}, Position: "control_append"}}}}
		require.NoError(t, service.preparePromptRulesSnapshot(&snapshot))
		output, application, err := ApplyBusinessSystemPromptToJSONContext(context.Background(), []byte(`{"input":"hello"}`), snapshot, BusinessSystemPromptTarget{Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Protocol: "responses"})
		require.NoError(t, err)
		require.True(t, application.Applied)
		require.Equal(t, len(body), len(gjson.GetBytes(output, "instructions").String()))
	})
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func unifiedPromptSnapshot(t *testing.T, platform string, roles, positions, bodies []string) BusinessSystemPromptSnapshot {
	t.Helper()
	snapshot := BusinessSystemPromptSnapshot{Enabled: true, Revision: 42, RulePolicy: &extensionv1.PromptRulePolicy{Version: 2}}
	for i, role := range roles {
		rule := extensionv1.PromptRule{ID: fmt.Sprintf("rule-%d", i), Name: fmt.Sprintf("Rule %d", i), Enabled: true, TemplateID: int64(i + 1), VersionID: int64(i + 1), Role: role, Position: positions[i], Platforms: []string{platform}, ModelMatch: "upstream"}
		if platform == "anthropic" && (positions[i] == "conversation_tail" || positions[i] == "after_last_user") {
			rule.Models = []string{"claude-opus-4-8"}
		}
		hash, _, err := extensionv1.ValidateTextDocument(bodies[i], extensionv1.PromptRulesMaxBytes)
		require.NoError(t, err)
		snapshot.RulePolicy.Rules = append(snapshot.RulePolicy.Rules, rule)
		snapshot.RulePolicy.DefaultRuleIDs = append(snapshot.RulePolicy.DefaultRuleIDs, rule.ID)
		snapshot.ResolvedRules = append(snapshot.ResolvedRules, extensionv1.ResolvedPromptRule{Rule: rule, Body: bodies[i], SHA256: hash})
	}
	return snapshot
}

func TestUnifiedPromptChatSixPositionsAndRealRoles(t *testing.T) {
	input := []byte(`{"instructions":"unrelated","messages":[{"role":"system","content":"client-system"},{"role":"developer","content":"client-developer"},{"role":"user","content":[{"type":"text","text":"first"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"assistant","content":"reply"},{"role":"user","content":"last"}]}`)
	positions := []string{"control_prepend", "control_append", "conversation_head", "conversation_tail", "before_last_user", "after_last_user"}
	expected := []int{0, 2, 0, 5, 4, 5}
	for _, role := range []string{"auto", "system", "developer"} {
		for n, position := range positions {
			t.Run(role+"/"+position, func(t *testing.T) {
				snapshot := unifiedPromptSnapshot(t, "openai", []string{role}, []string{position}, []string{"site"})
				out, app, err := ApplyBusinessSystemPromptToJSON(input, snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "chat"})
				require.NoError(t, err)
				require.True(t, app.Applied)
				require.Equal(t, "unrelated", gjson.GetBytes(out, "instructions").String())
				messages := gjson.GetBytes(out, "messages").Array()
				actualRole := role
				if actualRole == "auto" {
					actualRole = "system"
				}
				require.Equal(t, actualRole, messages[expected[n]].Get("role").String())
				require.Equal(t, "site", messages[expected[n]].Get("content").String())
				require.Equal(t, expected[n], *app.RulesPlan.Placements[0].Index)
				originals := append(messages[:expected[n]], messages[expected[n]+1:]...)
				for i, original := range gjson.GetBytes(input, "messages").Array() {
					require.JSONEq(t, original.Raw, originals[i].Raw)
				}
			})
		}
	}
}

func TestUnifiedPromptResponsesNativeExplicitAndStringInput(t *testing.T) {
	snapshot := unifiedPromptSnapshot(t, "openai", []string{"auto", "auto", "developer", "system"}, []string{"control_prepend", "control_append", "before_last_user", "after_last_user"}, []string{"before", "after", "dev", "sys"})
	input := []byte(`{"instructions":"  exact client\n","input":"hello","previous_response_id":"resp_prior"}`)
	out, app, err := ApplyBusinessSystemPromptToJSON(input, snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses", AccountType: "apikey"})
	require.NoError(t, err)
	require.Equal(t, "before\n\n  exact client\n\n\nafter", gjson.GetBytes(out, "instructions").String())
	require.Equal(t, "developer", gjson.GetBytes(out, "input.0.role").String())
	require.Equal(t, "hello", gjson.GetBytes(out, "input.1.content").String())
	require.Equal(t, "system", gjson.GetBytes(out, "input.2.role").String())
	require.Equal(t, "resp_prior", gjson.GetBytes(out, "previous_response_id").String())
	require.Len(t, app.RulesPlan.Placements, 4)
	require.Equal(t, "  exact client\n", app.ClientInstructions)
	require.Nil(t, snapshot.ResolvedRules[0].StructuredContent)
	// A pure inline request does not start or consult a Skill registry / RPC.
	history := strings.Repeat("history", extensionv1.MaxPayloadBytes/7+1)
	body, _ := json.Marshal(map[string]any{"input": history})
	out, _, err = ApplyBusinessSystemPromptToJSONContext(context.Background(), body, unifiedPromptSnapshot(t, "openai", []string{"auto"}, []string{"control_append"}, []string{"inline"}), BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"})
	require.NoError(t, err)
	require.Equal(t, history, gjson.GetBytes(out, "input").String())
}

func TestUnifiedPromptMissingAnchorAndUnsafeToolBoundariesSkip(t *testing.T) {
	cases := []struct{ name, protocol, platform, body, position, reason string }{
		{"no user", "responses", "openai", `{"input":[{"role":"assistant","content":"answer"}]}`, "before_last_user", "last_user_missing"},
		{"Claude tool results are not users", "messages", "anthropic", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":"output"}]}]}`, "after_last_user", "last_user_missing"},
		{"open Chat tool call", "chat", "openai", `{"messages":[{"role":"user","content":"hello"},{"role":"assistant","tool_calls":[{"id":"one","type":"function","function":{"name":"f","arguments":"{}"}}]}]}`, "conversation_tail", "unsafe_tool_boundary"},
		{"parallel Responses tool group", "responses", "openai", `{"input":[{"type":"function_call","call_id":"one"},{"type":"function_call","call_id":"two"},{"type":"function_call_output","call_id":"one","output":"ok"},{"role":"user","content":"interrupt"},{"type":"function_call_output","call_id":"two","output":"ok"}]}`, "before_last_user", "unsafe_tool_boundary"},
		{"Claude illegal assistant tail", "messages", "anthropic", `{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"done"}]}`, "conversation_tail", "illegal_message_boundary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := unifiedPromptSnapshot(t, tc.platform, []string{"auto"}, []string{tc.position}, []string{"site"})
			out, app, err := ApplyBusinessSystemPromptToJSON([]byte(tc.body), snapshot, BusinessSystemPromptTarget{Platform: tc.platform, Protocol: tc.protocol, UpstreamModel: "claude-opus-4-8"})
			require.NoError(t, err)
			require.Equal(t, tc.body, string(out))
			require.False(t, app.Applied)
			require.Empty(t, app.RulesPlan.Placements)
			require.Equal(t, 0, app.EffectiveByteLength)
			require.Equal(t, tc.reason, app.RulesPlan.Skipped[0].Reason)
		})
	}
}

func TestUnifiedPromptClaudeBlocksAndSameBoundaryOrder(t *testing.T) {
	snapshot := unifiedPromptSnapshot(t, "anthropic", []string{"auto", "system", "auto", "system"}, []string{"control_prepend", "control_append", "after_last_user", "after_last_user"}, []string{"top-before", "top-after", "message-first", "message-second"})
	input := []byte(`{"system":[{"type":"text","text":"client","cache_control":{"type":"ephemeral","ttl":"1h"},"citations":{"enabled":true}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"url","url":"https://example.invalid/a.png"}}]},{"role":"assistant","content":"answer"}]}`)
	out, app, err := ApplyBusinessSystemPromptToJSON(input, snapshot, BusinessSystemPromptTarget{Platform: "anthropic", Protocol: "messages", UpstreamModel: "claude-opus-4-8"})
	require.NoError(t, err)
	require.JSONEq(t, gjson.GetBytes(input, "system.0").Raw, gjson.GetBytes(out, "system.1").Raw)
	require.Equal(t, "top-before", gjson.GetBytes(out, "system.0.text").String())
	require.Equal(t, "top-after", gjson.GetBytes(out, "system.2.text").String())
	require.Equal(t, int64(3), gjson.GetBytes(out, "messages.#").Int())
	require.Equal(t, "system", gjson.GetBytes(out, "messages.1.role").String())
	require.Equal(t, "message-first", gjson.GetBytes(out, "messages.1.content.0.text").String())
	require.Equal(t, "message-second", gjson.GetBytes(out, "messages.1.content.1.text").String())
	require.Equal(t, 1, *app.RulesPlan.Placements[2].Index)
	require.Equal(t, 0, *app.RulesPlan.Placements[2].BlockIndex)
	require.Equal(t, 1, *app.RulesPlan.Placements[3].Index)
	require.Equal(t, 1, *app.RulesPlan.Placements[3].BlockIndex)
	require.JSONEq(t, gjson.GetBytes(input, "messages.0").Raw, gjson.GetBytes(out, "messages.0").Raw)
	require.JSONEq(t, gjson.GetBytes(input, "messages.1").Raw, gjson.GetBytes(out, "messages.2").Raw)
}

func TestUnifiedPromptStructuredContentAndGeminiCarrier(t *testing.T) {
	t.Run("migrated Claude blocks keep metadata", func(t *testing.T) {
		blocks := `[{"type":"text","text":"identity","cache_control":{"type":"ephemeral"}},{"type":"text","text":"custom","cache_control":{"type":"ephemeral","ttl":"1h"}}]`
		snapshot := unifiedPromptSnapshot(t, "anthropic", []string{"auto"}, []string{"control_prepend"}, []string{blocks})
		snapshot.ResolvedRules[0].ContentFormat = extensionv1.PromptContentAnthropicSystemBlocks
		snapshot.ResolvedRules[0].StructuredContent = json.RawMessage(blocks)
		input := []byte(`{"system":"client","messages":[{"role":"user","content":"hi"}]}`)
		out, _, err := ApplyBusinessSystemPromptToJSON(input, snapshot, BusinessSystemPromptTarget{Platform: "anthropic", Protocol: "messages"})
		require.NoError(t, err)
		require.Equal(t, "1h", gjson.GetBytes(out, "system.1.cache_control.ttl").String())
		require.Equal(t, "client", gjson.GetBytes(out, "system.2.text").String())
		require.JSONEq(t, gjson.GetBytes(input, "messages").Raw, gjson.GetBytes(out, "messages").Raw)
	})
	t.Run("Gemini preserves system metadata and contents", func(t *testing.T) {
		snapshot := unifiedPromptSnapshot(t, "antigravity", []string{"auto", "system"}, []string{"control_prepend", "control_append"}, []string{"before", "after"})
		input := []byte(`{"systemInstruction":{"role":"user","parts":[{"text":"client","custom":42}],"custom":"keep"},"contents":[{"role":"user","parts":[{"text":"hi"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}]}`)
		out, app, err := ApplyBusinessSystemPromptToJSON(input, snapshot, BusinessSystemPromptTarget{Platform: "antigravity", Protocol: "gemini"})
		require.NoError(t, err)
		require.True(t, app.Applied)
		require.Equal(t, "user", gjson.GetBytes(out, "systemInstruction.role").String())
		require.Equal(t, "keep", gjson.GetBytes(out, "systemInstruction.custom").String())
		require.Equal(t, "before", gjson.GetBytes(out, "systemInstruction.parts.0.text").String())
		require.JSONEq(t, gjson.GetBytes(input, "systemInstruction.parts.0").Raw, gjson.GetBytes(out, "systemInstruction.parts.1").Raw)
		require.Equal(t, "after", gjson.GetBytes(out, "systemInstruction.parts.2.text").String())
		require.JSONEq(t, gjson.GetBytes(input, "contents").Raw, gjson.GetBytes(out, "contents").Raw)
	})
}

func TestUnifiedPromptSkipsRehashActualPlanAndDoNotMutateSnapshot(t *testing.T) {
	snapshot := unifiedPromptSnapshot(t, "openai", []string{"auto", "developer"}, []string{"control_append", "before_last_user"}, []string{"native", "skipped"})
	input := []byte(`{"input":[{"role":"assistant","content":"done"}]}`)
	planned, err := PlanBusinessSystemPrompt(context.Background(), snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"})
	require.NoError(t, err)
	oldHash := planned.RulesPlan.SHA256
	out, app, err := applyPromptRules(input, planned)
	require.NoError(t, err)
	require.Equal(t, "native", gjson.GetBytes(out, "instructions").String())
	require.Len(t, app.RulesPlan.Placements, 1)
	require.Len(t, app.RulesPlan.Skipped, 1)
	require.NotEqual(t, oldHash, app.RulesPlan.SHA256)
	require.Len(t, planned.RulesPlan.Placements, 2)
	require.Empty(t, planned.RulesPlan.Skipped)
	require.Equal(t, oldHash, planned.RulesPlan.SHA256)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = ApplyBusinessSystemPromptToJSONContext(canceled, input, snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"})
	require.ErrorIs(t, err, context.Canceled)
}

func TestUnifiedPromptCompletedBuiltinAndExternalToolHistory(t *testing.T) {
	snapshot := unifiedPromptSnapshot(t, "openai", []string{"developer"}, []string{"before_last_user"}, []string{"site"})
	input := []byte(`{"input":[{"type":"web_search_call","id":"ws_done","status":"completed"},{"type":"mcp_call","id":"mcp_done","output":"ok"},{"type":"tool_search_call","call_id":"search","arguments":{"query":"find"}},{"type":"tool_search_output","call_id":"search","tools":[]},{"role":"user","content":"next"}]}`)
	out, app, err := ApplyBusinessSystemPromptToJSON(input, snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"})
	require.NoError(t, err)
	require.True(t, app.Applied)
	require.Equal(t, "site", gjson.GetBytes(out, "input.4.content").String())
	for index, item := range gjson.GetBytes(input, "input").Array()[:4] {
		require.JSONEq(t, item.Raw, gjson.GetBytes(out, fmt.Sprintf("input.%d", index)).Raw)
	}
}

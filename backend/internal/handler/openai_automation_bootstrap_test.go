package handler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const automationBootstrapPrompt = "Review the project and report any important changes."
const automationHeartbeat = `<heartbeat><automation_id>wiki</automation_id><current_time_iso>2026-09-09T06:17:00.123456789Z</current_time_iso><instructions>Review the project and report any important changes.</instructions></heartbeat>`

func codexAutomationBootstrap(automationID, lastRun, prompt string) string {
	return "Automation: Scheduled project review\n" +
		"Automation ID: " + automationID + "\n" +
		"Automation memory: $CODEX_HOME/automations/" + automationID + "/memory.md\n" +
		"Last run: " + lastRun + "\n\n" + prompt
}

func codexAutomationBootstrapBody(t *testing.T, output, callID string) []byte {
	t.Helper()
	return []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` +
		mustJSON(t, output) + callID + `}]}`)
}

func TestNormalizeCodexAutomationBootstrapSupportedLastRunValues(t *testing.T) {
	tests := []struct {
		name    string
		lastRun string
		crlf    bool
	}{
		{name: "never", lastRun: "never"},
		{name: "timestamp", lastRun: "2026-09-01T02:06:34.536Z (1788228394536)"},
		{name: "crlf", lastRun: "never", crlf: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := codexAutomationBootstrap("wiki-maintenance", tt.lastRun, automationBootstrapPrompt)
			if tt.crlf {
				output = strings.ReplaceAll(output, "\n", "\r\n")
			}
			got, changed := normalizeCodexAutomationBootstrap(codexAutomationBootstrapBody(t, output, ""))
			require.True(t, changed)
			require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
			require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
			require.Equal(t, output, gjson.GetBytes(got, "input.0.content.0.text").String())
			require.False(t, gjson.GetBytes(got, "input.0.call_id").Exists())
		})
	}
}

func TestNormalizeCodexAutomationBootstrapHeartbeat(t *testing.T) {
	tests := []struct {
		name   string
		output string
		callID string
	}{
		{name: "legacy id only", output: `<heartbeat><automation_id>wiki</automation_id></heartbeat>`},
		{name: "current missing call id", output: automationHeartbeat},
		{name: "current empty call id", output: automationHeartbeat, callID: `,"call_id":""`},
		{
			name:   "original whitespace and escaped instructions",
			output: "\n<heartbeat>\r\n  <instructions>\n Review &amp; report &lt;changes&gt;.\n </instructions>\r\n <automation_id>wiki-1</automation_id>\r\n <current_time_iso>2026-09-09T14:17:00+08:00</current_time_iso>\r\n</heartbeat>\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := normalizeCodexAutomationBootstrap(codexAutomationBootstrapBody(t, tt.output, tt.callID))
			require.True(t, changed)
			require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
			require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
			require.Equal(t, "input_text", gjson.GetBytes(got, "input.0.content.0.type").String())
			require.Equal(t, tt.output, gjson.GetBytes(got, "input.0.content.0.text").String())
			require.False(t, gjson.GetBytes(got, "input.0.call_id").Exists())

			again, changedAgain := normalizeCodexAutomationBootstrap(got)
			require.False(t, changedAgain)
			require.Equal(t, got, again)
		})
	}
}

func TestNormalizeCodexAutomationBootstrapRejectsUnsafeShapes(t *testing.T) {
	validOutput := codexAutomationBootstrap("wiki", "never", automationBootstrapPrompt)
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "ordinary missing call id",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"other","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "tui namespace",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_tui","name":"automation_update","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "valid call id",
			body: codexAutomationBootstrapBody(t, validOutput, `,"call_id":"call-1"`),
		},
		{
			name: "null call id",
			body: codexAutomationBootstrapBody(t, validOutput, `,"call_id":null`),
		},
		{
			name: "non-string call id",
			body: codexAutomationBootstrapBody(t, validOutput, `,"call_id":42`),
		},
		{
			name: "whitespace call id",
			body: codexAutomationBootstrapBody(t, validOutput, `,"call_id":"  "`),
		},
		{
			name: "non-string previous response",
			body: []byte(`{"model":"gpt-5","previous_response_id":null,"input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "ambiguous call context",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `},{"type":"custom_tool_call"}]}`),
		},
		{
			name: "ambiguous item reference",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `},{"type":"item_reference","id":""}]}`),
		},
		{
			name: "padded namespace",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":" codex_app ","name":"automation_update","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "padded name",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":" automation_update ","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "other type",
			body: []byte(`{"model":"gpt-5","input":[{"type":"custom_tool_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "non-string output",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":[{"type":"input_text","text":` + mustJSON(t, validOutput) + `}]}]}`),
		},
		{
			name: "duplicate call id",
			body: codexAutomationBootstrapBody(t, validOutput, `,"call_id":"call-1","call_id":""`),
		},
		{
			name: "duplicate input",
			body: []byte(`{"model":"gpt-5","input":[],"input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "mismatched memory id",
			body: codexAutomationBootstrapBody(t, strings.Replace(validOutput, "/wiki/memory.md", "/other/memory.md", 1), ""),
		},
		{
			name: "unsafe automation id",
			body: codexAutomationBootstrapBody(t, codexAutomationBootstrap("../wiki", "never", automationBootstrapPrompt), ""),
		},
		{
			name: "invalid timestamp",
			body: codexAutomationBootstrapBody(t, codexAutomationBootstrap("wiki", "yesterday", automationBootstrapPrompt), ""),
		},
		{
			name: "mismatched timestamp epoch",
			body: codexAutomationBootstrapBody(t, codexAutomationBootstrap("wiki", "2026-09-01T02:06:34.536Z (1)", automationBootstrapPrompt), ""),
		},
		{
			name: "missing separator",
			body: codexAutomationBootstrapBody(t, strings.Replace(validOutput, "\n\n"+automationBootstrapPrompt, "\n"+automationBootstrapPrompt, 1), ""),
		},
		{
			name: "empty prompt",
			body: codexAutomationBootstrapBody(t, codexAutomationBootstrap("wiki", "never", " \n"), ""),
		},
		{
			name: "mixed missing call id output",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `},{"type":"computer_call_output","output":"done"}]}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := normalizeCodexAutomationBootstrap(tt.body)
			require.False(t, changed)
			require.Equal(t, tt.body, got)
		})
	}
}

func TestNormalizeCodexAutomationBootstrapRejectsUnsafeHeartbeatShapes(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "arbitrary tool output", output: `<result><automation_id>wiki</automation_id></result>`},
		{name: "root attribute", output: `<heartbeat status="ok"><automation_id>wiki</automation_id></heartbeat>`},
		{name: "namespaced root", output: `<heartbeat xmlns="urn:codex"><automation_id>wiki</automation_id></heartbeat>`},
		{name: "extra child", output: `<heartbeat><automation_id>wiki</automation_id><status>ok</status></heartbeat>`},
		{name: "nested id content", output: `<heartbeat><automation_id><value>wiki</value></automation_id></heartbeat>`},
		{name: "padded id", output: `<heartbeat><automation_id> wiki </automation_id></heartbeat>`},
		{name: "unsafe id", output: `<heartbeat><automation_id>../wiki</automation_id></heartbeat>`},
		{name: "comment", output: `<heartbeat><!-- ok --><automation_id>wiki</automation_id></heartbeat>`},
		{name: "trailing content", output: `<heartbeat><automation_id>wiki</automation_id></heartbeat>extra`},
		{name: "multiple roots", output: `<heartbeat><automation_id>wiki</automation_id></heartbeat><heartbeat><automation_id>wiki</automation_id></heartbeat>`},
		{name: "missing automation id", output: `<heartbeat><current_time_iso>2026-09-09T06:17:00Z</current_time_iso><instructions>Review.</instructions></heartbeat>`},
		{name: "partial current timestamp", output: `<heartbeat><automation_id>wiki</automation_id><current_time_iso>2026-09-09T06:17:00Z</current_time_iso></heartbeat>`},
		{name: "partial current instructions", output: `<heartbeat><automation_id>wiki</automation_id><instructions>Review.</instructions></heartbeat>`},
		{name: "invalid timestamp", output: strings.Replace(automationHeartbeat, "2026-09-09T06:17:00.123456789Z", "yesterday", 1)},
		{name: "timestamp without timezone", output: strings.Replace(automationHeartbeat, "2026-09-09T06:17:00.123456789Z", "2026-09-09T06:17:00", 1)},
		{name: "padded timestamp", output: strings.Replace(automationHeartbeat, "2026-09-09T06:17:00.123456789Z", " 2026-09-09T06:17:00Z ", 1)},
		{name: "empty instructions", output: strings.Replace(automationHeartbeat, automationBootstrapPrompt, " \n\t", 1)},
		{name: "nested instructions", output: strings.Replace(automationHeartbeat, automationBootstrapPrompt, "<text>Review.</text>", 1)},
		{name: "instructions attribute", output: strings.Replace(automationHeartbeat, "<instructions>", `<instructions mode="all">`, 1)},
		{name: "namespaced child", output: strings.Replace(automationHeartbeat, "<instructions>", `<instructions xmlns="urn:codex">`, 1)},
		{name: "duplicate id", output: strings.Replace(automationHeartbeat, "</automation_id>", "</automation_id><automation_id>wiki</automation_id>", 1)},
		{name: "duplicate timestamp", output: strings.Replace(automationHeartbeat, "</current_time_iso>", "</current_time_iso><current_time_iso>2026-09-09T06:17:00Z</current_time_iso>", 1)},
		{name: "duplicate instructions", output: strings.Replace(automationHeartbeat, "</instructions>", "</instructions><instructions>Review again.</instructions>", 1)},
		{name: "processing instruction", output: `<?xml version="1.0"?>` + automationHeartbeat},
		{name: "unknown entity", output: strings.Replace(automationHeartbeat, automationBootstrapPrompt, "&unknown;", 1)},
		{name: "unclosed root", output: strings.TrimSuffix(automationHeartbeat, "</heartbeat>")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := codexAutomationBootstrapBody(t, tt.output, "")
			got, changed := normalizeCodexAutomationBootstrap(body)
			require.False(t, changed)
			require.Equal(t, body, got)
		})
	}
}

func TestNormalizeCodexAutomationBootstrapWithLongHistoricalContext(t *testing.T) {
	for _, heartbeatCount := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d heartbeats", heartbeatCount), func(t *testing.T) {
			items := []string{
				`{"type" : "message", "role":"user", "content":"before\u003cstart\u003e"}`,
				`{"type":"reasoning", "id":"rs-1", "encrypted_content":"opaque%2F+==", "summary":[]}`,
				`{"type":"web_search_call", "id":"ws-1", "status":"completed", "action":{"type":"search","query":"project notes"}}`,
				`{"type":"item_reference", "id":"item-1"}`,
			}
			heartbeats := make(map[int]string, heartbeatCount)
			for i := 0; i < 39; i++ {
				items = append(items,
					fmt.Sprintf(`{"type" : "custom_tool_call", "call_id":"call-%d", "name":"apply_patch", "input":"patch\n", "metadata":{"integer":9007199254740993}}`, i),
					fmt.Sprintf(`{"type":"custom_tool_call_output", "call_id":"call-%d", "output":"history\u003cvalue\u003e\n", "metadata":{"decimal":1.2300e+02}}`, i),
				)
				if i == 18 || (heartbeatCount == 2 && i == 38) {
					output := automationHeartbeat
					callID := ""
					if i == 38 {
						output = strings.Replace(output, "06:17:00.123456789Z", "06:27:00Z", 1)
						callID = `,"call_id":""`
					}
					heartbeats[len(items)] = output
					items = append(items, `{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":`+mustJSON(t, output)+callID+`}`)
				}
			}
			// A real result for this exact tool and envelope must remain historical,
			// not stop normalization of independent heartbeat inputs elsewhere.
			items = append(items,
				`{"type":"function_call", "namespace":"codex_app", "name":"automation_update", "call_id":"call-real", "arguments":"{}"}`,
				`{"type":"function_call_output", "namespace":"codex_app", "name":"automation_update", "call_id":"call-real", "output":`+mustJSON(t, automationHeartbeat)+`}`,
				`{"type":"message", "role":"user", "content":"after"}`,
			)
			body := []byte("{\n \"model\":\"gpt-5\", \"previous_response_id\":\"resp-1\",\n" +
				` "metadata" : {"integer":9007199254740993,"decimal":1.2300e+02,"escaped":"\u0061","padding":[ 1 , 2 ]},` +
				"\n \"input\":[\n  " + strings.Join(items, ",\n  ") + "\n ]\n}")

			got, changed := normalizeCodexAutomationBootstrap(body)
			require.True(t, changed)
			for _, field := range []string{"model", "previous_response_id", "metadata"} {
				require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(got, field).Raw, field)
			}
			gotItems := gjson.GetBytes(got, "input").Array()
			require.Len(t, gotItems, len(items))
			for i, original := range items {
				if output, converted := heartbeats[i]; converted {
					require.Equal(t, "message", gotItems[i].Get("type").String())
					require.Equal(t, "user", gotItems[i].Get("role").String())
					require.Equal(t, "input_text", gotItems[i].Get("content.0.type").String())
					require.Equal(t, output, gotItems[i].Get("content.0.text").String())
					require.False(t, gotItems[i].Get("call_id").Exists())
					continue
				}
				require.Equal(t, original, gotItems[i].Raw, "history item %d", i)
			}

			again, changedAgain := normalizeCodexAutomationBootstrap(got)
			require.False(t, changedAgain)
			require.Equal(t, got, again)
		})
	}
}

func TestNormalizeCodexAutomationBootstrapWithServerToolHistory(t *testing.T) {
	tests := []struct {
		name    string
		item    string
		changed bool
	}{
		{
			name:    "web search",
			item:    `{"type" : "web_search_call", "id":"ws_history", "status":"completed", "action":{"type":"search","query":"notes"}}`,
			changed: true,
		},
		{
			name:    "file search",
			item:    `{"type":"file_search_call", "id":"fs_history", "status":"completed", "queries":["notes"], "results":[]}`,
			changed: true,
		},
		{
			name:    "code interpreter",
			item:    `{"type":"code_interpreter_call", "id":"ci_history", "status":"completed", "container_id":"cntr_history", "code":"print(1)", "outputs":[{"type":"logs","logs":"1\n"}]}`,
			changed: true,
		},
		{
			name:    "image generation",
			item:    `{"type":"image_generation_call", "id":"ig_history", "status":"completed", "result":"aW1hZ2U="}`,
			changed: true,
		},
		{
			name:    "mcp",
			item:    `{"type":"mcp_call", "id":"mcp_history", "status":"completed", "server_label":"docs", "name":"read", "arguments":"{}", "output":"notes\u003cvalue\u003e"}`,
			changed: true,
		},
		{
			name:    "server tool search",
			item:    `{"type":"tool_search_call", "id":"tsc_history", "status":"completed", "execution":"server", "arguments":{"query":"docs"}}`,
			changed: true,
		},
		{name: "client tool search", item: `{"type":"tool_search_call","id":"tsc_history","execution":"client","arguments":{"query":"docs"}}`},
		{name: "ambiguous tool search execution", item: `{"type":"tool_search_call","id":"tsc_history","arguments":{"query":"docs"}}`},
		{name: "function call item id is not call id", item: `{"type":"function_call","id":"fc_history","name":"read","arguments":"{}"}`},
		{name: "function output item id is not call id", item: `{"type":"function_call_output","id":"fc_history","output":"done"}`},
		{name: "custom call item id is not call id", item: `{"type":"custom_tool_call","id":"ctc_history","name":"read","input":"notes"}`},
		{name: "custom output item id is not call id", item: `{"type":"custom_tool_call_output","id":"fc_history","output":"done"}`},
		{name: "computer call item id is not call id", item: `{"type":"computer_call","id":"cu_history","action":{"type":"screenshot"}}`},
		{name: "computer output item id is not call id", item: `{"type":"computer_call_output","id":"cu_history","output":"done"}`},
		{name: "unknown call with item id", item: `{"type":"unknown_call","id":"unknown_history"}`},
		{name: "missing server item id", item: `{"type":"web_search_call","status":"completed"}`},
		{name: "empty server item id", item: `{"type":"web_search_call","id":" "}`},
		{name: "non-string server item id", item: `{"type":"web_search_call","id":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5","input":[` + tt.item + `,{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, automationHeartbeat) + `}]}`)
			got, changed := normalizeCodexAutomationBootstrap(body)
			require.Equal(t, tt.changed, changed)
			if !tt.changed {
				require.Equal(t, body, got)
				return
			}
			require.Equal(t, tt.item, gjson.GetBytes(got, "input.0").Raw)
			require.Equal(t, "message", gjson.GetBytes(got, "input.1.type").String())
			require.Equal(t, "user", gjson.GetBytes(got, "input.1.role").String())
			require.Equal(t, automationHeartbeat, gjson.GetBytes(got, "input.1.content.0.text").String())

			// The extra server-tool-history allowance belongs only to automation;
			// delegation's existing historical context boundary is unchanged.
			delegationBody := []byte(`{"model":"gpt-5","input":[` + tt.item + `,{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSON(t, delegationEnvelope) + `}]}`)
			delegationGot, delegationChanged := normalizeCodexDelegationBootstrap(delegationBody)
			require.False(t, delegationChanged)
			require.Equal(t, delegationBody, delegationGot)
		})
	}
}

func TestNormalizeCodexAutomationBootstrapPreservesOrderAndIsIdempotent(t *testing.T) {
	output := codexAutomationBootstrap("wiki", "never", automationBootstrapPrompt)
	body := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":"before"},{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, output) + `},{"type":"message","role":"user","content":"after"}]}`)

	got, changed := normalizeCodexAutomationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "before", gjson.GetBytes(got, "input.0.content").String())
	require.Equal(t, output, gjson.GetBytes(got, "input.1.content.0.text").String())
	require.Equal(t, "after", gjson.GetBytes(got, "input.2.content").String())

	again, changedAgain := normalizeCodexAutomationBootstrap(got)
	require.False(t, changedAgain)
	require.Equal(t, got, again)
}

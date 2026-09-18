package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ensureCodexReasoningInclude：带 reasoning 时补齐 include，幂等且保留既有项。
func TestEnsureCodexReasoningInclude(t *testing.T) {
	// reasoning 存在、include 缺失 → 注入
	body := map[string]any{"reasoning": map[string]any{"effort": "medium"}}
	require.True(t, ensureCodexReasoningInclude(body))
	require.Equal(t, []any{"reasoning.encrypted_content"}, body["include"])
	// 幂等：再次调用不重复
	require.False(t, ensureCodexReasoningInclude(body))

	// 无 reasoning → 不动
	body2 := map[string]any{}
	require.False(t, ensureCodexReasoningInclude(body2))
	_, ok := body2["include"]
	require.False(t, ok)

	// 既有 include 保留并追加
	body3 := map[string]any{
		"reasoning": map[string]any{"effort": "high"},
		"include":   []any{"foo"},
	}
	require.True(t, ensureCodexReasoningInclude(body3))
	require.Equal(t, []any{"foo", "reasoning.encrypted_content"}, body3["include"])
}

// applyCodexClientMetadata：用账号真实 device_id 注入 installation 标识，幂等、不覆盖既有项、不伪造。
func TestApplyCodexClientMetadata(t *testing.T) {
	// 仅 OpenAI OAuth 账号才有 device_id（GetOpenAIDeviceID 的门控）。
	acc := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_device_id": "dev-xyz"}}

	body := map[string]any{}
	require.True(t, applyCodexClientMetadata(body, acc))
	cm, ok := body["client_metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "dev-xyz", cm["x-codex-installation-id"])
	// 幂等
	require.False(t, applyCodexClientMetadata(body, acc))

	// OAuth 账号但无 device_id → 不写入（不伪造）
	body2 := map[string]any{}
	require.False(t, applyCodexClientMetadata(body2, &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	_, ok = body2["client_metadata"]
	require.False(t, ok)

	// 既有 client_metadata（如 turn metadata）保留，仅补 installation 键
	body3 := map[string]any{"client_metadata": map[string]any{"x-codex-turn-metadata": "t"}}
	require.True(t, applyCodexClientMetadata(body3, acc))
	cm3, _ := body3["client_metadata"].(map[string]any)
	require.Equal(t, "t", cm3["x-codex-turn-metadata"])
	require.Equal(t, "dev-xyz", cm3["x-codex-installation-id"])
}

// defaultCodexSynthInstructions：按模型选用真实 Codex base prompt。
func TestDefaultCodexSynthInstructionsModelAware(t *testing.T) {
	require.True(t, strings.Contains(defaultCodexSynthInstructions("gpt-5-codex"), "You are Codex, based on GPT-5"))
	require.True(t, strings.Contains(defaultCodexSynthInstructions("gpt-5.5"), "You are Codex, a coding agent based on GPT-5"))
	require.False(t, strings.Contains(defaultCodexSynthInstructions("gpt-5.5"), "You are GPT-5.1 running in the Codex CLI"))
	require.True(t, strings.Contains(defaultCodexSynthInstructions("gpt-5.2"), "You are GPT-5.2 running in the Codex CLI"))
	require.True(t, strings.Contains(defaultCodexSynthInstructions("gpt-5.1"), "You are GPT-5.1 running in the Codex CLI"))
}

// compareRequestIntegrity：已记录的无损 Codex 兼容转换两侧归一后相等；真实丢失/改写只报告首个差异字段名。
func TestRequestIntegrityCanonicalizationCases(t *testing.T) {
	account := &Account{
		ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"model_mapping": map[string]any{"my-model": "gpt-5.5"}},
	}
	cases := []struct {
		name, before, after, want string
		opts                      requestIntegrityOptions
	}{
		{
			name:   "string input becomes message array",
			before: `{"model":"gpt-5.4","input":"hi"}`,
			after:  `{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"store":false,"stream":true}`,
		},
		{
			name:   "reserved tool alias",
			before: `{"tools":[{"type":"function","name":"python"}]}`,
			after:  `{"tools":[{"type":"function","name":"python__sub2api"}]}`,
		},
		{
			name:   "namespace flatten",
			before: `{"tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"lookup"}]}]}`,
			after:  `{"tools":[{"type":"function","name":"ns__lookup"}]}`,
			opts:   requestIntegrityOptions{FlattenNamespaces: true},
		},
		{
			name:   "lite additional_tools move",
			before: `{"tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"lookup"}]}],"reasoning":{"effort":"low"}}`,
			after:  `{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"lookup"}]}]}],"reasoning":{"effort":"low","context":"all_turns"},"parallel_tool_calls":false}`,
			opts:   requestIntegrityOptions{ResponsesLite: true},
		},
		{
			name:   "account model mapping",
			before: `{"model":"my-model"}`,
			after:  `{"model":"gpt-5.5"}`,
		},
		{
			name:   "service-owned instructions suffix",
			before: `{"instructions":"client"}`,
			after:  `{"instructions":"client\n\nserver"}`,
		},
		{
			name:   "image tool injected when bridge allowed",
			before: `{"tools":[{"type":"function","name":"lookup"}]}`,
			after:  `{"tools":[{"type":"function","name":"lookup"},{"type":"image_generation","output_format":"png"}],"tool_choice":"auto"}`,
			opts:   requestIntegrityOptions{ImageBridgeEnabled: true},
		},
		{
			name:   "dropped input item",
			before: `{"input":[{"type":"message","role":"user","content":"a"},{"type":"message","role":"user","content":"b"}]}`,
			after:  `{"input":[{"type":"message","role":"user","content":"a"}]}`,
			want:   "input",
		},
		{
			name:   "dropped tool",
			before: `{"tools":[{"type":"function","name":"lookup"}]}`,
			after:  `{"tools":[]}`,
			want:   "tools",
		},
		{
			name:   "changed previous_response_id",
			before: `{"previous_response_id":"resp_a"}`,
			after:  `{"previous_response_id":"resp_b"}`,
			want:   "previous_response_id",
		},
		{
			name:   "lost encrypted reasoning item",
			before: `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]},{"type":"message","role":"user","content":"hi"}]}`,
			after:  `{"input":[{"type":"message","role":"user","content":"hi"}]}`,
			want:   "input",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field, err := compareRequestIntegrity(account, tc.opts, []byte(tc.before), []byte(tc.after))
			require.NoError(t, err)
			require.Equal(t, tc.want, field)
		})
	}
}

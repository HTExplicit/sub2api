package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

type promptCoreInitializationStore struct {
	promptConfigMemoryStore
	initializations int
}

func (s *promptCoreInitializationStore) InitializeIndependentPrompts(context.Context, RemoteSkillRegistryFiles) (map[string][]byte, error) {
	s.initializations++
	return map[string][]byte{}, nil
}
func (*promptCoreInitializationStore) PromptVersionPreserveEcho(context.Context, int64) (bool, error) {
	return false, nil
}
func (*promptCoreInitializationStore) PromptRuleHistory(context.Context, string) ([]PromptHistoryVersion, error) {
	return nil, nil
}

func TestPromptCoreProviderInitializationIsNotRepeatedByDomainStart(t *testing.T) {
	store := &promptCoreInitializationStore{}
	store.loaded = BusinessSystemPromptSnapshot{Revision: 7, RulePolicy: &nativeapi.PromptRulePolicy{Version: 2, Rules: []nativeapi.PromptRule{}, DefaultRuleIDs: []string{}}}
	svc := NewBusinessSystemPromptService(store, nil)
	svc.SetFrozenPromptFiles(ProvideFrozenPromptFiles(store, nil))
	require.NoError(t, svc.Initialize(context.Background()))
	runtime := NewPromptDomainRuntime(svc)
	runtime.Start(context.Background())
	t.Cleanup(runtime.Stop)
	require.True(t, runtime.ready)
	require.Equal(t, 1, store.initializations)
	require.Equal(t, 1, store.loadCalls)
}

func TestPromptCoreOmittedCompatibilityFlagsArePreserved(t *testing.T) {
	svc, store, _, input := newPromptConfigService(t)
	store.loaded.CompactEnabled, store.loaded.ExposeServerPrompt = true, true
	require.NoError(t, svc.Reload(context.Background()))
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &payload))
	delete(payload, "compact_enabled")
	delete(payload, "expose_server_prompt")
	raw, err = json.Marshal(payload)
	require.NoError(t, err)
	var decoded PromptConfigUpdate
	require.NoError(t, json.Unmarshal(raw, &decoded))
	state, err := svc.SavePromptConfig(context.Background(), decoded, 7)
	require.NoError(t, err)
	require.True(t, state.CompactEnabled)
	require.True(t, state.ExposeServerPrompt)
	require.True(t, store.last.CompactEnabled && store.last.ExposeServerPrompt)
}

func TestPromptCoreRejectsRebindingNewRulesToOldSources(t *testing.T) {
	svc, store, _, input := newPromptConfigService(t)
	input.Policy.Rules[0].TemplateID, input.Policy.Rules[0].VersionID = 42, 43
	_, err := svc.SavePromptConfig(context.Background(), input, 7)
	require.ErrorIs(t, err, ErrBusinessSystemPromptInvalid)
	require.Zero(t, store.saves)
}

func TestPromptCoreStructuredTextEditsPreserveMetadata(t *testing.T) {
	body := `{"blocks":[{"type":"text","text":"first","enabled":false},{"type":"text","text":"second","cache_control":{"type":"ephemeral","ttl":"1h","custom":"keep"}}],"expansion_prompt":"literal {fp}"}`
	require.NoError(t, ValidateStructuredPromptTextEdit(body, strings.Replace(body, "second", "new second", 1)))
	require.NoError(t, ValidateStructuredPromptTextEdit(body, strings.Replace(body, "literal {fp}", "new expansion", 1)))
	array := `[{"type":"text","text":"array text","cache_control":{"type":"ephemeral","ttl":"1h"}},null,{"enabled":false}]`
	require.NoError(t, ValidateStructuredPromptTextEdit(array, strings.Replace(array, "array text", "edited array", 1)))
	for _, changed := range []string{strings.Replace(body, `"enabled":false`, `"enabled":true`, 1), strings.Replace(body, `"ttl":"1h"`, `"ttl":"5m"`, 1), `{"blocks":[]}`} {
		require.ErrorIs(t, ValidateStructuredPromptTextEdit(body, changed), ErrBusinessSystemPromptInvalid)
	}
}

func TestPromptCoreFrozenReaderDoesNotFollowSourceOrExposeMutableBytes(t *testing.T) {
	files := &FrozenPromptFiles{files: map[string][]byte{"SKILL.md": []byte("frozen publication"), "scripts/tool.py": []byte("print('frozen')")}}
	first, err := files.LoadPublishedFile(context.Background(), "SKILL.md")
	require.NoError(t, err)
	first.Body[0] = 'X'
	next, err := files.LoadPublishedFile(context.Background(), "SKILL.md")
	require.NoError(t, err)
	require.Equal(t, "frozen publication", string(next.Body))
	require.Equal(t, first.ETag, next.ETag)
	for _, path := range []string{"../SKILL.md", "scripts/../SKILL.md", "SKILL.md?x", "missing.md"} {
		_, err := files.LoadPublishedFile(context.Background(), path)
		require.ErrorIs(t, err, ErrRemoteSkillPublicFileNotFound)
	}
	require.True(t, PreserveRestoredPromptEcho("public", "public", true))
	require.False(t, PreserveRestoredPromptEcho("public", "edited public", true))
	require.False(t, PreserveRestoredPromptEcho("private", "private", false))
}

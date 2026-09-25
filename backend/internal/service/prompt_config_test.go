package service

import (
	"context"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type promptConfigMemoryStore struct {
	fakeBusinessSystemPromptStore
	saves int
	last  PromptConfigUpdate
}

func (f *promptConfigMemoryStore) SavePromptConfig(_ context.Context, input PromptConfigUpdate, _ int64) error {
	f.saves++
	f.last = input
	policy := input.Policy
	policy.Rules = append([]extensionv1.PromptRule(nil), input.Policy.Rules...)
	for i := range policy.Rules {
		rule := &policy.Rules[i]
		if draft, ok := input.Contents[rule.ID]; ok {
			rule.TemplateID, rule.VersionID = 11, 12
			hash, size, err := ValidateBusinessSystemPromptBody(draft.Body)
			if err != nil {
				return err
			}
			f.detail = BusinessSystemPromptTemplateDetail{
				Template: BusinessSystemPromptTemplate{ID: 11},
				Versions: []BusinessSystemPromptVersion{{ID: 12, TemplateID: 11, Version: 1, Body: draft.Body, SHA256: hash, ByteLength: size, CompositionMode: BusinessSystemPromptCompositionInline}},
			}
		}
	}
	f.loaded = BusinessSystemPromptSnapshot{Revision: input.ExpectedRevision + 1, Enabled: input.Enabled,
		ExposeServerPrompt: input.ExposeServerPrompt, CompactEnabled: input.CompactEnabled, RulePolicy: &policy}
	return nil
}

func newPromptConfigService(t *testing.T) (*BusinessSystemPromptService, *promptConfigMemoryStore, *fakeBusinessSystemPromptBus, PromptConfigUpdate) {
	t.Helper()
	store := &promptConfigMemoryStore{}
	store.loaded = BusinessSystemPromptSnapshot{Revision: 4, Enabled: true,
		RulePolicy: &extensionv1.PromptRulePolicy{Version: 2, Rules: []extensionv1.PromptRule{}, DefaultRuleIDs: []string{}}}
	bus := &fakeBusinessSystemPromptBus{}
	svc := NewBusinessSystemPromptService(store, bus)
	require.NoError(t, svc.Reload(context.Background()))
	rule := extensionv1.PromptRule{ID: "new", Name: "New", Enabled: true, Role: "developer", Position: "before_last_user", Platforms: []string{PlatformOpenAI}, ModelMatch: "upstream", Models: []string{}}
	input := PromptConfigUpdate{ExpectedRevision: 4, Enabled: true,
		Policy:   extensionv1.PromptRulePolicy{Version: 2, Rules: []extensionv1.PromptRule{rule}, DefaultRuleIDs: []string{"new"}},
		Contents: map[string]PromptContentDraft{"new": {Body: "unsaved instruction"}},
	}
	return svc, store, bus, input
}

func TestPromptConfigSavesContentAndRulesTogether(t *testing.T) {
	svc, store, bus, input := newPromptConfigService(t)
	state, err := svc.SavePromptConfig(context.Background(), input, 7)
	require.NoError(t, err)
	require.Equal(t, 1, store.saves)
	require.Equal(t, []int64{5}, bus.revisions)
	require.Equal(t, int64(5), state.Revision)
	require.Equal(t, int64(12), state.Policy.Rules[0].VersionID)
	require.Equal(t, "unsaved instruction", state.Contents["new"].Body)
	require.NotEmpty(t, state.Capabilities)
	snapshot, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.False(t, snapshot.Draft)
	output, application, err := ApplyBusinessSystemPromptToJSON([]byte(`{"model":"gpt-test","input":"question"}`), snapshot,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, ProviderPlatform: PlatformOpenAI, Protocol: "responses", AccountType: AccountTypeAPIKey})
	require.NoError(t, err)
	require.True(t, application.Applied)
	require.Equal(t, "developer", gjson.GetBytes(output, "input.0.role").String())
	require.Equal(t, "unsaved instruction", gjson.GetBytes(output, "input.0.content").String())
}

func TestPromptConfigDraftAndRejectedEditsDoNotWrite(t *testing.T) {
	svc, store, bus, input := newPromptConfigService(t)
	snapshot, _ := svc.CurrentSnapshot()
	snapshot.RulePolicy = &input.Policy
	require.NoError(t, svc.preparePromptRulesDraft(context.Background(), &snapshot, input.Contents))
	output, _, err := ApplyBusinessSystemPromptToJSON([]byte(`{"input":"question"}`), snapshot,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, ProviderPlatform: PlatformOpenAI, Protocol: "responses", AccountType: AccountTypeAPIKey})
	require.NoError(t, err)
	require.Equal(t, "unsaved instruction", gjson.GetBytes(output, "input.0.content").String())
	require.Zero(t, store.saves)
	current, _ := svc.CurrentSnapshot()
	require.Empty(t, current.RulePolicy.Rules)
	input.ExpectedRevision = 3
	_, err = svc.SavePromptConfig(context.Background(), input, 7)
	require.ErrorIs(t, err, ErrBusinessSystemPromptRevisionConflict)
	input.ExpectedRevision = 4
	input.Contents["unknown"] = PromptContentDraft{Body: "not attached to a rule"}
	_, err = svc.SavePromptConfig(context.Background(), input, 7)
	require.ErrorIs(t, err, ErrBusinessSystemPromptInvalid)
	require.Zero(t, store.saves)
	require.Empty(t, bus.revisions)
}

func TestPromptConfigInlineRuntimeStartsWithoutSkillRegistry(t *testing.T) {
	svc, _, _, _ := newPromptConfigService(t)
	svc.bus = nil
	runtime := NewPromptDomainRuntime(svc)
	runtime.Start(context.Background())
	t.Cleanup(runtime.Stop)
	require.True(t, runtime.ready)
	snapshot, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.NotNil(t, snapshot.RulePolicy)
}

func TestPromptConfigSourceFailureDoesNotPreventDisabling(t *testing.T) {
	svc, store, _, input := newPromptConfigService(t)
	_, err := svc.SavePromptConfig(context.Background(), input, 7)
	require.NoError(t, err)
	version := store.detail.Versions[0]
	version.ID = 13
	version.CompositionMode = BusinessSystemPromptCompositionCodexSkillHybrid
	version.BundleID = BusinessSystemPromptRemoteSkillBundleID
	store.detail.Versions = append(store.detail.Versions, version)
	rule := store.loaded.RulePolicy.Rules[0]
	rule.ID, rule.VersionID, rule.Position, rule.Role = "remote", 13, "control_append", "auto"
	store.loaded.RulePolicy.Rules = append(store.loaded.RulePolicy.Rules, rule)
	// Loading a missing optional source keeps unrelated inline rules available.
	require.NoError(t, svc.Reload(context.Background()))
	snapshot, _ := svc.CurrentSnapshot()
	require.True(t, snapshot.Degraded)
	output, application, err := ApplyBusinessSystemPromptToJSON([]byte(`{"input":"question"}`), snapshot,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, ProviderPlatform: PlatformOpenAI, Protocol: "responses", AccountType: AccountTypeAPIKey})
	require.NoError(t, err)
	require.True(t, application.Applied)
	require.Equal(t, "unsaved instruction", gjson.GetBytes(output, "input.0.content").String())
	state, err := svc.SavePromptConfig(context.Background(), PromptConfigUpdate{
		ExpectedRevision: snapshot.Revision, Policy: *snapshot.RulePolicy, Enabled: false,
	}, 7)
	require.NoError(t, err)
	require.False(t, state.Enabled)
	require.False(t, state.Contents["remote"].Available)
}

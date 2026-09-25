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

type changingPromptPublicationStore struct {
	fakeBusinessSystemPromptStore
	onRead func()
}

func (s *changingPromptPublicationStore) GetBusinessSystemPromptTemplate(_ context.Context, id int64) (BusinessSystemPromptTemplateDetail, error) {
	if s.onRead != nil {
		s.onRead()
	}
	detail := s.detail
	detail.Template.ID = id
	detail.Versions = append([]BusinessSystemPromptVersion(nil), detail.Versions...)
	for i := range detail.Versions {
		detail.Versions[i].TemplateID = id
	}
	return detail, nil
}

func TestPromptConfigCompilesOnePublicationGeneration(t *testing.T) {
	registry := &RemoteSkillRegistryService{}
	publish := func(revision int64, body string) {
		t.Helper()
		hash := hashBusinessSystemPromptBundleBytes([]byte(body))
		require.NoError(t, registry.installPublication(RemoteSkillPublication{
			Revision: revision, CandidateID: revision, EffectiveTreeSHA256: strings.Repeat("a", 64),
			EffectivePromptSHA256: hash, EffectivePromptBody: body,
			Files: map[string][]byte{"SKILL.md": []byte("fixture")}, Prompt: RemoteSkillPromptVersion{ID: revision, EffectiveSHA256: hash},
		}))
	}
	publish(1, "first publication")
	baseHash, baseSize, err := ValidateBusinessSystemPromptBody("base")
	require.NoError(t, err)
	store := &changingPromptPublicationStore{}
	store.detail = BusinessSystemPromptTemplateDetail{Versions: []BusinessSystemPromptVersion{{
		ID: 2, Body: "base", SHA256: baseHash, ByteLength: baseSize,
		CompositionMode: BusinessSystemPromptCompositionCodexSkillHybrid, BundleID: BusinessSystemPromptRemoteSkillBundleID,
	}}}
	store.loaded = BusinessSystemPromptSnapshot{Revision: 7, Enabled: true, RulePolicy: &extensionv1.PromptRulePolicy{Version: 2, DefaultRuleIDs: []string{"one", "two"}, Rules: []extensionv1.PromptRule{
		{ID: "one", Name: "One", Enabled: true, TemplateID: 1, VersionID: 2, Role: "auto", Platforms: []string{"openai"}, Position: "control_append"},
		{ID: "two", Name: "Two", Enabled: true, TemplateID: 3, VersionID: 2, Role: "auto", Platforms: []string{"openai"}, Position: "control_append"},
	}}}
	reads := 0
	store.onRead = func() {
		reads++
		if reads == 2 {
			publish(2, "second publication")
		}
	}
	svc := NewBusinessSystemPromptService(store, nil)
	svc.registry = registry
	require.NoError(t, svc.Reload(context.Background()))
	first, _ := svc.CurrentSnapshot()
	require.Equal(t, int64(1), first.RegistryRevision)
	for _, rule := range first.ResolvedRules {
		require.Equal(t, "first publication", rule.Body)
	}
	store.onRead = nil
	require.NoError(t, svc.Reload(context.Background()))
	second, _ := svc.CurrentSnapshot()
	require.Equal(t, int64(2), second.RegistryRevision)
	for _, rule := range second.ResolvedRules {
		require.Equal(t, "second publication", rule.Body)
	}
	svc.installBusinessSystemPromptSnapshot(first)
	current, _ := svc.CurrentSnapshot()
	require.Equal(t, int64(2), current.RegistryRevision, "late compilation of the same configuration cannot rewind its source")
	reads = 0
	store.onRead = func() {
		reads++
		if reads == 2 {
			publish(3, "third publication")
		}
	}
	config, err := svc.PromptConfig(context.Background())
	require.NoError(t, err)
	for _, content := range config.Contents {
		require.Equal(t, "second publication", content.Body)
	}
}

func TestPromptConfigBudgetsOnlySelectedPlatformContent(t *testing.T) {
	store := &promptConfigMemoryStore{}
	body := strings.Repeat("x", 8<<10)
	hash, size, err := ValidateBusinessSystemPromptBody(body)
	require.NoError(t, err)
	store.detail = BusinessSystemPromptTemplateDetail{Template: BusinessSystemPromptTemplate{ID: 1}, Versions: []BusinessSystemPromptVersion{{ID: 2, TemplateID: 1, Body: body, SHA256: hash, ByteLength: size, CompositionMode: "inline"}}}
	policy := extensionv1.PromptRulePolicy{Version: 2, Rules: []extensionv1.PromptRule{}, DefaultRuleIDs: []string{}}
	for i := 0; i < 33; i++ {
		platform := "openai"
		if i == 32 {
			platform = "anthropic"
		}
		id := fmt.Sprintf("rule-%d", i)
		policy.Rules = append(policy.Rules, extensionv1.PromptRule{ID: id, Name: id, Enabled: true, TemplateID: 1, VersionID: 2, Role: "auto", Position: "control_append", Platforms: []string{platform}})
		policy.DefaultRuleIDs = append(policy.DefaultRuleIDs, id)
	}
	svc := NewBusinessSystemPromptService(store, nil)
	snapshot := BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, RulePolicy: &policy}
	require.NoError(t, svc.preparePromptRulesDraft(context.Background(), &snapshot, nil), "the union of independent provider rules is valid")
	_, application, err := ApplyBusinessSystemPromptToJSON([]byte(`{"input":"question"}`), snapshot, BusinessSystemPromptTarget{Platform: "openai", ProviderPlatform: "openai", Protocol: "responses", AccountType: "apikey"})
	require.NoError(t, err)
	require.Len(t, application.RulesPlan.Placements, 32)
	policy.Rules[32].Platforms = []string{"openai"}
	snapshot.RulePolicy = &policy
	require.NoError(t, svc.preparePromptRulesDraft(context.Background(), &snapshot, nil))
	_, _, err = ApplyBusinessSystemPromptToJSON([]byte(`{"input":"question"}`), snapshot, BusinessSystemPromptTarget{Platform: "openai", ProviderPlatform: "openai", Protocol: "responses", AccountType: "apikey"})
	require.Error(t, err, "a single actual request still enforces 256 KiB")
}

func TestPromptConfigGeminiPreviewUsesURLModelMapping(t *testing.T) {
	snapshot := unifiedPromptSnapshot(t, PlatformGemini, []string{"auto"}, []string{"control_append"}, []string{"site instruction"})
	snapshot.RulePolicy.Rules[0].Models = []string{"gemini-2.5-pro"}
	snapshot.ResolvedRules[0].Rule = snapshot.RulePolicy.Rules[0]
	svc := NewBusinessSystemPromptService(nil, nil)
	svc.snapshot.Store(&snapshot)
	account := &Account{ID: 83, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"client-alias": "gemini-2.5-pro"}}}
	svc.accountRepo = &promptRulesPreviewAccountRepo{account: account}
	result, err := svc.PreviewPromptRules(context.Background(), account.ID, PromptRulesPreviewRequest{Protocol: "gemini", Body: json.RawMessage(`{"model":"client-alias","contents":[{"role":"user","parts":[{"text":"question"}]}]}`)})
	require.NoError(t, err)
	require.Equal(t, "client-alias", result.RequestedModel)
	require.Equal(t, "gemini-2.5-pro", result.UpstreamModel)
	require.True(t, result.Application.Applied)
	require.Equal(t, "site instruction", gjson.GetBytes(result.Body, "systemInstruction.parts.0.text").String())
}

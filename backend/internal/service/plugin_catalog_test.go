package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type catalogPointRepository struct {
	PluginRepository
	installation *PluginInstallation
	reads        []int64
}

func (r *catalogPointRepository) GetByID(_ context.Context, id int64) (*PluginInstallation, error) {
	r.reads = append(r.reads, id)
	if r.installation == nil || r.installation.ID != id {
		return nil, errors.New("catalog installation unavailable")
	}
	copy := *r.installation
	copy.Bindings = append([]PluginBinding(nil), r.installation.Bindings...)
	return &copy, nil
}

func catalogGuardTestManager(t *testing.T, invoke func(extensionv1.Invocation) (extensionv1.Result, error)) (*PluginManager, *catalogPointRepository) {
	t.Helper()
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, invoke)
	registry := manager.extensions.Load()
	installation := registry.installations[1]
	installation.State, installation.RuntimeGeneration = PluginStateEnabled, 1
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityCatalog, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}
	repo := &catalogPointRepository{installation: installation}
	manager.repo = repo
	registry.runtimes[1].installation = installation
	return manager, repo
}

func TestPluginCatalogCacheHonorsActivationHealthAndConfiguration(t *testing.T) {
	calls := 0
	capacity := int64(1050000)
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		calls++
		require.Equal(t, extensionv1.CapabilityCatalog, in.Capability)
		raw, _ := json.Marshal(extensionv1.CatalogMatch{Matched: true, Entry: &OfficialModelContextCapacity{ModelContextCapacity: ModelContextCapacity{ContextWindow: capacity}, ModelID: "test-model"}})
		return extensionv1.Result{Payload: raw}, nil
	})
	registry := manager.extensions.Load()
	registry.installations[1].State = PluginStateEnabled
	registry.installations[1].Bindings = []PluginBinding{{Capability: extensionv1.CapabilityCatalog, Platform: "*", AccountType: "*", Enabled: true}}
	manager.repo = &pluginTokenRepository{installation: registry.installations[1]}
	registry.runtimes[1].installation = registry.installations[1]
	query := extensionv1.CatalogQuery{Candidates: []string{"test-model"}, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey}
	first, err := manager.ResolveCatalog(context.Background(), query)
	require.NoError(t, err)
	first.Entry.ContextWindow = 1
	second, err := manager.ResolveCatalog(context.Background(), query)
	require.NoError(t, err)
	require.EqualValues(t, 1050000, second.Entry.ContextWindow)
	require.Equal(t, 1, calls)
	registry.runtimes[1].draining.Store(true)
	_, err = manager.ResolveCatalog(context.Background(), query)
	require.Error(t, err, "a cached reference cannot hide a failed plugin")
	registry.runtimes[1].draining.Store(false)
	registry.installations[1].Bindings[0].Enabled = false
	disabled, err := manager.ResolveCatalog(context.Background(), query)
	require.NoError(t, err)
	require.Nil(t, disabled.Entry)
	registry.installations[1].Bindings[0].Enabled = true
	registry.runtimes[1].configRevision.Add(1)
	capacity = 128000
	changed, err := manager.ResolveCatalog(context.Background(), query)
	require.NoError(t, err)
	require.EqualValues(t, 128000, changed.Entry.ContextWindow)
	require.Equal(t, 2, calls)
}

func TestPluginCatalogCacheRejectsConfiguringAndCanceledRequests(t *testing.T) {
	calls := 0
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		calls++
		raw, err := json.Marshal(extensionv1.CatalogMatch{Matched: true, Entry: &OfficialModelContextCapacity{ModelContextCapacity: ModelContextCapacity{ContextWindow: 200000}}})
		return extensionv1.Result{Payload: raw}, err
	})
	registry := manager.extensions.Load()
	installation := registry.installations[1]
	installation.State = PluginStateEnabled
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityCatalog, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}
	manager.repo = &pluginTokenRepository{installation: installation}
	registry.runtimes[1].installation = installation
	query := extensionv1.CatalogQuery{Candidates: []string{"test-model"}, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey}
	prime, err := manager.ResolveCatalog(context.Background(), query)
	require.NoError(t, err)
	require.NotNil(t, prime.Entry)
	t.Run("configuring", func(t *testing.T) {
		registry.runtimes[1].configuring.Store(true)
		t.Cleanup(func() { registry.runtimes[1].configuring.Store(false) })
		result, err := manager.ResolveCatalog(context.Background(), query)
		require.Error(t, err, "cached results must not bypass the configuration barrier")
		require.Nil(t, result.Entry)
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := manager.ResolveCatalog(ctx, query)
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, result.Entry)
	})
	require.Equal(t, 1, calls, "neither rejected request may reach the plugin")
}

func TestPluginCatalogCarriesAccountIDThroughRolloutAndCache(t *testing.T) {
	var calls []int64
	manager, _ := catalogGuardTestManager(t, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		var query extensionv1.CatalogQuery
		require.NoError(t, json.Unmarshal(in.Payload, &query))
		require.Equal(t, in.AccountID, query.AccountID, "the invocation gate and cache query must use the same host account")
		calls = append(calls, in.AccountID)
		raw, err := json.Marshal(extensionv1.CatalogMatch{Matched: true, Entry: &OfficialModelContextCapacity{ModelContextCapacity: ModelContextCapacity{ContextWindow: 200000 + in.AccountID}}})
		return extensionv1.Result{Payload: raw}, err
	})
	previous := processExtensionCatalog.Load()
	t.Cleanup(func() { processExtensionCatalog.Store(previous) })
	processExtensionCatalog.Store(&extensionCatalogProvider{resolver: manager})
	var included, excluded int64
	for id := int64(1); included == 0 || excluded == 0; id++ {
		if stablePluginBucket(id) < 50 {
			included = id
		} else {
			excluded = id
		}
	}
	account := &Account{ID: included, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com/v1"}}
	installation := manager.extensions.Load().installations[1]
	installation.Bindings[0].RolloutPercent = 0
	require.Nil(t, LookupOfficialModelContextCapacity(account, "test-model"))
	require.Empty(t, calls, "0%% rollout must not invoke a catalog for a real account")
	installation.Bindings[0].RolloutPercent = 50
	first := LookupOfficialModelContextCapacity(account, "test-model")
	require.NotNil(t, first)
	require.EqualValues(t, 200000+included, first.ContextWindow)
	account.ID = excluded
	require.Nil(t, LookupOfficialModelContextCapacity(account, "test-model"))
	require.Equal(t, []int64{included}, calls)
	installation.Bindings[0].RolloutPercent = 100
	second := LookupOfficialModelContextCapacity(account, "test-model")
	require.NotNil(t, second)
	require.EqualValues(t, 200000+excluded, second.ContextWindow, "another account cannot reuse the first account's cache")
	account.ID = included
	require.EqualValues(t, 200000+included, LookupOfficialModelContextCapacity(account, "test-model").ContextWindow)
	require.Equal(t, []int64{included, excluded}, calls, "the original account should reuse only its own cache")
}

func TestPluginCatalogCacheChecksPersistedScopeGenerationAndConfig(t *testing.T) {
	calls := 0
	manager, repo := catalogGuardTestManager(t, func(extensionv1.Invocation) (extensionv1.Result, error) {
		calls++
		raw, err := json.Marshal(extensionv1.CatalogMatch{Matched: true, Entry: &OfficialModelContextCapacity{ModelContextCapacity: ModelContextCapacity{ContextWindow: 200000}}})
		return extensionv1.Result{Payload: raw}, err
	})
	query := extensionv1.CatalogQuery{AccountID: 42, Candidates: []string{"test-model"}, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey}
	_, err := manager.ResolveCatalog(context.Background(), query)
	require.NoError(t, err)
	baseline := repo.installation
	for _, tc := range []struct {
		name   string
		change func(*PluginInstallation)
		err    bool
	}{
		{"disabled-binding", func(i *PluginInstallation) { i.Bindings[0].Enabled = false }, false},
		{"zero-rollout", func(i *PluginInstallation) { i.Bindings[0].RolloutPercent = 0 }, false},
		{"disabled-state", func(i *PluginInstallation) { i.State = PluginStateDisabled }, false},
		{"new-generation", func(i *PluginInstallation) { i.RuntimeGeneration++ }, true},
		{"new-persisted-config", func(i *PluginInstallation) { i.ConfigEncrypted = "changed-config" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated := *baseline
			updated.Bindings = append([]PluginBinding(nil), baseline.Bindings...)
			tc.change(&updated)
			repo.installation = &updated
			result, err := manager.ResolveCatalog(context.Background(), query)
			require.Equal(t, tc.err, err != nil)
			require.Nil(t, result.Entry, "stale local registry/cache must not hide persisted changes")
		})
	}
	require.Equal(t, 1, calls)
	require.Equal(t, []int64{1, 1, 1, 1, 1, 1}, repo.reads, "only the selected plugin is point-read, never the full installation table")
}

func TestPluginCatalogDiscardsReplyFromChangedConfigRevision(t *testing.T) {
	calls := 0
	var runtime *pluginRuntime
	manager, _ := catalogGuardTestManager(t, func(extensionv1.Invocation) (extensionv1.Result, error) {
		calls++
		if calls == 1 {
			runtime.configRevision.Add(1)
		}
		raw, err := json.Marshal(extensionv1.CatalogMatch{Matched: true, Entry: &OfficialModelContextCapacity{ModelContextCapacity: ModelContextCapacity{ContextWindow: 128000}}})
		return extensionv1.Result{Payload: raw}, err
	})
	runtime = manager.extensions.Load().runtimes[1]
	query := extensionv1.CatalogQuery{AccountID: 42, Candidates: []string{"test-model"}, Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey}
	result, err := manager.ResolveCatalog(context.Background(), query)
	require.Error(t, err)
	require.Nil(t, result.Entry)
	require.Zero(t, runtime.catalogCacheSize.Load(), "a reply spanning configuration revisions must not populate a cache")
	result, err = manager.ResolveCatalog(context.Background(), query)
	require.NoError(t, err)
	require.EqualValues(t, 128000, result.Entry.ContextWindow)
	_, err = manager.ResolveCatalog(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Zero(t, runtime.inFlight.Load(), "catalog policy leases must always be released")
}

func TestPluginCatalogNamespaceLimitMatchesHostCandidates(t *testing.T) {
	previous := processExtensionCatalog.Load()
	t.Cleanup(func() { processExtensionCatalog.Store(previous) })
	// The official catalog resolves in process; no plugin catalog is installed.
	processExtensionCatalog.Store(nil)
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com/v1"}}
	model := strings.Repeat("namespace", 35) + "/gpt-6-astra"
	require.Greater(t, len(model), 300)
	require.True(t, validModelContextID(model))
	require.Contains(t, modelContextReferenceCandidates(model), "gpt-6-astra")
	entry := LookupOfficialModelContextCapacity(account, model)
	require.NotNil(t, entry, "the original long namespace must not suppress the valid leaf candidate")
	require.Equal(t, "gpt-6-astra", entry.ModelID)
	model = strings.Repeat("n", 512-len("/gpt-6-astra")) + "/gpt-6-astra"
	require.Len(t, model, 512)
	require.NotNil(t, LookupOfficialModelContextCapacity(account, model))
	tooLong := "n" + model
	require.Len(t, tooLong, 513)
	require.False(t, validModelContextID(tooLong))
	require.Nil(t, LookupOfficialModelContextCapacity(account, tooLong))
	_, err := resolveOfficialModelCatalog(extensionv1.CatalogQuery{Candidates: modelContextReferenceCandidates(tooLong), Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey})
	require.Error(t, err, "the official catalog must independently reject a 513-byte candidate")
}

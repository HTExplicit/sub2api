package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

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
	registry.installations[1].Bindings = []PluginBinding{{Capability: extensionv1.CapabilityCatalog, Platform: "*", AccountType: "*", Enabled: true}}
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

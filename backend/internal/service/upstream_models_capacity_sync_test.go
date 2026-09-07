package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newCapacitySyncFixture(body string, accountID int64) (*AccountTestService, *upstreamModelMetadataRepoStub, *Account) {
	repo := &upstreamModelMetadataRepoStub{}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))},
		{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(`{"error":"unavailable"}`))},
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	account := &Account{ID: accountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://capacity.example/v1"}}
	return svc, repo, account
}

func TestSyncUpstreamModelCatalogCapacityOnlyPersistsWithoutReplacingCapabilitiesOrCustom(t *testing.T) {
	svc, repo, account := newCapacitySyncFixture(`{"data":[{"id":"unknown-model","context_window":700000,"max_context_window":900000}]}`, 81)
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "models.dev", Models: map[string]UpstreamModelMetadata{
		"unknown-model": {ID: "unknown-model", Reasoning: ptrCapacitySyncBool(true), SupportedReasoningLevels: []string{"high"}, InputModalities: []string{"text"}, ContextWindow: 300000},
	}})
	account.Extra[ModelContextOverridesExtraKey] = map[string]int64{"unknown-model": 1050000}
	beforeCapabilities := account.GetUpstreamModelMetadataSnapshot()

	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.NotContains(t, repo.updates, ModelContextOverridesExtraKey)
	require.NotContains(t, repo.updates, UpstreamModelMetadataExtraKey)
	require.Equal(t, beforeCapabilities, account.GetUpstreamModelMetadataSnapshot())
	require.Equal(t, map[string]int64{"unknown-model": 1050000}, account.Extra[ModelContextOverridesExtraKey])
	snapshot := account.GetUpstreamModelContextCapacitySnapshot()
	require.NotNil(t, snapshot)
	require.Equal(t, int64(700000), snapshot.Models["unknown-model"].ContextWindow)
	require.Equal(t, int64(900000), snapshot.Models["unknown-model"].MaxContextWindow)
	require.NotEmpty(t, snapshot.Models["unknown-model"].ObservedAt)
	require.Len(t, catalog.CapacityRows, 1)
}

func TestSyncUpstreamModelCatalogCapacityPreviewNeverPersists(t *testing.T) {
	svc, repo, account := newCapacitySyncFixture(`{"models":[{"id":"dynamic-model","context_window":512000},{"id":"id-only-model"}]}`, 0)
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Nil(t, repo.updates)
	require.Len(t, catalog.CapacityRows, 2)
	require.Equal(t, int64(512000), account.GetUpstreamModelContextCapacitySnapshot().Models["dynamic-model"].ContextWindow)
}

func TestSyncUpstreamModelCatalogCapacityPartialRefreshKeepsActualObservationTime(t *testing.T) {
	svc, repo, account := newCapacitySyncFixture(`{"data":[{"id":"still-listed"},{"id":"new-model","context_window":800000,"max_output_tokens":"invalid"}]}`, 82)
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{
		ObservedAt: "2026-01-01T00:00:00Z", Models: map[string]ModelContextCapacity{
			"still-listed": {ContextWindow: 400000}, "removed-model": {ContextWindow: 900000},
		},
	})
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"new-model", "still-listed"}, catalog.Models)
	require.Contains(t, repo.updates, UpstreamModelContextCapacitiesExtraKey)
	snapshot := account.GetUpstreamModelContextCapacitySnapshot()
	require.Equal(t, int64(400000), snapshot.Models["still-listed"].ContextWindow)
	require.Equal(t, "2026-01-01T00:00:00Z", snapshot.Models["still-listed"].ObservedAt)
	require.Equal(t, int64(800000), snapshot.Models["new-model"].ContextWindow)
	require.Zero(t, snapshot.Models["new-model"].MaxOutputTokens)
	require.NotContains(t, snapshot.Models, "removed-model")
	require.Equal(t, int64(800000), catalog.Metadata["new-model"].ContextWindow)
}

func TestSyncUpstreamModelCatalogCapacityOutputOnlyRefreshPreservesKnownContext(t *testing.T) {
	svc, _, account := newCapacitySyncFixture(`{"data":[{"id":"model","max_output_tokens":32000}]}`, 83)
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{
		ObservedAt: "2026-01-01T00:00:00Z", Models: map[string]ModelContextCapacity{
			"model": {ContextWindow: 512000, MaxContextWindow: 1000000, MaxOutputTokens: 16000},
		},
	})
	_, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	snapshot := account.GetUpstreamModelContextCapacitySnapshot()
	require.Equal(t, int64(512000), snapshot.Models["model"].ContextWindow)
	require.Equal(t, int64(1000000), snapshot.Models["model"].MaxContextWindow)
	require.Equal(t, int64(32000), snapshot.Models["model"].MaxOutputTokens)
	require.Equal(t, "2026-01-01T00:00:00Z", snapshot.Models["model"].ObservedAt)
}

func TestSyncUpstreamModelCatalogCapacitySourceChangeCannotInheritPreviousLimits(t *testing.T) {
	svc, repo, account := newCapacitySyncFixture(`{"data":[{"id":"model","max_output_tokens":32000}]}`, 84)
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{
		ObservedAt: "2026-01-01T00:00:00Z", Models: map[string]ModelContextCapacity{"model": {ContextWindow: 900000}},
	})
	account.Extra[ModelContextOverridesExtraKey] = map[string]int64{"model": 1050000}
	account.Credentials["base_url"] = "https://different-source.example/v1"
	_, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	snapshot := account.GetUpstreamModelContextCapacitySnapshot()
	require.NotNil(t, snapshot)
	require.Equal(t, ModelContextCapacitySourceIdentity(account), snapshot.SourceIdentity)
	require.Zero(t, snapshot.Models["model"].ContextWindow)
	require.Equal(t, int64(32000), snapshot.Models["model"].MaxOutputTokens)
	require.NotContains(t, repo.updates, ModelContextOverridesExtraKey)
	require.Equal(t, map[string]int64{"model": 1050000}, account.Extra[ModelContextOverridesExtraKey])
}

func TestExtractUpstreamModelContextCatalogMalformedOptionalCapacityKeepsOtherCapabilities(t *testing.T) {
	models, metadata, err := extractUpstreamModelCatalog([]byte(`{"data":[{"id":"model","context_window":"not-an-integer","max_context_window":700000,"reasoning":false,"input_modalities":["text"],"max_output_tokens":32000}]}`), false)
	require.NoError(t, err)
	require.Equal(t, []string{"model"}, models)
	require.Equal(t, int64(700000), metadata["model"].ContextWindow)
	require.Equal(t, int64(32000), metadata["model"].MaxOutputTokens)
	require.NotNil(t, metadata["model"].Reasoning)
	require.False(t, *metadata["model"].Reasoning)
	require.Equal(t, []string{"text"}, metadata["model"].InputModalities)
}

func ptrCapacitySyncBool(value bool) *bool { return &value }

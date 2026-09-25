package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestSyncUpstreamModelCatalogCapacityReplacesDeclarationKeepingCapabilitiesAndCustom(t *testing.T) {
	svc, repo, account := newCapacitySyncFixture(`{"data":[{"id":"unknown-model","context_window":700000,"max_context_window":900000}]}`, 81)
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "models.dev", Models: map[string]UpstreamModelMetadata{
		"unknown-model": {ID: "unknown-model", Reasoning: ptrCapacitySyncBool(true), SupportedReasoningLevels: []string{"high"}, InputModalities: []string{"text"}, ContextWindow: 300000},
	}})
	account.Extra[ModelContextOverridesExtraKey] = map[string]int64{"unknown-model": 1050000}

	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.NotContains(t, repo.updates, ModelContextOverridesExtraKey)
	require.Contains(t, repo.updates, UpstreamModelMetadataExtraKey)
	require.Equal(t, map[string]int64{"unknown-model": 1050000}, account.Extra[ModelContextOverridesExtraKey])
	entry, ok := account.GetUpstreamModelMetadata("unknown-model")
	require.True(t, ok)
	require.Equal(t, []string{"high"}, entry.SupportedReasoningLevels, "capability metadata survives a capacity refresh")
	require.Equal(t, int64(700000), entry.ContextWindow)
	require.Equal(t, int64(900000), entry.MaxContextWindow)
	require.Equal(t, ModelContextSourceUpstream, entry.CapacitySource)
	require.NotEmpty(t, entry.ObservedAt)
	require.Len(t, catalog.CapacityRows, 1)
}

func TestSyncUpstreamModelCatalogCapacityPreviewNeverPersists(t *testing.T) {
	svc, repo, account := newCapacitySyncFixture(`{"models":[{"id":"dynamic-model","context_window":512000},{"id":"id-only-model"}]}`, 0)
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Nil(t, repo.updates)
	require.Len(t, catalog.CapacityRows, 2)
	entry, ok := account.GetUpstreamModelMetadata("dynamic-model")
	require.True(t, ok)
	require.Equal(t, int64(512000), entry.ContextWindow)
}

func TestSyncUpstreamModelCatalogCapacityIsTheCurrentDeclaration(t *testing.T) {
	svc, repo, account := newCapacitySyncFixture(`{"data":[{"id":"still-listed"},{"id":"output-only","max_output_tokens":32000},{"id":"new-model","context_window":800000,"max_output_tokens":"invalid"}]}`, 82)
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", SyncedAt: "2026-01-01T00:00:00Z", Models: map[string]UpstreamModelMetadata{
		"still-listed":  {ID: "still-listed", ContextWindow: 400000, CapacitySource: ModelContextSourceUpstream, ObservedAt: "2026-01-01T00:00:00Z"},
		"output-only":   {ID: "output-only", ContextWindow: 512000, MaxContextWindow: 1000000, MaxOutputTokens: 16000, CapacitySource: ModelContextSourceUpstream},
		"removed-model": {ID: "removed-model", ContextWindow: 900000, CapacitySource: ModelContextSourceUpstream},
	}})
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"new-model", "output-only", "still-listed"}, catalog.Models)
	require.Contains(t, repo.updates, UpstreamModelMetadataExtraKey)
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	require.NotContains(t, snapshot.Models, "still-listed", "a value the upstream no longer declares is not kept")
	require.NotContains(t, snapshot.Models, "removed-model")
	require.Equal(t, int64(800000), snapshot.Models["new-model"].ContextWindow)
	require.Zero(t, snapshot.Models["new-model"].MaxOutputTokens)
	outputOnly := snapshot.Models["output-only"]
	require.Zero(t, outputOnly.ContextWindow, "an older window is never mixed into a newer declaration")
	require.Equal(t, int64(32000), outputOnly.MaxOutputTokens)
	require.NotEqual(t, "2026-01-01T00:00:00Z", snapshot.SyncedAt)
}

func TestSyncUpstreamModelCatalogFollowsAnthropicPagination(t *testing.T) {
	repo := &upstreamModelMetadataRepoStub{}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-a","max_input_tokens":200000,"max_tokens":64000}],"has_more":true,"last_id":"claude-a"}`))},
		{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-b","max_input_tokens":1000000}],"has_more":false,"last_id":"claude-b"}`))},
		{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(`{"error":"unavailable"}`))},
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	account := &Account{ID: 85, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://api.anthropic.com"}}
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-a", "claude-b"}, catalog.Models)
	require.GreaterOrEqual(t, len(upstream.requests), 2)
	require.Equal(t, "1000", upstream.requests[0].URL.Query().Get("limit"))
	require.Equal(t, "claude-a", upstream.requests[1].URL.Query().Get("after_id"))
	entry, ok := account.GetUpstreamModelMetadata("claude-b")
	require.True(t, ok)
	require.Equal(t, int64(1000000), entry.MaxInputTokens)
	first, _ := account.GetUpstreamModelMetadata("claude-a")
	require.Equal(t, int64(64000), first.MaxOutputTokens, "Anthropic max_tokens is the model output limit")
}

func TestExtractUpstreamModelContextCatalogMalformedOptionalCapacityKeepsOtherCapabilities(t *testing.T) {
	body := []byte(`{"data":[{"id":"model","context_window":"not-an-integer","max_context_window":700000,"reasoning":false,"input_modalities":["text"],"max_output_tokens":32000}]}`)
	models, metadata, err := extractUpstreamModelCatalog(body, false)
	require.NoError(t, err)
	require.Equal(t, []string{"model"}, models)
	applyUpstreamModelCapacityDeclarations(metadata, body, PlatformOpenAI, "2026-09-25T00:00:00Z")
	require.Zero(t, metadata["model"].ContextWindow)
	require.Equal(t, int64(700000), metadata["model"].MaxContextWindow)
	require.Equal(t, int64(32000), metadata["model"].MaxOutputTokens)
	require.Equal(t, ModelContextSourceUpstream, metadata["model"].CapacitySource)
	require.NotNil(t, metadata["model"].Reasoning)
	require.False(t, *metadata["model"].Reasoning)
	require.Equal(t, []string{"text"}, metadata["model"].InputModalities)
}

func TestUpstreamModelCatalogRefreshSelectsStaleActiveAPIKeyAccounts(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	refresh := NewUpstreamModelCatalogRefreshService(nil, nil)
	refresh.now = func() time.Time { return now }
	refresh.recordResult(5, errors.New("upstream unavailable"))
	syncedAt := func(at time.Time) map[string]any {
		return map[string]any{UpstreamModelMetadataExtraKey: map[string]any{"synced_at": at.Format(time.RFC3339), "models": map[string]any{}}}
	}
	accounts := []Account{
		{ID: 1, Status: StatusActive, Type: AccountTypeAPIKey, Extra: syncedAt(now.Add(-25 * time.Hour))},
		{ID: 2, Status: StatusActive, Type: AccountTypeAPIKey, Extra: syncedAt(now.Add(-time.Hour))},
		{ID: 3, Status: StatusActive, Type: AccountTypeOAuth},
		{ID: 4, Status: StatusDisabled, Type: AccountTypeAPIKey},
		{ID: 5, Status: StatusActive, Type: AccountTypeAPIKey},
		{ID: 6, Status: StatusActive, Type: AccountTypeAPIKey},
	}
	due := make([]int64, 0)
	for _, account := range refresh.dueAccounts(accounts) {
		due = append(due, account.ID)
	}
	require.Equal(t, []int64{6, 1}, due, "never synced first, then a day old; fresh, OAuth, inactive and backing-off accounts wait")
}

func TestRecordUpstreamModelCapacityObservationsStoresOAuthManifestDeclaration(t *testing.T) {
	repo := &upstreamModelMetadataRepoStub{}
	account := &Account{ID: 9101, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	upstreamCapacityObservationMarks.Delete(account.ID)
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "models.dev", Models: map[string]UpstreamModelMetadata{
		"gpt-5.5": {ID: "gpt-5.5", Reasoning: ptrCapacitySyncBool(true), ContextWindow: 400000},
	}})

	recordUpstreamModelCapacityObservations(context.Background(), repo, account,
		[]byte(`{"models":[{"slug":"gpt-5.5","context_window":272000,"max_context_window":272000}]}`))

	snapshot, ok := repo.updates[UpstreamModelMetadataExtraKey].(UpstreamModelMetadataSnapshot)
	require.True(t, ok)
	entry := snapshot.Models["gpt-5.5"]
	require.Equal(t, UpstreamModelMetadata{ID: "gpt-5.5", Reasoning: ptrCapacitySyncBool(true), ContextWindow: 272000, MaxContextWindow: 272000,
		CapacitySource: ModelContextSourceUpstream, ObservedAt: entry.ObservedAt}, entry)
}

func ptrCapacitySyncBool(value bool) *bool { return &value }

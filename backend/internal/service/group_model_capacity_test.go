package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type groupCapacityAccountRepo struct {
	AccountRepository
	accounts         []Account
	err              error
	capacityCalls    int
	schedulableCalls int
	queryGroupID     *int64
	includeGrouped   bool
}

// ListModelCapacityCandidates mirrors the SQL repository: every active account
// of the platforms, whatever its schedulable switch.
func (r *groupCapacityAccountRepo) ListModelCapacityCandidates(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error) {
	r.capacityCalls++
	r.queryGroupID = groupID
	r.includeGrouped = includeGrouped
	if r.err != nil {
		return nil, r.err
	}
	var result []Account
	for _, account := range r.accounts {
		if account.Status != StatusActive {
			continue
		}
		for _, platform := range platforms {
			if platform == account.Platform {
				result = append(result, account)
				break
			}
		}
	}
	return result, nil
}

func (r *groupCapacityAccountRepo) ListModelAvailabilityCandidates(_ context.Context, _ *int64, _ []string, _ bool) ([]Account, error) {
	return append([]Account(nil), r.accounts...), r.err
}

func (r *groupCapacityAccountRepo) ListSchedulableByGroupID(_ context.Context, _ int64) ([]Account, error) {
	r.schedulableCalls++
	return append([]Account(nil), r.accounts...), nil
}

type groupCapacityRouteRepo struct {
	CompositeModelRouteRepository
	routes []CompositeModelRoute
	err    error
	calls  int
}

func (r *groupCapacityRouteRepo) ListByGroup(_ context.Context, _ int64, _ bool) ([]CompositeModelRoute, error) {
	r.calls++
	return r.routes, r.err
}

func newGroupCapacityAccount(id int64, mapping map[string]any, capacities map[string]int64) Account {
	return Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"base_url": "https://capacity.example/v1", "model_mapping": mapping},
		Extra:       map[string]any{ModelContextOverridesExtraKey: capacities},
	}
}

func observedCapacityExtra(models map[string]UpstreamModelMetadata) UpstreamModelMetadataSnapshot {
	for id, entry := range models {
		entry.ID, entry.CapacitySource = id, ModelContextSourceUpstream
		models[id] = entry
	}
	return UpstreamModelMetadataSnapshot{Source: "upstream", SyncedAt: "2026-09-25T00:00:00Z", Models: models}
}

func TestApplyModelContextCapacityPreservesUnknownFields(t *testing.T) {
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(`{"slug":"alias","sentinel":{"must":"stay"},"supports_search_tool":true,"context_window":272000,"max_context_window":1050000,"max_output_tokens":64000,"auto_compact_token_limit":900000}`), &fields))
	sentinel := append(json.RawMessage(nil), fields["sentinel"]...)
	capacity := ResolvedModelContextCapacity{ModelContextCapacity: ModelContextCapacity{ContextWindow: 600000, MaxContextWindow: 750000, CapacityBasis: "total_context"}, Source: "custom"}
	require.True(t, ApplyModelContextCapacityToFields(fields, capacity, true))
	require.Equal(t, "600000", string(fields["context_window"]))
	require.Equal(t, "750000", string(fields["max_context_window"]))
	require.Equal(t, "null", string(fields["auto_compact_token_limit"]))
	require.NotContains(t, fields, "max_output_tokens")
	require.Equal(t, sentinel, fields["sentinel"])
	require.Equal(t, "true", string(fields["supports_search_tool"]))
	require.False(t, ApplyModelContextCapacityToFields(fields, capacity, true), "projection must be idempotent")
	before, _ := json.Marshal(fields)
	require.False(t, ApplyModelContextCapacityToFields(fields, unknownModelContextCapacity("no_capacity_evidence"), true))
	after, _ := json.Marshal(fields)
	require.Equal(t, before, after, "an unknown capacity never rewrites or invents fields")
	fields["auto_compact_token_limit"] = json.RawMessage("580000")
	ApplyModelContextCapacityToFields(fields, capacity, true)
	require.Equal(t, "580000", string(fields["auto_compact_token_limit"]), "valid upstream threshold remains intact")
}

func TestGroupModelCapacityIncludesWildcardUnscheduledAndTemporarilyBlockedCandidates(t *testing.T) {
	first := newGroupCapacityAccount(1, map[string]any{"alias": "large-model"}, map[string]int64{"large-model": 1000000})
	second := newGroupCapacityAccount(2, map[string]any{"a*": "small-model"}, map[string]int64{"small-model": 600000})
	future := time.Now().Add(time.Hour)
	second.RateLimitResetAt = &future
	unknown := newGroupCapacityAccount(3, nil, nil)
	disabled := newGroupCapacityAccount(4, nil, map[string]int64{"alias": 1000})
	disabled.Status = "disabled"
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	repo := &groupCapacityAccountRepo{accounts: []Account{first, second, unknown, disabled}}
	catalog := loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, nil, &group.ID, group.Platform)
	capacity := catalog.resolve(context.Background(), group.Platform, "alias")
	require.Equal(t, int64(600000), capacity.ContextWindow, "temporary rate limits must not widen the catalog; an unknown peer does not lower it")
	require.Equal(t, "custom", capacity.Source)
	require.Equal(t, "group_minimum", capacity.Reason)
	require.Equal(t, 1, repo.capacityCalls)
	require.Zero(t, repo.schedulableCalls)
	paused := newGroupCapacityAccount(5, map[string]any{"alias": "paused-model"}, map[string]int64{"paused-model": 300000})
	paused.Schedulable = false
	repo.accounts = append(repo.accounts, paused)
	catalog = loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, nil, &group.ID, group.Platform)
	require.Equal(t, int64(300000), catalog.resolve(context.Background(), group.Platform, "alias").ContextWindow, "the schedulable switch does not remove an active account's capacity")
}

func TestGroupModelCapacityUsesChannelAndActualAccountTargets(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	account := newGroupCapacityAccount(1, map[string]any{"channel-model": "actual-model"}, map[string]int64{"actual-model": 700000, "alias": 1000})
	channels := &ChannelService{}
	channels.cache.Store(populateChannelCache([]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{group.ID}, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"alias": "channel-model"}}}}, map[int64]string{group.ID: group.Platform}))
	catalog := newGroupModelCapacityCatalog([]Account{account}, true, nil, true, &group.ID, channels)
	capacity := catalog.resolve(context.Background(), group.Platform, "alias")
	require.Equal(t, int64(700000), capacity.ContextWindow)
	require.Equal(t, "custom", capacity.Source)
}

func TestGroupModelCapacityCompositeAmbiguityAndBatchQueries(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformComposite}
	account := newGroupCapacityAccount(1, map[string]any{"alias": "native-model", "other": "native-model"}, map[string]int64{"native-model": 700000})
	repo := &groupCapacityAccountRepo{accounts: []Account{account}}
	routes := &groupCapacityRouteRepo{routes: []CompositeModelRoute{
		{ID: 1, PublicModel: "alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "alias", Endpoint: CompositeRouteEndpointAny, Enabled: true},
		{ID: 2, PublicModel: "alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "other", Endpoint: CompositeRouteEndpointResponses, Enabled: true},
	}}
	svc := &GatewayService{accountRepo: repo, compositeResolver: NewCompositeRouteResolver(routes)}
	body, err := svc.ProjectModelListContextCapacities(context.Background(), group, &group.ID, group.Platform, []byte(`{"object":"list","data":[{"id":"alias","sentinel":42},{"id":"other"}]}`))
	require.NoError(t, err)
	var decoded struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.NotContains(t, decoded.Data[0], "context_window", "an ambiguous route has no advertised capacity")
	require.Equal(t, float64(42), decoded.Data[0]["sentinel"])
	require.Equal(t, "alias", decoded.Data[0]["id"])
	require.Equal(t, "other", decoded.Data[1]["id"])
	require.Equal(t, float64(700000), decoded.Data[1]["context_window"])
	require.Equal(t, 1, repo.capacityCalls)
	require.Equal(t, 1, routes.calls)
	require.Zero(t, repo.schedulableCalls)
}

func TestGroupModelCapacityOAuthParticipatesAndQueryFailure(t *testing.T) {
	ordinary := newGroupCapacityAccount(1, map[string]any{"alias": "native-model"}, map[string]int64{"native-model": 700000})
	oauth := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"alias": "gpt-6-sol"}},
		Extra: map[string]any{UpstreamModelMetadataExtraKey: observedCapacityExtra(map[string]UpstreamModelMetadata{
			"gpt-6-sol": {ContextWindow: 272000, MaxContextWindow: 872000},
		})}}
	catalog := newGroupModelCapacityCatalog([]Account{ordinary, oauth}, true, nil, true, nil, nil)
	capacity := catalog.resolve(context.Background(), PlatformOpenAI, "alias")
	require.Equal(t, int64(272000), capacity.ContextWindow, "an OAuth candidate bounds the group with its observed Codex window")
	require.Equal(t, int64(700000), capacity.MaxContextWindow)
	require.Equal(t, "upstream", capacity.Source)
	require.Equal(t, "group_minimum", capacity.Reason)
	repo := &groupCapacityAccountRepo{err: errors.New("database unavailable")}
	catalog = loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, nil, nil, PlatformOpenAI)
	capacity = catalog.resolve(context.Background(), PlatformOpenAI, "gpt-6-astra")
	require.False(t, capacity.Known(), "never guess capacity from the public slug")
	require.Equal(t, "account_query_failed", capacity.Reason)
}

func TestGroupModelCapacityAggregatesDistinctMaxInputOutputConservatively(t *testing.T) {
	first := newGroupCapacityAccount(1, nil, nil)
	second := newGroupCapacityAccount(2, nil, nil)
	first.Extra[UpstreamModelMetadataExtraKey] = observedCapacityExtra(map[string]UpstreamModelMetadata{"unlisted": {ContextWindow: 400000, MaxContextWindow: 900000, MaxInputTokens: 390000, MaxOutputTokens: 64000}})
	second.Extra[UpstreamModelMetadataExtraKey] = observedCapacityExtra(map[string]UpstreamModelMetadata{"unlisted": {ContextWindow: 500000, MaxContextWindow: 700000, MaxOutputTokens: 32000}})
	catalog := newGroupModelCapacityCatalog([]Account{first, second}, true, nil, true, nil, nil)
	capacity := catalog.resolve(context.Background(), PlatformOpenAI, "unlisted")
	require.Equal(t, int64(400000), capacity.ContextWindow)
	require.Equal(t, int64(700000), capacity.MaxContextWindow)
	require.Equal(t, int64(390000), capacity.MaxInputTokens, "a peer without an input limit is bounded by its larger window")
	require.Equal(t, int64(32000), capacity.MaxOutputTokens)
}

func TestCodexCapacityProjectionRefreshesETagWithoutChangingRawCache(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	account := newGroupCapacityAccount(1, nil, map[string]int64{"unlisted": 600000})
	raw := []byte(`{"models":[{"slug":"unlisted","context_window":410000,"max_context_window":800000,"auto_compact_token_limit":750000,"sentinel":{"ok":true}}]}`)
	repo := &groupCapacityAccountRepo{accounts: []Account{account}}
	svc := &OpenAIGatewayService{accountRepo: repo}
	manifest := &OpenAIModelsResponse{Body: append([]byte(nil), raw...), upstreamSourceBody: append([]byte(nil), raw...), ETag: `"upstream"`}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, ""))
	firstETag := manifest.ETag
	model := decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, float64(600000), model["context_window"])
	require.Nil(t, model["auto_compact_token_limit"])
	require.Equal(t, map[string]any{"ok": true}, model["sentinel"])
	require.Equal(t, raw, manifest.upstreamSourceBody)
	repo.accounts[0].Extra = map[string]any{ModelContextOverridesExtraKey: map[string]int64{"unlisted": 700000}}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, firstETag))
	require.False(t, manifest.NotModified)
	require.NotEqual(t, firstETag, manifest.ETag)
	model = decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, float64(700000), model["context_window"])
	latestETag := manifest.ETag
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, latestETag))
	require.True(t, manifest.NotModified)
	require.Equal(t, raw, manifest.upstreamSourceBody)
}

func TestCodexCapacityProjectionCompositeUsesSharedRouteRepository(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformComposite}
	account := newGroupCapacityAccount(1, nil, map[string]int64{"native-model": 625001})
	repo := &groupCapacityAccountRepo{accounts: []Account{account}}
	routes := &groupCapacityRouteRepo{routes: []CompositeModelRoute{{ID: 1, PublicModel: "alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "native-model", Endpoint: CompositeRouteEndpointAny, Enabled: true}}}
	svc := &GatewayService{accountRepo: repo, compositeResolver: NewCompositeRouteResolver(routes)}
	manifest := &OpenAIModelsResponse{Body: []byte(`{"models":[{"slug":"alias","context_window":900000,"sentinel":"unchanged"}]}`)}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, ""))
	model := decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, "alias", model["slug"])
	require.Equal(t, "unchanged", model["sentinel"])
	require.Equal(t, float64(625001), model["context_window"])
	require.Equal(t, "custom", model["context_capacity_source"])
	require.Equal(t, 1, repo.capacityCalls)
	require.Equal(t, 1, routes.calls)
}

func TestMiniMaxCompositeCapacityIncludesFallbackAndKeepsExplicitRoutePrecedence(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformComposite}
	first := newGroupCapacityAccount(1, map[string]any{"routed-alias": "native-model"}, map[string]int64{"native-model": 625001})
	first.Platform = PlatformMiniMax
	fallback := newGroupCapacityAccount(2, map[string]any{"routed-alias": "fallback-model"}, map[string]int64{"fallback-model": 350001})
	fallback.Platform = PlatformMiniMax
	future := time.Now().Add(time.Hour)
	fallback.RateLimitResetAt = &future
	other := newGroupCapacityAccount(3, map[string]any{"gpt-6-astra": "other-model"}, map[string]int64{"other-model": 1050000})
	repo := &groupCapacityAccountRepo{accounts: []Account{first, fallback, other}}
	routes := &groupCapacityRouteRepo{routes: []CompositeModelRoute{{ID: 1, PublicModel: "gpt-6-astra", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformMiniMax, UpstreamModel: "routed-alias", Endpoint: CompositeRouteEndpointAny, Enabled: true}}}
	svc := &GatewayService{accountRepo: repo, compositeResolver: NewCompositeRouteResolver(routes)}
	raw := []byte(`{"models":[{"slug":"gpt-6-astra","context_window":900000,"sentinel":"unchanged"}]}`)
	manifest := &OpenAIModelsResponse{Body: append([]byte(nil), raw...)}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, ""))
	model := decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, "gpt-6-astra", model["slug"], "explicit MiniMax route must beat model-name and account-ownership inference")
	require.Equal(t, "unchanged", model["sentinel"])
	require.Equal(t, float64(350001), model["context_window"], "temporarily blocked MiniMax fallback still narrows the actual candidate pool")
	require.Equal(t, "custom", model["context_capacity_source"])
	require.Equal(t, "group_minimum", model["context_capacity_reason"])
	require.Equal(t, codexModelsManifestBodyETag(manifest.Body), manifest.ETag)
	require.Equal(t, 1, repo.capacityCalls)
	require.Equal(t, 1, routes.calls)
	require.Zero(t, repo.schedulableCalls)

	previousETag := manifest.ETag
	repo.accounts[1].Extra[ModelContextOverridesExtraKey] = map[string]int64{"fallback-model": 300001}
	manifest = &OpenAIModelsResponse{Body: append([]byte(nil), raw...)}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, previousETag))
	require.False(t, manifest.NotModified)
	require.NotEqual(t, previousETag, manifest.ETag)
	require.Equal(t, float64(300001), decodeCodexManifestModels(t, manifest.Body)[0]["context_window"])
	require.Contains(t, string(raw), `"context_window":900000`, "capacity projection must leave the upstream source unchanged")
}

func TestPinnedModelCapacityUsesTheWholeForwardingPool(t *testing.T) {
	for _, codex := range []bool{false, true} {
		t.Run(fmt.Sprintf("codex=%t", codex), func(t *testing.T) {
			first := newGroupCapacityAccount(1, map[string]any{"public-alias": "native-a"}, map[string]int64{"native-a": 600000})
			second := newGroupCapacityAccount(2, map[string]any{"public-alias": "native-b"}, nil)
			fallback := newGroupCapacityAccount(3, map[string]any{"public-alias": "native-c"}, map[string]int64{"native-c": 300000})
			group := &Group{ID: 9, Platform: PlatformOpenAI,
				CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{first.ID, second.ID}}}
			field, idField := "data", "id"
			if codex {
				field, idField = "models", "slug"
			}
			raw := []byte(fmt.Sprintf(`{"%s":[{"%s":"public-alias","context_window":900000,"sentinel":"first"}]}`, field, idField))
			repo := &groupCapacityAccountRepo{accounts: []Account{first, second}}
			svc := &OpenAIGatewayService{accountRepo: repo}
			project := func() map[string]any {
				response := &OpenAIModelsResponse{Body: append([]byte(nil), raw...)}
				if codex {
					require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, response, ""))
				} else {
					require.NoError(t, svc.ProjectOpenAIModelsListContextCapacities(context.Background(), group, response, ""))
				}
				var envelope map[string][]map[string]any
				require.NoError(t, json.Unmarshal(response.Body, &envelope))
				return envelope[field][0]
			}
			row := project()
			require.Equal(t, float64(600000), row["context_window"], "a peer without evidence does not lower the known window")
			require.Equal(t, "first", row["sentinel"], "non-capacity fields keep the discovered entry")
			repo.accounts = append(repo.accounts, fallback)
			row = project()
			require.Equal(t, float64(300000), row["context_window"], "discovery accounts are not an exclusive forwarding pool")
			require.Equal(t, "custom", row["context_capacity_source"])
		})
	}
}

func TestGroupCapacitySimpleModeKeepsActualOpenAIFullPool(t *testing.T) {
	groupID := int64(9)
	for _, simple := range []bool{false, true} {
		t.Run(fmt.Sprintf("simple=%t", simple), func(t *testing.T) {
			repo := &groupCapacityAccountRepo{}
			cfg := &config.Config{}
			if simple {
				cfg.RunMode = config.RunModeSimple
			}
			loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, cfg, &groupID, PlatformOpenAI)
			require.Equal(t, simple, repo.includeGrouped)
			if simple {
				require.Nil(t, repo.queryGroupID, "simple-mode forwarding still uses the full OpenAI platform pool")
			} else {
				require.Equal(t, &groupID, repo.queryGroupID)
			}
		})
	}
}

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
	accounts          []Account
	err               error
	availabilityCalls int
	schedulableCalls  int
	queryGroupID      *int64
	includeGrouped    bool
}

func (r *groupCapacityAccountRepo) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error) {
	r.availabilityCalls++
	r.queryGroupID = groupID
	r.includeGrouped = includeGrouped
	if r.err != nil {
		return nil, r.err
	}
	var result []Account
	for _, account := range r.accounts {
		if account.Status != StatusActive || !account.Schedulable {
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

func TestApplyModelContextCapacityPreservesUnknownAndProtectedFields(t *testing.T) {
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
	require.False(t, ApplyModelContextCapacityToFields(fields, ResolvedModelContextCapacity{Source: "protected"}, true))
	after, _ := json.Marshal(fields)
	require.Equal(t, before, after)
	fields["auto_compact_token_limit"] = json.RawMessage("580000")
	ApplyModelContextCapacityToFields(fields, capacity, true)
	require.Equal(t, "580000", string(fields["auto_compact_token_limit"]), "valid upstream threshold remains intact")
}

func TestGroupModelCapacityIncludesWildcardUnrestrictedAndTemporarilyBlockedCandidates(t *testing.T) {
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
	require.Equal(t, DefaultModelContextWindow, capacity.ContextWindow, "unrestricted unknown peer participates despite the exact claimant")
	require.Equal(t, "default", capacity.Source)
	require.Equal(t, 1, repo.availabilityCalls)
	require.Zero(t, repo.schedulableCalls)
	repo.accounts = []Account{first, second, disabled}
	catalog = loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, nil, &group.ID, group.Platform)
	require.Equal(t, int64(600000), catalog.resolve(context.Background(), group.Platform, "alias").ContextWindow, "temporary rate limits must not widen the catalog")
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
	account.Extra["openai_passthrough"] = true
	catalog = newGroupModelCapacityCatalog([]Account{account}, true, nil, true, &group.ID, channels)
	capacity = catalog.resolve(context.Background(), group.Platform, "alias")
	require.Equal(t, DefaultModelContextWindow, capacity.ContextWindow, "Responses passthrough and Chat account mapping differ")
	require.Equal(t, "ambiguous_endpoint_targets", capacity.Reason)
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
	require.Equal(t, float64(DefaultModelContextWindow), decoded.Data[0]["context_window"])
	require.Equal(t, "ambiguous_endpoint_routes", decoded.Data[0]["context_capacity_reason"])
	require.Equal(t, float64(42), decoded.Data[0]["sentinel"])
	require.Equal(t, "alias", decoded.Data[0]["id"])
	require.Equal(t, "other", decoded.Data[1]["id"])
	require.Equal(t, 1, repo.availabilityCalls)
	require.Equal(t, 1, routes.calls)
	require.Zero(t, repo.schedulableCalls)
}

func TestGroupModelCapacityProtectedCandidatesAndQueryFailure(t *testing.T) {
	ordinary := newGroupCapacityAccount(1, map[string]any{"alias": "native-model"}, map[string]int64{"native-model": 700000})
	oauth := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	catalog := newGroupModelCapacityCatalog([]Account{ordinary, oauth}, true, nil, true, nil, nil)
	require.Equal(t, "protected", catalog.resolve(context.Background(), PlatformOpenAI, "alias").Source)
	cindy := newGroupCapacityAccount(3, nil, nil)
	cindy.Credentials["base_url"] = "https://api.laxarouter.ai"
	catalog = newGroupModelCapacityCatalog([]Account{ordinary, cindy}, true, nil, true, nil, nil)
	require.Equal(t, "protected", catalog.resolve(context.Background(), PlatformOpenAI, "gpt-5.6-sol").Source)
	repo := &groupCapacityAccountRepo{err: errors.New("database unavailable")}
	catalog = loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, nil, nil, PlatformOpenAI)
	capacity := catalog.resolve(context.Background(), PlatformOpenAI, "gpt-6-astra")
	require.Equal(t, DefaultModelContextWindow, capacity.ContextWindow)
	require.Equal(t, "account_query_failed", capacity.Reason, "never guess official capacity from the public slug")
}

func TestGroupModelCapacityAggregatesDistinctMaxInputOutputConservatively(t *testing.T) {
	first := newGroupCapacityAccount(1, nil, nil)
	second := newGroupCapacityAccount(2, nil, nil)
	first.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{Models: map[string]ModelContextCapacity{"unlisted": {ContextWindow: 400000, MaxContextWindow: 900000, MaxInputTokens: 390000, MaxOutputTokens: 64000}}})
	second.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{Models: map[string]ModelContextCapacity{"unlisted": {ContextWindow: 500000, MaxContextWindow: 700000, MaxOutputTokens: 32000}}})
	catalog := newGroupModelCapacityCatalog([]Account{first, second}, true, nil, true, nil, nil)
	capacity := catalog.resolve(context.Background(), PlatformOpenAI, "unlisted")
	require.Equal(t, int64(400000), capacity.ContextWindow)
	require.Equal(t, int64(700000), capacity.MaxContextWindow)
	require.Zero(t, capacity.MaxInputTokens, "unknown peer input limit is not inferred")
	require.Equal(t, int64(32000), capacity.MaxOutputTokens)
}

func TestConvertOpenAIModelListLiveCapacitySurvivesSparseConversion(t *testing.T) {
	source := []byte(`{"data":[{"id":"unlisted-model","context_window":410001,"max_context_window":710003,"max_output_tokens":12345,"supports_search_tool":true}]}`)
	account := newGroupCapacityAccount(1, nil, nil)
	converted := convertOpenAIModelListToCodexManifestForAccount(source, &account)
	model := decodeCodexManifestModels(t, converted)[0]
	require.Equal(t, float64(410001), model["context_window"])
	require.Equal(t, float64(710003), model["max_context_window"])
	require.Equal(t, float64(12345), model["max_output_tokens"])
	require.Equal(t, true, model["supports_search_tool"])
	require.Nil(t, account.GetUpstreamModelContextCapacitySnapshot(), "per-request conversion does not persist observations")
}

func TestCodexCapacityProjectionRefreshesETagWithoutChangingRawCache(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	account := newGroupCapacityAccount(1, nil, map[string]int64{"unlisted": 600000})
	raw := []byte(`{"models":[{"slug":"unlisted","context_window":410000,"max_context_window":800000,"auto_compact_token_limit":750000,"sentinel":{"ok":true}}]}`)
	repo := &groupCapacityAccountRepo{accounts: []Account{account}}
	svc := &OpenAIGatewayService{accountRepo: repo}
	manifest := &OpenAIModelsResponse{Body: append([]byte(nil), raw...), upstreamSourceBody: append([]byte(nil), raw...), ETag: `"upstream"`}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, "", &account))
	firstETag := manifest.ETag
	model := decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, float64(600000), model["context_window"])
	require.Nil(t, model["auto_compact_token_limit"])
	require.Equal(t, map[string]any{"ok": true}, model["sentinel"])
	require.Equal(t, raw, manifest.upstreamSourceBody)
	repo.accounts[0].Extra = map[string]any{ModelContextOverridesExtraKey: map[string]int64{"unlisted": 700000}}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, firstETag, &account))
	require.False(t, manifest.NotModified)
	require.NotEqual(t, firstETag, manifest.ETag)
	model = decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, float64(700000), model["context_window"])
	latestETag := manifest.ETag
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, latestETag, &account))
	require.True(t, manifest.NotModified)
	require.Equal(t, raw, manifest.upstreamSourceBody)
}

func TestCodexCapacityProjectionLiveWinsOverSnapshotButNotOverride(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	account := newGroupCapacityAccount(1, nil, nil)
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{Models: map[string]ModelContextCapacity{"unlisted": {ContextWindow: 300000}}})
	raw := []byte(`{"models":[{"slug":"unlisted","context_window":410000,"max_context_window":800000,"auto_compact_token_limit":400000}]}`)
	svc := &OpenAIGatewayService{accountRepo: &groupCapacityAccountRepo{accounts: []Account{account}}}
	manifest := &OpenAIModelsResponse{Body: append([]byte(nil), raw...), upstreamSourceBody: append([]byte(nil), raw...)}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, "", &account))
	model := decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, float64(410000), model["context_window"])
	require.Equal(t, float64(800000), model["max_context_window"])
	require.Equal(t, "upstream", model["context_capacity_source"])
	require.Equal(t, float64(400000), model["auto_compact_token_limit"])
	require.Equal(t, int64(300000), account.GetUpstreamModelContextCapacitySnapshot().Models["unlisted"].ContextWindow)
}

func TestCodexCapacityProjectionRejectsLiveFromChangedAccountSource(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	source := newGroupCapacityAccount(1, nil, nil)
	current := newGroupCapacityAccount(1, nil, nil)
	current.Credentials["base_url"] = "https://different-capacity.example/v1"
	current.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{Models: map[string]ModelContextCapacity{"unlisted": {ContextWindow: 300000}}})
	raw := []byte(`{"models":[{"slug":"unlisted","context_window":900000,"max_context_window":1000000}]}`)
	svc := &OpenAIGatewayService{accountRepo: &groupCapacityAccountRepo{accounts: []Account{current}}}
	manifest := &OpenAIModelsResponse{Body: append([]byte(nil), raw...), upstreamSourceBody: append([]byte(nil), raw...)}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, "", &source))
	model := decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, float64(300000), model["context_window"], "same account ID cannot authenticate observations from an old endpoint")
	require.Equal(t, "upstream", model["context_capacity_source"])
	require.Equal(t, raw, manifest.upstreamSourceBody)
}

func TestCodexCapacityProjectionPinnedSourcesPreserveProtectedAndMergeOrder(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	first := newGroupCapacityAccount(1, map[string]any{"model-a": "model-a", "shared": "shared"}, nil)
	second := newGroupCapacityAccount(2, map[string]any{"model-b": "model-b", "shared": "shared"}, nil)
	oauth := Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"protected-model": "protected-model"}}}
	rawFirst := []byte(`{"models":[{"slug":"model-a","context_window":410000,"max_context_window":610000},{"slug":"shared","context_window":900000,"display_name":"first"}]}`)
	rawSecond := []byte(`{"models":[{"slug":"shared","context_window":800000,"display_name":"second"},{"slug":"model-b","context_window":520000}]}`)
	rawProtected := []byte(`{"models":[{"slug":"shared","context_window":700001},{"slug":"protected-model","context_window":123456,"max_context_window":234567,"auto_compact_token_limit":999999,"sentinel":"preserve"}]}`)
	merged, err := mergeCodexModelsManifestBodies([][]byte{rawFirst, rawSecond, rawProtected})
	require.NoError(t, err)
	manifest := &OpenAIModelsResponse{Body: merged, capacitySources: []codexModelCapacitySource{
		newCodexModelCapacitySource(&first, rawFirst), newCodexModelCapacitySource(&second, rawSecond), newCodexModelCapacitySource(&oauth, rawProtected),
	}}
	manifest.capacitySources[0].visibleModels = map[string]bool{"model-a": true, "shared": true}
	manifest.capacitySources[1].visibleModels = map[string]bool{"model-b": true}
	manifest.capacitySources[2].visibleModels = map[string]bool{"protected-model": true}
	svc := &OpenAIGatewayService{accountRepo: &groupCapacityAccountRepo{accounts: []Account{first, second, oauth}}}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, "", &first))
	models := decodeCodexManifestModels(t, manifest.Body)
	require.Equal(t, []any{"model-a", "shared", "model-b", "protected-model"}, []any{models[0]["slug"], models[1]["slug"], models[2]["slug"], models[3]["slug"]})
	require.Equal(t, float64(410000), models[0]["context_window"])
	require.Equal(t, float64(800000), models[1]["context_window"], "both pinned ordinary raw observations participate")
	require.Equal(t, "upstream", models[1]["context_capacity_source"], "a losing, non-candidate OAuth duplicate does not reclassify the winning ordinary row")
	require.Equal(t, "first", models[1]["display_name"], "first-seen non-capacity fields remain unchanged")
	require.Equal(t, float64(520000), models[2]["context_window"])
	require.Equal(t, float64(123456), models[3]["context_window"])
	require.Equal(t, float64(999999), models[3]["auto_compact_token_limit"])
	require.NotContains(t, models[3], "context_capacity_source")
	require.Equal(t, "preserve", models[3]["sentinel"])
	// A source's identity and protected status are captured values, not a
	// pointer back to an account whose profile could be changed later.
	oauth.Type = AccountTypeAPIKey
	oauth.Credentials["base_url"] = "https://changed.example/v1"
	require.True(t, manifest.capacitySources[2].protected)
	require.NotEqual(t, ModelContextCapacitySourceIdentity(&oauth), manifest.capacitySources[2].identity)
}

func TestCodexCapacityProjectionProvenanceCloneAndCacheBudget(t *testing.T) {
	account := newGroupCapacityAccount(1, nil, nil)
	raw := []byte(`{"models":[{"slug":"unlisted","context_window":410000}]}`)
	manifest := &OpenAIModelsResponse{Body: append([]byte(nil), raw...), upstreamSourceBody: append([]byte(nil), raw...),
		capacityProtectedModels: map[string]bool{"protected": true}, capacitySources: []codexModelCapacitySource{newCodexModelCapacitySource(&account, raw)}}
	manifest.capacitySources[0].visibleModels = map[string]bool{"protected": true}
	cloned := cloneOpenAIModelsResponse(manifest)
	cloned.Body[0], cloned.upstreamSourceBody[0], cloned.capacitySources[0].body[0] = 'x', 'y', 'z'
	delete(cloned.capacityProtectedModels, "protected")
	delete(cloned.capacitySources[0].visibleModels, "protected")
	require.Equal(t, byte('{'), manifest.Body[0])
	require.Equal(t, byte('{'), manifest.upstreamSourceBody[0])
	require.Equal(t, byte('{'), manifest.capacitySources[0].body[0])
	require.True(t, manifest.capacityProtectedModels["protected"])
	require.True(t, manifest.capacitySources[0].visibleModels["protected"])
	cache := openAIModelsCache{}
	manifest.capacitySources[0].body = make([]byte, openAIModelsCacheBodyLimit)
	cache.set("over-budget", manifest, time.Now())
	require.Empty(t, cache.entries, "raw provenance is included in the existing body memory budget")
}

func TestCodexCapacityProjectionCompositeUsesSharedRouteRepository(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformComposite}
	account := newGroupCapacityAccount(1, nil, map[string]int64{"native-model": 625001})
	repo := &groupCapacityAccountRepo{accounts: []Account{account}}
	routes := &groupCapacityRouteRepo{routes: []CompositeModelRoute{{ID: 1, PublicModel: "alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "native-model", Endpoint: CompositeRouteEndpointAny, Enabled: true}}}
	svc := &GatewayService{accountRepo: repo, compositeResolver: NewCompositeRouteResolver(routes)}
	manifest := &OpenAIModelsResponse{Body: []byte(`{"models":[{"slug":"alias","context_window":900000,"sentinel":"unchanged"}]}`)}
	require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, manifest, "", &account))
	model := decodeCodexManifestModels(t, manifest.Body)[0]
	require.Equal(t, "alias", model["slug"])
	require.Equal(t, "unchanged", model["sentinel"])
	require.Equal(t, float64(625001), model["context_window"])
	require.Equal(t, "custom", model["context_capacity_source"])
	require.Equal(t, 1, repo.availabilityCalls)
	require.Equal(t, 1, routes.calls)
}

func TestPinnedModelCapacityRetainsMappedSourcesAndFallbackCandidates(t *testing.T) {
	for _, codex := range []bool{false, true} {
		t.Run(fmt.Sprintf("codex=%t", codex), func(t *testing.T) {
			first := newGroupCapacityAccount(1, map[string]any{"public-alias": "native-a"}, map[string]int64{"native-a": 600000})
			second := newGroupCapacityAccount(2, map[string]any{"public-alias": "native-b"}, nil)
			fallback := newGroupCapacityAccount(3, map[string]any{"public-alias": "native-c"}, map[string]int64{"native-c": 300000})
			group := &Group{ID: 9, Platform: PlatformOpenAI, ModelRoutingEnabled: true,
				ModelRouting:              map[string][]int64{"public-alias": {first.ID}},
				CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{first.ID, second.ID}}}
			makeResponse := func(account *Account, model string, capacity int, sentinel string) *OpenAIModelsResponse {
				field, idField := "data", "id"
				if codex {
					field, idField = "models", "slug"
				}
				raw := []byte(fmt.Sprintf(`{"%s":[{"%s":%q,"context_window":%d,"sentinel":%q}]}`, field, idField, model, capacity, sentinel))
				response := &OpenAIModelsResponse{Body: append([]byte(nil), raw...), capacitySources: []codexModelCapacitySource{newCodexModelCapacitySource(account, raw)}}
				var err error
				response.Body, err = projectAccountModelsBody(response.Body, account, group, codex)
				require.NoError(t, err)
				require.NoError(t, applyMappedModelsCapacityVisibility(response, account, codex))
				return response
			}
			firstResponse := makeResponse(&first, "native-a", 900000, "first")
			secondResponse := makeResponse(&second, "native-b", 410000, "second")
			sources, protected, err := mergePinnedModelsCapacityProvenance([]pinnedOpenAIModelsResult{
				{account: &first, response: firstResponse}, {account: &second, response: secondResponse},
			}, codex)
			require.NoError(t, err)
			require.Len(t, sources, 2)
			require.True(t, sources[0].visibleModels["public-alias"])
			require.Empty(t, sources[1].visibleModels)
			require.Contains(t, string(sources[1].body), "native-b")
			require.NotContains(t, string(sources[1].body), "public-alias")
			repo := &groupCapacityAccountRepo{accounts: []Account{first, second}}
			svc := &OpenAIGatewayService{accountRepo: repo}
			project := func() map[string]any {
				response := &OpenAIModelsResponse{Body: append([]byte(nil), firstResponse.Body...), capacitySources: sources, capacityProtectedModels: protected}
				if codex {
					require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, response, "", nil))
				} else {
					require.NoError(t, svc.ProjectOpenAIModelsListContextCapacities(context.Background(), group, response, ""))
				}
				var envelope map[string][]map[string]any
				require.NoError(t, json.Unmarshal(response.Body, &envelope))
				if codex {
					return envelope["models"][0]
				}
				return envelope["data"][0]
			}
			row := project()
			require.Equal(t, float64(410000), row["context_window"], "losing ordinary source still narrows actual capacity")
			require.Equal(t, "upstream", row["context_capacity_source"])
			require.Equal(t, "first", row["sentinel"], "non-capacity fields keep the first discovered entry")
			repo.accounts = append(repo.accounts, fallback)
			row = project()
			require.Equal(t, float64(300000), row["context_window"], "discovery/routed IDs are not an exclusive forwarding pool")
			require.Equal(t, "custom", row["context_capacity_source"])
			require.Contains(t, string(firstResponse.Body), `"context_window":900000`, "final overlays do not mutate discovery responses")
		})
	}
}

func TestMappedOAuthCapacityProtectionSurvivesCandidateQueryFailure(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(fmt.Sprintf("pinned=%t", pinned), func(t *testing.T) {
			account := newGroupCapacityAccount(7, map[string]any{"public-alias": "upstream-model"}, nil)
			account.Type = AccountTypeOAuth
			group := &Group{ID: 9, Platform: PlatformOpenAI,
				CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{7}}}
			raw := []byte(`{"models":[{"slug":"upstream-model","context_window":123456,"max_context_window":234567,"auto_compact_token_limit":999999,"sentinel":{"keep":true}}]}`)
			response := &OpenAIModelsResponse{Body: append([]byte(nil), raw...), capacitySources: []codexModelCapacitySource{newCodexModelCapacitySource(&account, raw)}}
			require.NoError(t, ApplyPinnedCodexModelsMapping(response, &account, group))
			if pinned {
				var err error
				response.capacitySources, response.capacityProtectedModels, err = mergePinnedModelsCapacityProvenance([]pinnedOpenAIModelsResult{{account: &account, response: response}}, true)
				require.NoError(t, err)
			}
			require.True(t, response.capacitySources[0].visibleModels["public-alias"])
			require.NotContains(t, response.capacitySources[0].visibleModels, "upstream-model")
			svc := &OpenAIGatewayService{accountRepo: &groupCapacityAccountRepo{err: errors.New("fixture lookup failed")}}
			require.NoError(t, svc.ProjectCodexModelContextCapacities(context.Background(), group, response, "", &account))
			row := decodeCodexManifestModels(t, response.Body)[0]
			require.Equal(t, "public-alias", row["slug"])
			require.Equal(t, float64(123456), row["context_window"])
			require.Equal(t, float64(999999), row["auto_compact_token_limit"])
			require.Equal(t, map[string]any{"keep": true}, row["sentinel"])
			require.NotContains(t, row, "context_capacity_source")
			require.Equal(t, raw, response.capacitySources[0].body)
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

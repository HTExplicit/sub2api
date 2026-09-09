package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func managedCatalogV2Fixture() (*Group, []Account) {
	const groupID int64 = 901
	const model = "claude-fable-5.1"
	endpoints := []string{CompositeRouteEndpointResponses, CompositeRouteEndpointMessages, CompositeRouteEndpointChatCompletions}
	group := &Group{ID: groupID, Status: StatusActive, Platform: PlatformAnthropic,
		ModelAllowlist:     GroupModelAllowlist{Enabled: true, Models: []string{model, "claude-fable-5"}},
		ManagedModelRoutes: ManagedModelRoutesConfig{Version: ManagedModelRoutesVersion, Enabled: true},
	}
	accounts := []Account{
		{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"base_url": "https://native.example/v1", "api_key": "synthetic-test-only", "model_mapping": map[string]any{model: "private-native"}},
			Extra:       map[string]any{ModelContextOverridesExtraKey: map[string]int64{model: 900000, "claude-fable-5": 800000}}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"base_url": "https://compatible.example/v1", "api_key": "synthetic-test-only", "model_mapping": map[string]any{model: "private-compatible"}},
			Extra:       map[string]any{"openai_responses_mode": "force_responses", ModelContextOverridesExtraKey: map[string]int64{"wire/fable-5.1": 500000, "wire/fable-5.1-cc": 123456}}},
	}
	branch := func(index int, public, actual, protocol string) ManagedModelRouteBranch {
		account := &accounts[index]
		selector := ManagedModelBranchSelector(group.ID, public, account.Platform, protocol, actual)
		account.Credentials["model_mapping"].(map[string]any)[selector] = actual
		return ManagedModelRouteBranch{Selector: selector, TargetPlatform: account.Platform, UpstreamProtocol: protocol, Endpoints: endpoints,
			Accounts: []ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: actual, AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: endpoints}}}
	}
	group.ManagedModelRoutes.Routes = []ManagedModelRoute{
		{PublicModel: model, Endpoints: endpoints, Branches: []ManagedModelRouteBranch{
			branch(0, model, model, CompositeRouteEndpointMessages),
			branch(1, model, "wire/fable-5.1", CompositeRouteEndpointResponses),
			branch(1, model, "wire/fable-5.1-cc", CompositeRouteEndpointChatCompletions),
		}},
		{PublicModel: "claude-fable-5", Endpoints: endpoints, Branches: []ManagedModelRouteBranch{branch(0, "claude-fable-5", "claude-fable-5", CompositeRouteEndpointMessages)}},
	}
	return group, accounts
}

func TestManagedModelCatalogV2NativeGroupIncludesEveryBranchAndRealTarget(t *testing.T) {
	group, accounts := managedCatalogV2Fixture()
	future := time.Now().Add(time.Hour)
	accounts[1].RateLimitResetAt = &future
	before, err := json.Marshal(accounts)
	require.NoError(t, err)
	repo := &groupCapacityAccountRepo{accounts: accounts}
	svc := &GatewayService{accountRepo: repo}
	models, err := svc.ManagedPublicModelIDs(context.Background(), group, CompositeRouteEndpointResponses)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-fable-5.1", "claude-fable-5"}, models, "different versions remain separate public IDs")
	body, err := svc.BuildCodexModelsManifestForGroup(context.Background(), group, "", models)
	require.NoError(t, err)
	require.Equal(t, int64(123456), gjson.GetBytes(body, "models.0.context_window").Int(), "second target on a cooling cross-platform account sets the minimum")
	require.Equal(t, "custom", gjson.GetBytes(body, "models.0.context_capacity_source").String())
	require.Equal(t, "group_minimum", gjson.GetBytes(body, "models.0.context_capacity_reason").String())
	require.Equal(t, int64(800000), gjson.GetBytes(body, "models.1.context_window").Int())
	require.NotContains(t, string(body), "s2pub-")
	require.NotContains(t, string(body), "wire/fable")
	require.NotContains(t, string(body), "private-")
	ordinary, err := svc.ProjectModelListContextCapacities(context.Background(), group, &group.ID, group.Platform, []byte(`{"data":[{"id":"claude-fable-5.1","keep":true}]}`))
	require.NoError(t, err)
	require.Equal(t, int64(123456), gjson.GetBytes(ordinary, "data.0.context_window").Int())
	require.True(t, gjson.GetBytes(ordinary, "data.0.keep").Bool())
	after, err := json.Marshal(accounts)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "catalog building must not change private mappings, snapshots or persistent settings")
	require.Zero(t, repo.schedulableCalls, "v2 publication metadata is not transient scheduler availability")
}

func TestManagedModelCatalogV2CooldownDoesNotUnpublishButChangedIdentityDoes(t *testing.T) {
	group, accounts := managedCatalogV2Fixture()
	future := time.Now().Add(time.Hour)
	for i := range accounts {
		accounts[i].RateLimitResetAt = &future
	}
	repo := &groupCapacityAccountRepo{accounts: accounts}
	svc := &GatewayService{accountRepo: repo}
	models, err := svc.ManagedPublicModelIDs(context.Background(), group, "")
	require.NoError(t, err)
	require.Equal(t, []string{"claude-fable-5.1", "claude-fable-5"}, models)
	repo.accounts[0].Schedulable = false
	models, err = svc.ManagedPublicModelIDs(context.Background(), group, "")
	require.NoError(t, err)
	require.Equal(t, []string{"claude-fable-5.1"}, models, "one disabled native account must not remove a compatible published branch")
	repo.accounts[1].Credentials["api_key"] = "synthetic-identity-change"
	models, err = svc.ManagedPublicModelIDs(context.Background(), group, "")
	require.NoError(t, err)
	require.Empty(t, models, "a catalog may retain cooldowns but never advertise an invalidated account identity")
}

func TestManagedModelCatalogV2CapabilityIntersectionUsesAllActualTargets(t *testing.T) {
	group, accounts := managedCatalogV2Fixture()
	yes := true
	accounts[0].SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"claude-fable-5.1": {ID: "claude-fable-5.1", Reasoning: &yes, SupportedReasoningLevels: []string{"low", "medium", "high"}, InputModalities: []string{"text", "image"}},
	}})
	accounts[1].SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"wire/fable-5.1":    {ID: "wire/fable-5.1", Reasoning: &yes, SupportedReasoningLevels: []string{"low", "high"}, InputModalities: []string{"text", "image"}},
		"wire/fable-5.1-cc": {ID: "wire/fable-5.1-cc", Reasoning: &yes, SupportedReasoningLevels: []string{"low"}, InputModalities: []string{"text"}},
	}})
	svc := &GatewayService{accountRepo: &groupCapacityAccountRepo{accounts: accounts}}
	body, err := svc.BuildCodexModelsManifestForGroup(context.Background(), group, "", []string{"claude-fable-5.1"})
	require.NoError(t, err)
	rows := decodeCodexManifestModels(t, body)
	require.Len(t, rows, 1)
	require.Equal(t, []string{"low"}, effortsFromManifestModel(t, rows[0]))
	require.Equal(t, []any{"text"}, rows[0]["input_modalities"])
	require.Equal(t, "claude-fable-5.1", rows[0]["display_name"])
	require.NotContains(t, string(body), "wire/fable")
	require.False(t, gjson.GetBytes(body, "models.0.supports_search_tool").Bool(), "one Chat branch must not imply every native/Responses branch implements search")
}

func TestManagedModelCatalogV2CapacityKeepsOriginalSnapshotIdentity(t *testing.T) {
	group, accounts := managedCatalogV2Fixture()
	delete(accounts[1].Extra, ModelContextOverridesExtraKey)
	accounts[1].SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{Models: map[string]ModelContextCapacity{
		"wire/fable-5.1":    {ContextWindow: 410001, MaxContextWindow: 710003, MaxOutputTokens: 12345},
		"wire/fable-5.1-cc": {ContextWindow: 110003, MaxContextWindow: 510007, MaxOutputTokens: 1234},
	}})
	snapshot := accounts[1].GetUpstreamModelContextCapacitySnapshot()
	require.NotNil(t, snapshot)
	identity := ModelContextCapacitySourceIdentity(&accounts[1])
	repo := &groupCapacityAccountRepo{accounts: accounts}
	catalog := loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, nil, &group.ID, group.Platform, group)
	capacity := catalog.resolve(context.Background(), group.Platform, "claude-fable-5.1")
	require.Equal(t, int64(110003), capacity.ContextWindow)
	require.Equal(t, "upstream", capacity.Source)
	require.Equal(t, identity, ModelContextCapacitySourceIdentity(&accounts[1]))
	require.Equal(t, snapshot, accounts[1].GetUpstreamModelContextCapacitySnapshot())
}

func TestManagedModelCatalogV2CrossPlatformQueryRemainsBoundToGroup(t *testing.T) {
	group, accounts := managedCatalogV2Fixture()
	repo := &groupCapacityAccountRepo{accounts: accounts}
	catalog := loadGroupModelCapacityCatalog(context.Background(), repo, nil, nil, &config.Config{RunMode: config.RunModeSimple}, &group.ID, group.Platform, group)
	require.Len(t, catalog.accounts, 2, "native group metadata query includes compatible branch platforms")
	require.Equal(t, &group.ID, repo.queryGroupID)
	require.False(t, repo.includeGrouped, "managed groups must not expand to the instance-wide simple-mode pool")
	projected := managedModelCatalogAccounts(group, accounts, CompositeRouteEndpointResponses)
	require.Len(t, projected, 4, "same account's alternate targets and distinct versions remain separate metadata candidates")
	var targets []string
	for i := range projected {
		if actual, found := projected[i].GetModelMapping()["claude-fable-5.1"]; found {
			targets = append(targets, actual)
		}
	}
	require.ElementsMatch(t, []string{"claude-fable-5.1", "wire/fable-5.1", "wire/fable-5.1-cc"}, targets)
}

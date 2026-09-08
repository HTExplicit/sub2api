package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func managedRoutesSnapshotTestConfig() ManagedModelRoutesConfig {
	return ManagedModelRoutesConfig{
		Version: 1,
		Enabled: true,
		Routes: []ManagedModelRoute{{
			PublicModel:    "gpt-public",
			Aliases:        []string{"gpt-public-alias"},
			Selector:       "s2pub-g23-m0123456789abcdef",
			TargetPlatform: PlatformOpenAI,
			Endpoints:      []string{"responses", "messages"},
			Accounts: []ManagedModelRouteAccount{{
				AccountID:          42,
				UpstreamModel:      "namespace/gpt-upstream-vip",
				AccountFingerprint: "fingerprint-for-test",
				Endpoints:          []string{"responses"},
			}},
		}},
	}
}

func TestAPIKeyAuthSnapshotGroupManagedModelRoutesRoundtrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config ManagedModelRoutesConfig
	}{
		{name: "complete verified route", config: managedRoutesSnapshotTestConfig()},
		{name: "enabled empty routes remain enforced", config: ManagedModelRoutesConfig{Version: 1, Enabled: true}},
		{name: "unmanaged default", config: ManagedModelRoutesConfig{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(23)
			apiKey := &APIKey{
				ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
				User: &User{ID: 2, Status: StatusActive},
				Group: &Group{
					ID: groupID, Platform: PlatformOpenAI, Status: StatusActive,
					Hydrated: true, ManagedModelRoutes: tc.config,
				},
			}
			svc := &APIKeyService{}
			payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
			require.NoError(t, err)
			var cached APIKeyAuthCacheEntry
			require.NoError(t, json.Unmarshal(payload, &cached))

			materialized, used, err := svc.applyAuthCacheEntry("k-managed-routes-roundtrip", &cached)
			require.NoError(t, err)
			require.True(t, used)
			require.NotNil(t, materialized.Group)
			require.Equal(t, tc.config, materialized.Group.ManagedModelRoutes)
			require.Equal(t, apiKeyAuthSnapshotVersion, cached.Snapshot.Version)
			require.Equal(t, EffectiveManagedModelAllowlist(apiKey.Group), cached.Snapshot.Group.ModelAllowlist)
			require.Equal(t, EffectiveManagedModelAllowlist(apiKey.Group), materialized.Group.ModelAllowlist)
		})
	}
}

type managedRoutesAuthRepositoryStub struct {
	APIKeyRepository
	apiKey *APIKey
}

func (r *managedRoutesAuthRepositoryStub) GetByKeyForAuth(context.Context, string) (*APIKey, error) {
	return r.apiKey, nil
}

func TestAPIKeyAuthManagedModelRoutesOverrideDriftedAllowlist(t *testing.T) {
	for _, withSlots := range []bool{false, true} {
		t.Run(map[bool]string{false: "unlimited lookup", true: "bounded lookup"}[withSlots], func(t *testing.T) {
			storedGroup := &Group{
				ID: 23, Platform: PlatformOpenAI, Status: StatusActive,
				ManagedModelRoutes: managedRoutesSnapshotTestConfig(),
				ModelAllowlist:     GroupModelAllowlist{Models: []string{"private-model"}},
			}
			svc := &APIKeyService{apiKeyRepo: &managedRoutesAuthRepositoryStub{apiKey: &APIKey{
				ID: 1, UserID: 2, Group: storedGroup, User: &User{ID: 2, Status: StatusActive},
			}}}
			if withSlots {
				svc.authLookupSlots = make(chan struct{}, 1)
			}
			apiKey, err := svc.lookupAPIKeyForAuth(context.Background(), "k-managed-drift")
			require.NoError(t, err)
			require.Equal(t, GroupModelAllowlist{Enabled: true, Models: []string{"gpt-public"}}, apiKey.Group.ModelAllowlist)
			require.False(t, storedGroup.ModelAllowlist.Enabled, "auth projection must not rewrite an admin's stored group value")
			require.Equal(t, []string{"private-model"}, storedGroup.ModelAllowlist.Models)

			// A current-version cache may contain a disabled/stale ordinary list;
			// restore must derive the public list from managed routes independently.
			snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
			snapshot.Group.ModelAllowlist = GroupModelAllowlist{}
			materialized := svc.snapshotToAPIKey("k-managed-drift", snapshot)
			require.Equal(t, GroupModelAllowlist{Enabled: true, Models: []string{"gpt-public"}}, materialized.Group.ModelAllowlist)
		})
	}
}

func TestAPIKeyServiceRejectsV25AuthSnapshotWithoutManagedModelRoutes(t *testing.T) {
	svc := &APIKeyService{}
	got, used, err := svc.applyAuthCacheEntry("k-legacy-managed-routes", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 25},
	})
	require.NoError(t, err)
	require.False(t, used, "a pre-managed-routes cache entry must be reloaded from storage")
	require.Nil(t, got)
}

func TestGroupCopiesClearManagedModelRoutes(t *testing.T) {
	source := &Group{
		ID: 23, Name: "managed-source", Platform: PlatformOpenAI, Status: StatusActive,
		ManagedModelRoutes: managedRoutesSnapshotTestConfig(),
		DefaultMappedModel: "s2pub-g23-m0123456789abcdef",
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			OpusMappedModel: "private-opus-model",
			ExactModelMappings: map[string]string{
				"gpt-public": "s2pub-g23-m0123456789abcdef",
				"private":    "private-upstream-model",
			},
		},
	}
	for name, copy := range map[string]*Group{
		"ordinary duplicate": cloneGroupForDuplicate(source, "copy-operation"),
		"Cindy split target": BuildCindySplitTargetGroup(source, "split-target"),
	} {
		t.Run(name, func(t *testing.T) {
			require.Zero(t, copy.ID)
			require.Equal(t, ManagedModelRoutesConfig{}, copy.ManagedModelRoutes,
				"a new group cannot reuse selectors or verified membership from the source group")
			require.Empty(t, copy.DefaultMappedModel)
			require.Equal(t, map[string]string{"private": "private-upstream-model"}, copy.MessagesDispatchModelConfig.ExactModelMappings)
			require.Equal(t, "private-opus-model", copy.MessagesDispatchModelConfig.OpusMappedModel)
		})
	}
	require.Equal(t, managedRoutesSnapshotTestConfig(), source.ManagedModelRoutes)
	require.Equal(t, "s2pub-g23-m0123456789abcdef", source.DefaultMappedModel)
	require.Len(t, source.MessagesDispatchModelConfig.ExactModelMappings, 2)

	unmanaged := &Group{
		ID: 99, Name: "unmanaged-source", DefaultMappedModel: "private-model",
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			ExactModelMappings: map[string]string{"private": "private-model"},
		},
	}
	copy := cloneGroupForDuplicate(unmanaged, "")
	require.Equal(t, unmanaged.DefaultMappedModel, copy.DefaultMappedModel)
	require.Equal(t, unmanaged.MessagesDispatchModelConfig, copy.MessagesDispatchModelConfig)
}

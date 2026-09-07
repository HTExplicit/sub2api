package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestAPIKeyService_RejectsV24AuthSnapshotWithoutModelAllowlist(t *testing.T) {
	// Both the downstream identity snapshot and the upstream allowlist snapshot
	// used version 24 independently. A persisted downstream entry must be missed,
	// not accepted with a zero-value allowlist after the JSON field rename.
	var cached APIKeyAuthCacheEntry
	err := json.Unmarshal([]byte(`{"snapshot":{"version":24,"api_key_id":1,"user_id":2,"group_id":9,"status":"active","user":{"id":2,"status":"active"},"group":{"id":9,"platform":"cindy","status":"active","strict_cindy_known":true,"strict_cindy":true,"models_list_config":{"enabled":true,"models":["gpt-5.4"]}}}}`), &cached)
	if err != nil {
		t.Fatalf("decode legacy snapshot: %v", err)
	}

	svc := &APIKeyService{}
	apiKey, used, err := svc.applyAuthCacheEntry("k-legacy-v24-models-list", &cached)
	if err != nil || used || apiKey != nil {
		t.Fatalf("expected v24 snapshot cache miss, got key=%v used=%v err=%v", apiKey != nil, used, err)
	}
}

func TestAPIKeyAuthSnapshotGroupModelAllowlistRoundtrip(t *testing.T) {
	for _, test := range []struct {
		name    string
		config  GroupModelAllowlist
		allowed bool
	}{
		{name: "enabled", config: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.4", "claude-*"}}, allowed: true},
		{name: "legacy enabled empty remains deny all", config: GroupModelAllowlist{Enabled: true}},
		{name: "disabled retains entries", config: GroupModelAllowlist{Models: []string{"claude-*"}}, allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			groupID := int64(9)
			apiKey := &APIKey{
				ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
				User: &User{ID: 2, Status: StatusActive},
				Group: &Group{
					ID: groupID, Platform: PlatformCindy, Status: StatusActive,
					Hydrated: true, StrictCindyKnown: true, StrictCindy: true,
					ModelAllowlist: test.config, ForceOpenAIFast: true,
				},
			}
			svc := &APIKeyService{}
			payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
			if err != nil {
				t.Fatalf("encode snapshot: %v", err)
			}
			var cached APIKeyAuthCacheEntry
			if err := json.Unmarshal(payload, &cached); err != nil {
				t.Fatalf("decode snapshot: %v", err)
			}
			materialized, used, err := svc.applyAuthCacheEntry("k-allowlist-roundtrip", &cached)
			if err != nil || !used || materialized == nil || materialized.Group == nil {
				t.Fatalf("expected current snapshot cache hit, got used=%v err=%v", used, err)
			}
			group := materialized.Group
			if !reflect.DeepEqual(test.config, group.ModelAllowlist) {
				t.Fatalf("allowlist changed in snapshot: got %#v want %#v", group.ModelAllowlist, test.config)
			}
			if got := group.ModelAllowlist.Allows("gpt-5.4"); got != test.allowed {
				t.Fatalf("allowlist admission changed in snapshot: got %v want %v", got, test.allowed)
			}
			if group.Platform != PlatformCindy || !group.StrictCindyKnown || !group.StrictCindy || !group.ForceOpenAIFast {
				t.Fatal("allowlist snapshot lost downstream Cindy identity or Fast fields")
			}
		})
	}
}

func TestAPIKeyService_RejectsV10AuthSnapshotWithoutModelAllowlist(t *testing.T) {
	groupID := int64(9)
	svc := &APIKeyService{}

	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-models-list", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{
			Version:  10,
			APIKeyID: 1,
			UserID:   2,
			GroupID:  &groupID,
			Status:   StatusActive,
			User: APIKeyAuthUserSnapshot{
				ID:          2,
				Status:      StatusActive,
				Role:        RoleUser,
				Balance:     10,
				Concurrency: 3,
			},
			Group: &APIKeyAuthGroupSnapshot{
				ID:               groupID,
				Name:             "openai",
				Platform:         PlatformOpenAI,
				Status:           StatusActive,
				SubscriptionType: SubscriptionTypeStandard,
				RateMultiplier:   1,
			},
		},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok {
		t.Fatalf("expected v10 auth snapshot to be rejected after model_allowlist was added")
	}
	if apiKey != nil {
		t.Fatalf("expected no API key from stale snapshot, got %#v", apiKey)
	}
}

func TestAPIKeyService_RejectsV15AuthSnapshotWithoutReasoningEffortPolicy(t *testing.T) {
	svc := &APIKeyService{}

	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-reasoning-mappings", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 15},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok {
		t.Fatal("expected v15 auth snapshot to be rejected after reasoning effort policy was added")
	}
	if apiKey != nil {
		t.Fatalf("expected no API key from stale snapshot, got %#v", apiKey)
	}
}

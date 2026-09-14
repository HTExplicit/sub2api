package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCapacityIdentityUsesForwardTargetAndLegacyOverrides(t *testing.T) {
	account := newGroupCapacityAccount(1, map[string]any{"public": "gpt-5.6-sol-high", "other": "gpt-5.6-sol"},
		map[string]int64{"gpt-5.6-sol-high": 650001, "public": 650001})
	resolve := NewAccountModelContextCapacityResolver(&account)
	require.Equal(t, int64(650001), resolve("gpt-5.6-sol", nil).ContextWindow)
	require.Equal(t, int64(650001), resolve("gpt-5.6-sol-high", nil).ContextWindow)
	catalog := newGroupModelCapacityCatalog([]Account{account}, true, nil, true, nil, nil)
	require.Equal(t, int64(650001), catalog.resolve(context.Background(), PlatformOpenAI, "public").ContextWindow)
	rows := BuildAccountModelContextCapacityRows(&account, []string{"gpt-5.6-sol-high"})
	var row *AccountModelContextCapacityRow
	for i := range rows {
		if rows[i].UpstreamModelID == "gpt-5.6-sol" {
			row = &rows[i]
		}
	}
	require.NotNil(t, row)
	require.Contains(t, row.UpstreamModelIDs, "gpt-5.6-sol-high")
	require.Contains(t, row.Aliases, "public")
	require.Equal(t, int64(650001), row.EffectiveContextWindow)
}

func TestCapacityIdentityConflictsAndExplicitEdit(t *testing.T) {
	account := newGroupCapacityAccount(1, map[string]any{"a": "native", "b": "native"}, map[string]int64{"a": 600001, "b": 700001, "unrelated": 12345})
	capacity := ResolveAccountModelContextCapacity(&account, "native")
	require.Equal(t, "conflicting_alias_overrides", capacity.Reason)
	require.NotEqual(t, "custom", capacity.Source)
	value := int64(800001)
	updated, err := ApplyAccountModelContextOverrides(&account, account.Extra[ModelContextOverridesExtraKey], map[string]*int64{"native": &value})
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"native": value, "unrelated": 12345}, updated)
	account.Extra[ModelContextOverridesExtraKey] = map[string]int64{"native": value, "a": 1000, "b": 2000}
	require.Equal(t, value, ResolveAccountModelContextCapacity(&account, "native").ContextWindow)
}

func TestCapacityIdentityDoesNotRemapRealTargetTwice(t *testing.T) {
	account := newGroupCapacityAccount(1, map[string]any{"a": "b", "b": "c"}, map[string]int64{"b": 650001, "c": 750001})
	catalog := newGroupModelCapacityCatalog([]Account{account}, true, nil, true, nil, nil)
	require.Equal(t, int64(650001), catalog.resolve(context.Background(), PlatformOpenAI, "a").ContextWindow)
	require.Equal(t, int64(750001), catalog.resolve(context.Background(), PlatformOpenAI, "b").ContextWindow)
	value := int64(850001)
	updated, err := ApplyAccountModelContextOverrides(&account, account.Extra[ModelContextOverridesExtraKey], map[string]*int64{"b": &value})
	require.NoError(t, err)
	require.Equal(t, value, updated["b"])
	require.Equal(t, int64(750001), updated["c"])
}

func TestCapacityIdentitySnapshotUsesRecognizedSpellingOnly(t *testing.T) {
	account := newGroupCapacityAccount(1, nil, nil)
	account.SetUpstreamModelContextCapacitySnapshot(UpstreamModelContextCapacitySnapshot{Models: map[string]ModelContextCapacity{
		"gpt-5.6-sol-high": {ContextWindow: 650001}, "vendor/gpt-5.6-sol-high": {ContextWindow: 450001},
	}})
	values, _ := capacitySnapshotTargets(&account, account.GetUpstreamModelContextCapacitySnapshot())
	require.Equal(t, int64(650001), values["gpt-5.6-sol"].ContextWindow)
	require.Equal(t, int64(450001), values["vendor/gpt-5.6-sol-high"].ContextWindow)
}

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type cindyFreeCatalogFixture struct {
	SchemaVersion  int                     `json:"schemaVersion"`
	SourceRevision string                  `json:"sourceRevision"`
	Models         []cindyFreeFixtureModel `json:"models"`
}

type cindyFreeFixtureModel struct {
	ID               string         `json:"id"`
	PublicID         string         `json:"publicId"`
	Kind             CindyModelKind `json:"kind"`
	OrdinaryRoutable bool           `json:"ordinaryRoutable"`
}

func TestCindyFreeCatalogFixtureMatchesExactElevenItemInventory(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/cindy_free_catalog_2026-08-29.json")
	require.NoError(t, err)

	var fixture cindyFreeCatalogFixture
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, 1, fixture.SchemaVersion)
	require.Equal(t, CindyFreeModelCatalogSourceRevision, fixture.SourceRevision)
	require.Len(t, fixture.Models, 11)

	capabilities := CindyCapabilities()
	require.Len(t, capabilities, len(fixture.Models))
	byUpstreamID := make(map[string]CindyCapability, len(capabilities))
	for _, capability := range capabilities {
		_, duplicate := byUpstreamID[capability.LiveUpstreamID]
		require.False(t, duplicate, capability.LiveUpstreamID)
		byUpstreamID[capability.LiveUpstreamID] = capability
	}

	ids := make([]string, 0, len(fixture.Models))
	ordinaryCount := 0
	specialCount := 0
	for _, model := range fixture.Models {
		ids = append(ids, model.ID)
		capability, ok := byUpstreamID[model.ID]
		require.True(t, ok, model.ID)
		require.Equal(t, model.PublicID, capability.PublicID, model.ID)
		require.Equal(t, model.Kind, capability.Kind, model.ID)
		require.Equal(t, model.OrdinaryRoutable, capability.PublicModel, model.ID)
		if model.OrdinaryRoutable {
			ordinaryCount++
			require.Equal(t, CindyModelKindText, capability.Kind, model.ID)
			require.NotEmpty(t, capability.VerifiedEndpoints, model.ID)
		} else {
			specialCount++
			require.Equal(t, CindyModelKindSpecial, capability.Kind, model.ID)
		}
	}
	require.Equal(t, 9, ordinaryCount)
	require.Equal(t, 2, specialCount)

	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n") + "\n"))
	require.Equal(t, CindyFreeModelCatalogSHA256, hex.EncodeToString(sum[:]))
}

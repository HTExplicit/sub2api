package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func metadataIdentityTestAccount() *Account {
	return &Account{ID: 9201, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		UpdatedAt:   time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC),
		Credentials: map[string]any{"base_url": "https://provider.example/v1", "api_key": "old-key", "api_protocol": APIProtocolAdaptive}}
}

func TestUpstreamMetadataIdentityTracksSourceContract(t *testing.T) {
	base := UpstreamModelMetadataSourceIdentity(metadataIdentityTestAccount())
	for name, edit := range map[string]func(*Account){
		"platform":     func(a *Account) { a.Platform = PlatformKimi },
		"type":         func(a *Account) { a.Type = AccountTypeOAuth },
		"base URL":     func(a *Account) { a.Credentials["base_url"] = "https://other.example/v1" },
		"account mode": func(a *Account) { a.Credentials["account_mode"] = AccountModeCoding },
		"protocol":     func(a *Account) { a.Credentials["api_protocol"] = APIProtocolAnthropic },
		"protocol endpoint": func(a *Account) {
			a.Credentials["api_base_urls"] = map[string]any{APIProtocolResponses: "https://responses.example/v1"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			account := metadataIdentityTestAccount()
			edit(account)
			require.NotEqual(t, base, UpstreamModelMetadataSourceIdentity(account))
		})
	}
	for name, edit := range map[string]func(*Account){
		"key rotation":   func(a *Account) { a.Credentials["api_key"] = "new-key" },
		"public mapping": func(a *Account) { a.Credentials["model_mapping"] = map[string]any{"public": "real"} },
		"name and proxy": func(a *Account) { id := int64(9); a.Name = "Renamed"; a.ProxyID = &id },
		"URL spelling":   func(a *Account) { a.Credentials["base_url"] = " https://PROVIDER.EXAMPLE:443/v1/ " },
	} {
		t.Run(name, func(t *testing.T) {
			account := metadataIdentityTestAccount()
			edit(account)
			require.Equal(t, base, UpstreamModelMetadataSourceIdentity(account))
		})
	}
}

func TestUpstreamMetadataIdentityInvalidatesEvidenceWithoutDeletingDiagnostics(t *testing.T) {
	account := metadataIdentityTestAccount()
	snapshot := UpstreamModelMetadataSnapshot{Source: ModelContextSourceUpstream, SourceIdentity: UpstreamModelMetadataSourceIdentity(account),
		SyncedAt: "2020-01-01T00:00:00Z", Models: map[string]UpstreamModelMetadata{
			"native":    {ID: "native", ContextWindow: 555000, CapacitySource: ModelContextSourceUpstream},
			"reference": {ID: "reference", ContextWindow: 444000, CapacitySource: ModelContextSourceRegistry},
		}}
	account.SetUpstreamModelMetadataSnapshot(snapshot)
	upstream, registry, _ := accountCapacityObservations(account)
	require.Equal(t, int64(555000), upstream["native"].ContextWindow, "age is not a hard expiration")
	require.Equal(t, int64(444000), registry["reference"].ContextWindow)
	account.Credentials["base_url"] = "https://new.example/v1"
	upstream, registry, _ = accountCapacityObservations(account)
	require.Empty(t, upstream)
	require.Empty(t, registry, "registry capacity is also bound to the old source snapshot")
	raw, _, _ := accountRawCapacityObservations(account)
	require.Equal(t, int64(555000), raw["native"].ContextWindow)
	_, rawRegistry, _ := accountRawCapacityObservations(account)
	require.Equal(t, int64(444000), rawRegistry["reference"].ContextWindow)
	require.Equal(t, snapshot, *account.GetUpstreamModelMetadataSnapshot())
	snapshot.SourceIdentity = ""
	account.SetUpstreamModelMetadataSnapshot(snapshot)
	upstream, _, _ = accountCapacityObservations(account)
	require.Empty(t, upstream, "legacy data must not be bound implicitly")
}

func TestUpstreamMetadataIdentityTracksOpenAIWireModeAndEscapedPath(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "key", "base_url": "https://provider.example/v1%2Fmodels"}}
	base := UpstreamModelMetadataSourceIdentity(account)
	account.Credentials["base_url"] = "https://provider.example/v1/models"
	require.NotEqual(t, base, UpstreamModelMetadataSourceIdentity(account), "escaped reserved path is not a path separator")
	account.Credentials["base_url"] = "https://provider.example/v1%2Fmodels"
	for _, mode := range []openai_compat.ResponsesSupportMode{
		openai_compat.ResponsesSupportModeForceResponses,
		openai_compat.ResponsesSupportModeForceChatCompletions,
	} {
		account.Extra = map[string]any{openai_compat.ExtraKeyResponsesMode: string(mode)}
		require.NotEqual(t, base, UpstreamModelMetadataSourceIdentity(account), "forced response wire mode binds a different source")
		account.Extra = nil
	}
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
	require.NotEqual(t, base, UpstreamModelMetadataSourceIdentity(account), "automatic wire mode observation binds a different source")
}

type metadataCASRepo struct {
	AccountRepository
	latest    *Account
	revisions []time.Time
	results   []bool
	err       error
	reads     int
	updates   map[string]any
}

func (r *metadataCASRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	return r.latest, nil
}

func (r *metadataCASRepo) UpdateExtraIfRevision(_ context.Context, _ int64, revision time.Time, updates map[string]any) (bool, error) {
	r.revisions = append(r.revisions, revision)
	if r.err != nil {
		return false, r.err
	}
	applied := true
	if len(r.results) >= len(r.revisions) {
		applied = r.results[len(r.revisions)-1]
	}
	if applied {
		r.updates = updates
	}
	return applied, nil
}

func TestUpstreamMetadataCASPreservesSourceAndNewerCatalog(t *testing.T) {
	for _, name := range []string{"ordinary edit", "endpoint changed", "newer catalog", "newer empty catalog", "second conflict", "write failure"} {
		t.Run(name, func(t *testing.T) {
			account := metadataIdentityTestAccount()
			latest := metadataIdentityTestAccount()
			latest.UpdatedAt = latest.UpdatedAt.Add(time.Minute)
			latest.Name = "edited name"
			repo := &metadataCASRepo{latest: latest, results: []bool{false, true}}
			switch name {
			case "endpoint changed":
				latest.Credentials["base_url"] = "https://new.example"
			case "newer catalog":
				latest.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", SourceIdentity: UpstreamModelMetadataSourceIdentity(latest), Models: map[string]UpstreamModelMetadata{"newer": {ContextWindow: 900000}}})
			case "newer empty catalog":
				latest.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", SourceIdentity: UpstreamModelMetadataSourceIdentity(latest), SyncedAt: latest.UpdatedAt.Format(time.RFC3339Nano), Models: map[string]UpstreamModelMetadata{}})
			case "second conflict":
				repo.results = []bool{false, false}
			case "write failure":
				repo.err = errors.New("write failed")
			}
			builds := 0
			applied, err := persistUpstreamModelMetadataSnapshot(context.Background(), repo, account, UpstreamModelMetadataSourceIdentity(account), func(current *Account) UpstreamModelMetadataSnapshot {
				builds++
				return UpstreamModelMetadataSnapshot{Source: "upstream", SourceIdentity: UpstreamModelMetadataSourceIdentity(current), Models: map[string]UpstreamModelMetadata{"received": {ContextWindow: 700000}}}
			})
			if name == "ordinary edit" {
				require.NoError(t, err)
				require.True(t, applied)
				require.Equal(t, 2, builds, "retry rebuilds from the one reread without requesting upstream again")
				require.Equal(t, []time.Time{account.UpdatedAt, latest.UpdatedAt}, repo.revisions)
				require.Contains(t, account.GetUpstreamModelMetadataSnapshot().Models, "received")
			} else {
				require.Error(t, err)
				require.False(t, applied)
				require.Nil(t, repo.updates)
				require.Nil(t, account.GetUpstreamModelMetadataSnapshot())
			}
			require.LessOrEqual(t, repo.reads, 1)
			require.LessOrEqual(t, len(repo.revisions), 2)
		})
	}
}

func TestUpstreamMetadataCASRequiresFencedRepository(t *testing.T) {
	account := metadataIdentityTestAccount()
	builds := 0
	applied, err := persistUpstreamModelMetadataSnapshot(context.Background(), &struct{ AccountRepository }{}, account, UpstreamModelMetadataSourceIdentity(account), func(*Account) UpstreamModelMetadataSnapshot {
		builds++
		return UpstreamModelMetadataSnapshot{}
	})
	require.ErrorContains(t, err, "revision-fenced")
	require.False(t, applied)
	require.Zero(t, builds)
}

func TestUpstreamMetadataObservationBindsOnlyFreshDeclarations(t *testing.T) {
	account := metadataIdentityTestAccount()
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
		"old":       {ID: "old", ContextWindow: 900000},
		"new":       {ID: "new", ContextWindow: 800000, Description: "retained capability"},
		"reference": {ID: "reference", ContextWindow: 400000, CapacitySource: ModelContextSourceRegistry},
	}})
	snapshot, changed := buildUpstreamCapacityObservationSnapshot(account, UpstreamModelMetadataSourceIdentity(account), map[string]ModelContextCapacity{"new": {ContextWindow: 500000}}, time.Now())
	require.True(t, changed)
	require.NotContains(t, snapshot.Models, "old")
	require.Equal(t, int64(500000), snapshot.Models["new"].ContextWindow)
	require.Equal(t, "retained capability", snapshot.Models["new"].Description)
	require.NotContains(t, snapshot.Models, "reference", "unbound legacy registry capacity is not promoted into the new snapshot")
	require.Equal(t, int64(900000), account.GetUpstreamModelMetadataSnapshot().Models["old"].ContextWindow, "building does not rewrite diagnostics")
}

func TestUpstreamMetadataSourceChangeDropsRegistryCapacityWithoutRebinding(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "old endpoint", true: "legacy unbound"}[legacy], func(t *testing.T) {
			account := metadataIdentityTestAccount()
			identity := UpstreamModelMetadataSourceIdentity(account)
			if legacy {
				identity = ""
			}
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "models.dev", SourceIdentity: identity,
				Models: map[string]UpstreamModelMetadata{"reference": {ID: "reference", Description: "capability retained", ContextWindow: 700000, CapacitySource: ModelContextSourceRegistry}}})
			if !legacy {
				account.Credentials["base_url"] = "https://changed.example/v1"
			}
			_, registry, _ := accountCapacityObservations(account)
			require.Empty(t, registry)
			metadata, exists := account.CurrentUpstreamModelMetadata("reference")
			require.True(t, exists)
			require.Zero(t, metadata.ContextWindow)
			require.Equal(t, "capability retained", metadata.Description)
			snapshot, changed := buildUpstreamCapacityObservationSnapshot(account, UpstreamModelMetadataSourceIdentity(account), map[string]ModelContextCapacity{"fresh": {ContextWindow: 300000}}, time.Now())
			require.True(t, changed)
			require.Zero(t, snapshot.Models["reference"].ContextWindow)
			require.Empty(t, snapshot.Models["reference"].CapacitySource)
			require.Equal(t, "capability retained", snapshot.Models["reference"].Description)
			require.Equal(t, int64(700000), account.GetUpstreamModelMetadataSnapshot().Models["reference"].ContextWindow)
		})
	}
}

func TestUpstreamMetadataDedupeChecksCurrentSnapshot(t *testing.T) {
	for _, change := range []string{"capacity replaced", "model removed", "snapshot unbound", "source replaced"} {
		t.Run(change, func(t *testing.T) {
			account := metadataIdentityTestAccount()
			upstreamCapacityObservationMarks.Delete(account.ID)
			t.Cleanup(func() { upstreamCapacityObservationMarks.Delete(account.ID) })
			repo := &metadataCASRepo{}
			body := []byte(`{"data":[{"id":"native","context_window":500000}]}`)
			recordUpstreamModelCapacityObservations(context.Background(), repo, account, body)
			require.Len(t, repo.revisions, 1)
			snapshot := account.GetUpstreamModelMetadataSnapshot()
			switch change {
			case "capacity replaced":
				entry := snapshot.Models["native"]
				entry.ContextWindow = 900000
				snapshot.Models["native"] = entry
			case "model removed":
				delete(snapshot.Models, "native")
			case "snapshot unbound":
				snapshot.SourceIdentity = ""
			case "source replaced":
				snapshot.SourceIdentity = "another-source"
			}
			account.SetUpstreamModelMetadataSnapshot(*snapshot)
			recordUpstreamModelCapacityObservations(context.Background(), repo, account, body)
			require.Len(t, repo.revisions, 2, "a prior process mark cannot skip a current mismatched snapshot")
			current := account.GetUpstreamModelMetadataSnapshot()
			require.Equal(t, UpstreamModelMetadataSourceIdentity(account), current.SourceIdentity)
			require.Equal(t, int64(500000), current.Models["native"].ContextWindow)
		})
	}
}

func TestUpstreamMetadataObservationMarksOnlyAppliedWrites(t *testing.T) {
	account := metadataIdentityTestAccount()
	upstreamCapacityObservationMarks.Delete(account.ID)
	t.Cleanup(func() { upstreamCapacityObservationMarks.Delete(account.ID) })
	repo := &metadataCASRepo{err: errors.New("write failed")}
	body := []byte(`{"data":[{"id":"native","context_window":500000}]}`)
	recordUpstreamModelCapacityObservations(context.Background(), repo, account, body)
	_, marked := upstreamCapacityObservationMarks.Load(account.ID)
	require.False(t, marked)
	repo.err = nil
	recordUpstreamModelCapacityObservations(context.Background(), repo, account, body)
	_, marked = upstreamCapacityObservationMarks.Load(account.ID)
	require.True(t, marked)
	writes := len(repo.revisions)
	recordUpstreamModelCapacityObservations(context.Background(), repo, account, body)
	require.Len(t, repo.revisions, writes, "identical successful observations are deduplicated")
	account.Credentials["base_url"] = "https://new.example/v1"
	recordUpstreamModelCapacityObservations(context.Background(), repo, account, body)
	require.Len(t, repo.revisions, writes+1, "the same numbers from another source are not deduplicated")
}

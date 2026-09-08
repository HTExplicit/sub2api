//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func schedulerManagedIdentityFixture(id int64, platform string) service.Account {
	proxyID := int64(37)
	return service.Account{
		ID: id, Platform: platform, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		GroupIDs: []int64{28},
		Credentials: map[string]any{
			"api_key": "offline-cache-fixture", "base_url": "https://fixture.invalid",
			"model_mapping":         map[string]any{"public-model": "verified-wire-model"},
			"pool_mode":             "round_robin",
			"pool_mode_retry_count": 2,
			"header_overrides":      map[string]any{"x-private-fixture": "do-not-project"},
			"nullable_identity":     nil,
		},
		Extra: map[string]any{
			"anthropic_apikey_auth_scheme": "authorization_bearer",
			"anthropic_passthrough":        false,
			"openai_compact_mode":          nil,
			"enable_tls_fingerprint":       false,
			"tls_fingerprint_profile_id":   nil,
		},
		ProxyID: &proxyID,
		Proxy: &service.Proxy{
			ID: proxyID, Protocol: "http", Host: "127.0.0.1", Port: 18080,
			Username: "offline-user", Password: "offline-password", Status: service.StatusActive,
		},
	}
}

func TestSchedulerCacheManagedIdentityRoundTripKeepsFullDigestWithoutExpandingProjection(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			account := schedulerManagedIdentityFixture(15523, platform)
			original, err := json.Marshal(account)
			require.NoError(t, err)
			fingerprint := service.ManagedModelAccountFingerprint(&account)
			require.Len(t, fingerprint, 64)
			bucket := service.SchedulerBucket{GroupID: 28, Platform: platform, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))

			snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
			require.NoError(t, err)
			require.True(t, hit)
			require.Len(t, snapshot, 1)
			metadata := snapshot[0]
			require.True(t, service.ValidSchedulerMetadataIdentity(metadata))
			require.Equal(t, fingerprint, metadata.SchedulerMetadata.IdentityFingerprint)
			require.Empty(t, service.ManagedModelAccountFingerprint(metadata), "a projection must not become a new full-account proof")
			require.Equal(t, "verified-wire-model", metadata.GetMappedModel("public-model"))
			for _, key := range []string{"pool_mode", "pool_mode_retry_count", "header_overrides", "nullable_identity"} {
				require.NotContains(t, metadata.Credentials, key)
			}
			for key := range account.Extra {
				require.NotContains(t, metadata.Extra, key)
			}
			require.Nil(t, metadata.ProxyID)
			require.Nil(t, metadata.Proxy)
			metaJSON, err := json.Marshal(metadata)
			require.NoError(t, err)
			require.NotContains(t, string(metaJSON), "do-not-project")
			require.NotContains(t, string(metaJSON), "offline-password")

			full, err := cache.GetAccount(ctx, account.ID)
			require.NoError(t, err)
			require.NotNil(t, full)
			require.Nil(t, full.SchedulerMetadata)
			require.Equal(t, fingerprint, service.ManagedModelAccountFingerprint(full))
			require.Equal(t, "round_robin", full.Credentials["pool_mode"])
			require.EqualValues(t, 2, full.Credentials["pool_mode_retry_count"])
			require.Contains(t, full.Credentials, "nullable_identity")
			require.Nil(t, full.Credentials["nullable_identity"])
			require.Contains(t, full.Extra, "openai_compact_mode")
			require.Nil(t, full.Extra["openai_compact_mode"])
			require.Equal(t, account.Proxy.URL(), full.Proxy.URL())
			fullJSON := cache.rdb.Get(ctx, schedulerAccountKey(strconv.FormatInt(account.ID, 10))).Val()
			require.NotContains(t, fullJSON, "scheduler_metadata")
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.Equal(t, original, after, "metadata construction must not mutate the full source")
		})
	}
}

type schedulerManagedIdentityFallbackRepo struct {
	service.AccountRepository
	accounts  []service.Account
	listReads int
	fullReads int
}

func (r *schedulerManagedIdentityFallbackRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]service.Account, error) {
	r.listReads++
	var accounts []service.Account
	for _, account := range r.accounts {
		if groupID == 28 && account.Platform == platform {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (r *schedulerManagedIdentityFallbackRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	r.fullReads++
	for _, account := range r.accounts {
		if account.ID == id {
			return &account, nil
		}
	}
	return nil, nil
}

func TestSchedulerCacheManagedIdentityLegacyOrInvalidSnapshotFallsBackAndRewarms(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*service.Account)
	}{
		{"legacy missing marker", func(a *service.Account) { a.SchedulerMetadata = nil }},
		{"unknown version", func(a *service.Account) { a.SchedulerMetadata.Version++ }},
		{"empty digest", func(a *service.Account) { a.SchedulerMetadata.IdentityFingerprint = "" }},
		{"short digest", func(a *service.Account) { a.SchedulerMetadata.IdentityFingerprint = strings.Repeat("a", 63) }},
		{"non hex digest", func(a *service.Account) { a.SchedulerMetadata.IdentityFingerprint = strings.Repeat("g", 64) }},
		{"uppercase digest", func(a *service.Account) { a.SchedulerMetadata.IdentityFingerprint = strings.Repeat("A", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			account := schedulerManagedIdentityFixture(15523, service.PlatformOpenAI)
			other := schedulerManagedIdentityFixture(16007, service.PlatformOpenAI)
			bucket := service.SchedulerBucket{GroupID: 28, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{other, account}))
			metadata := buildSchedulerMetadataAccount(account)
			tc.mutate(&metadata)
			encoded, err := json.Marshal(metadata)
			require.NoError(t, err)
			require.NoError(t, cache.rdb.Set(ctx, schedulerAccountMetaKey(strconv.FormatInt(account.ID, 10)), encoded, 0).Err())
			snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
			require.NoError(t, err)
			require.False(t, hit, "one unproven member must miss the entire bucket")
			require.Nil(t, snapshot)

			repo := &schedulerManagedIdentityFallbackRepo{accounts: []service.Account{other, account}}
			svc := service.NewSchedulerSnapshotService(cache, nil, repo, nil, nil)
			fresh, mixed, err := svc.ListSchedulableAccounts(ctx, &bucket.GroupID, bucket.Platform, false)
			require.NoError(t, err)
			require.False(t, mixed)
			require.Len(t, fresh, 2)
			require.Equal(t, 1, repo.listReads)
			require.Nil(t, fresh[1].SchedulerMetadata, "the miss must return the complete DB row")
			require.Contains(t, fresh[1].Credentials, "pool_mode")
			warmed, _, err := svc.ListSchedulableAccounts(ctx, &bucket.GroupID, bucket.Platform, false)
			require.NoError(t, err)
			require.Len(t, warmed, 2)
			require.Equal(t, 1, repo.listReads, "the next read must reuse the rebuilt cache")
			require.True(t, service.ValidSchedulerMetadataIdentity(&warmed[1]))
			require.Equal(t, service.ManagedModelAccountFingerprint(&account), warmed[1].SchedulerMetadata.IdentityFingerprint)
		})
	}
}

func TestSchedulerCacheManagedIdentityProjectionCannotPolluteFullWrites(t *testing.T) {
	for _, invalidMarker := range []bool{false, true} {
		for _, writer := range []string{"SetAccount", "writeAccountIDs", "SetSnapshot", "SetSnapshotAndReturnAccountIDs"} {
			name := writer + "/valid_marker"
			if invalidMarker {
				name = writer + "/invalid_marker"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				cache := newSchedulerCacheUnit(t)
				cache.writeChunkSize = 1 // A late projection must stop earlier chunks too.
				first := schedulerManagedIdentityFixture(15523, service.PlatformOpenAI)
				second := schedulerManagedIdentityFixture(16007, service.PlatformOpenAI)
				bucket := service.SchedulerBucket{GroupID: 28, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
				token, err := cache.CaptureBucketWriteToken(ctx, bucket)
				require.NoError(t, err)
				require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{first, second}))
				require.NoError(t, cache.rdb.Set(ctx, schedulerLegacyAccountMetaKey("16007"), "legacy-sentinel", 0).Err())
				require.NoError(t, cache.rdb.Set(ctx, schedulerLastUsedKey("16007"), "1700000000000", 0).Err())
				keys := []string{
					schedulerAccountKey("15523"), schedulerAccountMetaKey("15523"),
					schedulerAccountKey("16007"), schedulerAccountMetaKey("16007"),
					schedulerLegacyAccountMetaKey("16007"), schedulerLastUsedKey("16007"),
					schedulerBucketKey(schedulerVersionPrefix, bucket),
					schedulerBucketKey(schedulerActivePrefix, bucket),
					schedulerBucketKey(schedulerReadyPrefix, bucket),
				}
				before, err := cache.rdb.MGet(ctx, keys...).Result()
				require.NoError(t, err)
				beforeKeys, err := cache.rdb.Keys(ctx, "*").Result()
				require.NoError(t, err)
				projection := buildSchedulerMetadataAccount(second)
				if invalidMarker {
					projection.SchedulerMetadata.Version = 0
				}
				full, metadata, err := marshalSchedulerCacheAccount(projection)
				require.ErrorContains(t, err, "metadata")
				require.Nil(t, full)
				require.Nil(t, metadata)
				reprojected := buildSchedulerMetadataAccount(projection)
				require.False(t, service.ValidSchedulerMetadataIdentity(&reprojected), "partial fields must never be re-signed")
				first.Name = "must-not-be-written"
				batch := []service.Account{first, projection}
				switch writer {
				case "SetAccount":
					err = cache.SetAccount(ctx, &projection)
				case "writeAccountIDs":
					var ids []int64
					ids, err = cache.writeAccountIDs(ctx, batch)
					require.Nil(t, ids)
				case "SetSnapshot":
					err = cache.SetSnapshot(ctx, bucket, token, batch)
				case "SetSnapshotAndReturnAccountIDs":
					var ids []int64
					ids, err = cache.SetSnapshotAndReturnAccountIDs(ctx, bucket, token, batch)
					require.Nil(t, ids)
				}
				require.ErrorContains(t, err, "metadata")
				after, err := cache.rdb.MGet(ctx, keys...).Result()
				require.NoError(t, err)
				require.Equal(t, before, after, "rejected input must neither overwrite nor delete existing caches or advance versions")
				afterKeys, err := cache.rdb.Keys(ctx, "*").Result()
				require.NoError(t, err)
				require.ElementsMatch(t, beforeKeys, afterKeys)
			})
		}
	}
}

func TestSchedulerCacheManagedIdentityProjectionInFullBlobForcesDBFallback(t *testing.T) {
	for _, invalidMarker := range []bool{false, true} {
		t.Run(strconv.FormatBool(invalidMarker), func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			account := schedulerManagedIdentityFixture(15523, service.PlatformOpenAI)
			projection := buildSchedulerMetadataAccount(account)
			if invalidMarker {
				projection.SchedulerMetadata.IdentityFingerprint = ""
			}
			encoded, err := json.Marshal(projection)
			require.NoError(t, err)
			require.NoError(t, cache.rdb.Set(ctx, schedulerAccountKey(strconv.FormatInt(account.ID, 10)), encoded, 0).Err())
			cached, err := cache.GetAccount(ctx, account.ID)
			require.NoError(t, err)
			require.Nil(t, cached, "even a valid projection digest cannot turn it into a full forwarding account")
			repo := &schedulerManagedIdentityFallbackRepo{accounts: []service.Account{account}}
			svc := service.NewSchedulerSnapshotService(cache, nil, repo, nil, nil)
			full, err := svc.GetAccount(ctx, account.ID)
			require.NoError(t, err)
			require.NotNil(t, full)
			require.Equal(t, 1, repo.fullReads)
			require.Nil(t, full.SchedulerMetadata)
			require.Contains(t, full.Credentials, "header_overrides")
			require.Equal(t, service.ManagedModelAccountFingerprint(&account), service.ManagedModelAccountFingerprint(full))
		})
	}
}

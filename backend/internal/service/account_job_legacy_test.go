//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func legacyCodexJobFixture(t *testing.T, target int64, fields map[string]json.RawMessage) (json.RawMessage, json.RawMessage, AccountJobItem) {
	t.Helper()
	canonical, err := json.Marshal(fields)
	require.NoError(t, err)
	digest := sha256.Sum256(append([]byte(strconv.FormatInt(target, 10)+"\x00"), canonical...))
	action := hex.EncodeToString(digest[:])
	raw, err := json.Marshal(PluginJobPayload{PluginID: 7, Operation: "harvest", Items: map[string]json.RawMessage{action: canonical}})
	require.NoError(t, err)
	return json.RawMessage(`{"plugin_id":7,"plugin_generation":3}`), raw, AccountJobItem{ID: 1, Action: action, TargetAccountID: &target}
}

func TestLegacyCodexAccountJobRequiresCompleteActionAndFrozenTarget(t *testing.T) {
	metadata, payload, item := legacyCodexJobFixture(t, 41, map[string]json.RawMessage{"model": json.RawMessage(`"gpt-6-astra"`), "force": json.RawMessage(`true`)})
	intent, err := DecodeLegacyCodexAccountJob(metadata, payload, item)
	require.NoError(t, err)
	require.Equal(t, 41, int(intent.AccountID))
	require.Equal(t, "gpt-6-astra", intent.Model)
	require.True(t, intent.Force)
	require.Len(t, item.Action, 64)
	t.Run("changed_target", func(t *testing.T) {
		other := item
		id := int64(99)
		other.TargetAccountID = &id
		_, err := DecodeLegacyCodexAccountJob(metadata, payload, other)
		require.ErrorIs(t, err, ErrAccountJobInvalidMetadata)
	})
	t.Run("missing_target", func(t *testing.T) {
		other := item
		other.TargetAccountID = nil
		_, err := DecodeLegacyCodexAccountJob(metadata, payload, other)
		require.ErrorIs(t, err, ErrAccountJobInvalidMetadata)
	})
	t.Run("truncated_action", func(t *testing.T) {
		other := item
		other.Action = other.Action[:48]
		_, err := DecodeLegacyCodexAccountJob(metadata, payload, other)
		require.ErrorIs(t, err, ErrAccountJobInvalidMetadata)
	})
	t.Run("changed_payload", func(t *testing.T) {
		changed := json.RawMessage(strings.Replace(string(payload), "gpt-6-astra", "gpt-6-sol", 1))
		_, err := DecodeLegacyCodexAccountJob(metadata, changed, item)
		require.ErrorIs(t, err, ErrAccountJobInvalidMetadata)
	})
	t.Run("different_source", func(t *testing.T) {
		_, err := DecodeLegacyCodexAccountJob([]byte(`{"plugin_id":8,"plugin_generation":3}`), payload, item)
		require.ErrorIs(t, err, ErrAccountJobInvalidMetadata)
	})
	t.Run("contradictory_account", func(t *testing.T) {
		meta, raw, entry := legacyCodexJobFixture(t, 41, map[string]json.RawMessage{"model": json.RawMessage(`"gpt-6-astra"`), "account_id": json.RawMessage(`99`)})
		_, err := DecodeLegacyCodexAccountJob(meta, raw, entry)
		require.ErrorIs(t, err, ErrAccountJobInvalidMetadata)
	})
	t.Run("unknown_operation", func(t *testing.T) {
		raw := json.RawMessage(strings.Replace(string(payload), `"harvest"`, `"proxy.test"`, 1))
		_, err := DecodeLegacyCodexAccountJob(metadata, raw, item)
		require.ErrorIs(t, err, ErrLegacyAccountJobUnsupported)
	})
	t.Run("stop_same_single_model", func(t *testing.T) {
		raw := json.RawMessage(strings.Replace(string(payload), `"harvest"`, `"stop"`, 1))
		got, err := DecodeLegacyCodexAccountJob(metadata, raw, item)
		require.NoError(t, err)
		require.Equal(t, "stop", got.Operation)
		require.Equal(t, intent.Model, got.Model)
	})
}

func TestAccountJobLegacyRetryPreservesActorCipherExpiryAndAction64(t *testing.T) {
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	metadata, payload, item := legacyCodexJobFixture(t, 41, map[string]json.RawMessage{"model": json.RawMessage(`"gpt-6-astra"`)})
	seed := AccountJobItemSeed{Ordinal: 1, Action: item.Action, TargetAccountID: item.TargetAccountID, Metadata: json.RawMessage(`{}`)}
	job, _, err := jobs.Submit(context.Background(), 9, AccountJobKindExtensionOperation, "legacy-original", payload, metadata, []AccountJobItemSeed{seed})
	require.NoError(t, err)
	repo.jobs[job.ID].Status = AccountJobStatusFailed
	repo.items[job.ID][0].Status = AccountJobItemStatusFailed
	original := repo.payloads[job.ID]
	_, _, err = jobs.RetryFailed(context.Background(), job.ID, 99, "wrong-actor")
	require.ErrorIs(t, err, ErrAccountJobNotFound)
	retry, replayed, err := jobs.RetryFailed(context.Background(), job.ID, 9, "native-retry")
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, original, repo.payloads[retry.ID])
	require.Equal(t, item.Action, repo.items[retry.ID][0].Action)
	require.Equal(t, int64(41), *repo.items[retry.ID][0].TargetAccountID)
	owner, err := AccountJobPluginExecution(retry.Metadata)
	require.NoError(t, err)
	require.Equal(t, AccountJobLegacyOwner{ID: 7, Generation: 3}, owner)
	same, replayed, err := jobs.RetryFailed(context.Background(), job.ID, 9, "native-retry")
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, retry.ID, same.ID)
	require.Len(t, repo.jobs, 2)
}

func TestAccountJobLegacyViewComparesSavedDigestWithoutQueryingTargets(t *testing.T) {
	identity := extensionv1.AccountViewIdentityV1{Version: 1, PluginID: 7, PluginKey: "codexrip.cindy-provider", ViewID: "cindy-accounts", PresetID: "cindy", PackageSHA256: strings.Repeat("a", 64), ViewDefinitionDigest: strings.Repeat("b", 64)}
	query := extensionv1.AccountViewQueryV1{Search: "saved-private-query", AccountIDs: []int64{2, 7}}
	record := AccountJobViewMetadata{AccountViewIdentityV1: identity, RuntimeGeneration: 3, PolicyRevision: 4, NormalizedQueryDigest: accountViewQueryDigest(query)}
	metadata, err := json.Marshal(map[string]any{"plugin_id": 7, "plugin_generation": 3, "account_view": record})
	require.NoError(t, err)
	payload, err := json.Marshal(map[string]any{"account_ids": []int64{2, 7}, "view_context": extensionv1.AccountViewContextV1{AccountViewIdentityV1: identity, Query: query}})
	require.NoError(t, err)
	job := &AccountJob{ID: 1, Kind: AccountJobKindBulkUpdate, Metadata: metadata}
	require.NoError(t, ValidateRecordedAccountJob(job, payload))
	require.NotContains(t, string(metadata), query.Search)
	changed := json.RawMessage(strings.Replace(string(payload), query.Search, "different-query", 1))
	require.ErrorIs(t, ValidateRecordedAccountJob(job, changed), ErrAccountJobInvalidMetadata)
	target := int64(2)
	require.NoError(t, ValidateRecordedAccountJobTargets(job, payload, []AccountJobItem{{TargetAccountID: &target}}))
	require.Error(t, ValidateRecordedAccountJobTargets(job, payload, []AccountJobItem{{}}))
}

func TestAccountJobLegacyDuplicateTargetsKeepLosers(t *testing.T) {
	ids, err := accountViewJobTargets(AccountJobKindDuplicateReview, json.RawMessage(`{"account_ids":[2,3]}`), []*int64{nil})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, ids)
	id := int64(2)
	ids, err = accountViewJobTargets(AccountJobKindDuplicateMerge, json.RawMessage(`{"survivor_account_id":2,"loser_account_ids":[3]}`), []*int64{&id})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, ids)
	_, err = accountViewJobTargets(AccountJobKindDuplicateReview, json.RawMessage(`{}`), []*int64{nil})
	require.Error(t, err)
	_, err = accountViewJobTargets(AccountJobKindDuplicateMerge, json.RawMessage(`{"survivor_account_id":2,"loser_account_ids":[-1]}`), []*int64{&id})
	require.Error(t, err)
}

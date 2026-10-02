//go:build unit

package service

import (
	"encoding/json"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func TestAccountJobLegacyViewComparesSavedDigestWithoutQueryingTargets(t *testing.T) {
	identity := extensionv1.AccountViewIdentityV1{Version: 1, PluginID: 7, PluginKey: "codexrip.account-tools", ViewID: "legacy-accounts", PresetID: "openai", PackageSHA256: strings.Repeat("a", 64), ViewDefinitionDigest: strings.Repeat("b", 64)}
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

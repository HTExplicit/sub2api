//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// These ledger fixtures execute the real PostgreSQL projection without starting
// a worker, discovering an upstream catalog, or sending a model request.
func TestAccountCapabilityCatalogEvidenceIntegration(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	prefix := fmt.Sprintf("capability-catalog-evidence-%d", time.Now().UnixNano())
	var actorID, accountID int64
	var runIDs []int64
	t.Cleanup(func() {
		for _, id := range runIDs {
			_, err := integrationDB.ExecContext(ctx, `DELETE FROM admin_capability_items WHERE run_id=$1`, id)
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, `DELETE FROM admin_capability_runs WHERE id=$1`, id)
			require.NoError(t, err)
		}
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, accountID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, actorID)
		require.NoError(t, err)
	})
	actorID = mustCreateUser(t, client, &service.User{Email: prefix + "@example.invalid"}).ID
	account := mustCreateAccount(t, client, &service.Account{Name: prefix, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	accountID = account.ID
	repo := &accountCapabilityRepository{db: integrationDB}
	baseTime := time.Now().UTC().Truncate(time.Microsecond)
	seed := func(t *testing.T, label, kind, model, protocol, profile, status, result string) int64 {
		t.Helper()
		run, replayed, err := repo.Create(ctx, &service.AccountCapabilityRun{
			CreatedBy: actorID, Kind: kind, IdempotencyKey: prefix + "-" + label,
			RequestHash: strings.Repeat("a", 64), FolderIDs: []int64{7}, AccountIDs: []int64{accountID},
		}, []service.AccountCapabilityItem{{
			Ordinal: 1, AccountID: accountID, AccountName: account.Name, FolderID: 7,
			ConfigFingerprint: strings.Repeat("b", 64), UpstreamModel: model, Protocol: protocol, Profile: profile,
		}})
		require.NoError(t, err)
		require.False(t, replayed)
		runIDs = append(runIDs, run.ID)
		finishedAt := baseTime.Add(time.Duration(len(runIDs)) * time.Second)
		var id int64
		err = integrationDB.QueryRowContext(ctx, `UPDATE admin_capability_items
 SET status=$2,result=$3::jsonb,request_count=1,finished_at=$4 WHERE run_id=$1 RETURNING id`,
			run.ID, status, result, finishedAt).Scan(&id)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `UPDATE admin_capability_runs SET status='completed',finished_at=$2 WHERE id=$1`, run.ID, finishedAt)
		require.NoError(t, err)
		return id
	}

	t.Run("smaller_nonempty_directory_keeps_original_observations", func(t *testing.T) {
		oldResult := `{"status":"discovered","source":"upstream","models":[{"id":"claude-fable-5"},{"id":"claude-fable-5-1"}]}`
		newResult := `{"status":"discovered","source":"upstream","models":[{"id":"claude-fable-5-1"}]}`
		oldID := seed(t, "old-directory", "discover", "", "", "text", "succeeded", oldResult)
		newID := seed(t, "new-directory", "discover", "", "", "text", "succeeded", newResult)
		page, err := repo.EvidenceItems(ctx, service.AccountCapabilityFilter{Kind: "discover", AccountID: accountID, FolderIDs: []int64{7}})
		require.NoError(t, err)
		require.EqualValues(t, 2, page.Total, "one original observation may supply several historical model declarations")
		require.Len(t, page.Items, 2)
		require.Equal(t, newID, page.Items[0].ID)
		require.Equal(t, oldID, page.Items[1].ID)
		require.JSONEq(t, newResult, string(page.Items[0].Result), "the current directory must not acquire the historical Fable 5 declaration")
		require.JSONEq(t, oldResult, string(page.Items[1].Result), "historical directory evidence stays immutable")
	})

	t.Run("cancellation_with_json_request_count_is_not_known_unsent", func(t *testing.T) {
		result := `{"status":"canceled","request_count":1}`
		id := seed(t, "possibly-sent-cancellation", "probe", "canceled-request-fixture", "responses", "text", "canceled", result)
		_, err := integrationDB.ExecContext(ctx, `UPDATE admin_capability_items SET request_count=0,dispatched_at=NULL WHERE id=$1`, id)
		require.NoError(t, err)
		page, err := repo.EvidenceItems(ctx, service.AccountCapabilityFilter{Kind: "probe", AccountID: accountID, Model: "canceled-request-fixture"})
		require.NoError(t, err)
		require.EqualValues(t, 1, page.Total)
		require.Len(t, page.Items, 1)
		require.Equal(t, id, page.Items[0].ID)
		require.Zero(t, page.Items[0].RequestCount)
		require.Nil(t, page.Items[0].DispatchedAt)
		require.JSONEq(t, result, string(page.Items[0].Result), "a durable JSON send count must keep the observation visible")
	})

	t.Run("tool_and_token_failures_cannot_revoke_basic_success", func(t *testing.T) {
		aliveID := seed(t, "basic-alive", "probe", "claude-fable-5", "responses", "text", "succeeded", `{"status":"alive","classification":"text_completed"}`)
		for _, tc := range []struct{ name, protocol, profile string }{
			{"tool-roundtrip", "responses", "tool_roundtrip"},
			{"responses-count", "responses_input_tokens", "text"},
			{"messages-count", "messages_count_tokens", "text"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				failedID := seed(t, tc.name, "probe", "claude-fable-5", tc.protocol, tc.profile, "failed", `{"status":"failed","classification":"credential_invalid","account_failure":true}`)
				page, err := repo.EvidenceItems(ctx, service.AccountCapabilityFilter{Kind: "probe", AccountID: accountID, FolderIDs: []int64{7}})
				require.NoError(t, err)
				byID := make(map[int64]service.AccountCapabilityItem, len(page.Items))
				for _, item := range page.Items {
					byID[item.ID] = item
				}
				require.Contains(t, byID, failedID, "the isolated failure remains visible")
				require.Contains(t, byID, aliveID)
				require.Equal(t, "succeeded", byID[aliveID].Status)
				require.False(t, byID[aliveID].PublicationSuperseded, "tool/count account_failure flags cannot invalidate basic generation evidence")
			})
		}
	})
}

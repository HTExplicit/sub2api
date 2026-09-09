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

// Exercise OnlyUntested through real transactional Create calls. All outcomes
// are explicit SQL fixtures; no worker, network request, or model is started.
func TestAccountCapabilityJobsEvidenceGuardIntegration(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	prefix := fmt.Sprintf("capability-evidence-guard-%d", time.Now().UnixNano())
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
	create := func(t *testing.T, label, kind, model, protocol, profile string, onlyUntested bool) (*service.AccountCapabilityRun, error) {
		t.Helper()
		run, replayed, err := repo.Create(ctx, &service.AccountCapabilityRun{
			CreatedBy: actorID, Kind: kind, IdempotencyKey: prefix + "-" + label,
			RequestHash: strings.Repeat("a", 64), OnlyUntested: onlyUntested,
			FolderIDs: []int64{7}, AccountIDs: []int64{accountID},
		}, []service.AccountCapabilityItem{{
			Ordinal: 1, AccountID: accountID, AccountName: account.Name, FolderID: 7,
			ConfigFingerprint: strings.Repeat("b", 64), UpstreamModel: model, Protocol: protocol, Profile: profile,
		}})
		if run != nil {
			runIDs = append(runIDs, run.ID)
		}
		require.False(t, replayed)
		return run, err
	}
	seed := func(t *testing.T, label, kind, model, protocol, profile, status, result string) int64 {
		t.Helper()
		run, err := create(t, label, kind, model, protocol, profile, false)
		require.NoError(t, err, "manual diagnostics remain available despite an account-wide failure")
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
	accountFailure := `{"status":"failed","source":"upstream","classification":"credential_invalid","account_failure":true}`
	for _, tc := range []struct{ name, kind, model, protocol string }{
		{"basic", "probe", "failed-base-model", "responses"},
		{"discovery", "discover", "", ""},
	} {
		t.Run(tc.name+"_failure_blocks_new_target_until_directory_recovery", func(t *testing.T) {
			failedID := seed(t, tc.name+"-failure", tc.kind, tc.model, tc.protocol, "text", "failed", accountFailure)
			target := "new-target-" + tc.name
			blocked, err := create(t, tc.name+"-blocked", "probe", target, "responses", "text", true)
			require.ErrorIs(t, err, service.ErrAccountCapabilityAlreadyAttempted)
			require.Nil(t, blocked, "a different model cannot bypass a current failure in the same account/folder/configuration")
			seed(t, tc.name+"-manual-diagnostic", "probe", "manual-diagnostic-"+tc.name, "responses", "text", "failed", `{"status":"failed","classification":"upstream_unavailable"}`)
			stillBlocked, err := create(t, tc.name+"-still-blocked", "probe", target, "responses", "text", true)
			require.ErrorIs(t, err, service.ErrAccountCapabilityAlreadyAttempted)
			require.Nil(t, stillBlocked, "an inconclusive manual diagnostic does not retire the credential failure")
			seed(t, tc.name+"-directory-recovery", "discover", "", "", "text", "succeeded", `{"status":"discovered","source":"upstream","models":[{"id":"claude-fable-5"}]}`)
			observations, err := repo.GetItemsByIDs(ctx, []int64{failedID})
			require.NoError(t, err)
			require.Len(t, observations, 1)
			require.True(t, observations[0].PublicationSuperseded, "the later real directory response retires the account-wide failure")
			allowed, err := create(t, tc.name+"-after-recovery", "probe", target, "responses", "text", true)
			require.NoError(t, err)
			require.NotNil(t, allowed, "the blocked target was never inserted and remains untested")
		})
	}
	for _, tc := range []struct{ name, protocol, profile string }{
		{"tools", "responses", "tool_roundtrip"},
		{"responses-count", "responses_input_tokens", "text"},
		{"messages-count", "messages_count_tokens", "text"},
	} {
		t.Run(tc.name+"_failure_does_not_block_a_new_target", func(t *testing.T) {
			seed(t, tc.name+"-failure", "probe", "isolated-failure-"+tc.name, tc.protocol, tc.profile, "failed", accountFailure)
			allowed, err := create(t, tc.name+"-allowed", "probe", "new-target-"+tc.name, "responses", "text", true)
			require.NoError(t, err, "tool/token account_failure must not become an account-wide probe veto")
			require.NotNil(t, allowed)
		})
	}
}

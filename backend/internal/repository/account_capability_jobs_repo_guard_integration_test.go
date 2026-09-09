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

// Two browser plans must not reserve different interfaces of the same target
// before either has a result. This exercises PostgreSQL's actual queue lock and
// conflict predicate only; all observations are fixtures, never model requests.
func TestAccountCapabilityRepositoryGuardConcurrentInterfacesIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := testEntClient(t)
	actor := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("capability-guard-%d@example.invalid", time.Now().UnixNano())})
	account := mustCreateAccount(t, client, &service.Account{Name: "capability-guard", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	repo := &accountCapabilityRepository{db: integrationDB}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, err := integrationDB.ExecContext(cleanupCtx, `DELETE FROM admin_capability_items WHERE run_id IN (SELECT id FROM admin_capability_runs WHERE created_by=$1)`, actor.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanupCtx, `DELETE FROM admin_capability_runs WHERE created_by=$1`, actor.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanupCtx, `DELETE FROM users WHERE id=$1`, actor.ID)
		require.NoError(t, err)
	})
	create := func(key, model, protocol string) (*service.AccountCapabilityRun, bool, error) {
		return repo.Create(ctx, &service.AccountCapabilityRun{
			CreatedBy: actor.ID, Kind: service.AccountCapabilityKindProbe, IdempotencyKey: key,
			RequestHash: strings.Repeat("a", 64), OnlyUntested: true,
			FolderIDs: []int64{7}, AccountIDs: []int64{account.ID},
		}, []service.AccountCapabilityItem{{
			Ordinal: 1, AccountID: account.ID, AccountName: account.Name, FolderID: 7,
			ConfigFingerprint: strings.Repeat("b", 64), UpstreamModel: model,
			Protocol: protocol, Profile: "text",
		}})
	}
	type creation struct {
		run      *service.AccountCapabilityRun
		protocol string
		replayed bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan creation, 2)
	for _, protocol := range []string{"responses", "chat_completions"} {
		go func(protocol string) {
			<-start
			run, replayed, err := create("parallel-"+protocol, "claude-fable-5", protocol)
			results <- creation{run: run, protocol: protocol, replayed: replayed, err: err}
		}(protocol)
	}
	close(start)
	var accepted, rejected []creation
	for range 2 {
		result := <-results
		if result.err == nil {
			accepted = append(accepted, result)
		} else {
			rejected = append(rejected, result)
		}
	}
	require.Len(t, accepted, 1, "different interface plans share one account/model/config reservation")
	require.Len(t, rejected, 1)
	require.False(t, accepted[0].replayed)
	require.ErrorIs(t, rejected[0].err, service.ErrAccountCapabilityAlreadyAttempted)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_capability_items WHERE account_id=$1`, account.ID).Scan(&count))
	require.Equal(t, 1, count, "a rejected plan must not leave a run item behind")

	// After the first interface definitively fails, the previously untested
	// interface can be considered once. The original failed pair stays blocked.
	_, err := integrationDB.ExecContext(ctx, `UPDATE admin_capability_items SET status='failed',result='{"status":"unsupported","classification":"protocol_unsupported","request_count":1}'::jsonb,request_count=1,finished_at=NOW() WHERE run_id=$1`, accepted[0].run.ID)
	require.NoError(t, err)
	_, _, err = create("repeat-failed-interface", "claude-fable-5", accepted[0].protocol)
	require.ErrorIs(t, err, service.ErrAccountCapabilityAlreadyAttempted)
	fallback, replayed, err := create("new-fallback-interface", "claude-fable-5", rejected[0].protocol)
	require.NoError(t, err)
	require.False(t, replayed)

	// Repeated new batches cannot turn the one allowed fallback into a third
	// paid interface after two failures. This is per-target, not a batch cap.
	_, err = integrationDB.ExecContext(ctx, `UPDATE admin_capability_items SET status='failed',result='{"status":"failed","classification":"upstream_unavailable","request_count":1}'::jsonb,request_count=1,finished_at=NOW() WHERE run_id=$1`, fallback.ID)
	require.NoError(t, err)
	_, _, err = create("forbidden-third-interface", "claude-fable-5", "messages")
	require.ErrorIs(t, err, service.ErrAccountCapabilityAlreadyAttempted)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_capability_items WHERE account_id=$1`, account.ID).Scan(&count))
	require.Equal(t, 2, count)

	// A different target remains eligible, but after its first basic success
	// it cannot receive even one unnecessary automatic protocol-expansion call.
	verified, _, err := create("different-target", "claude-fable-5-1", "responses")
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE admin_capability_items SET status='succeeded',result='{"status":"alive","request_count":1}'::jsonb,request_count=1,finished_at=NOW() WHERE run_id=$1`, verified.ID)
	require.NoError(t, err)
	_, _, err = create("unnecessary-success-expansion", "claude-fable-5-1", "messages")
	require.ErrorIs(t, err, service.ErrAccountCapabilityAlreadyAttempted)
}

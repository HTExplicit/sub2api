//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newTicketIntegrationAccount(t *testing.T) (*accountRepository, *service.Account, time.Time) {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{Name: t.Name(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Credentials: map[string]any{"access_token": "fixture", "chatgpt_account_id": "principal-one"}, Extra: map[string]any{"model_context_windows": map[string]any{"gpt-5.6-sol": 200000}}})
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID) })
	return newAccountRepositoryWithSQL(client, integrationDB, nil), account, time.Now().UTC().Truncate(time.Second)
}

func successfulTicket(a *service.Account, model string, now time.Time) *service.CodexTicketRecord {
	return &service.CodexTicketRecord{AccountID: a.ID, Model: model, Identity: service.CodexTicketAccountIdentity(a), State: "gAAAAA" + strings.Repeat("B", 286), Length: 292, CapturedAt: now, ExpiresAt: now.Add(time.Hour)}
}

func TestCodexTicketRepositoryClaimsAtomicTicketsAndStoppedImport(t *testing.T) {
	ctx := context.Background()
	r, a, now := newTicketIntegrationAccount(t)
	model := "gpt-5.6-sol"
	due, err := r.DueCodexTickets(ctx, []string{model}, now, 5)
	require.NoError(t, err)
	for _, item := range due {
		require.NotEqual(t, a.ID, item.AccountID)
	}
	claim, err := r.ClaimCodexTicket(ctx, a.ID, model, "manual-one", 0, true, false, now)
	require.NoError(t, err)
	_, err = r.ClaimCodexTicket(ctx, a.ID, model, "manual-two", 0, true, false, now)
	require.ErrorIs(t, err, service.ErrCodexTicketBusy)
	ticket := successfulTicket(a, model, now)
	applied, err := r.FinishCodexTicket(ctx, claim, ticket, service.CodexTicketResult{Success: true}, now)
	require.NoError(t, err)
	require.True(t, applied)
	loaded, err := r.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Contains(t, loaded.Extra, "model_context_windows")
	require.Contains(t, loaded.Extra, "codex_turn_ticket:"+model)
	_, err = r.ClaimCodexTicket(ctx, a.ID, model, "manual-two", 0, true, false, now)
	require.ErrorIs(t, err, service.ErrCodexTicketValid)
	forced, err := r.ClaimCodexTicket(ctx, a.ID, model, "manual-force", 0, true, true, now)
	require.NoError(t, err)
	applied, err = r.FinishCodexTicket(ctx, forced, nil, service.CodexTicketFailure("ticket_length"), now)
	require.NoError(t, err)
	require.False(t, applied)
	loaded, err = r.GetByID(ctx, a.ID)
	require.NoError(t, err)
	raw, _ := json.Marshal(loaded.Extra["codex_turn_ticket:"+model])
	require.Contains(t, string(raw), ticket.State)
	require.NoError(t, r.StopCodexTicket(ctx, a.ID, []string{model}, now))
	require.NoError(t, r.SeedCodexTicketRenewals(ctx, []string{model}, 292, now))
	var phase string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT phase FROM openai_codex_ticket_runtime WHERE account_id=$1 AND model=$2`, a.ID, model).Scan(&phase))
	require.Equal(t, "stopped", phase)
}

func TestCodexTicketRepositoryConcurrentLeaseAndAccountInvalidation(t *testing.T) {
	ctx := context.Background()
	r, a, now := newTicketIntegrationAccount(t)
	model := "gpt-6-astra"
	results := make(chan *service.CodexTicketLifecycle, 2)
	var wg sync.WaitGroup
	for _, op := range []string{"one", "two"} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			claim, err := r.ClaimCodexTicket(ctx, a.ID, model, op, 0, true, false, now)
			if err == nil {
				results <- claim
			} else {
				require.ErrorIs(t, err, service.ErrCodexTicketBusy)
			}
		}(op)
	}
	wg.Wait()
	close(results)
	var claims []*service.CodexTicketLifecycle
	for c := range results {
		claims = append(claims, c)
	}
	require.Len(t, claims, 1)
	// OAuth refresh preserves the owner and active claim.
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=credentials||'{"access_token":"refreshed"}'::jsonb WHERE id=$1`, a.ID)
	require.NoError(t, err)
	var lease string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT lease_id FROM openai_codex_ticket_runtime WHERE account_id=$1 AND model=$2`, a.ID, model).Scan(&lease))
	require.Equal(t, claims[0].LeaseID, lease)
	// Disable and re-enable before IO completes must still reject its old result.
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET status='disabled' WHERE id=$1`, a.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET status='active' WHERE id=$1`, a.ID)
	require.NoError(t, err)
	applied, err := r.FinishCodexTicket(ctx, claims[0], successfulTicket(a, model, now), service.CodexTicketResult{Success: true}, now)
	require.NoError(t, err)
	require.False(t, applied)
}

func TestCodexTicketRepositoryInterruptedRenewalIsConsumed(t *testing.T) {
	ctx := context.Background()
	r, a, now := newTicketIntegrationAccount(t)
	model := "gpt-5.6-sol"
	claim, err := r.ClaimCodexTicket(ctx, a.ID, model, "manual", 0, true, false, now)
	require.NoError(t, err)
	ticket := successfulTicket(a, model, now)
	_, err = r.FinishCodexTicket(ctx, claim, ticket, service.CodexTicketResult{Success: true}, now)
	require.NoError(t, err)
	before := ticket.ExpiresAt.Add(-time.Minute)
	pre, err := r.ClaimCodexTicket(ctx, a.ID, model, "renewal", 0, false, false, before)
	require.NoError(t, err)
	require.Equal(t, "pre_running", pre.Phase)
	// Process crashed. Expired lease consumes the pre-stage; it cannot replay it.
	_, err = r.ClaimCodexTicket(ctx, a.ID, model, "renewal", 0, false, false, before.Add(46*time.Second))
	require.ErrorIs(t, err, service.ErrCodexTicketNotDue)
	post, err := r.ClaimCodexTicket(ctx, a.ID, model, "renewal", 0, false, false, ticket.ExpiresAt.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, "post_running", post.Phase)
	_, err = r.ClaimCodexTicket(ctx, a.ID, model, "renewal", 0, false, false, ticket.ExpiresAt.Add(2*time.Minute))
	require.ErrorIs(t, err, service.ErrCodexTicketNotDue)
	var phase string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT phase FROM openai_codex_ticket_runtime WHERE account_id=$1 AND model=$2`, a.ID, model).Scan(&phase))
	require.Equal(t, "stopped", phase)
}

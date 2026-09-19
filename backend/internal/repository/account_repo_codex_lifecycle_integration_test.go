//go:build integration

package repository

import (
	"context"
	"database/sql"
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
	var oldSetting string
	settingErr := integrationDB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='openai_codex_ticket_enabled'`).Scan(&oldSetting)
	require.True(t, settingErr == nil || settingErr == sql.ErrNoRows)
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('openai_codex_ticket_enabled','true') ON CONFLICT(key) DO UPDATE SET value='true'`)
	require.NoError(t, err)
	t.Cleanup(func() {
		if settingErr == sql.ErrNoRows {
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM settings WHERE key='openai_codex_ticket_enabled'`)
		} else {
			_, _ = integrationDB.ExecContext(ctx, `UPDATE settings SET value=$1 WHERE key='openai_codex_ticket_enabled'`, oldSetting)
		}
	})
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
	// A legacy ticket without an identity stamp cannot survive changing owner.
	raw, _ := json.Marshal(map[string]any{"codex_turn_ticket:" + model: map[string]any{"state": "gAAAAA" + strings.Repeat("B", 286), "length": 292, "expires_at": now.Add(time.Hour)}})
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=extra||$2::jsonb WHERE id=$1`, a.ID, string(raw))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=credentials||'{"chatgpt_account_id":"principal-two"}'::jsonb WHERE id=$1`, a.ID)
	require.NoError(t, err)
	loaded, err := r.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NotContains(t, loaded.Extra, "codex_turn_ticket:"+model)
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

type codexTicketSnapshotRecorder struct {
	service.SchedulerCache
	mu         sync.Mutex
	accountIDs []int64
}

func (r *codexTicketSnapshotRecorder) SetAccount(_ context.Context, account *service.Account) error {
	if r == nil || account == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accountIDs = append(r.accountIDs, account.ID)
	return nil
}

func (r *codexTicketSnapshotRecorder) contains(accountID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.accountIDs {
		if id == accountID {
			return true
		}
	}
	return false
}

func TestCodexTicketSeedRefreshesAuthoritativeSchedulerSnapshot(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now().UTC().Truncate(time.Second)
	model := "gpt-6-astra"
	account := mustCreateAccount(t, client, &service.Account{
		Name:        t.Name(),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Credentials: map[string]any{"access_token": "seed-refresh-token", "chatgpt_account_id": "seed-refresh-principal"},
		Extra:       map[string]any{},
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID)
	})
	account.Extra["codex_turn_ticket:"+model] = successfulTicket(account, model, now)
	raw, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, account.ID, string(raw))
	require.NoError(t, err)

	recorder := &codexTicketSnapshotRecorder{}
	repo := newAccountRepositoryWithSQL(client, integrationDB, recorder)
	require.NoError(t, repo.SeedCodexTicketRenewals(ctx, []string{model}, 292, now))
	require.True(t, recorder.contains(account.ID), "startup seeding must refresh the full authoritative scheduler snapshot")
}

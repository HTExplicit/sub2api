//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRepositorySuccessfulTestRecoveryAtomic(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	a := mustCreateAccount(t, tx.Client(), &service.Account{Name: "connection recovery", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}})
	_, err := tx.ExecContext(ctx, `UPDATE accounts SET status='error',error_message='recoverable',schedulable=false,rate_limited_at=NOW(),rate_limit_reset_at=NOW()+INTERVAL '1 hour',overload_until=NOW()+INTERVAL '1 hour',temp_unschedulable_until=NOW()+INTERVAL '1 hour',temp_unschedulable_reason='transient',extra='{"keep":true,"model_rate_limits":{"m":1},"antigravity_quota_scopes":{"g":1}}'::jsonb WHERE id=$1`, a.ID)
	require.NoError(t, err)
	result, err := repo.RecoverAfterSuccessfulTest(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, result.ClearedError)
	require.True(t, result.ClearedRateLimit)
	require.True(t, result.ManualStatePreserved)
	after, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusActive, after.Status)
	require.False(t, after.Schedulable)
	require.Nil(t, after.RateLimitResetAt)
	require.Nil(t, after.TempUnschedulableUntil)
	require.Nil(t, after.OverloadUntil)
	require.Equal(t, true, after.Extra["keep"])
	require.NotContains(t, after.Extra, "model_rate_limits")
	result, err = repo.RecoverAfterSuccessfulTest(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, result.ClearedError)
	require.False(t, result.ClearedRateLimit)
	// A test began with an error snapshot; the administrator disabled it before
	// that test completed. Recovery must inspect the current locked row.
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET status='error',error_message='old',schedulable=true WHERE id=$1`, a.ID)
	require.NoError(t, err)
	observed, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusError, observed.Status)
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET status='disabled',schedulable=false,error_message='manual',rate_limit_reset_at=$2 WHERE id=$1`, a.ID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	result, err = repo.RecoverAfterSuccessfulTest(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, result.ManualStatePreserved)
	require.False(t, result.ClearedError)
	require.False(t, result.ClearedRateLimit)
	after, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusDisabled, after.Status)
	require.Equal(t, "manual", after.ErrorMessage)
	require.False(t, after.Schedulable)
	require.NotNil(t, after.RateLimitResetAt)
}

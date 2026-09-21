package repository

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func transientDatabaseError(code string) error {
	return &pgconn.PgError{Code: code, Message: "database is temporarily unavailable"}
}

func TestIsTransientDatabaseInitializationError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "postgres is starting", err: transientDatabaseError("57P03"), want: true},
		{name: "connection failure", err: fmt.Errorf("wrapped: %w", transientDatabaseError("08006")), want: true},
		{name: "authentication failure", err: transientDatabaseError("28P01"), want: false},
		{name: "constraint failure", err: fmt.Errorf("migration: %w", transientDatabaseError("23514")), want: false},
		{name: "other operator intervention", err: transientDatabaseError("57P01"), want: false},
		{name: "migration error", err: errors.New("migration checksum mismatch"), want: false},
		{name: "legacy postgres is starting", err: &pq.Error{Code: "57P03"}, want: true},
		{name: "wrapped legacy connection failure", err: fmt.Errorf("wrapped: %w", &pq.Error{Code: "08006"}), want: true},
		{name: "nil", err: nil, want: false},
		{name: "typed nil pgx", err: (*pgconn.PgError)(nil), want: false},
		{name: "typed nil pq", err: (*pq.Error)(nil), want: false},
		{name: "canceled", err: context.Canceled, want: false},
		{name: "wrapped deadline", err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), want: false},
		{name: "sqlstate only in message", err: errors.New("database starting: SQLSTATE 57P03"), want: false},
		{name: "network error is not added to policy", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isTransientDatabaseInitializationError(tt.err))
		})
	}
}

func TestInitializeDatabaseWithRetryEventuallySucceeds(t *testing.T) {
	attempts := 0
	var delays []time.Duration
	err := initializeDatabaseWithRetryWithWait(context.Background(), func(context.Context) error {
		attempts++
		if attempts <= 3 {
			return transientDatabaseError("57P03")
		}
		return nil
	}, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 4, attempts)
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, delays)
}

func TestInitializeDatabaseWithRetryFailsFastForPermanentError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "authentication", err: transientDatabaseError("28P01")},
		{name: "wrapped constraint", err: fmt.Errorf("migration: %w", transientDatabaseError("23514"))},
		{name: "migration data", err: errors.New("migration checksum mismatch")},
		{name: "direct cancellation", err: context.Canceled},
		{name: "direct deadline", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			waitCalled := false
			err := initializeDatabaseWithRetryWithWait(context.Background(), func(context.Context) error {
				attempts++
				return test.err
			}, func(_ context.Context, _ time.Duration) error {
				waitCalled = true
				return nil
			})

			require.ErrorIs(t, err, test.err)
			require.Equal(t, 1, attempts)
			require.False(t, waitCalled)
		})
	}
}

func TestInitializeDatabaseWithRetryStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	err := initializeDatabaseWithRetryWithWait(ctx, func(context.Context) error {
		attempts++
		return transientDatabaseError("57P03")
	}, func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, attempts)
}

func TestInitializeDatabaseWithRetryReturnsLastErrorAfterLimit(t *testing.T) {
	attempts := 0
	lastErr := fmt.Errorf("wrapped connection: %w", transientDatabaseError("08006"))
	var delays []time.Duration
	err := initializeDatabaseWithRetryWithWait(context.Background(), func(context.Context) error {
		attempts++
		return lastErr
	}, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	})

	require.ErrorIs(t, err, lastErr)
	require.Equal(t, 8, maxDatabaseInitializationRetries)
	require.Equal(t, 9, attempts, "one initial attempt plus eight retries")
	require.Equal(t, []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second,
	}, delays)
}

func TestInitializeDatabaseWithRetryAllowsIdempotentMigrationRetry(t *testing.T) {
	applied := make(map[string]bool)
	attempts := 0
	err := initializeDatabaseWithRetryWithWait(context.Background(), func(context.Context) error {
		attempts++
		if !applied["001_init.sql"] {
			applied["001_init.sql"] = true
			return transientDatabaseError("57P03")
		}
		return nil
	}, func(_ context.Context, _ time.Duration) error { return nil })

	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	require.True(t, applied["001_init.sql"])
}

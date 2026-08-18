//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsRepositoryBatchInsertErrorLogs(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE ops_error_logs RESTART IDENTITY")

	repo := NewOpsRepository(integrationDB).(*opsRepository)
	now := time.Now().UTC()
	inserted, err := repo.BatchInsertErrorLogs(ctx, []*service.OpsInsertErrorLogInput{
		{
			RequestID:    "batch-ops-1",
			ErrorPhase:   "upstream",
			ErrorType:    "upstream_error",
			Severity:     "error",
			StatusCode:   429,
			ErrorMessage: "rate limited",
			CreatedAt:    now,
		},
		{
			RequestID:    "batch-ops-2",
			ErrorPhase:   "internal",
			ErrorType:    "api_error",
			Severity:     "error",
			StatusCode:   500,
			ErrorMessage: "internal error",
			CreatedAt:    now.Add(time.Millisecond),
		},
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, inserted)

	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_error_logs WHERE request_id IN ('batch-ops-1', 'batch-ops-2')").Scan(&count))
	require.Equal(t, 2, count)
}

func TestPGXBatchInsertAuditAndSystemLogs(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	auditRequestID := "pgx-audit-batch"
	systemRequestID := "pgx-system-batch"
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM audit_logs WHERE request_id = $1", auditRequestID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_system_logs WHERE request_id = $1", systemRequestID)
	})

	auditRepo := NewAuditLogRepository(integrationDB)
	inserted, err := auditRepo.BatchInsert(ctx, []*service.AuditLog{
		nil,
		{
			CreatedAt:   now,
			Action:      "pgx.batch.audit",
			Method:      "POST",
			Path:        "/internal/pgx-batch",
			RequestID:   auditRequestID,
			StatusCode:  202,
			LatencyMs:   3,
			RequestBody: `{"safe":true}`,
			Extra:       map[string]any{"driver": "pgx"},
		},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, inserted)

	opsRepo := NewOpsRepository(integrationDB)
	inserted, err = opsRepo.BatchInsertSystemLogs(ctx, []*service.OpsInsertSystemLogInput{
		nil,
		{
			CreatedAt: now,
			Level:     "info",
			Component: "repository-test",
			Message:   "pgx batch insert",
			RequestID: systemRequestID,
			ExtraJSON: `{"driver":"pgx"}`,
		},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, inserted)

	var auditCount, systemCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs WHERE request_id = $1", auditRequestID).Scan(&auditCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_system_logs WHERE request_id = $1", systemRequestID).Scan(&systemCount))
	require.Equal(t, 1, auditCount)
	require.Equal(t, 1, systemCount)
}

func TestEnqueueSchedulerOutbox_DeduplicatesIdempotentEvents(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")

	accountID := int64(12345)
	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil))
	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil))

	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1", service.SchedulerOutboxEventAccountChanged).Scan(&count))
	require.Equal(t, 1, count)

	var firstID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT id FROM scheduler_outbox WHERE event_type = $1", service.SchedulerOutboxEventAccountChanged).Scan(&firstID))
	events, err := NewSchedulerOutboxRepository(integrationDB).ListAfterAndReleaseDedup(ctx, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, firstID, events[0].ID)

	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1", service.SchedulerOutboxEventAccountChanged).Scan(&count))
	require.Equal(t, 2, count)
}

func TestSchedulerOutbox_ListAfterAndReleaseDedup_AllowsSameKeyWhileEventInFlight(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")

	accountID := int64(17345)
	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil))

	events, err := NewSchedulerOutboxRepository(integrationDB).ListAfterAndReleaseDedup(ctx, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)

	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil))

	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1", service.SchedulerOutboxEventAccountChanged).Scan(&count))
	require.Equal(t, 2, count)

	var pendingKeys int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE dedup_key IS NOT NULL").Scan(&pendingKeys))
	require.Equal(t, 1, pendingKeys)
}

func TestEnqueueSchedulerOutbox_CoalescesAccountStateBurst(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")

	accountID := int64(22345)
	for range 50 {
		require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil))
	}

	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1", service.SchedulerOutboxEventAccountChanged).Scan(&count))
	t.Logf("same-account account_changed burst: calls=50 inserted=%d", count)
	require.Equal(t, 1, count)
}

func TestEnqueueSchedulerOutbox_DoesNotDeduplicateDifferentPayload(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")

	accountID := int64(32345)
	payload1 := map[string]any{"group_ids": []int64{1}}
	payload2 := map[string]any{"group_ids": []int64{2}}
	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountChanged, &accountID, nil, payload1))
	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountChanged, &accountID, nil, payload2))

	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1", service.SchedulerOutboxEventAccountChanged).Scan(&count))
	require.Equal(t, 2, count)
}

func TestEnqueueSchedulerOutbox_DoesNotDeduplicateLastUsed(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")

	accountID := int64(67890)
	payload1 := map[string]any{"last_used": map[string]int64{"67890": 100}}
	payload2 := map[string]any{"last_used": map[string]int64{"67890": 200}}
	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountLastUsed, &accountID, nil, payload1))
	require.NoError(t, enqueueSchedulerOutbox(ctx, integrationDB, service.SchedulerOutboxEventAccountLastUsed, &accountID, nil, payload2))

	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1", service.SchedulerOutboxEventAccountLastUsed).Scan(&count))
	require.Equal(t, 2, count)
}

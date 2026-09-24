package repository

import (
	"database/sql"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const ticketStateColumns = `account_id,model,identity,phase,next_at,expires_at,lease_id,lease_until,operation,job_id,manual_was_enrolled,last_attempt_at,last_result`

func scanTicketState(row interface{ Scan(...any) error }) (*service.CodexTicketLifecycle, error) {
	s := &service.CodexTicketLifecycle{}
	var next, expiry, lease, attempt sql.NullTime
	var raw []byte
	err := row.Scan(&s.AccountID, &s.Model, &s.Identity, &s.Phase, &next, &expiry, &s.LeaseID, &lease, &s.Operation, &s.JobID, &s.ManualWasEnrolled, &attempt, &raw)
	if err != nil {
		return nil, err
	}
	if next.Valid {
		s.NextAt = &next.Time
	}
	if expiry.Valid {
		s.ExpiresAt = &expiry.Time
	}
	if lease.Valid {
		s.LeaseUntil = &lease.Time
	}
	if attempt.Valid {
		s.LastAttemptAt = &attempt.Time
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s.LastResult)
	}
	return s, nil
}

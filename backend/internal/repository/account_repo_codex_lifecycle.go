package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

var _ service.CodexTicketRepository = (*accountRepository)(nil)

func (r *accountRepository) ticketTx(ctx context.Context) (*sql.Tx, error) {
	db, ok := r.sql.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return nil, errors.New("ticket transactional store unavailable")
	}
	return db.BeginTx(ctx, nil)
}

func lockTicketAccount(ctx context.Context, tx *sql.Tx, id int64) (*service.Account, error) {
	a := &service.Account{ID: id}
	var credentials, extra []byte
	err := tx.QueryRowContext(ctx, `SELECT platform,type,status,credentials,extra,parent_account_id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&a.Platform, &a.Type, &a.Status, &credentials, &extra, &a.ParentAccountID)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(credentials, &a.Credentials) != nil {
		return nil, service.ErrCodexTicketInactive
	}
	_ = json.Unmarshal(extra, &a.Extra)
	return a, nil
}

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

func ticketFromAccount(a *service.Account, model string) *service.CodexTicketRecord {
	if a == nil {
		return nil
	}
	raw, err := json.Marshal(a.Extra["codex_turn_ticket:"+model])
	if err != nil {
		return nil
	}
	var t service.CodexTicketRecord
	if json.Unmarshal(raw, &t) != nil || t.State == "" {
		return nil
	}
	return &t
}

func validStoredTicket(a *service.Account, model string, length int, now time.Time) *service.CodexTicketRecord {
	t := ticketFromAccount(a, model)
	if t == nil || len(t.State) != length || len(t.State) < 6 || t.State[:6] != "gAAAAA" || !now.Before(t.ExpiresAt) {
		return nil
	}
	if t.Identity != "" && t.Identity != service.CodexTicketAccountIdentity(a) {
		return nil
	}
	return t
}

func saveTicketState(ctx context.Context, tx *sql.Tx, s *service.CodexTicketLifecycle, ticket *service.CodexTicketRecord) error {
	result, err := json.Marshal(s.LastResult)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE openai_codex_ticket_runtime SET identity=$3,phase=$4,next_at=$5,expires_at=$6,lease_id=$7,lease_until=$8,operation=$9,job_id=$10,manual_was_enrolled=$11,last_attempt_at=$12,last_result=$13::jsonb,updated_at=NOW() WHERE account_id=$1 AND model=$2`, s.AccountID, s.Model, s.Identity, s.Phase, s.NextAt, s.ExpiresAt, s.LeaseID, s.LeaseUntil, s.Operation, s.JobID, s.ManualWasEnrolled, s.LastAttemptAt, string(result))
	if err != nil {
		return err
	}
	updates := map[string]any{service.CodexTicketRuntimeExtraPrefix + s.Model: s}
	if ticket != nil {
		updates["codex_turn_ticket:"+s.Model] = ticket
	}
	raw, err := json.Marshal(updates)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=(CASE WHEN jsonb_typeof(extra)='object' THEN extra ELSE '{}'::jsonb END)||$2::jsonb WHERE id=$1`, s.AccountID, string(raw))
	return err
}

func (r *accountRepository) SeedCodexTicketRenewals(ctx context.Context, models []string, length int, now time.Time) error {
	accounts, err := r.ListByPlatform(ctx, service.PlatformOpenAI)
	if err != nil {
		return err
	}
	for i := range accounts {
		if !service.CodexTicketAccountEligible(&accounts[i]) {
			continue
		}
		for _, model := range models {
			if validStoredTicket(&accounts[i], model, length, now) == nil {
				continue
			}
			if err := r.seedCodexTicket(ctx, accounts[i].ID, model, length, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *accountRepository) seedCodexTicket(ctx context.Context, id int64, model string, length int, now time.Time) error {
	tx, err := r.ticketTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := lockTicketAccount(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	ticket := validStoredTicket(a, model, length, now)
	if ticket == nil || !service.CodexTicketAccountEligible(a) {
		return nil
	}
	identity := service.CodexTicketAccountIdentity(a)
	res, err := tx.ExecContext(ctx, `INSERT INTO openai_codex_ticket_runtime(account_id,model,identity) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, model, identity)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	ticket.Identity = identity
	s := &service.CodexTicketLifecycle{AccountID: id, Model: model, Identity: identity}
	s.Complete(now, ticket, service.CodexTicketResult{Success: true, Code: "ticket_imported", ExpiresAt: &ticket.ExpiresAt})
	if err = saveTicketState(ctx, tx, s, ticket); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *accountRepository) DueCodexTickets(ctx context.Context, models []string, now time.Time, limit int) ([]service.CodexTicketLifecycle, error) {
	if limit < 1 || limit > 5 {
		limit = 5
	}
	raw, err := json.Marshal(models)
	if err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT `+ticketStateColumns+` FROM openai_codex_ticket_runtime WHERE model IN (SELECT jsonb_array_elements_text($1::jsonb)) AND ((phase IN ('ready','retry') AND next_at<=$2) OR (phase IN ('pre_running','post_running','manual_running') AND lease_until<=$2)) ORDER BY COALESCE(next_at,lease_until),account_id LIMIT $3`, string(raw), now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.CodexTicketLifecycle
	for rows.Next() {
		s, err := scanTicketState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *accountRepository) ClaimCodexTicket(ctx context.Context, id int64, model, operation string, jobID int64, manual, force bool, now time.Time) (*service.CodexTicketLifecycle, error) {
	tx, err := r.ticketTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var disabled bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM settings WHERE key='openai_codex_ticket_enabled' AND value='false')`).Scan(&disabled); err != nil {
		return nil, err
	}
	if disabled {
		return nil, service.ErrCodexTicketDisabled
	}
	a, err := lockTicketAccount(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrCodexTicketInactive
	}
	if err != nil {
		return nil, err
	}
	identity := service.CodexTicketAccountIdentity(a)
	_, err = tx.ExecContext(ctx, `INSERT INTO openai_codex_ticket_runtime(account_id,model,identity) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, model, identity)
	if err != nil {
		return nil, err
	}
	s, err := scanTicketState(tx.QueryRowContext(ctx, `SELECT `+ticketStateColumns+` FROM openai_codex_ticket_runtime WHERE account_id=$1 AND model=$2 FOR UPDATE`, id, model))
	if err != nil {
		return nil, err
	}
	finishWithoutIO := func(reason error) (*service.CodexTicketLifecycle, error) {
		if e := saveTicketState(ctx, tx, s, nil); e != nil {
			return nil, e
		}
		if e := tx.Commit(); e != nil {
			return nil, e
		}
		return nil, reason
	}
	if !service.CodexTicketAccountEligible(a) || s.Identity != identity {
		s.Phase = "stopped"
		s.NextAt = nil
		s.LeaseID = ""
		s.LeaseUntil = nil
		if s.Identity != identity {
			s.Identity = identity
			s.ExpiresAt = nil
			delete(a.Extra, "codex_turn_ticket:"+model)
			_, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=extra-$2 WHERE id=$1`, id, "codex_turn_ticket:"+model)
			if err != nil {
				return nil, err
			}
		}
		if !manual || !service.CodexTicketAccountEligible(a) {
			return finishWithoutIO(service.ErrCodexTicketInactive)
		}
	}
	if s.LeaseUntil != nil && now.Before(*s.LeaseUntil) && s.LeaseID != "" {
		return nil, service.ErrCodexTicketBusy
	}
	if s.LeaseID != "" {
		s.Complete(now, nil, service.CodexTicketFailure("ticket_interrupted"))
	}
	if manual && s.Operation == operation {
		return finishWithoutIO(service.ErrCodexTicketAlreadyAttempted)
	}
	if manual && !force && validStoredTicket(a, model, 292, now) != nil {
		return finishWithoutIO(service.ErrCodexTicketValid)
	}
	if jobID > 0 {
		var canceled bool
		if err = tx.QueryRowContext(ctx, `SELECT cancel_requested_at IS NOT NULL OR status<>'running' FROM admin_account_jobs WHERE id=$1`, jobID).Scan(&canceled); err != nil {
			return nil, err
		}
		if canceled {
			return nil, context.Canceled
		}
	}
	if err = s.Begin(now, manual); err != nil {
		return finishWithoutIO(err)
	}
	expires := now.Add(45 * time.Second)
	s.LeaseID = uuid.NewString()
	s.LeaseUntil = &expires
	s.Operation = operation
	s.JobID = jobID
	s.LastAttemptAt = &now
	if err = saveTicketState(ctx, tx, s, nil); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s, nil
}

func (r *accountRepository) FinishCodexTicket(ctx context.Context, claim *service.CodexTicketLifecycle, ticket *service.CodexTicketRecord, result service.CodexTicketResult, now time.Time) (bool, error) {
	tx, err := r.ticketTx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := lockTicketAccount(ctx, tx, claim.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s, err := scanTicketState(tx.QueryRowContext(ctx, `SELECT `+ticketStateColumns+` FROM openai_codex_ticket_runtime WHERE account_id=$1 AND model=$2 FOR UPDATE`, claim.AccountID, claim.Model))
	if err != nil {
		return false, err
	}
	if s.LeaseID != claim.LeaseID || s.LeaseID == "" {
		return false, nil
	}
	var disabled bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM settings WHERE key='openai_codex_ticket_enabled' AND value='false')`).Scan(&disabled); err != nil {
		return false, err
	}
	canceled := false
	if s.JobID > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT cancel_requested_at IS NOT NULL OR status<>'running' FROM admin_account_jobs WHERE id=$1`, s.JobID).Scan(&canceled); err != nil {
			return false, err
		}
	}
	if canceled || disabled || !service.CodexTicketAccountEligible(a) || s.Identity != service.CodexTicketAccountIdentity(a) {
		ticket = nil
		result = service.CodexTicketFailure("ticket_stale")
		s.Phase = "stopped"
		s.NextAt = nil
		s.ManualWasEnrolled = false
	}
	s.Complete(now, ticket, result)
	if err = saveTicketState(ctx, tx, s, ticket); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	if ticket != nil {
		r.syncSchedulerAccountSnapshotDetached(ctx, a.ID)
	}
	return ticket != nil, nil
}

func (r *accountRepository) StopCodexTicket(ctx context.Context, id int64, models []string, now time.Time) error {
	tx, err := r.ticketTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := lockTicketAccount(ctx, tx, id)
	if err != nil {
		return err
	}
	for _, model := range models {
		_, err = tx.ExecContext(ctx, `INSERT INTO openai_codex_ticket_runtime(account_id,model,identity) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, model, service.CodexTicketAccountIdentity(a))
		if err != nil {
			return err
		}
		s, err := scanTicketState(tx.QueryRowContext(ctx, `SELECT `+ticketStateColumns+` FROM openai_codex_ticket_runtime WHERE account_id=$1 AND model=$2 FOR UPDATE`, id, model))
		if err != nil {
			return err
		}
		s.Phase = "stopped"
		s.Complete(now, nil, service.CodexTicketFailure("ticket_stopped"))
		if err = saveTicketState(ctx, tx, s, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

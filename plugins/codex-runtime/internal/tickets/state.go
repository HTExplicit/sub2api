package tickets

import (
	"errors"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

const Length = 292
const TTL = time.Hour

var ErrNotDue = errors.New("ticket_not_due")
var ErrRunning = errors.New("ticket_attempt_in_progress")

type Ticket struct {
	State      string    `json:"state"`
	AccountID  int64     `json:"account_id"`
	Identity   string    `json:"identity"`
	Model      string    `json:"model"`
	CapturedAt time.Time `json:"captured_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (t *Ticket) Valid(now time.Time, accountID int64, identity, model string) bool {
	return t != nil && t.AccountID == accountID && t.Identity == identity && t.Model == model &&
		len(t.State) == Length && strings.HasPrefix(t.State, "gAAAAA") && !t.CapturedAt.After(now) &&
		now.Before(t.ExpiresAt) && !t.ExpiresAt.After(t.CapturedAt.Add(TTL))
}

// State includes the spent automatic attempt before any network IO. A process
// restart resumes only persisted pending stages, never a running/spent attempt.
type State struct {
	Schema            int                                    `json:"schema,omitempty"`
	Enrolled          bool                                   `json:"enrolled,omitempty"`
	Failures          int                                    `json:"failures,omitempty"`
	Qualification     *extensionv1.CodexRoutingQualification `json:"qualification,omitempty"`
	Observation       *extensionv1.CodexRoutingObservation   `json:"observation,omitempty"`
	Identity          string                                 `json:"identity"`
	OperationID       string                                 `json:"operation_id,omitempty"`
	Phase             string                                 `json:"phase"`
	NextAt            *time.Time                             `json:"next_attempt_at,omitempty"`
	ExpiresAt         *time.Time                             `json:"expires_at,omitempty"`
	ManualWasEnrolled bool                                   `json:"manual_was_enrolled,omitempty"`
	LastCode          string                                 `json:"last_code,omitempty"`
	LastAttemptAt     *time.Time                             `json:"last_attempt_at,omitempty"`
	Ticket            *Ticket                                `json:"ticket,omitempty"`
}

func (s *State) migrateRouting() {
	if s.Schema >= extensionv1.CodexRoutingSchema {
		return
	}
	s.Schema = extensionv1.CodexRoutingSchema
	s.Enrolled = s.Phase == "ready" || s.Phase == "retry"
	if s.Enrolled {
		s.Phase = "needs_cookie_verification"
		now := time.Now().UTC()
		s.NextAt = &now
	}
	// Old STATE is never a Cookie qualification. Keep stopped/running markers
	// and operation IDs so migration cannot resurrect a spent attempt.
	s.Ticket, s.Qualification, s.ExpiresAt = nil, nil, nil
}

func (s *State) beginRouting(now time.Time, manual bool) error {
	if s.Phase == "manual_running" || s.Phase == "pre_running" || s.Phase == "post_running" {
		return ErrRunning
	}
	if !manual && (!s.Enrolled || s.Phase == "stopped" || s.Failures >= 2 || s.NextAt == nil || now.Before(*s.NextAt)) {
		return ErrNotDue
	}
	s.ManualWasEnrolled = s.Enrolled
	s.Phase = "pre_running"
	if manual {
		s.Phase = "manual_running"
	}
	s.LastAttemptAt, s.NextAt = &now, nil
	return nil
}

func (s *State) completeRouting(now time.Time, q *extensionv1.CodexRoutingQualification, observation extensionv1.CodexRoutingObservation) {
	manual := s.Phase == "manual_running"
	s.Observation, s.LastCode = &observation, observation.Code
	if q != nil {
		s.Qualification, s.ExpiresAt = q, &q.ExpiresAt
		s.Enrolled, s.Failures, s.Phase = true, 0, "ready"
		due := q.ExpiresAt.Add(-extensionv1.CodexRoutingRefreshLead)
		s.NextAt = &due
		return
	}
	s.Failures++
	if (manual && !s.ManualWasEnrolled) || s.Failures >= 2 || observation.Code == "ticket_canceled" || observation.Code == "ticket_interrupted" {
		s.Stop()
		return
	}
	s.Phase = "retry"
	due := now.Add(extensionv1.CodexRoutingRefreshLead)
	s.NextAt = &due
}

func (s *State) Begin(now time.Time, manual bool) error {
	switch s.Phase {
	case "manual_running", "pre_running", "post_running":
		return ErrRunning
	}
	if manual {
		s.ManualWasEnrolled = s.Phase == "ready" || s.Phase == "retry"
		s.Phase = "manual_running"
		s.LastAttemptAt = &now
		s.NextAt = nil
		return nil
	}
	if s.NextAt == nil || now.Before(*s.NextAt) || s.ExpiresAt == nil {
		return ErrNotDue
	}
	switch s.Phase {
	case "ready":
		if now.Before(*s.ExpiresAt) {
			s.Phase = "pre_running"
		} else {
			due := s.ExpiresAt.Add(time.Minute)
			s.Phase = "retry"
			s.NextAt = &due
			if now.Before(due) {
				return ErrNotDue
			}
			s.Phase = "post_running"
		}
	case "retry":
		s.Phase = "post_running"
	default:
		return ErrNotDue
	}
	s.LastAttemptAt = &now
	s.NextAt = nil
	return nil
}

func (s *State) Complete(now time.Time, ticket *Ticket, code string) {
	prior := s.Phase
	s.LastCode = code
	if ticket != nil {
		s.Ticket = ticket
		s.ExpiresAt = &ticket.ExpiresAt
		due := ticket.ExpiresAt.Add(-time.Minute)
		s.NextAt = &due
		s.Phase = "ready"
		return
	}
	if s.ExpiresAt != nil {
		if prior == "manual_running" && s.ManualWasEnrolled && now.Before(*s.ExpiresAt) {
			due := s.ExpiresAt.Add(-time.Minute)
			s.Phase = "ready"
			if !now.Before(due) {
				due = s.ExpiresAt.Add(time.Minute)
				s.Phase = "retry"
			}
			s.NextAt = &due
			return
		}
		if prior == "pre_running" {
			due := s.ExpiresAt.Add(time.Minute)
			s.NextAt = &due
			s.Phase = "retry"
			return
		}
	}
	s.Stop()
}

func (s *State) Stop() { s.Phase = "stopped"; s.NextAt = nil }

func Eligible(platform, accountType string, shadow bool) bool {
	return platform == "openai" && (accountType == "oauth" || accountType == "setup-token") && !shadow
}

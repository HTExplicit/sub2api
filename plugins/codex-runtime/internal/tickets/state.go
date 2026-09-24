package tickets

import (
	"encoding/json"
	"errors"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var ErrNotDue = errors.New("ticket_not_due")
var ErrRunning = errors.New("ticket_attempt_in_progress")

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
	// Opaque only so every legacy representation can be discarded without
	// interpreting its length, timestamps or account-plan-specific format.
	Ticket json.RawMessage `json:"ticket,omitempty"`
}

func (s *State) migrateRouting() {
	s.Ticket = nil
	if s.Schema >= extensionv1.CodexRoutingSchema {
		return
	}
	s.Schema = extensionv1.CodexRoutingSchema
	s.Enrolled = s.Phase == "ready" || s.Phase == "retry"
	if s.Enrolled {
		s.Phase = "needs_cookie_verification"
		now := time.Now().UTC()
		s.NextAt = &now
	} else {
		s.NextAt = nil
	}
	// Old STATE is never a Cookie qualification. Keep stopped/running markers
	// and operation IDs so migration cannot resurrect a spent attempt.
	s.Qualification, s.ExpiresAt = nil, nil
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

func (s *State) Stop() { s.Phase = "stopped"; s.NextAt = nil }

func Eligible(platform, accountType string, shadow bool) bool {
	return platform == "openai" && (accountType == "oauth" || accountType == "setup-token") && !shadow
}

package service

import (
	"context"
	"time"
)

// Candidates belong to sources, never to a target verdict. The active source
// is preferred until expiry; a target rejection only cools that qualification.
func (s *CodexGatewayBorrowService) currentCandidateLocked() *codexGatewayBorrowCandidate {
	if s.candidate != nil && time.Now().Before(s.candidate.expires) {
		return s.candidate
	}
	for _, id := range s.config.SourceAccountIDs {
		if c := s.candidates[id]; c != nil && time.Now().Before(c.expires) {
			s.candidate = c
			return c
		}
	}
	return s.candidate // Retain the expired candidate for status and trigger evidence.
}

func (s *CodexGatewayBorrowService) storeCandidateLocked(candidate *codexGatewayBorrowCandidate) {
	if s.candidates == nil {
		s.candidates = make(map[int64]*codexGatewayBorrowCandidate)
	}
	now := time.Now()
	clamp := func(old *codexGatewayBorrowCandidate) {
		if old != nil && now.Before(old.expires) && old.cookie.Value == candidate.cookie.Value && old.expires.Before(candidate.expires) {
			candidate.expires = old.expires
		}
	}
	clamp(s.candidate)
	for _, old := range s.candidates {
		clamp(old)
	}
	s.candidates[candidate.sourceID] = candidate
	// A backup does not displace a live primary from a different source.
	if s.candidate == nil || !now.Before(s.candidate.expires) || s.candidate.sourceID == candidate.sourceID {
		s.candidate = candidate
	}
	// Other source proofs remain usable. Cookie, model and identity are already
	// in their keys, so no target can accidentally inherit another qualification.
}

func (s *CodexGatewayBorrowService) liveCookieLocked(key string) bool {
	if c := s.candidate; c != nil && borrowHash(c.cookie.Value) == key && time.Now().Before(c.expires) {
		return true
	}
	for _, c := range s.candidates {
		if borrowHash(c.cookie.Value) == key && time.Now().Before(c.expires) {
			return true
		}
	}
	return false
}

func (s *CodexGatewayBorrowService) candidateCurrentLocked(rev uint64, candidate *codexGatewayBorrowCandidate) bool {
	if candidate == nil || s.revision != rev || !s.config.Enabled || s.revisionCtx.Err() != nil || !time.Now().Before(candidate.expires) {
		return false
	}
	current := s.candidates[candidate.sourceID]
	if current == nil {
		current = s.candidate
	}
	return current != nil && current.sourceID == candidate.sourceID && current.cookie.Value == candidate.cookie.Value && current.cookie.Path == candidate.cookie.Path && current.expires.Equal(candidate.expires)
}

type CodexGatewayBorrowAcquisition struct {
	Trigger    string     `json:"trigger,omitempty"`
	Phase      string     `json:"phase,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Error      string     `json:"error,omitempty"`
	RetryAfter *time.Time `json:"retry_after,omitempty"`
}

type borrowAcquisitionTriggerKey struct{}

func withBorrowAcquisitionTrigger(ctx context.Context, candidate *codexGatewayBorrowCandidate) context.Context {
	trigger := "missing_route"
	if candidate != nil {
		trigger = "expired_route"
	}
	return context.WithValue(ctx, borrowAcquisitionTriggerKey{}, trigger)
}

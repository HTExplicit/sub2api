package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

func (s *CodexGatewayBorrowService) candidateRejectedLocked() bool {
	return s.candidate != nil && s.rejectedCookie != "" && borrowHash(s.candidate.cookie.Value) == s.rejectedCookie
}

func (s *CodexGatewayBorrowService) trackCandidateValidationLocked(revision uint64, cookie string) func() {
	if s.validatingRoutes == nil {
		s.validatingRoutes = make(map[string]int)
	}
	s.validatingRoutes[cookie]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.revision != revision {
			return
		}
		s.validatingRoutes[cookie]--
		if s.validatingRoutes[cookie] == 0 {
			delete(s.validatingRoutes, cookie)
		}
	}
}

// Source completion produces a candidate, not proof that a target can use it.
// Retire a definitively rejected candidate only when it has no live successful
// proof and no other validation in flight. Other targets' usable routes survive.
func (s *CodexGatewayBorrowService) rejectCandidateForRetry(err error, cooling bool) bool {
	var failure *CodexGatewayBorrowFailure
	if s == nil || !errors.As(err, &failure) || failure.CookieFingerprint == "" ||
		(failure.Reason != "target_state_changed" && failure.Reason != "target_route_changed") {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != failure.Revision || !s.config.Enabled || s.candidate == nil || s.revisionCtx.Err() != nil {
		return false
	}
	if borrowHash(s.candidate.cookie.Value) != failure.CookieFingerprint {
		// A concurrent waiter has already replaced this candidate. Retry the
		// published replacement; never retire it using the older observation.
		return !cooling
	}
	if failure.Verification != nil && failure.Verification.ExpiresAt != nil && !failure.Verification.ExpiresAt.Equal(s.candidate.expires) {
		return !cooling // A fresh lease cannot be retired by its predecessor's result.
	}
	now := time.Now()
	if now.Before(s.prepareFailedUntil) {
		return false
	}
	for _, check := range s.qualifications {
		if check.cookieKey == failure.CookieFingerprint && check.result.Success && now.Before(check.expires) {
			return false
		}
	}
	// The presentation index holds only the latest check per account/model;
	// another request identity can still be validating the same candidate.
	if s.validatingRoutes[failure.CookieFingerprint] > 0 {
		return false
	}
	if s.rejectedRoutes == nil {
		s.rejectedRoutes = make(map[string]time.Time)
	}
	// Remember retired credentials through their original lease, including
	// A -> B -> A source responses. Never forget a live rejection to make room.
	if len(s.rejectedRoutes) >= 1024 {
		for key, expires := range s.rejectedRoutes {
			if !now.Before(expires) {
				delete(s.rejectedRoutes, key)
			}
		}
		if _, known := s.rejectedRoutes[failure.CookieFingerprint]; !known && len(s.rejectedRoutes) >= 1024 {
			return false
		}
	}
	s.rejectedRoutes[failure.CookieFingerprint] = s.candidate.expires
	s.rejectedCookie, s.rejectedCause = failure.CookieFingerprint, err
	if cooling {
		s.prepareFailedUntil = now.Add(codexGatewayBorrowFailureWait)
		s.prepareFailure = err.Error()
	}
	return true
}

// One request may try at most one replacement through the same configured
// sources/proxies. There is no background renewal or business replay. A second
// rejection cools acquisition for 15 seconds; per-shot and caller deadlines,
// shared-flight cancellation and the diagnostic's eight-request cap still apply.
func (s *CodexGatewayBorrowService) applyWithCandidateRetry(req *http.Request, account *Account, model, proxy string, profile *tlsfingerprint.Profile, cachedOnly, force bool) (*http.Request, *CodexGatewayBorrowApplication, error) {
	wire, application, firstErr := s.apply(req, account, model, proxy, profile, cachedOnly, force)
	if firstErr == nil || cachedOnly || req == nil || req.Context().Err() != nil || !s.rejectCandidateForRetry(firstErr, false) {
		return wire, application, firstErr
	}
	retryCtx, cancel := context.WithTimeout(req.Context(), codexGatewayBorrowTimeout)
	defer cancel()
	wire, application, retryErr := s.apply(req.WithContext(retryCtx), account, model, proxy, profile, false, force)
	if retryErr != nil {
		s.rejectCandidateForRetry(retryErr, true)
		// Preserve the original target verdict as well as the replacement's
		// complete cause, including cancellation or an exhausted diagnostic cap.
		return wire, nil, errors.Join(firstErr, retryErr)
	}
	if wire != nil && application != nil && application.Applied {
		// The bounded preparation context must not cancel the business send.
		wire = wire.WithContext(context.WithValue(req.Context(), codexBorrowAppliedContextKey{}, true))
	}
	return wire, application, nil
}

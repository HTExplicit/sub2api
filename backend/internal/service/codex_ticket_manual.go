package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type codexTicketUpstreamError struct{ message string }

func (e *codexTicketUpstreamError) Error() string { return e.message }

// Shared by manual jobs and automatic renewal, across administrators.
func (s *OpenAIGatewayService) codexTicketSlots() chan struct{} {
	s.openaiCodexTicketLifecycleMu.Lock()
	defer s.openaiCodexTicketLifecycleMu.Unlock()
	if s.openaiCodexTicketSlots == nil {
		s.openaiCodexTicketSlots = make(chan struct{}, 5)
	}
	return s.openaiCodexTicketSlots
}

func (s *OpenAIGatewayService) CodexTicketModels() []string {
	return append([]string(nil), s.openAICodexTicketConfig().Models...)
}
func (s *OpenAIGatewayService) CodexTicketsEnabled(ctx context.Context) bool {
	return s.openAICodexTicketEnabledContext(ctx)
}

func (s *OpenAIGatewayService) HarvestCodexTicket(ctx context.Context, id int64, model, operation string, jobID int64, force bool) CodexTicketResult {
	return s.runCodexTicketAttempt(ctx, id, model, operation, jobID, true, force)
}

func (s *OpenAIGatewayService) StopCodexTicketRenewal(ctx context.Context, id int64, models []string) error {
	store, ok := s.accountRepo.(CodexTicketRepository)
	if !ok {
		return errors.New("ticket store unavailable")
	}
	return store.StopCodexTicket(ctx, id, models, time.Now())
}

func (s *OpenAIGatewayService) runCodexTicketAttempt(parent context.Context, id int64, model, operation string, jobID int64, manual, force bool) CodexTicketResult {
	if s == nil || !s.openAICodexTicketEnabledContext(parent) {
		return CodexTicketFailure("ticket_disabled")
	}
	model = strings.TrimSpace(model)
	if parent.Err() != nil {
		return CodexTicketFailure("ticket_canceled")
	}
	if !s.openAICodexTicketConfiguredModel(model) {
		return CodexTicketFailure("ticket_model_invalid")
	}
	store, ok := s.accountRepo.(CodexTicketRepository)
	if !ok {
		return CodexTicketFailure("ticket_persist")
	}
	slots := s.codexTicketSlots()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-parent.Done():
		return CodexTicketFailure("ticket_canceled")
	}
	started := time.Now()
	claim, err := store.ClaimCodexTicket(parent, id, model, operation, jobID, manual, force, started)
	if err != nil {
		switch {
		case errors.Is(err, ErrCodexTicketValid):
			return CodexTicketResult{Success: true, Code: "ticket_skipped", Message: "已有有效票据，本次跳过"}
		case errors.Is(err, ErrCodexTicketBusy):
			return CodexTicketFailure("ticket_busy")
		case errors.Is(err, ErrCodexTicketAlreadyAttempted):
			return CodexTicketFailure("ticket_interrupted")
		case errors.Is(err, ErrCodexTicketInactive):
			return CodexTicketFailure("ticket_ineligible")
		case errors.Is(err, ErrCodexTicketDisabled):
			return CodexTicketFailure("ticket_disabled")
		case errors.Is(err, context.Canceled):
			return CodexTicketFailure("ticket_canceled")
		case errors.Is(err, ErrCodexTicketNotDue):
			return CodexTicketResult{Code: "ticket_not_due", Message: "未到续期时间"}
		default:
			return CodexTicketFailure("ticket_persist")
		}
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	var ticket *CodexTicketRecord
	result := func() CodexTicketResult {
		account, err := s.accountRepo.GetByID(ctx, id)
		if err != nil || !CodexTicketAccountEligible(account) {
			return CodexTicketFailure("ticket_ineligible")
		}
		if CodexTicketAccountIdentity(account) != claim.Identity {
			return CodexTicketFailure("ticket_stale")
		}
		proxyURL := s.openAICodexTicketHarvestProxyURLContext(ctx)
		if proxyURL == "" {
			return CodexTicketFailure("ticket_proxy_missing")
		}
		var prepared *codexTicketPreparedProxy
		if _, hasTrustStore := s.accountRepo.(CodexTicketProxyTrustRepository); hasTrustStore {
			var proxyResult *CodexTicketProxyTestResult
			prepared, proxyResult, err = s.prepareCodexTicketProxy(ctx, proxyURL, manual, true)
			if err != nil {
				if proxyResult != nil && strings.HasPrefix(proxyResult.Code, "ticket_") {
					return CodexTicketFailure(proxyResult.Code)
				}
				return CodexTicketFailure(codexTicketTransportFailureCode(err))
			}
			defer prepared.transport.CloseIdleConnections()
		}
		token, _, err := s.GetAccessToken(ctx, account)
		if err != nil || strings.TrimSpace(token) == "" {
			return CodexTicketFailure("ticket_token")
		}
		state, status, err := s.fireCodexTicketProbe(ctx, account, token, model, proxyURL, 25*time.Second, prepared)
		if err != nil {
			var upstream *codexTicketUpstreamError
			if errors.As(err, &upstream) {
				r := CodexTicketFailure("ticket_upstream")
				r.HTTPStatus = status
				r.ObservedLength = len(state)
				if upstream.message != "" {
					r.Message = upstream.message
				}
				return r
			}
			return CodexTicketFailure(codexTicketTransportFailureCode(err))
		}
		failure := CodexTicketResult{HTTPStatus: status, ObservedLength: len(state)}
		switch {
		case status != 200:
			failure = CodexTicketFailure("ticket_upstream")
		case state == "":
			failure = CodexTicketFailure("ticket_missing_header")
		case len(state) != 292:
			failure = CodexTicketFailure("ticket_length")
		case !strings.HasPrefix(state, openAICodexTicketStatePrefix):
			failure = CodexTicketFailure("ticket_prefix")
		default:
			now := time.Now()
			expiry := now.Add(time.Duration(s.openAICodexTicketConfig().TTLSeconds) * time.Second)
			ticket = &CodexTicketRecord{AccountID: id, Model: model, Identity: claim.Identity, State: state, Length: len(state), CapturedAt: now, ExpiresAt: expiry, Attempts: 1}
			sum := sha256.Sum256([]byte(state))
			return CodexTicketResult{Success: true, Code: "ticket_ready", Message: "292票据已取得", HTTPStatus: status, ObservedLength: len(state), ExpiresAt: &expiry, Fingerprint: hex.EncodeToString(sum[:8])}
		}
		failure.HTTPStatus = status
		failure.ObservedLength = len(state)
		return failure
	}()
	result.DurationMS = time.Since(started).Milliseconds()
	if parent.Err() != nil {
		ticket = nil
		result = CodexTicketFailure("ticket_canceled")
	}
	if !s.openAICodexTicketEnabledContext(parent) {
		ticket = nil
		result = CodexTicketFailure("ticket_disabled")
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 8*time.Second)
	defer finishCancel()
	applied, err := store.FinishCodexTicket(finishCtx, claim, ticket, result, time.Now())
	if err != nil {
		return CodexTicketFailure("ticket_persist")
	}
	if ticket != nil && !applied {
		return CodexTicketFailure("ticket_stale")
	}
	if applied {
		// Publish in memory only after the database transaction committed.
		raw, _ := json.Marshal(ticket)
		var mem openAICodexTicket
		_ = json.Unmarshal(raw, &mem)
		s.openaiCodexTickets.Store(openAICodexTicketKey(id, model), &mem)
	}
	return result
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type codexBorrowProbeShapeContextKey struct{}

const (
	codexBorrowProbeRanxiLiteral = "ranxi_literal_without_tier"
	codexBorrowProbeRanxiTier    = "ranxi_literal_with_final_tier"
	codexBorrowProbeCurrent      = "current_final_tier"
)

// These are observations of one fixed candidate, never qualifications usable
// by business requests. The original pinned route probe selects Astra; rendering
// a selected Sol model here is explicitly the downstream model extension.
type CodexBorrowProbeContext struct {
	SourceAccountID          int64     `json:"source_account_id"`
	TargetProxyID            *int64    `json:"target_proxy_id"`
	CookieFingerprint        string    `json:"cookie_fingerprint"`
	CandidateExpiresAt       time.Time `json:"candidate_expires_at"`
	TemplateFingerprint      string    `json:"template_fingerprint"`
	RequestedTier            string    `json:"requested_service_tier"`
	FinalTier                string    `json:"final_service_tier"`
	RoutingHint              string    `json:"routing_hint"`
	PlaintextBodyFingerprint string    `json:"plaintext_body_fingerprint"`
	OriginalAstraModel       bool      `json:"original_astra_model"`
}

// Literal request field order from ranxi2001/sub2api d3e43f2, LGPL-3.0,
// openai_codex_state_probe.go:320-347. Tier is the only semantic addition in
// the second variant. Model always stays the explicitly selected real model.
func borrowRanxiLiteralPayload(model, tier string) []byte {
	modelJSON, _ := json.Marshal(model)
	body := `{"model":` + string(modelJSON) + `,"instructions":"Reply with OK.","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Reply with OK."}]}],"stream":true,"store":false,"parallel_tool_calls":true,"include":["reasoning.encrypted_content"]`
	if tier != "" {
		tierJSON, _ := json.Marshal(tier)
		body += `,"service_tier":` + string(tierJSON)
	}
	return []byte(body + `}`)
}

func (s *CodexGatewayBorrowService) diagnoseProbeContract(ctx context.Context, revision uint64, revisionCtx context.Context, request CodexBorrowDiagnosticRequest, limit int, emit func(CodexBorrowDiagnosticEvent)) error {
	var requests atomic.Int32
	d := &codexBorrowDiagnostic{limit: int32(limit), requests: &requests, borrow: true, session: uuid.NewString(), serviceTier: request.ServiceTier,
		onRequest: func(count int32) {
			emit(CodexBorrowDiagnosticEvent{Type: "request", Mode: "borrowed", Requests: count, Limit: limit})
		}}
	ctx = context.WithValue(WithCodexGatewayBorrowObservation(ctx), codexBorrowDiagnosticContextKey{}, d)
	emit(CodexBorrowDiagnosticEvent{Type: "phase", Mode: "borrowed", Limit: limit})
	account, template, proxy, err := s.accountTemplate(ctx, request.AccountID, request.Model)
	if err != nil {
		return err
	}
	policyBody, err := s.gateway.applyOpenAIFastPolicyToBody(ctx, account, request.Model, borrowObservationPayload(request.Model, "Reply with OK.", "", request.ServiceTier))
	if err != nil {
		return err // A blocked local tier must not consume a source or probe request.
	}
	finalTier := gjson.GetBytes(policyBody, "service_tier").String()
	s.mu.Lock()
	candidate := s.candidate
	current := s.candidateCurrentLocked(revision, candidate)
	s.mu.Unlock()
	if !current {
		if err := s.prepareSource(ctx, revision, revisionCtx); err != nil {
			return err
		}
		s.mu.Lock()
		candidate = s.candidate
		current = s.candidateCurrentLocked(revision, candidate)
		s.mu.Unlock()
	}
	if !current {
		return ErrCodexGatewayBorrowChanged
	}
	// The snapshot cannot be silently replaced halfway through the comparison.
	ctx, cancel := context.WithDeadline(ctx, candidate.expires)
	defer cancel()
	d.prepareHTTPSessionTurn(template.Header, make(map[string]any))
	credential, err := resolveCredentialAccount(ctx, s.accounts, account)
	if err != nil {
		return err
	}
	enforceCodexIdentityHeadersForAccount(template.Header, credential, s.gateway.codexIdentityOverrideUA(account))
	setOpenAICodexRoutingHint(template.Header, account, request.Model, finalTier)
	template = template.WithContext(withCodexBorrowServiceTier(ctx, finalTier))
	identity := borrowRequestFingerprint(template, request.Model, proxy, nil, candidate.cookie.Value)
	shapes := []string{codexBorrowProbeRanxiLiteral, codexBorrowProbeRanxiTier, codexBorrowProbeCurrent}
	if finalTier == "" {
		shapes = []string{codexBorrowProbeRanxiLiteral, codexBorrowProbeCurrent}
	}
	for _, shape := range shapes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if requests.Load() >= int32(limit) {
			return ErrCodexBorrowDiagnosticBudget
		}
		tier := finalTier
		if shape == codexBorrowProbeRanxiLiteral {
			tier = ""
		}
		operation := withCodexBorrowServiceTier(context.WithValue(ctx, codexBorrowProbeShapeContextKey{}, shape), tier)
		variant := template.WithContext(operation)
		started := time.Now()
		verification := s.probeTarget(operation, variant, account, request.Model, proxy, nil, candidate)
		body := borrowRanxiLiteralPayload(request.Model, tier)
		if shape == codexBorrowProbeCurrent {
			body = borrowObservationPayload(request.Model, "Reply with OK.", "", tier)
		}
		result := CodexBorrowDiagnosticResult{Scenario: "probe_contract", Mode: "borrowed", ProbeOnly: true,
			Verification: &verification, DurationMS: time.Since(started).Milliseconds(), ReportedModel: verification.ReportedModel,
			ProbeContext: &CodexBorrowProbeContext{SourceAccountID: candidate.sourceID, TargetProxyID: account.ProxyID,
				CookieFingerprint: borrowHash(candidate.cookie.Value), CandidateExpiresAt: candidate.expires,
				TemplateFingerprint: identity, RequestedTier: request.ServiceTier, FinalTier: finalTier, RoutingHint: template.Header.Get(openAICodexRoutingHintHeader),
				PlaintextBodyFingerprint: borrowHash(string(body)), OriginalAstraModel: request.Model == codexGatewayBorrowSourceModel}}
		if !verification.Success {
			result.FailureStage, result.FailureReason, result.Error = "target_validation", verification.Reason, verification.Error
		}
		// No business send occurred. Completed/applied/dispatched remain false;
		// the verification contains each probe's actual terminal and STATE facts.
		emit(CodexBorrowDiagnosticEvent{Type: "result", Mode: "borrowed", Result: &result, Requests: requests.Load(), Limit: limit})
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
	}
	emit(CodexBorrowDiagnosticEvent{Type: "done", Requests: requests.Load(), Limit: limit})
	return ctx.Err()
}

package service

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

type CodexGatewayBorrowPelicanResult struct {
	RawAnswer   string `json:"raw_answer"`
	RawResponse string `json:"raw_response"`
	ModelID     string `json:"model_id"`
	Effort      string `json:"effort"`
	Status      string `json:"status"`
	Error       string `json:"error"`
}

func codexGatewayBorrowModelEfforts() map[string][]string {
	result := map[string][]string{}
	for _, model := range DefaultCodexGatewayBorrowConfig().Models {
		levels := configuredCodexGPTReasoningLevels(model)
		efforts := make([]string, 0, len(levels))
		for _, level := range levels {
			// The local manifest's Ultra entry describes Codex orchestration.
			// The inference observation sends only native API effort values.
			if level.Effort != "ultra" {
				efforts = append(efforts, level.Effort)
			}
		}
		result[model] = efforts
	}
	return result
}

// GeneratePelican observes exactly one request on an already-qualified route.
// Missing, expired or mismatched evidence produces "skipped" without acquiring
// a source cookie or validating a target. The runner owns its bounded parallel
// work, persistence and preview; this method changes no route or account state.
func (s *CodexGatewayBorrowService) GeneratePelican(ctx context.Context, accountID int64, model, effort string) (*CodexGatewayBorrowPelicanResult, error) {
	result := &CodexGatewayBorrowPelicanResult{ModelID: model, Effort: effort, Status: "skipped"}
	if effort == "" {
		effort, result.Effort = "high", "high"
	}
	if !slices.Contains(codexGatewayBorrowModelEfforts()[model], effort) {
		result.Error = "reasoning effort is unsupported for this model"
		return result, errors.New(result.Error)
	}
	cfg := s.ConfigSnapshot()
	if !cfg.Enabled || !slices.Contains(cfg.TargetAccountIDs, accountID) || !slices.Contains(cfg.Models, model) {
		result.Error = "target model is not configured for gateway borrowing"
		return result, nil
	}
	s.mu.Lock()
	rev, revisionCtx := s.revision, s.revisionCtx
	s.mu.Unlock()
	s.mu.Lock()
	candidate := s.currentCandidateLocked()
	cached := candidate != nil && time.Now().Before(candidate.expires)
	s.mu.Unlock()
	if !cached {
		result.Error = "qualified route cache is missing or expired"
		return result, nil
	}
	requestCtx, cancel := borrowRevisionContext(ctx, revisionCtx, codexGatewayBorrowTimeout)
	defer cancel()
	a, template, proxy, err := s.accountTemplate(requestCtx, accountID, model, true)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	if supported, _ := AccountTestReasoningOptions(a, model); !slices.Contains(supported, effort) {
		result.Error = "selected reasoning effort is not supported by the account's local model metadata"
		return result, errors.New(result.Error)
	}
	// Use the same finalized target identity as manual verification. Effort
	// and prompt do not enter the route key; model/auth/exit/business STATE do.
	req, err := borrowObservationRequest(requestCtx, template.Header, model, CodexGatewayBorrowPelicanPrompt, effort, "", nil, HTTPUpstreamProfileCodexBorrowTarget)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	borrowed, applied, err := s.Apply(req, a, model, proxy, nil, true)
	if err != nil || applied == nil || !applied.Applied {
		result.Error = "matching qualified route cache is missing, expired or changed"
		return result, nil
	}
	s.mu.Lock()
	valid := s.revision == rev && revisionCtx.Err() == nil && s.liveCookieLocked(applied.CookieFingerprint)
	s.mu.Unlock()
	if !valid {
		result.Error = "gateway borrow configuration or route changed before dispatch"
		return result, nil
	}
	// The final cached check happened immediately before this single dispatch.
	// It does not write qualification results, even when the response fails.
	shot, sendErr := s.sendObservation(borrowed, a, proxy, nil, model, codexGatewayBorrowPelicanMaxBody)
	result.RawAnswer, result.RawResponse = shot.answer, shot.raw
	if shot.model != "" {
		result.ModelID = shot.model
	}
	if sendErr != nil {
		result.Status, result.Error = "failed", borrowObservationError(shot, sendErr)
		if shot.incomplete {
			result.Status = "incomplete"
		}
		return result, sendErr
	}
	if shot.status != http.StatusOK || shot.errorText != "" {
		result.Status, result.Error = "failed", borrowObservationError(shot, nil)
		if shot.incomplete {
			result.Status = "incomplete"
		}
		return result, nil
	}
	if !shot.complete || strings.TrimSpace(shot.answer) == "" {
		result.Status, result.Error = "incomplete", borrowObservationError(shot, nil)
		return result, nil
	}
	result.Status = "complete"
	return result, nil
}

package service

import (
	"context"
	"time"
)

// Diagnostics never clear an ordinary breaker or borrow its half-open probe.
// The production adapter implements this with a read-only Redis command.
type codexQualityBreakerReader interface {
	CodexQualityRuntimeBlocked(context.Context, int64, []string) (bool, error)
}

func (s *OpenAIGatewayService) codexQualityHealthBlocked(ctx context.Context, account *Account) bool {
	now := time.Now()
	if value, exists := s.openaiAccountRuntimeBlockUntil.Load(account.ID); exists {
		until, valid := value.(time.Time)
		if !valid || until.IsZero() || now.Before(until) {
			return true
		}
	}
	model := openAIAccountModelTransientModel(codexQualityModel)
	state := s.getOpenAIAccountModelTransientState()
	state.mu.Lock()
	entry := state.entries[openAIAccountModelKey{AccountID: account.ID, Model: model}]
	modelBlocked := now.Before(entry.blockUntil)
	state.mu.Unlock()
	if modelBlocked {
		return true
	}
	if proxyID, exists := openAIProxyStreamCircuitProxyID(account); exists {
		circuit := s.getOpenAIProxyStreamCircuit()
		circuit.mu.Lock()
		blocked := !circuit.settings.disabled && now.Before(circuit.entries[proxyID].blockedUntil)
		circuit.mu.Unlock()
		if blocked {
			return true
		}
	}
	if s.cache != nil {
		reader, ok := s.cache.(codexQualityBreakerReader)
		if !ok {
			return true
		}
		blocked, err := reader.CodexQualityRuntimeBlocked(ctx, account.ID, []string{"", model})
		if err != nil || blocked {
			return true
		}
	}
	if s.rateLimitService != nil && s.rateLimitService.settingService != nil {
		thresholds := s.rateLimitService.settingService.GetAccountSchedulingThresholds(ctx)
		decision := EvaluateAccountSchedulingThreshold(account, thresholds, now)
		if decision.ShouldPause && decision.Until != nil && now.Before(*decision.Until) {
			return true
		}
	}
	return false
}

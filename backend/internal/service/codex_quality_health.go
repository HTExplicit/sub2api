package service

import (
	"context"
	"fmt"
	"time"
)

// Diagnostics never clear an ordinary breaker or borrow its half-open probe.
// The production adapter implements this with a read-only Redis command.
type codexQualityBreakerReader interface {
	CodexQualityRuntimeBlocked(context.Context, int64, []string) (bool, error)
}

func (s *OpenAIGatewayService) codexQualityHealthBlocked(ctx context.Context, account *Account) bool {
	return s.codexQualityHealthBlock(ctx, account) != ""
}

// codexQualityHealthBlock names the ordinary health state that blocks a
// diagnostic send, or returns "" when none does. It only reads that state.
func (s *OpenAIGatewayService) codexQualityHealthBlock(ctx context.Context, account *Account) string {
	now := time.Now()
	if value, exists := s.openaiAccountRuntimeBlockUntil.Load(account.ID); exists {
		until, valid := value.(time.Time)
		switch {
		case !valid:
			return fmt.Sprintf("account runtime block holds an unreadable value %v", value)
		case until.IsZero():
			return "account runtime block has no end time"
		case now.Before(until):
			return "account runtime block until " + until.UTC().Format(time.RFC3339)
		}
	}
	model := openAIAccountModelTransientModel(codexQualityModel)
	state := s.getOpenAIAccountModelTransientState()
	state.mu.Lock()
	entry := state.entries[openAIAccountModelKey{AccountID: account.ID, Model: model}]
	state.mu.Unlock()
	if now.Before(entry.blockUntil) {
		return fmt.Sprintf("model %s transient block until %s (failure streak %d)", model, entry.blockUntil.UTC().Format(time.RFC3339), entry.failureStreak)
	}
	if proxyID, exists := openAIProxyStreamCircuitProxyID(account); exists {
		circuit := s.getOpenAIProxyStreamCircuit()
		circuit.mu.Lock()
		disabled := circuit.settings.disabled
		blockedUntil := circuit.entries[proxyID].blockedUntil
		circuit.mu.Unlock()
		if !disabled && now.Before(blockedUntil) {
			return fmt.Sprintf("proxy %d stream circuit is open until %s", proxyID, blockedUntil.UTC().Format(time.RFC3339))
		}
	}
	if s.cache != nil {
		reader, ok := s.cache.(codexQualityBreakerReader)
		if !ok {
			return "the runtime breaker state cannot be read without claiming a probe"
		}
		blocked, err := reader.CodexQualityRuntimeBlocked(ctx, account.ID, []string{"", model})
		if err != nil {
			return "read the runtime breaker state: " + err.Error()
		}
		if blocked {
			return fmt.Sprintf("the runtime breaker of account %d is open or half-open for %s", account.ID, model)
		}
	}
	if s.rateLimitService != nil && s.rateLimitService.settingService != nil {
		thresholds := s.rateLimitService.settingService.GetAccountSchedulingThresholds(ctx)
		decision := EvaluateAccountSchedulingThreshold(account, thresholds, now)
		if decision.ShouldPause && decision.Until != nil && now.Before(*decision.Until) {
			return fmt.Sprintf("scheduling threshold pause until %s (%s %s window %s: %.1f%% used, threshold %d%%)", decision.Until.UTC().Format(time.RFC3339), decision.Platform, decision.Scope, decision.Window, decision.UsedPercent, decision.ThresholdPercent)
		}
	}
	return ""
}

package service

import (
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

var adminObservabilityConfigOverride atomic.Pointer[AdminObservabilityConfig]

// LegacyAdminObservabilityConfig is the deploy-time default: account traffic
// telemetry follows the legacy gateway flag and the flat theme is on.
func LegacyAdminObservabilityConfig(cfg *config.Config) AdminObservabilityConfig {
	return AdminObservabilityConfig{TelemetryEnabled: cfg == nil || !cfg.Gateway.AccountTrafficTelemetryDisabled, ThemeEnabled: true}
}

// ConfigureAdminObservability installs the effective telemetry and theme
// switches (startup load, admin update, tests). A nil value restores the
// built-in default.
func ConfigureAdminObservability(config *AdminObservabilityConfig) {
	adminObservabilityConfigOverride.Store(config)
}

func currentAdminObservabilityConfig() AdminObservabilityConfig {
	if config := adminObservabilityConfigOverride.Load(); config != nil {
		return *config
	}
	return LegacyAdminObservabilityConfig(nil)
}

// FlatThemeEnabled reports whether the flat site theme is switched on.
func FlatThemeEnabled() bool {
	return currentAdminObservabilityConfig().ThemeEnabled
}

type TrafficObservationPolicy struct {
	Enabled        bool          `json:"enabled"`
	Classification DecisionTable `json:"classification"`
}

// currentAccountTrafficObservationPolicy returns the telemetry switch and the
// outcome rules for one account. Accounts without a concrete identity are never
// observed.
func currentAccountTrafficObservationPolicy(account *Account) (TrafficObservationPolicy, bool) {
	if account == nil || account.ID <= 0 || account.Platform == "" || account.Platform == "*" || account.Type == "" || account.Type == "*" {
		return TrafficObservationPolicy{}, false
	}
	return TrafficObservationPolicy{Enabled: currentAdminObservabilityConfig().TelemetryEnabled, Classification: accountTrafficOutcomeRules}, true
}

// accountTrafficOutcomeRules classifies a finished turn. Client cancellation
// outranks errors, and a WebSocket turn counts as completed only with a
// demonstrable terminal event. Every result is an AccountTrafficOutcome.
var accountTrafficOutcomeRules = func() DecisionTable {
	eq := func(field, value string) DecisionCondition {
		return DecisionCondition{Field: field, Operator: "eq", Value: value}
	}
	num := func(field, op, value string) DecisionCondition {
		return DecisionCondition{Field: field, Operator: op, Value: value}
	}
	rule := func(result AccountTrafficOutcome, when ...DecisionCondition) DecisionRule {
		return DecisionRule{When: when, Result: string(result)}
	}
	return DecisionTable{Default: string(AccountTrafficOutcomeFailedOther), Rules: []DecisionRule{
		rule(AccountTrafficOutcomeCancelled, eq("client_cancelled", "true")),
		rule(AccountTrafficOutcomeCancelled, eq("ws", "true"), eq("terminal", "response.cancelled")),
		rule(AccountTrafficOutcomeCancelled, eq("ws", "true"), eq("terminal", "response.incomplete")),
		rule(AccountTrafficOutcomeUpstream429, eq("has_error", "true"), eq("error_status", "429")),
		rule(AccountTrafficOutcomeUpstream5xx, eq("has_error", "true"), num("error_status", "gte", "500"), num("error_status", "lt", "600")),
		rule(AccountTrafficOutcomeFailedOther, eq("has_error", "true")),
		rule(AccountTrafficOutcomeFailedOther, eq("has_result", "false")),
		rule(AccountTrafficOutcomeCompleted2xx, eq("ws", "false")),
		rule(AccountTrafficOutcomeCompleted2xx, eq("terminal", "response.completed")),
		rule(AccountTrafficOutcomeCompleted2xx, eq("terminal", "response.done")),
		rule(AccountTrafficOutcomeUpstream429, eq("terminal", "response.failed"), eq("terminal_status", "429")),
		rule(AccountTrafficOutcomeUpstream5xx, eq("terminal", "response.failed"), num("terminal_status", "gte", "500"), num("terminal_status", "lt", "600")),
	}}
}()

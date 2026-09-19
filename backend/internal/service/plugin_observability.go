package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func LegacyAdminObservabilityConfig(cfg *config.Config) extensionv1.AdminObservabilityConfig {
	return extensionv1.AdminObservabilityConfig{TelemetryEnabled: cfg == nil || !cfg.Gateway.AccountTrafficTelemetryDisabled, ThemeEnabled: true}
}

func currentTrafficObservationPolicy(ctx context.Context) (extensionv1.TrafficObservationPolicy, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	call, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, "*", "*", extensionv1.Invocation{Capability: extensionv1.CapabilityObservability, Operation: "observability.policy", Payload: json.RawMessage(`{}`)})
	var policy extensionv1.TrafficObservationPolicy
	if err != nil || result.Code != "" || json.Unmarshal(result.Payload, &policy) != nil || policy.Classification.Validate() != nil {
		return policy, false
	}
	if !AccountTrafficOutcome(policy.Classification.Default).Valid() {
		return policy, false
	}
	for _, rule := range policy.Classification.Rules {
		if !AccountTrafficOutcome(rule.Result).Valid() {
			return policy, false
		}
	}
	return policy, true
}

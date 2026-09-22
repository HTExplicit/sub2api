package policy

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type Module struct {
	mu     sync.RWMutex
	config extensionv1.AdminObservabilityConfig
	host   *extensionv1.Client
}

func New() *Module {
	return &Module{config: extensionv1.AdminObservabilityConfig{TelemetryEnabled: true, ThemeEnabled: true}}
}
func (m *Module) SetHost(host *extensionv1.Client) { m.mu.Lock(); m.host = host; m.mu.Unlock() }
func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	config := extensionv1.AdminObservabilityConfig{TelemetryEnabled: true, ThemeEnabled: true}
	if json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(raw, &config) != nil {
		return nil, errors.New("invalid observability configuration")
	}
	for key, value := range fields {
		if (key != "telemetry_enabled" && key != "theme_enabled") || (string(value) != "true" && string(value) != "false") {
			return nil, errors.New("unknown or invalid observability setting")
		}
	}
	return json.Marshal(config)
}
func (m *Module) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	normalized, err := m.ValidateConfig(ctx, raw)
	if err != nil {
		return err
	}
	var config extensionv1.AdminObservabilityConfig
	if err := json.Unmarshal(normalized, &config); err != nil {
		return err
	}
	m.mu.Lock()
	m.config = config
	m.mu.Unlock()
	return nil
}
func (m *Module) Status(context.Context) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(m.config)
}
func (m *Module) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	m.mu.RLock()
	config, host := m.config, m.host
	m.mu.RUnlock()
	var output any
	switch {
	case in.Capability == extensionv1.CapabilityObservability && in.Operation == "observability.policy":
		output = extensionv1.TrafficObservationPolicy{Enabled: config.TelemetryEnabled, Classification: trafficRules()}
	case in.Capability == extensionv1.CapabilityAdmin && in.Operation == "observability.describe":
		output = config
	case in.Capability == extensionv1.CapabilityAdmin && in.Operation == "telemetry.read":
		if !config.TelemetryEnabled || host == nil {
			return extensionv1.Result{Code: "telemetry_unavailable", Message: "Account telemetry is unavailable", HTTPStatus: 503}, nil
		}
		var query extensionv1.AccountQuery
		if json.Unmarshal(in.Payload, &query) != nil || query.AccountID <= 0 {
			return extensionv1.Result{}, errors.New("invalid account telemetry request")
		}
		raw, _ := json.Marshal(extensionv1.AccountQuery{AccountID: query.AccountID})
		result, err := host.Call(ctx, extensionv1.HostInvocation{Operation: extensionv1.HostMetricsQuery, Payload: raw})
		if err != nil || result.Code != "" {
			return extensionv1.Result{Code: "telemetry_unavailable", Message: "Account telemetry is unavailable", HTTPStatus: 503}, nil
		}
		var snapshot extensionv1.AccountTrafficSnapshot
		if json.Unmarshal(result.Payload, &snapshot) != nil || snapshot.AccountID != query.AccountID {
			return extensionv1.Result{}, errors.New("invalid telemetry snapshot")
		}
		output = projectTraffic(snapshot)
	default:
		return extensionv1.Result{}, errors.New("unsupported observability operation")
	}
	raw, err := json.Marshal(output)
	return extensionv1.Result{Payload: raw}, err
}

func trafficRules() extensionv1.DecisionTable {
	eq := func(field, value string) extensionv1.DecisionCondition {
		return extensionv1.DecisionCondition{Field: field, Operator: "eq", Value: value}
	}
	num := func(field, op, value string) extensionv1.DecisionCondition {
		return extensionv1.DecisionCondition{Field: field, Operator: op, Value: value}
	}
	rule := func(result string, when ...extensionv1.DecisionCondition) extensionv1.DecisionRule {
		return extensionv1.DecisionRule{When: when, Result: result}
	}
	return extensionv1.DecisionTable{Default: "failed_other", Rules: []extensionv1.DecisionRule{
		rule("cancelled", eq("client_cancelled", "true")),
		rule("cancelled", eq("ws", "true"), eq("terminal", "response.cancelled")),
		rule("cancelled", eq("ws", "true"), eq("terminal", "response.incomplete")),
		rule("upstream_429", eq("has_error", "true"), eq("error_status", "429")),
		rule("upstream_5xx", eq("has_error", "true"), num("error_status", "gte", "500"), num("error_status", "lt", "600")),
		rule("failed_other", eq("has_error", "true")),
		rule("failed_other", eq("has_result", "false")),
		rule("completed_2xx", eq("ws", "false")),
		rule("completed_2xx", eq("terminal", "response.completed")),
		rule("completed_2xx", eq("terminal", "response.done")),
		rule("upstream_429", eq("terminal", "response.failed"), eq("terminal_status", "429")),
		rule("upstream_5xx", eq("terminal", "response.failed"), num("terminal_status", "gte", "500"), num("terminal_status", "lt", "600")),
	}}
}

func projectTraffic(snapshot extensionv1.AccountTrafficSnapshot) []extensionv1.AccountTrafficDisplay {
	rows := make([]extensionv1.AccountTrafficDisplay, 0, 2)
	for _, protocol := range []string{"http", "ws"} {
		counters, ok := snapshot.Protocols[protocol]
		if !ok {
			continue
		}
		finished := counters.Completed2xx + counters.Upstream429 + counters.Upstream5xx + counters.Cancelled + counters.FailedOther
		row := extensionv1.AccountTrafficDisplay{Protocol: protocol, AccountTrafficCounters: counters, Finished: finished, Unfinished: max(0, counters.Started-finished)}
		if finished > 0 {
			ratio := float64(counters.Completed2xx) / float64(finished)
			row.CompletionRate = &ratio
		}
		rows = append(rows, row)
	}
	return rows
}

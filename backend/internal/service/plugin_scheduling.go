package service

import (
	"encoding/json"
	"path"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

const PluginAccountProjectionKey = "plugin_account_projections"

type PluginAccountProjection struct {
	Identity     string                                      `json:"identity"`
	Scheduling   map[string]extensionv1.SchedulingConstraint `json:"scheduling"`
	Observations map[string]extensionv1.AccountObservation   `json:"observations,omitempty"`
}

// Scheduling uses a sanitized account projection and immutable plugin policy,
// so candidate filtering never launches a process RPC or reads ticket material.
func (m *PluginManager) SchedulingDecision(account *Account, model string, now time.Time) extensionv1.SchedulingDecision {
	allow := extensionv1.SchedulingDecision{Allowed: true}
	if m == nil || account == nil {
		return allow
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return extensionv1.SchedulingDecision{Reason: "plugin_registry_unavailable", Scope: model}
	}
	for id, installation := range registry.installations {
		if !pluginHasCapability(installation, extensionv1.CapabilityScheduling, account.Platform, account.Type) {
			continue
		}
		runtime := registry.runtimes[id]
		if registry.unavailable != "" || runtime == nil || runtime.draining.Load() || runtime.client.Exited() || !pluginDependenciesHealthy(installation, registry, map[int64]bool{}) {
			return extensionv1.SchedulingDecision{Reason: "plugin_unavailable", Scope: model}
		}
		rules := runtime.scheduling.Load()
		if rules == nil {
			return extensionv1.SchedulingDecision{Reason: "plugin_policy_unavailable", Scope: model}
		}
		projection := accountPluginProjection(account, installation.PluginKey)
		for _, rule := range *rules {
			if rule.ExcludeShadows && account.IsShadow() {
				continue
			}
			matched := false
			for _, pattern := range rule.Models {
				if pattern == model {
					matched = true
					break
				}
				if ok, _ := path.Match(pattern, model); ok {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			constraint, observed := projection.Scheduling[model]
			if !observed {
				constraint, observed = projection.Scheduling["*"]
			}
			if projection.Identity != CodexTicketAccountIdentity(account) {
				observed = false
			}
			if observed && constraint.Until != nil && !now.Before(*constraint.Until) {
				observed = false
			}
			if observed {
				if constraint.Effect == "deny" {
					return extensionv1.SchedulingDecision{Reason: constraint.Reason, Until: constraint.Until, Scope: model}
				}
				continue
			}
			if rule.Default == "deny" {
				return extensionv1.SchedulingDecision{Reason: rule.Reason, Scope: model}
			}
		}
	}
	return allow
}

func accountPluginProjection(account *Account, key string) PluginAccountProjection {
	var projection PluginAccountProjection
	if account == nil {
		return projection
	}
	if values, ok := account.Extra[PluginAccountProjectionKey].(map[string]any); ok {
		if raw, err := json.Marshal(values[key]); err == nil {
			_ = json.Unmarshal(raw, &projection)
		}
	}
	return projection
}

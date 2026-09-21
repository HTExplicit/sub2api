package service

import (
	"sort"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Host-owned metadata, not a manifest or public plugin protocol field. An empty
// versioned binding list denies every account; absence retains legacy metadata.
type PluginContributionAccountScope struct {
	Version  int                       `json:"version"`
	Bindings []PluginContributionScope `json:"bindings"`
}

type PluginContributionScope struct {
	Platform       string `json:"platform"`
	AccountType    string `json:"account_type"`
	RolloutPercent int    `json:"rollout_percent"`
}

// A current persisted record cannot authorize flags read from an older applied
// configuration. Runtime installation/config publication uses this same lock;
// no storage query or RPC is performed while holding it.
func (m *PluginManager) contributionAppliedRevision(installation *PluginInstallation, runtime *pluginRuntime) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runtime == nil || runtime.configuring.Load() || !samePluginRuntime(installation, runtime.installation) ||
		installation.ConfigEncrypted != runtime.installation.ConfigEncrypted {
		return 0, false
	}
	return runtime.configRevision.Load(), true
}

func contributionRequiredCapabilities(contribution *extensionv1.Contribution) []string {
	if contribution == nil {
		return nil
	}
	var required []string
	add := func(capability string) {
		if capability == "" {
			return
		}
		for _, previous := range required {
			if previous == capability {
				return
			}
		}
		required = append(required, capability)
	}
	add(contribution.Capability)
	if (contribution.Action != "" || contribution.ResourceAction != nil || contribution.Slot == extensionv1.AccountViewSlot) && contribution.Permission == "admin" {
		add(extensionv1.CapabilityAdmin)
	}
	if contribution.Slot == "theme" {
		add(extensionv1.CapabilityUI)
	}
	return required
}

func intersectContributionScope(left, right string) (string, bool) {
	if left == right {
		return left, true
	}
	if left == "*" {
		return right, true
	}
	if right == "*" {
		return left, true
	}
	return "", false
}

func contributionEffectiveBindings(installation *PluginInstallation, contribution *extensionv1.Contribution) []PluginContributionScope {
	out := []PluginContributionScope{{Platform: "*", AccountType: "*", RolloutPercent: 100}}
	if installation == nil {
		return []PluginContributionScope{}
	}
	for _, capability := range contributionRequiredCapabilities(contribution) {
		next := map[[2]string]int{}
		for _, current := range out {
			for _, binding := range installation.Bindings {
				if !binding.Enabled || binding.Capability != capability || binding.RolloutPercent < 0 || binding.RolloutPercent > 100 {
					continue
				}
				platform, platformOK := intersectContributionScope(current.Platform, binding.Platform)
				kind, kindOK := intersectContributionScope(current.AccountType, binding.AccountType)
				if !platformOK || !kindOK {
					continue
				}
				key, percent := [2]string{platform, kind}, min(current.RolloutPercent, binding.RolloutPercent)
				if previous, exists := next[key]; !exists || percent > previous {
					next[key] = percent
				}
			}
		}
		out = make([]PluginContributionScope, 0, len(next))
		for key, percent := range next {
			out = append(out, PluginContributionScope{Platform: key[0], AccountType: key[1], RolloutPercent: percent})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		return out[i].AccountType < out[j].AccountType
	})
	return out
}

func pluginContributionBindingsEnabled(installation *PluginInstallation, contribution *extensionv1.Contribution) bool {
	if installation == nil || contribution == nil {
		return false
	}
	if contribution.Slot == "theme" || contribution.AllAccounts {
		if contribution.AllAccounts && contribution.Capability == "" {
			return false
		}
		for _, binding := range contributionEffectiveBindings(installation, contribution) {
			if binding.Platform == "*" && binding.AccountType == "*" && binding.RolloutPercent == 100 {
				return true
			}
		}
		return false
	}
	// Old optional-capability contributions keep their existing page admission.
	// Execution still has its intrinsic Admin gate; no business dependency is guessed.
	if contribution.Capability == "" {
		return hasEnabledPluginBinding(installation.Bindings)
	}
	for _, capability := range contributionRequiredCapabilities(contribution) {
		found := false
		for _, binding := range installation.Bindings {
			if binding.Enabled && binding.Capability == capability {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func contributionAccountAllowed(installation *PluginInstallation, contribution *extensionv1.Contribution, account extensionv1.Account) bool {
	if account.ID <= 0 || !extensionv1.AccountMatchesFilter(account, contribution.AccountFilter) {
		return false
	}
	for _, capability := range contributionRequiredCapabilities(contribution) {
		if !pluginHasInvocationCapability(installation, extensionv1.Invocation{Capability: capability, AccountID: account.ID}, account.Platform, account.Type) {
			return false
		}
	}
	return true
}

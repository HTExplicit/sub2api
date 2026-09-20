package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func (m *PluginManager) InvokeAdminExtension(ctx context.Context, id, accountID int64, operation string, payload json.RawMessage) (extensionv1.Result, error) {
	platform, accountType, err := m.validateAdminExtension(ctx, id, accountID, operation)
	if err != nil {
		return extensionv1.Result{}, err
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(payload, &body) != nil || body == nil {
		return extensionv1.Result{}, errors.New("object payload required")
	}
	if accountID > 0 {
		body["account_id"], _ = json.Marshal(accountID)
	} else {
		delete(body, "account_id")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return extensionv1.Result{}, err
	}
	return m.InvokeExtension(ctx, id, platform, accountType, extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: operation, Payload: raw})
}

func (m *PluginManager) ValidateAdminExtension(ctx context.Context, id, accountID int64, operation string) error {
	_, _, err := m.validateAdminExtension(ctx, id, accountID, operation)
	return err
}

func (m *PluginManager) validateAdminExtension(ctx context.Context, id, accountID int64, operation string) (string, string, error) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return "", "", errors.New("plugin registry unavailable")
	}
	installation := registry.installations[id]
	if installation == nil || !hasEnabledPluginBinding(installation.Bindings) {
		return "", "", errors.New("plugin disabled")
	}
	declared := false
	var accountFilter *extensionv1.AccountFilter
	for _, contribution := range installation.Manifest.Contributions {
		if contribution.Action == operation && contribution.Permission == "admin" {
			if strings.HasPrefix(contribution.Slot, "account.") && accountID <= 0 {
				return "", "", errors.New("account context required")
			}
			declared = true
			accountFilter = contribution.AccountFilter
			break
		}
	}
	if !declared {
		return "", "", errors.New("undeclared plugin admin operation")
	}
	platform, accountType := "*", "*"
	if accountID == 0 {
		for _, binding := range installation.Bindings {
			if binding.Enabled && binding.Capability == extensionv1.CapabilityAdmin {
				platform, accountType = binding.Platform, binding.AccountType
				break
			}
		}
	}
	if accountID > 0 {
		directory, ok := m.accountDirectory.(PluginExtensionAccountDirectory)
		if !ok {
			return "", "", errors.New("account directory unavailable")
		}
		account, err := directory.ReadExtensionAccount(ctx, accountID)
		if err != nil || account == nil {
			return "", "", errors.New("account unavailable")
		}
		platform, accountType = account.Platform, account.Type
		if !extensionv1.AccountMatchesFilter(*account, accountFilter) {
			return "", "", errors.New("plugin action is not available for this account")
		}
	}
	if !pluginHasCapability(installation, extensionv1.CapabilityAdmin, platform, accountType) {
		return "", "", errors.New("plugin admin capability outside scope")
	}
	runtime := registry.runtimes[id]
	if runtime == nil || runtime.draining.Load() || runtime.client.Exited() {
		return "", "", errors.New("plugin runtime unavailable")
	}
	return platform, accountType, nil
}

type pluginExtensionRegistry struct {
	installations map[int64]*PluginInstallation
	runtimes      map[int64]*pluginRuntime
	unavailable   string
}

func validatePluginRegistry(installations []*PluginInstallation) error {
	exclusive := make(map[string]int64)
	type scopedOperation struct {
		owner                                   int64
		capability, name, platform, accountType string
	}
	var operationOwners []scopedOperation
	byID := make(map[int64]*PluginInstallation)
	for _, installation := range installations {
		if !hasEnabledPluginBinding(installation.Bindings) {
			continue
		}
		byID[installation.ID] = installation
		for _, binding := range installation.Bindings {
			if !binding.Enabled {
				continue
			}
			for _, name := range installation.Manifest.Operations[binding.Capability] {
				for _, owner := range operationOwners {
					if owner.owner != installation.ID && owner.capability == binding.Capability && owner.name == name &&
						(pluginScopeMatches(owner.platform, binding.Platform) || pluginScopeMatches(binding.Platform, owner.platform)) &&
						(pluginScopeMatches(owner.accountType, binding.AccountType) || pluginScopeMatches(binding.AccountType, owner.accountType)) {
						return fmt.Errorf("extension operation has overlapping enabled owners: %s", name)
					}
				}
				operationOwners = append(operationOwners, scopedOperation{installation.ID, binding.Capability, name, binding.Platform, binding.AccountType})
			}
		}
		for _, binding := range installation.Bindings {
			if !binding.Enabled || (binding.Capability != PluginCapabilityOpenAIOAuthOutbound && binding.Capability != extensionv1.CapabilityProvider) {
				continue
			}
			key := binding.Capability + "/" + binding.Platform + "/" + binding.AccountType
			if owner, ok := exclusive[key]; ok && owner != installation.ID {
				return fmt.Errorf("exclusive plugin capability already enabled: %s", key)
			}
			exclusive[key] = installation.ID
		}
		if err := pluginDependenciesReady(installation, installations); err != nil {
			return err
		}
	}
	visiting, done := make(map[int64]bool), make(map[int64]bool)
	var visit func(int64) error
	visit = func(id int64) error {
		if visiting[id] {
			return errors.New("plugin dependency cycle")
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range byID[id].Manifest.Dependencies {
			for target, candidate := range byID {
				if target == id {
					continue
				}
				for _, binding := range candidate.Bindings {
					if binding.Enabled && binding.Capability == dependency.Capability && (dependency.Platform == "" || pluginScopeMatches(binding.Platform, dependency.Platform)) && (dependency.AccountType == "" || pluginScopeMatches(binding.AccountType, dependency.AccountType)) {
						if err := visit(target); err != nil {
							return err
						}
					}
				}
			}
		}
		visiting[id] = false
		done[id] = true
		return nil
	}
	for id := range byID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func pluginDependenciesHealthy(installation *PluginInstallation, registry *pluginExtensionRegistry, seen map[int64]bool) bool {
	if installation == nil || seen[installation.ID] {
		return false
	}
	seen[installation.ID] = true
	defer delete(seen, installation.ID)
	for _, dependency := range installation.Manifest.Dependencies {
		ready := false
		for id, candidate := range registry.installations {
			if id == installation.ID {
				continue
			}
			runtime := registry.runtimes[id]
			if runtime == nil || runtime.draining.Load() || runtime.client.Exited() {
				continue
			}
			for _, binding := range candidate.Bindings {
				if binding.Enabled && binding.Capability == dependency.Capability && (dependency.Platform == "" || pluginScopeMatches(binding.Platform, dependency.Platform)) && (dependency.AccountType == "" || pluginScopeMatches(binding.AccountType, dependency.AccountType)) && pluginDependenciesHealthy(candidate, registry, seen) {
					ready = true
				}
			}
		}
		if !ready {
			return false
		}
	}
	return true
}

type PluginContribution struct {
	PackageSHA256 string `json:"package_sha256,omitempty"`
	extensionv1.Contribution
	StylesheetURL string `json:"stylesheet_url,omitempty"`
	PluginID      int64  `json:"plugin_id"`
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
}

func hasEnabledPluginBinding(bindings []PluginBinding) bool {
	for _, binding := range bindings {
		if binding.Enabled {
			return true
		}
	}
	return false
}

func pluginScopeMatches(wanted, actual string) bool { return wanted == "*" || wanted == actual }

func pluginHasCapability(installation *PluginInstallation, capability, platform, accountType string) bool {
	if installation == nil {
		return false
	}
	for _, binding := range installation.Bindings {
		if binding.Enabled && binding.Capability == capability &&
			pluginScopeMatches(binding.Platform, platform) && pluginScopeMatches(binding.AccountType, accountType) {
			return true
		}
	}
	return false
}

func pluginDependenciesReady(installation *PluginInstallation, all []*PluginInstallation) error {
	for _, dependency := range installation.Manifest.Dependencies {
		found := false
		for _, candidate := range all {
			if candidate.ID == installation.ID {
				continue
			}
			for _, binding := range candidate.Bindings {
				if binding.Enabled && binding.Capability == dependency.Capability &&
					(dependency.Platform == "" || pluginScopeMatches(binding.Platform, dependency.Platform)) &&
					(dependency.AccountType == "" || pluginScopeMatches(binding.AccountType, dependency.AccountType)) {
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("missing enabled plugin dependency %s", dependency.Capability)
		}
	}
	return nil
}

func (m *PluginManager) Contributions() []PluginContribution {
	registry := m.extensions.Load()
	if registry == nil {
		return nil
	}
	var out []PluginContribution
	for id, installation := range registry.installations {
		if !hasEnabledPluginBinding(installation.Bindings) {
			continue
		}
		runtime := registry.runtimes[id]
		available := registry.unavailable == "" && runtime != nil && !runtime.draining.Load() && !runtime.client.Exited() && pluginDependenciesHealthy(installation, registry, map[int64]bool{})
		for _, contribution := range installation.Manifest.Contributions {
			if contribution.Capability != "" {
				enabled := false
				for _, binding := range installation.Bindings {
					if binding.Enabled && binding.Capability == contribution.Capability {
						enabled = true
						break
					}
				}
				if !enabled {
					continue
				}
			}
			if contribution.Slot == "theme" && !pluginHasCapability(installation, extensionv1.CapabilityUI, "*", "*") {
				continue
			}
			flag, known := m.contributionConfigured(installation, runtime, contribution.ConfigFlag)
			if known && !flag {
				continue
			}
			item := PluginContribution{Contribution: contribution, PluginID: id, Available: available, PackageSHA256: installation.PackageSHA256}
			if !available || !known {
				item.Available = false
				item.Reason = "plugin_unavailable"
			}
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		if out[i].PluginID != out[j].PluginID {
			return out[i].PluginID < out[j].PluginID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *PluginManager) contributionConfigured(installation *PluginInstallation, runtime *pluginRuntime, flag string) (bool, bool) {
	if flag == "" {
		return true, true
	}
	var raw json.RawMessage
	if runtime != nil {
		if snapshot := runtime.configSnapshot.Load(); snapshot != nil {
			raw = *snapshot
		}
	}
	if len(raw) == 0 && m.encryptor != nil {
		var err error
		raw, err = m.decryptConfig(installation)
		if err != nil {
			return false, false
		}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false, false
	}
	var enabled bool
	value, exists := fields[flag]
	if !exists {
		return false, true
	}
	if json.Unmarshal(value, &enabled) != nil {
		return false, false
	}
	return enabled, true
}

// Public metadata never includes an admin operation, iframe entrypoint or
// account filter. User surfaces execute through existing authenticated APIs.
func (m *PluginManager) PublicContributions() []PluginContribution {
	out := make([]PluginContribution, 0)
	for _, item := range m.Contributions() {
		isTheme := item.Permission == "public" && item.Slot == "theme"
		if !isTheme && (item.Permission != "user" || item.Slot != "surface") {
			continue
		}
		if isTheme {
			installation, _ := m.installedByID(item.PluginID)
			if installation == nil {
				continue
			}
			item.StylesheetURL = publicThemeAssetURL(installation, item.Entrypoint)
		}
		item.Action, item.Entrypoint, item.ConfigFlag = "", "", ""
		item.Fields, item.AccountFilter, item.Assets = nil, nil, nil
		out = append(out, item)
	}
	return out
}

func (m *PluginManager) InvokeExtension(ctx context.Context, id int64, platform, accountType string, in extensionv1.Invocation) (extensionv1.Result, error) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return extensionv1.Result{}, errors.New("plugin registry unavailable")
	}
	installation := registry.installations[id]
	if !pluginHasCapability(installation, in.Capability, platform, accountType) {
		return extensionv1.Result{}, errors.New("plugin capability is disabled or outside its scope")
	}
	if !pluginDependenciesHealthy(installation, registry, map[int64]bool{}) {
		return extensionv1.Result{}, errors.New("plugin dependency unavailable")
	}
	runtime := registry.runtimes[id]
	if runtime == nil || runtime.extension == nil || runtime.client.Exited() || !runtime.beginRequest() {
		return extensionv1.Result{}, errors.New("enabled plugin is unavailable")
	}
	defer runtime.finishRequest()
	result, err := runtime.extension.Invoke(ctx, in)
	result.PluginID = id
	return result, err
}

func (m *PluginManager) publishExtensionRegistryLocked(installations []*PluginInstallation, unavailable string) {
	registry := &pluginExtensionRegistry{installations: make(map[int64]*PluginInstallation), runtimes: make(map[int64]*pluginRuntime), unavailable: unavailable}
	for _, installation := range installations {
		copy := *installation
		copy.DesiredEnabled = hasEnabledPluginBinding(copy.Bindings)
		registry.installations[copy.ID] = &copy
		if copy.DesiredEnabled {
			registry.runtimes[copy.ID] = m.runtimes[copy.ID]
		}
	}
	m.extensions.Store(registry)
}

func (m *PluginManager) replaceExtensionRegistrationLocked(installation *PluginInstallation, runtime *pluginRuntime) {
	registry := &pluginExtensionRegistry{installations: make(map[int64]*PluginInstallation), runtimes: make(map[int64]*pluginRuntime)}
	if previous := m.extensions.Load(); previous != nil {
		registry.unavailable = previous.unavailable
		for id, item := range previous.installations {
			registry.installations[id] = item
		}
		for id, item := range previous.runtimes {
			registry.runtimes[id] = item
		}
	}
	copy := *installation
	copy.DesiredEnabled = hasEnabledPluginBinding(copy.Bindings)
	registry.installations[copy.ID] = &copy
	if runtime == nil {
		delete(registry.runtimes, copy.ID)
	} else {
		registry.runtimes[copy.ID] = runtime
	}
	m.extensions.Store(registry)
}

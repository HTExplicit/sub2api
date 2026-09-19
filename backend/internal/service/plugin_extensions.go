package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

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
	for _, contribution := range installation.Manifest.Contributions {
		if contribution.Action == operation && contribution.Permission == "admin" {
			declared = true
			break
		}
	}
	if !declared {
		return "", "", errors.New("undeclared plugin admin operation")
	}
	platform, accountType := "*", "*"
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

type PluginContribution struct {
	extensionv1.Contribution
	PluginID  int64  `json:"plugin_id"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
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
		available := registry.unavailable == "" && runtime != nil && !runtime.draining.Load() && !runtime.client.Exited()
		for _, contribution := range installation.Manifest.Contributions {
			item := PluginContribution{Contribution: contribution, PluginID: id, Available: available}
			if !available {
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

func (m *PluginManager) InvokeExtension(ctx context.Context, id int64, platform, accountType string, in extensionv1.Invocation) (extensionv1.Result, error) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return extensionv1.Result{}, errors.New("plugin registry unavailable")
	}
	installation := registry.installations[id]
	if !pluginHasCapability(installation, in.Capability, platform, accountType) {
		return extensionv1.Result{}, errors.New("plugin capability is disabled or outside its scope")
	}
	runtime := registry.runtimes[id]
	if runtime == nil || runtime.extension == nil || runtime.client.Exited() || !runtime.beginRequest() {
		return extensionv1.Result{}, errors.New("enabled plugin is unavailable")
	}
	defer runtime.finishRequest()
	return runtime.extension.Invoke(ctx, in)
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

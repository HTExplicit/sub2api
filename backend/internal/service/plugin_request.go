package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func (m *PluginManager) ApplyRequestHeaders(ctx context.Context, account *Account, model string, headers http.Header) error {
	if m == nil || account == nil || headers == nil {
		return nil
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return errors.New("plugin registry unavailable")
	}
	var ids []int64
	for id, installation := range registry.installations {
		operations, explicit := installation.Manifest.Operations[extensionv1.CapabilityRequest]
		if (!explicit || slices.Contains(operations, "inject")) && pluginHasCapability(installation, extensionv1.CapabilityRequest, account.Platform, account.Type) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	raw, err := json.Marshal(extensionv1.SchedulingRequest{Account: *extensionAccount(account), Model: model, Now: time.Now().UTC()})
	if err != nil {
		return err
	}
	for _, id := range ids {
		result, err := m.InvokeExtension(ctx, id, account.Platform, account.Type, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "inject", Payload: raw})
		if err != nil {
			return err
		}
		if result.Code != "" {
			return errors.New("plugin request prerequisite unavailable")
		}
		var changes struct {
			Headers map[string]string `json:"headers"`
		}
		if json.Unmarshal(result.Payload, &changes) != nil {
			return errors.New("invalid plugin header result")
		}
		for name, value := range changes.Headers {
			headers.Set(name, value)
		}
	}
	return nil
}

func (m *PluginManager) installedByKey(key string) (*PluginInstallation, *pluginRuntime) {
	if m == nil {
		return nil, nil
	}
	registry := m.extensions.Load()
	if registry == nil {
		return nil, nil
	}
	for id, installation := range registry.installations {
		if installation.PluginKey == key {
			return installation, registry.runtimes[id]
		}
	}
	return nil, nil
}

func (m *PluginManager) activeConfig(key string) (json.RawMessage, bool) {
	installation, runtime := m.installedByKey(key)
	if installation == nil || !hasEnabledPluginBinding(installation.Bindings) || runtime == nil {
		return nil, false
	}
	config := runtime.configSnapshot.Load()
	if config == nil {
		return nil, false
	}
	return append(json.RawMessage(nil), (*config)...), true
}

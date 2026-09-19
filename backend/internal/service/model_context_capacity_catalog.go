package service

import (
	"context"
	"encoding/json"
	"errors"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"sort"
	"strconv"
	"sync/atomic"
	"time"
)

type extensionCatalogProvider struct{ resolver extensionv1.CatalogResolver }

var processExtensionCatalog atomic.Pointer[extensionCatalogProvider]

func lookupExtensionCatalog(query extensionv1.CatalogQuery) *OfficialModelContextCapacity {
	provider := processExtensionCatalog.Load()
	if provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := provider.resolver.ResolveCatalog(ctx, query)
	if err != nil {
		return nil
	}
	return result.Entry
}
func (m *PluginManager) ResolveCatalog(ctx context.Context, query extensionv1.CatalogQuery) (extensionv1.CatalogMatch, error) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return extensionv1.CatalogMatch{}, errors.New("model catalog unavailable")
	}
	var ids []int64
	for id, installation := range registry.installations {
		if pluginHasCapability(installation, extensionv1.CapabilityCatalog, query.Platform, query.AccountType) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	raw, err := json.Marshal(query)
	if err != nil {
		return extensionv1.CatalogMatch{}, err
	}
	var resolved extensionv1.CatalogMatch
	for _, id := range ids {
		runtime := registry.runtimes[id]
		if runtime == nil || runtime.draining.Load() || runtime.client.Exited() || !pluginDependenciesHealthy(registry.installations[id], registry, map[int64]bool{}) {
			return extensionv1.CatalogMatch{}, errors.New("enabled model catalog unavailable")
		}
		cacheKey := strconv.FormatUint(runtime.configRevision.Load(), 10) + ":" + string(raw)
		var payload json.RawMessage
		if cached, ok := runtime.catalogCache.Load(cacheKey); ok {
			payload = cached.(json.RawMessage)
		} else {
			out, callErr := m.InvokeExtension(ctx, id, query.Platform, query.AccountType, extensionv1.Invocation{Capability: extensionv1.CapabilityCatalog, Operation: "resolve", Payload: raw})
			if callErr != nil || out.Code != "" {
				return extensionv1.CatalogMatch{}, errors.New("enabled model catalog unavailable")
			}
			payload = out.Payload
		}
		var match extensionv1.CatalogMatch
		if json.Unmarshal(payload, &match) != nil {
			return extensionv1.CatalogMatch{}, errors.New("invalid model catalog response")
		}
		if match.Entry != nil && runtime.catalogCacheSize.Load() < 2048 {
			if _, loaded := runtime.catalogCache.LoadOrStore(cacheKey, payload); !loaded {
				runtime.catalogCacheSize.Add(1)
			}
		}
		if !match.Matched {
			continue
		}
		if match.Entry == nil {
			return match, nil
		}
		if resolved.Entry != nil && resolved.Entry.ModelContextCapacity != match.Entry.ModelContextCapacity {
			return extensionv1.CatalogMatch{Matched: true}, nil
		}
		resolved = match
	}
	return resolved, nil
}

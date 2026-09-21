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
	if err := ctx.Err(); err != nil {
		return extensionv1.CatalogMatch{}, err
	}
	if query.AccountID < 0 {
		return extensionv1.CatalogMatch{}, errors.New("invalid model catalog account")
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return extensionv1.CatalogMatch{}, errors.New("model catalog unavailable")
	}
	invocation := extensionv1.Invocation{Capability: extensionv1.CapabilityCatalog, Operation: "resolve", AccountID: query.AccountID}
	var ids []int64
	for id, installation := range registry.installations {
		if pluginHasInvocationCapability(installation, invocation, query.Platform, query.AccountType) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	raw, err := json.Marshal(query)
	if err != nil {
		return extensionv1.CatalogMatch{}, err
	}
	invocation.Payload = raw
	var resolved extensionv1.CatalogMatch
	type catalogLease struct {
		ctx      context.Context
		runtime  *pluginRuntime
		revision uint64
	}
	var leases []catalogLease
	for _, id := range ids {
		runtime := registry.runtimes[id]
		if runtime == nil || runtime.client == nil || runtime.extension == nil || runtime.draining.Load() || runtime.configuring.Load() || runtime.client.Exited() || m.repo == nil {
			return extensionv1.CatalogMatch{}, errors.New("enabled model catalog unavailable")
		}
		// Recheck just this selected installation before consulting cached policy.
		// A stale local registry cannot authorize persisted disable/scope changes.
		current, err := m.repo.GetByID(ctx, id)
		if err != nil || !samePluginRuntime(current, registry.installations[id]) {
			return extensionv1.CatalogMatch{}, errors.New("enabled model catalog unavailable")
		}
		if current.State == PluginStateDisabled || !pluginHasInvocationCapability(current, invocation, query.Platform, query.AccountType) {
			continue
		}
		if current.State != PluginStateEnabled || !pluginDependenciesHealthy(current, registry, map[int64]bool{}) {
			return extensionv1.CatalogMatch{}, errors.New("enabled model catalog unavailable")
		}
		m.mu.Lock()
		configCurrent := samePluginRuntime(current, runtime.installation) && current.ConfigEncrypted == runtime.installation.ConfigEncrypted
		m.mu.Unlock()
		if !configCurrent {
			return extensionv1.CatalogMatch{}, errors.New("enabled model catalog unavailable")
		}
		bound, release, err := m.bindHostPolicyContext(ctx, current, runtime)
		if err != nil {
			return extensionv1.CatalogMatch{}, err
		}
		defer release()
		revision := runtime.configRevision.Load()
		leases = append(leases, catalogLease{bound, runtime, revision})
		cacheKey := strconv.FormatUint(revision, 10) + ":" + string(raw)
		var payload json.RawMessage
		if cached, ok := runtime.catalogCache.Load(cacheKey); ok {
			payload = cached.(json.RawMessage)
		} else {
			// This resolver already read the selected installation and holds its
			// exact business lease. Invoke that same runtime without a second
			// database read or a nested lifetime admission.
			admission := &pluginInvocationAdmission{ctx: bound, runtime: runtime, revision: revision, owner: id}
			out, callErr := admission.invoke(invocation)
			if callErr != nil || out.Code != "" {
				return extensionv1.CatalogMatch{}, errors.New("enabled model catalog unavailable")
			}
			payload = out.Payload
		}
		if err := bound.Err(); err != nil {
			return extensionv1.CatalogMatch{}, err
		}
		if runtime.configuring.Load() || runtime.configRevision.Load() != revision {
			return extensionv1.CatalogMatch{}, errors.New("model catalog configuration changed")
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
	for _, lease := range leases {
		if err := lease.ctx.Err(); err != nil {
			return extensionv1.CatalogMatch{}, err
		}
		if lease.runtime.configuring.Load() || lease.runtime.configRevision.Load() != lease.revision {
			return extensionv1.CatalogMatch{}, errors.New("model catalog configuration changed")
		}
	}
	return resolved, nil
}

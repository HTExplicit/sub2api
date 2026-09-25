package service

import (
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"strings"
)

// Input is an upstream target, never a public routing alias. In particular,
// A -> B and B -> C must not cause a second mapping when resolving A's capacity.
func capacityCanonicalUpstreamID(account *Account, model string) string {
	model = strings.TrimSpace(model)
	if account != nil && account.IsOpenAIApiKey() {
		if base, _, accepted := resolveOpenAIModelReasoningAlias(model); accepted {
			return base
		}
	}
	return model
}

// Legacy writes sometimes used public aliases. A real mapping target keeps its
// identity even when the same name is also a public alias elsewhere in the map.
func capacityOverrideTargetResolver(account *Account) func(string) string {
	mapping := account.GetModelMapping()
	targets := make(map[string]bool, len(mapping))
	for _, target := range mapping {
		targets[target] = true
	}
	return func(id string) string {
		if !targets[id] {
			if target, ok := mapping[id]; ok && validModelContextID(target) {
				id = target
			}
		}
		return capacityCanonicalUpstreamID(account, id)
	}
}

func resolveCapacityOverrides(account *Account) (map[string]int64, map[string]bool) {
	raw := modelContextOverridesFromExtra(account.Extra[ModelContextOverridesExtraKey])
	resolve := capacityOverrideTargetResolver(account)
	values := make(map[string]int64, len(raw))
	conflicts := make(map[string]bool)
	for id, value := range raw {
		target := resolve(id)
		if exact, ok := raw[target]; ok && resolve(target) == target {
			values[target] = exact
			continue
		}
		if previous, exists := values[target]; exists && previous != value {
			conflicts[target] = true
		}
		values[target] = value
	}
	for target := range conflicts {
		delete(values, target)
	}
	return values, conflicts
}

// accountCapacityObservations splits the persisted upstream model snapshot into
// this account's own upstream declarations and models.dev registry references,
// keyed by the recognized upstream identity. A real target's own entry wins
// over alias spellings; two alias spellings with different limits are a
// conflict, not a choice.
func accountCapacityObservations(account *Account) (upstream, registry map[string]ModelContextCapacity, conflicts map[string]bool) {
	upstream = make(map[string]ModelContextCapacity)
	registry = make(map[string]ModelContextCapacity)
	conflicts = make(map[string]bool)
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil {
		return upstream, registry, conflicts
	}
	valuesFor := func(source string) map[string]ModelContextCapacity {
		if source == ModelContextSourceRegistry {
			return registry
		}
		return upstream
	}
	exact := make(map[string]bool)
	for id, entry := range snapshot.Models {
		capacity, source := upstreamMetadataCapacity(snapshot.Source, entry)
		if source == "" || !validModelContextID(id) || capacityCanonicalUpstreamID(account, id) != id {
			continue
		}
		valuesFor(source)[id] = capacity
		exact[source+"\x00"+id] = true
	}
	for id, entry := range snapshot.Models {
		capacity, source := upstreamMetadataCapacity(snapshot.Source, entry)
		target := capacityCanonicalUpstreamID(account, id)
		if source == "" || !validModelContextID(id) || target == id || exact[source+"\x00"+target] {
			continue
		}
		values := valuesFor(source)
		if previous, exists := values[target]; exists && !sameModelContextLimits(previous, capacity) {
			conflicts[target] = true
		}
		values[target] = capacity
	}
	for target := range conflicts {
		delete(upstream, target)
		delete(registry, target)
	}
	return upstream, registry, conflicts
}

// upstreamMetadataCapacity returns one snapshot entry's capacity and whose
// declaration it is. An entry without per-model provenance counts as the
// upstream's only when the snapshot saw no registry enrichment.
func upstreamMetadataCapacity(snapshotSource string, entry UpstreamModelMetadata) (ModelContextCapacity, string) {
	capacity := sanitizeModelContextCapacity(ModelContextCapacity{
		ContextWindow: entry.ContextWindow, MaxContextWindow: entry.MaxContextWindow,
		MaxInputTokens: entry.MaxInputTokens, MaxOutputTokens: entry.MaxOutputTokens,
		ObservedAt: entry.ObservedAt,
	})
	if !modelContextCapacityHasLimits(capacity) {
		return ModelContextCapacity{}, ""
	}
	switch entry.CapacitySource {
	case ModelContextSourceUpstream, ModelContextSourceRegistry:
		return capacity, entry.CapacitySource
	}
	if snapshotSource == ModelContextSourceUpstream {
		return capacity, ModelContextSourceUpstream
	}
	return capacity, ModelContextSourceRegistry
}

// Explicit edits address one logical upstream model and remove its obsolete
// spelling keys. Unedited values are preserved, including unresolved conflicts.
func ApplyAccountModelContextOverrides(account *Account, existing any, patch map[string]*int64) (map[string]int64, error) {
	if err := ValidateModelContextOverrides(account, patch); err != nil {
		return nil, err
	}
	resolve := capacityOverrideTargetResolver(account)
	normalized := make(map[string]*int64, len(patch))
	for id, value := range patch {
		target := resolve(id)
		if previous, exists := normalized[target]; exists && ((previous == nil) != (value == nil) || (previous != nil && *previous != *value)) {
			return nil, infraerrors.BadRequest("MODEL_CONTEXT_OVERRIDE_CONFLICT", "aliases of the same upstream model have conflicting context overrides")
		}
		normalized[target] = value
	}
	values := modelContextOverridesFromExtra(existing)
	for id := range values {
		if _, edited := normalized[resolve(id)]; edited {
			delete(values, id)
		}
	}
	return ApplyModelContextOverrides(values, normalized)
}

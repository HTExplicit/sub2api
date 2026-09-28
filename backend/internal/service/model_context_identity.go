package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
)

// UpstreamModelMetadataSourceIdentity binds observations to the provider and
// endpoint contract that produced them. Credentials, proxies and public model
// aliases are deliberately absent: changing those does not change the source.
func UpstreamModelMetadataSourceIdentity(account *Account) string {
	if account == nil {
		return ""
	}
	identity := struct {
		Platform, Type, Mode, Protocol, CatalogBase, AnthropicBase string
		ResponsesMode, ResponsesSupport                            string
		ProtocolBases                                              map[string]string
	}{
		Platform:      strings.ToLower(strings.TrimSpace(account.Platform)),
		Type:          strings.ToLower(strings.TrimSpace(account.Type)),
		Mode:          strings.ToLower(strings.TrimSpace(account.GetCredential("account_mode"))),
		Protocol:      account.GetAPIProtocol(),
		CatalogBase:   normalizeCapacitySourceURL(upstreamModelRegistryBaseURL(account)),
		AnthropicBase: normalizeCapacitySourceURL(account.GetAnthropicProtocolBaseURL()),
	}
	if account.IsOpenAI() {
		identity.ResponsesMode = string(openai_compat.NormalizeResponsesSupportMode(account.GetExtraString(openai_compat.ExtraKeyResponsesMode)))
		switch openai_compat.ResolveResponsesSupport(account.Extra) {
		case openai_compat.ResponsesSupportYes:
			identity.ResponsesSupport = "yes"
		case openai_compat.ResponsesSupportNo:
			identity.ResponsesSupport = "no"
		default:
			identity.ResponsesSupport = "unknown"
		}
	}
	if account.IsOpenAIOAuth() {
		identity.CatalogBase = normalizeCapacitySourceURL(chatgptCodexModelsURL)
	}
	if account.IsAnthropic() && account.IsOAuth() {
		identity.CatalogBase = "https://api.anthropic.com"
	}
	if account.IsMultiProtocolAPIKey() {
		identity.ProtocolBases = make(map[string]string)
		for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAnthropic} {
			identity.ProtocolBases[protocol] = normalizeCapacitySourceURL(account.GetCNProtocolBaseURL(protocol))
		}
	}
	body, _ := json.Marshal(identity)
	digest := sha256.Sum256(body)
	return "v1:" + hex.EncodeToString(digest[:])
}

func normalizeCapacitySourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	host, port := strings.ToLower(parsed.Hostname()), parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	parsed.Host = host
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	}
	parsed.User, parsed.Fragment, parsed.RawFragment = nil, "", ""
	// url.Parse keeps an escaped path in RawPath. Preserve it so a reserved
	// slash (%2F) cannot become the same identity as a path separator.
	if parsed.RawPath == "" {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	parsed.RawQuery = parsed.Query().Encode()
	return parsed.String()
}

func upstreamModelMetadataSourceMatches(account *Account, snapshot *UpstreamModelMetadataSnapshot) bool {
	return snapshot != nil && snapshot.SourceIdentity != "" && snapshot.SourceIdentity == UpstreamModelMetadataSourceIdentity(account)
}

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
	return accountCapacityObservationsWithSourceCheck(account, true)
}

// Raw observations remain available to administrators after an endpoint edit
// or when a legacy snapshot has no source binding. They are not current evidence.
func accountRawCapacityObservations(account *Account) (upstream, registry map[string]ModelContextCapacity, conflicts map[string]bool) {
	return accountCapacityObservationsWithSourceCheck(account, false)
}

func accountCapacityObservationsWithSourceCheck(account *Account, checkSource bool) (upstream, registry map[string]ModelContextCapacity, conflicts map[string]bool) {
	upstream = make(map[string]ModelContextCapacity)
	registry = make(map[string]ModelContextCapacity)
	conflicts = make(map[string]bool)
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil {
		return upstream, registry, conflicts
	}
	usableSource := !checkSource || upstreamModelMetadataSourceMatches(account, snapshot)
	valuesFor := func(source string) map[string]ModelContextCapacity {
		if source == ModelContextSourceRegistry {
			return registry
		}
		return upstream
	}
	exact := make(map[string]bool)
	for id, entry := range snapshot.Models {
		capacity, source := upstreamMetadataCapacity(snapshot.Source, entry)
		if !usableSource {
			continue
		}
		if source == "" || !validModelContextID(id) || capacityCanonicalUpstreamID(account, id) != id {
			continue
		}
		valuesFor(source)[id] = capacity
		exact[source+"\x00"+id] = true
	}
	for id, entry := range snapshot.Models {
		capacity, source := upstreamMetadataCapacity(snapshot.Source, entry)
		if !usableSource {
			continue
		}
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

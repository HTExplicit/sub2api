package service

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	MaxSafeModelContextTokens        int64 = 9007199254740991
	ModelContextOverridesExtraKey          = "model_context_overrides"
	ModelContextCapacityBasisTotal         = "total_context"
	ModelContextCapacityBasisInput         = "input_limit"
	ModelContextCapacityBasisMaximum       = "max_context_window"
)

// Capacity evidence, highest priority first. The order is the same for every
// account and host: an admin override, what this account's own upstream
// declared, the release-pinned reference catalog, then the models.dev
// registry. There is no default: an unknown capacity is never advertised.
const (
	ModelContextSourceCustom   = "custom"
	ModelContextSourceUpstream = "upstream"
	ModelContextSourceOfficial = "official"
	ModelContextSourceRegistry = "registry"
)

// ModelContextCapacity keeps an upstream's distinct limits intact. A zero is
// unknown, never a promise of an unlimited window.
type ModelContextCapacity = extensionv1.ModelContextCapacity

// ModelContextCapacityReference preserves the raw product reference of a
// catalog entry.
type ModelContextCapacityReference = extensionv1.ModelContextCapacityReference

// OfficialModelContextCapacity is release-owned evidence. Match constraints and
// reference values are not client-writeable.
type OfficialModelContextCapacity = extensionv1.OfficialModelContextCapacity

// ResolvedModelContextCapacity is one answer for one model. An empty Source
// means unknown; callers must not advertise it.
type ResolvedModelContextCapacity struct {
	ModelContextCapacity
	Source string `json:"source,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Known reports whether the answer carries a usable planning window.
func (capacity ResolvedModelContextCapacity) Known() bool {
	return capacity.Source != "" && validModelContextTokens(capacity.ContextWindow)
}

func unknownModelContextCapacity(reason string) ResolvedModelContextCapacity {
	return ResolvedModelContextCapacity{Reason: reason}
}

// AccountModelContextCapacityRow is the admin view of one real upstream model:
// every piece of evidence plus the automatic and effective answers. Every
// account accepts overrides, so Editable is always true.
type AccountModelContextCapacityRow struct {
	UpstreamModelID        string                        `json:"upstream_model_id"`
	Aliases                []string                      `json:"aliases"`
	UpstreamModelIDs       []string                      `json:"upstream_model_ids,omitempty"`
	Editable               bool                          `json:"editable"`
	Upstream               *ModelContextCapacity         `json:"upstream,omitempty"`
	Official               *OfficialModelContextCapacity `json:"official,omitempty"`
	Registry               *ModelContextCapacity         `json:"registry,omitempty"`
	CustomContextWindow    *int64                        `json:"custom_context_window,omitempty"`
	AutomaticContextWindow int64                         `json:"automatic_context_window"`
	AutomaticSource        string                        `json:"automatic_source"`
	EffectiveContextWindow int64                         `json:"effective_context_window"`
	EffectiveSource        string                        `json:"effective_source"`
	CapacityBasis          string                        `json:"capacity_basis"`
	MaxContextWindow       int64                         `json:"max_context_window,omitempty"`
	MaxInputTokens         int64                         `json:"max_input_tokens,omitempty"`
	MaxOutputTokens        int64                         `json:"max_output_tokens,omitempty"`
	Reason                 string                        `json:"reason,omitempty"`
}

func validModelContextTokens(value int64) bool {
	return value > 0 && value <= MaxSafeModelContextTokens
}

type modelContextEvidence struct {
	source   string
	capacity ModelContextCapacity
}

// resolveModelContextEvidence takes the planning window (context_window as the
// default working window, max_context_window as the real maximum) from the
// highest-priority evidence that states one. Independent input/output limits
// keep the first compatible declaration in the same priority order; they are
// never synthesized from the context window.
func resolveModelContextEvidence(items []modelContextEvidence) ResolvedModelContextCapacity {
	for _, item := range items {
		planning, ok := modelContextPlanningCapacity(item.capacity)
		if !ok {
			continue
		}
		result := ResolvedModelContextCapacity{ModelContextCapacity: planning, Source: item.source}
		compatible := func(limit func(ModelContextCapacity) int64) int64 {
			for _, candidate := range items {
				if value := limit(sanitizeModelContextCapacity(candidate.capacity)); value > 0 && value <= result.ContextWindow {
					return value
				}
			}
			return 0
		}
		result.MaxInputTokens = compatible(func(value ModelContextCapacity) int64 { return value.MaxInputTokens })
		result.MaxOutputTokens = compatible(func(value ModelContextCapacity) int64 { return value.MaxOutputTokens })
		return result
	}
	return unknownModelContextCapacity("no_capacity_evidence")
}

func modelContextPlanningCapacity(raw ModelContextCapacity) (ModelContextCapacity, bool) {
	value := sanitizeModelContextCapacity(raw)
	switch {
	case value.ContextWindow > 0:
		value.CapacityBasis = ModelContextCapacityBasisTotal
	case value.MaxInputTokens > 0:
		value.ContextWindow = value.MaxInputTokens
		value.CapacityBasis = ModelContextCapacityBasisInput
	case value.MaxContextWindow > 0:
		value.ContextWindow = value.MaxContextWindow
		value.CapacityBasis = ModelContextCapacityBasisMaximum
	default:
		return ModelContextCapacity{}, false
	}
	// A declared maximum below the default window caps it: a client never
	// plans beyond the real maximum.
	if value.MaxContextWindow > 0 && value.MaxContextWindow < value.ContextWindow {
		value.ContextWindow = value.MaxContextWindow
	}
	if value.MaxContextWindow < value.ContextWindow {
		value.MaxContextWindow = value.ContextWindow
	}
	return value, true
}

func sanitizeModelContextCapacity(value ModelContextCapacity) ModelContextCapacity {
	for _, field := range []*int64{&value.ContextWindow, &value.MaxContextWindow, &value.MaxInputTokens, &value.MaxOutputTokens} {
		if !validModelContextTokens(*field) {
			*field = 0
		}
	}
	switch value.CapacityBasis {
	case ModelContextCapacityBasisTotal, ModelContextCapacityBasisInput, ModelContextCapacityBasisMaximum:
	default:
		value.CapacityBasis = ""
	}
	return value
}

func modelContextCapacityHasLimits(value ModelContextCapacity) bool {
	return validModelContextTokens(value.ContextWindow) || validModelContextTokens(value.MaxContextWindow) ||
		validModelContextTokens(value.MaxInputTokens) || validModelContextTokens(value.MaxOutputTokens)
}

func sameModelContextLimits(left, right ModelContextCapacity) bool {
	return left.ContextWindow == right.ContextWindow && left.MaxContextWindow == right.MaxContextWindow &&
		left.MaxInputTokens == right.MaxInputTokens && left.MaxOutputTokens == right.MaxOutputTokens
}

func validModelContextID(modelID string) bool {
	if modelID == "" || strings.TrimSpace(modelID) != modelID || len(modelID) > 512 || strings.Contains(modelID, "*") {
		return false
	}
	for _, ch := range modelID {
		if ch < 0x20 || ch == 0x7f {
			return false
		}
	}
	return true
}

// isMediaModelForCapacity identifies image, video and audio generators. They
// have no text-generation window, so no capacity is ever advertised for them.
func isMediaModelForCapacity(modelID string) bool {
	if isCodexDedicatedMediaModel(modelID) {
		return true
	}
	id := strings.ToLower(codexProviderQualifiedModelID(modelID))
	if colon := strings.IndexByte(id, ':'); colon >= 0 {
		id = id[:colon]
	}
	for _, token := range strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == '/' }) {
		switch token {
		case "image", "images", "imagine", "video", "audio", "tts", "whisper", "transcribe", "speech", "realtime",
			"sora", "veo", "dall", "seedance", "seedream", "kling", "hailuo", "music", "lyria":
			return true
		}
		if strings.HasPrefix(token, "imagen") {
			return true
		}
	}
	return false
}

// ValidateModelContextOverrides distinguishes omitted (nil map), no-op (empty
// map), and deletion (nil value at one model key). It never mutates the account;
// every account may carry overrides.
func ValidateModelContextOverrides(_ *Account, patch map[string]*int64) error {
	if patch == nil {
		return nil
	}
	return validateModelContextOverridesPatch(patch)
}

func validateModelContextOverridesPatch(patch map[string]*int64) error {
	for modelID, value := range patch {
		if !validModelContextID(modelID) {
			return infraerrors.BadRequest("INVALID_MODEL_CONTEXT_OVERRIDE", "model context override requires a concrete upstream model ID")
		}
		if value != nil && !validModelContextTokens(*value) {
			return infraerrors.BadRequest("INVALID_MODEL_CONTEXT_OVERRIDE", "model context override must be a positive safe integer")
		}
	}
	return nil
}

// ApplyModelContextOverrides is also used under the repository row lock. The
// returned map is fresh, and a patch cannot erase unrelated model overrides.
func ApplyModelContextOverrides(existing any, patch map[string]*int64) (map[string]int64, error) {
	if err := validateModelContextOverridesPatch(patch); err != nil {
		return nil, err
	}
	values := modelContextOverridesFromExtra(existing)
	for modelID, value := range patch {
		if value == nil {
			delete(values, modelID)
		} else {
			values[modelID] = *value
		}
	}
	return values, nil
}

func modelContextOverridesFromExtra(raw any) map[string]int64 {
	values := make(map[string]int64)
	body, err := json.Marshal(raw)
	if err != nil {
		return values
	}
	var stored map[string]json.RawMessage
	if json.Unmarshal(body, &stored) != nil {
		return values
	}
	for modelID, rawValue := range stored {
		if value := parsePositiveSafeModelContextTokens(rawValue); validModelContextID(modelID) && value > 0 {
			values[modelID] = value
		}
	}
	return values
}

// accountModelCapacityEvidence decodes one account's evidence once. Resolving a
// model performs no I/O and never mutates the account.
type accountModelCapacityEvidence struct {
	account          *Account
	overrides        map[string]int64
	overrideConflict map[string]bool
	upstream         map[string]ModelContextCapacity
	registry         map[string]ModelContextCapacity
	observedConflict map[string]bool
}

func newAccountModelCapacityEvidence(account *Account) *accountModelCapacityEvidence {
	evidence := &accountModelCapacityEvidence{account: account}
	evidence.overrides, evidence.overrideConflict = resolveCapacityOverrides(account)
	evidence.upstream, evidence.registry, evidence.observedConflict = accountCapacityObservations(account)
	return evidence
}

// resolve answers for a real upstream model ID. Without custom it returns the
// automatic answer an override would replace.
func (evidence *accountModelCapacityEvidence) resolve(modelID string, useCustom bool) ResolvedModelContextCapacity {
	account := evidence.account
	modelID = capacityCanonicalUpstreamID(account, modelID)
	if !validModelContextID(modelID) {
		return unknownModelContextCapacity("upstream_model_unresolved")
	}
	if isMediaModelForCapacity(modelID) {
		return unknownModelContextCapacity("media_model")
	}
	items := make([]modelContextEvidence, 0, 4)
	if value, ok := evidence.overrides[modelID]; ok && useCustom {
		items = append(items, modelContextEvidence{ModelContextSourceCustom, ModelContextCapacity{
			ContextWindow: value, MaxContextWindow: value, CapacityBasis: ModelContextCapacityBasisTotal,
		}})
	}
	if value, ok := evidence.upstream[modelID]; ok {
		items = append(items, modelContextEvidence{ModelContextSourceUpstream, value})
	}
	if official := LookupOfficialModelContextCapacity(account, modelID); official != nil {
		items = append(items, modelContextEvidence{ModelContextSourceOfficial, official.ModelContextCapacity})
	}
	if value, ok := evidence.registry[modelID]; ok {
		items = append(items, modelContextEvidence{ModelContextSourceRegistry, value})
	}
	result := resolveModelContextEvidence(items)
	if useCustom && evidence.overrideConflict[modelID] {
		result.Reason = "conflicting_alias_overrides"
	} else if !result.Known() && evidence.observedConflict[modelID] {
		result.Reason = "conflicting_upstream_alias_capacities"
	}
	return result
}

// ResolveAccountModelContextCapacity answers for one account and one real
// upstream model ID.
func ResolveAccountModelContextCapacity(account *Account, upstreamModelID string) ResolvedModelContextCapacity {
	if account == nil {
		return unknownModelContextCapacity("account_missing")
	}
	return newAccountModelCapacityEvidence(account).resolve(upstreamModelID, true)
}

// LookupOfficialModelContextCapacity receives the real upstream target. The
// reference spellings are lookups only: they never become request IDs,
// observation keys or override keys. Do not use the Codex routing map: it also
// upgrades older models.
func LookupOfficialModelContextCapacity(account *Account, upstreamModelID string) *OfficialModelContextCapacity {
	if account == nil || !validModelContextID(upstreamModelID) {
		return nil
	}
	query := officialCatalogQuery{
		Candidates: modelContextReferenceCandidates(upstreamModelID),
		Platform:   account.Platform, AccountMode: account.GetAccountMode(),
	}
	if parsed, err := url.Parse(upstreamModelRegistryBaseURL(account)); err == nil {
		query.Scheme, query.Host, query.Port, query.Path = parsed.Scheme, parsed.Hostname(), parsed.Port(), parsed.Path
		query.HasURLCredentials = parsed.User != nil
	}
	return lookupOfficialModelCatalog(query)
}

// modelContextReferenceCandidates lists reference-lookup spellings of one real
// upstream ID: the ID itself, then without a service-tier suffix (":free",
// ":batch"), without a vendor namespace ("anthropic/claude-opus-4.6"), in the
// recognized GPT spelling and without a finite GPT effort alias. Dotted and
// hyphenated version separators compare equal in the catalog match itself.
func modelContextReferenceCandidates(modelID string) []string {
	candidates := []string{modelID}
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if validModelContextID(candidate) && !containsExactModelContextString(candidates, candidate) {
			candidates = append(candidates, candidate)
		}
	}
	base := modelID
	if colon := strings.LastIndexByte(base, ':'); colon > 0 {
		switch strings.ToLower(base[colon+1:]) {
		case "free", "batch":
			base = base[:colon]
			add(base)
		}
	}
	if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
		add(base[slash+1:])
	}
	spelling := canonicalizeOpenAIModelAliasSpelling(base)
	add(spelling)
	// Only the finite effort spellings are identity variants here. Date-looking
	// and arbitrary suffixes are not evidence for a model family.
	if dash := strings.LastIndexByte(spelling, '-'); dash >= 0 {
		suffix := spelling[dash+1:]
		if isKnownCodexModelSuffix(suffix) && !isCodexDateSuffix(suffix) {
			add(spelling[:dash])
		}
	}
	return candidates
}

func containsExactModelContextString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// BuildAccountModelContextCapacityRows lists the requested models, the
// account's mapping targets, every model with persisted evidence and every
// override, each with its evidence and answers.
func BuildAccountModelContextCapacityRows(account *Account, modelIDs []string) []AccountModelContextCapacityRow {
	rows := make([]AccountModelContextCapacityRow, 0)
	if account == nil {
		return rows
	}
	ids := make(map[string]struct{})
	variants := make(map[string][]string)
	add := func(modelID string) {
		if validModelContextID(modelID) {
			target := capacityCanonicalUpstreamID(account, modelID)
			ids[target] = struct{}{}
			if modelID != target {
				variants[target] = append(variants[target], modelID)
			}
		}
	}
	for _, modelID := range modelIDs {
		add(modelID)
	}
	aliases := make(map[string][]string)
	for alias, target := range account.GetModelMapping() {
		add(target)
		if validModelContextID(target) && validModelContextID(alias) && alias != target {
			canonical := capacityCanonicalUpstreamID(account, target)
			aliases[canonical] = append(aliases[canonical], alias)
		}
	}
	evidence := newAccountModelCapacityEvidence(account)
	for _, values := range []map[string]ModelContextCapacity{evidence.upstream, evidence.registry} {
		for modelID := range values {
			add(modelID)
		}
	}
	for _, flags := range []map[string]bool{evidence.overrideConflict, evidence.observedConflict} {
		for modelID := range flags {
			add(modelID)
		}
	}
	for modelID := range evidence.overrides {
		add(modelID)
	}
	ordered := make([]string, 0, len(ids))
	for modelID := range ids {
		ordered = append(ordered, modelID)
	}
	sort.Strings(ordered)
	for _, modelID := range ordered {
		row := AccountModelContextCapacityRow{UpstreamModelID: modelID, UpstreamModelIDs: dedupeAndSortModelIDs(variants[modelID]), Aliases: []string{}, Editable: true}
		if len(aliases[modelID])+len(variants[modelID]) > 0 {
			row.Aliases = dedupeAndSortModelIDs(append(aliases[modelID], variants[modelID]...))
		}
		if value, ok := evidence.upstream[modelID]; ok {
			row.Upstream = &value
		}
		if value, ok := evidence.registry[modelID]; ok {
			row.Registry = &value
		}
		row.Official = LookupOfficialModelContextCapacity(account, modelID)
		if value, ok := evidence.overrides[modelID]; ok {
			row.CustomContextWindow = &value
		}
		automatic, effective := evidence.resolve(modelID, false), evidence.resolve(modelID, true)
		row.AutomaticContextWindow, row.AutomaticSource = automatic.ContextWindow, automatic.Source
		row.EffectiveContextWindow, row.EffectiveSource = effective.ContextWindow, effective.Source
		row.CapacityBasis, row.MaxContextWindow = effective.CapacityBasis, effective.MaxContextWindow
		row.MaxInputTokens, row.MaxOutputTokens, row.Reason = effective.MaxInputTokens, effective.MaxOutputTokens, effective.Reason
		rows = append(rows, row)
	}
	return rows
}

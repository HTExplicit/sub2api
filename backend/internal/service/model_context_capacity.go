package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	DefaultModelContextWindow              int64 = 200000
	MaxSafeModelContextTokens              int64 = 9007199254740991
	UpstreamModelContextCapacitiesExtraKey       = "upstream_model_context_capacities"
	ModelContextOverridesExtraKey                = "model_context_overrides"
	ModelContextCapacityBasisTotal               = "total_context"
	ModelContextCapacityBasisInput               = "input_limit"
	ModelContextCapacityBasisMaximum             = "max_context_window"
)

// ModelContextCapacity keeps the upstream's distinct limits intact. A zero is
// unknown, never a promise of an unlimited window. ObservedAt belongs to an
// actual upstream observation, not to a registry enrichment or account edit.
type ModelContextCapacity = extensionv1.ModelContextCapacity

type UpstreamModelContextCapacitySnapshot struct {
	ObservedAt     string                          `json:"observed_at"`
	SourceIdentity string                          `json:"source_identity"`
	Models         map[string]ModelContextCapacity `json:"models"`
}

// ModelContextCapacityReference preserves the raw product reference separately
// from the planning capacity selected by this project.
type ModelContextCapacityReference = extensionv1.ModelContextCapacityReference

// OfficialModelContextCapacity is release-owned evidence. Match constraints and
// reference values are not client-writeable.
type OfficialModelContextCapacity = extensionv1.OfficialModelContextCapacity

type ResolvedModelContextCapacity struct {
	ModelContextCapacity
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"`
}

type AccountModelContextCapacityRow struct {
	UpstreamModelID        string                        `json:"upstream_model_id"`
	Aliases                []string                      `json:"aliases"`
	UpstreamModelIDs       []string                      `json:"upstream_model_ids,omitempty"`
	Editable               bool                          `json:"editable"`
	Upstream               *ModelContextCapacity         `json:"upstream,omitempty"`
	Official               *OfficialModelContextCapacity `json:"official,omitempty"`
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

// IsModelContextCapacityProtected follows credential/provider identity, not an
// editable proxy URL. An OAuth proxy is still an official OAuth wire contract.
func IsModelContextCapacityProtected(account *Account) bool {
	return account != nil && (account.Platform == PlatformCindy ||
		account.EffectiveProviderProfile() == ProviderProfileCindyLaxaV1 ||
		IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		account.IsOAuth())
}

func CanManageModelContextCapacity(account *Account) bool {
	if account == nil || IsModelContextCapacityProtected(account) {
		return false
	}
	if account.Type != AccountTypeAPIKey && account.Type != AccountTypeUpstream {
		return false
	}
	switch account.Platform {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformGrok,
		PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformAntigravity:
		return true
	default:
		return false
	}
}

// officialModelContextCapacityAPIHosts are the vendors' own API hosts. Their
// model lists rarely declare capacities, so the release catalog stays
// authoritative there. Any other host is a third-party relay that may serve a
// smaller window than the vendor product: its own declaration outranks the
// catalog, and official evidence only fills the fields it left blank.
var officialModelContextCapacityAPIHosts = []string{
	"api.openai.com", "api.anthropic.com", "generativelanguage.googleapis.com", "api.x.ai", "api.deepseek.com",
	"dashscope.aliyuncs.com", "dashscope-intl.aliyuncs.com", "dashscope-us.aliyuncs.com", "cn-hongkong.dashscope.aliyuncs.com",
	"api.moonshot.cn", "api.moonshot.ai", "api.kimi.com", "open.bigmodel.cn", "api.z.ai",
	"api.minimax.io", "api.minimaxi.com", "api.minimax.chat", "ark.cn-beijing.volces.com",
}

// modelContextCapacityUpstreamFirst reports whether the account's model list
// comes from a third-party host. An empty or unparsable endpoint keeps the
// official-first order rather than guessing a relay.
func modelContextCapacityUpstreamFirst(account *Account) bool {
	if account == nil {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(upstreamModelRegistryBaseURL(account)))
	if err != nil || parsed == nil || parsed.Hostname() == "" {
		return false
	}
	return !containsFoldModelContextString(officialModelContextCapacityAPIHosts, parsed.Hostname())
}

// ResolveModelContextCapacity applies the official-first order used on the
// vendors' own hosts: custom > official > upstream > default.
func ResolveModelContextCapacity(custom *int64, official *OfficialModelContextCapacity, upstream *ModelContextCapacity) ResolvedModelContextCapacity {
	return resolveModelContextCapacity(custom, official, upstream, false)
}

// ResolveModelContextCapacityForAccount follows the account's host: official
// first on vendor API hosts, upstream first on third-party relays.
func ResolveModelContextCapacityForAccount(account *Account, custom *int64, official *OfficialModelContextCapacity, upstream *ModelContextCapacity) ResolvedModelContextCapacity {
	return resolveModelContextCapacity(custom, official, upstream, modelContextCapacityUpstreamFirst(account))
}

// resolveModelContextCapacity chooses context/default-maximum as one source
// tuple. Independent input/output limits retain their own evidence in the same
// source order when compatible with that planning context. They are never
// filled from the default, clamped or synthesized from the context window.
func resolveModelContextCapacity(custom *int64, official *OfficialModelContextCapacity, upstream *ModelContextCapacity, upstreamFirst bool) ResolvedModelContextCapacity {
	result := resolveModelContextPlanningWindow(custom, official, upstream, upstreamFirst)
	var officialLimits, upstreamLimits ModelContextCapacity
	if official != nil {
		officialLimits = official.ModelContextCapacity
	}
	if upstream != nil {
		upstreamLimits = *upstream
	}
	primary, secondary := officialLimits, upstreamLimits
	if upstreamFirst {
		primary, secondary = upstreamLimits, officialLimits
	}
	compatibleLimit := func(values ...int64) int64 {
		for _, value := range values {
			if validModelContextTokens(value) && value <= result.ContextWindow {
				return value
			}
		}
		return 0
	}
	result.MaxInputTokens = compatibleLimit(primary.MaxInputTokens, secondary.MaxInputTokens)
	result.MaxOutputTokens = compatibleLimit(primary.MaxOutputTokens, secondary.MaxOutputTokens)
	return result
}

func resolveModelContextPlanningWindow(custom *int64, official *OfficialModelContextCapacity, upstream *ModelContextCapacity, upstreamFirst bool) ResolvedModelContextCapacity {
	if custom != nil && validModelContextTokens(*custom) {
		return ResolvedModelContextCapacity{
			ModelContextCapacity: ModelContextCapacity{ContextWindow: *custom, MaxContextWindow: *custom, CapacityBasis: ModelContextCapacityBasisTotal},
			Source:               "custom",
		}
	}
	var candidates []ResolvedModelContextCapacity
	if official != nil {
		if capacity, ok := modelContextPlanningCapacity(official.ModelContextCapacity); ok {
			candidates = append(candidates, ResolvedModelContextCapacity{ModelContextCapacity: capacity, Source: "official"})
		}
	}
	if upstream != nil {
		if capacity, ok := modelContextPlanningCapacity(*upstream); ok {
			candidate := ResolvedModelContextCapacity{ModelContextCapacity: capacity, Source: "upstream"}
			if upstreamFirst {
				candidates = append([]ResolvedModelContextCapacity{candidate}, candidates...)
			} else {
				candidates = append(candidates, candidate)
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ResolvedModelContextCapacity{
		ModelContextCapacity: ModelContextCapacity{
			ContextWindow: DefaultModelContextWindow, MaxContextWindow: DefaultModelContextWindow,
			CapacityBasis: ModelContextCapacityBasisTotal,
		},
		Source: "default", Reason: "no_verified_capacity",
	}
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
	// Raw evidence is preserved in the snapshot/admin row. Contradictory maximum
	// fields are not projected as a false client contract.
	if value.MaxContextWindow > 0 && value.MaxContextWindow < value.ContextWindow {
		value.MaxContextWindow = 0
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

func (account *Account) SetUpstreamModelContextCapacitySnapshot(snapshot UpstreamModelContextCapacitySnapshot) {
	if account == nil {
		return
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	if snapshot.SourceIdentity == "" {
		snapshot.SourceIdentity = ModelContextCapacitySourceIdentity(account)
	}
	account.Extra[UpstreamModelContextCapacitiesExtraKey] = snapshot
}

func (account *Account) GetUpstreamModelContextCapacitySnapshot() *UpstreamModelContextCapacitySnapshot {
	snapshot, _ := readModelContextCapacitySnapshot(account)
	return snapshot
}

// ModelContextCapacitySourceIdentity binds observations to the actual upstream
// endpoint/product/protocol. It intentionally excludes keys, network proxies,
// names and public model mappings: those are not model-capacity evidence.
func ModelContextCapacitySourceIdentity(account *Account) string {
	if account == nil {
		return ""
	}
	normalizeEndpoint := func(endpoint string) string {
		endpoint = strings.TrimSpace(endpoint)
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return endpoint
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		if parsed.Port() == "443" && parsed.Scheme == "https" {
			parsed.Host = parsed.Hostname()
		}
		if parsed.Port() == "80" && parsed.Scheme == "http" {
			parsed.Host = parsed.Hostname()
		}
		// Existing request URL builders retain query parameters, which may
		// select different provider/region backends. Keep them in the private
		// hash; canonical ordering avoids invalidation from a cosmetic reorder.
		if query, err := url.ParseQuery(parsed.RawQuery); err == nil {
			parsed.RawQuery = query.Encode()
		}
		parsed.User, parsed.Fragment = nil, ""
		parsed.ForceQuery = false
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
		return parsed.String()
	}
	identity := struct {
		Platform         string            `json:"platform"`
		Wire             string            `json:"wire"`
		Profile          string            `json:"profile"`
		BaseURL          string            `json:"base_url"`
		AnthropicBaseURL string            `json:"anthropic_base_url,omitempty"`
		AccountMode      string            `json:"account_mode"`
		APIProtocol      string            `json:"api_protocol"`
		APIBaseURLs      map[string]string `json:"api_base_urls,omitempty"`
	}{
		Platform: account.Platform, Wire: account.EffectiveWirePlatform(), Profile: account.EffectiveProviderProfile(),
		BaseURL: normalizeEndpoint(upstreamModelRegistryBaseURL(account)),
		// CN Anthropic requests can use a custom Messages endpoint while model
		// sync uses a fixed official OpenAI-format endpoint. Bind both identities.
		AnthropicBaseURL: normalizeEndpoint(account.GetAnthropicProtocolBaseURL()),
		AccountMode:      account.GetAccountMode(), APIProtocol: account.GetAPIProtocol(),
		APIBaseURLs: make(map[string]string),
	}
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAnthropic} {
		var endpoint string
		switch endpoints := account.Credentials["api_base_urls"].(type) {
		case map[string]any:
			endpoint, _ = endpoints[protocol].(string)
		case map[string]string:
			endpoint = endpoints[protocol]
		}
		if strings.TrimSpace(endpoint) != "" {
			identity.APIBaseURLs[protocol] = normalizeEndpoint(endpoint)
		}
	}
	body, _ := json.Marshal(identity)
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func readModelContextCapacitySnapshot(account *Account) (*UpstreamModelContextCapacitySnapshot, string) {
	if account == nil || account.Extra == nil {
		return nil, ""
	}
	body, err := json.Marshal(account.Extra[UpstreamModelContextCapacitiesExtraKey])
	if err != nil {
		return nil, "capacity_snapshot_unreadable"
	}
	var stored struct {
		ObservedAt     string                     `json:"observed_at"`
		SourceIdentity string                     `json:"source_identity"`
		Models         map[string]json.RawMessage `json:"models"`
	}
	if json.Unmarshal(body, &stored) != nil || len(stored.Models) == 0 {
		return nil, ""
	}
	if stored.SourceIdentity == "" || stored.SourceIdentity != ModelContextCapacitySourceIdentity(account) {
		return nil, "upstream_source_changed"
	}
	snapshot := &UpstreamModelContextCapacitySnapshot{ObservedAt: stored.ObservedAt, SourceIdentity: stored.SourceIdentity, Models: make(map[string]ModelContextCapacity, len(stored.Models))}
	for modelID, raw := range stored.Models {
		if !validModelContextID(modelID) {
			continue
		}
		value := ParseUpstreamModelContextCapacity(raw, "")
		if value.ObservedAt == "" {
			value.ObservedAt = stored.ObservedAt
		}
		snapshot.Models[modelID] = value
	}
	return snapshot, ""
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

// ValidateModelContextOverrides distinguishes omitted (nil map), no-op (empty
// map), and deletion (nil value at one model key). It never mutates the account.
func ValidateModelContextOverrides(account *Account, patch map[string]*int64) error {
	if patch == nil {
		return nil
	}
	if !CanManageModelContextCapacity(account) {
		return infraerrors.BadRequest("MODEL_CONTEXT_OVERRIDES_READ_ONLY", "model context overrides are unavailable for this account")
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

// NewAccountModelContextCapacityResolver decodes account snapshots once. A live
// value replaces only the observed upstream source for this call and never
// writes to account state or a shared upstream cache.
func NewAccountModelContextCapacityResolver(account *Account) func(string, *ModelContextCapacity) ResolvedModelContextCapacity {
	if !CanManageModelContextCapacity(account) {
		return func(modelID string, live *ModelContextCapacity) ResolvedModelContextCapacity {
			return protectedAccountModelContextCapacity(account, modelID, live)
		}
	}
	snapshot, snapshotReason := readModelContextCapacitySnapshot(account)
	observations, observationConflicts := capacitySnapshotTargets(account, snapshot)
	custom, conflicts := resolveCapacityOverrides(account)
	return func(modelID string, live *ModelContextCapacity) ResolvedModelContextCapacity {
		modelID = capacityCanonicalUpstreamID(account, modelID)
		if !validModelContextID(modelID) {
			result := ResolveModelContextCapacity(nil, nil, nil)
			result.Reason = "upstream_model_unresolved"
			return result
		}
		var override *int64
		if value, ok := custom[modelID]; ok {
			override = &value
		}
		var upstream *ModelContextCapacity
		if value, ok := observations[modelID]; ok {
			upstream = &value
		}
		if live != nil {
			upstream = live
		}
		result := ResolveModelContextCapacityForAccount(account, override, LookupOfficialModelContextCapacity(account, modelID), upstream)
		if conflicts[modelID] {
			result.Reason = "conflicting_alias_overrides"
		}
		if result.Source == "default" && observationConflicts[modelID] {
			result.Reason = "conflicting_upstream_alias_capacities"
		}
		if result.Source == "default" && snapshotReason != "" {
			result.Reason = snapshotReason
		}
		return result
	}
}

func ResolveAccountModelContextCapacity(account *Account, upstreamModelID string) ResolvedModelContextCapacity {
	return NewAccountModelContextCapacityResolver(account)(upstreamModelID, nil)
}

// LookupOfficialModelContextCapacity receives the real upstream target. Matching
// variants are only reference lookups: they never become request IDs, observation
// keys or override keys. Exact catalog entries win before namespace/spelling
// variants. Do not use the Codex routing map: it also upgrades older models.
func LookupOfficialModelContextCapacity(account *Account, upstreamModelID string) *OfficialModelContextCapacity {
	if account == nil || !validModelContextID(upstreamModelID) {
		return nil
	}
	query := extensionv1.CatalogQuery{AccountID: account.ID, Candidates: modelContextReferenceCandidates(upstreamModelID), Platform: account.Platform, AccountType: account.Type, AccountMode: account.GetAccountMode()}
	if parsed, err := url.Parse(upstreamModelRegistryBaseURL(account)); err == nil {
		query.Scheme, query.Host, query.Port, query.Path = parsed.Scheme, parsed.Hostname(), parsed.Port(), parsed.Path
		query.HasURLCredentials = parsed.User != nil
	}
	return lookupExtensionCatalog(query)
}

func modelContextReferenceCandidates(modelID string) []string {
	candidates := []string{modelID}
	add := func(candidate string) {
		if validModelContextID(candidate) && !containsExactModelContextString(candidates, candidate) {
			candidates = append(candidates, candidate)
		}
	}
	if slash := strings.LastIndexByte(modelID, '/'); slash >= 0 {
		add(strings.TrimSpace(modelID[slash+1:]))
	}
	spelling := canonicalizeOpenAIModelAliasSpelling(modelID)
	add(spelling)
	// Only the existing finite effort spellings are identity variants here.
	// Date-looking and arbitrary suffixes are not evidence for a model family.
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

func containsFoldModelContextString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func protectedAccountModelContextCapacity(account *Account, modelID string, live *ModelContextCapacity) ResolvedModelContextCapacity {
	result := ResolvedModelContextCapacity{Source: "protected", Reason: "dedicated_catalog_read_only"}
	if account == nil {
		return result
	}
	if account.Platform == PlatformCindy || account.EffectiveProviderProfile() == ProviderProfileCindyLaxaV1 ||
		IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		if capability, ok := resolveKnownCindyCapability(modelID); ok {
			result.ModelContextCapacity = ModelContextCapacity{
				ContextWindow: int64(capability.EffectiveCodexContextWindow()), MaxInputTokens: int64(capability.MaxInputTokens),
				MaxOutputTokens: int64(capability.MaxOutputTokens), CapacityBasis: ModelContextCapacityBasisInput,
			}
		}
		return result
	}
	if live != nil {
		if capacity, ok := modelContextPlanningCapacity(*live); ok {
			result.ModelContextCapacity = capacity
			return result
		}
	}
	if snapshot := account.GetUpstreamModelContextCapacitySnapshot(); snapshot != nil {
		if capacity, ok := modelContextPlanningCapacity(snapshot.Models[modelID]); ok {
			result.ModelContextCapacity = capacity
			return result
		}
	}
	// Legacy mixed-source metadata is usable only as the pre-existing protected
	// display value. It is never relabelled as a raw upstream observation.
	if metadata, ok := account.GetUpstreamModelMetadata(modelID); ok {
		result.ModelContextCapacity, _ = modelContextPlanningCapacity(ModelContextCapacity{
			ContextWindow: metadata.ContextWindow, MaxContextWindow: metadata.MaxContextWindow, MaxOutputTokens: metadata.MaxOutputTokens,
		})
		if result.ContextWindow > 0 {
			return result
		}
	}
	if account.IsOpenAIOAuthLike() && isOpenAIGPT6SolOrLunaModel(modelID) {
		result.ModelContextCapacity = ModelContextCapacity{
			ContextWindow: openai.GPT6CodexContextWindow, MaxContextWindow: openai.GPT6CodexMaxContextWindow,
			CapacityBasis: ModelContextCapacityBasisTotal,
		}
		result.Reason = "codex_catalog_reference"
	}
	return result
}

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
	snapshot := account.GetUpstreamModelContextCapacitySnapshot()
	observations, _ := capacitySnapshotTargets(account, snapshot)
	if snapshot != nil {
		for modelID := range snapshot.Models {
			add(modelID)
		}
	}
	custom, conflicts := resolveCapacityOverrides(account)
	for modelID := range custom {
		add(modelID)
	}
	for modelID := range conflicts {
		add(modelID)
	}
	if !CanManageModelContextCapacity(account) {
		if legacy := account.GetUpstreamModelMetadataSnapshot(); legacy != nil {
			for modelID := range legacy.Models {
				add(modelID)
			}
		}
	}
	ordered := make([]string, 0, len(ids))
	for modelID := range ids {
		ordered = append(ordered, modelID)
	}
	sort.Strings(ordered)
	resolve := NewAccountModelContextCapacityResolver(account)
	for _, modelID := range ordered {
		row := AccountModelContextCapacityRow{UpstreamModelID: modelID, UpstreamModelIDs: dedupeAndSortModelIDs(variants[modelID]), Aliases: []string{}, Editable: CanManageModelContextCapacity(account)}
		if len(aliases[modelID])+len(variants[modelID]) > 0 {
			row.Aliases = dedupeAndSortModelIDs(append(aliases[modelID], variants[modelID]...))
		}
		if capacity, ok := observations[modelID]; ok && modelContextCapacityHasLimits(capacity) {
			row.Upstream = &capacity
		}
		var automatic ResolvedModelContextCapacity
		if row.Editable {
			row.Official = LookupOfficialModelContextCapacity(account, modelID)
			if value, ok := custom[modelID]; ok {
				row.CustomContextWindow = &value
			}
			automatic = ResolveModelContextCapacityForAccount(account, nil, row.Official, row.Upstream)
		} else {
			automatic = resolve(modelID, nil)
			if account.IsOpenAIOAuthLike() && isOpenAIGPT6SolOrLunaModel(modelID) {
				// Expose product references without applying API limits to OAuth.
				row.Official = LookupOfficialModelContextCapacity(account, modelID)
			}
		}
		effective := resolve(modelID, nil)
		row.AutomaticContextWindow, row.AutomaticSource = automatic.ContextWindow, automatic.Source
		row.EffectiveContextWindow, row.EffectiveSource = effective.ContextWindow, effective.Source
		row.CapacityBasis, row.MaxContextWindow = effective.CapacityBasis, effective.MaxContextWindow
		row.MaxInputTokens, row.MaxOutputTokens, row.Reason = effective.MaxInputTokens, effective.MaxOutputTokens, effective.Reason
		rows = append(rows, row)
	}
	return rows
}

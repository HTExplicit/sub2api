package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

// ApplyModelContextCapacityToFields is the final, capacity-only wire projection.
// It never changes model membership, request budgets, or tool/reasoning metadata.
// Protected catalogs deliberately retain their original wire representation.
func ApplyModelContextCapacityToFields(fields map[string]json.RawMessage, capacity ResolvedModelContextCapacity, codex bool) bool {
	if fields == nil || capacity.Source == "protected" {
		return false
	}
	if !validModelContextTokens(capacity.ContextWindow) {
		capacity = defaultGroupModelCapacity("capacity_unavailable")
	}
	changed := false
	set := func(key string, value any) {
		encoded, _ := json.Marshal(value)
		if !bytes.Equal(bytes.TrimSpace(fields[key]), encoded) {
			fields[key] = encoded
			changed = true
		}
	}
	remove := func(key string) {
		if _, exists := fields[key]; exists {
			delete(fields, key)
			changed = true
		}
	}
	set("context_window", capacity.ContextWindow)
	maximum := capacity.MaxContextWindow
	if !validModelContextTokens(maximum) || maximum < capacity.ContextWindow {
		maximum = capacity.ContextWindow
	}
	set("max_context_window", maximum)
	for key, value := range map[string]int64{
		"max_input_tokens": capacity.MaxInputTokens, "max_output_tokens": capacity.MaxOutputTokens,
	} {
		if validModelContextTokens(value) {
			set(key, value)
		} else {
			// Do not retain stale lower-priority limits from a source that lost.
			remove(key)
		}
	}
	set("context_capacity_source", capacity.Source)
	set("context_capacity_basis", capacity.CapacityBasis)
	if capacity.Reason == "" {
		remove("context_capacity_reason")
	} else {
		set("context_capacity_reason", capacity.Reason)
	}
	if codex {
		var limit int64
		raw, exists := fields["auto_compact_token_limit"]
		if !exists || (string(bytes.TrimSpace(raw)) != "null" &&
			(json.Unmarshal(raw, &limit) != nil || limit <= 0 || limit > capacity.ContextWindow)) {
			set("auto_compact_token_limit", nil)
		}
	}
	return changed
}

func restoreLiveCodexAutoCompactLimits(body []byte, limits map[string]json.RawMessage) ([]byte, error) {
	if len(limits) == 0 {
		return body, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &rows); err != nil {
		return nil, err
	}
	changed := false
	for i, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil {
			continue
		}
		var slug string
		if json.Unmarshal(fields["slug"], &slug) != nil {
			continue
		}
		raw, exists := limits[slug]
		if !exists {
			continue
		}
		var limit, window int64
		_ = json.Unmarshal(fields["context_window"], &window)
		if json.Unmarshal(raw, &limit) != nil || !validModelContextTokens(limit) || limit > window {
			raw = json.RawMessage("null")
		}
		if !bytes.Equal(fields["auto_compact_token_limit"], raw) {
			fields["auto_compact_token_limit"] = raw
			rows[i], _ = json.Marshal(fields)
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	envelope["models"], _ = json.Marshal(rows)
	return json.Marshal(envelope)
}

func defaultGroupModelCapacity(reason string) ResolvedModelContextCapacity {
	return ResolvedModelContextCapacity{
		ModelContextCapacity: ModelContextCapacity{
			ContextWindow: DefaultModelContextWindow, MaxContextWindow: DefaultModelContextWindow,
			CapacityBasis: "total_context",
		},
		Source: "default", Reason: reason,
	}
}

// groupModelCapacityCatalog is a request-local view of persistent routing
// candidates. Snapshots and overrides are decoded once per account, not once
// per model. No external upstream/catalog I/O is performed here.
type groupModelCapacityCatalog struct {
	accounts        []Account
	resolvers       []func(string, *ModelContextCapacity) ResolvedModelContextCapacity
	routes          []CompositeModelRoute
	available       bool
	routesAvailable bool
	groupID         *int64
	group           *Group
	channelService  *ChannelService
	channelLookups  map[string]*channelLookup
	channelErrors   map[string]bool
	liveByAccount   map[int64]map[string]ModelContextCapacity
}

func newGroupModelCapacityCatalog(accounts []Account, available bool, routes []CompositeModelRoute, routesAvailable bool, groupID *int64, channels *ChannelService) *groupModelCapacityCatalog {
	catalog := &groupModelCapacityCatalog{
		accounts: accounts, available: available, routes: routes, routesAvailable: routesAvailable,
		groupID: groupID, channelService: channels,
		channelLookups: make(map[string]*channelLookup), channelErrors: make(map[string]bool),
		resolvers:     make([]func(string, *ModelContextCapacity) ResolvedModelContextCapacity, len(accounts)),
		liveByAccount: make(map[int64]map[string]ModelContextCapacity),
	}
	for i := range catalog.accounts {
		catalog.resolvers[i] = NewAccountModelContextCapacityResolver(&catalog.accounts[i])
	}
	return catalog
}

func (catalog *groupModelCapacityCatalog) bindLiveSource(source codexModelCapacitySource) {
	if source.identity == "" || len(source.body) == 0 {
		return
	}
	for i := range catalog.accounts {
		account := &catalog.accounts[i]
		if account.ID == source.accountID && ModelContextCapacitySourceIdentity(account) == source.identity {
			catalog.liveByAccount[source.accountID], _ = ParseUpstreamModelContextCapacities(source.body, source.platform)
			return
		}
	}
}

func loadGroupModelCapacityCatalog(ctx context.Context, repo AccountRepository, routesRepo CompositeModelRouteRepository, channels *ChannelService, cfg *config.Config, groupID *int64, platform string, groups ...*Group) *groupModelCapacityCatalog {
	var group *Group
	if len(groups) > 0 {
		group = groups[0]
	}
	managed := group != nil && group.ManagedModelRoutes.Enabled
	platforms := []string{platform}
	useMixed := platform == PlatformAnthropic || platform == PlatformGemini
	if useMixed {
		platforms = append(platforms, PlatformAntigravity)
	} else if platform == PlatformComposite {
		platforms = []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformCindy}
	}
	if managed {
		// Native-branded groups can contain compatible-platform branches. Query
		// the published graph, not the brand, and keep this pool group-bound.
		platforms = managedCatalogPlatforms(group)
	}
	queryGroupID, includeGrouped := groupID, false
	if !managed && cfg != nil && cfg.RunMode == config.RunModeSimple && (!useMixed || groupID == nil) {
		queryGroupID, includeGrouped = nil, true
	}
	var accounts []Account
	available := false
	if repo != nil {
		var err error
		accounts, err = repo.ListModelAvailabilityCandidates(ctx, queryGroupID, platforms, includeGrouped)
		available = err == nil
	}
	var routes []CompositeModelRoute
	routesAvailable := true
	if platform == PlatformComposite {
		routesAvailable = routesRepo != nil && groupID != nil
		if routesAvailable {
			var err error
			routes, err = routesRepo.ListByGroup(ctx, *groupID, false)
			routesAvailable = err == nil
		}
	}
	catalog := newGroupModelCapacityCatalog(accounts, available, routes, routesAvailable, groupID, channels)
	catalog.group = group
	return catalog
}

type capacityModelTarget struct {
	platform string
	model    string
	owned    bool
}

// compositeTarget follows the actual explicit-route, account-ownership,
// detector precedence. A models list has no endpoint, so differing text-route
// targets cannot be advertised as one confirmed capacity.
func (catalog *groupModelCapacityCatalog) compositeTarget(model string) (capacityModelTarget, string) {
	if !catalog.routesAvailable {
		return capacityModelTarget{}, "route_query_failed"
	}
	var common capacityModelTarget
	for _, endpoint := range []string{CompositeRouteEndpointMessages, CompositeRouteEndpointResponses, CompositeRouteEndpointChatCompletions, CompositeRouteEndpointGemini} {
		target := capacityModelTarget{model: model}
		if route, ok := matchCompositeRoute(catalog.routes, model, endpoint); ok {
			target.platform = strings.TrimSpace(route.TargetPlatform)
			if strings.TrimSpace(route.UpstreamModel) != "" {
				target.model = strings.TrimSpace(route.UpstreamModel)
			}
		} else {
			for _, account := range catalog.accounts {
				if !isConcreteRequestPlatform(account.Platform) || !explicitModelMappingClaims(account, model) {
					continue
				}
				if target.platform != "" && target.platform != account.Platform {
					return capacityModelTarget{}, "ambiguous_model_ownership"
				}
				target.platform, target.owned = account.Platform, true
			}
			if target.platform == "" {
				target.platform, _ = DetectModelPlatform(model)
			}
		}
		if !isConcreteRequestPlatform(target.platform) {
			return capacityModelTarget{}, "unresolved_model_target"
		}
		if common.platform != "" && common != target {
			return capacityModelTarget{}, "ambiguous_endpoint_routes"
		}
		common = target
	}
	return common, ""
}

func (catalog *groupModelCapacityCatalog) channel(ctx context.Context, platform string) (*channelLookup, bool) {
	if catalog.channelService == nil || catalog.groupID == nil {
		return nil, true
	}
	if lookup, exists := catalog.channelLookups[platform]; exists {
		return lookup, !catalog.channelErrors[platform]
	}
	lookup, err := catalog.channelService.lookupGroupChannel(WithResolvedTargetPlatform(ctx, platform), *catalog.groupID)
	catalog.channelLookups[platform] = lookup
	catalog.channelErrors[platform] = err != nil
	return lookup, err == nil
}

func capacityAccountMatchesPlatform(account *Account, platform string) bool {
	if account.Platform == platform {
		return true
	}
	return (platform == PlatformAnthropic || platform == PlatformGemini) &&
		account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled()
}

func capacityAccountModelTarget(account *Account, selectionModel, forwardModel string) (string, bool) {
	if account == nil {
		return "", false
	}
	if account.Platform == PlatformAntigravity {
		if mapAntigravityModel(account, selectionModel) == "" {
			return "", false
		}
		return mapAntigravityModel(account, forwardModel), true
	}
	if account.IsBedrock() {
		if _, supported := ResolveBedrockModelID(account, selectionModel); !supported {
			return "", false
		}
		// A regional Bedrock ARN is not an official Anthropic API model ID.
		model, supported := ResolveBedrockModelID(account, forwardModel)
		return model, supported
	}
	if account.Platform == PlatformAnthropic && account.Type != AccountTypeAPIKey {
		selectionModel = claude.NormalizeModelID(selectionModel)
		forwardModel = claude.NormalizeModelID(forwardModel)
		if account.Type == AccountTypeServiceAccount {
			selectionModel = normalizeVertexAnthropicModelID(selectionModel)
			forwardModel = normalizeVertexAnthropicModelID(forwardModel)
		}
	}
	if !account.IsModelSupported(selectionModel) {
		return "", false
	}
	if account.Platform == PlatformGrok {
		return xai.ResolveGrokTextResponsesModelID(account.GetMappedModel(forwardModel), grokDefaultResponsesModel), true
	}
	if account.Platform == PlatformOpenAI || IsCNProvider(account.Platform) {
		return resolveOpenAIAccountUpstreamModelForRequest(account, forwardModel, false), true
	}
	return account.GetMappedModel(forwardModel), true
}

// possibleAccountTargets checks the text forwarding paths without evaluating
// transient scheduler gates. Passthrough and Messages dispatch can legitimately
// select a different model from Responses, so a list-level answer must not infer
// a capacity from just one of those paths.
func (catalog *groupModelCapacityCatalog) possibleAccountTargets(account *Account, target capacityModelTarget, forwardModel string) ([]string, bool) {
	selectionModel := target.model
	compatible := target.platform == PlatformOpenAI || target.platform == PlatformGrok || IsCNProvider(target.platform)
	if compatible {
		selectionModel = forwardModel
	}
	model, supported := capacityAccountModelTarget(account, selectionModel, forwardModel)
	models := make([]string, 0, 3)
	if supported {
		models = append(models, model)
	}
	if compatible {
		if account.IsModelSupported(forwardModel) {
			chatModel := normalizeOpenAIModelForUpstream(account, resolveOpenAIForwardModel(account, forwardModel, ""))
			if target.platform == PlatformGrok {
				chatModel = xai.ResolveGrokTextResponsesModelID(chatModel, grokDefaultResponsesModel)
			}
			models = append(models, chatModel)
		}
		// Only configured Messages dispatch participates in the generic catalog;
		// it is not a fallback for normal Responses or Chat forwarding.
		if catalog.group != nil && catalog.group.AllowMessagesDispatch {
			dispatch := catalog.group.ResolveMessagesDispatchModel(target.model)
			if catalog.group.Platform == PlatformComposite && (target.platform == PlatformGrok || IsCNProvider(target.platform)) {
				dispatch = ""
			}
			messagesSelection := NormalizeOpenAICompatRequestedModel(target.model)
			if dispatch != "" {
				messagesSelection = dispatch
			}
			if account.IsModelSupported(messagesSelection) {
				bodyModel := NormalizeOpenAICompatRequestedModel(forwardModel)
				if account.IsAnthropicProtocol() || account.IsAdaptiveAPIProtocol() {
					bodyModel = forwardModel
				}
				models = append(models, normalizeOpenAIModelForUpstream(account, resolveOpenAIForwardModel(account, bodyModel, dispatch)))
			}
		}
	} else if target.platform == PlatformGemini {
		if nativeModel, nativeSupported := capacityAccountModelTarget(account, forwardModel, forwardModel); nativeSupported {
			models = append(models, nativeModel)
		}
	}
	if len(models) == 0 {
		return nil, false
	}
	return models, true
}

func (catalog *groupModelCapacityCatalog) resolve(ctx context.Context, platform, model string) ResolvedModelContextCapacity {
	if !catalog.available {
		return defaultGroupModelCapacity("account_query_failed")
	}
	if catalog.group != nil && catalog.group.ManagedModelRoutes.Enabled && catalog.group.ManagedModelRoutes.Version == ManagedModelRoutesVersion {
		return catalog.resolveManaged(ctx, model)
	}
	target := capacityModelTarget{platform: platform, model: model}
	if platform == PlatformComposite {
		var reason string
		target, reason = catalog.compositeTarget(model)
		if reason != "" {
			// An ambiguous mixed catalog must never overwrite a protected row
			// merely because its exact endpoint target could not be resolved.
			for i := range catalog.accounts {
				if !CanManageModelContextCapacity(&catalog.accounts[i]) && catalog.accounts[i].IsModelSupported(model) {
					return ResolvedModelContextCapacity{Source: "protected"}
				}
			}
			return defaultGroupModelCapacity(reason)
		}
	}
	lookup, ok := catalog.channel(ctx, target.platform)
	if !ok {
		return defaultGroupModelCapacity("channel_query_failed")
	}
	mapping := ChannelMappingResult{MappedModel: target.model}
	if lookup != nil {
		mapping = resolveMapping(lookup, *catalog.groupID, target.model)
		if billingModel := billingModelForRestriction(mapping.BillingModelSource, target.model, mapping.MappedModel); billingModel != "" && checkRestricted(lookup, *catalog.groupID, billingModel) {
			return defaultGroupModelCapacity("model_route_restricted")
		}
	}
	var result ResolvedModelContextCapacity
	count := 0
	var minimumMax, minimumInput, minimumOutput int64
	for i := range catalog.accounts {
		account := &catalog.accounts[i]
		if catalog.group != nil && catalog.group.ManagedModelRoutes.Enabled {
			if _, allowed := managedCatalogAccountMappingWithScheduling(catalog.group, account, "", false)[model]; !allowed {
				continue
			}
		}
		if !capacityAccountMatchesPlatform(account, target.platform) || (target.owned && !explicitModelMappingClaims(*account, model)) {
			continue
		}
		if catalog.group != nil && catalog.group.RequirePrivacySet && !account.IsPrivacySet() {
			continue
		}
		upstreamModels, supported := catalog.possibleAccountTargets(account, target, mapping.MappedModel)
		if !supported {
			continue
		}
		if !CanManageModelContextCapacity(account) {
			return ResolvedModelContextCapacity{Source: "protected"}
		}
		upstreamModel := strings.TrimSpace(upstreamModels[0])
		for _, possible := range upstreamModels[1:] {
			if strings.TrimSpace(possible) != upstreamModel {
				return defaultGroupModelCapacity("ambiguous_endpoint_targets")
			}
		}
		if strings.TrimSpace(upstreamModel) == "" {
			return defaultGroupModelCapacity("unresolved_model_target")
		}
		if lookup != nil && lookup.channel.BillingModelSource == BillingModelSourceUpstream && checkRestricted(lookup, *catalog.groupID, upstreamModel) {
			continue
		}
		var live *ModelContextCapacity
		if observed, exists := catalog.liveByAccount[account.ID][upstreamModel]; exists {
			live = &observed
		}
		candidate := catalog.resolvers[i](upstreamModel, live)
		maximum := candidate.MaxContextWindow
		if maximum < candidate.ContextWindow {
			maximum = candidate.ContextWindow
		}
		if count == 0 || maximum < minimumMax {
			minimumMax = maximum
		}
		if count == 0 || candidate.MaxInputTokens < minimumInput {
			minimumInput = candidate.MaxInputTokens
		}
		if count == 0 || candidate.MaxOutputTokens < minimumOutput {
			minimumOutput = candidate.MaxOutputTokens
		}
		if count == 0 || candidate.ContextWindow < result.ContextWindow ||
			(candidate.ContextWindow == result.ContextWindow && candidate.Source < result.Source) {
			result = candidate
		}
		count++
	}
	if count == 0 {
		return defaultGroupModelCapacity("no_model_candidate")
	}
	if count > 1 && result.Reason == "" {
		result.Reason = "group_minimum"
	}
	result.MaxContextWindow, result.MaxInputTokens, result.MaxOutputTokens = minimumMax, minimumInput, minimumOutput
	return result
}

// resolveManaged follows verified branch targets directly. Unlike ordinary
// composite inference, differing platforms/targets are an intentional pool,
// not ambiguity. Resolve against the original account so per-account snapshot
// identities and exact upstream override keys remain authoritative.
func (catalog *groupModelCapacityCatalog) resolveManaged(ctx context.Context, model string) ResolvedModelContextCapacity {
	var result ResolvedModelContextCapacity
	var minimumMax, minimumInput, minimumOutput int64
	count := 0
	for i := range catalog.accounts {
		account := &catalog.accounts[i]
		seenTargets := make(map[string]bool)
		for _, target := range managedCatalogAccountTargets(catalog.group, account, "", false) {
			if target.publicModel != model || seenTargets[target.upstreamModel] {
				continue
			}
			lookup, ok := catalog.channel(ctx, target.branch.TargetPlatform)
			if !ok {
				return defaultGroupModelCapacity("channel_query_failed")
			}
			if lookup != nil && checkRestricted(lookup, *catalog.groupID, model) {
				return defaultGroupModelCapacity("model_route_restricted")
			}
			if !CanManageModelContextCapacity(account) {
				return ResolvedModelContextCapacity{Source: "protected"}
			}
			seenTargets[target.upstreamModel] = true
			var live *ModelContextCapacity
			if observed, exists := catalog.liveByAccount[account.ID][target.upstreamModel]; exists {
				live = &observed
			}
			candidate := catalog.resolvers[i](target.upstreamModel, live)
			maximum := candidate.MaxContextWindow
			if maximum < candidate.ContextWindow {
				maximum = candidate.ContextWindow
			}
			if count == 0 || maximum < minimumMax {
				minimumMax = maximum
			}
			if count == 0 || candidate.MaxInputTokens < minimumInput {
				minimumInput = candidate.MaxInputTokens
			}
			if count == 0 || candidate.MaxOutputTokens < minimumOutput {
				minimumOutput = candidate.MaxOutputTokens
			}
			if count == 0 || candidate.ContextWindow < result.ContextWindow ||
				(candidate.ContextWindow == result.ContextWindow && candidate.Source < result.Source) {
				result = candidate
			}
			count++
		}
	}
	if count == 0 {
		return defaultGroupModelCapacity("no_model_candidate")
	}
	if count > 1 && result.Reason == "" {
		result.Reason = "group_minimum"
	}
	result.MaxContextWindow, result.MaxInputTokens, result.MaxOutputTokens = minimumMax, minimumInput, minimumOutput
	return result
}

func projectModelCapacityEnvelope(body []byte, codex bool, resolve func(string) ResolvedModelContextCapacity, protected map[string]bool) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode model capacity envelope: %w", err)
	}
	listKey, modelKey := "data", "id"
	if codex {
		listKey, modelKey = "models", "slug"
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(envelope[listKey], &rows); err != nil {
		return nil, fmt.Errorf("decode model capacity rows: %w", err)
	}
	changed := false
	visibleRows := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil || fields == nil {
			visibleRows = append(visibleRows, row)
			continue
		}
		var model string
		if json.Unmarshal(fields[modelKey], &model) != nil || strings.TrimSpace(model) == "" {
			visibleRows = append(visibleRows, row)
			continue
		}
		// This is the final request-local envelope, including pinned upstream
		// catalogs. Filter before capacity projection and ETag calculation, not
		// from the source mappings or the shared upstream discovery cache.
		if IsManagedModelSelector(model) {
			changed = true
			continue
		}
		if protected[model] {
			visibleRows = append(visibleRows, row)
			continue
		}
		if ApplyModelContextCapacityToFields(fields, resolve(model), codex) {
			row, _ = json.Marshal(fields)
			changed = true
		}
		visibleRows = append(visibleRows, row)
	}
	if !changed {
		return body, nil
	}
	envelope[listKey], _ = json.Marshal(visibleRows)
	return json.Marshal(envelope)
}

func buildDefaultCapacityCodexManifest(modelIDs []string, reason string) ([]byte, error) {
	body, err := BuildCodexModelsManifest(modelIDs)
	if err != nil {
		return nil, err
	}
	return projectModelCapacityEnvelope(body, true, func(string) ResolvedModelContextCapacity {
		return defaultGroupModelCapacity(reason)
	}, nil)
}

// ProjectModelListContextCapacities applies the final reserved-selector filter
// and appends optional metadata. Retained /v1/models rows keep their existing
// platform-specific shape, IDs and order.
func (s *GatewayService) ProjectModelListContextCapacities(ctx context.Context, group *Group, groupID *int64, platform string, body []byte) ([]byte, error) {
	if platform == "" {
		platform = PlatformAnthropic
	}
	var catalog *groupModelCapacityCatalog
	if s == nil {
		catalog = newGroupModelCapacityCatalog(nil, false, nil, false, groupID, nil)
	} else {
		var routesRepo CompositeModelRouteRepository
		if s.compositeResolver != nil {
			routesRepo = s.compositeResolver.repo
		}
		catalog = loadGroupModelCapacityCatalog(ctx, s.accountRepo, routesRepo, s.channelService, s.cfg, groupID, platform, group)
	}
	catalog.group = group
	return projectModelCapacityEnvelope(body, false, func(model string) ResolvedModelContextCapacity {
		return catalog.resolve(ctx, platform, model)
	}, nil)
}

// ProjectCodexModelContextCapacities runs after all group/Cindy merges and
// before conditional response handling. Local overlays never enter the raw
// upstream cache; a changed override therefore changes the final ETag.
func (s *OpenAIGatewayService) ProjectCodexModelContextCapacities(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, ifNoneMatch string, source *Account) error {
	if manifest == nil || manifest.NotModified || len(manifest.Body) == 0 || group == nil {
		return nil
	}
	catalog := loadGroupModelCapacityCatalog(ctx, s.accountRepo, nil, s.channelService, s.cfg, &group.ID, group.Platform, group)
	return projectCodexModelContextCapacities(ctx, group, manifest, ifNoneMatch, source, catalog)
}

// ProjectOpenAIModelsListContextCapacities is the final, request-local overlay
// for upstream-discovered ordinary catalogs. Discovery sources supply raw
// observations; capacity candidates still follow the actual forwarding pool.
func (s *OpenAIGatewayService) ProjectOpenAIModelsListContextCapacities(ctx context.Context, group *Group, response *OpenAIModelsResponse, ifNoneMatch string) error {
	if response == nil || response.NotModified || len(response.Body) == 0 || group == nil {
		return nil
	}
	catalog := loadGroupModelCapacityCatalog(ctx, s.accountRepo, nil, s.channelService, s.cfg, &group.ID, group.Platform, group)
	return projectOpenAIModelsContextCapacities(ctx, group, response, ifNoneMatch, nil, catalog, false)
}

// The shared gateway owns composite route configuration, including the
// instance attached to the OpenAI-compatible handler for native dispatch.
func (s *GatewayService) ProjectCodexModelContextCapacities(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, ifNoneMatch string, source *Account) error {
	if manifest == nil || manifest.NotModified || len(manifest.Body) == 0 || group == nil {
		return nil
	}
	var routesRepo CompositeModelRouteRepository
	if s.compositeResolver != nil {
		routesRepo = s.compositeResolver.repo
	}
	catalog := loadGroupModelCapacityCatalog(ctx, s.accountRepo, routesRepo, s.channelService, s.cfg, &group.ID, group.Platform, group)
	return projectCodexModelContextCapacities(ctx, group, manifest, ifNoneMatch, source, catalog)
}

func projectCodexModelContextCapacities(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, ifNoneMatch string, source *Account, catalog *groupModelCapacityCatalog) error {
	return projectOpenAIModelsContextCapacities(ctx, group, manifest, ifNoneMatch, source, catalog, true)
}

func projectOpenAIModelsContextCapacities(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, ifNoneMatch string, source *Account, catalog *groupModelCapacityCatalog, codex bool) error {
	catalog.group = group
	sources := manifest.capacitySources
	if len(sources) == 0 && source != nil {
		body := manifest.upstreamSourceBody
		if len(body) == 0 && !CanManageModelContextCapacity(source) {
			body = manifest.Body
		}
		sources = []codexModelCapacitySource{newCodexModelCapacitySource(source, body)}
	}
	protected := make(map[string]bool, len(manifest.capacityProtectedModels))
	for model := range manifest.capacityProtectedModels {
		protected[model] = true
	}
	for _, capacitySource := range sources {
		catalog.bindLiveSource(capacitySource)
		if !capacitySource.protected {
			continue
		}
		if capacitySource.visibleModels != nil {
			for model := range capacitySource.visibleModels {
				protected[model] = true
			}
			continue
		}
		for model := range rawModelCapacitySourceIDs(capacitySource.body) {
			protected[model] = true
		}
	}
	body, err := projectModelCapacityEnvelope(manifest.Body, codex, func(model string) ResolvedModelContextCapacity {
		return catalog.resolve(ctx, group.Platform, model)
	}, protected)
	if err != nil {
		return err
	}
	manifest.Body = body
	manifest.ETag = codexModelsManifestBodyETag(body)
	if codexModelsManifestETagMatches(ifNoneMatch, manifest.ETag) {
		manifest.Body, manifest.NotModified = nil, true
	}
	return nil
}

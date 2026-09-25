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

// ApplyModelContextCapacityToFields writes one known capacity onto a model row.
// It never changes model membership, request budgets, or tool/reasoning
// metadata. An unknown capacity leaves the row untouched: nothing is invented,
// and a row's own upstream fields are not erased by ignorance.
func ApplyModelContextCapacityToFields(fields map[string]json.RawMessage, capacity ResolvedModelContextCapacity, codex bool) bool {
	if fields == nil || !capacity.Known() {
		return false
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
			// Do not retain limits from evidence that lost to this answer.
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

// modelCapacityCandidateLister is implemented by the SQL account repository:
// every active account that can serve a model bounds its capacity. The
// scheduler's schedulable switch changes routing, not what an upstream holds.
type modelCapacityCandidateLister interface {
	ListModelCapacityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error)
}

func listModelCapacityCandidates(ctx context.Context, repo AccountRepository, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error) {
	if lister, ok := repo.(modelCapacityCandidateLister); ok {
		return lister.ListModelCapacityCandidates(ctx, groupID, platforms, includeGrouped)
	}
	// Repositories without the capacity query (test doubles) expose their
	// configured availability pool instead.
	return repo.ListModelAvailabilityCandidates(ctx, groupID, platforms, includeGrouped)
}

// groupModelCapacityCatalog is a request-local view of the group's capacity
// candidates. Evidence is decoded once per account, not once per model, and no
// upstream or registry I/O is performed here.
type groupModelCapacityCatalog struct {
	accounts        []Account
	evidence        []*accountModelCapacityEvidence
	routes          []CompositeModelRoute
	available       bool
	routesAvailable bool
	groupID         *int64
	group           *Group
	channelService  *ChannelService
	channelLookups  map[string]*channelLookup
	channelErrors   map[string]bool
}

func newGroupModelCapacityCatalog(accounts []Account, available bool, routes []CompositeModelRoute, routesAvailable bool, groupID *int64, channels *ChannelService) *groupModelCapacityCatalog {
	catalog := &groupModelCapacityCatalog{
		accounts: accounts, available: available, routes: routes, routesAvailable: routesAvailable,
		groupID: groupID, channelService: channels,
		channelLookups: make(map[string]*channelLookup), channelErrors: make(map[string]bool),
		evidence: make([]*accountModelCapacityEvidence, len(accounts)),
	}
	for i := range catalog.accounts {
		catalog.evidence[i] = newAccountModelCapacityEvidence(&catalog.accounts[i])
	}
	return catalog
}

func loadGroupModelCapacityCatalog(ctx context.Context, repo AccountRepository, routesRepo CompositeModelRouteRepository, channels *ChannelService, cfg *config.Config, groupID *int64, platform string) *groupModelCapacityCatalog {
	platforms := []string{platform}
	useMixed := platform == PlatformAnthropic || platform == PlatformGemini
	if useMixed {
		platforms = append(platforms, PlatformAntigravity)
	} else if platform == PlatformComposite {
		platforms = []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo}
	}
	queryGroupID, includeGrouped := groupID, false
	if cfg != nil && cfg.RunMode == config.RunModeSimple && (!useMixed || groupID == nil) {
		queryGroupID, includeGrouped = nil, true
	}
	var accounts []Account
	available := false
	if repo != nil {
		var err error
		accounts, err = listModelCapacityCandidates(ctx, repo, queryGroupID, platforms, includeGrouped)
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
	return newGroupModelCapacityCatalog(accounts, available, routes, routesAvailable, groupID, channels)
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

// possibleAccountTargets lists the real upstream models an account may forward
// a public model to on the text paths (Responses, Chat passthrough, configured
// Messages dispatch). A list has no endpoint, so every one of them counts.
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

// resolve is the group answer for one public model: the minimum over every
// active candidate account (and every upstream target it may forward to) whose
// capacity is known. Candidates without evidence do not lower the minimum; when
// no candidate is known the model has no capacity.
func (catalog *groupModelCapacityCatalog) resolve(ctx context.Context, platform, model string) ResolvedModelContextCapacity {
	if isMediaModelForCapacity(model) {
		return unknownModelContextCapacity("media_model")
	}
	if !catalog.available {
		return unknownModelContextCapacity("account_query_failed")
	}
	target := capacityModelTarget{platform: platform, model: model}
	if platform == PlatformComposite {
		var reason string
		if target, reason = catalog.compositeTarget(model); reason != "" {
			return unknownModelContextCapacity(reason)
		}
	}
	lookup, ok := catalog.channel(ctx, target.platform)
	if !ok {
		return unknownModelContextCapacity("channel_query_failed")
	}
	mapping := ChannelMappingResult{MappedModel: target.model}
	if lookup != nil {
		mapping = resolveMapping(lookup, *catalog.groupID, target.model)
		if billingModel := billingModelForRestriction(mapping.BillingModelSource, target.model, mapping.MappedModel); billingModel != "" && checkRestricted(lookup, *catalog.groupID, billingModel) {
			return unknownModelContextCapacity("model_route_restricted")
		}
	}
	candidates := 0
	known := make([]ResolvedModelContextCapacity, 0)
	for i := range catalog.accounts {
		account := &catalog.accounts[i]
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
		for _, upstreamModel := range dedupeAndSortModelIDs(upstreamModels) {
			if lookup != nil && lookup.channel.BillingModelSource == BillingModelSourceUpstream && checkRestricted(lookup, *catalog.groupID, upstreamModel) {
				continue
			}
			candidates++
			if capacity := catalog.evidence[i].resolve(upstreamModel, true); capacity.Known() {
				known = append(known, capacity)
			}
		}
	}
	switch {
	case candidates == 0:
		return unknownModelContextCapacity("no_model_candidate")
	case len(known) == 0:
		return unknownModelContextCapacity("no_capacity_evidence")
	}
	return minimumModelContextCapacity(known)
}

func modelContextSourceRank(source string) int {
	switch source {
	case ModelContextSourceCustom:
		return 0
	case ModelContextSourceUpstream:
		return 1
	case ModelContextSourceOfficial:
		return 2
	default:
		return 3
	}
}

// minimumModelContextCapacity takes the smallest known value of every limit so
// that no candidate is promised a window it cannot hold. The answer carries the
// source of the smallest default window.
func minimumModelContextCapacity(candidates []ResolvedModelContextCapacity) ResolvedModelContextCapacity {
	result := candidates[0]
	minimum := func(current, value int64) int64 {
		if value > 0 && (current <= 0 || value < current) {
			return value
		}
		return current
	}
	var maximum, input, output int64
	for _, candidate := range candidates {
		if candidate.ContextWindow < result.ContextWindow ||
			(candidate.ContextWindow == result.ContextWindow && modelContextSourceRank(candidate.Source) < modelContextSourceRank(result.Source)) {
			result = candidate
		}
		candidateMaximum := candidate.MaxContextWindow
		if candidateMaximum < candidate.ContextWindow {
			candidateMaximum = candidate.ContextWindow
		}
		maximum = minimum(maximum, candidateMaximum)
		input = minimum(input, candidate.MaxInputTokens)
		output = minimum(output, candidate.MaxOutputTokens)
	}
	result.MaxContextWindow, result.MaxInputTokens, result.MaxOutputTokens = maximum, input, output
	if input > result.ContextWindow {
		result.MaxInputTokens = 0
	}
	if output > result.ContextWindow {
		result.MaxOutputTokens = 0
	}
	if len(candidates) > 1 {
		result.Reason = "group_minimum"
	}
	return result
}

func projectModelCapacityEnvelope(body []byte, codex bool, resolve func(string) ResolvedModelContextCapacity) ([]byte, error) {
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
	for i, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil || fields == nil {
			continue
		}
		var model string
		if json.Unmarshal(fields[modelKey], &model) != nil || strings.TrimSpace(model) == "" {
			continue
		}
		if ApplyModelContextCapacityToFields(fields, resolve(model), codex) {
			rows[i], _ = json.Marshal(fields)
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	envelope[listKey], _ = json.Marshal(rows)
	return json.Marshal(envelope)
}

// ProjectModelListContextCapacities adds known capacities to a /v1/models body
// without changing its platform-specific row shape, IDs, order or filters.
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
		catalog = loadGroupModelCapacityCatalog(ctx, s.accountRepo, routesRepo, s.channelService, s.cfg, groupID, platform)
	}
	catalog.group = group
	return projectModelCapacityEnvelope(body, false, func(model string) ResolvedModelContextCapacity {
		return catalog.resolve(ctx, platform, model)
	})
}

// ProjectCodexModelContextCapacities runs after all group merges and before
// conditional response handling. It never enters the raw upstream cache, so a
// changed capacity changes the final ETag.
func (s *OpenAIGatewayService) ProjectCodexModelContextCapacities(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, ifNoneMatch string) error {
	if manifest == nil || manifest.NotModified || len(manifest.Body) == 0 || group == nil {
		return nil
	}
	catalog := loadGroupModelCapacityCatalog(ctx, s.accountRepo, nil, s.channelService, s.cfg, &group.ID, group.Platform)
	return projectOpenAIModelsContextCapacities(ctx, group, manifest, ifNoneMatch, catalog, true)
}

// ProjectOpenAIModelsListContextCapacities is the final overlay for
// upstream-discovered ordinary catalogs.
func (s *OpenAIGatewayService) ProjectOpenAIModelsListContextCapacities(ctx context.Context, group *Group, response *OpenAIModelsResponse, ifNoneMatch string) error {
	if response == nil || response.NotModified || len(response.Body) == 0 || group == nil {
		return nil
	}
	catalog := loadGroupModelCapacityCatalog(ctx, s.accountRepo, nil, s.channelService, s.cfg, &group.ID, group.Platform)
	return projectOpenAIModelsContextCapacities(ctx, group, response, ifNoneMatch, catalog, false)
}

// The shared gateway owns composite route configuration, including the
// instance attached to the OpenAI-compatible handler for native dispatch.
func (s *GatewayService) ProjectCodexModelContextCapacities(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, ifNoneMatch string) error {
	if manifest == nil || manifest.NotModified || len(manifest.Body) == 0 || group == nil {
		return nil
	}
	var routesRepo CompositeModelRouteRepository
	if s.compositeResolver != nil {
		routesRepo = s.compositeResolver.repo
	}
	catalog := loadGroupModelCapacityCatalog(ctx, s.accountRepo, routesRepo, s.channelService, s.cfg, &group.ID, group.Platform)
	return projectOpenAIModelsContextCapacities(ctx, group, manifest, ifNoneMatch, catalog, true)
}

func projectOpenAIModelsContextCapacities(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, ifNoneMatch string, catalog *groupModelCapacityCatalog, codex bool) error {
	catalog.group = group
	body, err := projectModelCapacityEnvelope(manifest.Body, codex, func(model string) ResolvedModelContextCapacity {
		return catalog.resolve(ctx, group.Platform, model)
	})
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

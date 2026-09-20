package catalog

import (
	"context"
	"encoding/json"
	"errors"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"sync"
)

type Module struct {
	mu     sync.RWMutex
	config extensionv1.CindyProviderConfig
}

func New() *Module { return &Module{config: extensionv1.CindyProviderConfig{BalanceDetection: true}} }
func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	cfg := extensionv1.CindyProviderConfig{BalanceDetection: true}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(raw, &cfg) != nil {
		return nil, errors.New("invalid Cindy provider configuration")
	}
	for key, value := range fields {
		if (key != "balance_detection" && key != "catalog_enabled" && key != "search_enabled") || (string(value) != "true" && string(value) != "false") {
			return nil, errors.New("unknown or invalid Cindy provider setting")
		}
	}
	return json.Marshal(cfg)
}
func (m *Module) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	normalized, err := m.ValidateConfig(ctx, raw)
	if err != nil {
		return err
	}
	var cfg extensionv1.CindyProviderConfig
	if err := json.Unmarshal(normalized, &cfg); err != nil {
		return err
	}
	m.mu.Lock()
	m.config = cfg
	m.mu.Unlock()
	return nil
}
func (m *Module) Status(context.Context) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(m.config)
}
func (m *Module) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if ctx.Err() != nil {
		return extensionv1.Result{}, ctx.Err()
	}
	m.mu.RLock()
	registry := Registry{Config: m.config}
	m.mu.RUnlock()
	if in.Capability == extensionv1.CapabilityAdmin && in.Operation == "provider.describe" {
		raw, err := json.Marshal(registry.CindyCatalogModels())
		return extensionv1.Result{Payload: raw}, err
	}
	if in.Capability != extensionv1.CapabilityProvider {
		return extensionv1.Result{}, errors.New("unsupported Cindy provider capability")
	}
	switch in.Operation {
	case "cindy.duplicates.plan":
		var request []extensionv1.CindyDuplicateCandidate
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid duplicate facts")
		}
		raw, err := json.Marshal(duplicateInventory(request))
		return extensionv1.Result{Payload: raw}, err
	case "cindy.groups.input":
		var request extensionv1.CindyGroupInputRequest
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid group input")
		}
		input, code := normalizeGroupInput(request)
		raw, err := json.Marshal(input)
		return extensionv1.Result{Payload: raw, Code: code}, err
	case "cindy.groups.partition":
		var request extensionv1.CindyGroupPartitionRequest
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid group membership facts")
		}
		plan, code := partitionGroup(request)
		raw, err := json.Marshal(plan)
		return extensionv1.Result{Payload: raw, Code: code}, err
	case "cindy.probe.plan", "cindy.probe.decide":
		if !registry.Config.BalanceDetection {
			return extensionv1.Result{Code: "disabled"}, nil
		}
		if in.Operation == "cindy.probe.plan" {
			raw, err := json.Marshal(balanceProbePlan())
			return extensionv1.Result{Payload: raw}, err
		}
		var input extensionv1.CindyProbeResult
		if err := json.Unmarshal(in.Payload, &input); err != nil {
			return extensionv1.Result{}, errors.New("invalid Cindy probe result")
		}
		decision, err := decideBalanceProbe(input)
		if err != nil {
			return extensionv1.Result{}, err
		}
		raw, err := json.Marshal(decision)
		return extensionv1.Result{Payload: raw}, err
	case "cindy.health":
		var observed extensionv1.CindyObservedResponse
		if json.Unmarshal(in.Payload, &observed) != nil {
			return extensionv1.Result{}, errors.New("invalid observed response")
		}
		raw, err := json.Marshal(registry.ClassifyResponse(observed))
		return extensionv1.Result{Payload: raw}, err
	case "cindy.features":
		raw, err := json.Marshal(registry.Config)
		return extensionv1.Result{Payload: raw}, err
	case "cindy.catalog":
		var query extensionv1.CindyCatalogQuery
		if json.Unmarshal(in.Payload, &query) != nil {
			return extensionv1.Result{}, errors.New("invalid Cindy catalog query")
		}
		registry.Images = query.Images
		raw, err := registry.Query(query)
		return extensionv1.Result{Payload: raw}, err
	case "cindy.pricing":
		raw, err := json.Marshal(registry.PricingSnapshot())
		return extensionv1.Result{Payload: raw}, err
	}
	return extensionv1.Result{}, errors.New("unsupported Cindy provider operation")
}
func (r Registry) Query(query extensionv1.CindyCatalogQuery) (json.RawMessage, error) {
	switch query.Method {
	case "ManagedChannelProjection":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		mapping, models := r.managedChannelProjection()
		return json.Marshal([]any{mapping, models})
	case "ManagedChannelModelAllowed":
		var model string
		if len(query.Args) != 1 || json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.managedChannelModelAllowed(model)})
	case "cindyModelCapabilityFromCapability":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var capability CindyCapability
		if json.Unmarshal(query.Args[0], &capability) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.cindyModelCapabilityFromCapability(capability)})
	case "CindyCapabilities":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyCapabilities()})
	case "CindyCatalogModels":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyCatalogModels()})
	case "CindyManagedModelMappings":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyManagedModelMappings()})
	case "ResolveCindyCapability":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		value, found := r.ResolveCindyCapability(model)
		return json.Marshal([]any{value, found})
	case "resolveKnownCindyCapability":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		value, found := r.resolveKnownCindyCapability(model)
		return json.Marshal([]any{value, found})
	case "CindyCompatibilityMappedUpstreamModel":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		value, found := r.CindyCompatibilityMappedUpstreamModel(model)
		return json.Marshal([]any{value, found})
	case "CindyCompatibilityRoutingTarget":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.CindyCompatibilityRoutingTarget(model)})
	case "CindyCompatibilityTextPricingForModel":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		value, found := r.CindyCompatibilityTextPricingForModel(model)
		return json.Marshal([]any{value, found})
	case "CindyMappedUpstreamModel":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		value, found := r.CindyMappedUpstreamModel(model)
		return json.Marshal([]any{value, found})
	case "CindyModelSupportsEndpoint":
		if len(query.Args) != 2 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		var endpoint CindyEndpoint
		if json.Unmarshal(query.Args[1], &endpoint) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.CindyModelSupportsEndpoint(model, endpoint)})
	case "CindyFreePoolModelSupportsEndpoint":
		if len(query.Args) != 2 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		var endpoint CindyEndpoint
		if json.Unmarshal(query.Args[1], &endpoint) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.CindyFreePoolModelSupportsEndpoint(model, endpoint)})
	case "CindyFreePoolModelAllowed":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.CindyFreePoolModelAllowed(model)})
	case "CindyAlphaSearchModelAvailable":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.CindyAlphaSearchModelAvailable(model)})
	case "CindyAlphaSearchUpstreamModel":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		value, found := r.CindyAlphaSearchUpstreamModel(model)
		return json.Marshal([]any{value, found})
	case "CindyManagedCompatibilityModels":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyManagedCompatibilityModels()})
	case "CindyManagedCompatibilityAliases":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyManagedCompatibilityAliases()})
	case "CindyModelHasVerifiedEndpoint":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.CindyModelHasVerifiedEndpoint(model)})
	case "CindyModelUsesExplicitZeroPrice":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.CindyModelUsesExplicitZeroPrice(model)})
	case "CindyImagePricingForModel":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		value, found := r.CindyImagePricingForModel(model)
		return json.Marshal([]any{value, found})
	case "CindyTextPricingForModel":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var model string
		if json.Unmarshal(query.Args[0], &model) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		value, found := r.CindyTextPricingForModel(model)
		return json.Marshal([]any{value, found})
	case "CindyPublicModelIDs":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyPublicModelIDs()})
	case "CindyCodexPublicModelIDs":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyCodexPublicModelIDs()})
	case "cindyCapabilitySupportsCodexModels":
		if len(query.Args) != 1 {
			return nil, errors.New("invalid catalog arguments")
		}
		var capability CindyCapability
		if json.Unmarshal(query.Args[0], &capability) != nil {
			return nil, errors.New("invalid catalog argument")
		}
		return json.Marshal([]any{r.cindyCapabilitySupportsCodexModels(capability)})
	case "CindyVerifiedCapabilities":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyVerifiedCapabilities()})
	case "CindyVerifiedModelCapabilities":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyVerifiedModelCapabilities()})
	case "CindyImageModelCapabilities":
		if len(query.Args) != 0 {
			return nil, errors.New("invalid catalog arguments")
		}
		return json.Marshal([]any{r.CindyImageModelCapabilities()})

	}
	return nil, errors.New("undeclared catalog function")
}
func PricingKey(method, model string) string {
	raw, _ := json.Marshal([]string{method, model})
	return string(raw)
}
func (r Registry) PricingSnapshot() extensionv1.CindyPricingSnapshot {
	out := extensionv1.CindyPricingSnapshot{Config: r.Config, Results: make(map[string]json.RawMessage)}
	ids := map[string]bool{}
	for _, entry := range cindyCapabilityCatalog {
		ids[entry.PublicID] = true
		ids[entry.LiveUpstreamID] = true
	}
	for alias := range cindyCompatibilityAliases {
		ids[alias] = true
	}
	for id := range ids {
		rawID, _ := json.Marshal(id)
		for _, method := range []string{"CindyTextPricingForModel", "CindyCompatibilityTextPricingForModel", "CindyImagePricingForModel", "CindyModelUsesExplicitZeroPrice", "resolveKnownCindyCapability", "CindyCompatibilityRoutingTarget"} {
			result, err := r.Query(extensionv1.CindyCatalogQuery{Method: method, Args: []json.RawMessage{rawID}})
			if err == nil {
				out.Results[PricingKey(method, id)] = result
			}
		}
	}
	return out
}

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const ManagedModelRoutesVersion = 2
const ManagedModelEndpointResponsesWebSocket = "responses_websocket"

// ErrManagedModelRouteUnavailable is deliberately independent of upstream
// errors: a stale publication is a local routing failure, not a dead credential.
var ErrManagedModelRouteUnavailable = infraerrors.New(http.StatusNotFound, "MANAGED_MODEL_ROUTE_UNAVAILABLE", "This public model route is unavailable; ask the administrator to verify its published capabilities")

type managedModelRequestContextKey struct{}

// ManagedModelRequest freezes the published route for one HTTP request or WS
// turn. It never contains credentials, and is not part of a client response.
type ManagedModelRequest struct {
	Version        int
	GroupID        int64
	GroupPlatform  string
	QuotaPlatform  string
	Endpoint       string
	SubmittedModel string
	Route          ManagedModelRoute
	Branch         *ManagedModelRouteBranch
}

// ManagedModelRoutesVersionSupported keeps existing publications readable;
// merely opening or extending a group never rewrites its old account mappings.
func ManagedModelRoutesVersionSupported(version int) bool {
	return version == 1 || version == ManagedModelRoutesVersion
}

// ManagedModelRouteBranches returns a read-only projection of both formats.
func ManagedModelRouteBranches(route ManagedModelRoute) []ManagedModelRouteBranch {
	if len(route.Branches) > 0 {
		return route.Branches
	}
	if route.Selector == "" && len(route.Accounts) == 0 {
		return nil
	}
	return []ManagedModelRouteBranch{{Selector: route.Selector, TargetPlatform: route.TargetPlatform, Endpoints: route.Endpoints, Accounts: route.Accounts}}
}

func (request *ManagedModelRequest) RoutingModel() string {
	if request == nil {
		return ""
	}
	if request.Branch != nil {
		return request.Branch.Selector
	}
	return request.Route.Selector
}

func (request *ManagedModelRequest) TargetPlatform() string {
	if request == nil {
		return ""
	}
	if request.Branch != nil {
		return request.Branch.TargetPlatform
	}
	return request.Route.TargetPlatform
}

func ManagedModelSelector(groupID int64, publicModel string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(publicModel)))
	return fmt.Sprintf("s2pub-g%d-m%s", groupID, hex.EncodeToString(sum[:8]))
}

// ManagedModelBranchSelector does not normalize real wire identifiers. Version,
// VIP/CC suffixes, case and protocol remain distinct, including on one account.
func ManagedModelBranchSelector(groupID int64, publicModel, platform, protocol, upstreamModel string) string {
	identity, _ := json.Marshal([]string{strings.TrimSpace(publicModel), platform, protocol, upstreamModel})
	sum := sha256.Sum256(identity)
	return fmt.Sprintf("s2pub-g%d-b%s", groupID, hex.EncodeToString(sum[:12]))
}

func managedModelBranchValid(group *Group, route ManagedModelRoute, branch ManagedModelRouteBranch) bool {
	if !isConcreteRequestPlatform(branch.TargetPlatform) || branch.TargetPlatform == PlatformCindy || len(branch.Accounts) == 0 || len(branch.Endpoints) == 0 {
		return false
	}
	if branch.UpstreamProtocol == "" {
		return branch.Selector == ManagedModelSelector(group.ID, route.PublicModel) &&
			(group.Platform == PlatformComposite || branch.TargetPlatform == group.Platform)
	}
	if !ManagedModelBranchProtocolSupported(branch.TargetPlatform, branch.UpstreamProtocol) {
		return false
	}
	upstream := branch.Accounts[0].UpstreamModel
	if strings.TrimSpace(upstream) == "" || IsManagedModelSelector(upstream) || branch.Selector != ManagedModelBranchSelector(group.ID, route.PublicModel, branch.TargetPlatform, branch.UpstreamProtocol, upstream) {
		return false
	}
	seen := make(map[int64]bool, len(branch.Accounts))
	for _, member := range branch.Accounts {
		if member.AccountID <= 0 || member.UpstreamModel != upstream || member.AccountFingerprint == "" || seen[member.AccountID] {
			return false
		}
		seen[member.AccountID] = true
		for _, endpoint := range member.Endpoints {
			if !managedModelEndpointAllowed(endpoint) || !managedModelHasEndpoint(branch.Endpoints, endpoint) {
				return false
			}
		}
	}
	for _, endpoint := range branch.Endpoints {
		if endpoint != CompositeRouteEndpointResponses && endpoint != CompositeRouteEndpointMessages && endpoint != CompositeRouteEndpointChatCompletions {
			return false
		}
	}
	return true
}

func managedModelQuotaPlatform(group *Group, route ManagedModelRoute) string {
	if group.Platform != PlatformComposite {
		return group.Platform
	}
	if isConcreteRequestPlatform(route.QuotaPlatform) {
		return route.QuotaPlatform
	}
	// Retaining a v1 route must retain the quota ledger it previously used,
	// even when a new compatible provider joins the same public model.
	if isConcreteRequestPlatform(route.TargetPlatform) {
		return route.TargetPlatform
	}
	if platform, ok := DetectModelPlatform(route.PublicModel); ok {
		return platform
	}
	platform := ""
	for _, branch := range ManagedModelRouteBranches(route) {
		if platform != "" && platform != branch.TargetPlatform {
			return ""
		}
		platform = branch.TargetPlatform
	}
	return platform
}

// Only deterministic, existing production adapters are admitted. Metadata and
// WebSocket protocols need their own evidence and are not generative branches.
func ManagedModelBranchProtocolSupported(platform, protocol string) bool {
	switch platform {
	case PlatformAnthropic:
		return protocol == CompositeRouteEndpointMessages
	case PlatformOpenAI:
		return protocol == CompositeRouteEndpointResponses || protocol == CompositeRouteEndpointChatCompletions
	default:
		if !IsCNProvider(platform) {
			return false
		}
		return protocol == CompositeRouteEndpointMessages || protocol == CompositeRouteEndpointChatCompletions ||
			(protocol == CompositeRouteEndpointResponses && (&Account{Platform: platform}).SupportsNativeCNResponses())
	}
}

// ManagedModelUsesOpenAIAdapter identifies the already deployed single-attempt
// forwarding family, not a model manufacturer. Grok is included for retained
// v1 routes only; it does not acquire an unproven new wire through this helper.
func ManagedModelUsesOpenAIAdapter(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformGrok || IsCNProvider(platform)
}

// EffectiveManagedModelAllowlist keeps the public catalog fail closed even if
// an ordinary group edit disables/loosens the derived allowlist. Admin DTOs
// still expose the stored fields so the management page can report the drift.
func EffectiveManagedModelAllowlist(group *Group) GroupModelAllowlist {
	if group == nil {
		return GroupModelAllowlist{}
	}
	if !group.ManagedModelRoutes.Enabled {
		return group.ModelAllowlist
	}
	allowlist := GroupModelAllowlist{Enabled: true}
	if !ManagedModelRoutesVersionSupported(group.ManagedModelRoutes.Version) {
		return allowlist
	}
	seen := make(map[string]bool)
	for _, route := range group.ManagedModelRoutes.Routes {
		model := strings.TrimSpace(route.PublicModel)
		if model == "" || IsManagedModelSelector(model) || len(ManagedModelRouteBranches(route)) == 0 || seen[strings.ToLower(model)] {
			continue
		}
		seen[strings.ToLower(model)] = true
		allowlist.Models = append(allowlist.Models, model)
	}
	return allowlist
}

func managedModelEndpointAllowed(endpoint string) bool {
	switch endpoint {
	case CompositeRouteEndpointResponses, CompositeRouteEndpointMessages,
		CompositeRouteEndpointCountTokens, CompositeRouteEndpointChatCompletions,
		ManagedModelEndpointResponsesWebSocket:
		return true
	default:
		return false
	}
}

func managedModelHasEndpoint(endpoints []string, endpoint string) bool {
	for _, item := range endpoints {
		if item == endpoint {
			return true
		}
	}
	return false
}

// ResolveManagedModelRoute accepts only a uniquely published public name (or
// an explicit/established equivalent spelling). Internal selectors are never
// client model names, even when a stale account mapping exposes them elsewhere.
func ResolveManagedModelRoute(group *Group, requestedModel, endpoint string) (*ManagedModelRequest, error) {
	if group == nil || !group.ManagedModelRoutes.Enabled {
		return nil, nil
	}
	config := group.ManagedModelRoutes
	requestedModel = strings.TrimSpace(requestedModel)
	if !ManagedModelRoutesVersionSupported(config.Version) || group.ID <= 0 ||
		group.Platform == PlatformCindy || requestedModel == "" ||
		IsManagedModelSelector(requestedModel) || !managedModelEndpointAllowed(endpoint) {
		return nil, ErrManagedModelRouteUnavailable
	}
	candidates := groupModelAllowlistCandidates(requestedModel)
	var matched *ManagedModelRequest
	for _, route := range config.Routes {
		if !managedModelHasEndpoint(route.Endpoints, endpoint) {
			continue
		}
		names := append([]string{route.PublicModel}, route.Aliases...)
		matches := false
		for _, name := range names {
			for _, candidate := range candidates {
				if strings.EqualFold(strings.TrimSpace(name), candidate) {
					matches = true
				}
			}
		}
		if !matches {
			continue
		}
		if matched != nil || strings.TrimSpace(route.PublicModel) == "" || IsManagedModelSelector(route.PublicModel) || len(ManagedModelRouteBranches(route)) == 0 || (config.Version == 1 && len(route.Branches) > 0) {
			return nil, ErrManagedModelRouteUnavailable
		}
		seenSelectors := make(map[string]bool)
		endpointAvailable := false
		for _, branch := range ManagedModelRouteBranches(route) {
			if !managedModelBranchValid(group, route, branch) || seenSelectors[branch.Selector] {
				return nil, ErrManagedModelRouteUnavailable
			}
			seenSelectors[branch.Selector] = true
			endpointAvailable = endpointAvailable || managedModelHasEndpoint(branch.Endpoints, endpoint)
		}
		if !endpointAvailable {
			return nil, ErrManagedModelRouteUnavailable
		}
		quotaPlatform := managedModelQuotaPlatform(group, route)
		if config.Version == ManagedModelRoutesVersion && !isConcreteRequestPlatform(quotaPlatform) {
			return nil, ErrManagedModelRouteUnavailable
		}
		matched = &ManagedModelRequest{Version: config.Version, GroupID: group.ID, GroupPlatform: group.Platform, QuotaPlatform: quotaPlatform, Endpoint: endpoint, SubmittedModel: requestedModel, Route: route}
		if err := projectManagedModelLegacyMetadata(matched); err != nil {
			return nil, err
		}
	}
	if matched == nil {
		return nil, ErrManagedModelRouteUnavailable
	}
	return matched, nil
}

func WithManagedModelRequest(ctx context.Context, request *ManagedModelRequest) context.Context {
	if ctx == nil || request == nil {
		return ctx
	}
	copy := *request
	if request.Version == ManagedModelRoutesVersion {
		if existing, ok := ManagedModelRequestFromContext(ctx); ok && existing.GroupID == request.GroupID && existing.Route.PublicModel == request.Route.PublicModel {
			copy.QuotaPlatform = existing.QuotaPlatform
		} else if platform, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok && isConcreteRequestPlatform(platform) {
			// Freeze only the original ingress override, never an attempt's
			// later ForcePlatform used solely to select the wire adapter.
			copy.QuotaPlatform = platform
		}
	}
	return context.WithValue(ctx, managedModelRequestContextKey{}, copy)
}

func ManagedModelRequestFromContext(ctx context.Context) (*ManagedModelRequest, bool) {
	if ctx == nil {
		return nil, false
	}
	request, ok := ctx.Value(managedModelRequestContextKey{}).(ManagedModelRequest)
	if !ok {
		return nil, false
	}
	return &request, true
}

// WithManagedModelBranch freezes an already published path, not an arbitrary
// caller-supplied route. The platform override is request-local and never edits
// the group's or account's private routing configuration.
func WithManagedModelBranch(ctx context.Context, branch ManagedModelRouteBranch) context.Context {
	request, ok := ManagedModelRequestFromContext(ctx)
	if !ok {
		return ctx
	}
	for _, published := range ManagedModelRouteBranches(request.Route) {
		if published.Selector == branch.Selector {
			copy := published
			request.Branch = &copy
			ctx = WithManagedModelRequest(ctx, request)
			ctx = WithResolvedTargetPlatform(ctx, published.TargetPlatform)
			ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, request.Route.PublicModel)
			ctx = context.WithValue(ctx, ctxkey.ResolvedUpstreamModel, published.Selector)
			return context.WithValue(ctx, ctxkey.ForcePlatform, published.TargetPlatform)
		}
	}
	return ctx
}

func managedModelBillingModel(ctx context.Context, groupID int64, model string) string {
	request, managed := ManagedModelRequestFromContext(ctx)
	if !managed || request.Version != ManagedModelRoutesVersion || request.GroupID != groupID {
		return model
	}
	for _, branch := range ManagedModelRouteBranches(request.Route) {
		if model == branch.Selector && (request.Branch == nil || request.Branch.Selector == branch.Selector) {
			return request.Route.PublicModel
		}
	}
	return model
}

// ManagedModelAccountAllowed is an additional candidate/forwarding invariant,
// not an alternative scheduler. It prevents a missing/edited channel mapping
// from falling back to a preserved private model mapping on the same account.
func ManagedModelAccountAllowed(ctx context.Context, account *Account, routingModel string) bool {
	request, managed := ManagedModelRequestFromContext(ctx)
	if !managed {
		return !managedSelectorWithoutPublishedContext(account, routingModel)
	}
	if account == nil ||
		account.IsOpenAIPassthroughEnabled() || account.IsAnthropicAPIKeyPassthroughEnabled() {
		return false
	}
	if request.Version == ManagedModelRoutesVersion && IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return false
	}
	mapped, exact := account.GetModelMapping()[routingModel]
	if !exact || mapped == "" {
		return false
	}
	fingerprint := ""
	for _, branch := range ManagedModelRouteBranches(request.Route) {
		if branch.Selector != routingModel || branch.TargetPlatform != account.Platform || !managedModelHasEndpoint(branch.Endpoints, request.Endpoint) ||
			(request.Branch != nil && request.Branch.Selector != branch.Selector) {
			continue
		}
		for _, member := range branch.Accounts {
			if member.AccountID != account.ID || member.UpstreamModel != mapped ||
				!managedModelHasEndpoint(member.Endpoints, request.Endpoint) || member.AccountFingerprint == "" {
				continue
			}
			if fingerprint == "" {
				fingerprint = managedModelFingerprintForRequest(ctx, account)
			}
			if fingerprint != "" && fingerprint == member.AccountFingerprint {
				return true
			}
		}
	}
	return false
}

func ValidateManagedModelAccount(ctx context.Context, account *Account, routingModel string) error {
	if !ManagedModelAccountAllowed(ctx, account, routingModel) {
		return ErrManagedModelRouteUnavailable
	}
	return nil
}

// PrepareManagedModelRequest preserves the original JSON except for the model
// spelling and an explicitly encoded reasoning alias. Conflicting/duplicate
// model keys are rejected instead of allowing parsers to bind different values.
func PrepareManagedModelRequest(group *Group, endpoint string, body []byte, fallbackModel string) ([]byte, *ManagedModelRequest, error) {
	if group == nil || !group.ManagedModelRoutes.Enabled {
		return body, nil, nil
	}
	if !gjson.ValidBytes(body) {
		return nil, nil, ErrManagedModelRouteUnavailable
	}
	modelKey, requested := "", ""
	count := 0
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		if strings.EqualFold(key.String(), "model") {
			count++
			modelKey = key.String()
			if value.Type == gjson.String {
				requested = value.String()
			}
		}
		return true
	})
	if count > 1 || (count == 1 && strings.TrimSpace(requested) == "") {
		return nil, nil, ErrManagedModelRouteUnavailable
	}
	if count == 0 {
		requested = fallbackModel
	}
	request, err := ResolveManagedModelRoute(group, requested, endpoint)
	if err != nil {
		return nil, nil, err
	}
	if request == nil {
		return body, nil, nil
	}
	if endpoint == CompositeRouteEndpointMessages || endpoint == CompositeRouteEndpointCountTokens {
		if _, effort, known := resolveOpenAIModelReasoningAlias(requested); known && !gjson.GetBytes(body, "output_config.effort").Exists() {
			body, err = sjson.SetBytes(body, "output_config.effort", effort)
		}
	} else {
		body, _, err = materializeOpenAIModelReasoningEffort(body, requested)
	}
	if err != nil {
		return nil, nil, ErrManagedModelRouteUnavailable
	}
	if modelKey != "" && modelKey != "model" {
		body, err = sjson.DeleteBytes(body, modelKey)
		if err != nil {
			return nil, nil, ErrManagedModelRouteUnavailable
		}
	}
	body, err = sjson.SetBytes(body, "model", request.Route.PublicModel)
	if err != nil {
		return nil, nil, ErrManagedModelRouteUnavailable
	}
	return body, request, nil
}

// ValidateManagedForwardAccount refreshes the account at the final boundary so
// a stale scheduler snapshot cannot retain a route after credentials or its
// exact mapping were edited. The account passed to the forwarder must match too.
func validateManagedForwardAccount(ctx context.Context, repo AccountRepository, account *Account, routingModel string) error {
	request, managed := ManagedModelRequestFromContext(ctx)
	if !managed {
		if managedSelectorWithoutPublishedContext(account, routingModel) {
			return ErrManagedModelRouteUnavailable
		}
		return nil
	}
	// A precomputed metadata digest is sufficient only while ranking candidates.
	// The actual forwarder must have hydrated the full account and the final
	// repository read must also be full; neither can borrow that cached proof.
	if account == nil || account.SchedulerMetadata != nil {
		return ErrManagedModelRouteUnavailable
	}
	if err := ValidateManagedModelAccount(ctx, account, routingModel); err != nil || repo == nil {
		return ErrManagedModelRouteUnavailable
	}
	latest, err := repo.GetByID(ctx, account.ID)
	if err != nil || latest == nil || latest.SchedulerMetadata != nil || !latest.IsSchedulable() || !managedModelAccountInGroup(latest, request.GroupID) || !ManagedModelAccountAllowed(ctx, latest, routingModel) {
		return ErrManagedModelRouteUnavailable
	}
	if latest.ProxyID != nil && (latest.Proxy == nil || !latest.Proxy.IsActive() || latest.Proxy.IsExpired(time.Now())) {
		return ErrManagedModelRouteUnavailable
	}
	return nil
}

func managedModelAccountInGroup(account *Account, groupID int64) bool {
	for _, id := range account.GroupIDs {
		if id == groupID {
			return true
		}
	}
	for _, binding := range account.AccountGroups {
		if binding.GroupID == groupID {
			return true
		}
	}
	for _, group := range account.Groups {
		if group != nil && group.ID == groupID {
			return true
		}
	}
	return false
}

// LatestManagedModelGroup is intentionally uncached for WS turn admission: an
// established connection must not keep a withdrawn publication alive.
func (s *GatewayService) LatestManagedModelGroup(ctx context.Context, groupID int64) (*Group, error) {
	if s == nil || s.groupRepo == nil {
		return nil, ErrManagedModelRouteUnavailable
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil || group == nil {
		return nil, ErrManagedModelRouteUnavailable
	}
	return group, nil
}

func (s *GatewayService) ValidateManagedModelAccountLatest(ctx context.Context, account *Account, routingModel string) error {
	if s == nil {
		return ErrManagedModelRouteUnavailable
	}
	return validateManagedForwardAccount(ctx, s.accountRepo, account, routingModel)
}

func (s *OpenAIGatewayService) ValidateManagedModelAccountLatest(ctx context.Context, account *Account, routingModel string) error {
	if s == nil {
		return ErrManagedModelRouteUnavailable
	}
	return validateManagedForwardAccount(ctx, s.accountRepo, account, routingModel)
}

// ValidateManagedModelCompilation checks the already compiled configuration;
// it does not repair or replace it in the request path. Unmanaged groups keep
// all existing channel and composite behavior.
func (s *GatewayService) ValidateManagedModelCompilation(ctx context.Context, group *Group, request *ManagedModelRequest) error {
	if request == nil {
		return nil
	}
	if s == nil || group == nil || !group.IsActive() || !group.ManagedModelRoutes.Enabled || group.ID != request.GroupID || s.channelService == nil {
		return ErrManagedModelRouteUnavailable
	}
	route := request.Route
	if request.Version == ManagedModelRoutesVersion {
		// V2 is already an explicit route graph. Composite's single-target
		// dispatcher must not decide which verified branch survives. Channels
		// remain the source of price/restriction policy, never a second router.
		for _, branch := range ManagedModelRouteBranches(route) {
			if !managedModelBranchValid(group, route, branch) {
				return ErrManagedModelRouteUnavailable
			}
			lookupCtx := WithManagedModelBranch(WithManagedModelRequest(ctx, request), branch)
			mapping := s.channelService.ResolveChannelMapping(lookupCtx, group.ID, branch.Selector)
			if mapping.ChannelID <= 0 || mapping.BillingModelSource != BillingModelSourceRequested || mapping.MappedModel != branch.Selector {
				return ErrManagedModelRouteUnavailable
			}
		}
		return nil
	}
	lookupModel := route.PublicModel
	if group.Platform == PlatformComposite {
		if s.compositeResolver == nil {
			return ErrManagedModelRouteUnavailable
		}
		endpoint := request.Endpoint
		if endpoint == ManagedModelEndpointResponsesWebSocket {
			endpoint = CompositeRouteEndpointResponses
		}
		decision, err := s.compositeResolver.Resolve(ctx, group.ID, route.PublicModel, endpoint)
		if err != nil || !decision.Matched || decision.Source != CompositeRouteSourceExplicit || decision.TargetPlatform != route.TargetPlatform || decision.UpstreamModel != route.Selector {
			return ErrManagedModelRouteUnavailable
		}
		lookupModel = route.Selector
	}
	lookupCtx := WithResolvedTargetPlatform(ctx, route.TargetPlatform)
	mapping := s.channelService.ResolveChannelMapping(lookupCtx, group.ID, lookupModel)
	if mapping.ChannelID <= 0 || mapping.BillingModelSource != BillingModelSourceRequested || mapping.MappedModel != route.Selector {
		return ErrManagedModelRouteUnavailable
	}
	if group.Platform != PlatformComposite && !mapping.Mapped {
		return ErrManagedModelRouteUnavailable
	}
	if route.TargetPlatform == PlatformOpenAI && (request.Endpoint == CompositeRouteEndpointMessages || request.Endpoint == CompositeRouteEndpointCountTokens) {
		if !group.AllowMessagesDispatch {
			return ErrManagedModelRouteUnavailable
		}
		if group.Platform != PlatformComposite && group.ResolveMessagesDispatchModel(route.PublicModel) != route.Selector {
			return ErrManagedModelRouteUnavailable
		}
	}
	return nil
}

// ManagedModelAccountFingerprint is the one shared probe/publication/runtime
// identity definition. Mapping keys, group bindings and scheduler counters are
// intentionally excluded: the route separately pins its exact selector target.
// Only a digest is returned; original authentication bytes are never exposed.
func ManagedModelAccountFingerprint(account *Account) string {
	if account == nil || account.SchedulerMetadata != nil {
		return ""
	}
	credentials := make(map[string]any, len(account.Credentials))
	for key, value := range account.Credentials {
		if key != "model_mapping" && key != "compact_model_mapping" {
			credentials[key] = value
		}
	}
	extra := make(map[string]any)
	for _, key := range []string{
		"openai_responses_mode", "openai_responses_supported", "openai_passthrough", "openai_oauth_passthrough",
		"anthropic_passthrough", "anthropic_apikey_auth_scheme", "openai_compact_mode", "openai_responses_flatten_namespaces",
		"openai_oauth_responses_websockets_v2_enabled", "openai_apikey_responses_websockets_v2_enabled",
		"openai_oauth_responses_websockets_v2_mode", "openai_apikey_responses_websockets_v2_mode", "openai_ws_force_http",
		"responses_websockets_v2_enabled", "openai_ws_enabled", "openai_ws_ingress_mode", "openai_ws_allow_store_recovery",
		"enable_tls_fingerprint", "tls_fingerprint_profile_id",
	} {
		if value, ok := account.Extra[key]; ok {
			extra[key] = value
		}
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return ""
		}
		proxyURL = account.Proxy.URL()
	}
	value := struct {
		Version     int            `json:"version"`
		Platform    string         `json:"platform"`
		Wire        string         `json:"wire"`
		Profile     string         `json:"profile"`
		Type        string         `json:"type"`
		ProxyID     *int64         `json:"proxy_id"`
		ProxyURL    string         `json:"proxy_url"`
		Credentials map[string]any `json:"credentials"`
		Extra       map[string]any `json:"extra"`
	}{1, account.Platform, account.EffectiveWirePlatform(), account.EffectiveProviderProfile(), account.Type, account.ProxyID, proxyURL, credentials, extra}
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("sub2api-managed-capability-v1\x00"), body...))
	return hex.EncodeToString(sum[:])
}

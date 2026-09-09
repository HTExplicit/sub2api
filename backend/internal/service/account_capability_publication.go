package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrCapabilityPublicationInvalid  = infraerrors.BadRequest("CAPABILITY_PUBLICATION_INVALID", "publication evidence or configuration is invalid")
	ErrCapabilityPublicationConflict = infraerrors.Conflict("CAPABILITY_PUBLICATION_CONFLICT", "configuration changed; generate a new preview")
	ErrCapabilityChangeSetNotFound   = infraerrors.NotFound("CAPABILITY_CHANGESET_NOT_FOUND", "capability change set not found")
)

type CapabilityPublicationScope struct {
	FolderIDs  []int64 `json:"folder_ids"`
	AccountIDs []int64 `json:"account_ids"`
}

type CapabilityPublicationModel struct {
	PublicModel string   `json:"public_model"`
	Aliases     []string `json:"aliases,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	EvidenceIDs []int64  `json:"evidence_ids"`
}

type CapabilityPublicationGroup struct {
	ID             int64                              `json:"id,omitempty"`
	Name           string                             `json:"name"`
	Platform       string                             `json:"platform"`
	RateMultiplier float64                            `json:"rate_multiplier"`
	Models         []CapabilityPublicationModel       `json:"models"`
	RemoveModels   []string                           `json:"remove_models,omitempty"`
	RemoveLines    []CapabilityPublicationLineRemoval `json:"remove_lines,omitempty"`
}

// Deletions are an explicit, exact instruction. Omitting a model or evidence
// from a merge request never removes its existing publication.
type CapabilityPublicationLineRemoval struct {
	PublicModel   string `json:"public_model"`
	AccountID     int64  `json:"account_id"`
	UpstreamModel string `json:"upstream_model"`
	Protocol      string `json:"protocol"`
}

type CapabilityPublicationRequest struct {
	IdempotencyKey        string                       `json:"idempotency_key,omitempty"`
	Operation             string                       `json:"operation,omitempty"`
	Scope                 CapabilityPublicationScope   `json:"scope"`
	Groups                []CapabilityPublicationGroup `json:"groups"`
	DetachAccountIDs      []int64                      `json:"detach_account_ids,omitempty"`
	SchedulingEvidenceIDs []int64                      `json:"scheduling_evidence_ids,omitempty"`
}

// These snapshots are server-owned and never serialized into an API response or
// audit record. In particular, Account contains credentials only while validating.
type CapabilityPublicationAccountSnapshot struct {
	Account  *Account
	Bindings map[int64]int
}

type CapabilityPublicationGroupSnapshot struct {
	Group    *Group
	IsNew    bool
	Channel  *Channel
	Routes   []CompositeModelRoute
	Bindings map[int64]int
}

type CapabilityPublicationEvidence struct {
	ID                int64
	AccountID         int64
	FolderID          *int64
	ConfigFingerprint string
	UpstreamModel     string
	Protocol          string
	Profile           string
	Status            string
	RunKind           string
	RunFolderIDs      []int64
	RunAccountIDs     []int64
	Result            json.RawMessage
	FinishedAt        *time.Time
	Superseded        bool
}

type CapabilityPublicationSnapshot struct {
	Request  CapabilityPublicationRequest
	Accounts map[int64]*CapabilityPublicationAccountSnapshot
	Groups   map[int64]*CapabilityPublicationGroupSnapshot
	Evidence map[int64]CapabilityPublicationEvidence
	// Fingerprint covers all modified fields and their dependencies, not secrets.
	Fingerprint string
}

type CapabilityPublicationChange struct {
	Kind        string  `json:"kind"`
	AccountID   int64   `json:"account_id,omitempty"`
	GroupID     int64   `json:"group_id,omitempty"`
	Label       string  `json:"label"`
	Before      any     `json:"before"`
	After       any     `json:"after"`
	EvidenceIDs []int64 `json:"evidence_ids"`
}

type CapabilityPublicationAccountPatch struct {
	AccountID int64 `json:"account_id"`
	// Only these exact selector keys may be updated; credentials are never replaced.
	ModelMapping    map[string]string `json:"model_mapping"`
	RemoveSelectors []string          `json:"remove_selectors,omitempty"`
	AddGroupIDs     []int64           `json:"add_group_ids"`
	RemoveGroupIDs  []int64           `json:"remove_group_ids"`
	Schedulable     *bool             `json:"schedulable,omitempty"`
}

type CapabilityPublicationGroupPatch struct {
	GroupID               int64                             `json:"group_id"`
	Create                bool                              `json:"create"`
	Name                  string                            `json:"name"`
	Platform              string                            `json:"platform"`
	RateMultiplier        float64                           `json:"rate_multiplier"`
	Status                string                            `json:"status"`
	ManagedModelRoutes    domain.ManagedModelRoutesConfig   `json:"managed_model_routes"`
	ModelAllowlist        GroupModelAllowlist               `json:"model_allowlist"`
	MessagesDispatch      OpenAIMessagesDispatchModelConfig `json:"messages_dispatch_model_config"`
	AllowMessagesDispatch bool                              `json:"allow_messages_dispatch"`
	DefaultMappedModel    string                            `json:"default_mapped_model"`
	ChannelMapping        map[string]map[string]string      `json:"channel_mapping"`
	// Existing prices are copied without inventing prices or modifying the old channel.
	ChannelPricing                    []ChannelModelPricing     `json:"channel_pricing"`
	ChannelFeatures                   string                    `json:"channel_features"`
	ChannelFeaturesConfig             map[string]any            `json:"channel_features_config"`
	ChannelApplyPricingToAccountStats bool                      `json:"channel_apply_pricing_to_account_stats"`
	ChannelAccountStatsPricingRules   []AccountStatsPricingRule `json:"channel_account_stats_pricing_rules"`
	CompositeRoutes                   []CompositeModelRoute     `json:"composite_routes"`
}

type CapabilityPublicationPlan struct {
	Groups   []CapabilityPublicationGroupPatch   `json:"groups"`
	Accounts []CapabilityPublicationAccountPatch `json:"accounts"`
	Changes  []CapabilityPublicationChange       `json:"changes"`
	Warnings []string                            `json:"warnings"`
}

type CapabilityChangeSet struct {
	ID                int64                         `json:"id"`
	Scope             CapabilityPublicationScope    `json:"scope"`
	Status            string                        `json:"status"`
	CreatedAt         time.Time                     `json:"created_at"`
	AppliedAt         *time.Time                    `json:"applied_at,omitempty"`
	Changes           []CapabilityPublicationChange `json:"changes"`
	Warnings          []string                      `json:"warnings"`
	Request           CapabilityPublicationRequest  `json:"-"`
	Plan              CapabilityPublicationPlan     `json:"-"`
	BeforeFingerprint string                        `json:"-"`
}

type CapabilityPublicationBuilder func(*CapabilityPublicationSnapshot) (*CapabilityPublicationPlan, error)

type AccountCapabilityPublicationRepository interface {
	Preview(context.Context, CapabilityPublicationRequest, CapabilityPublicationBuilder) (*CapabilityChangeSet, error)
	Get(context.Context, int64) (*CapabilityChangeSet, error)
	Apply(context.Context, int64, CapabilityPublicationBuilder) (*CapabilityChangeSet, error)
}

type AccountCapabilityPublicationService struct {
	repo      AccountCapabilityPublicationRepository
	billing   *BillingService
	auth      APIKeyAuthCacheInvalidator
	channels  *ChannelService
	gateway   *GatewayService
	scheduler *SchedulerSnapshotService
}

func NewAccountCapabilityPublicationService(repo AccountCapabilityPublicationRepository, billing *BillingService, auth APIKeyAuthCacheInvalidator, channels *ChannelService, gateway *GatewayService, scheduler *SchedulerSnapshotService) *AccountCapabilityPublicationService {
	return &AccountCapabilityPublicationService{repo: repo, billing: billing, auth: auth, channels: channels, gateway: gateway, scheduler: scheduler}
}

func (s *AccountCapabilityPublicationService) Preview(ctx context.Context, req CapabilityPublicationRequest) (*CapabilityChangeSet, error) {
	if err := validateCapabilityPublicationRequest(req); err != nil {
		return nil, err
	}
	if req.IdempotencyKey == "" {
		b, _ := json.Marshal(req)
		h := sha256.Sum256(b)
		req.IdempotencyKey = hex.EncodeToString(h[:])
	}
	return s.repo.Preview(ctx, req, s.build)
}

func (s *AccountCapabilityPublicationService) Get(ctx context.Context, id int64) (*CapabilityChangeSet, error) {
	return s.repo.Get(ctx, id)
}

func (s *AccountCapabilityPublicationService) Apply(ctx context.Context, id int64) (*CapabilityChangeSet, error) {
	set, err := s.repo.Apply(ctx, id, s.build)
	if err != nil {
		return nil, err
	}
	// Durable auth/scheduler outbox entries were committed with the mutation.
	// Synchronous invalidation also makes the first request after Apply observe it.
	if s.channels != nil {
		s.channels.InvalidateCache()
	}
	for _, gp := range set.Plan.Groups {
		if s.auth != nil {
			s.auth.InvalidateAuthCacheByGroupID(ctx, gp.GroupID)
		}
		if s.gateway != nil {
			id := gp.GroupID
			s.gateway.InvalidateAvailableModelsCache(&id, "")
		}
	}
	if s.scheduler != nil {
		for _, ap := range set.Plan.Accounts {
			ids := append(append([]int64{}, ap.AddGroupIDs...), ap.RemoveGroupIDs...)
			_ = s.scheduler.RefreshAccountAndGroups(ctx, ap.AccountID, ids)
		}
	}
	return set, nil
}

func validateCapabilityPublicationRequest(req CapabilityPublicationRequest) error {
	if req.Operation != "" && req.Operation != "merge" {
		return publicationInvalid("only explicit merge operations are supported; removals must name models or lines")
	}
	if (len(req.Groups) == 0 && len(req.SchedulingEvidenceIDs) == 0) || len(req.Groups) > 50 || len(req.Scope.AccountIDs) == 0 || len(req.Scope.AccountIDs) > 500 || len(req.Scope.FolderIDs) == 0 || len(req.IdempotencyKey) > 128 {
		return ErrCapabilityPublicationInvalid
	}
	if !publicationPositiveUnique(req.Scope.AccountIDs) || !publicationPositiveUnique(req.Scope.FolderIDs) || !publicationPositiveUnique(req.DetachAccountIDs) || !publicationPositiveUnique(req.SchedulingEvidenceIDs) {
		return ErrCapabilityPublicationInvalid
	}
	seenGroups := map[int64]bool{}
	seenNames := map[string]bool{}
	for _, gp := range req.Groups {
		if gp.ID < 0 || gp.Name == "" || len(gp.Name) > 100 || (gp.Platform != PlatformOpenAI && gp.Platform != PlatformComposite) || gp.RateMultiplier < 0 || len(gp.Models) > 300 {
			return ErrCapabilityPublicationInvalid
		}
		if (gp.ID != 0 && seenGroups[gp.ID]) || seenNames[gp.Name] {
			return ErrCapabilityPublicationInvalid
		}
		seenGroups[gp.ID], seenNames[gp.Name] = true, true
		if gp.ID == 0 && ((gp.Name != "Qwen" && gp.Name != "MiniMax") || gp.RateMultiplier != 0.3 || gp.Platform != PlatformOpenAI) {
			return ErrCapabilityPublicationInvalid
		}
		seenModels := map[string]bool{}
		for _, model := range gp.Models {
			if !publicationValidModel(model.PublicModel) || len(model.EvidenceIDs) == 0 || len(model.EvidenceIDs) > 1000 || !publicationPositiveUnique(model.EvidenceIDs) || len(model.Aliases) > 30 || (model.Tier != "" && model.Tier != "standard" && model.Tier != "vip") {
				return ErrCapabilityPublicationInvalid
			}
			for _, name := range append([]string{model.PublicModel}, model.Aliases...) {
				key := strings.ToLower(strings.TrimSpace(name))
				if !publicationValidModel(name) || seenModels[key] {
					return ErrCapabilityPublicationInvalid
				}
				seenModels[key] = true
			}
		}
		removedModels := map[string]bool{}
		for _, model := range gp.RemoveModels {
			key := strings.ToLower(model)
			if !publicationValidModel(model) || removedModels[key] || seenModels[key] {
				return ErrCapabilityPublicationInvalid
			}
			removedModels[key] = true
		}
		removedLines := map[string]bool{}
		for _, line := range gp.RemoveLines {
			key := fmt.Sprintf("%s|%d|%s|%s", strings.ToLower(line.PublicModel), line.AccountID, line.UpstreamModel, line.Protocol)
			if !publicationValidModel(line.PublicModel) || !publicationValidModel(line.UpstreamModel) || line.AccountID <= 0 || removedLines[key] || removedModels[strings.ToLower(line.PublicModel)] ||
				(line.Protocol != "" && line.Protocol != "responses" && line.Protocol != "chat_completions" && line.Protocol != "messages") {
				return ErrCapabilityPublicationInvalid
			}
			removedLines[key] = true
		}
	}
	return nil
}

func publicationValidModel(model string) bool {
	return model != "" && model == strings.TrimSpace(model) && len(model) <= 200 && !strings.ContainsAny(model, "*?\r\n\t") && !strings.HasPrefix(strings.ToLower(model), "s2pub-")
}

func publicationPositiveUnique(ids []int64) bool {
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func publicationHasID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func CapabilityPublicationSelector(groupID int64, publicModel string) string {
	return ManagedModelSelector(groupID, publicModel)
}

type publicationProbeResult struct {
	Status         string `json:"status"`
	Classification string `json:"classification"`
	AccountFailure bool   `json:"account_failure"`
	Protocol       string `json:"protocol"`
	Profile        string `json:"profile"`
	UpstreamModel  string `json:"upstream_model"`
	RequestCount   int    `json:"request_count"`
}

func publicationValidateEvidenceScope(snap *CapabilityPublicationSnapshot, id int64) (CapabilityPublicationEvidence, error) {
	e, ok := snap.Evidence[id]
	as := snap.Accounts[e.AccountID]
	if !ok || as == nil || as.Account == nil || e.Superseded || e.FinishedAt == nil || !publicationHasID(snap.Request.Scope.AccountIDs, e.AccountID) {
		return e, ErrCapabilityPublicationInvalid
	}
	a := as.Account
	if a.ManagementFolderID == nil || e.FolderID == nil || *a.ManagementFolderID != *e.FolderID || !publicationHasID(snap.Request.Scope.FolderIDs, *a.ManagementFolderID) || !publicationHasID(e.RunFolderIDs, *a.ManagementFolderID) || !publicationHasID(e.RunAccountIDs, a.ID) {
		return e, ErrCapabilityPublicationConflict
	}
	if a.Platform == PlatformCindy || a.ProviderProfile == "cindy" || a.ParentAccountID != nil || e.ConfigFingerprint != ManagedModelAccountFingerprint(a) {
		return e, ErrCapabilityPublicationConflict
	}
	return e, nil
}

func publicationValidateEvidence(snap *CapabilityPublicationSnapshot, id int64) (CapabilityPublicationEvidence, publicationProbeResult, error) {
	e, err := publicationValidateEvidenceScope(snap, id)
	if err != nil {
		return e, publicationProbeResult{}, err
	}
	if e.RunKind != "probe" {
		return e, publicationProbeResult{}, ErrCapabilityPublicationInvalid
	}
	var result publicationProbeResult
	if json.Unmarshal(e.Result, &result) != nil || result.Protocol != e.Protocol || result.UpstreamModel != e.UpstreamModel || result.RequestCount < 1 || result.Profile != e.Profile {
		return e, result, ErrCapabilityPublicationInvalid
	}
	return e, result, nil
}

// Discovery can prove a credential was explicitly rejected even when there are
// no mainline model candidates worth spending an inference call on. That narrow
// account-level fact is never promoted into model, protocol or liveness proof.
func publicationValidateSchedulingEvidence(snap *CapabilityPublicationSnapshot, id int64) (CapabilityPublicationEvidence, publicationProbeResult, error) {
	e, err := publicationValidateEvidenceScope(snap, id)
	if err != nil {
		return e, publicationProbeResult{}, err
	}
	if e.RunKind == "probe" {
		return publicationValidateEvidence(snap, id)
	}
	if e.RunKind != "discover" || e.Status != "failed" {
		return e, publicationProbeResult{}, ErrCapabilityPublicationInvalid
	}
	var result AccountCapabilityDiscoveryResult
	if json.Unmarshal(e.Result, &result) != nil || result.Status != "failed" || result.Source != "upstream" || result.RequestCount < 1 || !result.AccountFailure ||
		(result.Classification != "credential_invalid" && result.Classification != "account_disabled") || (result.HTTPStatus != 401 && result.HTTPStatus != 403) {
		return e, publicationProbeResult{}, ErrCapabilityPublicationInvalid
	}
	return e, publicationProbeResult{Status: result.Status, Classification: result.Classification, AccountFailure: true, RequestCount: result.RequestCount}, nil
}

func (s *AccountCapabilityPublicationService) hasPrice(group *CapabilityPublicationGroupSnapshot, model string) bool {
	if group == nil {
		return CapabilityHasIdentifiedPricing(s.billing, nil, nil, model)
	}
	return CapabilityHasIdentifiedPricing(s.billing, group.Group, group.Channel, model)
}

// CapabilityHasIdentifiedPricing is the shared read-only pricing admission for
// inventory and publication. Existing exact group/channel prices retain their
// precedence; the billing service recognizes both exact dynamic token prices
// and exact built-in fallback prices, without inventing a family/substr price.
func CapabilityHasIdentifiedPricing(billing *BillingService, group *Group, channel *Channel, model string) bool {
	if group != nil {
		for _, p := range group.ModelPricing {
			for _, m := range p.Models {
				if strings.EqualFold(m, model) && publicationHasTokenPrice(p) {
					return true
				}
			}
		}
	}
	if channel != nil {
		if p := channel.GetModelPricing(model); p != nil && publicationHasTokenPrice(*p) {
			return true
		}
	}
	return billing.HasIdentifiedTokenPricing(model)
}

func publicationHasTokenPrice(p ChannelModelPricing) bool {
	return (p.InputPrice != nil && p.OutputPrice != nil) || (p.BillingMode == BillingModePerRequest && p.PerRequestPrice != nil)
}

func (s *AccountCapabilityPublicationService) build(snap *CapabilityPublicationSnapshot) (*CapabilityPublicationPlan, error) {
	// Scope protects removals too: an account without selected probe evidence can
	// still lose old bindings/selectors and must remain in the approved folders.
	for _, id := range snap.Request.Scope.AccountIDs {
		as := snap.Accounts[id]
		if as == nil || as.Account == nil || as.Account.ManagementFolderID == nil || !publicationHasID(snap.Request.Scope.FolderIDs, *as.Account.ManagementFolderID) {
			return nil, ErrCapabilityPublicationConflict
		}
	}
	plan := &CapabilityPublicationPlan{Groups: []CapabilityPublicationGroupPatch{}, Accounts: []CapabilityPublicationAccountPatch{}, Changes: []CapabilityPublicationChange{}, Warnings: []string{}}
	patches := map[int64]*CapabilityPublicationAccountPatch{}
	evidenceByAccount := map[int64][]int64{}
	getPatch := func(id int64) *CapabilityPublicationAccountPatch {
		if patches[id] == nil {
			patches[id] = &CapabilityPublicationAccountPatch{AccountID: id, ModelMapping: map[string]string{}, AddGroupIDs: []int64{}, RemoveGroupIDs: []int64{}}
		}
		return patches[id]
	}
	for _, input := range snap.Request.Groups {
		if err := s.mergePublicationGroup(snap, input, plan, getPatch, evidenceByAccount); err != nil {
			return nil, err
		}
	}
	// Positive route evidence enables scheduling. Account-global disable requires
	// a server-classified terminal credential failure, not merely a failed model.
	for accountID, ids := range evidenceByAccount {
		as := snap.Accounts[accountID]
		if as.Account.Status != StatusActive {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("account %d is not active; existing status is preserved", accountID))
		}
		if !as.Account.Schedulable {
			v := true
			getPatch(accountID).Schedulable = &v
		}
		evidenceByAccount[accountID] = publicationUniqueIDs(ids)
	}
	for _, eid := range snap.Request.SchedulingEvidenceIDs {
		e, result, err := publicationValidateSchedulingEvidence(snap, eid)
		if err != nil {
			return nil, err
		}
		if result.AccountFailure && (result.Classification == "credential_invalid" || result.Classification == "account_disabled") && e.Status == "failed" {
			if len(evidenceByAccount[e.AccountID]) > 0 {
				return nil, publicationInvalid("conflicting live and account-terminal evidence; explicitly recheck this account")
			}
			if snap.Accounts[e.AccountID].Account.Schedulable {
				v := false
				getPatch(e.AccountID).Schedulable = &v
			}
			evidenceByAccount[e.AccountID] = append(evidenceByAccount[e.AccountID], eid)
		} else {
			return nil, ErrCapabilityPublicationInvalid
		}
	}
	accountIDs := make([]int64, 0, len(patches))
	for id := range patches {
		accountIDs = append(accountIDs, id)
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	for _, id := range accountIDs {
		ap, as := patches[id], snap.Accounts[id]
		if as == nil {
			return nil, ErrCapabilityPublicationConflict
		}
		ap.AddGroupIDs, ap.RemoveGroupIDs, ap.RemoveSelectors = publicationUniqueIDs(ap.AddGroupIDs), publicationUniqueIDs(ap.RemoveGroupIDs), publicationUniqueStrings(ap.RemoveSelectors)
		beforeMappings := map[string]string{}
		mapping := as.Account.GetModelMapping()
		for selector, target := range ap.ModelMapping {
			if old, exists := mapping[selector]; exists {
				beforeMappings[selector] = old
				if old != target && !publicationOwnedSelector(snap, id, selector) {
					return nil, ErrCapabilityPublicationConflict
				}
			}
		}
		for _, selector := range ap.RemoveSelectors {
			if old, exists := mapping[selector]; exists {
				beforeMappings[selector] = old
			}
		}
		eids := publicationUniqueIDs(evidenceByAccount[id])
		if len(ap.ModelMapping) > 0 || len(ap.RemoveSelectors) > 0 {
			plan.Changes = append(plan.Changes, CapabilityPublicationChange{Kind: "mappings", AccountID: id, Label: as.Account.Name, Before: beforeMappings, After: map[string]any{"set": ap.ModelMapping, "remove": ap.RemoveSelectors}, EvidenceIDs: eids})
		}
		if len(ap.AddGroupIDs) > 0 || len(ap.RemoveGroupIDs) > 0 {
			plan.Changes = append(plan.Changes, CapabilityPublicationChange{Kind: "bindings", AccountID: id, Label: as.Account.Name, Before: publicationBindingView(as.Bindings), After: map[string]any{"add_group_ids": ap.AddGroupIDs, "remove_group_ids": ap.RemoveGroupIDs, "preserve_existing_priorities": true}, EvidenceIDs: eids})
		}
		if ap.Schedulable != nil {
			plan.Changes = append(plan.Changes, CapabilityPublicationChange{Kind: "scheduling", AccountID: id, Label: as.Account.Name, Before: as.Account.Schedulable, After: *ap.Schedulable, EvidenceIDs: eids})
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("account %d scheduling is account-global and also affects its private groups; quota, cooldown, expiration and status are unchanged", id))
		}
		plan.Accounts = append(plan.Accounts, *ap)
	}
	return plan, nil
}

func publicationOwnedSelector(snap *CapabilityPublicationSnapshot, accountID int64, selector string) bool {
	for _, gs := range snap.Groups {
		for _, rt := range gs.Group.ManagedModelRoutes.Routes {
			for _, branch := range ManagedModelRouteBranches(rt) {
				if branch.Selector == selector {
					for _, member := range branch.Accounts {
						if member.AccountID == accountID {
							return true
						}
					}
				}
			}
		}
	}
	return false
}
func publicationInvalid(message string) error {
	return infraerrors.BadRequest("CAPABILITY_PUBLICATION_INVALID", message)
}
func publicationHasString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func publicationSortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
func publicationUniqueIDs(ids []int64) []int64 {
	set := map[int64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func publicationUniqueStrings(values []string) []string {
	set := map[string]bool{}
	for _, v := range values {
		set[v] = true
	}
	return publicationSortedKeys(set)
}
func publicationChannelView(ch *Channel) any {
	if ch == nil {
		return nil
	}
	return map[string]any{"id": ch.ID, "name": ch.Name, "billing_model_source": ch.BillingModelSource, "restrict_models": ch.RestrictModels, "model_mapping": ch.ModelMapping, "model_pricing": ch.ModelPricing, "features": ch.Features, "features_config": ch.FeaturesConfig, "apply_pricing_to_account_stats": ch.ApplyPricingToAccountStats, "account_stats_pricing_rules": ch.AccountStatsPricingRules}
}
func publicationBindingView(bindings map[int64]int) []map[string]any {
	ids := make([]int64, 0, len(bindings))
	for id := range bindings {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]any{"group_id": id, "priority": bindings[id]})
	}
	return out
}

// The repository uses this in addition to its locked snapshot CAS; a changed
// pricing registry or evidence policy cannot silently alter an approved plan.
func CapabilityPublicationPlansEqual(a, b *CapabilityPublicationPlan) bool {
	if a == nil || b == nil {
		return a == b
	}
	aa, errA := json.Marshal(struct {
		Groups   []CapabilityPublicationGroupPatch
		Accounts []CapabilityPublicationAccountPatch
	}{a.Groups, a.Accounts})
	bb, errB := json.Marshal(struct {
		Groups   []CapabilityPublicationGroupPatch
		Accounts []CapabilityPublicationAccountPatch
	}{b.Groups, b.Accounts})
	return errA == nil && errB == nil && string(aa) == string(bb)
}

func publicationPreservePrivateMappings(a *Account) error {
	if a.IsOpenAIPassthroughEnabled() || a.IsAnthropicAPIKeyPassthroughEnabled() {
		return publicationInvalid("unsupported_preserve_private_semantics: automatic passthrough account")
	}
	privateCount := 0
	for source, target := range a.GetModelMapping() {
		if strings.HasPrefix(source, "s2pub-") {
			continue
		}
		if strings.ContainsAny(source, "*?") || strings.ContainsAny(target, "*?") {
			return publicationInvalid("unsupported_preserve_private_semantics: wildcard model mapping")
		}
		privateCount++
	}
	if privateCount == 0 {
		return publicationInvalid("unsupported_preserve_private_semantics: empty private model mapping")
	}
	return nil
}

// CapabilityIngressEndpoints compiles successful *wire* evidence into client
// entry points whose managed-v2 adapter deterministically pins that wire. This
// per-request choice never changes stored account routing flags or enables WS.
func CapabilityIngressEndpoints(a *Account, protocol string) []string {
	if a == nil || a.Type != AccountTypeAPIKey {
		return nil
	}
	if protocol == "responses_websocket" {
		if a.Platform == PlatformOpenAI && !shouldForwardOpenAIResponsesViaRawChatCompletions(a) {
			return []string{"responses_websocket"}
		}
		return nil
	}
	if protocol == "responses_input_tokens" {
		if a.Platform == PlatformOpenAI {
			return []string{"count_tokens"}
		}
		return nil
	}
	if protocol == "messages_count_tokens" {
		if a.Platform == PlatformAnthropic {
			return []string{"count_tokens"}
		}
		return nil
	}
	if a.Platform == PlatformDeepseek {
		if protocol == "responses" || protocol == "chat_completions" || protocol == "messages" {
			return []string{"chat_completions", "messages", "responses"}
		}
		return nil
	}
	if a.Platform == PlatformAnthropic {
		if protocol == "messages" {
			return []string{"chat_completions", "messages", "responses"}
		}
		return nil
	}
	if a.Platform != PlatformOpenAI {
		return nil
	}
	if protocol == "responses" || protocol == "chat_completions" {
		// Both adapters already exist. The selected managed branch freezes the
		// successful protocol even when the account's ordinary default differs.
		return []string{"chat_completions", "messages", "responses"}
	}
	return nil
}

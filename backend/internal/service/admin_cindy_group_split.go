package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	CindyGroupClassificationPureCindy = "pure_cindy"
	CindyGroupClassificationMixed     = "mixed"
	CindyGroupClassificationNoCindy   = "no_cindy"

	CindyGroupSourceKeepsCindy    = "cindy"
	CindyGroupSourceKeepsOrdinary = "ordinary"
)

var (
	ErrCindyGroupAdminUnavailable = infraerrors.ServiceUnavailable("CINDY_GROUP_ADMIN_UNAVAILABLE", "Cindy group administration is unavailable")
	ErrCindyGroupInvalidInput     = infraerrors.BadRequest("CINDY_GROUP_SPLIT_INVALID_INPUT", "invalid Cindy group split request")
	ErrCindyGroupNotOpenAI        = infraerrors.BadRequest("CINDY_GROUP_NOT_OPENAI", "Cindy group split requires an OpenAI group")
	ErrCindyGroupNotMixed         = infraerrors.Conflict("CINDY_GROUP_NOT_MIXED", "group membership is not mixed")
	ErrCindyGroupSplitDrift       = infraerrors.Conflict("CINDY_GROUP_SPLIT_DRIFT", "group membership changed after preview")
	ErrCindyGroupAPIKeySelection  = infraerrors.BadRequest("CINDY_GROUP_API_KEY_SELECTION_INVALID", "selected API keys must be explicitly bound to the source group")
)

// CindyGroupAuditEntry exposes group-level counts without account identities.
type CindyGroupAuditEntry struct {
	GroupID              int64  `json:"group_id"`
	GroupName            string `json:"group_name"`
	Status               string `json:"status"`
	Classification       string `json:"classification"`
	CindyAccountCount    int64  `json:"cindy_account_count"`
	OrdinaryAccountCount int64  `json:"ordinary_account_count"`
	APIKeyCount          int64  `json:"api_key_count"`
}

// CindyGroupAuditSummary aggregates the three structural classifications.
type CindyGroupAuditSummary struct {
	PureCindyGroups int64 `json:"pure_cindy_groups"`
	MixedGroups     int64 `json:"mixed_groups"`
	NoCindyGroups   int64 `json:"no_cindy_groups"`
}

// CindyGroupAuditResult is the admin audit response.
type CindyGroupAuditResult struct {
	Summary CindyGroupAuditSummary `json:"summary"`
	Groups  []CindyGroupAuditEntry `json:"groups"`
}

// CindyGroupSplitInput is shared by preview and commit. MemberFingerprint is
// required only for commit; APIKeyIDs is intentionally empty by default.
type CindyGroupSplitInput struct {
	SourceKeeps       string  `json:"source_keeps"`
	TargetName        string  `json:"target_name"`
	APIKeyIDs         []int64 `json:"api_key_ids"`
	MemberFingerprint string  `json:"member_fingerprint,omitempty"`
}

// CindyGroupSplitPreview contains only anonymous impact counts.
type CindyGroupSplitPreview struct {
	SourceGroupID        int64  `json:"source_group_id"`
	SourceGroupName      string `json:"source_group_name"`
	SourceKeeps          string `json:"source_keeps"`
	TargetName           string `json:"target_name"`
	TargetClassification string `json:"target_classification"`
	MemberFingerprint    string `json:"member_fingerprint"`
	CindyAccountCount    int64  `json:"cindy_account_count"`
	OrdinaryAccountCount int64  `json:"ordinary_account_count"`
	AccountsToMove       int64  `json:"accounts_to_move"`
	SourceAPIKeyCount    int64  `json:"source_api_key_count"`
	APIKeysToRebind      int64  `json:"api_keys_to_rebind"`
	APIKeysRemaining     int64  `json:"api_keys_remaining"`
}

// CindyGroupSplitResult describes an atomically committed split.
type CindyGroupSplitResult struct {
	CindyGroupSplitPreview
	TargetGroupID int64 `json:"target_group_id"`
}

// CindyGroupSplitRepositorySnapshot carries the source configuration only
// between the repository and service. It is never serialized by handlers.
type CindyGroupSplitRepositorySnapshot struct {
	SourceGroup *Group
	Preview     CindyGroupSplitPreview
}

// CindyGroupRepository is the narrow persistence contract for the downstream
// Cindy group administration feature.
type CindyGroupRepository interface {
	AuditCindyGroups(ctx context.Context) ([]CindyGroupAuditEntry, error)
	PreviewCindyGroupSplit(ctx context.Context, groupID int64, input CindyGroupSplitInput) (*CindyGroupSplitRepositorySnapshot, error)
	CommitCindyGroupSplit(ctx context.Context, groupID int64, input CindyGroupSplitInput) (*CindyGroupSplitResult, error)
}

// CindyGroupAdminService is intentionally separate from the broad AdminService
// contract so downstream functionality stays independently replaceable.
type CindyGroupAdminService interface {
	AuditCindyGroups(ctx context.Context) (*CindyGroupAuditResult, error)
	PreviewCindyGroupSplit(ctx context.Context, groupID int64, input CindyGroupSplitInput) (*CindyGroupSplitPreview, error)
	SplitCindyGroup(ctx context.Context, groupID int64, input CindyGroupSplitInput) (*CindyGroupSplitResult, error)
}

func (s *adminServiceImpl) cindyGroupRepository() (CindyGroupRepository, error) {
	if s == nil || s.groupRepo == nil {
		return nil, ErrCindyGroupAdminUnavailable
	}
	repo, ok := s.groupRepo.(CindyGroupRepository)
	if !ok || repo == nil {
		return nil, ErrCindyGroupAdminUnavailable
	}
	return repo, nil
}

// AuditCindyGroups returns structural Cindy membership counts for OpenAI groups.
func (s *adminServiceImpl) AuditCindyGroups(ctx context.Context) (*CindyGroupAuditResult, error) {
	repo, err := s.cindyGroupRepository()
	if err != nil {
		return nil, err
	}
	entries, err := repo.AuditCindyGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("audit Cindy groups: %w", err)
	}
	if entries == nil {
		entries = []CindyGroupAuditEntry{}
	}
	result := &CindyGroupAuditResult{Groups: entries}
	for _, entry := range entries {
		switch entry.Classification {
		case CindyGroupClassificationPureCindy:
			result.Summary.PureCindyGroups++
		case CindyGroupClassificationMixed:
			result.Summary.MixedGroups++
		default:
			result.Summary.NoCindyGroups++
		}
	}
	return result, nil
}

// PreviewCindyGroupSplit validates an explicit split selection and returns its
// anonymous impact plus a compare-and-swap membership fingerprint.
func (s *adminServiceImpl) PreviewCindyGroupSplit(ctx context.Context, groupID int64, input CindyGroupSplitInput) (*CindyGroupSplitPreview, error) {
	if groupID <= 0 {
		return nil, ErrCindyGroupInvalidInput
	}
	normalized, err := normalizeCindyGroupSplitInput(input, false)
	if err != nil {
		return nil, err
	}
	repo, err := s.cindyGroupRepository()
	if err != nil {
		return nil, err
	}
	snapshot, err := repo.PreviewCindyGroupSplit(ctx, groupID, normalized)
	if err != nil {
		return nil, fmt.Errorf("preview Cindy group split: %w", err)
	}
	if snapshot == nil {
		return nil, errors.New("cindy group split preview returned no snapshot")
	}
	return &snapshot.Preview, nil
}

// SplitCindyGroup commits a previously previewed split with repository-level
// fingerprint revalidation and one database transaction.
func (s *adminServiceImpl) SplitCindyGroup(ctx context.Context, groupID int64, input CindyGroupSplitInput) (*CindyGroupSplitResult, error) {
	if groupID <= 0 {
		return nil, ErrCindyGroupInvalidInput
	}
	normalized, err := normalizeCindyGroupSplitInput(input, true)
	if err != nil {
		return nil, err
	}
	repo, err := s.cindyGroupRepository()
	if err != nil {
		return nil, err
	}

	result, err := repo.CommitCindyGroupSplit(ctx, groupID, normalized)
	if err != nil {
		return nil, fmt.Errorf("commit Cindy group split: %w", err)
	}
	if result == nil {
		return nil, errors.New("cindy group split returned no result")
	}
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
		s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, result.TargetGroupID)
	}
	return result, nil
}

func normalizeCindyGroupSplitInput(input CindyGroupSplitInput, requireFingerprint bool) (CindyGroupSplitInput, error) {
	input.SourceKeeps = strings.ToLower(strings.TrimSpace(input.SourceKeeps))
	if input.SourceKeeps != CindyGroupSourceKeepsCindy && input.SourceKeeps != CindyGroupSourceKeepsOrdinary {
		return CindyGroupSplitInput{}, ErrCindyGroupInvalidInput
	}
	input.TargetName = strings.TrimSpace(input.TargetName)
	if input.TargetName == "" || utf8.RuneCountInString(input.TargetName) > maxGroupNameRunes {
		return CindyGroupSplitInput{}, ErrCindyGroupInvalidInput
	}

	seen := make(map[int64]struct{}, len(input.APIKeyIDs))
	normalizedIDs := make([]int64, 0, len(input.APIKeyIDs))
	for _, id := range input.APIKeyIDs {
		if id <= 0 {
			return CindyGroupSplitInput{}, ErrCindyGroupAPIKeySelection
		}
		if _, exists := seen[id]; exists {
			return CindyGroupSplitInput{}, ErrCindyGroupAPIKeySelection
		}
		seen[id] = struct{}{}
		normalizedIDs = append(normalizedIDs, id)
	}
	sort.Slice(normalizedIDs, func(i, j int) bool { return normalizedIDs[i] < normalizedIDs[j] })
	input.APIKeyIDs = normalizedIDs
	input.MemberFingerprint = strings.ToLower(strings.TrimSpace(input.MemberFingerprint))
	if requireFingerprint {
		decoded, err := hex.DecodeString(input.MemberFingerprint)
		if err != nil || len(decoded) != 32 {
			return CindyGroupSplitInput{}, ErrCindyGroupInvalidInput
		}
	}
	return input, nil
}

// BuildCindySplitTargetGroup clones all persisted group policy required by a
// split target while resetting identity and runtime-only fields.
func BuildCindySplitTargetGroup(source *Group, targetName string) *Group {
	target := cloneGroupForDuplicate(source, "")
	target.Name = targetName
	target.Status = source.Status
	target.DuplicateOperationID = ""
	target.LongContextPricingEnabled = source.LongContextPricingEnabled
	target.ProfitControlEnabled = source.ProfitControlEnabled
	target.ProfitMinMargin = source.ProfitMinMargin
	target.ProfitSafetyBuffer = source.ProfitSafetyBuffer
	if source.ModelPricing != nil {
		target.ModelPricing = make([]ChannelModelPricing, len(source.ModelPricing))
		for i := range source.ModelPricing {
			target.ModelPricing[i] = source.ModelPricing[i].Clone()
		}
	}
	return target
}

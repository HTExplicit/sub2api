//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type cindyGroupRepositoryStub struct {
	GroupRepository
	auditEntries []CindyGroupAuditEntry
	preview      *CindyGroupSplitRepositorySnapshot
	commit       *CindyGroupSplitResult
	previewInput CindyGroupSplitInput
	commitInput  CindyGroupSplitInput
	previewCalls int
	commitCalls  int
}

func (s *cindyGroupRepositoryStub) AuditCindyGroups(context.Context) ([]CindyGroupAuditEntry, error) {
	return s.auditEntries, nil
}

func (s *cindyGroupRepositoryStub) PreviewCindyGroupSplit(_ context.Context, _ int64, input CindyGroupSplitInput) (*CindyGroupSplitRepositorySnapshot, error) {
	s.previewCalls++
	s.previewInput = input
	return s.preview, nil
}

func (s *cindyGroupRepositoryStub) CommitCindyGroupSplit(_ context.Context, _ int64, input CindyGroupSplitInput) (*CindyGroupSplitResult, error) {
	s.commitCalls++
	s.commitInput = input
	return s.commit, nil
}

type cindyGroupAuthInvalidatorSpy struct {
	groupIDs []int64
}

func (s *cindyGroupAuthInvalidatorSpy) InvalidateAuthCacheByKey(_ context.Context, _ string) {}

func (s *cindyGroupAuthInvalidatorSpy) InvalidateAuthCacheByUserID(_ context.Context, _ int64) {}

func (s *cindyGroupAuthInvalidatorSpy) InvalidateAuthCacheByGroupID(_ context.Context, groupID int64) {
	s.groupIDs = append(s.groupIDs, groupID)
}

func TestAdminServiceAuditCindyGroupsSummarizesClassifications(t *testing.T) {
	repo := &cindyGroupRepositoryStub{auditEntries: []CindyGroupAuditEntry{
		{GroupID: 1, Classification: CindyGroupClassificationPureCindy},
		{GroupID: 2, Classification: CindyGroupClassificationMixed},
		{GroupID: 3, Classification: CindyGroupClassificationNoCindy},
	}}
	svc := &adminServiceImpl{groupRepo: repo}

	result, err := svc.AuditCindyGroups(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Summary.PureCindyGroups)
	require.Equal(t, int64(1), result.Summary.MixedGroups)
	require.Equal(t, int64(1), result.Summary.NoCindyGroups)
	require.Len(t, result.Groups, 3)
}

func TestAdminServicePreviewCindyGroupSplitNormalizesExplicitSelection(t *testing.T) {
	repo := &cindyGroupRepositoryStub{preview: &CindyGroupSplitRepositorySnapshot{
		SourceGroup: &Group{ID: 7},
		Preview: CindyGroupSplitPreview{
			SourceGroupID:     7,
			MemberFingerprint: "fingerprint",
		},
	}}
	svc := &adminServiceImpl{groupRepo: repo}

	result, err := svc.PreviewCindyGroupSplit(context.Background(), 7, CindyGroupSplitInput{
		SourceKeeps: " CINDY ",
		TargetName:  " Cindy Split ",
		APIKeyIDs:   []int64{9, 3},
	})
	require.NoError(t, err)
	require.Equal(t, "fingerprint", result.MemberFingerprint)
	require.Equal(t, CindyGroupSourceKeepsCindy, repo.previewInput.SourceKeeps)
	require.Equal(t, "Cindy Split", repo.previewInput.TargetName)
	require.Equal(t, []int64{3, 9}, repo.previewInput.APIKeyIDs)
}

func TestAdminServiceSplitCindyGroupCommitsWithoutSecondPreview(t *testing.T) {
	fingerprint := "b55e34d35e40f425b885f8293c7f0c8a9f61f505fa0f2a1258a6c68f3d63998a"
	repo := &cindyGroupRepositoryStub{commit: &CindyGroupSplitResult{
		CindyGroupSplitPreview: CindyGroupSplitPreview{SourceGroupID: 7},
		TargetGroupID:          11,
	}}
	invalidator := &cindyGroupAuthInvalidatorSpy{}
	svc := &adminServiceImpl{groupRepo: repo, authCacheInvalidator: invalidator}

	result, err := svc.SplitCindyGroup(context.Background(), 7, CindyGroupSplitInput{
		SourceKeeps:       CindyGroupSourceKeepsOrdinary,
		TargetName:        "Cindy Only",
		APIKeyIDs:         []int64{8},
		MemberFingerprint: fingerprint,
	})
	require.NoError(t, err)
	require.Equal(t, int64(11), result.TargetGroupID)
	require.Zero(t, repo.previewCalls, "commit must perform its authoritative snapshot inside the repository transaction")
	require.Equal(t, 1, repo.commitCalls)
	require.Equal(t, fingerprint, repo.commitInput.MemberFingerprint)
	require.Equal(t, []int64{7, 11}, invalidator.groupIDs)
}

func TestNormalizeCindyGroupSplitInputRejectsAmbiguousSelections(t *testing.T) {
	fingerprint := "b55e34d35e40f425b885f8293c7f0c8a9f61f505fa0f2a1258a6c68f3d63998a"
	tests := []struct {
		name  string
		input CindyGroupSplitInput
	}{
		{name: "unknown source side", input: CindyGroupSplitInput{SourceKeeps: "both", TargetName: "target"}},
		{name: "empty target", input: CindyGroupSplitInput{SourceKeeps: CindyGroupSourceKeepsCindy}},
		{name: "duplicate key", input: CindyGroupSplitInput{SourceKeeps: CindyGroupSourceKeepsCindy, TargetName: "target", APIKeyIDs: []int64{4, 4}}},
		{name: "non positive key", input: CindyGroupSplitInput{SourceKeeps: CindyGroupSourceKeepsCindy, TargetName: "target", APIKeyIDs: []int64{0}}},
		{name: "missing commit fingerprint", input: CindyGroupSplitInput{SourceKeeps: CindyGroupSourceKeepsCindy, TargetName: "target"}},
		{name: "invalid commit fingerprint", input: CindyGroupSplitInput{SourceKeeps: CindyGroupSourceKeepsCindy, TargetName: "target", MemberFingerprint: fingerprint[:63]}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeCindyGroupSplitInput(test.input, true)
			require.Error(t, err)
		})
	}
}

func TestBuildCindySplitTargetGroupCopiesPricingAndResetsIdentity(t *testing.T) {
	price := 0.25
	source := &Group{
		ID:                        7,
		Name:                      "Mixed",
		Platform:                  PlatformOpenAI,
		Status:                    StatusActive,
		RateMultiplier:            1.5,
		LongContextPricingEnabled: true,
		ProfitControlEnabled:      true,
		ProfitMinMargin:           0.25,
		ProfitSafetyBuffer:        0.05,
		ModelPricing: []ChannelModelPricing{{
			Platform:    PlatformOpenAI,
			Models:      []string{"gpt-5.6-luna"},
			BillingMode: BillingModeToken,
			InputPrice:  &price,
		}},
		DuplicateOperationID: "old-operation",
	}

	target := BuildCindySplitTargetGroup(source, "Cindy Only")
	require.Zero(t, target.ID)
	require.Equal(t, "Cindy Only", target.Name)
	require.Equal(t, StatusActive, target.Status)
	require.Empty(t, target.DuplicateOperationID)
	require.True(t, target.LongContextPricingEnabled)
	require.True(t, target.ProfitControlEnabled)
	require.Equal(t, 0.25, target.ProfitMinMargin)
	require.Equal(t, 0.05, target.ProfitSafetyBuffer)
	require.Equal(t, source.ModelPricing, target.ModelPricing)
	target.ModelPricing[0].Models[0] = "changed"
	require.Equal(t, "gpt-5.6-luna", source.ModelPricing[0].Models[0])
}

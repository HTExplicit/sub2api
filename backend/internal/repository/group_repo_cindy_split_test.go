//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupRepositoryAuditCindyGroupsUsesStrictIdentityAndAnonymousCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)WITH account_counts AS .*WHERE g\.platform = \$1.*ORDER BY g\.sort_order ASC, g\.id ASC`).
		WithArgs(
			service.PlatformOpenAI,
			service.PlatformOpenAI,
			service.AccountTypeAPIKey,
			"https://api.laxarouter.ai",
			"https://api.laxarouter.ai/",
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "status", "account_count", "cindy_account_count", "api_key_count",
		}).
			AddRow(int64(1), "Cindy", service.StatusActive, int64(2), int64(2), int64(3)).
			AddRow(int64(2), "Mixed", service.StatusActive, int64(4), int64(1), int64(2)).
			AddRow(int64(3), "Ordinary", service.StatusDisabled, int64(1), int64(0), int64(0)))

	repo := &groupRepository{sql: db}
	entries, err := repo.AuditCindyGroups(context.Background())
	require.NoError(t, err)
	require.Equal(t, []service.CindyGroupAuditEntry{
		{
			GroupID:              1,
			GroupName:            "Cindy",
			Status:               service.StatusActive,
			Classification:       service.CindyGroupClassificationPureCindy,
			CindyAccountCount:    2,
			OrdinaryAccountCount: 0,
			APIKeyCount:          3,
		},
		{
			GroupID:              2,
			GroupName:            "Mixed",
			Status:               service.StatusActive,
			Classification:       service.CindyGroupClassificationMixed,
			CindyAccountCount:    1,
			OrdinaryAccountCount: 3,
			APIKeyCount:          2,
		},
		{
			GroupID:              3,
			GroupName:            "Ordinary",
			Status:               service.StatusDisabled,
			Classification:       service.CindyGroupClassificationNoCindy,
			CindyAccountCount:    0,
			OrdinaryAccountCount: 1,
			APIKeyCount:          0,
		},
	}, entries)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCindyGroupSplitFingerprintTracksSemanticDrift(t *testing.T) {
	now := time.Date(2026, 8, 16, 1, 2, 3, 0, time.UTC)
	source := &service.Group{
		ID:        9,
		Name:      "mixed",
		Status:    service.StatusActive,
		Platform:  service.PlatformOpenAI,
		UpdatedAt: now,
	}
	members := []cindyGroupSplitMember{{
		accountID: 11,
		priority:  5,
		isCindy:   true,
	}}
	keys := []cindyGroupSplitAPIKey{{id: 21}}
	input := service.CindyGroupSplitInput{
		SourceKeeps: service.CindyGroupSourceKeepsCindy,
		TargetName:  "ordinary",
		APIKeyIDs:   []int64{21},
	}

	baseline, err := cindyGroupSplitFingerprint(source, members, keys, input)
	require.NoError(t, err)
	require.Len(t, baseline, 64)

	changedMembers := append([]cindyGroupSplitMember(nil), members...)
	changedMembers[0].priority++
	changedFingerprint, err := cindyGroupSplitFingerprint(source, changedMembers, keys, input)
	require.NoError(t, err)
	require.NotEqual(t, baseline, changedFingerprint)

	changedMembers = append([]cindyGroupSplitMember(nil), members...)
	changedMembers[0].isCindy = false
	changedFingerprint, err = cindyGroupSplitFingerprint(source, changedMembers, keys, input)
	require.NoError(t, err)
	require.NotEqual(t, baseline, changedFingerprint)

	changedKeys := append([]cindyGroupSplitAPIKey(nil), keys...)
	changedKeys[0].id++
	changedFingerprint, err = cindyGroupSplitFingerprint(source, members, changedKeys, input)
	require.NoError(t, err)
	require.NotEqual(t, baseline, changedFingerprint)

	changedSource := *source
	changedSource.UpdatedAt = changedSource.UpdatedAt.Add(time.Second)
	changedFingerprint, err = cindyGroupSplitFingerprint(&changedSource, members, keys, input)
	require.NoError(t, err)
	require.Equal(t, baseline, changedFingerprint, "generic row timestamps are not target policy")

	changedSource.RateMultiplier++
	changedFingerprint, err = cindyGroupSplitFingerprint(&changedSource, members, keys, input)
	require.NoError(t, err)
	require.NotEqual(t, baseline, changedFingerprint)
}

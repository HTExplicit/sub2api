package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRepositoryClassifyStrictCindyGroupUsesAggregateIdentityQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		members    int64
		cindy      int64
		wantStrict bool
	}{
		{name: "all non-deleted members are Cindy", members: 2, cindy: 2, wantStrict: true},
		{name: "mixed non-deleted membership", members: 2, cindy: 1, wantStrict: false},
		{name: "no non-deleted membership", members: 0, cindy: 0, wantStrict: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var capturedSQL string
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &capturedSQL}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			mock.ExpectQuery("cindy group identity aggregate").
				WithArgs(
					int64(42),
					service.PlatformCindy,
					service.WirePlatformOpenAI,
					service.ProviderProfileCindyLaxaV1,
					service.PlatformCindy,
					service.WirePlatformOpenAI,
					service.ProviderProfileCindyLaxaV1,
					service.AccountTypeAPIKey,
					"https://api.laxarouter.ai",
					"https://api.laxarouter.ai/",
				).
				WillReturnRows(sqlmock.NewRows([]string{"account_count", "cindy_account_count"}).
					AddRow(test.members, test.cindy))

			repo := &accountRepository{sql: db}
			got, err := repo.ClassifyStrictCindyGroup(context.Background(), 42)
			require.NoError(t, err)
			require.Equal(t, test.wantStrict, got)
			require.NoError(t, mock.ExpectationsWereMet())

			normalizedSQL := strings.ToLower(capturedSQL)
			require.Contains(t, normalizedSQL, "sum(case when")
			require.Contains(t, normalizedSQL, "a.wire_platform = $6")
			require.Contains(t, normalizedSQL, "a.provider_profile = $7")
			require.Contains(t, normalizedSQL, "lower(trim")
			require.Contains(t, normalizedSQL, "join groups g")
			require.Contains(t, normalizedSQL, "g.platform = $2")
			require.Contains(t, normalizedSQL, "g.wire_platform = $3")
			require.Contains(t, normalizedSQL, "g.provider_profile = $4")
			require.Contains(t, normalizedSQL, "g.deleted_at is null")
			require.Contains(t, normalizedSQL, "a.deleted_at is null")
			require.NotContains(t, normalizedSQL, "a.status")
		})
	}
}

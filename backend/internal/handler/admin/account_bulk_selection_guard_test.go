package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountJobBulkUpdateInvalidExplicitIDsDoNotBroadenScope(t *testing.T) {
	for _, test := range []struct {
		name       string
		ids        []int64
		filters    *BulkUpdateAccountFilters
		wantStatus int
		wantIDs    []int64
	}{
		{name: "invalid_ids_without_filters", ids: []int64{0, -1}, wantStatus: http.StatusBadRequest},
		{name: "invalid_ids_with_all_filters", ids: []int64{0, -1}, filters: &BulkUpdateAccountFilters{}, wantStatus: http.StatusBadRequest},
		{name: "invalid_ids_before_filter_validation", ids: []int64{0, -1}, filters: &BulkUpdateAccountFilters{Group: "not-an-id"}, wantStatus: http.StatusBadRequest},
		{name: "valid_ids_still_ignore_filters", ids: []int64{0, 7, -1, 3, 7}, filters: &BulkUpdateAccountFilters{Group: "not-an-id"}, wantStatus: http.StatusAccepted, wantIDs: []int64{3, 7}},
		{name: "filter_only_preserved", filters: &BulkUpdateAccountFilters{}, wantStatus: http.StatusAccepted, wantIDs: []int64{3, 7, 99}},
		{name: "empty_ids_with_filters_preserved", ids: []int64{}, filters: &BulkUpdateAccountFilters{}, wantStatus: http.StatusAccepted, wantIDs: []int64{3, 7, 99}},
	} {
		t.Run(test.name, func(t *testing.T) {
			admin := newBulkJobScopeAdmin(3, 7, 99)
			admin.matches = []int64{3, 7, 99}
			router, handler, scopeRepo := newBulkJobScopeRouter(admin)
			repo := &bulkSelectionGuardRepository{bulkJobScopeRepository: scopeRepo}
			handler.SetAccountJobService(service.NewAccountJobService(repo, accountJobTestEncryptor{}))

			recorder := submitBulkJobScopeRequest(t, router, BulkUpdateAccountsRequest{
				AccountIDs: test.ids, Filters: test.filters, Name: "updated",
			}, "selection-scope")

			require.Equal(t, test.wantStatus, recorder.Code, recorder.Body.String())
			require.Empty(t, admin.updatedIDs, "acceptance must not execute the job synchronously")
			if test.wantStatus == http.StatusBadRequest {
				require.Contains(t, recorder.Body.String(), "account_ids must include at least one positive ID")
				require.Zero(t, repo.replayLookups, "invalid explicit IDs must be rejected before idempotency replay")
				require.Zero(t, admin.filterCalls, "invalid explicit IDs must not broaden to filter targets")
				require.Empty(t, repo.created, "invalid explicit IDs must not enqueue a job")
				return
			}

			require.Len(t, repo.created, 1)
			var targets []int64
			for _, seed := range repo.created[0].Items {
				require.NotNil(t, seed.TargetAccountID)
				targets = append(targets, *seed.TargetAccountID)
			}
			require.Equal(t, test.wantIDs, targets)
			if len(test.ids) > 0 {
				require.Zero(t, admin.filterCalls, "valid explicit IDs must retain priority over filters")
			}
		})
	}
}

type bulkSelectionGuardRepository struct {
	*bulkJobScopeRepository
	replayLookups int
}

func (r *bulkSelectionGuardRepository) FindIdempotent(ctx context.Context, actor int64, kind, key string) (*service.AccountJob, error) {
	r.replayLookups++
	return r.bulkJobScopeRepository.FindIdempotent(ctx, actor, kind, key)
}

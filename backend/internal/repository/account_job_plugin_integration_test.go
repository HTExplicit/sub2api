//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAccountJobPluginFenceRejectsStaleSubmissionAndKeepsHostLease(t *testing.T) {
	ctx := context.Background()
	plugins, current, candidate := installedUpdateFixture(t)
	user := mustCreateUser(t, testEntClient(t), &service.User{Email: "job-fence-" + uuid.NewString() + "@example.com", PasswordHash: "fixture"})
	jobs := NewAccountJobRepository(integrationDB)
	var jobID int64
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM admin_account_jobs WHERE id=$1", jobID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
	})
	params := service.CreateAccountJobParams{CreatedBy: user.ID, Kind: service.AccountJobKindImportData, IdempotencyKey: uuid.NewString(), RequestHash: strings.Repeat("b", 64), PayloadCipher: "fixture", PayloadExpires: time.Now().Add(time.Hour), Metadata: json.RawMessage(fmt.Sprintf(`{"plugin_id":%d,"plugin_generation":%d}`, current.ID, current.RuntimeGeneration)), Items: []service.AccountJobItemSeed{{Ordinal: 1, Metadata: []byte(`{}`)}}, Attempt: 1}
	job, _, err := jobs.Create(ctx, params)
	require.NoError(t, err)
	jobID = job.ID
	releaseHostTask, err := plugins.HoldPluginRuntime(ctx, current)
	require.NoError(t, err)
	defer releaseHostTask()
	require.NoError(t, plugins.StagePluginUpdate(ctx, current, candidate, service.PluginUpdatePinned))
	pending, err := plugins.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.ErrorIs(t, plugins.CommitPluginUpdate(ctx, pending, candidate), service.ErrPluginUpdateWaiting)
	params.IdempotencyKey = uuid.NewString()
	_, _, err = jobs.Create(ctx, params)
	require.ErrorIs(t, err, service.ErrAccountJobPluginUnavailable, "updating must reject late submissions")
	releaseHostTask()
	require.NoError(t, plugins.CommitPluginUpdate(ctx, pending, candidate))
	_, _, err = jobs.Create(ctx, params)
	require.ErrorIs(t, err, service.ErrAccountJobPluginUnavailable, "old generation cannot create work after promotion")
	finished, err := jobs.Get(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, service.AccountJobStatusCanceled, finished.Status)
}

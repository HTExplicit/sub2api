//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

// Preserve the historical digest contract verified in 9774e09fb. The old owner
// metadata is an immutable record; executing native jobs needs no plugin install.
func TestAccountJobPluginActionDigestRoundTrip(t *testing.T) {
	ctx := context.Background()
	user := mustCreateUser(t, testEntClient(t), &service.User{Email: "job-digest-" + uuid.NewString() + "@example.com", PasswordHash: "fixture"})
	jobs := NewAccountJobRepository(integrationDB)
	var jobID int64
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM admin_account_jobs WHERE id=$1", jobID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
	})
	// SubmitJob uses the complete SHA-256 hex value to address an encrypted
	// per-target payload. The stored action must remain the same lookup key.
	digest := sha256.Sum256([]byte("7\x00{\"model\":\"gpt-6-astra\"}"))
	action := hex.EncodeToString(digest[:])
	params := service.CreateAccountJobParams{
		CreatedBy: user.ID, Kind: service.AccountJobKindExtensionOperation,
		IdempotencyKey: uuid.NewString(), RequestHash: strings.Repeat("b", 64),
		PayloadCipher: "fixture", PayloadExpires: time.Now().Add(time.Hour),
		Metadata: json.RawMessage(`{"plugin_id":1,"plugin_generation":8}`),
		Items:    []service.AccountJobItemSeed{{Ordinal: 1, Action: action, Metadata: []byte(`{}`)}}, Attempt: 1,
	}
	job, replayed, err := jobs.Create(ctx, params)
	require.NoError(t, err)
	jobID = job.ID
	require.False(t, replayed)
	items, err := jobs.ListItems(ctx, jobID, "", 1, 10)
	require.NoError(t, err)
	require.Len(t, items.Items, 1)
	require.Equal(t, action, items.Items[0].Action, "the payload digest must not be truncated or re-encoded")

	again, replayed, err := jobs.Create(ctx, params)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, jobID, again.ID)
	items, err = jobs.ListItems(ctx, jobID, "", 1, 10)
	require.NoError(t, err)
	require.Len(t, items.Items, 1, "an idempotent submission must not duplicate its target")
}

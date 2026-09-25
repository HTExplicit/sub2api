package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountViewNativeFactsProjection(t *testing.T) {
	now := time.Now().UTC()
	account := &service.Account{ID: 17, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: false,
		Credentials: map[string]any{"base_url": "https://relay.example.test", "api_key": "synthetic-private-key", "plan_type": "plus"}, Extra: map[string]any{"privacy_mode": "true", "private_secret": "synthetic-extra-secret"}}
	full := AccountFromServiceShallow(account)
	require.NotNil(t, full.AccountViewFacts)
	require.Equal(t, service.AccountViewFactsFromAccount(account, now), full.AccountViewFacts)
	require.Equal(t, "unschedulable", full.AccountViewFacts.Status)
	require.Equal(t, "plus", full.AccountViewFacts.Plan)
	compact := AccountListItemFromAccount(full)
	require.Equal(t, full.AccountViewFacts, compact.AccountViewFacts)
	raw, err := json.Marshal(compact.AccountViewFacts)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "synthetic-private-key")
	require.NotContains(t, string(raw), "synthetic-extra-secret")
	require.NotContains(t, string(raw), "credentials")
	require.NotContains(t, string(raw), "extra")
}

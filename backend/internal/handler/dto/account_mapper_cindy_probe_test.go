package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountFromServiceShallowMapsNullableCindyBalanceProbeFields(t *testing.T) {
	jobID := int64(91)
	outcome := "recovered"
	checkedAt := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	mapped := AccountFromServiceShallow(&service.Account{
		ID:                     7,
		CindyBalanceProbeJobID: &jobID, CindyBalanceProbeOutcome: &outcome,
		CindyBalanceProbeCheckedAt: &checkedAt,
	})
	require.NotNil(t, mapped)
	require.Equal(t, jobID, *mapped.CindyBalanceProbeJobID)
	require.Equal(t, outcome, *mapped.CindyBalanceProbeOutcome)
	require.Equal(t, checkedAt, *mapped.CindyBalanceProbeCheckedAt)

	payload, err := json.Marshal(mapped)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(payload, &document))
	require.Equal(t, float64(jobID), document["cindy_balance_probe_job_id"])
	require.Equal(t, outcome, document["cindy_balance_probe_outcome"])
	require.Equal(t, checkedAt.Format(time.RFC3339), document["cindy_balance_probe_checked_at"])
}

func TestAccountFromServiceShallowKeepsMissingCindyBalanceProbeFieldsNull(t *testing.T) {
	mapped := AccountFromServiceShallow(&service.Account{ID: 8})
	payload, err := json.Marshal(mapped)
	require.NoError(t, err)

	var document map[string]any
	require.NoError(t, json.Unmarshal(payload, &document))
	require.Contains(t, document, "cindy_balance_probe_job_id")
	require.Contains(t, document, "cindy_balance_probe_outcome")
	require.Contains(t, document, "cindy_balance_probe_checked_at")
	require.Nil(t, document["cindy_balance_probe_job_id"])
	require.Nil(t, document["cindy_balance_probe_outcome"])
	require.Nil(t, document["cindy_balance_probe_checked_at"])
}

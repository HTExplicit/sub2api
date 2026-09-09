//go:build unit

package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountCapabilityCatalogEvidenceRepositoryPreservesBothLatestAttemptAndSuccess(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	now := time.Now().UTC()
	old := now.Add(-72 * time.Hour)
	prefix := `WITH scoped AS \([\s\S]+r.kind=\$1 AND i.account_id IN \(\$2\) AND i.folder_id IN \(\$3\)[\s\S]+latest_attempts AS \([\s\S]+DISTINCT ON \(account_id,folder_id,config_fingerprint,upstream_model,protocol,profile\)[\s\S]+last_successes AS \([\s\S]+status='succeeded'[\s\S]+SELECT id FROM latest_attempts UNION SELECT id FROM last_successes[\s\S]+`
	mock.ExpectQuery(prefix+`SELECT COUNT\(\*\) FROM`).WithArgs("probe", int64(31), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	rows := sqlmock.NewRows(capabilityJobsItemColumns()).
		AddRow(12, 2, 1, 31, "fixture", 7, strings.Repeat("b", 64), "claude-fable-5", "responses", "text", []byte(`[]`), "failed",
			[]byte(`{"status":"failed","classification":"upstream_unavailable","request_count":1}`), 1, now, now, now, now, "probe", false).
		AddRow(9, 1, 1, 31, "fixture", 7, strings.Repeat("b", 64), "claude-fable-5", "responses", "text", []byte(`[]`), "succeeded",
			[]byte(`{"status":"alive","classification":"text_completed","request_count":1}`), 1, old, old, old, old, "probe", false)
	mock.ExpectQuery(prefix+`SELECT `+regexp.QuoteMeta(capabilityItemColumns)+` FROM`).
		WithArgs("probe", int64(31), int64(7), 50, 0).WillReturnRows(rows)
	page, err := repo.EvidenceItems(context.Background(), service.AccountCapabilityFilter{Kind: "probe", FolderIDs: []int64{7}, AccountIDs: []int64{31}})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
	require.Len(t, page.Items, 2)
	require.Equal(t, "failed", page.Items[0].Status)
	require.Equal(t, "succeeded", page.Items[1].Status)
	require.False(t, page.Items[1].PublicationSuperseded)
	require.Equal(t, old, *page.Items[1].FinishedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityCatalogEvidenceRepositoryRetainsReservedAndPossiblySentTargets(t *testing.T) {
	repo, mock := newCapabilityJobsRepoTest(t)
	prefix := `WITH scoped AS \([\s\S]+FROM scoped WHERE NOT \(status='canceled' AND dispatched_at IS NULL AND request_count=0[\s\S]+result->>'request_count_unknown' IS DISTINCT FROM 'true'\)[\s\S]+`
	mock.ExpectQuery(prefix + `SELECT COUNT\(\*\) FROM`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	now := time.Now().UTC()
	mock.ExpectQuery(prefix+`SELECT `+regexp.QuoteMeta(capabilityItemColumns)+` FROM`).WithArgs(50, 0).
		WillReturnRows(sqlmock.NewRows(capabilityJobsItemColumns()).AddRow(15, 3, 1, 31, "fixture", 7, strings.Repeat("b", 64), "claude-fable-5", "chat_completions", "text", []byte(`[]`), "running", []byte(`{}`), 0, now, nil, nil, now, "probe", false))
	page, err := repo.EvidenceItems(context.Background(), service.AccountCapabilityFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "running", page.Items[0].Status)
	require.Nil(t, page.Items[0].FinishedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCapabilityCatalogEvidenceSupersessionUsesOnlyDefinitiveNarrowFailures(t *testing.T) {
	require.Contains(t, capabilityPublicationSupersededSQL, "n.folder_id=i.folder_id")
	require.Contains(t, capabilityPublicationSupersededSQL, "n.upstream_model=i.upstream_model AND n.protocol=i.protocol AND n.profile=i.profile")
	require.Contains(t, capabilityPublicationSupersededSQL, "n.result->>'classification' IN ('model_unavailable','protocol_unsupported')")
	require.Contains(t, capabilityPublicationSupersededSQL, "i.result->>'account_failure' IS DISTINCT FROM 'true'", "a model-level failure cannot retire a prior account-wide credential failure")
	require.NotContains(t, capabilityPublicationSupersededSQL, "n.status IN ('failed','indeterminate','stale')")
	for _, classification := range []string{"timeout", "rate_limited", "upstream_unavailable", "tool_contract_mismatch", "metadata_available"} {
		require.NotContains(t, capabilityPublicationSupersededSQL, "'"+classification+"'")
	}
	require.Contains(t, capabilityPublicationSupersededSQL, "nr.kind='probe' AND n.profile='text'")
	require.Contains(t, capabilityPublicationSupersededSQL, "n.protocol IN ('responses','chat_completions','messages','responses_websocket')")
	require.NotContains(t, capabilityPublicationSupersededSQL, "responses_input_tokens")
	require.NotContains(t, capabilityPublicationSupersededSQL, "messages_count_tokens")
	require.NotContains(t, capabilityPublicationSupersededSQL, "INTERVAL")
}

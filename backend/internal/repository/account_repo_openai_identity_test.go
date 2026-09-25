package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRepository_OAuthIdentityCASPlatformAndAtomicOutbox(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
			repo := newAccountRepositoryWithSQL(nil, exec, nil)
			proxyID := int64(29)
			update := repo.UpdateOpenAIOAuthCredentialsIfUnchanged
			if platform == service.PlatformGrok {
				update = repo.UpdateGrokOAuthCredentialsIfUnchanged
			}
			applied, err := update(context.Background(), 42,
				map[string]any{"refresh_token": "synthetic-old", "_token_version": int64(9)},
				&proxyID,
				map[string]any{"refresh_token": "synthetic-new", "_token_version": int64(10)},
			)
			require.NoError(t, err)
			require.True(t, applied)
			require.Len(t, exec.execQueries, 1)
			query := normalizeSQLWhitespace(exec.execQueries[0])
			require.Contains(t, query, "WITH updated AS")
			require.Contains(t, query, "a.platform = $3")
			require.Contains(t, query, "a.type = $4")
			require.Contains(t, query, "a.credentials = $5::jsonb")
			require.Contains(t, query, "a.proxy_id IS NOT DISTINCT FROM $6")
			require.Contains(t, query, "INSERT INTO scheduler_outbox")
			require.NotContains(t, query, "SET status")
			require.Len(t, exec.execArgs[0], 7)
			require.Equal(t, platform, exec.execArgs[0][2])
			require.Equal(t, service.AccountTypeOAuth, exec.execArgs[0][3])
			require.Equal(t, &proxyID, exec.execArgs[0][5])
			require.Equal(t, service.SchedulerOutboxEventAccountChanged, exec.execArgs[0][6])
		})
	}
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type failingCindyClassifierExecutor struct {
	sqlExecutor
	err error
}

func (e failingCindyClassifierExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(strings.ToLower(query), "from account_groups") {
		return nil, e.err
	}
	return e.sqlExecutor.QueryContext(ctx, query, args...)
}

func TestAPIKeyRepositoryGetByKeyForAuthMaterializesCindyIdentity(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "cindy-auth-materialized@test.com")
	group, err := client.Group.Create().
		SetName("cindy-auth-materialized").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	account, err := client.Account.Create().
		SetName("cindy-auth-materialized-account").
		SetPlatform(service.PlatformOpenAI).
		SetType(service.AccountTypeAPIKey).
		SetStatus(service.StatusActive).
		SetCredentials(map[string]any{
			"api_key":  "upstream-secret",
			"base_url": "https://api.laxarouter.ai",
		}).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AccountGroup.Create().
		SetAccountID(account.ID).
		SetGroupID(group.ID).
		SetPriority(50).
		Save(ctx)
	require.NoError(t, err)

	key := &service.APIKey{
		UserID: user.ID, GroupID: &group.ID, Key: "sk-cindy-auth-materialized",
		Name: "Cindy materialized", Status: service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	strict, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, strict.Group)
	require.True(t, strict.Group.StrictCindyKnown)
	require.True(t, strict.Group.StrictCindy)

	_, err = client.Account.UpdateOneID(account.ID).
		SetCredentials(map[string]any{
			"api_key":  "upstream-secret",
			"base_url": "https://api.openai.com",
		}).
		Save(ctx)
	require.NoError(t, err)

	ordinary, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, ordinary.Group)
	require.True(t, ordinary.Group.StrictCindyKnown)
	require.False(t, ordinary.Group.StrictCindy)
}

func TestAPIKeyRepositoryGetByKeyForAuthTreatsDisabledOrdinaryMemberAsMixed(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "cindy-auth-disabled-ordinary@test.com")
	group, err := client.Group.Create().
		SetName("cindy-auth-disabled-ordinary").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	createMember := func(name, status, baseURL string) {
		account, createErr := client.Account.Create().
			SetName(name).
			SetPlatform(service.PlatformOpenAI).
			SetType(service.AccountTypeAPIKey).
			SetStatus(status).
			SetCredentials(map[string]any{
				"api_key":  "upstream-secret",
				"base_url": baseURL,
			}).
			Save(ctx)
		require.NoError(t, createErr)
		_, createErr = client.AccountGroup.Create().
			SetAccountID(account.ID).
			SetGroupID(group.ID).
			SetPriority(50).
			Save(ctx)
		require.NoError(t, createErr)
	}
	createMember("active-cindy", service.StatusActive, "https://api.laxarouter.ai")
	createMember("disabled-ordinary", service.StatusDisabled, "https://api.openai.com")

	key := &service.APIKey{
		UserID: user.ID, GroupID: &group.ID, Key: "sk-cindy-auth-disabled-ordinary",
		Name: "Mixed materialized", Status: service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.True(t, got.Group.StrictCindyKnown)
	require.False(t, got.Group.StrictCindy,
		"strict identity must include disabled non-deleted members")
}

func TestAPIKeyRepositoryGetByKeyForAuthMaterializesEmptyGroupAsKnownOrdinary(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "ordinary-auth-materialized@test.com")
	group, err := client.Group.Create().
		SetName("ordinary-auth-materialized").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{
		UserID: user.ID, GroupID: &group.ID, Key: "sk-ordinary-auth-materialized",
		Name: "Ordinary materialized", Status: service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.True(t, got.Group.StrictCindyKnown)
	require.False(t, got.Group.StrictCindy)
}

func TestAPIKeyRepositoryGetByKeyForAuthSkipsCindyAggregateForNonOpenAIGroup(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "gemini-auth-materialized@test.com")
	group, err := client.Group.Create().
		SetName("gemini-auth-materialized").
		SetPlatform(service.PlatformGemini).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{
		UserID: user.ID, GroupID: &group.ID, Key: "sk-gemini-auth-materialized",
		Name: "Gemini materialized", Status: service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	identityDB, identityMock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = identityDB.Close() })
	repo.sql = identityDB

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.True(t, got.Group.StrictCindyKnown)
	require.False(t, got.Group.StrictCindy)
	require.NoError(t, identityMock.ExpectationsWereMet(), "non-OpenAI auth must not execute the Cindy identity aggregate")
}

func TestAPIKeyRepositoryGetByKeyForAuthFailsClosedWhenIdentityClassificationFails(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "cindy-auth-classifier-failure@test.com")
	group, err := client.Group.Create().
		SetName("cindy-auth-classifier-failure").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{
		UserID: user.ID, GroupID: &group.ID, Key: "sk-cindy-auth-classifier-failure",
		Name: "Classifier failure", Status: service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	classifierErr := errors.New("classifier storage unavailable")
	repo.sql = failingCindyClassifierExecutor{sqlExecutor: repo.sql, err: classifierErr}
	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.ErrorIs(t, err, classifierErr)
	require.Nil(t, got)
}

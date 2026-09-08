package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type managedModelWSFixture struct {
	group          *service.Group
	latestAccount  *service.Account
	groupErr       error
	compilationErr error
	groupReads     int
	accountReads   int
}

func (f *managedModelWSFixture) LatestManagedModelGroup(context.Context, int64) (*service.Group, error) {
	f.groupReads++
	return f.group, f.groupErr
}

func (f *managedModelWSFixture) ValidateManagedModelCompilation(context.Context, *service.Group, *service.ManagedModelRequest) error {
	return f.compilationErr
}

func (f *managedModelWSFixture) ValidateManagedModelAccountLatest(ctx context.Context, account *service.Account, model string) error {
	f.accountReads++
	if err := service.ValidateManagedModelAccount(ctx, account, model); err != nil {
		return err
	}
	if f.latestAccount == nil || !f.latestAccount.IsSchedulable() {
		return service.ErrManagedModelRouteUnavailable
	}
	request, ok := service.ManagedModelRequestFromContext(ctx)
	bound := false
	if ok {
		for _, groupID := range f.latestAccount.GroupIDs {
			bound = bound || groupID == request.GroupID
		}
	}
	if !bound {
		return service.ErrManagedModelRouteUnavailable
	}
	return service.ValidateManagedModelAccount(ctx, f.latestAccount, model)
}

func newManagedModelWSFixture() (*managedModelWSGuard, *managedModelWSFixture, *service.Account) {
	const groupID, accountID = int64(23), int64(41)
	selector := service.ManagedModelSelector(groupID, "gpt-5.4")
	account := &service.Account{
		ID: accountID, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		GroupIDs: []int64{groupID},
		Credentials: map[string]any{
			"api_key": "offline-fixture-key", "base_url": "https://fixture.invalid",
			"model_mapping": map[string]any{selector: "verified-wire-model", "gpt-5.4": "private-vip-model"},
		},
	}
	group := &service.Group{
		ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive,
		ManagedModelRoutes: service.ManagedModelRoutesConfig{
			Version: service.ManagedModelRoutesVersion, Enabled: true,
			Routes: []service.ManagedModelRoute{{
				PublicModel: "gpt-5.4", Aliases: []string{"public-gpt"}, Selector: selector,
				TargetPlatform: service.PlatformOpenAI,
				Endpoints:      []string{service.ManagedModelEndpointResponsesWebSocket},
				Accounts: []service.ManagedModelRouteAccount{{
					AccountID: accountID, UpstreamModel: "verified-wire-model",
					AccountFingerprint: service.ManagedModelAccountFingerprint(account),
					Endpoints:          []string{service.ManagedModelEndpointResponsesWebSocket},
				}},
			}},
		},
	}
	fixture := &managedModelWSFixture{group: group, latestAccount: account}
	return newManagedModelWSGuard(group, fixture, fixture), fixture, account
}

func TestManagedModelWSFirstFrameCanonicalizesAliasAndPreservesEffort(t *testing.T) {
	for _, tc := range []struct {
		name, payload, effort string
	}{
		{"official alias", `{"type":"response.create","Model":" GPT-5.4-HIGH ","input":"ping"}`, "high"},
		{"explicit wins", `{"type":"response.create","model":"gpt-5.4-high","reasoning":{"effort":"low"}}`, "low"},
		{"declared alias", `{"model":" PUBLIC-GPT "}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard, _, _ := newManagedModelWSFixture()
			body, request, _, err := guard.prepareFrame(context.Background(), 1, []byte(tc.payload), "", nil)
			require.NoError(t, err)
			require.Equal(t, "gpt-5.4", request.Route.PublicModel)
			require.Equal(t, "gpt-5.4", gjson.GetBytes(body, "model").String())
			require.False(t, gjson.GetBytes(body, "Model").Exists())
			require.Equal(t, tc.effort, gjson.GetBytes(body, "reasoning.effort").String())
			require.Equal(t, "response.create", gjson.GetBytes(body, "type").String())
			require.Nil(t, guard.current.Load(), "raw preparation must not replace an in-flight turn snapshot")
		})
	}
}

func TestManagedModelWSRawFramesRejectAmbiguousOrPrivateRouting(t *testing.T) {
	guard, _, account := newManagedModelWSFixture()
	selector := service.ManagedModelSelector(23, "gpt-5.4")
	for _, payload := range []string{
		`{"type":"response.cancel","type":"response.create","model":"gpt-5.4"}`,
		`{"Type":"response.create","model":"gpt-5.4"}`,
		`{"type":"response.create","model":"gpt-5.4","model":"gpt-5.4"}`,
		`{"type":"response.create","model":"gpt-5.4","Model":"gpt-5.4"}`,
		`{"type":"response.create","model":"verified-wire-model"}`,
		`{"type":"response.create","model":"` + selector + `"}`,
		`{"type":"response.create","model":"unknown"}`,
		`{"type":"response.create","model":"gpt-5.4","session":{"model":"unknown"}}`,
		`{"type":"session.update","session":{"model":"gpt-5.4","Model":"unknown"}}`,
		`{"type":"session.update","session":{"model":"` + selector + `"}}`,
		`{"type":"response.cancel","model":"gpt-5.4"}`,
		`{"type":"unknown.event"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			_, _, _, err := guard.prepareFrame(context.Background(), 2, []byte(payload), "gpt-5.4", account)
			require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
		})
	}
}

func TestManagedModelWSOmittedModelUsesEffectivePublicAliasAndLatestPublication(t *testing.T) {
	guard, fixture, account := newManagedModelWSFixture()
	ctx := context.Background()
	_, _, _, err := guard.prepareFrame(ctx, 1, []byte(`{"model":"gpt-5.4-high"}`), "", nil)
	require.NoError(t, err)
	body, request, _, err := guard.prepareFrame(ctx, 2, []byte(`{"type":"response.create"}`), "gpt-5.4", account)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", request.Route.PublicModel)
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
	require.Equal(t, 2, fixture.groupReads)
	_, _, _, err = guard.prepareFrame(ctx, 3, []byte(`{"type":"response.create"}`), "unknown-session-model", account)
	require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
	fixture.group.ManagedModelRoutes.Routes = nil
	_, _, _, err = guard.prepareFrame(ctx, 3, []byte(`{"type":"response.create"}`), "gpt-5.4", account)
	require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
}

func TestManagedModelWSSessionUpdateMapsOnlyVerifiedWireAndRetainsPublicIdentity(t *testing.T) {
	guard, _, account := newManagedModelWSFixture()
	body, request, _, err := guard.prepareFrame(context.Background(), 2, []byte(`{"type":"session.update","session":{"Model":"public-gpt","instructions":"unchanged"}}`), "gpt-5.4", account)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", request.Route.PublicModel)
	require.Equal(t, "verified-wire-model", gjson.GetBytes(body, "session.model").String())
	require.Equal(t, "unchanged", gjson.GetBytes(body, "session.instructions").String())
	require.False(t, gjson.GetBytes(body, "session.Model").Exists())
	require.Nil(t, guard.current.Load())
	_, _, _, err = guard.prepareFrame(context.Background(), 1, []byte(`{"type":"session.update","session":{"model":"gpt-5.4"}}`), "", account)
	require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
}

func TestManagedModelWSTurnChecksSnapshotAndLiveAccount(t *testing.T) {
	guard, fixture, account := newManagedModelWSFixture()
	ctx := context.Background()
	_, request, err := guard.resolve(ctx, "gpt-5.4")
	require.NoError(t, err)
	// Passthrough checks the public payload before mapping; native validates
	// its already-mapped payload against a successful same-turn snapshot.
	_, _, _, err = guard.validatePayload(ctx, 2, []byte(`{"type":"response.create","model":"gpt-5.4"}`), "gpt-5.4", account)
	require.NoError(t, err)
	require.NoError(t, guard.validateTurn(ctx, 2, account))
	require.NoError(t, guard.mappedTurn(ctx, 2, request, account))
	publicBody, _, _, err := guard.validatePayload(ctx, 2, []byte(`{"type":"response.create","model":"verified-wire-model"}`), "gpt-5.4", account)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(publicBody, "model").String())
	for _, payload := range []string{
		`{"type":"response.create","model":"private-vip-model"}`,
		`{"type":"response.create","model":"verified-wire-model","model":"gpt-5.4"}`,
		`{"type":"response.create","model":"verified-wire-model","Model":"verified-wire-model"}`,
	} {
		_, _, _, err = guard.validatePayload(ctx, 2, []byte(payload), "gpt-5.4", account)
		require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
	}
	_, _, _, err = guard.validatePayload(ctx, 3, []byte(`{"type":"response.create","model":"verified-wire-model"}`), "gpt-5.4", account)
	require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable, "a prior turn cannot authorize client wire IDs")
	changedAccount := *account
	changedAccount.Credentials = map[string]any{
		"api_key": "edited-offline-fixture-key", "base_url": "https://fixture.invalid",
		"model_mapping": map[string]any{request.Route.Selector: "verified-wire-model"},
	}
	fixture.latestAccount = &changedAccount
	require.ErrorIs(t, guard.validateTurn(ctx, 2, account), service.ErrManagedModelRouteUnavailable)
	require.ErrorIs(t, guard.mappedTurn(ctx, 3, request, account), service.ErrManagedModelRouteUnavailable)
}

func TestManagedModelWSGroupAndCompilationChangesFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*managedModelWSFixture)
	}{
		{"withdrawn", func(f *managedModelWSFixture) { f.group.ManagedModelRoutes.Routes = nil }},
		{"disabled", func(f *managedModelWSFixture) { f.group.ManagedModelRoutes.Enabled = false }},
		{"inactive", func(f *managedModelWSFixture) { f.group.Status = "disabled" }},
		{"platform changed", func(f *managedModelWSFixture) { f.group.Platform = service.PlatformComposite }},
		{"channel edited", func(f *managedModelWSFixture) { f.compilationErr = errors.New("changed compilation") }},
		{"group lookup failed", func(f *managedModelWSFixture) { f.groupErr = errors.New("lookup failed") }},
		{"endpoint withdrawn", func(f *managedModelWSFixture) {
			f.group.ManagedModelRoutes.Routes[0].Endpoints = []string{service.CompositeRouteEndpointResponses}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard, fixture, account := newManagedModelWSFixture()
			_, request, err := guard.resolve(context.Background(), "gpt-5.4")
			require.NoError(t, err)
			require.NoError(t, guard.mappedTurn(context.Background(), 1, request, account))
			tc.change(fixture)
			require.ErrorIs(t, guard.validateTurn(context.Background(), 1, account), service.ErrManagedModelRouteUnavailable)
		})
	}
}

func TestManagedModelWSLatestAccountRemovalOrMappingEditClosesNextTurn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*service.Account, string)
	}{
		{"removed from group", func(a *service.Account, _ string) { a.GroupIDs = nil }},
		{"scheduling disabled", func(a *service.Account, _ string) { a.Schedulable = false }},
		{"selector mapping edited", func(a *service.Account, selector string) {
			a.Credentials = map[string]any{
				"api_key": "offline-fixture-key", "base_url": "https://fixture.invalid",
				"model_mapping": map[string]any{selector: "private-vip-model"},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard, fixture, account := newManagedModelWSFixture()
			_, request, err := guard.resolve(context.Background(), "gpt-5.4")
			require.NoError(t, err)
			require.NoError(t, guard.mappedTurn(context.Background(), 1, request, account))
			latest := *account
			tc.change(&latest, request.Route.Selector)
			fixture.latestAccount = &latest
			_, _, _, err = guard.prepareFrame(context.Background(), 2, []byte(`{"type":"response.create"}`), "gpt-5.4", account)
			require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
		})
	}
}

func TestManagedModelWSUnmanagedAndCindyStayUnchanged(t *testing.T) {
	require.Nil(t, newManagedModelWSGuard(nil, nil, nil))
	require.Nil(t, newManagedModelWSGuard(&service.Group{Platform: service.PlatformOpenAI}, nil, nil))
	require.Nil(t, newManagedModelWSGuard(&service.Group{Platform: service.PlatformCindy, ManagedModelRoutes: service.ManagedModelRoutesConfig{Enabled: true}}, nil, nil))
	guard, fixture, _ := newManagedModelWSFixture()
	guard.groups = nil
	_, _, err := guard.resolve(context.Background(), "gpt-5.4")
	require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
	guard.groups = fixture
	guard.accounts = nil
	_, request, err := guard.resolve(context.Background(), "gpt-5.4")
	require.NoError(t, err)
	require.ErrorIs(t, guard.mappedTurn(context.Background(), 1, request, nil), service.ErrManagedModelRouteUnavailable)
}

func TestManagedModelWSUnmanagedConnectionObservesFirstPublicationWithoutRewriting(t *testing.T) {
	_, fixture, _ := newManagedModelWSFixture()
	fixture.group.ManagedModelRoutes.Enabled = false
	initialGroup := *fixture.group
	payload := []byte(`  {"type":"response.create","model":"private-name","model":"legacy-duplicate"} `)
	unchanged, err := observeUnmanagedModelWSFrame(context.Background(), fixture, &initialGroup, payload)
	require.NoError(t, err)
	require.Equal(t, payload, unchanged)
	require.Same(t, &payload[0], &unchanged[0], "unmanaged frames must retain the original byte slice")
	fixture.group.ManagedModelRoutes.Enabled = true
	_, err = observeUnmanagedModelWSFrame(context.Background(), fixture, &initialGroup, payload)
	require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable, "the old connection must reconnect after first publication")
	fixture.group.ManagedModelRoutes.Enabled = false
	fixture.groupErr = errors.New("lookup failed")
	_, err = observeUnmanagedModelWSFrame(context.Background(), fixture, &initialGroup, payload)
	require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
	for _, privateGroup := range []*service.Group{
		{ID: initialGroup.ID, Platform: service.PlatformOpenAI, IsExclusive: true},
		{ID: initialGroup.ID, Platform: service.PlatformCindy},
	} {
		unchanged, err = observeUnmanagedModelWSFrame(context.Background(), nil, privateGroup, payload)
		require.NoError(t, err)
		require.Same(t, &payload[0], &unchanged[0])
	}
}

//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
)

type viewFixtureRepo struct {
	installations  map[int64]*PluginInstallation
	held, released []int64
}

func (r *viewFixtureRepo) List(context.Context) ([]*PluginInstallation, error) {
	var out []*PluginInstallation
	for _, item := range r.installations {
		out = append(out, item)
	}
	return out, nil
}
func (r *viewFixtureRepo) GetByID(_ context.Context, id int64) (*PluginInstallation, error) {
	item := r.installations[id]
	if item == nil {
		return nil, errors.New("not found")
	}
	return cloneHostIOInstallation(item), nil
}
func (r *viewFixtureRepo) GetByKey(ctx context.Context, key string) (*PluginInstallation, error) {
	for id, item := range r.installations {
		if item.PluginKey == key {
			return r.GetByID(ctx, id)
		}
	}
	return nil, errors.New("not found")
}
func (*viewFixtureRepo) Install(context.Context, *PluginInstallation, []PluginBinding) (*PluginInstallation, error) {
	return nil, errors.New("unexpected fixture install")
}
func (*viewFixtureRepo) GetArtifact(context.Context, int64) ([]byte, error) {
	return nil, errors.New("unexpected fixture artifact")
}
func (*viewFixtureRepo) Delete(context.Context, int64, string) error {
	return errors.New("unexpected fixture delete")
}
func (*viewFixtureRepo) BeginEnable(context.Context, int64, string, string) error {
	return errors.New("unexpected fixture enable")
}
func (*viewFixtureRepo) MarkRuntimeHealthy(context.Context, int64, string, string) error {
	return errors.New("unexpected fixture runtime write")
}
func (*viewFixtureRepo) UpdateState(context.Context, int64, string, string, *time.Time, string, string) error {
	return errors.New("unexpected fixture state write")
}
func (*viewFixtureRepo) UpdateConfig(context.Context, int64, string, string) error {
	return errors.New("unexpected fixture config write")
}
func (*viewFixtureRepo) UpdateBindingsAndState(context.Context, int64, []PluginBinding, string, string, *time.Time, string, string) error {
	return errors.New("unexpected fixture bindings write")
}
func (r *viewFixtureRepo) HoldObservedPluginRuntime(_ context.Context, installation *PluginInstallation) (PluginRuntimeLease, error) {
	r.held = append(r.held, installation.ID)
	return &viewFixtureLease{done: make(chan struct{}), release: func() { r.released = append(r.released, installation.ID) }}, nil
}

type viewFixtureLease struct {
	done    chan struct{}
	once    sync.Once
	release func()
}

func (l *viewFixtureLease) Done() <-chan struct{} { return l.done }
func (*viewFixtureLease) Err() error              { return nil }
func (l *viewFixtureLease) Release()              { l.once.Do(func() { l.release(); close(l.done) }) }

type viewFixtureDirectory struct {
	accounts map[int64]*Account
	reads    int
}

func (d *viewFixtureDirectory) ReadAccountViewAccount(_ context.Context, id int64) (*Account, error) {
	d.reads++
	if a := d.accounts[id]; a != nil {
		copy := *a
		return &copy, nil
	}
	return nil, ErrAccountNotFound
}
func (d *viewFixtureDirectory) ReadExtensionAccount(ctx context.Context, id int64) (*extensionv1.Account, error) {
	account, err := d.ReadAccountViewAccount(ctx, id)
	return extensionAccount(account), err
}
func (*viewFixtureDirectory) ListExtensionAccounts(context.Context, extensionv1.AccountQuery) ([]extensionv1.Account, error) {
	return nil, errors.New("unexpected unscoped list")
}
func (*viewFixtureDirectory) ResolveExtensionIdentity(context.Context, extensionv1.AccountQuery) (*extensionv1.OutboundIdentity, error) {
	return nil, errors.New("credential API must not be called")
}
func (*viewFixtureDirectory) ListPluginAccounts(context.Context, PluginAccountScope, string, string) ([]PluginAccountInfo, error) {
	return nil, errors.New("unexpected plugin account list")
}
func (*viewFixtureDirectory) ResolvePluginOutboundIdentity(context.Context, PluginAccountScope, int64) (*PluginOutboundIdentity, error) {
	return nil, errors.New("credential API must not be called")
}

func viewFixtureContribution() extensionv1.Contribution {
	yes := true
	return extensionv1.Contribution{ID: CindyAccountViewID, Slot: extensionv1.AccountViewSlot, Permission: "admin", Capability: extensionv1.CapabilityProvider, Label: map[string]string{"en": "Cindy"}, AccountView: &extensionv1.AccountViewDefinitionV1{
		Version: 1, Source: extensionv1.AccountConsoleSource, BaseQuery: extensionv1.AccountViewPredicate{CindyOnly: &yes}, DefaultPreset: "cindy",
		Presets: []extensionv1.AccountViewPreset{
			{ID: "cindy", Label: map[string]string{"en": "Cindy"}, Counter: "cindy_total"},
			{ID: "insufficient", Label: map[string]string{"en": "Insufficient"}, Query: extensionv1.AccountViewPredicate{CindyBalanceStatus: "insufficient"}, Counter: "cindy_insufficient_count"},
			{ID: "banned", Label: map[string]string{"en": "Banned"}, Query: extensionv1.AccountViewPredicate{CindyHealthStatus: "banned"}, Counter: "cindy_banned_count"},
		}, Layout: []extensionv1.AccountViewLayout{{Kind: "account_table"}}, Table: extensionv1.AccountViewTable{Layouts: []string{"table", "cards", "compact"}, ColumnRefs: []string{}, ActionRefs: []string{}},
		LegacyQueryAliases: []extensionv1.AccountViewLegacyAlias{{Match: map[string]string{"cindy_only": "true"}, Preset: "cindy", Priority: 10}},
	}}
}

func newViewFixture(t *testing.T) (*PluginManager, *viewFixtureRepo, *viewFixtureDirectory, extensionv1.AccountViewContextV1) {
	t.Helper()
	view := viewFixtureContribution()
	grants := []extensionv1.ResourceGrant{{Name: "cindy.probe.create", Capability: extensionv1.CapabilityProvider, Permission: "admin"}}
	for _, name := range []string{"cindy.cleanup.insufficient.preview", "cindy.cleanup.insufficient.submit", "cindy.cleanup.banned.preview", "cindy.cleanup.banned.submit", "cindy.balance.recover"} {
		grants = append(grants, extensionv1.ResourceGrant{Name: name, Capability: extensionv1.CapabilityProvider, Permission: "admin"})
	}
	owner := &PluginInstallation{ID: 1, PluginKey: CindyAccountViewPluginKey, State: PluginStateEnabled, RuntimeGeneration: 3, Revision: 7, PackageSHA256: strings.Repeat("a", 64), Manifest: PluginManifest{ID: CindyAccountViewPluginKey, Contributions: []extensionv1.Contribution{view}, Resources: grants, UI: PluginUIManifest{Entrypoint: "ui/index.html"}, Capabilities: []PluginCapability{{ID: extensionv1.CapabilityProvider, Platform: PlatformCindy, AccountType: AccountTypeAPIKey}, {ID: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*"}}}, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: PlatformCindy, AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: 100}, {Capability: extensionv1.CapabilityAdmin, Platform: PlatformCindy, AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: 100}}}
	tool := &PluginInstallation{ID: 2, PluginKey: "codexrip.account-tools", State: PluginStateEnabled, RuntimeGeneration: 4, Revision: 8, PackageSHA256: strings.Repeat("b", 64), Manifest: PluginManifest{ID: "codexrip.account-tools", Resources: []extensionv1.ResourceGrant{{Name: "tests.submit", Capability: extensionv1.CapabilityAdmin, Permission: "admin"}}}, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}}
	repo := &viewFixtureRepo{installations: map[int64]*PluginInstallation{1: owner, 2: tool}}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	registry := &pluginExtensionRegistry{installations: map[int64]*PluginInstallation{}, runtimes: map[int64]*pluginRuntime{}}
	for id, item := range repo.installations {
		registry.installations[id] = cloneHostIOInstallation(item)
		registry.runtimes[id] = &pluginRuntime{installation: cloneHostIOInstallation(item), client: hcplugin.NewClient(&hcplugin.ClientConfig{}), done: make(chan struct{})}
	}
	manager.extensions.Store(registry)
	now := time.Now().UTC()
	directory := &viewFixtureDirectory{accounts: map[int64]*Account{}}
	for _, id := range []int64{2, 3, 4, 5} {
		directory.accounts[id] = &Account{ID: id, Name: "fixture", Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, UpdatedAt: now, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "synthetic-private-key"}, Extra: map[string]any{"private_secret": "synthetic-extra"}}
	}
	directory.accounts[3].CindyBalanceInsufficientAt = &now
	directory.accounts[4].CindyBannedAt = &now
	directory.accounts[5].Platform = PlatformOpenAI
	manager.accountDirectory = directory
	request := extensionv1.AccountViewContextV1{AccountViewIdentityV1: extensionv1.AccountViewIdentityV1{Version: 1, PluginID: 1, PluginKey: owner.PluginKey, PackageSHA256: owner.PackageSHA256, ViewID: view.ID, PresetID: "cindy", ViewDefinitionDigest: AccountViewDefinitionDigest(&view)}}
	return manager, repo, directory, request
}

func applyViewFixture(t *testing.T, manager *PluginManager, repo *viewFixtureRepo) {
	t.Helper()
	for id, current := range repo.installations {
		manager.extensions.Load().installations[id] = cloneHostIOInstallation(current)
		manager.extensions.Load().runtimes[id].installation = cloneHostIOInstallation(current)
	}
}

func TestAccountViewManifestContract(t *testing.T) {
	_, repo, _, _ := newViewFixture(t)
	baseline := repo.installations[1].Manifest
	require.NoError(t, validateAccountViewContributions(baseline))
	for name, mutate := range map[string]func(*PluginManifest){
		"cross-owner ref": func(m *PluginManifest) {
			m.Contributions[0].AccountView.Table.ActionRefs = []string{"other-owner/recover"}
		},
		"core refs without opt in": func(m *PluginManifest) { m.Contributions[0].AccountView.CoreFilterSurfaceRefs = []string{"controls"} },
		"unknown source":           func(m *PluginManifest) { m.Contributions[0].AccountView.Source = "raw.sql" },
		"two tables": func(m *PluginManifest) {
			m.Contributions[0].AccountView.Layout = append(m.Contributions[0].AccountView.Layout, extensionv1.AccountViewLayout{Kind: "account_table"})
		},
		"preset collision": func(m *PluginManifest) { m.Contributions[0].AccountView.Presets[1].ID = "cindy" },
		"alias owner":      func(m *PluginManifest) { m.ID = "other.owner" },
		"admin absent":     func(m *PluginManifest) { m.Capabilities = m.Capabilities[:1] },
		"raw display source": func(m *PluginManifest) {
			m.Contributions = append(m.Contributions, extensionv1.Contribution{ID: "column", Slot: "account.columns", Permission: "admin", DisplayFields: []extensionv1.DisplayField{{Key: "value", Kind: "text"}}, ValueBindings: map[string]string{"value": "credentials.api_key"}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(baseline)
			var copy PluginManifest
			require.NoError(t, json.Unmarshal(raw, &copy))
			mutate(&copy)
			require.Error(t, validateAccountViewContributions(copy))
		})
	}
	for _, raw := range []string{`{"version":1,"source":"accounts.console.v1","base_query":{"sql":"SELECT 1"},"default_preset":"cindy","presets":[],"layout":[],"table":{}}`, `{"version":1,"source":"accounts.console.v1","default_preset":"cindy","presets":[],"layout":[],"table":{}}`} {
		var view extensionv1.AccountViewDefinitionV1
		require.Error(t, json.Unmarshal([]byte(raw), &view))
	}
}

func TestAccountViewCindySourceDeclaration(t *testing.T) {
	raw, err := os.ReadFile("../../../plugins/cindy-provider/manifest.source.json")
	require.NoError(t, err)
	var manifest PluginManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.NoError(t, validateAccountViewContributions(manifest))
	found := false
	for _, contribution := range manifest.Contributions {
		if contribution.Slot == extensionv1.AccountViewSlot {
			found = true
			require.Equal(t, CindyAccountViewID, contribution.ID)
			require.Contains(t, contribution.AccountView.CoreFilterSurfaceRefs, "cindy-account-controls")
		}
	}
	require.True(t, found)
}

func TestAccountViewOwnerQualifiedResolution(t *testing.T) {
	manager, repo, _, request := newViewFixture(t)
	ctx, release, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer release()
	_, bound := AccountViewFromContext(ctx)
	require.True(t, bound)
	_, primary := PluginExecutionFromContext(ctx)
	require.False(t, primary, "view binding must not seize a later tool's owner")
	wrong := request
	wrong.PluginKey = "other.owner"
	_, _, err = manager.BindAccountViewRequest(context.Background(), wrong)
	require.Error(t, err)
	wrong = request
	wrong.PackageSHA256 = strings.Repeat("c", 64)
	_, _, err = manager.BindAccountViewRequest(context.Background(), wrong)
	require.Error(t, err)
	duplicate := cloneHostIOInstallation(repo.installations[1])
	duplicate.ID = 9
	manager.extensions.Load().installations[9] = duplicate
	_, _, err = manager.BindAccountViewRequest(context.Background(), request)
	require.Error(t, err)
}

type viewConsoleRepo struct {
	AccountRepository
	accounts map[int64]*Account
}

func (r *viewConsoleRepo) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	var out []*Account
	for _, id := range ids {
		if account := r.accounts[id]; account != nil {
			copy := *account
			out = append(out, &copy)
		}
	}
	return out, nil
}

func expectViewConsoleRead(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT .*"accounts"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(5).AddRow(3).AddRow(2))
	mock.ExpectQuery(`SELECT .*"accounts"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

func TestAccountViewScopeBeforeAggregation(t *testing.T) {
	manager, repo, directory, request := newViewFixture(t)
	repo.installations[1].Bindings[1].RolloutPercent = 50
	applyViewFixture(t, manager, repo)
	ctx, release, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer release()
	require.NoError(t, ValidateAccountViewTargets(ctx, []int64{2}))
	require.Error(t, ValidateAccountViewTargets(ctx, []int64{2, 3}))
	require.Error(t, ValidateAccountViewTargets(ctx, []int64{-1}))
	require.Error(t, ValidateAccountViewPredicateQuery(ctx, map[string]string{"cindy_only": "false"}))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	admin := &adminServiceImpl{entClient: client, accountRepo: &viewConsoleRepo{accounts: directory.accounts}}
	expectViewConsoleRead(mock)
	items, total, err := admin.ListAccountsConsole(ctx, 1, 1, AccountConsoleFilters{})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.EqualValues(t, 2, items[0].ID)
	// Native core uses the same host source without consulting a view runtime.
	expectViewConsoleRead(mock)
	_, coreTotal, err := admin.ListAccountsConsole(context.Background(), 1, 1, AccountConsoleFilters{})
	require.NoError(t, err)
	require.EqualValues(t, 3, coreTotal)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountViewFacetRelaxationAndPresetCounters(t *testing.T) {
	manager, _, directory, request := newViewFixture(t)
	request.PresetID = "insufficient"
	ctx, release, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer release()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	admin := &adminServiceImpl{entClient: client, accountRepo: &viewConsoleRepo{accounts: directory.accounts}}
	for range 4 {
		expectViewConsoleRead(mock)
	}
	mock.ExpectQuery(`SELECT .*"account_folders"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`SELECT .*"account_tags"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	facets, err := admin.GetAccountConsoleFacets(ctx, AccountConsoleFilters{})
	require.NoError(t, err)
	require.Equal(t, 1, facets.Total, "facet relaxation must retain the active insufficient preset")
	require.Equal(t, map[string]int{"cindy": 2, "insufficient": 1, "banned": 0}, facets.ViewPresetCounts, "each preset counter evaluates over base+scope, not the active preset")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountViewDualOwnerLifetimeAndUICapture(t *testing.T) {
	manager, repo, _, request := newViewFixture(t)
	request.Query = extensionv1.AccountViewQueryV1{Search: "private-search", AccountIDs: []int64{2}}
	viewCtx, releaseView, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	ctx, releaseTool, err := manager.BindResourceContext(viewCtx, 2, repo.installations[2].PackageSHA256, repo.installations[2].Manifest.Resources[0])
	require.NoError(t, err)
	primary, _ := PluginExecutionFromContext(ctx)
	origin, _, _ := AccountViewExecutionFromContext(ctx)
	require.EqualValues(t, 2, primary.ID)
	require.EqualValues(t, 1, origin.ID)
	require.Error(t, ValidateAccountViewTargets(ctx, []int64{3}), "selection snapshot cannot be enlarged by a resource message")
	fences, err := PluginExecutionFences(ctx, repo.installations[2].PluginKey)
	require.NoError(t, err)
	require.EqualValues(t, 1, fences[0].ID)
	require.EqualValues(t, 2, fences[1].ID)
	session, err := manager.CreateUIAssetSession(WithPluginUIActor(ctx, 42), 1, "", "admin", time.Minute)
	require.NoError(t, err)
	require.Equal(t, request.AccountViewIdentityV1, *session.ViewContext)
	require.NoError(t, manager.ValidateUIRequestBinding(ctx, session.Token, 1, 42, &request.AccountViewIdentityV1))
	require.Error(t, manager.ValidateUIRequestBinding(ctx, session.Token, 1, 43, &request.AccountViewIdentityV1))
	require.Error(t, manager.ValidateUIRequestBinding(ctx, session.Token, 1, 42, nil))
	claims, err := manager.parseUIAssetToken(session.Token)
	require.NoError(t, err)
	raw, _ := json.Marshal(claims)
	require.NotContains(t, string(raw), "private-search")
	require.NotContains(t, string(raw), "synthetic-private-key")
	manager.extensions.Load().runtimes[1].beginDrain()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, []int64{1, 2}, repo.held)
	releaseTool()
	releaseView()
	require.ElementsMatch(t, []int64{1, 2}, repo.released)
}

type viewDuplicateInventoryRepo struct {
	AccountRepository
	accounts []Account
}

func (r *viewDuplicateInventoryRepo) ListByPlatform(_ context.Context, platform string) ([]Account, error) {
	if platform != PlatformCindy {
		return nil, errors.New("unexpected duplicate inventory platform")
	}
	return append([]Account(nil), r.accounts...), nil
}

func TestAccountViewDuplicateInventoryFiltersBeforeAggregation(t *testing.T) {
	manager, plugins, directory, request := newViewFixture(t)
	plugins.installations[1].Bindings[1].RolloutPercent = 50
	applyViewFixture(t, manager, plugins)
	request.PresetID = "insufficient"
	ctx, release, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer release()
	now := time.Now().UTC()
	first, second := *directory.accounts[2], *directory.accounts[2]
	first.CindyBalanceInsufficientAt, second.CindyBalanceInsufficientAt = &now, &now
	second.ID = 6 // Both 2 and 6 are enrolled; account 3 is outside Admin50.
	wrongBase := first
	wrongBase.ID, wrongBase.WirePlatform = 5, "not-openai"
	repo := &viewDuplicateInventoryRepo{accounts: []Account{*directory.accounts[3], *directory.accounts[4], wrongBase, first, second}}
	admin := &adminServiceImpl{accountRepo: repo}
	fixture := &capturedCindyManagement{}
	previous := processExtensionOperations.Load()
	processExtensionOperations.Store(&extensionOperationProvider{invoker: fixture})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })

	groups, err := admin.BuildCindyDuplicateIdentityInventory(ctx)
	require.NoError(t, err)
	require.Len(t, fixture.payloads, 1)
	var candidates []extensionv1.CindyDuplicateCandidate
	require.NoError(t, json.Unmarshal([]byte(fixture.payloads[0]), &candidates))
	var ids []int64
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	require.ElementsMatch(t, []int64{2, 6}, ids, "base, preset and bucket must apply before aggregate policy sees account facts")
	require.Len(t, groups, 1)
	require.ElementsMatch(t, []int64{2, 6}, groups[0].OtherAccountIDs)
	require.Zero(t, groups[0].ProposedOwnerID, "the existing policy keeps exhausted members terminal")
	require.NotContains(t, fixture.payloads[0], "synthetic-private-key")

	_, err = admin.BuildCindyDuplicateIdentityInventory(context.Background())
	require.NoError(t, err)
	require.Len(t, fixture.payloads, 2)
	require.NoError(t, json.Unmarshal([]byte(fixture.payloads[1]), &candidates))
	require.Len(t, candidates, len(repo.accounts), "no-view inventory keeps the original candidate semantics")
}

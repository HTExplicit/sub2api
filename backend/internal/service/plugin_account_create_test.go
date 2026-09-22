//go:build unit

package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type accountCreateFixtureInvoker struct {
	promptPolicyFixture
	manager *PluginManager
}

func (f *accountCreateFixtureInvoker) BindAccountCreate(ctx context.Context, platform, kind, profile string, request *extensionv1.ProviderCreateRequestV1) (context.Context, func(), error) {
	return f.manager.BindAccountCreate(ctx, platform, kind, profile, request)
}

func newAccountCreateFixture(t *testing.T) (*PluginManager, *viewFixtureRepo, *extensionv1.ProviderCreateRequestV1) {
	t.Helper()
	manager, repo, _, _ := newViewFixture(t)
	raw, err := os.ReadFile("../../../plugins/cindy-provider/manifest.source.json")
	require.NoError(t, err)
	var manifest PluginManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	var create *extensionv1.Contribution
	for _, contribution := range manifest.Contributions {
		if contribution.Slot == extensionv1.AccountCreateSlot {
			copy := contribution
			create = &copy
		}
	}
	require.NotNil(t, create)
	repo.installations[1].Manifest.Contributions = append(repo.installations[1].Manifest.Contributions, *create)
	applyViewFixture(t, manager, repo)
	request := &extensionv1.ProviderCreateRequestV1{ContributionID: create.ID,
		ExpectedPackageSHA256: repo.installations[1].PackageSHA256, ExpectedDefinitionSHA256: AccountCreateDefinitionDigest(create),
		ExpectedRuntimeGeneration: repo.installations[1].RuntimeGeneration, Values: map[string]string{}, InheritDefaults: []string{}}
	return manager, repo, request
}

func useAccountCreateFixture(t *testing.T) (*PluginManager, *viewFixtureRepo, *extensionv1.ProviderCreateRequestV1) {
	t.Helper()
	manager, repo, request := newAccountCreateFixture(t)
	previous := processExtensionOperations.Load()
	processExtensionOperations.Store(&extensionOperationProvider{invoker: &accountCreateFixtureInvoker{manager: manager}})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	return manager, repo, request
}

type accountCreateGroupFixture struct {
	GroupRepository
	groups       []Group
	defaultReads int
}

func (f *accountCreateGroupFixture) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	f.defaultReads++
	return f.groups, nil
}

func (f *accountCreateGroupFixture) GetByID(_ context.Context, id int64) (*Group, error) {
	for _, group := range f.groups {
		if group.ID == id {
			return &group, nil
		}
	}
	return nil, ErrGroupNotFound
}

func accountCreateGroups() *accountCreateGroupFixture {
	return &accountCreateGroupFixture{groups: []Group{{ID: 91, Name: "cindy-default", Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Status: StatusActive}}}
}

func TestAccountCreateDeclarationAndNullableDefaults(t *testing.T) {
	_, repo, request := newAccountCreateFixture(t)
	baseline := repo.installations[1].Manifest
	require.NoError(t, validateAccountCreateContributions(baseline))
	create := accountCreateContribution(repo.installations[1], request.ContributionID)
	require.Nil(t, create.AccountCreate.Defaults.LoadFactor)
	for _, mutate := range []func(*PluginManifest){
		func(m *PluginManifest) { m.ID = "other.owner" },
		func(m *PluginManifest) {
			m.Contributions[1].AccountCreate.CredentialUI.BaseURL = "https://other.invalid"
		},
		func(m *PluginManifest) {
			m.Contributions[1].AccountCreate.FieldBindings["device_id"] = "credentials.api_key"
		},
		func(m *PluginManifest) { m.Contributions[1].AccountCreate.MinimumEffectiveGroups = 0 },
		func(m *PluginManifest) { m.Contributions[1].AccountCreate.Defaults.LoadFactor = ptrAccountCreateInt(0) },
		func(m *PluginManifest) { m.Contributions = append(m.Contributions, m.Contributions[1]) },
	} {
		raw, err := json.Marshal(baseline)
		require.NoError(t, err)
		var changed PluginManifest
		require.NoError(t, json.Unmarshal(raw, &changed))
		mutate(&changed)
		require.Error(t, validateAccountCreateContributions(changed))
	}
	raw, err := json.Marshal(create.AccountCreate)
	require.NoError(t, err)
	var decoded extensionv1.AccountCreateDefinitionV1
	require.NoError(t, json.Unmarshal(raw, &decoded), "load_factor:null is part of the contract")
	for _, invalid := range []string{
		strings.Replace(string(raw), `"priority":50`, `"priority":null`, 1),
		strings.Replace(string(raw), `"version":1`, `"version":1,"callback":"arbitrary"`, 1),
	} {
		require.Error(t, json.Unmarshal([]byte(invalid), &decoded))
	}
}

func ptrAccountCreateInt(value int) *int { return &value }

func TestAccountCreateIDlessScopeAndFreshPreconditions(t *testing.T) {
	for _, percentages := range [][2]int{{100, 100}, {99, 100}, {100, 99}, {0, 100}} {
		manager, repo, request := newAccountCreateFixture(t)
		repo.installations[1].Bindings[0].RolloutPercent = percentages[0]
		repo.installations[1].Bindings[1].RolloutPercent = percentages[1]
		applyViewFixture(t, manager, repo)
		_, release, err := manager.BindAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, request)
		if percentages == [2]int{100, 100} {
			require.NoError(t, err)
			release()
		} else {
			require.ErrorIs(t, err, ErrAccountCreateUnavailable)
		}
		require.Zero(t, manager.accountDirectory.(*viewFixtureDirectory).reads, "IDless admission never invents an account bucket")
	}
	manager, repo, request := newAccountCreateFixture(t)
	for _, mutate := range []func(*extensionv1.ProviderCreateRequestV1){
		func(r *extensionv1.ProviderCreateRequestV1) { r.ExpectedPackageSHA256 = strings.Repeat("d", 64) },
		func(r *extensionv1.ProviderCreateRequestV1) { r.ExpectedDefinitionSHA256 = strings.Repeat("e", 64) },
		func(r *extensionv1.ProviderCreateRequestV1) { r.ExpectedRuntimeGeneration++ },
		func(r *extensionv1.ProviderCreateRequestV1) { r.ContributionID = "other-create" },
	} {
		copy := *request
		mutate(&copy)
		_, _, err := manager.BindAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, &copy)
		require.ErrorIs(t, err, ErrAccountCreateUnavailable)
	}
	repo.installations[1].ConfigEncrypted = "changed-but-not-applied"
	_, _, err := manager.BindAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, nil)
	require.ErrorIs(t, err, ErrAccountCreateUnavailable, "legacy omission cannot use a stale applied config")
}

func TestAccountCreateCentralDefaultsGroupsAndExplicitAuto(t *testing.T) {
	_, plugins, request := useAccountCreateFixture(t)
	accounts := &accountRepoStubForBulkUpdate{createID: 71}
	groups := accountCreateGroups()
	svc := &adminServiceImpl{accountRepo: accounts, groupRepo: groups, cindyAccountMutations: &recordingAdminCindyMutationRunner{}}
	input := &CreateAccountInput{Name: "synthetic", Platform: PlatformCindy, Type: AccountTypeAPIKey,
		Credentials: cindyCredentials(), SkipMixedChannelCheck: true}
	legacy, err := svc.CreateAccount(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, []int64{91}, accounts.bindGroupsByAccount[71])
	require.Equal(t, 1, groups.defaultReads, "minimum is checked after the server fallback")
	require.Zero(t, legacy.Concurrency)
	require.Zero(t, legacy.Priority)
	require.Nil(t, legacy.RateMultiplier, "legacy untagged numeric semantics remain unchanged")
	require.Equal(t, "force_responses", legacy.Extra[CindyResponsesModeExtraKey])
	request.InheritDefaults = append([]string(nil), accountCreateDefaultTargets...)
	input.ProviderCreate = request
	input.ExplicitCreateFields = map[string]bool{}
	inherited, err := svc.CreateAccount(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 3, inherited.Concurrency)
	require.Equal(t, 50, inherited.Priority)
	require.Equal(t, 1.0, *inherited.RateMultiplier)
	require.Nil(t, inherited.LoadFactor)
	zero := 0.0
	input.RateMultiplier = &zero
	input.ExplicitCreateFields = map[string]bool{"concurrency": true, "priority": true, "rate_multiplier": true, "load_factor": true}
	input.Extra = map[string]any{CindyResponsesModeExtraKey: "auto", CindyDeviceIDSourceExtraKey: "registration-record"}
	explicit, err := svc.CreateAccount(context.Background(), input)
	require.NoError(t, err)
	require.Zero(t, explicit.Concurrency)
	require.Zero(t, explicit.Priority)
	require.Zero(t, *explicit.RateMultiplier)
	require.Nil(t, explicit.LoadFactor)
	require.Equal(t, "auto", explicit.Extra[CindyResponsesModeExtraKey])
	require.Equal(t, "generated-production-v1", explicit.Extra[CindyDeviceIDSourceExtraKey])
	accounts.createAccount = nil
	groups.groups = nil
	_, err = svc.CreateAccount(context.Background(), input)
	require.ErrorIs(t, err, ErrAccountCreateGroupsRequired)
	require.Nil(t, accounts.createAccount)
	plugins.installations[1].State = PluginStateDisabled
	_, err = svc.CreateAccount(context.Background(), &CreateAccountInput{Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: cindyCredentials(), GroupIDs: []int64{91}})
	require.ErrorIs(t, err, ErrAccountCreateUnavailable, "omitting provider_create cannot bypass disable")
}

func TestAccountCreateDeclaredInputsStayInHost(t *testing.T) {
	manager, _, request := newAccountCreateFixture(t)
	request.Values["device_id"] = strings.Repeat("a", 64)
	ctx, release, err := manager.BindAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, request)
	require.NoError(t, err)
	defer release()
	input := &CreateAccountInput{Credentials: cindyCredentials(), Extra: map[string]any{CindyDeviceIDSourceExtraKey: "registration-record"}}
	require.NoError(t, applyAccountCreateProfile(ctx, input))
	extra, err := normalizeCindyDeviceIdentityForCreate(PlatformCindy, AccountTypeAPIKey, input.Credentials, input.Extra)
	require.NoError(t, err)
	require.Equal(t, request.Values["device_id"], extra[CindyDeviceIDExtraKey])
	require.Equal(t, "input-preserved", extra[CindyDeviceIDSourceExtraKey])
	input.Extra = map[string]any{CindyDeviceIDExtraKey: strings.Repeat("b", 64)}
	require.ErrorIs(t, applyAccountCreateProfile(ctx, input), ErrAccountCreateInvalid)
	request.Values = map[string]string{"credentials": "not allowed"}
	_, _, err = manager.BindAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, request)
	require.ErrorIs(t, err, ErrAccountCreateInvalid)
}

func TestAccountCreateLegacyImportMappingIsPreserved(t *testing.T) {
	useAccountCreateFixture(t)
	accounts := &accountRepoStubForBulkUpdate{createID: 83}
	svc := &adminServiceImpl{accountRepo: accounts, groupRepo: accountCreateGroups(), cindyAccountMutations: &recordingAdminCindyMutationRunner{}}
	mapping := map[string]any{"client-alias": "provider/model-one", "custom-route": "upstream-model-two"}
	credentials := cindyCredentials()
	credentials["model_mapping"] = mapping
	// Data import passes the ordinary credential map through this central input
	// without provider_create; a hidden UI editor is not a storage prohibition.
	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "legacy-import", Platform: PlatformCindy, Type: AccountTypeAPIKey,
		Credentials: credentials, GroupIDs: []int64{91}, SkipMixedChannelCheck: true, SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.Equal(t, mapping, account.Credentials["model_mapping"])
	require.Equal(t, mapping, accounts.createAccount.Credentials["model_mapping"])
	require.Equal(t, mapping, credentials["model_mapping"], "normalization must not rewrite imported mapping values")
}

func TestAccountCreateLegacyProvenanceRejectsInvalidSource(t *testing.T) {
	useAccountCreateFixture(t)
	accounts := &accountRepoStubForBulkUpdate{createID: 84}
	svc := &adminServiceImpl{accountRepo: accounts, groupRepo: accountCreateGroups(), cindyAccountMutations: &recordingAdminCindyMutationRunner{}}
	for _, source := range []any{nil, 7, false, "unknown-provenance"} {
		_, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
			Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: cindyCredentials(),
			Extra: map[string]any{CindyDeviceIDSourceExtraKey: source}, GroupIDs: []int64{91}, SkipMixedChannelCheck: true,
		})
		requireApplicationErrorReason(t, err, "CINDY_DEVICE_ID_SOURCE_INVALID")
		require.Nil(t, accounts.createAccount)
	}
	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: cindyCredentials(),
		Extra:    map[string]any{CindyDeviceIDExtraKey: strings.Repeat("f", 64), CindyDeviceIDSourceExtraKey: "registration-record"},
		GroupIDs: []int64{91}, SkipMixedChannelCheck: true,
	})
	require.NoError(t, err)
	require.Equal(t, "input-preserved", account.Extra[CindyDeviceIDSourceExtraKey], "valid legacy input is checked but actual provenance is still host-derived")
}

func TestAccountCreateOrdinaryOpenAILaxaRemainsIndependent(t *testing.T) {
	previous := processExtensionOperations.Load()
	processExtensionOperations.Store(nil)
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	accounts := &accountRepoStubForBulkUpdate{createID: 81}
	svc := &adminServiceImpl{accountRepo: accounts}
	input := &CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: cindyCredentials(), SkipDefaultGroupBind: true}
	account, err := svc.CreateAccount(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, PlatformOpenAI, account.Platform)
	require.NotEqual(t, ProviderProfileCindyLaxaV1, account.ProviderProfile)
	require.NotContains(t, account.Extra, CindyResponsesModeExtraKey)
	require.NotContains(t, account.Extra, CindyDeviceIDExtraKey)
	input.Extra = map[string]any{CindyResponsesModeExtraKey: "force_chat_completions"}
	account, err = svc.CreateAccount(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, "force_chat_completions", account.Extra[CindyResponsesModeExtraKey])
}

func TestAccountCreateOriginContextDoesNotCaptureOrdinaryAccount(t *testing.T) {
	manager, _, request := newAccountCreateFixture(t)
	ctx, release, err := manager.BindAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, request)
	require.NoError(t, err)
	defer release()
	svc := &adminServiceImpl{accountRepo: &accountRepoStubForBulkUpdate{createID: 82}}
	account, err := svc.CreateAccount(ctx, &CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: cindyCredentials(), SkipDefaultGroupBind: true})
	require.NoError(t, err, "an inherited host context must not impose another profile's minimum groups on ordinary OpenAI")
	require.NotContains(t, account.Extra, CindyResponsesModeExtraKey)
	require.NotContains(t, account.Extra, CindyDeviceIDExtraKey)
}

func TestAccountCreateTransactionOwnsLeaseAndCommitAdmission(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		manager, plugins, request := newAccountCreateFixture(t)
		ctx, release, err := manager.BindAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, request)
		require.NoError(t, err)
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
		mock.ExpectBegin()
		tx, err := client.Tx(context.Background())
		require.NoError(t, err)
		retainAccountCreateUntilTransactionEnds(ctx, tx, release)
		require.Empty(t, plugins.released, "returning from an inner create does not finish its outer transaction")
		if canceled {
			manager.extensions.Load().runtimes[1].beginDrain()
			require.ErrorIs(t, tx.Commit(), ErrAccountCreateUnavailable)
			require.Empty(t, plugins.released, "rejected commit retains the lease until rollback finishes")
			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
		} else {
			mock.ExpectCommit()
			require.NoError(t, tx.Commit())
		}
		require.Equal(t, []int64{1}, plugins.released)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestAccountCreateProjectionAndSameOwnerFenceIntersection(t *testing.T) {
	manager, plugins, request := newAccountCreateFixture(t)
	var projected *PluginContribution
	for _, contribution := range manager.Contributions() {
		if contribution.Slot == extensionv1.AccountCreateSlot {
			copy := contribution
			projected = &copy
		}
	}
	require.NotNil(t, projected)
	require.True(t, projected.Available)
	require.Equal(t, request.ExpectedDefinitionSHA256, projected.CreateDefinitionDigest)
	require.Equal(t, request.ExpectedRuntimeGeneration, projected.RuntimeGeneration)
	raw, err := json.Marshal(projected)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"credentials"`)
	require.NotContains(t, string(raw), `"extra"`)
	require.NotContains(t, string(raw), "test-key")
	view := plugins.installations[1].Manifest.Contributions[0]
	viewCtx, viewRelease, err := manager.BindAccountViewRequest(context.Background(), extensionv1.AccountViewContextV1{AccountViewIdentityV1: extensionv1.AccountViewIdentityV1{
		Version: 1, PluginID: 1, PluginKey: CindyAccountViewPluginKey, PackageSHA256: request.ExpectedPackageSHA256,
		ViewID: view.ID, PresetID: "cindy", ViewDefinitionDigest: AccountViewDefinitionDigest(&view),
	}})
	require.NoError(t, err)
	defer viewRelease()
	ctx, release, err := manager.BindAccountCreate(WithPluginExecution(viewCtx, plugins.installations[2]), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, request)
	require.NoError(t, err)
	defer release()
	primary, _ := PluginExecutionFromContext(ctx)
	require.EqualValues(t, 2, primary.ID)
	fences, err := PluginExecutionFences(ctx, "")
	require.NoError(t, err)
	require.Len(t, fences, 2)
	require.True(t, fences[0].OriginView && fences[0].OriginCreate)
	require.EqualValues(t, 1, fences[0].ID)
	require.True(t, fences[1].Primary)
	create, _ := AccountCreateFromContext(ctx)
	create.installation.Revision++
	_, err = PluginExecutionFences(ctx, "")
	require.Error(t, err, "two policies for one installation cannot overwrite conflicting revisions")
}

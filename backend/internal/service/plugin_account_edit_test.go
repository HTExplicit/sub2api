//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type accountEditFixtureInvoker struct {
	*PluginManager
	catalog cindyIndependentSnapshotFixture
	owner   int64
}

func (f *accountEditFixtureInvoker) InvokeOperation(ctx context.Context, platform, kind string, input extensionv1.Invocation) (extensionv1.Result, error) {
	if input.Operation != "cindy.catalog" {
		return promptPolicyFixture{}.InvokeOperation(ctx, platform, kind, input)
	}
	result, err := f.catalog.InvokeOperation(ctx, platform, kind, input)
	result.PluginID = f.owner
	return result, err
}

func (f *accountEditFixtureInvoker) InvokeCachedOperation(ctx context.Context, platform, kind string, input extensionv1.Invocation) (extensionv1.Result, error) {
	return f.InvokeOperation(ctx, platform, kind, input)
}

func newAccountEditFixture(t *testing.T) (*accountEditFixtureInvoker, *viewFixtureRepo, *Account, *extensionv1.ProviderEditRequestV1, extensionv1.AccountViewContextV1) {
	t.Helper()
	manager, repo, directory, view := newViewFixture(t)
	raw, err := os.ReadFile("../../../plugins/cindy-provider/manifest.source.json")
	require.NoError(t, err)
	var manifest PluginManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	var edit extensionv1.Contribution
	for _, contribution := range manifest.Contributions {
		if contribution.Slot == extensionv1.AccountEditSlot {
			edit = contribution
		}
	}
	require.NotNil(t, edit.AccountEdit)
	repo.installations[1].Manifest.Contributions = append(repo.installations[1].Manifest.Contributions, edit)
	applyViewFixture(t, manager, repo)
	account := directory.accounts[2]
	account.Extra[CindyDeviceIDExtraKey], account.Extra[CindyDeviceIDSourceExtraKey] = strings.Repeat("d", 64), "input-preserved"
	account.CindyCredentialGeneration = 6
	request := &extensionv1.ProviderEditRequestV1{ContributionID: edit.ID, ExpectedPackageSHA256: repo.installations[1].PackageSHA256,
		ExpectedDefinitionSHA256: AccountEditDefinitionDigest(&edit), ExpectedRuntimeGeneration: repo.installations[1].RuntimeGeneration,
		ExpectedStateSHA256: AccountEditStateDigest(account), Changes: map[string]extensionv1.AccountEditChangeV1{}}
	invoker := &accountEditFixtureInvoker{PluginManager: manager, catalog: cindyIndependentSnapshotFixture{snapshot: independentCindySnapshot(t, "edit", true)}, owner: 1}
	previous := processExtensionOperations.Load()
	processExtensionOperations.Store(&extensionOperationProvider{invoker: invoker})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	return invoker, repo, account, request, view
}

func editSet(value string) extensionv1.AccountEditChangeV1 {
	return extensionv1.AccountEditChangeV1{Op: "set", Value: &value}
}
func editClear() extensionv1.AccountEditChangeV1 { return extensionv1.AccountEditChangeV1{Op: "clear"} }

func TestAccountEditDeclarationAndStrictRequest(t *testing.T) {
	f, repo, _, request, _ := newAccountEditFixture(t)
	base := repo.installations[1].Manifest
	require.NoError(t, validateAccountEditContributions(base))
	for name, mutate := range map[string]func(*PluginManifest){
		"owner":     func(m *PluginManifest) { m.ID = "other.owner" },
		"endpoint":  func(m *PluginManifest) { m.Contributions[1].AccountEdit.CredentialUI.BaseURL = "https://other.invalid" },
		"ws_auto":   func(m *PluginManifest) { m.Contributions[1].AccountEdit.WireControls[2].Values = []string{"auto"} },
		"duplicate": func(m *PluginManifest) { m.Contributions = append(m.Contributions, m.Contributions[1]) },
		"retained":  func(m *PluginManifest) { m.Contributions[1].RetainedControls = true },
		"defaults":  func(m *PluginManifest) { m.Contributions[1].AccountCreate = &extensionv1.AccountCreateDefinitionV1{} },
		"admin":     func(m *PluginManifest) { m.Capabilities = m.Capabilities[:1] },
		"path":      func(m *PluginManifest) { m.Contributions[1].AccountEdit.WireControls[0].Target = "extra.api_key" },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var copy PluginManifest
			require.NoError(t, json.Unmarshal(raw, &copy))
			mutate(&copy)
			require.Error(t, validateAccountEditContributions(copy))
		})
	}
	raw, _ := json.Marshal(request)
	for _, invalid := range []string{
		strings.Replace(string(raw), `"changes":{}`, `"changes":{"credentials.api_key":{"op":"clear"}}`, 1),
		strings.Replace(string(raw), `"changes":{}`, `"changes":{"responses_websocket_mode":{"op":"set","value":"auto"}}`, 1),
		strings.Replace(string(raw), `"changes":{}`, `"changes":{"compact_mode":{"op":"clear","value":"auto"}}`, 1),
		strings.Replace(string(raw), `"changes":{}`, `"changes":{},"inherit_defaults":[]`, 1),
		strings.Replace(string(raw), `"changes":{}`, `"changes":{"compact_mode":{"op":"clear","path":"extra"}}`, 1),
		strings.Replace(string(raw), `"changes":{}`, `"changes":null`, 1),
	} {
		var copy extensionv1.ProviderEditRequestV1
		err := json.Unmarshal([]byte(invalid), &copy)
		if err == nil {
			err = validateAccountEditRequest(&copy)
		}
		require.Error(t, err)
	}
	for _, contribution := range f.Contributions() {
		if contribution.Slot == extensionv1.AccountEditSlot {
			require.Equal(t, request.ExpectedDefinitionSHA256, contribution.EditDefinitionDigest)
			require.Equal(t, request.ExpectedRuntimeGeneration, contribution.RuntimeGeneration)
			require.True(t, contribution.Available)
			require.Equal(t, []string{extensionv1.CapabilityProvider, extensionv1.CapabilityAdmin}, contributionRequiredCapabilities(&contribution.Contribution))
			return
		}
	}
	t.Fatal("missing edit projection")
}

func TestAccountEditCodecRetainsRawAndFiniteClear(t *testing.T) {
	_, _, account, request, _ := newAccountEditFixture(t)
	account.Extra["openai_responses_mode"] = nil
	account.Extra["openai_compact_mode"] = "unknown-retained"
	for _, mode := range []string{"shared", "dedicated"} {
		account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
		account.Extra["openai_apikey_responses_websockets_v2_enabled"] = false
		account.Extra["responses_websockets_v2_enabled"] = true
		account.Extra["openai_ws_enabled"] = true
		account.Extra["openai_ws_force_http"] = true
		account.Extra["openai_passthrough"] = true
		account.Extra["openai_oauth_responses_websockets_v2_enabled"] = true
		account.Extra["openai_compact_supported"] = false
		before := accountEditRawState(account)
		values, changed, err := accountEditDesired(account, nil, map[string]any{"unrelated": true}, nil, nil)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, before, values)
		request.Changes = map[string]extensionv1.AccountEditChangeV1{"responses_mode": editClear(), "compact_mode": editSet("auto"), "responses_websocket_mode": editClear()}
		values, changed, err = accountEditDesired(account, nil, nil, request, nil)
		require.NoError(t, err)
		require.True(t, changed)
		copy := *account
		applyAccountEditOwned(&copy, values)
		require.NotContains(t, copy.Extra, "openai_responses_mode")
		require.Equal(t, "auto", copy.Extra["openai_compact_mode"])
		for _, key := range accountEditExtraKeys[2:] {
			require.NotContains(t, copy.Extra, key)
		}
		for _, key := range []string{"openai_ws_force_http", "openai_passthrough", "openai_oauth_responses_websockets_v2_enabled", "openai_compact_supported"} {
			require.Equal(t, account.Extra[key], copy.Extra[key])
		}
		require.Equal(t, before, accountEditRawState(account), "codec may not mutate source")
	}
	request.Changes = map[string]extensionv1.AccountEditChangeV1{"responses_mode": editSet("auto"), "responses_websocket_mode": editSet("off")}
	values, _, err := accountEditDesired(account, nil, nil, request, nil)
	require.NoError(t, err)
	require.Equal(t, "auto", values["openai_responses_mode"].Value)
	require.True(t, values["responses_websockets_v2_enabled"].Present, "set must not clean up fallback keys")
	_, _, err = accountEditDesired(account, nil, map[string]any{"openai_responses_mode": "force_responses"}, request, nil)
	require.ErrorIs(t, err, ErrAccountEditInvalid)
}

func TestAccountEditStateDigestIsBoundedAndPresenceSensitive(t *testing.T) {
	_, _, account, _, _ := newAccountEditFixture(t)
	initial := AccountEditStateDigest(account)
	account.Extra["usage"] = 123
	account.Credentials["api_key"] = "not-reflected-in-digest"
	require.Equal(t, initial, AccountEditStateDigest(account))
	account.Extra["openai_responses_mode"] = nil
	require.NotEqual(t, initial, AccountEditStateDigest(account))
	delete(account.Extra, "openai_responses_mode")
	account.Extra["openai_ws_enabled"] = true
	require.NotEqual(t, initial, AccountEditStateDigest(account))
	delete(account.Extra, "openai_ws_enabled")
	account.CindyCredentialGeneration++
	require.NotEqual(t, initial, AccountEditStateDigest(account))
}

func TestAccountEditNoopAndBasicAreIndependentOfProviderHealth(t *testing.T) {
	f, repo, account, request, _ := newAccountEditFixture(t)
	repo.installations[1].State = PluginStateDisabled
	f.extensions.Load().runtimes[1].beginDrain()
	for _, typed := range []*extensionv1.ProviderEditRequestV1{nil, request} {
		ctx, release, err := PrepareAccountEdit(context.Background(), account, nil, nil, typed)
		require.NoError(t, err)
		require.NoError(t, ValidateAccountEditFresh(ctx))
		release()
	}
	require.Empty(t, repo.held, "no-op must not acquire a business runtime lease")
	stale := *request
	stale.ExpectedStateSHA256 = strings.Repeat("0", 64)
	_, _, err := PrepareAccountEdit(context.Background(), account, nil, nil, &stale)
	require.ErrorIs(t, err, ErrAccountEditStateChanged)
	stale = *request
	stale.ExpectedRuntimeGeneration++
	_, _, err = PrepareAccountEdit(context.Background(), account, nil, nil, &stale)
	require.ErrorIs(t, err, ErrAccountEditUnavailable)
	_, _, err = PrepareAccountEdit(context.Background(), account, nil, map[string]any{"openai_compact_mode": "force_off"}, nil)
	require.ErrorIs(t, err, ErrAccountEditUnavailable)
	ordinary := *account
	ordinary.Platform, ordinary.ProviderProfile = PlatformOpenAI, ""
	_, release, err := PrepareAccountEdit(context.Background(), &ordinary, nil, map[string]any{"openai_compact_mode": "force_off"}, nil)
	require.NoError(t, err)
	release()
	context, err := LoadAccountEditContext(context.Background(), &ordinary)
	require.NoError(t, err)
	require.Equal(t, "core", context.Kind)
}

func TestAccountEditRealIDAdmissionAndIndependentOwnerFences(t *testing.T) {
	f, repo, account, request, viewRequest := newAccountEditFixture(t)
	request.Changes["compact_mode"] = editSet("force_off")
	for _, capability := range []string{extensionv1.CapabilityProvider, extensionv1.CapabilityAdmin} {
		for index := range repo.installations[1].Bindings {
			if repo.installations[1].Bindings[index].Capability == capability {
				repo.installations[1].Bindings[index].RolloutPercent = int(stablePluginBucket(account.ID))
			}
		}
		applyViewFixture(t, f.PluginManager, repo)
		_, _, err := PrepareAccountEdit(context.Background(), account, nil, nil, request)
		require.ErrorIs(t, err, ErrAccountEditUnavailable)
		for index := range repo.installations[1].Bindings {
			repo.installations[1].Bindings[index].RolloutPercent = 100
		}
	}
	applyViewFixture(t, f.PluginManager, repo)
	viewCtx, releaseView, err := f.BindAccountViewRequest(context.Background(), viewRequest)
	require.NoError(t, err)
	defer releaseView()
	ctx, release, err := PrepareAccountEdit(WithPluginExecution(viewCtx, repo.installations[2]), account, nil, nil, request)
	require.NoError(t, err)
	defer release()
	execution, _ := PluginExecutionFromContext(ctx)
	require.EqualValues(t, 2, execution.ID)
	fences, err := PluginExecutionFences(ctx, "")
	require.NoError(t, err)
	require.Len(t, fences, 2)
	require.EqualValues(t, 1, fences[0].ID)
	require.True(t, fences[0].OriginView)
	require.True(t, fences[0].OriginEdit)
	require.False(t, fences[0].OriginCreate)
	require.Equal(t, account.ID, fences[0].EditAccountID)
	require.True(t, fences[1].Primary)
	_, err = AccountEditOwnedForUpdate(ctx, account, nil, nil, request)
	require.ErrorIs(t, err, ErrAccountEditUnavailable, "new owned work is forbidden before the ordered transaction fence")
	bad := fences[0]
	bad.EditDefinitionSHA256 = strings.Repeat("0", 64)
	_, err = mergePluginExecutionFences([]PluginExecutionFence{fences[0], bad})
	require.ErrorIs(t, err, ErrAccountEditUnavailable)
}

func TestAccountEditCatalogContextUsesOneActualAccountSnapshot(t *testing.T) {
	f, repo, account, _, _ := newAccountEditFixture(t)
	account.Extra["openai_responses_mode"] = "not-public"
	account.Extra["openai_compact_mode"] = nil
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = "dedicated"
	result, err := LoadAccountEditContext(context.Background(), account)
	require.NoError(t, err)
	require.True(t, result.Profile.Available)
	require.Equal(t, "ready", result.Catalog.Status)
	require.Len(t, f.catalog.calls, 1)
	require.Equal(t, account.ID, f.catalog.calls[0].AccountID)
	require.Len(t, *result.Catalog.Models, len(f.catalog.snapshot.CatalogModels))
	raw, _ := json.Marshal(result)
	for _, forbidden := range []string{"not-public", "synthetic-private-key", "private_secret", strings.Repeat("d", 64)} {
		require.NotContains(t, string(raw), forbidden)
	}
	require.Equal(t, json.RawMessage("null"), result.Values["compact_mode"].Value)
	require.Equal(t, json.RawMessage(`"dedicated"`), result.Values["responses_websocket_mode"].Value)
	require.False(t, result.Values["responses_mode"].Recognized)
	require.Equal(t, repo.held, repo.released)
	f.catalog.snapshot.Config.CatalogEnabled = false
	result, err = LoadAccountEditContext(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "catalog_disabled", result.Catalog.Reason)
	require.Nil(t, result.Catalog.Models)
	require.Nil(t, result.Catalog.Aliases)
	f.catalog.disabled = true
	result, err = LoadAccountEditContext(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "catalog_rpc_failed", result.Catalog.Reason)
	require.True(t, result.Profile.Available)
	f.catalog.disabled = false
	f.catalog.snapshot.Config.CatalogEnabled = true
	f.owner = 9
	result, err = LoadAccountEditContext(context.Background(), account)
	require.NoError(t, err)
	require.False(t, result.Profile.Available)
	require.Nil(t, result.Catalog.Models)
}

func TestAccountEditMappingPartitionAndLegacyReplacement(t *testing.T) {
	f, _, account, request, _ := newAccountEditFixture(t)
	snapshot, err := newCindyCatalogSnapshot(f.catalog.snapshot, 1, f.catalog.snapshot.Images)
	require.NoError(t, err)
	var managed extensionv1.CindyCatalogModel
	for _, model := range snapshot.CatalogModels {
		if model.Managed {
			managed = model
			break
		}
	}
	require.NotEmpty(t, managed.ID)
	account.Credentials["model_mapping"] = map[string]any{managed.ID: managed.LiveUpstreamID, "custom": "upstream-custom"}
	account.Credentials["compact_model_mapping"] = map[string]any{"compact-custom": "upstream-compact"}
	request.ExpectedCatalogNamespace = snapshot.Namespace
	request.Changes["model_mapping"] = extensionv1.AccountEditChangeV1{Op: "set"}
	values, _, err := accountEditDesired(account, map[string]any{"model_mapping": map[string]any{"another": "upstream-other"}}, nil, request, snapshot)
	require.NoError(t, err)
	require.Equal(t, map[string]any{managed.ID: managed.LiveUpstreamID, "another": "upstream-other"}, values["model_mapping"].Value)
	require.Equal(t, account.Credentials["compact_model_mapping"], values["compact_model_mapping"].Value)
	request.Changes["model_mapping"] = editClear()
	values, _, err = accountEditDesired(account, nil, nil, request, snapshot)
	require.NoError(t, err)
	require.Equal(t, map[string]any{managed.ID: managed.LiveUpstreamID}, values["model_mapping"].Value)
	request.Changes["compact_model_mapping"] = editClear()
	values, _, err = accountEditDesired(account, nil, nil, request, snapshot)
	require.NoError(t, err)
	require.False(t, values["compact_model_mapping"].Present)
	values, _, err = accountEditDesired(account, map[string]any{"model_mapping": map[string]any{managed.ID: "shadowed-but-useful"}}, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{managed.ID: "shadowed-but-useful"}, values["model_mapping"].Value)
	_, _, err = accountEditDesired(account, nil, nil, request, nil)
	require.ErrorIs(t, err, ErrAccountEditCatalogUnavailable, "unknown partition intent cannot be called a no-op")
	account.Credentials["model_mapping"] = map[string]any{"custom": "upstream-custom"}
	values, _, err = accountEditDesired(account, nil, nil, request, snapshot)
	require.NoError(t, err)
	require.False(t, values["model_mapping"].Present)
}

func TestAccountEditCatalogFailureDoesNotBlockStaticWire(t *testing.T) {
	f, _, account, request, _ := newAccountEditFixture(t)
	f.catalog.disabled = true
	request.Changes["compact_mode"] = editSet("auto")
	_, release, err := PrepareAccountEdit(context.Background(), account, nil, nil, request)
	require.NoError(t, err)
	release()
	require.Empty(t, f.catalog.calls)
	request.Changes = map[string]extensionv1.AccountEditChangeV1{"model_mapping": editClear()}
	request.ExpectedCatalogNamespace = "1:unknown"
	_, _, err = PrepareAccountEdit(context.Background(), account, nil, nil, request)
	require.ErrorIs(t, err, ErrAccountEditCatalogUnavailable)
}

func TestAccountEditNoopRereadCannotAcquireNewWrite(t *testing.T) {
	_, _, account, _, _ := newAccountEditFixture(t)
	account.Extra["openai_compact_mode"] = "auto"
	ctx, release, err := PrepareAccountEdit(context.Background(), account, nil, map[string]any{"openai_compact_mode": "auto"}, nil)
	require.NoError(t, err)
	defer release()
	account.Extra["openai_compact_mode"] = "force_off"
	_, err = AccountEditOwnedForUpdate(ctx, account, nil, map[string]any{"openai_compact_mode": "auto"}, nil)
	require.ErrorIs(t, err, ErrAccountEditStateChanged)
}

type accountEditMutationFixture struct {
	t      *testing.T
	before func()
	calls  int
}

func (r *accountEditMutationFixture) Run(ctx context.Context, _ int64, mutate func(context.Context) (*Account, error)) (*Account, error) {
	r.calls++
	db, mock, err := sqlmock.New()
	require.NoError(r.t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	mock.ExpectBegin()
	tx, err := client.Tx(ctx)
	require.NoError(r.t, err)
	if r.before != nil {
		r.before()
	}
	MarkAccountEditFenced(ctx)
	account, mutationErr := mutate(dbent.NewTxContext(ctx, tx))
	mock.ExpectRollback()
	require.NoError(r.t, tx.Rollback())
	require.NoError(r.t, mock.ExpectationsWereMet())
	return account, mutationErr
}

func TestAccountEditCentralUpdateRetainClearAndMixedFailure(t *testing.T) {
	f, repo, account, request, _ := newAccountEditFixture(t)
	storage := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{account.ID: account}}
	runner := &accountEditMutationFixture{t: t}
	svc := &adminServiceImpl{accountRepo: storage, cindyAccountMutations: runner}
	account.Extra["openai_responses_mode"] = nil
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = "shared"
	account.Credentials["model_mapping"] = map[string]any{"saved": "saved-target"}
	repo.installations[1].State = PluginStateDisabled
	_, err := svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Name: "basic", Extra: map[string]any{"ordinary": true}})
	require.NoError(t, err)
	require.Nil(t, account.Extra["openai_responses_mode"])
	require.Contains(t, account.Extra, "openai_responses_mode")
	require.Equal(t, "shared", account.Extra["openai_apikey_responses_websockets_v2_mode"])
	require.Equal(t, "basic", account.Name)
	require.Empty(t, repo.held)
	_, err = svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Name: "must-not-change", Extra: map[string]any{"openai_compact_mode": "force_off"}})
	require.ErrorIs(t, err, ErrAccountEditUnavailable)
	require.Equal(t, "basic", account.Name)
	require.Len(t, storage.updatedAccounts, 1)
	repo.installations[1].State = PluginStateEnabled
	applyViewFixture(t, f.PluginManager, repo)
	request.ExpectedStateSHA256 = AccountEditStateDigest(account)
	request.Changes = map[string]extensionv1.AccountEditChangeV1{"responses_mode": editClear(), "responses_websocket_mode": editClear()}
	_, err = svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Name: "clear", ProviderEdit: request})
	require.NoError(t, err)
	require.NotContains(t, account.Extra, "openai_responses_mode")
	require.NotContains(t, account.Extra, "openai_apikey_responses_websockets_v2_mode")
	require.Equal(t, map[string]any{"saved": "saved-target"}, account.Credentials["model_mapping"])
	require.Equal(t, repo.held, repo.released)
}

func TestAccountEditOuterTransactionKeepsLeaseThroughRollback(t *testing.T) {
	f, repo, account, request, _ := newAccountEditFixture(t)
	request.Changes["compact_mode"] = editSet("force_off")
	ctx, release, err := PrepareAccountEdit(context.Background(), account, nil, nil, request)
	require.NoError(t, err)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	retainBoundAccountEditLease(ctx, tx)
	release()
	require.Empty(t, repo.released)
	f.extensions.Load().runtimes[1].beginDrain()
	require.ErrorIs(t, tx.Commit(), ErrAccountEditUnavailable)
	require.Empty(t, repo.released)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.Equal(t, []int64{1}, repo.released)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountEditUnrelatedCoreMappingReplacementAndPayloads(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini} {
		account := &Account{ID: 71, Name: "core", Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive,
			Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "native-secret", "model_mapping": map[string]any{"old": "target"}, "compact_model_mapping": map[string]any{"old": "target"}},
			Extra:       map[string]any{"openai_responses_mode": nil, "openai_passthrough": true}}
		storage := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{account.ID: account}}
		svc := &adminServiceImpl{accountRepo: storage}
		_, err := svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}})
		require.NoError(t, err, platform)
		require.NotContains(t, account.Credentials, "model_mapping", platform)
		require.NotContains(t, account.Credentials, "compact_model_mapping", platform)
		require.Equal(t, "native-secret", account.Credentials["api_key"])
		require.Equal(t, platform, account.Platform)
		require.True(t, account.Extra["openai_passthrough"].(bool))
		require.Contains(t, account.Extra, "openai_responses_mode")
		require.Nil(t, account.Extra["openai_responses_mode"])
	}
	for _, kind := range []string{AccountJobKindImportData, AccountJobKindBatchCreate, AccountJobKindExtensionOperation} {
		for _, raw := range []string{`[1,"two",null]`, `null`, `"native"`, `{"_host_account_edit":"native-owned-by-other-kind"}`} {
			got, err := accountJobPayloadWithEdit(context.Background(), kind, []byte(raw), nil)
			require.NoError(t, err)
			require.Equal(t, raw, string(got))
		}
	}
}

func TestAccountEditFrozenJobIdentityCannotBecomeCore(t *testing.T) {
	_, _, account, _, _ := newAccountEditFixture(t)
	snapshot := AccountJobEditSnapshot{StateSHA256: AccountEditStateDigest(account)}
	raw, _ := json.Marshal(map[string]any{accountJobEditSnapshotsKey: map[string]AccountJobEditSnapshot{"2": snapshot}})
	account.Platform, account.ProviderProfile = PlatformOpenAI, ""
	_, _, err := PrepareAccountJobEdit(context.Background(), raw, account, nil, map[string]any{"openai_compact_mode": "auto"})
	require.ErrorIs(t, err, ErrAccountEditStateChanged)
}

func TestAccountEditJobSnapshotKeepsOriginalHashOwnerAndTTL(t *testing.T) {
	f, plugins, account, _, _ := newAccountEditFixture(t)
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	payload := json.RawMessage(`{"account_ids":[2],"extra":{"openai_compact_mode":"force_off"}}`)
	ctx := WithAccountJobEditAccounts(WithPluginExecution(context.Background(), plugins.installations[2]), []*Account{account})
	job, replayed, err := jobs.Submit(ctx, 9, AccountJobKindBulkUpdate, "edit-frozen", payload, nil, []AccountJobItemSeed{{TargetAccountID: &account.ID}})
	require.NoError(t, err)
	require.False(t, replayed)
	hash := sha256.Sum256(payload)
	require.Equal(t, hex.EncodeToString(hash[:]), job.RequestHash)
	owner, err := AccountJobPluginExecution(job.Metadata)
	require.NoError(t, err)
	require.EqualValues(t, 2, owner.ID)
	frozen := repo.payloads[job.ID]
	plaintext, err := (accountJobTestCipher{}).Decrypt(frozen.cipher)
	require.NoError(t, err)
	require.Contains(t, plaintext, accountJobEditSnapshotsKey)
	require.NotContains(t, plaintext, "synthetic-private-key")
	bound, release, err := PrepareAccountJobEdit(ctx, []byte(plaintext), account, nil, map[string]any{"openai_compact_mode": "force_off"})
	require.NoError(t, err)
	inherited, _ := PluginExecutionFromContext(bound)
	require.Equal(t, owner, inherited)
	release()
	plugins.installations[1].RuntimeGeneration++
	applyViewFixture(t, f.PluginManager, plugins)
	_, _, err = PrepareAccountJobEdit(ctx, []byte(plaintext), account, nil, map[string]any{"openai_compact_mode": "force_off"})
	require.ErrorIs(t, err, ErrAccountEditUnavailable)
	replayedJob, replayed, err := jobs.Submit(ctx, 9, AccountJobKindBulkUpdate, "edit-frozen", payload, nil, []AccountJobItemSeed{{TargetAccountID: &account.ID}})
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, job.ID, replayedJob.ID)
	require.Equal(t, frozen, repo.payloads[job.ID])
	_, _, err = jobs.Submit(ctx, 9, AccountJobKindBulkUpdate, "forged", []byte(`{"_host_account_edit":{},"extra":{"openai_compact_mode":"force_off"}}`), nil, []AccountJobItemSeed{{TargetAccountID: &account.ID}})
	require.ErrorIs(t, err, ErrAccountEditInvalid)
}

func TestAccountEditNativeCoreJobAndNullRemainIndependent(t *testing.T) {
	_, _, account, _, _ := newAccountEditFixture(t)
	processExtensionOperations.Store(nil)
	ordinary := *account
	ordinary.Platform, ordinary.ProviderProfile = PlatformOpenAI, ""
	ctx := WithAccountJobEditAccounts(context.Background(), []*Account{&ordinary})
	payload := json.RawMessage(`{"extra":{"openai_responses_mode":"auto"}}`)
	encoded, err := accountJobPayloadWithEdit(ctx, AccountJobKindBulkUpdate, payload, []AccountJobItemSeed{{TargetAccountID: &ordinary.ID}})
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(encoded))
	values, changed, err := accountEditDesired(account, map[string]any{"model_mapping": nil}, map[string]any{"openai_responses_mode": nil}, nil, nil)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, values["model_mapping"].Present)
	require.Nil(t, values["model_mapping"].Value)
	require.True(t, values["openai_responses_mode"].Present)
	require.Nil(t, values["openai_responses_mode"].Value)
	mode, _, err := normalizeBulkOpenAIResponsesMode("auto")
	require.NoError(t, err)
	require.Equal(t, "auto", mode)
	input := &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Extra: map[string]any{"openai_responses_mode": "auto"}}
	settings, err := normalizeBulkOpenAISettings(input)
	require.NoError(t, err)
	_, err = validateBulkOpenAISettingsTargets(input, settings, map[int64]*Account{account.ID: account})
	require.Error(t, err, "Cindy bulk Responses eligibility must not expand")
}

func TestAccountEditDuplicatePreservesProtocolAbsenceAndValues(t *testing.T) {
	for _, raw := range []any{nil, "auto", "force_chat_completions", "legacy-retained"} {
		for _, present := range []bool{false, true} {
			repo := newDuplicateAccountRepoStub()
			svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
			source := &Account{Name: "source", Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey,
				Credentials: cindyCredentials(), Extra: map[string]any{CindyDeviceIDExtraKey: strings.Repeat("e", 64), CindyDeviceIDSourceExtraKey: "input-preserved", "nested": map[string]any{"kept": true}}}
			if present {
				source.Extra["openai_responses_mode"] = raw
			}
			require.NoError(t, repo.Create(context.Background(), source))
			copy, err := svc.DuplicateAccount(context.Background(), source.ID, "admin:9", "unchanged-copy")
			require.NoError(t, err)
			value, copiedPresence := copy.Extra["openai_responses_mode"]
			require.Equal(t, present, copiedPresence)
			if present {
				require.Equal(t, raw, value)
			}
			require.False(t, copy.Schedulable)
			require.Empty(t, copy.GroupIDs)
			require.NotEqual(t, source.Extra[CindyDeviceIDExtraKey], copy.Extra[CindyDeviceIDExtraKey])
			copy.Extra["nested"].(map[string]any)["kept"] = false
			require.Equal(t, true, source.Extra["nested"].(map[string]any)["kept"])
			replayed, err := svc.DuplicateAccount(context.Background(), source.ID, "admin:9", "unchanged-copy")
			require.NoError(t, err)
			require.Equal(t, copy.ID, replayed.ID)
		}
	}
}

func TestAccountEditFenceDataUsesActualBucketAndFreshConfig(t *testing.T) {
	_, repo, account, request, _ := newAccountEditFixture(t)
	installation := repo.installations[1]
	manifest, _ := json.Marshal(installation.Manifest)
	fence := PluginExecutionFence{ID: 1, OriginEdit: true, EditAccountID: account.ID, PluginKey: installation.PluginKey, EditContributionID: request.ContributionID,
		EditDefinitionSHA256: request.ExpectedDefinitionSHA256, ConfigSHA256: accountCreateConfigDigest(installation.ConfigEncrypted)}
	require.NoError(t, ValidateAccountEditFenceData(fence, manifest, installation.ConfigEncrypted, installation.Bindings))
	for _, capability := range []string{extensionv1.CapabilityProvider, extensionv1.CapabilityAdmin} {
		bindings := append([]PluginBinding(nil), installation.Bindings...)
		for index := range bindings {
			if bindings[index].Capability == capability {
				bindings[index].RolloutPercent = int(stablePluginBucket(account.ID))
			}
		}
		require.ErrorIs(t, ValidateAccountEditFenceData(fence, manifest, installation.ConfigEncrypted, bindings), ErrAccountEditUnavailable)
	}
	require.ErrorIs(t, ValidateAccountEditFenceData(fence, manifest, "changed", installation.Bindings), ErrAccountEditUnavailable)
}

func TestAccountEditUpdateExtraAndBulkCannotBypassAdmission(t *testing.T) {
	_, plugins, account, _, _ := newAccountEditFixture(t)
	storage := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{account.ID: account}, getByIDsAccounts: []*Account{account}}
	runner := &accountEditMutationFixture{t: t}
	svc := &adminServiceImpl{accountRepo: storage, cindyAccountMutations: runner}
	plugins.installations[1].State = PluginStateDisabled
	err := svc.UpdateAccountExtra(context.Background(), account.ID, map[string]any{"openai_compact_mode": "force_off"})
	require.ErrorIs(t, err, ErrAccountEditUnavailable)
	require.Empty(t, storage.updatedAccounts)
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Name: "not-written", Credentials: map[string]any{"model_mapping": map[string]any{"custom": "target"}}})
	require.ErrorIs(t, err, ErrAccountEditUnavailable)
	require.Zero(t, storage.bulkUpdateCalls)
	account.Extra["openai_compact_mode"] = "force_off"
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Extra: map[string]any{"openai_compact_mode": "force_off"}})
	require.NoError(t, err)
	require.Equal(t, 1, storage.bulkUpdateCalls)
	require.Equal(t, "force_off", storage.lastBulkUpdate.Extra["openai_compact_mode"])
}

func TestAccountEditBasicSavePreservesIndependentViewScope(t *testing.T) {
	f, plugins, account, _, request := newAccountEditFixture(t)
	ctx, releaseView, err := f.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer releaseView()
	storage := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{account.ID: account}}
	svc := &adminServiceImpl{accountRepo: storage, cindyAccountMutations: &accountEditMutationFixture{t: t}}
	_, err = svc.UpdateAccount(ctx, 5, &UpdateAccountInput{Name: "outside-view"})
	require.ErrorIs(t, err, ErrAccountViewScope)
	require.Empty(t, storage.updatedAccounts)
	held := len(plugins.held)
	_, err = svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{Name: "basic-view-save"})
	require.NoError(t, err)
	require.Equal(t, held, len(plugins.held), "basic-only save does not acquire edit ownership")
	plugins.installations[1].State = PluginStateDisabled
	_, err = svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{Name: "must-not-save"})
	require.ErrorIs(t, err, ErrAccountViewUnavailable)
	require.Equal(t, "basic-view-save", account.Name)
}

func TestAccountEditBulkLockedIdentityChangeCannotRestoreUnderOldPlan(t *testing.T) {
	_, _, account, _, _ := newAccountEditFixture(t)
	storage := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{account.ID: account}, getByIDsAccounts: []*Account{account}}
	runner := &accountEditMutationFixture{t: t, before: func() { account.Credentials["base_url"] = "https://changed.invalid" }}
	svc := &adminServiceImpl{accountRepo: storage, cindyAccountMutations: runner}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Credentials: map[string]any{
		"base_url": "https://api.laxarouter.ai", "model_mapping": map[string]any{"new-custom": "upstream"},
	}})
	require.ErrorIs(t, err, ErrAccountEditStateChanged)
	require.Equal(t, 1, runner.calls)
	require.Zero(t, storage.bulkUpdateCalls)
}

func TestAccountEditLegacyQueuedNoopDoesNotRequireNewProviderWork(t *testing.T) {
	_, plugins, account, _, _ := newAccountEditFixture(t)
	plugins.installations[1].State = PluginStateDisabled
	account.Extra["openai_compact_mode"] = nil
	ctx, release, err := PrepareAccountJobEdit(context.Background(), []byte(`{"extra":{"openai_compact_mode":null}}`), account, nil, map[string]any{"openai_compact_mode": nil})
	require.NoError(t, err)
	defer release()
	_, bound := AccountEditFromContext(ctx)
	require.True(t, bound)
	require.Empty(t, plugins.held)
	account.Extra["openai_compact_mode"] = "force_off"
	_, err = AccountEditOwnedForUpdate(ctx, account, nil, map[string]any{"openai_compact_mode": nil}, nil)
	require.ErrorIs(t, err, ErrAccountEditStateChanged)
	_, _, err = PrepareAccountJobEdit(context.Background(), []byte(`{}`), account, nil, map[string]any{"openai_compact_mode": "auto"})
	require.ErrorIs(t, err, ErrAccountEditStateChanged, "an old queued actual delta requires a fresh submission")
}

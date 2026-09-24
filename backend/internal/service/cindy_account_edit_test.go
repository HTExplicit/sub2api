//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func newNativeAccountEditFixture(t *testing.T) (*Account, *extensionv1.ProviderEditRequestV1) {
	t.Helper()
	original := cindyProvider.Load()
	ConfigureCindyProvider(&extensionv1.CindyProviderConfig{BalanceDetection: true, CatalogEnabled: true, SearchEnabled: true})
	t.Cleanup(func() { cindyProvider.Store(original) })
	account := &Account{ID: 2, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, CindyCredentialGeneration: 6,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "synthetic-cindy-only"},
		Extra:       map[string]any{CindyDeviceIDExtraKey: strings.Repeat("d", 64), CindyDeviceIDSourceExtraKey: "input-preserved"}}
	request := &extensionv1.ProviderEditRequestV1{ExpectedStateSHA256: AccountEditStateDigest(account), Changes: map[string]extensionv1.AccountEditChangeV1{}}
	return account, request
}
func editSet(value string) extensionv1.AccountEditChangeV1 {
	return extensionv1.AccountEditChangeV1{Op: "set", Value: &value}
}
func editClear() extensionv1.AccountEditChangeV1 { return extensionv1.AccountEditChangeV1{Op: "clear"} }
func TestAccountEditCodecRetainsRawAndFiniteClear(t *testing.T) {
	account, request := newNativeAccountEditFixture(t)
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
	account, _ := newNativeAccountEditFixture(t)
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

func TestAccountEditMappingPartitionAndLegacyReplacement(t *testing.T) {
	account, request := newNativeAccountEditFixture(t)
	snapshot, err := LoadCindyCatalogSnapshot(context.Background(), account)
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

func TestAccountEditNoopRereadCannotAcquireNewWrite(t *testing.T) {
	account, _ := newNativeAccountEditFixture(t)
	account.Extra["openai_compact_mode"] = "auto"
	ctx, release, err := PrepareAccountEdit(context.Background(), account, nil, map[string]any{"openai_compact_mode": "auto"}, nil)
	require.NoError(t, err)
	defer release()
	account.Extra["openai_compact_mode"] = "force_off"
	_, err = AccountEditOwnedForUpdate(ctx, account, nil, map[string]any{"openai_compact_mode": "auto"}, nil)
	require.ErrorIs(t, err, ErrAccountEditStateChanged)
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
	for _, kind := range []string{AccountJobKindImportData, AccountJobKindBatchCreate} {
		for _, raw := range []string{`[1,"two",null]`, `null`, `"native"`, `{"_host_account_edit":"native-owned-by-other-kind"}`} {
			got, err := accountJobPayloadWithEdit(context.Background(), kind, []byte(raw), nil)
			require.NoError(t, err)
			require.Equal(t, raw, string(got))
		}
	}
}

func TestAccountEditFrozenJobIdentityCannotBecomeCore(t *testing.T) {
	account, _ := newNativeAccountEditFixture(t)
	snapshot := AccountJobEditSnapshot{StateSHA256: AccountEditStateDigest(account)}
	raw, _ := json.Marshal(map[string]any{accountJobEditSnapshotsKey: map[string]AccountJobEditSnapshot{"2": snapshot}})
	account.Platform, account.ProviderProfile = PlatformOpenAI, ""
	_, _, err := PrepareAccountJobEdit(context.Background(), raw, account, nil, map[string]any{"openai_compact_mode": "auto"})
	require.ErrorIs(t, err, ErrAccountEditStateChanged)
}

func TestAccountEditNativeCoreJobAndNullRemainIndependent(t *testing.T) {
	account, _ := newNativeAccountEditFixture(t)
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

func TestAccountEditLegacyQueuedNoopDoesNotRequireNewProviderWork(t *testing.T) {
	account, _ := newNativeAccountEditFixture(t)
	account.Extra["openai_compact_mode"] = nil
	ctx, release, err := PrepareAccountJobEdit(context.Background(), []byte(`{"extra":{"openai_compact_mode":null}}`), account, nil, map[string]any{"openai_compact_mode": nil})
	require.NoError(t, err)
	defer release()
	_, bound := AccountEditFromContext(ctx)
	require.True(t, bound)
	account.Extra["openai_compact_mode"] = "force_off"
	_, err = AccountEditOwnedForUpdate(ctx, account, nil, map[string]any{"openai_compact_mode": nil}, nil)
	require.ErrorIs(t, err, ErrAccountEditStateChanged)
	_, _, err = PrepareAccountJobEdit(context.Background(), []byte(`{}`), account, nil, map[string]any{"openai_compact_mode": "auto"})
	require.ErrorIs(t, err, ErrAccountEditStateChanged, "an old queued actual delta requires a fresh submission")
}

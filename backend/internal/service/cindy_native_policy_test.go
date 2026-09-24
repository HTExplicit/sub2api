//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func TestNativeCindyCreateDefaultsAndExplicitFields(t *testing.T) {
	request := &extensionv1.ProviderCreateRequestV1{Values: map[string]string{"device_id": strings.Repeat("d", 64)},
		InheritDefaults: []string{"concurrency", "priority", "rate_multiplier", "load_factor", "responses_mode"}}
	ctx, release, err := bindProcessAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, request)
	require.NoError(t, err)
	defer release()
	input := &CreateAccountInput{Platform: PlatformCindy, Type: AccountTypeAPIKey, Priority: 0,
		ExplicitCreateFields: map[string]bool{"priority": true},
		Extra:                map[string]any{CindyResponsesModeExtraKey: "auto", CindyDeviceIDSourceExtraKey: "input-preserved"}}
	require.NoError(t, applyAccountCreateProfile(ctx, input))
	require.Equal(t, 3, input.Concurrency)
	require.Zero(t, input.Priority, "an explicitly supplied zero must not inherit priority 50")
	require.Equal(t, float64(1), *input.RateMultiplier)
	require.Nil(t, input.LoadFactor)
	require.Equal(t, "auto", input.Extra[CindyResponsesModeExtraKey])
	require.Equal(t, request.Values["device_id"], input.Extra[CindyDeviceIDExtraKey])
	require.NotContains(t, input.Extra, CindyDeviceIDSourceExtraKey, "provenance is derived by the identity normalizer")
	create, bound := AccountCreateFromContext(ctx)
	require.True(t, bound)
	require.Equal(t, 1, create.MinimumEffectiveGroups)
}

func TestNativeCindyCreateRejectsWrongIdentityAndUnknownDeviceInput(t *testing.T) {
	_, _, err := bindProcessAccountCreate(context.Background(), PlatformOpenAI, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, nil)
	require.ErrorIs(t, err, ErrAccountCreateInvalid)
	for _, request := range []*extensionv1.ProviderCreateRequestV1{
		{Values: map[string]string{"unknown": "value"}},
		{Values: map[string]string{"device_id": strings.Repeat("d", 65)}},
		{InheritDefaults: []string{"priority", "priority"}},
		{InheritDefaults: []string{"unknown"}},
	} {
		_, _, err = bindProcessAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, request)
		require.ErrorIs(t, err, ErrAccountCreateInvalid)
	}
}

func TestNativeCindyProviderCacheAndPolicyLease(t *testing.T) {
	old := cindyProvider.Load()
	t.Cleanup(func() { cindyProvider.Store(old) })
	ConfigureCindyProvider(&extensionv1.CindyProviderConfig{BalanceDetection: true, CatalogEnabled: true})
	invocation := extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.features", Payload: json.RawMessage(`{}`)}
	first, err := invokeCindyProviderCached(context.Background(), invocation)
	require.NoError(t, err)
	before := string(first.Payload)
	first.Payload[0] = 'x'
	next, err := invokeCindyProviderCached(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, before, string(next.Payload), "caller buffers cannot change the shared cached answer")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = invokeCindyProviderCached(canceled, invocation)
	require.ErrorIs(t, err, context.Canceled)

	_, release, err := bindProcessAccountCreate(context.Background(), PlatformCindy, AccountTypeAPIKey, ProviderProfileCindyLaxaV1, nil)
	require.NoError(t, err)
	defer release()
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		ConfigureCindyProvider(&extensionv1.CindyProviderConfig{})
		close(done)
	}()
	<-started
	select {
	case <-done:
		t.Fatal("configuration replaced while an account transaction still owned the policy")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("configuration update did not resume after the native policy lease ended")
	}
	updated, err := invokeCindyProviderCached(context.Background(), invocation)
	require.NoError(t, err)
	var config extensionv1.CindyProviderConfig
	require.NoError(t, json.Unmarshal(updated.Payload, &config))
	require.False(t, config.BalanceDetection)
	require.False(t, config.CatalogEnabled)
}

func TestNativeCindyEditContextAndFrozenJobStayInstallationFree(t *testing.T) {
	account, _ := newNativeAccountEditFixture(t)
	view, err := LoadAccountEditContext(context.Background(), account, "off")
	require.NoError(t, err)
	require.True(t, view.Profile.Native)
	require.True(t, view.Profile.Available)
	require.Equal(t, "off", view.Values["responses_websocket_mode"].Effective)
	raw, err := json.Marshal(view.Profile)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "plugin_id")
	ctx := WithAccountJobEditAccounts(context.Background(), []*Account{account})
	payload := json.RawMessage(`{"extra":{"openai_compact_mode":"force_off"}}`)
	encoded, err := accountJobPayloadWithEdit(ctx, AccountJobKindBulkUpdate, payload, []AccountJobItemSeed{{TargetAccountID: &account.ID}})
	require.NoError(t, err)
	bound, release, err := PrepareAccountJobEdit(context.Background(), encoded, account, nil, map[string]any{"openai_compact_mode": "force_off"})
	require.NoError(t, err)
	require.NoError(t, ValidateAccountEditFresh(bound))
	release()
	ConfigureCindyProvider(&extensionv1.CindyProviderConfig{BalanceDetection: false, CatalogEnabled: true})
	_, _, err = PrepareAccountJobEdit(context.Background(), encoded, account, nil, map[string]any{"openai_compact_mode": "force_off"})
	require.ErrorIs(t, err, ErrAccountEditUnavailable, "a queued native edit must retain its submitted policy")
}

// Existing canonical-identity service tests share the native fixture.
func useAccountCreateFixture(t *testing.T) {
	original := cindyProvider.Load()
	ConfigureCindyProvider(&extensionv1.CindyProviderConfig{BalanceDetection: true, CatalogEnabled: true, SearchEnabled: true})
	t.Cleanup(func() { cindyProvider.Store(original) })
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

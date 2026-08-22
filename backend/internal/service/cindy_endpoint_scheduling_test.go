package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func cindyEndpointSchedulingAccount(id int64) Account {
	return Account{
		ID:              id,
		Name:            "cindy",
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Status:          StatusActive,
		Schedulable:     true,
		Concurrency:     1,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://api.laxarouter.ai",
		},
	}
}

func TestAccountSupportsOpenAICapabilities_StrictCindyModelEndpointMatrix(t *testing.T) {
	account := cindyEndpointSchedulingAccount(51001)
	tests := []struct {
		name       string
		model      string
		capability OpenAIEndpointCapability
		want       bool
	}{
		{name: "messages-only Claude on Messages", model: "claude-opus-5", capability: OpenAIEndpointCapabilityMessages, want: true},
		{name: "messages-only Claude not on Responses", model: "claude-opus-5", capability: OpenAIEndpointCapabilityResponses, want: false},
		{name: "messages-only Claude not on Chat", model: "claude-opus-5", capability: OpenAIEndpointCapabilityChatCompletions, want: false},
		{name: "image-only Gemini not on Responses", model: "gemini-3-pro-image", capability: OpenAIEndpointCapabilityResponses, want: false},
		{name: "image-only Gemini not on Chat", model: "gemini-3-pro-image", capability: OpenAIEndpointCapabilityChatCompletions, want: false},
		{name: "GPT image public ID uses exact Responses bridge", model: "gpt-image-2", capability: OpenAIEndpointCapabilityResponses, want: true},
		{name: "GPT image live ID uses exact Responses bridge", model: "openai/gpt-image-2", capability: OpenAIEndpointCapabilityResponses, want: true},
		{name: "GPT image is not a chat model", model: "gpt-image-2", capability: OpenAIEndpointCapabilityChatCompletions, want: false},
		{name: "Luna supports Responses", model: "gpt-5.6-luna", capability: OpenAIEndpointCapabilityResponses, want: true},
		{name: "Luna supports Chat", model: "gpt-5.6-luna", capability: OpenAIEndpointCapabilityChatCompletions, want: true},
		{name: "Luna supports Messages", model: "gpt-5.6-luna", capability: OpenAIEndpointCapabilityMessages, want: true},
		{name: "hidden search model is not client selectable", model: CindyWebSearchModel, capability: OpenAIEndpointCapabilityAlphaSearch, want: false},
		{name: "public Responses model supports alpha search", model: "gpt-5.6-luna", capability: OpenAIEndpointCapabilityAlphaSearch, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, accountSupportsOpenAICapabilities(context.Background(), &account, tt.model, tt.capability, ""))
		})
	}
}

func TestAccountSupportsOpenAICapabilities_FirstClassCindySearchUsesPublicResponsesModels(t *testing.T) {
	account := cindyEndpointSchedulingAccount(51002)

	require.True(t, accountSupportsOpenAICapabilities(
		context.Background(), &account, "gpt-5.6-sol", OpenAIEndpointCapabilityAlphaSearch, "",
	))
	require.False(t, accountSupportsOpenAICapabilities(
		context.Background(), &account, CindyWebSearchModel, OpenAIEndpointCapabilityAlphaSearch, "",
	))
	require.False(t, accountSupportsOpenAICapabilities(
		context.Background(), &account, "claude-opus-5", OpenAIEndpointCapabilityAlphaSearch, "",
	))
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_FirstClassCindySearch(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(51003)
	account := cindyEndpointSchedulingAccount(51004)
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(), &groupID, "", "", "gpt-5.6-sol", nil,
		OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityAlphaSearch,
		false, false, false, PlatformCindy,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, account.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_MixedGroupGatesCindyByEndpoint(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(51010)
	cindy := cindyEndpointSchedulingAccount(51011)
	ordinary := Account{
		ID:          51012,
		Name:        "ordinary-openai-compatible",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "ordinary-key",
			"base_url": "https://compat.example",
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{cindy, ordinary}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	tests := []struct {
		name       string
		model      string
		capability OpenAIEndpointCapability
	}{
		{name: "Messages-only Claude cannot select Cindy for Responses", model: "claude-opus-5", capability: OpenAIEndpointCapabilityResponses},
		{name: "image-only Gemini cannot select Cindy for Chat", model: "gemini-3-pro-image", capability: OpenAIEndpointCapabilityChatCompletions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(
				context.Background(), &groupID, "", "", tt.model, nil,
				OpenAIUpstreamTransportAny, tt.capability, false, false, false,
			)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, ordinary.ID, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_MixedLegacyLaxaUsesGenericOpenAIPool(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(51013)
	legacyLaxa := Account{
		ID: 51014, Name: "legacy-laxa", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: cindyCredentials(),
	}
	ordinary := Account{
		ID: 51015, Name: "ordinary-openai-compatible", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "ordinary-key", "base_url": "https://compat.example"},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{legacyLaxa, ordinary}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	for _, test := range []struct {
		name       string
		excludedID int64
		wantID     int64
	}{
		{name: "legacy remains selectable", excludedID: ordinary.ID, wantID: legacyLaxa.ID},
		{name: "ordinary member remains selectable", excludedID: legacyLaxa.ID, wantID: ordinary.ID},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(
				context.Background(), &groupID, "", "", "gpt-5.6-luna",
				map[int64]struct{}{test.excludedID: {}}, OpenAIUpstreamTransportAny,
				OpenAIEndpointCapabilityResponses, false, false, false, PlatformOpenAI,
			)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, test.wantID, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_CindyMessagesUsesMessagesCapability(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(51020)
	cindy := cindyEndpointSchedulingAccount(51021)
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{cindy}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(), &groupID, "", "", "claude-opus-5", nil,
		OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityMessages, false, false, false, PlatformCindy,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, cindy.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_MixedMessagesUsesNativeCindyModel(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(51030)
	cindy := cindyEndpointSchedulingAccount(51031)
	cindy.Priority = 0
	ordinary := Account{
		ID:          51032,
		Name:        "ordinary-openai-compatible",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    100,
		Credentials: map[string]any{
			"api_key":  "ordinary-key",
			"base_url": "https://compat.example",
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{cindy, ordinary}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	nativeModel := "claude-sonnet-4-5-20250929"
	legacyMappedModel := "gpt-5.3-codex"
	ctx := WithOpenAICindyRequestedModel(context.Background(), nativeModel)
	require.Equal(t, nativeModel, openAIRequestedModelForAccount(ctx, &cindy, legacyMappedModel))
	require.Equal(t, legacyMappedModel, openAIRequestedModelForAccount(ctx, &ordinary, legacyMappedModel))
	require.True(t, isOpenAICompatibleAccountEligibleForRequest(
		ctx, &cindy, PlatformCindy, legacyMappedModel, false, OpenAIEndpointCapabilityMessages,
	))
	require.NotNil(t, svc.resolveFreshSchedulableOpenAIAccount(
		ctx, &cindy, PlatformCindy, legacyMappedModel, false, OpenAIEndpointCapabilityMessages,
	))
	require.NotNil(t, svc.recheckSelectedOpenAIAccountFromDB(
		ctx, &cindy, &groupID, PlatformCindy, legacyMappedModel, false, OpenAIEndpointCapabilityMessages,
	))

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx, &groupID, "", "", legacyMappedModel, map[int64]struct{}{ordinary.ID: {}},
		OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityMessages, false, false, false, PlatformCindy,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, cindy.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_MixedImagesGatesCindyByOperation(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(51050)
	cindy := cindyEndpointSchedulingAccount(51051)
	ordinary := Account{
		ID: 51052, Name: "ordinary-openai-compatible", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "ordinary-key", "base_url": "https://compat.example"},
	}
	newService := func(accounts []Account) *OpenAIGatewayService {
		cfg := &config.Config{}
		cfg.Gateway.Scheduling.LoadBatchEnabled = false
		return &OpenAIGatewayService{
			accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
			cache:              &schedulerTestGatewayCache{},
			cfg:                cfg,
			concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
		}
	}

	selection, _, err := newService([]Account{cindy, ordinary}).SelectAccountWithSchedulerForImages(
		context.Background(), &groupID, "", "gpt-image-2", nil,
		OpenAIImagesCapabilityBasic, CindyEndpointImagesEdit,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, ordinary.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	imageCtx := withOpenAICindyImageEndpoint(context.Background(), CindyEndpointImagesEdit)
	selection, _, err = newService([]Account{cindy}).selectAccountWithScheduler(
		imageCtx, &groupID, "", "", "gemini-3-pro-image", nil,
		OpenAIUpstreamTransportHTTPSSE, "", OpenAIImagesCapabilityBasic,
		false, PlatformCindy, false, false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, cindy.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cindy "github.com/HTExplicit/sub2api-plugins/cindyprovider/catalog"
	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCindyBillingUsesCapturedReferenceAcrossAsyncBoundary(t *testing.T) {
	account := &Account{ID: 41, Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	snapshot := (cindy.Registry{Config: extensionv1.CindyProviderConfig{CatalogEnabled: true}}).PricingSnapshot()
	key, _ := json.Marshal([]string{"CindyTextPricingForModel", "gpt-5.6-luna"})
	snapshot.Results[string(key)], _ = json.Marshal([]any{CindyTextPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 9e-6, CacheReadInputTokenCost: 1e-6, CacheCreationInputTokenCostPresent: true}, true})
	parent := context.WithValue(context.Background(), cindyPricingContextKey{}, &capturedCindyPricing{accountID: account.ID, snapshot: &snapshot})
	worker := CopyProviderPricingContext(parent, context.Background())
	require.True(t, shouldUseCindyTextPricingContext(worker, account, "gpt-5.6-luna"))
	stored := cindyPricingSnapshotFromContext(worker, account)
	require.Same(t, &snapshot, stored)
	cost, err := calculateCindyCatalogTextCost(NewBillingService(&config.Config{}, nil), "gpt-5.6-luna", UsageTokens{InputTokens: 1000, OutputTokens: 100}, 1, "", false, stored)
	require.NoError(t, err)
	require.InDelta(t, 0.005, cost.InputCost, 1e-12)
	require.InDelta(t, 0.0009, cost.OutputCost, 1e-12)
	other := *account
	other.ID++
	require.Nil(t, cindyPricingSnapshotFromContext(worker, &other), "a retry account cannot inherit the previous account's reference")
}

type cindyPricingFixture struct {
	module      *cindy.Module
	unavailable bool
}

func TestCindyUnavailableBlocksTokenCountingAndEmbeddingsBeforeIO(t *testing.T) {
	previous := processExtensionOperations.Load()
	processExtensionOperations.Store(nil)
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	account := &Account{ID: 41, Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	service := &OpenAIGatewayService{}
	for _, endpoint := range []string{"responses-input-tokens", "messages-count-tokens", "embeddings"} {
		t.Run(endpoint, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
			var err error
			switch endpoint {
			case "responses-input-tokens":
				err = service.ForwardResponsesInputTokens(c.Request.Context(), c, account, []byte(`{"model":"gpt-5.6-luna","input":"test"}`))
			case "messages-count-tokens":
				err = service.ForwardCountTokensAsAnthropic(c.Request.Context(), c, account, []byte(`{"model":"gpt-5.6-luna","messages":[]}`), "")
			case "embeddings":
				_, err = service.ForwardEmbeddings(c.Request.Context(), c, account, []byte(`{"model":"test","input":"test"}`), "")
			}
			require.Error(t, err)
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		})
	}
}

func (f *cindyPricingFixture) InvokeOperation(ctx context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if f.unavailable {
		return extensionv1.Result{}, ErrExtensionOperationUnavailable
	}
	return f.module.Invoke(ctx, in)
}

func TestCindyNewTurnRefreshesPricingWithoutChangingPendingBill(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	fixture := &cindyPricingFixture{module: cindy.New()}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: fixture})
	account := &Account{ID: 41, Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	require.NoError(t, fixture.module.ApplyConfig(context.Background(), []byte(`{"catalog_enabled":true}`)))
	first, err := CaptureCindyPricingContext(context.Background(), nil, account)
	require.NoError(t, err)
	pending := CopyProviderPricingContext(first, context.Background())
	require.True(t, cindyCatalogEnabledForBilling(pending, account))
	require.NoError(t, fixture.module.ApplyConfig(context.Background(), []byte(`{"catalog_enabled":false}`)))
	second, err := RefreshCindyPricingContext(first, account)
	require.NoError(t, err)
	require.False(t, cindyCatalogEnabledForBilling(second, account))
	require.True(t, cindyCatalogEnabledForBilling(pending, account))
	fixture.unavailable = true
	_, err = RefreshCindyPricingContext(second, account)
	require.Error(t, err, "a stopped provider cannot start a new request")
	cost, err := calculateCindyCatalogTextCost(NewBillingService(&config.Config{}, nil), " gpt-5.6-luna ", UsageTokens{InputTokens: 100}, 1, "", false, cindyPricingSnapshotFromContext(pending, account))
	require.NoError(t, err, "an already completed request retains its exact pricing reference")
	require.Positive(t, cost.InputCost)
}

func TestCindyPricingScopeCannotReuseAnExcludedAccountPolicyCache(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	module := cindy.New()
	require.NoError(t, module.ApplyConfig(context.Background(), []byte(`{"catalog_enabled":true}`)))
	var calls []extensionv1.Invocation
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		calls = append(calls, in)
		return module.Invoke(context.Background(), in)
	})
	installation := manager.extensions.Load().installations[1]
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: PlatformCindy, AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: 100}}
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityProvider: {"cindy.features", "cindy.pricing"}}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	account := &Account{ID: 41, Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "private-fixture-token"}}
	parent, err := CaptureCindyPricingContext(context.Background(), nil, account)
	require.NoError(t, err)
	require.Len(t, calls, 2)
	for _, call := range calls {
		require.EqualValues(t, 41, call.AccountID)
		require.JSONEq(t, `{}`, string(call.Payload), "scope metadata must not carry the account's credentials")
	}
	pendingBill := CopyProviderPricingContext(parent, context.Background())
	installation.Bindings[0].RolloutPercent = int(stablePluginBucket(account.ID))
	require.Error(t, EnsureCindyProviderAvailable(context.Background(), account))
	_, err = CaptureCindyPricingContext(parent, nil, account)
	require.Error(t, err, "an existing captured value cannot authorize a new excluded request")
	require.Len(t, calls, 2, "revoked scope must fail before the cached policy can be returned")
	require.NotNil(t, cindyPricingSnapshotFromContext(pendingBill, account), "a completed request can still settle using its captured reference")
}

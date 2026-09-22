package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	cindy "github.com/HTExplicit/sub2api-plugins/cindyprovider/catalog"
	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// The pricing reply is serialized from A, then the current provider becomes B.
// Only HTTP is faked: the test enters the real Forward and RecordUsage callers.
type cindySecondReviewSwap struct {
	mu            sync.Mutex
	before, after extensionv1.CindyCatalogSnapshotV1
	pricing       extensionv1.CindyPricingSnapshot
	pricingAfter  *extensionv1.CindyPricingSnapshot
	fallback      extensionv1.OperationInvoker
	swapped       bool
	disabled      bool
	owner         int64
	pricingOwner  int64
	catalogAfter  int
}

func (f *cindySecondReviewSwap) InvokeOperation(ctx context.Context, platform, accountType string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	if in.Capability != extensionv1.CapabilityProvider {
		return f.fallback.InvokeOperation(ctx, platform, accountType, in)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.disabled {
		return extensionv1.Result{}, ErrExtensionOperationDisabled
	}
	owner := f.owner
	if owner == 0 {
		owner = 701
	}
	current := f.before
	if f.swapped {
		current = f.after
	}
	var value any
	switch in.Operation {
	case "cindy.features":
		value = current.Config
	case "cindy.pricing":
		pricing := f.pricing
		if f.swapped && f.pricingAfter != nil {
			pricing = *f.pricingAfter
		}
		raw, err := json.Marshal(pricing)
		f.swapped = true
		if f.pricingOwner != 0 {
			owner = f.pricingOwner
		}
		return extensionv1.Result{PluginID: owner, Payload: raw}, err
	case "cindy.catalog":
		var query extensionv1.CindyCatalogQuery
		if err := json.Unmarshal(in.Payload, &query); err != nil {
			return extensionv1.Result{}, err
		}
		var model string
		if len(query.Args) > 0 {
			_ = json.Unmarshal(query.Args[0], &model)
		}
		switch query.Method {
		case extensionv1.CindyCatalogSnapshotMethodV1:
			current.Images = query.Images
			value = []any{current}
			if f.swapped {
				f.catalogAfter++
			}
		case "CindyCompatibilityMappedUpstreamModel":
			mapped, found := current.CompatibilityMappings[model]
			value = []any{mapped, found}
		case "CindyMappedUpstreamModel":
			mapped, found := current.AvailableMappings[model]
			value = []any{mapped, found}
		case "CindyCompatibilityRoutingTarget":
			found := false
			for _, mapped := range current.CompatibilityMappings {
				found = found || model == mapped
			}
			value = []any{found}
		default:
			return f.fallback.InvokeOperation(ctx, platform, accountType, in)
		}
	default:
		return f.fallback.InvokeOperation(ctx, platform, accountType, in)
	}
	raw, err := json.Marshal(value)
	return extensionv1.Result{PluginID: owner, Payload: raw}, err
}

func newCindySecondReviewSwap(t *testing.T) *cindySecondReviewSwap {
	t.Helper()
	images, _ := currentImageToolsConfig()
	registry := cindy.Registry{Config: extensionv1.CindyProviderConfig{CatalogEnabled: true, BalanceDetection: true}, Images: images}
	before := registry.CatalogSnapshotV1()
	raw, err := json.Marshal(before)
	require.NoError(t, err)
	var after extensionv1.CindyCatalogSnapshotV1
	require.NoError(t, json.Unmarshal(raw, &after))
	var target CindyCapability
	for _, capability := range after.Capabilities {
		if capability.PublicID == "deepseek-v4-flash" {
			target = capability
		}
	}
	require.NotEmpty(t, target.LiveUpstreamID)
	const alias = "gpt-5.4-mini"
	after.Metadata.CatalogVersion = "second-review-b"
	after.CompatibilityAliases[alias] = target.PublicID
	after.CompatibilityMappings[alias] = target.LiveUpstreamID
	after.AvailableMappings[alias] = target.LiveUpstreamID
	after.LegacyLiveMappings[alias] = target.LiveUpstreamID
	_, err = validateCindyCatalogSnapshot(after)
	require.NoError(t, err)
	pricing := registry.PricingSnapshot()
	pricingAfter := registry.PricingSnapshot()
	pricingAfter.CatalogSnapshot = &after
	for _, method := range []string{"CindyTextPricingForModel", "CindyImagePricingForModel", "CindyModelUsesExplicitZeroPrice", "resolveKnownCindyCapability", "CindyCompatibilityRoutingTarget"} {
		pricingAfter.Results[cindy.PricingKey(method, alias)] = pricing.Results[cindy.PricingKey(method, target.PublicID)]
	}
	pricingAfter.Results[cindy.PricingKey("CindyCompatibilityTextPricingForModel", alias)] = pricing.Results[cindy.PricingKey("CindyTextPricingForModel", target.PublicID)]
	for _, id := range []string{target.PublicID, target.LiveUpstreamID} {
		pricingAfter.Results[cindy.PricingKey("CindyCompatibilityTextPricingForModel", id)] = pricing.Results[cindy.PricingKey("CindyTextPricingForModel", target.PublicID)]
	}
	previous := processExtensionOperations.Load()
	require.NotNil(t, previous)
	f := &cindySecondReviewSwap{before: before, after: after, pricing: pricing, pricingAfter: &pricingAfter, fallback: previous.invoker}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: f})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	return f
}

func secondReviewHTTPResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"second-review","status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":10}}`))}
}

func TestCindySecondReviewNewRequestBAndDisabledNoIO(t *testing.T) {
	f := newCindySecondReviewSwap(t)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{secondReviewHTTPResponse(), secondReviewHTTPResponse()}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformOpenAI
	var first context.Context
	for index, want := range []string{f.before.LegacyLiveMappings["gpt-5.4-mini"], f.after.LegacyLiveMappings["gpt-5.4-mini"]} {
		c := cindyHTTPToWSV2TestContext("/v1/responses")
		_, err := svc.Forward(c.Request.Context(), c, account, []byte(`{"model":"gpt-5.4-mini","stream":false,"input":"synthetic"}`))
		require.NoError(t, err)
		require.Equal(t, want, gjson.GetBytes(upstream.bodies[index], "model").String())
		var capability CindyCapability
		var found bool
		require.True(t, queryCindyPricingSnapshot(cindyPricingSnapshotFromContext(c.Request.Context(), account), "resolveKnownCindyCapability", "gpt-5.4-mini", []any{&capability, &found}))
		require.True(t, found)
		require.Equal(t, want, capability.LiveUpstreamID)
		if index == 0 {
			first = c.Request.Context()
		}
	}
	f.disabled = true
	c := cindyHTTPToWSV2TestContext("/v1/responses")
	c.Request = c.Request.WithContext(first)
	_, err := svc.Forward(first, c, account, []byte(`{"model":"gpt-5.4-mini","stream":false,"input":"must-not-send"}`))
	require.Error(t, err)
	require.Len(t, upstream.requests, 2, "a captured A reference never authorizes a disabled new use")
}

func TestCindySecondReviewCompanionValidationAndIdentityIsolation(t *testing.T) {
	for _, mismatch := range []string{"missing", "config", "images", "owner"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newCindySecondReviewSwap(t)
			account := cindyHTTPToWSV2TestAccount()
			switch mismatch {
			case "missing":
				f.pricing.CatalogSnapshot = nil
			case "config":
				f.pricing.Config.CatalogEnabled = !f.pricing.CatalogSnapshot.Config.CatalogEnabled
			case "images":
				f.pricing.CatalogSnapshot.Images.ResponsesImageEnabled = !f.pricing.CatalogSnapshot.Images.ResponsesImageEnabled
			case "owner":
				f.pricingOwner = 702
			}
			_, err := CaptureCindyPricingContext(context.Background(), nil, account)
			require.Error(t, err)
		})
	}
	f := newCindySecondReviewSwap(t)
	account := cindyHTTPToWSV2TestAccount()
	first, err := CaptureCindyPricingContext(context.Background(), nil, account)
	require.NoError(t, err)
	copied := CopyProviderPricingContext(first, context.Background())
	other := *account
	other.ID++
	require.Nil(t, capturedCindyPolicyFromContext(copied, &other))
	otherSnapshot, err := LoadCindyCatalogSnapshot(copied, &other)
	require.NoError(t, err)
	require.Equal(t, f.after.Metadata.CatalogVersion, otherSnapshot.Metadata.CatalogVersion)
	f.owner = 702
	_, err = LoadCindyCatalogSnapshot(copied, account)
	require.Error(t, err, "owner handoff must not certify a previous provider's captured policy")
	require.NotNil(t, cindyPricingSnapshotFromContext(copied, account), "completed A billing keeps its reference despite revocation")
}

func TestCindySecondReviewWSTwoTurnsKeepMappingAndBillingTogether(t *testing.T) {
	f := newCindySecondReviewSwap(t)
	controlCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := newStagedPassthroughConn()
	cfg := passthroughLifecycleConfig()
	cfg.RunMode = config.RunModeSimple
	cfg.Default.RateMultiplier = 1
	svc := newPassthroughLifecycleService(cfg, upstream)
	svc.billingService = NewBillingService(cfg, nil)
	svc.deferredService = &DeferredService{}
	logs := &cindyTextUsageLogRepo{}
	svc.usageLogRepo = logs
	account := passthroughLifecycleAccount()
	account.Credentials["base_url"] = "https://api.laxarouter.ai"
	type turnEvidence struct {
		turn   int
		wire   string
		priced string
		input  float64
		output float64
	}
	results := make(chan turnEvidence, 2)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, controlCtx, svc, account,
		func(c *gin.Context) *OpenAIWSIngressHooks {
			initial, err := CaptureCindyPricingContext(controlCtx, c, account)
			require.NoError(t, err)
			turns := NewProviderPricingTurnContexts(initial, account)
			return &OpenAIWSIngressHooks{
				InitialRequestModel:        "gpt-5.4-mini",
				CopyProviderPricingContext: turns.Copy,
				MapRequestModel:            func(_ int, model string) (string, error) { return model, nil },
				BeforeTurn: func(turn int) error {
					_, err := turns.Copy(turn, controlCtx)
					return err
				},
				AfterTurn: func(turn int, result *OpenAIForwardResult, err error) {
					require.NoError(t, err)
					require.NotNil(t, result)
					worker, captured := turns.Take(turn, context.Background())
					require.True(t, captured)
					var capability CindyCapability
					var found bool
					require.True(t, queryCindyPricingSnapshot(cindyPricingSnapshotFromContext(worker, account), "resolveKnownCindyCapability", "gpt-5.4-mini", []any{&capability, &found}))
					require.True(t, found)
					require.NoError(t, svc.RecordUsage(worker, &OpenAIRecordUsageInput{
						Result: result, APIKey: &APIKey{}, User: &User{ID: 1}, Account: account,
						ChannelUsageFields: ChannelUsageFields{OriginalModel: "gpt-5.4-mini"},
					}))
					require.NotNil(t, logs.last)
					results <- turnEvidence{turn: turn, wire: result.UpstreamModel, priced: capability.LiveUpstreamID, input: logs.last.InputCost, output: logs.last.OutputCost}
				},
			}
		})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.4-mini","stream":false}`)
	defer func() { _ = client.CloseNow() }()
	for index, want := range []string{f.before.LegacyLiveMappings["gpt-5.4-mini"], f.after.LegacyLiveMappings["gpt-5.4-mini"]} {
		if index > 0 {
			writeCtx, done := context.WithTimeout(controlCtx, 3*time.Second)
			require.NoError(t, client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.4-mini","stream":false}`)))
			done()
		}
		request := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
		require.Equal(t, want, gjson.GetBytes(request, "model").String())
		upstream.Send(`{"type":"response.completed","response":{"id":"second-review-turn","model":"` + want + `","status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":10}}}`)
		_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
		require.NoError(t, err)
		select {
		case recorded := <-results:
			require.Equal(t, index+1, recorded.turn)
			require.Equal(t, want, recorded.wire)
			require.Equal(t, want, recorded.priced)
			t.Logf("turn=%d wire=%s priced=%s input_cost=%.12f output_cost=%.12f", recorded.turn, recorded.wire, recorded.priced, recorded.input, recorded.output)
		case <-time.After(3 * time.Second):
			t.Fatal("synthetic turn did not reach real usage recording")
		}
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic WS proxy did not stop")
	}
}

func TestCindySecondReviewForwardKeepsCapturedAliasIdentity(t *testing.T) {
	const alias = "gpt-5.4-mini"
	images, _ := currentImageToolsConfig()
	registry := cindy.Registry{Config: extensionv1.CindyProviderConfig{CatalogEnabled: true, BalanceDetection: true}, Images: images}
	first := registry.CatalogSnapshotV1()
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	var second extensionv1.CindyCatalogSnapshotV1
	require.NoError(t, json.Unmarshal(raw, &second))
	var target CindyCapability
	for _, capability := range second.Capabilities {
		if capability.PublicID == "deepseek-v4-flash" {
			target = capability
		}
	}
	require.NotEmpty(t, target.LiveUpstreamID)
	second.Metadata.CatalogVersion = "second-review-b"
	second.CompatibilityAliases[alias] = target.PublicID
	second.CompatibilityMappings[alias] = target.LiveUpstreamID
	second.AvailableMappings[alias] = target.LiveUpstreamID
	second.LegacyLiveMappings[alias] = target.LiveUpstreamID
	_, err = validateCindyCatalogSnapshot(first)
	require.NoError(t, err, "A is a complete legal snapshot")
	_, err = validateCindyCatalogSnapshot(second)
	require.NoError(t, err, "B is a complete legal snapshot, not an invalid-fixture shortcut")
	previous := processExtensionOperations.Load()
	require.NotNil(t, previous)
	swap := &cindySecondReviewSwap{before: first, after: second, pricing: registry.PricingSnapshot(), fallback: previous.invoker}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: swap})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"second-review","status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":10}}`)),
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	logs := &cindyTextUsageLogRepo{}
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector(),
		billingService: NewBillingService(cfg, nil), usageLogRepo: logs, deferredService: &DeferredService{}}
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformOpenAI
	c := cindyHTTPToWSV2TestContext("/v1/responses")
	result, err := svc.Forward(c.Request.Context(), c, account,
		[]byte(`{"model":"gpt-5.4-mini","stream":false,"input":"synthetic-only"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 1, "exactly one fake HTTP request must reach the actual wire boundary")
	pricing := cindyPricingSnapshotFromContext(c.Request.Context(), account)
	require.NotNil(t, pricing)
	var pricedCapability CindyCapability
	var found bool
	require.True(t, queryCindyPricingSnapshot(pricing, "resolveKnownCindyCapability", alias, []any{&pricedCapability, &found}))
	require.True(t, found)
	worker := CopyProviderPricingContext(c.Request.Context(), context.Background())
	require.NoError(t, svc.RecordUsage(worker, &OpenAIRecordUsageInput{
		Result: result, APIKey: &APIKey{}, User: &User{ID: 1}, Account: account,
		ChannelUsageFields: ChannelUsageFields{OriginalModel: alias},
	}))
	require.NotNil(t, logs.last)
	actual := gjson.GetBytes(upstream.lastBody, "model").String()
	t.Logf("fake_http=%d catalog_after_pricing=%d requested=%s wire=%s priced_identity=%s billing_primary=%s input_cost=%.12f output_cost=%.12f",
		len(upstream.requests), swap.catalogAfter, result.Model, actual, pricedCapability.LiveUpstreamID,
		forwardResultBillingModel(result.Model, result.UpstreamModel), logs.last.InputCost, logs.last.OutputCost)
	require.Equal(t, pricedCapability.LiveUpstreamID, actual,
		"one request must not use B's new alias target while settling A's captured alias identity")
}

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	cindy "github.com/HTExplicit/sub2api-plugins/cindyprovider/catalog"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const nativeSnapshotImageID = "gpt-image-2.5-flare"

type cindyNativeImageSnapshotFixture struct {
	*cindySecondReviewSwap
	calls       []extensionv1.Invocation
	nativeFacts []extensionv1.ImageNativeRequest
}

func (f *cindyNativeImageSnapshotFixture) InvokeOperation(ctx context.Context, platform, accountType string, in extensionv1.Invocation) (extensionv1.Result, error) {
	f.calls = append(f.calls, in)
	if in.Operation == "cindy.catalog" && !f.disabled {
		var query extensionv1.CindyCatalogQuery
		if err := json.Unmarshal(in.Payload, &query); err != nil {
			return extensionv1.Result{}, err
		}
		if query.Method == "ResolveCindyCapability" || query.Method == "CindyModelSupportsEndpoint" {
			current := f.before
			if f.swapped {
				current = f.after
			}
			var model string
			if len(query.Args) > 0 {
				_ = json.Unmarshal(query.Args[0], &model)
			}
			capability, found := nativeSnapshotCapability(current, model)
			var value any
			if query.Method == "ResolveCindyCapability" {
				value = []any{capability, found && capability.PublicModel}
			} else {
				var endpoint CindyEndpoint
				if len(query.Args) > 1 {
					_ = json.Unmarshal(query.Args[1], &endpoint)
				}
				verified := false
				for _, allowed := range capability.VerifiedEndpoints {
					verified = verified || allowed == endpoint
				}
				value = []any{found && capability.PublicModel && current.Config.CatalogEnabled && current.Images.StudioEnabled && verified}
			}
			raw, err := json.Marshal(value)
			owner := f.owner
			if owner == 0 {
				owner = 701
			}
			return extensionv1.Result{PluginID: owner, Payload: raw}, err
		}
	}
	return f.cindySecondReviewSwap.InvokeOperation(ctx, platform, accountType, in)
}

func nativeSnapshotCapability(snapshot extensionv1.CindyCatalogSnapshotV1, model string) (CindyCapability, bool) {
	if target, ok := snapshot.CompatibilityAliases[model]; ok {
		model = target
	}
	for _, capability := range snapshot.Capabilities {
		if model == capability.PublicID || model == capability.LiveUpstreamID {
			return capability, true
		}
	}
	return CindyCapability{}, false
}

func cloneNativeImageCatalog(t *testing.T, snapshot extensionv1.CindyCatalogSnapshotV1) extensionv1.CindyCatalogSnapshotV1 {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var cloned extensionv1.CindyCatalogSnapshotV1
	require.NoError(t, json.Unmarshal(raw, &cloned))
	return cloned
}

func publishNativeImageFixture(t *testing.T, snapshot *extensionv1.CindyCatalogSnapshotV1, revision, live, quality string, price float64) {
	t.Helper()
	var published CindyCapability
	var oldLive string
	for index := range snapshot.Capabilities {
		if snapshot.Capabilities[index].PublicID != nativeSnapshotImageID {
			continue
		}
		capability := snapshot.Capabilities[index]
		oldLive = capability.LiveUpstreamID
		capability.LiveUpstreamID, capability.RegistryID = live, live
		capability.PublicModel = true
		capability.Description = "Synthetic verified native image capability"
		capability.MetadataSourceRevision, capability.PricingSource = revision, revision
		capability.VerifiedEndpoints = []CindyEndpoint{CindyEndpointImagesGenerate}
		capability.ClientSurfaces = []string{CindyClientSurfaceImage, CindyClientSurfaceOpenAI}
		capability.Controls = &CindyCapabilityControls{Generation: &CindyImageRequestControls{
			Sizes: []string{"1024x1024"}, Qualities: []string{quality}, MaxOutputCount: 1,
		}}
		capability.ImagePricing = &CindyImagePricing{OutputCostPerImage: price}
		snapshot.Capabilities[index], published = capability, capability
		break
	}
	require.NotEmpty(t, oldLive, "fixture must update a real inventory image entry")
	for index := range snapshot.CatalogModels {
		model := &snapshot.CatalogModels[index]
		if model.ID == published.PublicID {
			model.LiveUpstreamID, model.PublicModel, model.Verified = live, true, true
			model.Endpoints = append([]CindyEndpoint(nil), published.VerifiedEndpoints...)
			model.SourceRevision = revision
		}
	}
	for index := range snapshot.CodexCapabilities {
		if snapshot.CodexCapabilities[index].PublicID == published.PublicID {
			snapshot.CodexCapabilities[index] = published
		}
	}
	projection := CindyModelCapability{Object: "model_capability", ID: published.PublicID, Kind: published.Kind,
		InputModalities: published.InputModalities, OutputModalities: published.OutputModalities,
		Endpoints: published.VerifiedEndpoints, ClientSurfaces: published.ClientSurfaces,
		AgentWireProtocols: published.AgentWireProtocols, MaxInputTokens: published.MaxInputTokens,
		MaxOutputTokens: published.MaxOutputTokens, PricingSource: published.PricingSource,
		ExplicitZeroPrice: published.ExplicitZeroPrice, Controls: published.Controls}
	replaced := false
	for index := range snapshot.ModelCapabilities {
		if snapshot.ModelCapabilities[index].ID == published.PublicID {
			snapshot.ModelCapabilities[index], replaced = projection, true
		}
	}
	if !replaced {
		snapshot.ModelCapabilities = append(snapshot.ModelCapabilities, projection)
		snapshot.PublicModelIDs = append(snapshot.PublicModelIDs, published.PublicID)
	}
	sort.Strings(snapshot.PublicModelIDs)
	sort.Slice(snapshot.ModelCapabilities, func(i, j int) bool { return snapshot.ModelCapabilities[i].ID < snapshot.ModelCapabilities[j].ID })
	for _, mapping := range []map[string]string{snapshot.AvailableMappings, snapshot.CompatibilityMappings, snapshot.LegacyLiveMappings} {
		for key, value := range mapping {
			if key == oldLive {
				delete(mapping, key)
			} else if value == oldLive {
				mapping[key] = live
			}
		}
		mapping[published.PublicID], mapping[live] = live, live
	}
	snapshot.Metadata.CatalogVersion, snapshot.Metadata.InventoryRevision = revision, revision
	ids := make([]string, 0, len(snapshot.Capabilities))
	for _, capability := range snapshot.Capabilities {
		ids = append(ids, capability.LiveUpstreamID)
	}
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "\n") + "\n"))
	snapshot.Metadata.InventorySHA256 = hex.EncodeToString(digest[:])
	_, err := validateCindyCatalogSnapshot(*snapshot)
	require.NoError(t, err, "A/B fixture must satisfy the complete catalog contract before forwarding")
}

func nativeImagePricingFixture(t *testing.T, registry cindy.Registry, snapshot extensionv1.CindyCatalogSnapshotV1) extensionv1.CindyPricingSnapshot {
	t.Helper()
	pricing := registry.PricingSnapshot()
	pricing.Config = snapshot.Config
	copy := cloneNativeImageCatalog(t, snapshot)
	pricing.CatalogSnapshot = &copy
	capability, found := nativeSnapshotCapability(snapshot, nativeSnapshotImageID)
	require.True(t, found)
	for _, model := range []string{capability.PublicID, capability.LiveUpstreamID} {
		for method, value := range map[string]any{
			"resolveKnownCindyCapability":     []any{capability, true},
			"CindyImagePricingForModel":       []any{*capability.ImagePricing, true},
			"CindyModelUsesExplicitZeroPrice": []any{false},
		} {
			raw, err := json.Marshal(value)
			require.NoError(t, err)
			pricing.Results[cindy.PricingKey(method, model)] = raw
		}
	}
	return pricing
}

func newNativeImageSnapshotFixture(t *testing.T, studioEnabled bool) *cindyNativeImageSnapshotFixture {
	t.Helper()
	registry := cindy.Registry{Config: extensionv1.CindyProviderConfig{CatalogEnabled: true}, Images: extensionv1.ImageToolsConfig{StudioEnabled: studioEnabled}}
	before := registry.CatalogSnapshotV1()
	publishNativeImageFixture(t, &before, "native-image-a", "fixture-a/native-image", "low", 0.125)
	after := cloneNativeImageCatalog(t, before)
	publishNativeImageFixture(t, &after, "native-image-b", "fixture-b/native-image", "high", 0.5)
	pricing, pricingAfter := nativeImagePricingFixture(t, registry, before), nativeImagePricingFixture(t, registry, after)
	previous := processExtensionOperations.Load()
	require.NotNil(t, previous)
	fixture := &cindyNativeImageSnapshotFixture{cindySecondReviewSwap: &cindySecondReviewSwap{
		before: before, after: after, pricing: pricing, pricingAfter: &pricingAfter, fallback: previous.invoker,
	}}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: fixture})
	images := before.Images
	ConfigureImageTools(&images)
	observeNativeImageFacts = func(facts extensionv1.ImageNativeRequest) { fixture.nativeFacts = append(fixture.nativeFacts, facts) }
	t.Cleanup(func() {
		processExtensionOperations.Store(previous)
		ConfigureImageTools(nil)
		observeNativeImageFacts = nil
	})
	return fixture
}

func TestCindyNativeImagesKeepCapturedWireAndControlsAcrossCatalogSwap(t *testing.T) {
	fixture := newNativeImageSnapshotFixture(t, true)
	account := cindyHTTPToWSV2TestAccount()
	upstream := &httpUpstreamRecorder{responses: []*http.Response{openAIImagesJSONResponse(), openAIImagesJSONResponse()}}
	svc := newOpenAIImagesTestService(upstream)
	for index, quality := range []string{"low", "high"} {
		body := []byte(`{"model":"gpt-image-2.5-flare","prompt":"synthetic-private-image-canary","n":1,"size":"1024x1024","quality":"` + quality + `","response_format":"b64_json"}`)
		c, _ := newOpenAIImagesTestContext(t, body)
		parsed, err := svc.ParseOpenAIImagesRequest(c, body)
		require.NoError(t, err)
		fixture.calls = nil
		result, err := svc.ForwardImages(c.Request.Context(), c, account, body, parsed, "")
		require.NoError(t, err)
		capture := capturedCindyPolicyFromContext(c.Request.Context(), account)
		require.NotNil(t, capture)
		capability, found := capture.catalog.Capability(nativeSnapshotImageID)
		require.True(t, found)
		want := []string{"fixture-a/native-image", "fixture-b/native-image"}[index]
		require.Equal(t, want, capability.LiveUpstreamID)
		require.Equal(t, want, gjson.GetBytes(upstream.bodies[index], "model").String(), "wire identity must use the captured companion, not the newly published catalog")
		require.Equal(t, want, result.UpstreamModel)
		require.Equal(t, want, fixture.nativeFacts[index].Capability.LiveUpstreamID)
		var price CindyImagePricing
		var priced bool
		require.True(t, queryCindyPricingSnapshot(capture.snapshot, "CindyImagePricingForModel", nativeSnapshotImageID, []any{&price, &priced}))
		require.True(t, priced)
		require.Equal(t, []float64{0.125, 0.5}[index], price.OutputCostPerImage)
		for _, call := range fixture.calls {
			if call.Capability == extensionv1.CapabilityProvider {
				require.Equal(t, account.ID, call.AccountID, "selected-account policy reads must not fall back to account-free admission")
			}
			if call.Operation == "image.native.validate" {
				require.NotContains(t, string(call.Payload), "synthetic-private-image-canary")
			}
		}
	}
	require.Greater(t, fixture.catalogAfter, 0, "current B was admitted while captured A remained authoritative")
}

func TestCindyNativeImagesUseCapturedAliasTypeAfterCurrentAliasChanges(t *testing.T) {
	fixture := newNativeImageSnapshotFixture(t, true)
	const alias = "fixture-native-image-alias"
	for _, entry := range []struct {
		snapshot *extensionv1.CindyCatalogSnapshotV1
		pricing  *extensionv1.CindyPricingSnapshot
		model    string
	}{
		{&fixture.before, &fixture.pricing, nativeSnapshotImageID},
		{&fixture.after, fixture.pricingAfter, "deepseek-v4-flash"},
	} {
		capability, found := nativeSnapshotCapability(*entry.snapshot, entry.model)
		require.True(t, found)
		entry.snapshot.CompatibilityAliases[alias] = capability.PublicID
		for _, mapping := range []map[string]string{entry.snapshot.AvailableMappings, entry.snapshot.CompatibilityMappings, entry.snapshot.LegacyLiveMappings} {
			mapping[alias] = capability.LiveUpstreamID
		}
		_, err := validateCindyCatalogSnapshot(*entry.snapshot)
		require.NoError(t, err, "alias retargeting must still be a valid independent provider snapshot")
		companion := cloneNativeImageCatalog(t, *entry.snapshot)
		entry.pricing.CatalogSnapshot = &companion
		for _, method := range []string{"CindyTextPricingForModel", "CindyCompatibilityTextPricingForModel", "CindyImagePricingForModel", "CindyModelUsesExplicitZeroPrice", "resolveKnownCindyCapability", "CindyCompatibilityRoutingTarget"} {
			entry.pricing.Results[cindy.PricingKey(method, alias)] = entry.pricing.Results[cindy.PricingKey(method, capability.PublicID)]
		}
	}
	body := []byte(`{"model":"fixture-native-image-alias","prompt":"synthetic","n":1,"size":"1024x1024","quality":"low"}`)
	c, _ := newOpenAIImagesTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err, "shared preselection sees the image alias in A")
	fixture.calls = nil
	result, err := svc.ForwardImages(c.Request.Context(), c, cindyHTTPToWSV2TestAccount(), body, parsed, "")
	require.NoError(t, err, "current alias type B must not replace the captured image type A")
	require.Equal(t, "fixture-a/native-image", result.UpstreamModel)
	require.Equal(t, "fixture-a/native-image", gjson.GetBytes(upstream.lastBody, "model").String())
	for _, call := range fixture.calls {
		if call.Capability == extensionv1.CapabilityProvider {
			require.EqualValues(t, 1, call.AccountID)
		}
	}
}

func TestCindyNativeImagesExistingCaptureStillRequiresFreshAdmission(t *testing.T) {
	for _, state := range []string{"captured-controls", "disabled", "owner-changed"} {
		t.Run(state, func(t *testing.T) {
			fixture := newNativeImageSnapshotFixture(t, true)
			account := cindyHTTPToWSV2TestAccount()
			body := []byte(`{"model":"gpt-image-2.5-flare","prompt":"synthetic","n":1,"size":"1024x1024","quality":"low"}`)
			c, _ := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			captured, err := CaptureCindyPricingContext(c.Request.Context(), c, account)
			require.NoError(t, err)
			switch state {
			case "disabled":
				fixture.disabled = true
			case "owner-changed":
				fixture.owner = 702
			}
			fixture.calls = nil
			result, err := svc.ForwardImages(captured, c, account, body, parsed, "")
			if state == "captured-controls" {
				require.NoError(t, err, "A low-quality control remains authoritative although B now allows high only")
				require.Equal(t, "fixture-a/native-image", result.UpstreamModel)
				require.Equal(t, "low", fixture.nativeFacts[0].Quality)
			} else {
				require.Error(t, err)
				require.Nil(t, result)
				require.Empty(t, upstream.requests, "a captured reference must not bypass fresh admission")
			}
			pricingReads := 0
			for _, call := range fixture.calls {
				if call.Operation == "cindy.pricing" {
					pricingReads++
				}
			}
			require.Zero(t, pricingReads, "an existing capture is not replaced or reread")
			require.NotNil(t, capturedCindyPolicyFromContext(captured, account))
		})
	}
}

func TestCindyNativeImagesKeepDisabledStudioAndNilPreselectionContracts(t *testing.T) {
	t.Run("studio-disabled", func(t *testing.T) {
		fixture := newNativeImageSnapshotFixture(t, false)
		body := []byte(`{"model":"gpt-image-2.5-flare","prompt":"synthetic","n":1,"size":"1024x1024","quality":"low"}`)
		c, _ := newOpenAIImagesTestContext(t, body)
		upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
		svc := newOpenAIImagesTestService(upstream)
		parsed, err := svc.ParseOpenAIImagesRequest(c, body)
		require.NoError(t, err)
		_, err = svc.ForwardImages(c.Request.Context(), c, cindyHTTPToWSV2TestAccount(), body, parsed, "")
		require.Error(t, err)
		require.Empty(t, upstream.requests)
		require.Len(t, fixture.nativeFacts, 1)
		require.False(t, fixture.nativeFacts[0].Verified)
		require.False(t, fixture.before.Images.ResponsesImageEnabled)
	})
	t.Run("nil-preselection", func(t *testing.T) {
		fixture := newNativeImageSnapshotFixture(t, true)
		request := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, N: 1, Size: "1024x1024", Quality: "low", Prompt: "private-preview-canary"}
		require.NoError(t, ValidateCindyImageRequest(nativeSnapshotImageID, request))
		require.Len(t, fixture.nativeFacts, 1)
		for _, call := range fixture.calls {
			require.Zero(t, call.AccountID, "shared preselection deliberately has no selected account")
			require.NotContains(t, string(call.Payload), "private-preview-canary")
		}
	})
}

func TestCindyNativeImagesPreserveExplicitOrdinaryOAuthAndLegacyMappings(t *testing.T) {
	for _, mode := range []string{"ordinary-apikey", "oauth", "setup-token", "legacy-explicit"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newNativeImageSnapshotFixture(t, true)
			fixture.disabled = mode != "legacy-explicit"
			account := newOpenAIImagesAPIKeyAccount()
			model, wire := "gpt-image-2", "gpt-image-2.5-flare"
			switch mode {
			case "oauth", "setup-token":
				account = directImagesTestAccount()
				if mode == "setup-token" {
					account.Type = AccountTypeSetupToken
				}
			case "legacy-explicit":
				account = cindyHTTPToWSV2TestAccount()
				account.Platform = PlatformOpenAI
				model, wire = "gpt-image-legacy-unlisted", "gpt-image-user-pinned"
			}
			account.Credentials["model_mapping"] = map[string]any{model: wire}
			body, err := json.Marshal(map[string]any{"model": model, "prompt": "synthetic"})
			require.NoError(t, err)
			c, _ := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			fixture.calls = nil
			result, err := svc.ForwardImages(c.Request.Context(), c, account, body, parsed, "")
			require.NoError(t, err)
			require.Equal(t, wire, result.UpstreamModel)
			require.Equal(t, wire, gjson.GetBytes(upstream.lastBody, "model").String())
			require.Empty(t, fixture.nativeFacts, "ordinary and legacy identity must not enable strict Cindy image policy")
			if mode != "legacy-explicit" {
				require.Nil(t, capturedCindyPolicyFromContext(c.Request.Context(), account))
				for _, call := range fixture.calls {
					require.False(t, strings.HasPrefix(call.Operation, "cindy."))
				}
			} else {
				require.NotNil(t, capturedCindyPolicyFromContext(c.Request.Context(), account))
			}
		})
	}
}

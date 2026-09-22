package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	cindy "github.com/HTExplicit/sub2api-plugins/cindyprovider/catalog"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type cindyIndependentSnapshotFixture struct {
	snapshot            extensionv1.CindyCatalogSnapshotV1
	disabled            bool
	unsupportedSnapshot bool
	calls               []extensionv1.Invocation
	after               func()
}

func (f *cindyIndependentSnapshotFixture) InvokeOperation(ctx context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	if in.Capability != extensionv1.CapabilityProvider {
		return extensionv1.Result{}, ErrExtensionOperationDisabled
	}
	f.calls = append(f.calls, in)
	if f.disabled {
		return extensionv1.Result{}, ErrExtensionOperationDisabled
	}
	var value any
	if in.Operation == "cindy.features" {
		value = f.snapshot.Config
	} else {
		var query extensionv1.CindyCatalogQuery
		if json.Unmarshal(in.Payload, &query) != nil {
			return extensionv1.Result{}, ErrExtensionOperationUnavailable
		}
		var model string
		if len(query.Args) > 0 {
			_ = json.Unmarshal(query.Args[0], &model)
		}
		switch query.Method {
		case extensionv1.CindyCatalogSnapshotMethodV1:
			if f.unsupportedSnapshot {
				return extensionv1.Result{}, ErrExtensionOperationUnavailable
			}
			copy := f.snapshot
			copy.Images = query.Images
			value = []any{copy}
		case "CindyMappedUpstreamModel":
			mapped, ok := f.snapshot.AvailableMappings[model]
			value = []any{mapped, ok}
		case "CindyCompatibilityMappedUpstreamModel":
			mapped, ok := f.snapshot.CompatibilityMappings[model]
			value = []any{mapped, ok}
		case "CindyCapabilities":
			value = []any{f.snapshot.Capabilities}
		case "CindyPublicModelIDs":
			value = []any{f.snapshot.PublicModelIDs}
		default:
			return extensionv1.Result{}, ErrExtensionOperationUnavailable
		}
	}
	raw, err := json.Marshal(value)
	if f.after != nil {
		f.after()
	}
	return extensionv1.Result{PluginID: 701, Payload: raw}, err
}

func independentCindySnapshot(t *testing.T, suffix string, enabled bool) extensionv1.CindyCatalogSnapshotV1 {
	t.Helper()
	snapshot := (cindy.Registry{Config: extensionv1.CindyProviderConfig{CatalogEnabled: enabled, BalanceDetection: true}}).CatalogSnapshotV1()
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	raw = bytes.ReplaceAll(raw, []byte("gpt-5.6-luna"), []byte("fixture-default-"+suffix))
	snapshot = extensionv1.CindyCatalogSnapshotV1{}
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	snapshot.Metadata.CatalogVersion = "independent-" + suffix
	snapshot.Metadata.InventoryRevision = "fixture-inventory-" + suffix
	ids := make([]string, 0, len(snapshot.Capabilities))
	for _, capability := range snapshot.Capabilities {
		ids = append(ids, capability.LiveUpstreamID)
	}
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "\n") + "\n"))
	snapshot.Metadata.InventorySHA256 = hex.EncodeToString(digest[:])
	_, err = validateCindyCatalogSnapshot(snapshot)
	require.NoError(t, err, "the replacement provider fixture must satisfy the complete V1 contract")
	return snapshot
}

func installIndependentCindyFixture(t *testing.T, f *cindyIndependentSnapshotFixture) {
	t.Helper()
	previous := processExtensionOperations.Load()
	processExtensionOperations.Store(&extensionOperationProvider{invoker: f})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
}

func TestCindyPluginOwnedDefaultAndLegacyWire(t *testing.T) {
	for _, suffix := range []string{"a", "b"} {
		t.Run(suffix+"/default", func(t *testing.T) {
			fixture := &cindyIndependentSnapshotFixture{snapshot: independentCindySnapshot(t, suffix, true)}
			installIndependentCindyFixture(t, fixture)
			account := newCindyNativeMessagesAccount()
			require.Equal(t, "openai/fixture-default-"+suffix, selectResponsesProbeModel(account))
		})
		t.Run(suffix+"/legacy_catalog_off", func(t *testing.T) {
			fixture := &cindyIndependentSnapshotFixture{snapshot: independentCindySnapshot(t, suffix, false)}
			installIndependentCindyFixture(t, fixture)
			account := newCindyNativeMessagesAccount()
			account.Platform = PlatformOpenAI
			account.Extra = map[string]any{"openai_passthrough": true}
			require.Equal(t, "openai/fixture-default-"+suffix, canonicalOpenAIAccountSchedulingModel(account, "fixture-default-"+suffix))
			require.Equal(t, "openai/fixture-default-"+suffix, resolveLegacyCindyOpenAIModel(account, "fixture-default-"+suffix))
		})
	}
}

func TestCindySnapshotIndependentMetadataAndCapturedData(t *testing.T) {
	fixture := &cindyIndependentSnapshotFixture{snapshot: independentCindySnapshot(t, "a", true)}
	installIndependentCindyFixture(t, fixture)
	account := newCindyNativeMessagesAccount()
	first, err := LoadCindyCatalogSnapshot(context.Background(), account)
	require.NoError(t, err)
	fixture.snapshot = independentCindySnapshot(t, "b", true)
	second, err := LoadCindyCatalogSnapshot(context.Background(), account)
	require.NoError(t, err)
	require.NotEqual(t, first.Namespace, second.Namespace)
	require.Equal(t, "independent-a", first.Metadata.CatalogVersion)
	require.Equal(t, "fixture-default-a", first.DefaultTestModel.PublicID)
	require.Equal(t, "independent-b", second.Metadata.CatalogVersion)
	require.Equal(t, "fixture-default-b", second.DefaultTestModel.PublicID)
	for _, call := range fixture.calls {
		require.Equal(t, account.ID, call.AccountID)
	}
	fixture.snapshot.Metadata.CatalogVersion = first.Metadata.CatalogVersion
	third, err := LoadCindyCatalogSnapshot(context.Background(), account)
	require.NoError(t, err)
	require.NotEqual(t, first.Namespace, third.Namespace, "data changes still isolate cache if a provider forgot to bump its human version")
	fixture.disabled = true
	_, err = LoadCindyCatalogSnapshot(context.Background(), account)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	require.Equal(t, "fixture-default-a", first.DefaultTestModel.PublicID, "a failed new read cannot mutate an already captured reply")
}

type cindyIndependentModelsRepo struct {
	AccountRepository
	accounts []Account
	calls    int
}

func (r *cindyIndependentModelsRepo) ListSchedulable(context.Context) ([]Account, error) {
	r.calls++
	return r.accounts, nil
}

func TestCindyIndependentSnapshotCacheAndProjectionUseOneReply(t *testing.T) {
	fixture := &cindyIndependentSnapshotFixture{snapshot: independentCindySnapshot(t, "a", true)}
	installIndependentCindyFixture(t, fixture)
	account := newCindyNativeMessagesAccount()
	ordinary := Account{ID: 502, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"ordinary-only": "ordinary-wire"}}}
	repo := &cindyIndependentModelsRepo{accounts: []Account{*account, ordinary}}
	svc := &GatewayService{accountRepo: repo, modelsListCache: gocache.New(time.Minute, 0), modelsListCacheTTL: time.Minute}
	firstModels := svc.GetAvailableModels(context.Background(), nil, "")
	require.Contains(t, firstModels, "fixture-default-a")
	require.Contains(t, firstModels, "ordinary-only")
	first, err := LoadCindyCatalogSnapshot(context.Background(), account)
	require.NoError(t, err)
	fixture.snapshot = independentCindySnapshot(t, "b", true)
	secondModels := svc.GetAvailableModels(context.Background(), nil, "")
	require.Contains(t, secondModels, "fixture-default-b")
	require.NotContains(t, secondModels, "fixture-default-a")
	require.Equal(t, 2, repo.calls, "the same host cache must reject a different provider content namespace")
	_ = svc.GetAvailableModels(context.Background(), nil, "")
	require.Equal(t, 2, repo.calls, "a matching provider namespace may reuse the account-list result")
	oldManifest, err := BuildCindyCodexModelsManifestSnapshot(first, "")
	require.NoError(t, err)
	require.Contains(t, string(oldManifest.Body), "fixture-default-a")
	require.NotContains(t, string(oldManifest.Body), "fixture-default-b")
	newManifest, err := BuildCindyCodexModelsManifestContext(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, string(newManifest.Body), "fixture-default-b")
	require.NotEqual(t, oldManifest.ETag, newManifest.ETag)
	ordinaryManifest := &OpenAIModelsResponse{Body: []byte(`{"models":[{"slug":"fixture-default-a","display_name":"ordinary-authority","custom":"kept"}]}`)}
	merged, err := MergeCindyCodexModelsManifestSnapshot(ordinaryManifest, "", first)
	require.NoError(t, err)
	require.Contains(t, string(merged.Body), "ordinary-authority")
	require.Contains(t, string(merged.Body), `"custom":"kept"`)
	require.False(t, merged.capacityProtectedModels["fixture-default-a"], "same-name ordinary provider metadata keeps precedence")
	requestA := openAIModelsRequest{accountID: account.ID, cindyCatalogEnabled: true, cindyCatalogVersion: first.Namespace}
	second, err := LoadCindyCatalogSnapshot(context.Background(), account)
	require.NoError(t, err)
	requestB := requestA
	requestB.cindyCatalogVersion = second.Namespace
	require.NotEqual(t, buildOpenAIModelsCacheKey(requestA), buildOpenAIModelsCacheKey(requestB))
	fixture.disabled = true
	withoutCindy := svc.GetAvailableModels(context.Background(), nil, "")
	require.Equal(t, []string{"ordinary-only"}, withoutCindy, "missing capability must not serve the old Cindy cache or block ordinary accounts")
	_, err = BuildCindyCodexModelsManifestContext(context.Background(), "")
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	svc.InvalidateAvailableModelsCache(nil, "")
	require.Empty(t, svc.modelsListCache.Items())
}

func TestCindySnapshotOrdinaryAndPinnedPathsHaveNoProviderDependency(t *testing.T) {
	fixture := &cindyIndependentSnapshotFixture{disabled: true}
	installIndependentCindyFixture(t, fixture)
	account := &Account{ID: 503, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com"}}
	require.Equal(t, openai.DefaultTestModel, selectResponsesProbeModel(account))
	repo := &cindyIndependentModelsRepo{accounts: []Account{*account}}
	svc := &GatewayService{accountRepo: repo, modelsListCache: gocache.New(time.Minute, 0), modelsListCacheTTL: time.Minute}
	require.Empty(t, svc.GetAvailableModels(context.Background(), nil, PlatformOpenAI))
	require.Empty(t, svc.GetAvailableModels(context.Background(), nil, PlatformOpenAI))
	require.Equal(t, 1, repo.calls)
	group := &Group{ID: 91, Platform: PlatformOpenAI, StrictCindyKnown: true, StrictCindy: false}
	strict, err := svc.ClassifyStrictCindyGroup(context.Background(), group)
	require.NoError(t, err)
	require.False(t, strict)
	group.CodexModelsManifestConfig.Enabled = true
	scope, err := (&OpenAIGatewayService{}).ResolveCindyCodexModelsScope(context.Background(), group)
	require.NoError(t, err)
	require.False(t, scope.CatalogOnly)
	require.False(t, scope.MergeCatalog)
	require.Empty(t, fixture.calls, "official/default/pinned-only work must never consult Cindy")
}

func TestCindySnapshotActualAccountAdmissionAndValidation(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	calls := 0
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(extensionv1.Invocation) (extensionv1.Result, error) {
		calls++
		return extensionv1.Result{}, nil
	})
	installation := manager.extensions.Load().installations[1]
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: PlatformCindy, AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: 0}}
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityProvider: {"cindy.catalog"}}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	_, err := LoadCindyCatalogSnapshot(context.Background(), newCindyNativeMessagesAccount())
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	require.Zero(t, calls, "zero-percent actual accounts must not receive policy content")
	for _, enabled := range []bool{false, true} {
		_, err = validateCindyCatalogSnapshot(independentCindySnapshot(t, "valid", enabled))
		require.NoError(t, err, "catalog-off snapshots retain valid descriptive defaults")
	}
	for _, mutate := range []func(*extensionv1.CindyCatalogSnapshotV1){
		func(s *extensionv1.CindyCatalogSnapshotV1) { s.Metadata.InventorySHA256 = strings.Repeat("0", 64) },
		func(s *extensionv1.CindyCatalogSnapshotV1) { s.DefaultTestModel.PublicID = "not-in-inventory" },
		func(s *extensionv1.CindyCatalogSnapshotV1) {
			s.LegacyLiveMappings["arbitrary"] = "https://invalid.example/model"
		},
		func(s *extensionv1.CindyCatalogSnapshotV1) { s.CodexCapabilities[0].DisplayName = "different-snapshot" },
	} {
		snapshot := independentCindySnapshot(t, "invalid", true)
		mutate(&snapshot)
		_, err = validateCindyCatalogSnapshot(snapshot)
		require.Error(t, err)
	}
}

type cindyIndependentNoHTTP struct{ calls int }

func (f *cindyIndependentNoHTTP) Do(*http.Request, string, int64, int) (*http.Response, error) {
	f.calls++
	return nil, errors.New("unexpected fixture HTTP")
}

func (f *cindyIndependentNoHTTP) DoWithTLS(r *http.Request, p string, id int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(r, p, id, c)
}

func TestCindySnapshotLegacyFailureDoesNotSendOrUseSharedFallback(t *testing.T) {
	for _, unavailableContract := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "old_provider_without_v1"}[unavailableContract], func(t *testing.T) {
			fixture := &cindyIndependentSnapshotFixture{snapshot: independentCindySnapshot(t, "a", false), disabled: !unavailableContract, unsupportedSnapshot: unavailableContract}
			installIndependentCindyFixture(t, fixture)
			account := newCindyNativeMessagesAccount()
			account.Platform = PlatformOpenAI
			account.Extra = map[string]any{"openai_passthrough": true}
			upstream := &cindyIndependentNoHTTP{}
			gateway := &OpenAIGatewayService{httpUpstream: upstream}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			body := []byte(`{"model":"gpt-5.4-mini","input":"fixture","store":false}`)
			result, err := gateway.forwardOpenAIPassthrough(c.Request.Context(), c, account, body, body, "gpt-5.4-mini", false, false, time.Now())
			require.Error(t, err)
			require.Nil(t, result)
			require.Zero(t, upstream.calls)
			require.Len(t, fixture.calls, 1)
			require.Equal(t, account.ID, fixture.calls[0].AccountID)
			var query extensionv1.CindyCatalogQuery
			require.NoError(t, json.Unmarshal(fixture.calls[0].Payload, &query))
			require.Equal(t, extensionv1.CindyCatalogSnapshotMethodV1, query.Method)
			_, err = replaceLegacyCindyWSPassthroughSessionModelContext(c.Request.Context(), account, []byte(`{"type":"session.update","session":{"model":"gpt-5.4-mini"}}`))
			require.Error(t, err, "session updates must propagate a missing contract instead of forwarding the original alias")
		})
	}
}

func TestCindySnapshotClientProjectionAndPurposeIsolation(t *testing.T) {
	snapshot := independentCindySnapshot(t, "a", false)
	for _, capability := range snapshot.Capabilities {
		if capability.PublicID == "deepseek-v4-flash" {
			snapshot.DefaultTestModel = extensionv1.CindyModelReference{PublicID: capability.PublicID, LiveUpstreamID: capability.LiveUpstreamID}
		}
	}
	fixture := &cindyIndependentSnapshotFixture{snapshot: snapshot}
	installIndependentCindyFixture(t, fixture)
	account := newCindyNativeMessagesAccount()
	account.Platform = PlatformOpenAI
	_, mapped, err := cindyLegacyLaxaLiveUpstreamModel(context.Background(), account, "deepseek-v4-flash")
	require.NoError(t, err)
	require.False(t, mapped, "changing a default reference must not expand the catalog-off legacy map")
	controller, supported := CindyResponsesImageRoutingModel(context.Background(), "gpt-image-2")
	require.False(t, supported)
	require.Empty(t, controller, "metadata updates never enable the image bridge")
	valid := independentCindySnapshot(t, "a", true)
	_, err = validateCindyCatalogSnapshot(valid)
	require.NoError(t, err)
	valid.ModelCapabilities[0].MaxInputTokens++
	_, err = validateCindyCatalogSnapshot(valid)
	require.Error(t, err, "public capacity must be from the same complete inventory")
}

func TestCindySnapshotAtomicReplyAndDescriptorOnlyCacheChange(t *testing.T) {
	fixture := &cindyIndependentSnapshotFixture{snapshot: independentCindySnapshot(t, "a", true)}
	installIndependentCindyFixture(t, fixture)
	fixture.after = func() {
		fixture.snapshot = independentCindySnapshot(t, "b", true)
		fixture.after = nil
	}
	manifestA, err := BuildCindyCodexModelsManifestContext(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, fixture.calls, 1, "the entire manifest must consume one provider reply even when replacement occurs immediately afterward")
	require.Contains(t, string(manifestA.Body), "fixture-default-a")
	require.NotContains(t, string(manifestA.Body), "fixture-default-b")
	snapshotB, err := LoadCindyCatalogSnapshot(context.Background(), nil)
	require.NoError(t, err)
	metadataB := snapshotB.Metadata
	changedID := fixture.snapshot.Capabilities[0].PublicID
	change := func(capability *CindyCapability) {
		if capability.PublicID == changedID {
			capability.DisplayName = "new provider display without a version bump"
			capability.CodexPresentation.DisplayName = capability.DisplayName
		}
	}
	for i := range fixture.snapshot.Capabilities {
		change(&fixture.snapshot.Capabilities[i])
	}
	for i := range fixture.snapshot.CodexCapabilities {
		change(&fixture.snapshot.CodexCapabilities[i])
	}
	for i := range fixture.snapshot.CatalogModels {
		if fixture.snapshot.CatalogModels[i].ID == changedID {
			fixture.snapshot.CatalogModels[i].DisplayName = "new provider display without a version bump"
		}
	}
	snapshotC, err := LoadCindyCatalogSnapshot(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, metadataB, snapshotC.Metadata, "version, revision and inventory IDs/hash deliberately remain identical")
	require.NotEqual(t, snapshotB.Namespace, snapshotC.Namespace, "the full content hash, not only version or inventory IDs, isolates this change")
	require.True(t, strings.HasPrefix(snapshotC.Namespace, "701:"))
	manifestC, err := BuildCindyCodexModelsManifestSnapshot(snapshotC, "")
	require.NoError(t, err)
	require.Contains(t, string(manifestC.Body), "fixture-default-b")
	require.Contains(t, string(manifestC.Body), "new provider display without a version bump")
}

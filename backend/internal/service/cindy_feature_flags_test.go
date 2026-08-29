package service

import (
	"context"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type cindyFeaturePendingStore struct {
	*stubGatewayCache
	pending map[int64]string
}

func (s *cindyFeaturePendingStore) GetCindyBalancePendingFingerprint(_ context.Context, accountID int64) (string, error) {
	return s.pending[accountID], nil
}

func (s *cindyFeaturePendingStore) HasCindyBalancePendingBatch(_ context.Context, accountIDs []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(accountIDs))
	for _, accountID := range accountIDs {
		if s.pending[accountID] != "" {
			result[accountID] = true
		}
	}
	return result, nil
}

func (s *cindyFeaturePendingStore) ClearCindyBalancePending(_ context.Context, accountID int64) error {
	delete(s.pending, accountID)
	return nil
}

func (s *cindyFeaturePendingStore) ClearCindyBalancePendingIfFingerprintMatches(
	_ context.Context,
	accountID int64,
	fingerprint string,
) error {
	if s.pending[accountID] == fingerprint {
		delete(s.pending, accountID)
	}
	return nil
}

func TestCindyRolloutFlagsAreIndependentlyDisableable(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{name: "balance", env: CindyBalanceDetectionEnabledEnv},
		{name: "catalog", env: CindyCapabilityCatalogEnabledEnv},
		{name: "image", env: ImageStudioEnabledEnv},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			rolloutEnv := []string{
				CindyBalanceDetectionEnabledEnv + "=true",
				CindyCapabilityCatalogEnabledEnv + "=true",
				CindySearchEnabledEnv + "=true",
				ImageStudioEnabledEnv + "=true",
				CindyResponsesImageBridgeEnabledEnv + "=true",
			}
			for index := range rolloutEnv {
				if strings.HasPrefix(rolloutEnv[index], test.env+"=") {
					rolloutEnv[index] = test.env + "=false"
				}
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestCindyRolloutFlagHelper$")
			cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
				CindyBalanceDetectionEnabledEnv,
				CindyCapabilityCatalogEnabledEnv,
				CindySearchEnabledEnv,
				ImageStudioEnabledEnv,
				CindyImageStudioEnabledEnv,
				CindyResponsesImageBridgeEnabledEnv,
				"SUB2API_CINDY_FLAG_HELPER",
			), append(rolloutEnv, "SUB2API_CINDY_FLAG_HELPER="+test.name)...)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated %s rollback failed: %v\n%s", test.name, err, output)
			}
		})
	}
}

func TestCindyRolloutFlagDefaultsAndParsing(t *testing.T) {
	tests := []struct {
		name     string
		env      []string
		expected string
	}{
		{name: "missing", expected: "true,false,false"},
		{name: "empty", env: []string{
			CindyBalanceDetectionEnabledEnv + "=",
			CindyCapabilityCatalogEnabledEnv + "=",
			ImageStudioEnabledEnv + "=",
			CindyImageStudioEnabledEnv + "=",
		}, expected: "true,false,false"},
		{name: "invalid unrelated flags retain defaults", env: []string{
			CindyBalanceDetectionEnabledEnv + "=invalid",
			CindyCapabilityCatalogEnabledEnv + "=invalid",
		}, expected: "true,false,false"},
		{name: "explicit true", env: []string{
			CindyBalanceDetectionEnabledEnv + "=true",
			CindyCapabilityCatalogEnabledEnv + "=true",
			ImageStudioEnabledEnv + "=true",
		}, expected: "true,true,true"},
		{name: "explicit false", env: []string{
			CindyBalanceDetectionEnabledEnv + "=false",
			CindyCapabilityCatalogEnabledEnv + "=false",
			ImageStudioEnabledEnv + "=false",
		}, expected: "false,false,false"},
		{name: "legacy fallback", env: []string{
			CindyCapabilityCatalogEnabledEnv + "=true",
			CindyImageStudioEnabledEnv + "=true",
		}, expected: "true,true,true"},
		{name: "matching declarations", env: []string{
			CindyCapabilityCatalogEnabledEnv + "=true",
			ImageStudioEnabledEnv + "=true",
			CindyImageStudioEnabledEnv + "=true",
		}, expected: "true,true,true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestCindyRolloutFlagParsingHelper$")
			cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
				CindyBalanceDetectionEnabledEnv,
				CindyCapabilityCatalogEnabledEnv,
				ImageStudioEnabledEnv,
				CindyImageStudioEnabledEnv,
				"SUB2API_CINDY_FLAG_PARSE_HELPER",
			), append(test.env, "SUB2API_CINDY_FLAG_PARSE_HELPER="+test.expected)...)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated %s parsing failed: %v\n%s", test.name, err, output)
			}
		})
	}
}

func TestCindyRolloutFlagParsingHelper(t *testing.T) {
	expected := os.Getenv("SUB2API_CINDY_FLAG_PARSE_HELPER")
	if expected == "" {
		t.Skip("subprocess helper")
	}
	got := []string{
		strconv.FormatBool(CindyBalanceDetectionFeatureEnabled()),
		strconv.FormatBool(CindyCapabilityCatalogFeatureEnabled()),
		strconv.FormatBool(CindyImageStudioFeatureEnabled()),
	}
	if strings.Join(got, ",") != expected {
		t.Fatalf("rollout flags: got %s, want %s", strings.Join(got, ","), expected)
	}
}

func TestCindyRolloutFlagHelper(t *testing.T) {
	switch os.Getenv("SUB2API_CINDY_FLAG_HELPER") {
	case "":
		t.Skip("subprocess helper")
	case "balance":
		if CindyBalanceDetectionFeatureEnabled() {
			t.Fatal("balance detection remained enabled")
		}
		if !CindyCapabilityCatalogFeatureEnabled() || !CindyImageStudioFeatureEnabled() {
			t.Fatal("balance rollback changed catalog or image features")
		}
		account := &Account{
			ID:              99001,
			Platform:        PlatformCindy,
			WirePlatform:    WirePlatformOpenAI,
			ProviderProfile: ProviderProfileCindyLaxaV1,
			Type:            AccountTypeAPIKey,
			Status:          StatusActive,
			Schedulable:     true,
			Credentials:     cindyCredentials(),
		}
		if cindyBalanceReplayBufferEnabled(account) {
			t.Fatal("balance rollback still enabled Cindy transport replay buffers")
		}
		for _, probe := range []struct {
			status  int
			payload string
		}{
			{status: http.StatusTooManyRequests, payload: exactCindyBudgetExceededBody},
			{status: http.StatusOK, payload: `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`},
			{status: http.StatusOK, payload: `{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`},
		} {
			if got := ClassifyCindyBalanceInsufficient(account, probe.status, []byte(probe.payload)); got != CindyBalanceSignalNone {
				t.Fatalf("disabled balance classifier returned %v", got)
			}
		}
		gateway := &OpenAIGatewayService{}
		if gateway.handleCindyBalanceTerminalEvent(context.Background(), account, nil, []byte(`{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`)) {
			t.Fatal("disabled stream classifier still handled an exact budget event")
		}
		if gateway.isOpenAIAccountRuntimeBlocked(account) {
			t.Fatal("disabled classifier created a new runtime block")
		}

		// Rollback disables only new detection. The DB marker remains the sole
		// balance authority; pre-v0.1.177 Redis pending state is cleanup-only.
		markedAt := time.Now().UTC()
		account.CindyBalanceInsufficientAt = &markedAt
		if account.IsSchedulable() {
			t.Fatal("existing DB balance marker became schedulable")
		}
		account.CindyBalanceInsufficientAt = nil
		fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
		if err != nil {
			t.Fatalf("fingerprint current Cindy account: %v", err)
		}
		store := &cindyFeaturePendingStore{
			stubGatewayCache: &stubGatewayCache{},
			pending:          map[int64]string{account.ID: fingerprint},
		}
		gateway = &OpenAIGatewayService{cache: store}
		if gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-luna") {
			t.Fatal("legacy pending marker overrode the unmarked DB state during rollback")
		}
		if store.pending[account.ID] != "" {
			t.Fatal("legacy pending marker was not cleaned during rollback")
		}
	case "catalog":
		if CindyCapabilityCatalogFeatureEnabled() || !CindyImageStudioFeatureEnabled() {
			t.Fatal("catalog rollback changed an independent surface")
		}
		if !CindyBalanceDetectionFeatureEnabled() {
			t.Fatal("catalog rollback changed balance detection")
		}
		if _, ok := ResolveCindyCapability("gpt-5.6-luna"); ok || len(CindyPublicModelIDs()) != 0 {
			t.Fatal("catalog rollback still exposed Cindy capabilities")
		}

		group := &Group{ID: 99101, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, StrictCindyKnown: true, StrictCindy: true}
		strict, err := classifyAuthenticatedStrictCindyGroup(context.Background(), nil, group)
		if err != nil || strict {
			t.Fatalf("catalog rollback retained strict routing gate: strict=%v err=%v", strict, err)
		}

		account := &Account{
			ID:              99102,
			Platform:        PlatformCindy,
			WirePlatform:    WirePlatformOpenAI,
			ProviderProfile: ProviderProfileCindyLaxaV1,
			Type:            AccountTypeAPIKey,
			Extra:           map[string]any{"openai_responses_supported": true},
			Credentials: map[string]any{
				"api_key":  "not-exposed",
				"base_url": "https://api.laxarouter.ai",
				"model_mapping": map[string]any{
					"legacy-model": "legacy-upstream-model",
					"gpt-5.6-sol":  "openai/gpt-5.6-sol",
					"gpt-5.6-luna": "openai/gpt-5.6-luna",
				},
			},
		}
		if !account.IsModelSupported("legacy-model") {
			t.Fatal("catalog rollback did not restore legacy account model mapping")
		}
		if got := account.GetMappedModel("legacy-model"); got != "legacy-upstream-model" {
			t.Fatalf("catalog rollback resolved model to %q, want legacy-upstream-model", got)
		}
		for requested, want := range map[string]string{
			"gpt-5.6-sol":  "openai/gpt-5.6-sol",
			"gpt-5.6-luna": "openai/gpt-5.6-luna",
		} {
			if !account.IsModelSupported(requested) {
				t.Fatalf("catalog rollback rejected configured stable Cindy model %q", requested)
			}
			if got := account.GetMappedModel(requested); got != want {
				t.Fatalf("catalog rollback resolved stable Cindy model %q to %q, want %q", requested, got, want)
			}
		}
		if CindyFreePoolModelSupportsEndpoint("gpt-5.6-sol", CindyEndpointResponses) {
			t.Fatal("catalog rollback restored paid Sol routing from stored account data")
		}
		if !CindyFreePoolModelSupportsEndpoint("gpt-5.6-luna", CindyEndpointResponses) {
			t.Fatal("catalog rollback removed permanent free Luna routing")
		}
		if _, ok := CindyCompatibilityMappedUpstreamModel("gpt-5.4"); ok {
			t.Fatal("catalog rollback exposed compatibility alias for a restricted target")
		}
		for requested, want := range map[string]string{
			"gpt-5.4-mini": "openai/gpt-5.6-luna",
		} {
			mapped, ok := CindyCompatibilityMappedUpstreamModel(requested)
			if !ok || mapped != want {
				t.Fatalf("catalog rollback compatibility helper mapped %q to %q, ok=%v, want %q", requested, mapped, ok, want)
			}
			if account.IsModelSupported(requested) {
				t.Fatalf("account layer accepted unresolved Cindy compatibility alias %q", requested)
			}
			if got := account.GetMappedModel(requested); got != requested {
				t.Fatalf("account layer rewrote unresolved alias %q to %q", requested, got)
			}
			if !account.IsModelSupported(want) || account.GetMappedModel(want) != want {
				t.Fatalf("account layer rejected resolved Cindy compatibility target %q", want)
			}
		}
		billingService := NewBillingService(&config.Config{}, nil)
		cost, err := (&OpenAIGatewayService{billingService: billingService}).calculateOpenAIRecordUsageTokenCost(
			context.Background(),
			&APIKey{},
			account,
			"gpt-5.4-mini",
			1,
			time.Time{},
			UsageTokens{InputTokens: 100, OutputTokens: 10},
			"",
			boolPtr(false),
		)
		if err != nil {
			t.Fatalf("catalog rollback lost Cindy compatibility alias pricing: %v", err)
		}
		if got, want := cost.InputCost, 100*0.2e-6; math.Abs(got-want) > 1e-12 {
			t.Fatalf("catalog rollback alias input cost = %g, want %g", got, want)
		}
		if got, want := cost.OutputCost, 10*1.2e-6; math.Abs(got-want) > 1e-12 {
			t.Fatalf("catalog rollback alias output cost = %g, want %g", got, want)
		}
		ordinary := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://ordinary.example.invalid",
			},
		}
		if got := ordinary.GetMappedModel("gpt-5.4-mini"); got != "gpt-5.4-mini" {
			t.Fatalf("ordinary OpenAI account received Cindy alias mapping %q", got)
		}
		for _, capability := range []OpenAIEndpointCapability{
			OpenAIEndpointCapabilityResponses,
			OpenAIEndpointCapabilityChatCompletions,
			OpenAIEndpointCapabilityMessages,
		} {
			if accountSupportsOpenAICapabilities(context.Background(), account, "legacy-model", capability, "") {
				t.Fatalf("catalog rollback restored removed legacy %s routing", capability)
			}
			if !accountSupportsOpenAICapabilities(context.Background(), account, "gpt-5.6-luna", capability, "") {
				t.Fatalf("catalog rollback rejected free Luna %s routing", capability)
			}
		}
		routingContext := WithOpenAICindyRequestedModel(context.Background(), "catalog-native-model")
		if got := openAIRequestedModelForAccount(routingContext, account, "legacy-model"); got != "legacy-model" {
			t.Fatalf("catalog rollback retained native Messages model %q", got)
		}
	case "image":
		if !CindyCapabilityCatalogFeatureEnabled() || CindyImageStudioFeatureEnabled() {
			t.Fatal("image rollback did not remain independent from text catalog")
		}
		if _, ok := ResolveCindyCapability("gpt-5.6-luna"); !ok {
			t.Fatal("image rollback hid a text capability")
		}
		if _, ok := ResolveCindyCapability("gpt-image-2"); ok {
			t.Fatal("image rollback exposed a model unavailable to the free-key pool")
		}
		if len(CindyImageModelCapabilities()) != 0 {
			t.Fatal("image rollback still exposed Image Studio capabilities")
		}
		if CindyModelSupportsResponsesImageBridge("gpt-image-2") {
			t.Fatal("image rollback exposed a bridge unavailable to the free-key pool")
		}
	default:
		t.Fatalf("unknown helper mode %q", os.Getenv("SUB2API_CINDY_FLAG_HELPER"))
	}
}

func withoutEnvironmentKeys(environment []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[strings.ToUpper(key)] = struct{}{}
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[strings.ToUpper(key)]; found {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

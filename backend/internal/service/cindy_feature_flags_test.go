package service

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

type cindyFeaturePendingStore struct {
	*stubGatewayCache
	pending map[int64]string
}

func (s *cindyFeaturePendingStore) MarkCindyBalancePending(_ context.Context, accountID int64, fingerprint string) error {
	s.pending[accountID] = fingerprint
	return nil
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
		{name: "image", env: CindyImageStudioEnabledEnv},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			rolloutEnv := []string{
				CindyBalanceDetectionEnabledEnv + "=true",
				CindyCapabilityCatalogEnabledEnv + "=true",
				CindyImageStudioEnabledEnv + "=true",
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
				CindyImageStudioEnabledEnv,
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
			CindyImageStudioEnabledEnv + "=",
		}, expected: "true,false,false"},
		{name: "invalid", env: []string{
			CindyBalanceDetectionEnabledEnv + "=invalid",
			CindyCapabilityCatalogEnabledEnv + "=invalid",
			CindyImageStudioEnabledEnv + "=invalid",
		}, expected: "true,false,false"},
		{name: "explicit true", env: []string{
			CindyBalanceDetectionEnabledEnv + "=true",
			CindyCapabilityCatalogEnabledEnv + "=true",
			CindyImageStudioEnabledEnv + "=true",
		}, expected: "true,true,true"},
		{name: "explicit false", env: []string{
			CindyBalanceDetectionEnabledEnv + "=false",
			CindyCapabilityCatalogEnabledEnv + "=false",
			CindyImageStudioEnabledEnv + "=false",
		}, expected: "false,false,false"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestCindyRolloutFlagParsingHelper$")
			cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
				CindyBalanceDetectionEnabledEnv,
				CindyCapabilityCatalogEnabledEnv,
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
			ID:          99001,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Credentials: cindyCredentials(),
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
		if IsAmbiguousCindyBalanceTerminalEvent(account, []byte(`{"type":"response.failed","response":{"error":{"message":"request failed"}}}`)) {
			t.Fatal("disabled balance detection still scheduled an ambiguous recheck")
		}
		gateway := &OpenAIGatewayService{}
		if gateway.handleCindyBalanceTerminalEvent(context.Background(), account, nil, []byte(`{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`)) {
			t.Fatal("disabled stream classifier still handled an exact budget event")
		}
		if gateway.isOpenAIAccountRuntimeBlocked(account) {
			t.Fatal("disabled classifier created a new runtime block")
		}

		// Rollback disables only new detection. Existing DB and Redis state must
		// remain fail-closed until explicit administrative recovery.
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
		if !gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-luna") {
			t.Fatal("existing durable pending marker was ignored during rollback")
		}
	case "catalog":
		if CindyCapabilityCatalogFeatureEnabled() || CindyImageStudioFeatureEnabled() {
			t.Fatal("catalog rollback did not disable catalog-dependent surfaces")
		}
		if !CindyBalanceDetectionFeatureEnabled() {
			t.Fatal("catalog rollback changed balance detection")
		}
		if _, ok := ResolveCindyCapability("gpt-5.6-luna"); ok || len(CindyPublicModelIDs()) != 0 {
			t.Fatal("catalog rollback still exposed Cindy capabilities")
		}

		group := &Group{ID: 99101, Platform: PlatformOpenAI, StrictCindyKnown: true, StrictCindy: true}
		strict, err := classifyAuthenticatedStrictCindyGroup(context.Background(), nil, group)
		if err != nil || strict {
			t.Fatalf("catalog rollback retained strict routing gate: strict=%v err=%v", strict, err)
		}

		account := &Account{
			ID:       99102,
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Extra:    map[string]any{"openai_responses_supported": true},
			Credentials: map[string]any{
				"api_key":  "not-exposed",
				"base_url": "https://api.laxarouter.ai",
				"model_mapping": map[string]any{
					"legacy-model": "legacy-upstream-model",
				},
			},
		}
		if !account.IsModelSupported("legacy-model") {
			t.Fatal("catalog rollback did not restore legacy account model mapping")
		}
		if got := account.GetMappedModel("legacy-model"); got != "legacy-upstream-model" {
			t.Fatalf("catalog rollback resolved model to %q, want legacy-upstream-model", got)
		}
		for _, capability := range []OpenAIEndpointCapability{
			OpenAIEndpointCapabilityResponses,
			OpenAIEndpointCapabilityChatCompletions,
			OpenAIEndpointCapabilityMessages,
		} {
			if !accountSupportsOpenAICapabilities(context.Background(), account, "legacy-model", capability, "") {
				t.Fatalf("catalog rollback rejected legacy %s routing", capability)
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
			t.Fatal("image rollback still exposed an image capability")
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

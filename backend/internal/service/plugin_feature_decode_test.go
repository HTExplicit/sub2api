package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type featureDecodeFixture struct{ payload json.RawMessage }

func (f featureDecodeFixture) InvokeOperation(context.Context, string, string, extensionv1.Invocation) (extensionv1.Result, error) {
	return extensionv1.Result{Payload: f.payload}, nil
}

func withFeatureDecodeFixture(t *testing.T, payload string) {
	t.Helper()
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: featureDecodeFixture{json.RawMessage(payload)}})
}

func TestPluginFeatureConfigsDiscardPartiallyDecodedFlags(t *testing.T) {
	t.Run("cindy", func(t *testing.T) {
		withFeatureDecodeFixture(t, `{"balance_detection":true,"catalog_enabled":"invalid","search_enabled":true}`)
		value, ok := currentCindyProviderConfig()
		require.False(t, ok)
		require.Equal(t, extensionv1.CindyProviderConfig{}, value)
		require.False(t, CindyBalanceDetectionFeatureEnabled())
		require.False(t, CindySearchFeatureEnabled())
	})
	for _, payload := range []string{
		`{"studio_enabled":true,"responses_image_enabled":"invalid"}`,
		`{"studio_enabled":"invalid","responses_image_enabled":true}`,
	} {
		t.Run(payload, func(t *testing.T) {
			withFeatureDecodeFixture(t, payload)
			value, ok := currentImageToolsConfig()
			require.False(t, ok)
			require.Equal(t, extensionv1.ImageToolsConfig{}, value)
			require.False(t, ImageStudioFeatureEnabled())
			require.False(t, CindyResponsesImageBridgeFeatureEnabled())
		})
	}
}

func TestCindyResponseDecisionDiscardsPartialDecode(t *testing.T) {
	for _, payload := range []string{`{"health":1,"balance":"invalid"}`, `{"balance":1,"health":"invalid"}`} {
		t.Run(payload, func(t *testing.T) {
			withFeatureDecodeFixture(t, payload)
			account := cindyHealthScopeAccount(7)
			body := []byte(`{"error":{"code":"budget_exceeded"}}`)
			require.Equal(t, CindyHealthSignalNone, ClassifyCindyHealthSignal(account, http.StatusTooManyRequests, body))
			require.Equal(t, CindyBalanceSignalNone, ClassifyCindyBalanceInsufficient(account, http.StatusTooManyRequests, body))
		})
	}
}

func TestPluginFeatureConfigsKeepValidExplicitFlags(t *testing.T) {
	t.Run("cindy", func(t *testing.T) {
		withFeatureDecodeFixture(t, `{"balance_detection":true,"catalog_enabled":false,"search_enabled":true}`)
		value, ok := currentCindyProviderConfig()
		require.True(t, ok)
		require.Equal(t, extensionv1.CindyProviderConfig{BalanceDetection: true, SearchEnabled: true}, value)
	})
	t.Run("image", func(t *testing.T) {
		withFeatureDecodeFixture(t, `{"studio_enabled":true,"responses_image_enabled":false}`)
		value, ok := currentImageToolsConfig()
		require.True(t, ok)
		require.Equal(t, extensionv1.ImageToolsConfig{StudioEnabled: true}, value)
	})
}

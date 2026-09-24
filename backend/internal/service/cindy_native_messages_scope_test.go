//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cindy "github.com/Wei-Shaw/sub2api/internal/cindyprovider/catalog"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCindyNativeMessagesUsesActualAccountAdmissionAndCapturedPricing(t *testing.T) {
	// Image tool switches are host state now; keep them empty so only scope metadata is compared.
	ConfigureImageTools(&extensionv1.ImageToolsConfig{})
	t.Cleanup(func() { ConfigureImageTools(nil) })
	for _, tc := range []struct {
		name        string
		accountID   int64
		unavailable bool
		nonCindy    bool
		allowed     bool
	}{
		{name: "policy_unavailable", accountID: 2, unavailable: true},

		{name: "native_account", accountID: 2, allowed: true},
		{name: "non_cindy_keeps_identity_error", accountID: 2, nonCindy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := captureNativeCindyTestInvoker()
			t.Cleanup(func() { restoreNativeCindyTestInvoker(previous) })
			module := cindy.New()
			require.NoError(t, module.ApplyConfig(context.Background(), []byte(`{"catalog_enabled":true}`)))
			var invocations []extensionv1.Invocation
			setNativeCindyTestInvoker(nativeCindyTestInvoker(func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
				if tc.unavailable {
					return extensionv1.Result{}, ErrExtensionOperationDisabled
				}
				invocations = append(invocations, in)
				return module.Invoke(ctx, in)
			}))
			account := newCindyNativeMessagesAccount()
			account.ID = tc.accountID
			if tc.nonCindy {
				account.Platform = PlatformOpenAI
				account.ProviderProfile = ""
				account.Credentials["base_url"] = "https://api.openai.com"
			}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"scope-native","type":"message","role":"assistant","model":"google/gemini-3.6-flash","content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":3,"output_tokens":1}}`)),
			}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			result, err := newCindyNativeMessagesService(upstream).ForwardCindyAnthropicMessages(c.Request.Context(), c, account,
				[]byte(`{"model":"gemini-3.6-flash","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`), "gemini-3.6-flash")
			if !tc.allowed {
				if tc.nonCindy {
					assert.EqualError(t, err, "strict Cindy account is required for native Messages passthrough")
					require.Empty(t, invocations, "the original identity guard still precedes plugin admission")
				} else {
					assert.ErrorContains(t, err, "Cindy provider policy is unavailable")
				}
				assert.Nil(t, result)
				require.Zero(t, upstream.calls, "rejected Cindy accounts must not reach either HTTP method")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 1, upstream.calls)
			require.Equal(t, 3, result.Usage.InputTokens)
			require.Equal(t, "gemini-3.6-flash", result.Model)
			require.Equal(t, "google/gemini-3.6-flash", result.UpstreamModel)
			features, pricing := 0, 0
			for _, invocation := range invocations {
				if invocation.Operation == "cindy.features" || invocation.Operation == "cindy.pricing" {
					require.Equal(t, account.ID, invocation.AccountID)
					require.JSONEq(t, `{}`, string(invocation.Payload), "only host scope metadata crosses the boundary")
					if invocation.Operation == "cindy.features" {
						features++
					} else {
						pricing++
					}
				}
			}
			require.Equal(t, 1, features)
			require.Equal(t, 1, pricing)
			captured := cindyPricingSnapshotFromContext(c.Request.Context(), account)
			require.NotNil(t, captured)
			require.True(t, captured.Config.CatalogEnabled)
			require.Same(t, captured, cindyPricingSnapshotFromContext(upstream.lastReq.Context(), account))
			pendingBill := CopyProviderPricingContext(c.Request.Context(), context.Background())
			require.NoError(t, module.ApplyConfig(context.Background(), []byte(`{"catalog_enabled":false}`)))
			require.Same(t, captured, cindyPricingSnapshotFromContext(pendingBill, account))
			require.True(t, captured.Config.CatalogEnabled, "later configuration must not replace an admitted request's price source")
			other := *account
			other.ID++
			require.Nil(t, cindyPricingSnapshotFromContext(pendingBill, &other))
		})
	}
}

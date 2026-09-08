package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountCapabilityDiscoveryDistinguishesFreshEmptyUnsupportedAndErrors(t *testing.T) {
	tests := []struct {
		name, body, status, classification string
		httpStatus, modelCount             int
	}{
		{"empty object envelope", `{"data":[]}`, "empty", "catalog_empty", 200, 0},
		{"empty array", `[]`, "empty", "catalog_empty", 200, 0},
		{"empty models envelope", `{"models":[]}`, "empty", "catalog_empty", 200, 0},
		{"null is not empty", `null`, "failed", "invalid_catalog", 200, 0},
		{"null list is not empty", `{"data":null}`, "failed", "invalid_catalog", 200, 0},
		{"no catalog", `{}`, "failed", "invalid_catalog", 200, 0},
		{"error200", `{"error":{"code":"invalid_api_key","message":"private-secret"}}`, "failed", "invalid_catalog", 200, 0},
		{"noncatalog200", `{"data":[{"display_name":"missing ID"}]}`, "failed", "invalid_catalog", 200, 0},
		{"HTML", `<html>private-secret</html>`, "failed", "invalid_catalog", 200, 0},
		{"unsupported404", `{"error":{"message":"private-secret"}}`, "unsupported", "catalog_unsupported", 404, 0},
		{"unsupported405", ``, "unsupported", "catalog_unsupported", 405, 0},
		{"invalidkey401", `{"error":{"code":"invalid_api_key","message":"private-secret"}}`, "failed", "credential_invalid", 401, 0},
		{"upstream503", `{"error":{"message":"private-secret"}}`, "failed", "upstream_unavailable", 503, 0},
		{"page with continuation", `{"data":[{"id":"Exact"}],"has_more":true}`, "partial", "catalog_partial", 200, 1},
		{"next page token", `{"models":[{"id":"Exact"}],"nextPageToken":"private-secret"}`, "partial", "catalog_partial", 200, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := capabilityProbeTestAccount()
			before, err := json.Marshal(account)
			require.NoError(t, err)
			upstream := &capabilityProbeFakeUpstream{do: func(req *http.Request, _ []byte, count int) (*http.Response, error) {
				require.Equal(t, 1, count, "no models.dev lookup or fallback request is allowed")
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, "https://relay.example.test/prefix/v1/models", req.URL.String())
				require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
				return capabilityProbeResponse(test.httpStatus, test.body, false), nil
			}}
			result := capabilityProbeTestService(upstream).Discover(context.Background(), account)
			require.Equal(t, test.status, result.Status)
			require.Equal(t, test.classification, result.Classification)
			require.Equal(t, test.httpStatus, result.HTTPStatus)
			require.Equal(t, 1, result.RequestCount)
			require.Len(t, result.Models, test.modelCount)
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			wire, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(wire), "private-secret")
			require.NotContains(t, string(wire), "Unwanted-VIP")
		})
	}
}

func TestAccountCapabilityDiscoveryPreservesExactIDsAndDoesNotPopulateMappings(t *testing.T) {
	account := capabilityProbeTestAccount()
	upstream := &capabilityProbeFakeUpstream{do: func(*http.Request, []byte, int) (*http.Response, error) {
		return capabilityProbeResponse(200, `{"data":[{"id":"models/ExactCase-SsVIP","display_name":"Exact Display"},{"id":"Vendor/Case"},{"id":"vendor/case","display_name":null},{"id":"Vendor/Case","display_name":"duplicate"},{"id":"Short","display_name":"  "}]}`, false), nil
	}}
	result := capabilityProbeTestService(upstream).Discover(context.Background(), account)
	require.Equal(t, "discovered", result.Status)
	require.Equal(t, []AccountCapabilityDiscoveredModel{
		{ID: "Short", DisplayName: "Short"},
		{ID: "Vendor/Case", DisplayName: "Vendor/Case"},
		{ID: "models/ExactCase-SsVIP", DisplayName: "Exact Display"},
		{ID: "vendor/case", DisplayName: "vendor/case"},
	}, result.Models)
	require.Equal(t, map[string]any{"PublicModel": "Unwanted-VIP"}, account.Credentials["model_mapping"])
	require.Nil(t, account.GetUpstreamModelMetadataSnapshot())
}

func TestAccountCapabilityDiscoveryOAuthIsNotRefreshed(t *testing.T) {
	account := capabilityProbeTestAccount()
	account.Type = AccountTypeOAuth
	upstream := &capabilityProbeFakeUpstream{do: func(*http.Request, []byte, int) (*http.Response, error) {
		t.Fatal("OAuth refresh/catalog call must not run")
		return nil, nil
	}}
	result := capabilityProbeTestService(upstream).Discover(context.Background(), account)
	require.Equal(t, "unsupported", result.Status)
	require.Zero(t, result.RequestCount)
	require.Empty(t, upstream.requests)
}

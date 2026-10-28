package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSyncUpstreamModelCatalogFollowsAnthropicPagination(t *testing.T) {
	repo := &upstreamModelMetadataRepoStub{}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-a","max_input_tokens":200000,"max_tokens":64000}],"has_more":true,"last_id":"claude-a"}`))},
		{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-b","max_input_tokens":1000000}],"has_more":false,"last_id":"claude-b"}`))},
		{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(`{"error":"unavailable"}`))},
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	account := &Account{ID: 85, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://api.anthropic.com"}}
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-a", "claude-b"}, catalog.Models)
	require.GreaterOrEqual(t, len(upstream.requests), 2)
	require.Equal(t, "1000", upstream.requests[0].URL.Query().Get("limit"))
	require.Equal(t, "claude-a", upstream.requests[1].URL.Query().Get("after_id"))
}

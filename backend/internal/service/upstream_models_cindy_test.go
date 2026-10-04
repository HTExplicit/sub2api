package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

var cindyFreeConversationIDs = []string{
	"deepseek/deepseek-v4-flash", "deepseek/deepseek-v4-flash-vision-exp", "deepseek/deepseek-v4-pro",
	"google/gemini-3.6-flash", "z-ai/glm-5.3-flash", "openai/gpt-5.6-luna", "tencent/hy3",
	"qwen/qwen3.8-27b", "qwen/qwen3.8-flash", "deepseek/deepseek-flash", "qwen/qwen3.8-omni-flash",
	"openai/gpt-6-luna", "meta/muse-spark-1.3", "moonshotai/kimi-k2.8-preview", "tencent/hy4-preview",
	"google/gemini-3.8-flash", "z-ai/glm-5.3-flashx", "anthropic/claude-sonnet-5-5",
}

func cindyMetadataFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/cindy_model_metadata_v4.json")
	require.NoError(t, err)
	return body
}

func cindySyncFixture(t *testing.T, id int64) (*AccountTestService, *Account, *upstreamModelMetadataRepoStub, *httpUpstreamRecorder) {
	t.Helper()
	rows := make([]map[string]any, 0, 20)
	for _, model := range append(append([]string{}, cindyFreeConversationIDs...), "cindy/auto-review", "cindy/web-search") {
		row := map[string]any{"id": model}
		if model == "deepseek/deepseek-flash" {
			row["max_input_tokens"], row["max_output_tokens"] = 900000, 12345
		}
		rows = append(rows, row)
	}
	body, err := json.Marshal(map[string]any{"data": rows})
	require.NoError(t, err)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))},
		{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(cindyMetadataFixture(t))))},
	}}
	repo := &upstreamModelMetadataRepoStub{}
	account := &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "private-account-key"}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	return svc, account, repo, upstream
}

func TestCindyModelCatalogAuthenticatedMembershipAndMetadata(t *testing.T) {
	svc, account, repo, upstream := cindySyncFixture(t, 17)
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 20)
	require.Len(t, catalog.Metadata, 18)
	require.Empty(t, catalog.Warnings)
	require.NotContains(t, catalog.Models, "openai/gpt-6-astra", "public metadata cannot grant access")
	require.NotContains(t, catalog.Metadata, "cindy/web-search")
	require.Len(t, upstream.requests, 2)
	require.Equal(t, cindyModelMetadataRegistryURL, upstream.requests[1].URL.String())
	require.Empty(t, upstream.requests[1].Header.Get("Authorization"))
	require.Empty(t, upstream.requests[1].Header.Get("x-api-key"))
	for _, id := range cindyFreeConversationIDs {
		require.True(t, upstreamModelMetadataIsComplete(catalog.Metadata[id]), id)
	}
	declared := catalog.Metadata["deepseek/deepseek-flash"]
	require.Equal(t, int64(900000), declared.MaxInputTokens)
	require.Equal(t, int64(12345), declared.MaxOutputTokens)
	require.Zero(t, declared.ContextWindow, "capacity declarations are not mixed")
	require.Equal(t, ModelContextSourceUpstream, declared.CapacitySource)
	registry := catalog.Metadata["qwen/qwen3.8-omni-flash"]
	require.Equal(t, ModelContextSourceRegistry, registry.CapacitySource)
	require.Contains(t, registry.InputModalities, "audio")
	require.Equal(t, "xhigh", registry.DefaultReasoningLevel)
	require.Zero(t, catalog.Metadata["moonshotai/kimi-k2.8-preview"].MaxOutputTokens)
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	require.Equal(t, UpstreamModelMetadataSourceIdentity(account), snapshot.SourceIdentity)
	require.Equal(t, "cindy-model-access", snapshot.Source)
	require.Contains(t, repo.updates, UpstreamModelMetadataExtraKey)
}

func TestCindyModelCatalogPreviewDoesNotPersist(t *testing.T) {
	svc, account, repo, _ := cindySyncFixture(t, 0)
	_, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Nil(t, repo.updates)
}

func TestCindyModelCatalogFailedRegistryPreservesSnapshot(t *testing.T) {
	svc, account, _, upstream := cindySyncFixture(t, 17)
	_, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	before, ok := account.GetUpstreamModelMetadata("qwen/qwen3.8-omni-flash")
	require.True(t, ok)
	svc.cindyMetadataRegistryAt = time.Now().Add(-7 * time.Hour)
	upstream.responses = []*http.Response{
		{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"qwen/qwen3.8-omni-flash"}]}`))},
		{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"schemaVersion":99,"models":[]}`))},
	}
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Warnings)
	after, ok := account.GetUpstreamModelMetadata("qwen/qwen3.8-omni-flash")
	require.True(t, ok)
	require.Equal(t, before, after)
	require.Len(t, svc.cindyMetadataRegistry, 19, "invalid data cannot replace valid cache")
}

func TestCindyModelMetadataContract(t *testing.T) {
	body := `{"schemaVersion":4,"models":[{"id":"test/model","mode":"chat","contextWindow":1000,"maxOutputTokens":200,"efforts":["high"],"defaultEffort":"high","modalities":{"input":["text"]},"perAgent":{"codex":{"contextWindow":900,"efforts":["low","high"],"defaultEffort":"low"}}}]}`
	metadata, err := parseCindyModelMetadataRegistry([]byte(body))
	require.NoError(t, err)
	require.Equal(t, int64(900), metadata["test/model"].ContextWindow)
	require.Equal(t, int64(200), metadata["test/model"].MaxOutputTokens)
	require.Equal(t, "low", metadata["test/model"].DefaultReasoningLevel)
	for _, invalid := range []string{
		strings.Replace(body, `"schemaVersion":4`, `"schemaVersion":5`, 1),
		strings.Replace(body, `"contextWindow":900`, `"contextWindow":0`, 1),
		strings.Replace(body, `"efforts":["low","high"]`, `"efforts":["invalid"]`, 1),
	} {
		_, err := parseCindyModelMetadataRegistry([]byte(invalid))
		require.Error(t, err)
	}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai.attacker.example"}}
	require.False(t, usesCindyModelMetadataRegistry(account))
	require.Equal(t, []string{"cindy/web-search"}, cindyCapabilitySyncModelIDs(account, []string{"cindy/web-search"}))
}

type cindyConcurrentMetadataUpstream struct {
	HTTPUpstream
	calls   atomic.Int64
	body    []byte
	entered chan struct{}
	release chan struct{}
}

func (u *cindyConcurrentMetadataUpstream) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls.Add(1)
	close(u.entered)
	<-u.release
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(u.body)))}, nil
}

func TestCindyModelMetadataCoalescesAndCaches(t *testing.T) {
	u := &cindyConcurrentMetadataUpstream{body: cindyMetadataFixture(t), entered: make(chan struct{}), release: make(chan struct{})}
	svc := &AccountTestService{httpUpstream: u}
	account := &Account{ID: 1}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_, err := svc.fetchCindyModelMetadataRegistry(context.Background(), account)
			if err != nil {
				t.Error(err)
			}
		})
	}
	<-u.entered
	close(u.release)
	wg.Wait()
	_, err := svc.fetchCindyModelMetadataRegistry(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, int64(1), u.calls.Load())
}

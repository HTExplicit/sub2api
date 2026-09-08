//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const promptCacheTestSession = "22222222-2222-4222-8222-222222222222"
const promptCacheTestBody = `{"model":"gpt-6-astra","prompt_cache_key":"private-cache-key","instructions":"private-instructions","tools":[{"name":"private-tool"}],"input":[{"role":"user","content":"private-prompt"},{"type":"reasoning","encrypted_content":"private-cipher","summary":[]}],"private-extension":{"value":9007199254740993}}`

func promptCacheTestContext(ctx context.Context) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Request.Header.Set("Session_id", promptCacheTestSession)
	return c
}

func promptCacheTestRegistry(t *testing.T) *promptCacheDiagnosticRegistry {
	t.Helper()
	previous := openAIPromptCacheDiagnostics
	r := newPromptCacheDiagnosticRegistry()
	openAIPromptCacheDiagnostics = r
	t.Cleanup(func() { openAIPromptCacheDiagnostics = previous })
	return r
}

func promptCacheTestStart(t *testing.T, r *promptCacheDiagnosticRegistry, limit int) *PromptCacheDiagnosticView {
	t.Helper()
	v, err := r.start(PromptCacheDiagnosticConfig{APIKeyID: 19, SessionID: promptCacheTestSession, Model: "gpt-6-astra", TTLSeconds: 60, MaxRequests: limit})
	require.NoError(t, err)
	return v
}

func TestPromptCacheDiagnosticFingerprintComparisons(t *testing.T) {
	key := []byte("test-key-never-exported")
	base := buildPromptCacheSnapshot(key, []byte(promptCacheTestBody), "test")
	require.Equal(t, "complete", base.Status)
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(promptCacheTestBody))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&decoded))
	canonical, err := json.MarshalIndent(decoded, "", "  ")
	require.NoError(t, err)
	same := comparePromptCacheSnapshots(base, buildPromptCacheSnapshot(key, canonical, "test"), 1)
	require.True(t, same.Comparable)
	require.True(t, same.InputEqual)
	require.True(t, same.CacheKeyEqual)
	require.True(t, same.UnknownEqual)
	require.Empty(t, same.ChangedFields)
	for _, tc := range []struct{ old, replacement, part, field string }{
		{"private-prompt", "changed-prompt", "content", ""},
		{"private-cipher", "changed-cipher", "encrypted_content", ""},
		{"private-instructions", "changed-instructions", "", "instructions"},
		{"private-tool", "changed-tool", "", "tools"},
	} {
		t.Run(tc.old, func(t *testing.T) {
			d := comparePromptCacheSnapshots(base, buildPromptCacheSnapshot(key, []byte(strings.Replace(promptCacheTestBody, tc.old, tc.replacement, 1)), "test"), 1)
			if tc.part != "" {
				require.Contains(t, d.ChangedParts, tc.part)
				require.False(t, d.AppendOnly)
			}
			if tc.field != "" {
				require.Contains(t, d.ChangedFields, tc.field)
				require.True(t, d.InputEqual)
			}
		})
	}
	changedKey := comparePromptCacheSnapshots(base, buildPromptCacheSnapshot(key, []byte(strings.Replace(promptCacheTestBody, "private-cache-key", "different-key", 1)), "test"), 1)
	require.False(t, changedKey.CacheKeyEqual)
	changedNumber := comparePromptCacheSnapshots(base, buildPromptCacheSnapshot(key, []byte(strings.Replace(promptCacheTestBody, "9007199254740993", "9007199254740992", 1)), "test"), 1)
	require.False(t, changedNumber.UnknownEqual)
	decoded["input"] = append(decoded["input"].([]any), map[string]any{"type": "function_call_output", "output": "private-output"})
	appended, err := json.Marshal(decoded)
	require.NoError(t, err)
	d := comparePromptCacheSnapshots(base, buildPromptCacheSnapshot(key, appended, "test"), 1)
	require.True(t, d.AppendOnly)
	require.Equal(t, 2, d.CommonItems)
	require.Equal(t, 3, d.CurrentItems)
}

func TestPromptCacheDiagnosticBoundsAndMalformedJSON(t *testing.T) {
	for _, body := range []string{
		`{"input":[],"input":[]}`, `{"input":[}`, `{} {}`, `[]`,
		strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66),
		`{"input":[` + strings.Repeat("0,", promptCacheDiagnosticMaxItems) + "0]}",
		`{"nodes":[` + strings.Repeat("0,", promptCacheDiagnosticMaxNodes) + "0]}",
		strings.Repeat("x", promptCacheDiagnosticMaxBody+1),
	} {
		s := buildPromptCacheSnapshot([]byte("key"), []byte(body), "test")
		require.NotEqual(t, "complete", s.Status)
		require.False(t, comparePromptCacheSnapshots(s, s, 1).Comparable)
	}
}

func TestPromptCacheDiagnosticScopeLimitsAndRetention(t *testing.T) {
	r := promptCacheTestRegistry(t)
	c := promptCacheTestContext(context.Background())
	require.Nil(t, r.begin(c, 19, []byte(promptCacheTestBody)))
	now := time.Now()
	r.now = func() time.Time { return now }
	v := promptCacheTestStart(t, r, 2)
	require.Equal(t, v.ID, promptCacheTestStart(t, r, 2).ID)
	require.Nil(t, r.begin(c, 20, []byte(promptCacheTestBody)))
	c.Request.Header.Set("Session_id", "other")
	require.Nil(t, r.begin(c, 19, []byte(promptCacheTestBody)))
	c.Request.Header.Set("Session_id", promptCacheTestSession)
	c.Request.URL.Path = "/v1/chat/completions"
	require.Nil(t, r.begin(c, 19, []byte(promptCacheTestBody)))
	c.Request.URL.Path = "/v1/responses"
	require.Nil(t, r.begin(c, 19, []byte(`{"model":"gpt-5.4"}`)))
	require.Nil(t, r.begin(c, 19, bytes.Repeat([]byte("x"), promptCacheDiagnosticMaxBody+1)))
	require.Equal(t, 1, r.get(v.ID, false).SkippedOversized)
	for range 2 {
		span := r.begin(c, 19, []byte(promptCacheTestBody))
		require.NotNil(t, span)
		span.finish(200)
	}
	require.Nil(t, r.begin(c, 19, []byte(promptCacheTestBody)))
	require.Equal(t, "request_limit", r.get(v.ID, false).Status)
	v2 := promptCacheTestStart(t, r, 2)
	require.NotEqual(t, v.ID, v2.ID)
	require.Equal(t, "stopped", r.get(v2.ID, true).Status)
	require.Nil(t, r.begin(c, 19, []byte(promptCacheTestBody)))
	now = now.Add(2 * time.Hour)
	require.Nil(t, r.get(v.ID, false))
	require.False(t, r.enabled.Load())
	_, err := r.start(PromptCacheDiagnosticConfig{APIKeyID: 19, SessionID: promptCacheTestSession, Model: "gpt-6-astra", TTLSeconds: 3601})
	require.Error(t, err)
}

func TestPromptCacheDiagnosticWirePrivacyUsageAndCancellation(t *testing.T) {
	r := promptCacheTestRegistry(t)
	v := promptCacheTestStart(t, r, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := promptCacheTestContext(ctx)
	finish := BeginOpenAIPromptCacheDiagnostic(c, 19, []byte(promptCacheTestBody))
	require.NotNil(t, finish)
	account := &Account{ID: 31}
	wire := strings.Replace(promptCacheTestBody, "private-instructions", "gateway-instructions", 1)
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/responses?private-url=secret-value", strings.NewReader(wire))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer private-credential")
	bodyBefore, headersBefore, lengthBefore := req.Body, req.Header.Clone(), req.ContentLength
	attempt := beginOpenAIPromptCacheHTTPAttempt(c, account, req, []byte("not-final"), false, "http://private-proxy:password@proxy.example")
	require.NotNil(t, attempt)
	require.True(t, bodyBefore == req.Body, "the original live Body wrapper must be unchanged")
	require.Equal(t, headersBefore, req.Header)
	require.Equal(t, lengthBefore, req.ContentLength)
	require.NotNil(t, req.GetBody)
	actualBody, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, wire, string(actualBody))
	attempt(&http.Response{StatusCode: 200}, nil)
	observeOpenAIReasoningAttemptUsage(c, []byte(`{"type":"response.completed","response":{"id":"private-response-id","model":"gpt-6-astra","usage":{"input_tokens":580713,"output_tokens":341,"input_tokens_details":{"cached_tokens":0}}}}`))
	cancel()
	finish()
	finish() // completion is idempotent
	got := r.get(v.ID, false)
	require.Len(t, got.Records, 1)
	record := got.Records[0]
	require.NotNil(t, record.CanceledAt)
	require.NotNil(t, record.FinishedAt)
	require.Len(t, record.Attempts, 1)
	a := record.Attempts[0]
	require.Equal(t, "get_body_clone", a.Wire.Source)
	require.Equal(t, []string{"instructions"}, a.IngressToWire.ChangedFields)
	require.Equal(t, int64(580713), a.Usage.Input)
	require.True(t, a.Usage.CacheReadReported)
	require.False(t, a.Usage.CacheWriteReported)
	require.Zero(t, a.Usage.Read)
	require.Equal(t, "response.completed", a.Terminal)
	serialized, err := json.Marshal(got)
	require.NoError(t, err)
	for _, forbidden := range []string{"private-", "gateway-instructions", "secret-value", "password", "upstream.example", "proxy.example", "9007199254740993"} {
		require.NotContains(t, string(serialized), forbidden)
	}
	got.Records[0].Attempts[0].Wire.Fields["instructions"] = promptCacheFieldFingerprint{}
	require.True(t, r.get(v.ID, false).Records[0].Attempts[0].Wire.Fields["instructions"].Present)
	require.Zero(t, r.inflight)
}

func TestPromptCacheDiagnosticAttemptBoundAndStableWire(t *testing.T) {
	r := promptCacheTestRegistry(t)
	v := promptCacheTestStart(t, r, 8)
	c := promptCacheTestContext(context.Background())
	finish := BeginOpenAIPromptCacheDiagnostic(c, 19, []byte(promptCacheTestBody))
	require.NotNil(t, finish)
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/responses", strings.NewReader(promptCacheTestBody))
	require.NoError(t, err)
	req.GetBody = nil // recovery disables transparent transport replay; never restore it
	for range 8 {
		fn := beginOpenAIPromptCacheHTTPAttempt(c, &Account{ID: 31}, req, []byte(promptCacheTestBody), true, "")
		require.NotNil(t, fn)
		fn(&http.Response{StatusCode: 400}, nil)
	}
	require.Nil(t, beginOpenAIPromptCacheHTTPAttempt(c, &Account{ID: 31}, req, []byte(promptCacheTestBody), true, ""))
	require.Nil(t, req.GetBody)
	finish()
	got := r.get(v.ID, false).Records[0]
	require.Equal(t, 1, got.OmittedAttempts)
	require.Len(t, got.Attempts, 8)
	require.True(t, got.Attempts[1].PreviousWire.InputEqual)
	require.True(t, got.Attempts[1].RouteEqual)
	require.Empty(t, got.Attempts[1].ChangedHeaders)

	c2 := promptCacheTestContext(context.Background())
	finish2 := BeginOpenAIPromptCacheDiagnostic(c2, 19, []byte(promptCacheTestBody))
	require.NotNil(t, finish2)
	fn := beginOpenAIPromptCacheHTTPAttempt(c2, &Account{ID: 31}, req, []byte(promptCacheTestBody), false, "")
	require.NotNil(t, fn)
	fn(nil, context.DeadlineExceeded)
	finish2()
	require.Equal(t, "unavailable", r.get(v.ID, false).Records[1].Attempts[0].Wire.Status)
}

func TestPromptCacheDiagnosticConcurrentViewsAndCapacity(t *testing.T) {
	r := promptCacheTestRegistry(t)
	v := promptCacheTestStart(t, r, 16)
	var spans []*promptCacheDiagnosticSpan
	for range 4 {
		span := r.begin(promptCacheTestContext(context.Background()), 19, []byte(promptCacheTestBody))
		require.NotNil(t, span)
		spans = append(spans, span)
	}
	require.Nil(t, r.begin(promptCacheTestContext(context.Background()), 19, []byte(promptCacheTestBody)))
	require.Equal(t, 1, r.get(v.ID, false).SkippedBusy)
	var wg sync.WaitGroup
	for _, span := range spans {
		wg.Go(func() { span.canceled(context.Canceled); span.finish(200) })
	}
	wg.Go(func() {
		for range 20 {
			_ = r.get(v.ID, false)
		}
	})
	wg.Wait()
	require.Zero(t, r.inflight)
	for i := 0; i < 3; i++ {
		_, err := r.start(PromptCacheDiagnosticConfig{APIKeyID: int64(20 + i), SessionID: promptCacheTestSession, Model: fmt.Sprintf("gpt-test-%d", i)})
		require.NoError(t, err)
	}
	_, err := r.start(PromptCacheDiagnosticConfig{APIKeyID: 99, SessionID: promptCacheTestSession, Model: "gpt-other"})
	require.Error(t, err)
}

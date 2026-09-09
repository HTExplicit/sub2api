package handler

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func managedModelV2AffinityRedisFixture(t *testing.T) (service.ManagedModelAffinityCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return repository.NewGatewayCache(client).(service.ManagedModelAffinityCache), server
}

func recordManagedModelV2AffinityJSON(t *testing.T, store *managedModelV2AffinityStore, pin managedModelV2Pin, body string) {
	t.Helper()
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	original := c.Writer
	restore := store.Wrap(c, 7, 9, "public-model", pin)
	c.Header("Content-Type", "application/json")
	middle := len(body) / 2
	_, err := c.Writer.Write([]byte(body[:middle]))
	require.NoError(t, err)
	_, err = c.Writer.WriteString(body[middle:])
	require.NoError(t, err)
	observer := c.Writer.(*managedModelV2AffinityWriter)
	restore()
	restore()
	require.Same(t, original, c.Writer)
	require.Equal(t, body, response.Body.String())
	require.Nil(t, observer.frame, "the observer must release response content after forwarding")
	require.Nil(t, observer.line)
	require.Nil(t, observer.signatures)
}

func TestManagedModelV2AffinityJSONReferencesAndIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newManagedModelV2AffinityStore()
	pin := managedModelV2Pin{AccountID: 41, BranchSelector: "branch-a"}
	recordManagedModelV2AffinityJSON(t, store, pin, `{"object":"response","id":"resp_first","output":[{"type":"reasoning","id":"rs_first","encrypted_content":"private-cipher"},{"type":"function_call","id":"fc_first","call_id":"call_first"}]}`)
	recordManagedModelV2AffinityJSON(t, store, pin, `{"type":"message","content":[{"type":"thinking","thinking":"private-thought","signature":"private-signature"},{"type":"redacted_thinking","data":"private-redacted"}]}`)
	for _, body := range []string{
		`{"previous_response_id":"resp_first"}`,
		`{"input":[{"type":"item_reference","id":"rs_first"}]}`,
		`{"input":[{"type":"item_reference","id":"fc_first"}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"private-cipher"}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","signature":"private-signature"}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"private-redacted"}]}]}`,
		`{"previous_response_id":"resp_first","input":[{"type":"reasoning","encrypted_content":"private-cipher"}]}`,
	} {
		got, err := store.Resolve(7, 9, "public-model", []byte(body))
		require.NoError(t, err)
		require.Equal(t, &pin, got)
		for _, scope := range []struct {
			key, group int64
			model      string
		}{{8, 9, "public-model"}, {7, 10, "public-model"}, {7, 9, "other-model"}} {
			got, err = store.Resolve(scope.key, scope.group, scope.model, []byte(body))
			require.Nil(t, got)
			require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
		}
	}
	// Mutating a returned pin cannot mutate the cache entry.
	got, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_first"}`))
	require.NoError(t, err)
	got.AccountID = 999
	got, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_first"}`))
	require.NoError(t, err)
	require.Equal(t, &pin, got)
	for key, element := range store.entries {
		require.IsType(t, [sha256.Size]byte{}, key)
		require.Equal(t, pin, element.Value.(*managedModelV2AffinityEntry).pin)
	}
}

func TestManagedModelV2AffinityCompleteHistoriesDoNotPin(t *testing.T) {
	store := newManagedModelV2AffinityStore()
	for _, body := range []string{
		`{"input":"hello","metadata":{"previous_response_id":"not-state","encrypted_content":"not-state","signature":"not-state"}}`,
		`{"input":[{"type":"function_call","id":"fc_unknown","call_id":"call_unknown","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_unknown","output":{"type":"thinking","signature":"tool-output-not-state"}}]}`,
		`{"messages":[{"role":"assistant","tool_calls":[{"id":"call_unknown","function":{"name":"tool","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_unknown","content":"ok"}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_unknown","input":{"signature":"not-state"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_unknown","content":[{"type":"thinking","signature":"nested-tool-data"}]}]}]}`,
		`{"previous_response_id":null,"input":[{"type":"reasoning","id":"rs_inline","encrypted_content":"","summary":[]}]}`,
		`{"input":[{"id":"message_inline","role":"assistant","content":[{"type":"output_text","text":"completed inline message"}]}]}`,
		`{"input":[{"type":"function_call_output","call_id":"call_unknown","output":{"signature":"first","signature":"second"}}],"metadata":{"input":null,"input":"not-routing"}}`,
		`{"tools":[{"type":"function","parameters":{"type":"object","properties":{"previous_response_id":{"type":"string"}}}}]}`,
	} {
		pin, err := store.Resolve(7, 9, "public-model", []byte(body))
		require.NoError(t, err, body)
		require.Nil(t, pin, body)
	}
}

func TestManagedModelV2AffinityRejectsDuplicateRoutingFieldsAndPinsImplicitReferences(t *testing.T) {
	store := newManagedModelV2AffinityStore()
	pin := managedModelV2Pin{AccountID: 41, BranchSelector: "branch-a"}
	recordManagedModelV2AffinityJSON(t, store, pin, `{"object":"response","id":"resp_known","output":[{"type":"reasoning","id":"rs_known","encrypted_content":"cipher_known"}]}`)
	for _, body := range []string{
		`{"input":[{"id":"rs_known"}]}`,
		`{"input":[{"type":"item_reference","id":"rs_known"}]}`,
		`{"input":[{"id":"rs_known","role":null,"content":null}]}`,
	} {
		got, err := store.Resolve(7, 9, "public-model", []byte(body))
		require.NoError(t, err)
		require.Equal(t, &pin, got)
	}
	got, err := store.Resolve(7, 9, "public-model", []byte(`{"input":[{"id":"rs_unknown"}]}`))
	require.Nil(t, got)
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
	got, err = store.Resolve(7, 9, "public-model", []byte(`{"input":[{"id":"rs_unknown","role":"assistant","content":null}]}`))
	require.Nil(t, got)
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
	for _, body := range []string{
		`{"previous_response_id":null,"previous_response_id":"resp_known"}`,
		`{"previous_response_id":null,"previous_response_\u0069d":"resp_known"}`,
		`{"Previous_Response_Id":"resp_known"}`,
		`{"input":[],"input":[{"type":"reasoning","encrypted_content":"cipher_known"}]}`,
		`{"input":[{"type":"function_call","type":"reasoning","encrypted_content":"cipher_known"}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":null,"encrypted_content":"cipher_known"}]}`,
		`{"input":[{"id":"rs_known","id":"rs_unknown"}]}`,
		`{"messages":[],"messages":[{"content":[{"type":"thinking","signature":"unknown"}]}]}`,
		`{"messages":[{"content":[],"content":[{"type":"thinking","signature":"unknown"}]}]}`,
		`{"messages":[{"content":[{"type":"text","type":"thinking","signature":"unknown"}]}]}`,
		`{"messages":[{"content":[{"type":"thinking","signature":null,"signature":"unknown"}]}]}`,
		`{"messages":[{"content":[{"type":"redacted_thinking","data":null,"data":"unknown"}]}]}`,
	} {
		got, err := store.Resolve(7, 9, "public-model", []byte(body))
		require.Nil(t, got, body)
		require.ErrorIs(t, err, errManagedModelV2AffinityInvalid, body)
	}
}

func TestManagedModelV2AffinityMissingInvalidAndConflictingReferences(t *testing.T) {
	store := newManagedModelV2AffinityStore()
	first := managedModelV2Pin{AccountID: 41, BranchSelector: "branch-a"}
	second := managedModelV2Pin{AccountID: 42, BranchSelector: "branch-b"}
	recordManagedModelV2AffinityJSON(t, store, first, `{"object":"response","id":"resp_a","output":[{"type":"reasoning","encrypted_content":"cipher_a"}]}`)
	recordManagedModelV2AffinityJSON(t, store, second, `{"object":"response","id":"resp_b","output":[{"type":"reasoning","encrypted_content":"cipher_b"}]}`)
	for _, body := range []string{
		`{"previous_response_id":"resp_unknown"}`,
		`{"input":[{"type":"item_reference","id":"unknown"}]}`,
		`{"input":{"type":"compaction","encrypted_content":"unknown"}}`,
		`{"previous_response_id":"resp_a","input":[{"type":"reasoning","encrypted_content":"unknown"}]}`,
	} {
		got, err := store.Resolve(7, 9, "public-model", []byte(body))
		require.Nil(t, got)
		require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
	}
	for _, body := range []string{`{`, `[]`, `{"previous_response_id":42}`, `{"input":[{"type":"reasoning","encrypted_content":{}}]}`} {
		got, err := store.Resolve(7, 9, "public-model", []byte(body))
		require.Nil(t, got)
		require.ErrorIs(t, err, errManagedModelV2AffinityInvalid)
	}
	got, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_a","input":[{"type":"reasoning","encrypted_content":"cipher_b"}]}`))
	require.Nil(t, got)
	require.ErrorIs(t, err, errManagedModelV2AffinityConflict)
	// Reusing the same account on a different actual branch is also a conflict.
	recordManagedModelV2AffinityJSON(t, store, managedModelV2Pin{AccountID: 41, BranchSelector: "branch-other"}, `{"id":"resp_a","object":"response"}`)
	recordManagedModelV2AffinityJSON(t, store, first, `{"id":"resp_a","object":"response"}`)
	got, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_a"}`))
	require.Nil(t, got)
	require.ErrorIs(t, err, errManagedModelV2AffinityConflict)
	var missingStore *managedModelV2AffinityStore
	_, err = missingStore.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_a"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
}

func TestManagedModelV2AffinitySSESplitLinesAndSignatureDeltas(t *testing.T) {
	store := newManagedModelV2AffinityStore()
	pin := managedModelV2Pin{AccountID: 44, BranchSelector: "branch-stream"}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	restore := store.Wrap(c, 7, 9, "public-model", pin)
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	body := ": keep-alive\r\n\r\n" +
		"event: response.created\r\ndata: {\"response\":\r\ndata: {\"id\":\"resp_stream\"}}\r\n\r\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_stream\",\"encrypted_content\":\"cipher_stream\"}}\n\n" +
		"event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"thinking\",\"signature\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"tail\"}}\n\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"redacted_stream\"}}\n\n" +
		"data: [DONE]\n\n"
	for offset := 0; offset < len(body); {
		end := min(offset+7, len(body))
		var err error
		if offset%2 == 0 {
			_, err = c.Writer.Write([]byte(body[offset:end]))
		} else {
			_, err = c.Writer.WriteString(body[offset:end])
		}
		require.NoError(t, err)
		c.Writer.Flush()
		offset = end
	}
	// IDs from complete events are available even before the response finishes.
	got, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_stream"}`))
	require.NoError(t, err)
	require.Equal(t, &pin, got)
	restore()
	require.Equal(t, body, response.Body.String())
	require.True(t, response.Flushed)
	got, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_stream","input":[{"type":"item_reference","id":"rs_stream"},{"type":"reasoning","encrypted_content":"cipher_stream"}],"messages":[{"role":"assistant","content":[{"type":"thinking","signature":"sig-tail"},{"type":"redacted_thinking","data":"redacted_stream"}]}]}`))
	require.NoError(t, err)
	require.Equal(t, &pin, got)
	_, err = store.Resolve(7, 9, "public-model", []byte(`{"messages":[{"content":[{"type":"thinking","signature":"sig-"}]}]}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing, "a signature delta is not a complete signature")
}

func TestManagedModelV2AffinitySSEOverflowDropsWholeFrameThenRecovers(t *testing.T) {
	store := newManagedModelV2AffinityStore()
	pin := managedModelV2Pin{AccountID: 44, BranchSelector: "branch-stream"}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	restore := store.Wrap(c, 7, 9, "public-model", pin)
	c.Header("Content-Type", "text/event-stream")
	chunks := []string{
		"data: " + strings.Repeat("x", managedModelV2AffinityFrameMax+1),
		"\n", // End of the oversized line is not the blank event delimiter.
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_discarded\"}}\n",
		"\r", "\n",
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_after\"}}", // No final newline.
	}
	for _, chunk := range chunks {
		_, err := c.Writer.WriteString(chunk)
		require.NoError(t, err)
		observer := c.Writer.(*managedModelV2AffinityWriter)
		require.LessOrEqual(t, len(observer.frame), managedModelV2AffinityFrameMax)
		require.LessOrEqual(t, len(observer.line), managedModelV2AffinityFrameMax)
	}
	restore()
	require.Equal(t, strings.Join(chunks, ""), response.Body.String())
	_, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_discarded"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
	got, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_after"}`))
	require.NoError(t, err)
	require.Equal(t, &pin, got)
}

func TestManagedModelV2AffinityJSONOverflowAndHTTPFailureDoNotBind(t *testing.T) {
	store := newManagedModelV2AffinityStore()
	pin := managedModelV2Pin{AccountID: 44, BranchSelector: "branch-stream"}
	recordManagedModelV2AffinityJSON(t, store, pin, `{"object":"response","id":"resp_large","padding":"`+strings.Repeat("x", managedModelV2AffinityFrameMax)+`"}`)
	_, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_large"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	restore := store.Wrap(c, 7, 9, "public-model", pin)
	c.Header("Content-Type", "application/json")
	c.Status(http.StatusServiceUnavailable)
	_, err = c.Writer.WriteString(`{"object":"response","id":"resp_error"}`)
	require.NoError(t, err)
	restore()
	_, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_error"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
}

type managedModelV2AffinityShortWriter struct {
	gin.ResponseWriter
	remaining int
}

func (w *managedModelV2AffinityShortWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p[:min(w.remaining, len(p))])
	w.remaining -= n
	if n < len(p) {
		return n, io.ErrShortWrite
	}
	return n, err
}

func TestManagedModelV2AffinityObservesOnlyDeliveredBytesOnPartialFailure(t *testing.T) {
	store := newManagedModelV2AffinityStore()
	pin := managedModelV2Pin{AccountID: 44, BranchSelector: "branch-stream"}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	first := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_delivered\"}}\n\n"
	second := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_not_delivered\"}}\n\n"
	c.Writer = &managedModelV2AffinityShortWriter{ResponseWriter: c.Writer, remaining: len(first)}
	restore := store.Wrap(c, 7, 9, "public-model", pin)
	c.Header("Content-Type", "text/event-stream")
	n, err := c.Writer.Write([]byte(first + second))
	require.Equal(t, len(first), n)
	require.ErrorIs(t, err, io.ErrShortWrite)
	restore()
	require.Equal(t, first, response.Body.String())
	got, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_delivered"}`))
	require.NoError(t, err)
	require.Equal(t, &pin, got)
	_, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_not_delivered"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
}

func TestManagedModelV2AffinityTTLBoundedLRUAndConcurrentAccess(t *testing.T) {
	store := newManagedModelV2AffinityStore()
	store.limit = 2
	now := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	pin := managedModelV2Pin{AccountID: 41, BranchSelector: "branch-a"}
	for _, id := range []string{"resp_a", "resp_b"} {
		recordManagedModelV2AffinityJSON(t, store, pin, fmt.Sprintf(`{"object":"response","id":%q}`, id))
	}
	_, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_a"}`))
	require.NoError(t, err)
	recordManagedModelV2AffinityJSON(t, store, pin, `{"object":"response","id":"resp_c"}`)
	require.Len(t, store.entries, 2)
	_, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_b"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
	now = now.Add(managedModelV2AffinityTTL - time.Second)
	_, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_a"}`))
	require.NoError(t, err)
	now = now.Add(time.Second)
	_, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_a"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing, "reads do not extend output evidence TTL")

	concurrentStore := newManagedModelV2AffinityStore()
	scope := managedModelV2AffinityScope(7, 9, "public-model")
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := range 16 {
				id := fmt.Sprintf("resp_%d_%d", worker, step)
				concurrentStore.remember(managedModelV2ReferenceKey(scope, managedModelV2ResponseReference, id), pin)
				got, err := concurrentStore.Resolve(7, 9, "public-model", []byte(fmt.Sprintf(`{"previous_response_id":%q}`, id)))
				if err != nil || got == nil || *got != pin {
					t.Errorf("concurrent resolve failed: %v", err)
				}
			}
		}(worker)
	}
	wg.Wait()
	require.Len(t, concurrentStore.entries, 128)
}

func TestManagedModelV2AffinityRedisSurvivesProcessStoreReplacement(t *testing.T) {
	cache, server := managedModelV2AffinityRedisFixture(t)
	first := newManagedModelV2AffinityStore(cache)
	pin := managedModelV2Pin{AccountID: 41, BranchSelector: "legacy-original"}
	recordManagedModelV2AffinityJSON(t, first, pin, `{"object":"response","id":"resp_durable","output":[{"type":"reasoning","id":"rs_durable","encrypted_content":"private-durable-cipher"}]}`)
	recordManagedModelV2AffinityJSON(t, first, pin, `{"type":"message","content":[{"type":"thinking","signature":"private-durable-signature"},{"type":"redacted_thinking","data":"private-durable-redacted"}]}`)
	fresh := newManagedModelV2AffinityStore(cache)
	require.Empty(t, fresh.entries)
	body := []byte(`{"previous_response_id":"resp_durable","input":[{"type":"reasoning","encrypted_content":"private-durable-cipher"}],"messages":[{"content":[{"type":"thinking","signature":"private-durable-signature"},{"type":"redacted_thinking","data":"private-durable-redacted"}]}]}`)
	got, err := fresh.Resolve(7, 9, "public-model", body)
	require.NoError(t, err)
	require.Equal(t, &pin, got)
	for _, key := range server.Keys() {
		value, err := server.Get(key)
		require.NoError(t, err)
		require.NotContains(t, key+value, "private-durable")
		require.NotContains(t, key+value, "resp_durable")
		require.NotContains(t, key+value, "rs_durable")
	}
	_, err = fresh.Resolve(8, 9, "public-model", body)
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
	// A fresh process observing a collision must invalidate the original
	// process's hot pin as well; a local-cache hit is not authoritative.
	recordManagedModelV2AffinityJSON(t, fresh, managedModelV2Pin{AccountID: 42, BranchSelector: "other-target"}, `{"object":"response","id":"resp_durable"}`)
	_, err = first.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_durable"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityConflict)
	server.FastForward(managedModelV2AffinityTTL)
	_, err = first.Resolve(7, 9, "public-model", []byte(`{"input":[{"type":"reasoning","encrypted_content":"private-durable-cipher"}]}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
}

type managedModelV2AffinityFaultCache struct {
	service.ManagedModelAffinityCache
	getError   error
	bindError  error
	bindCalls  int
	beforeBind func(context.Context, []string, service.ManagedModelAffinityBinding, time.Duration)
}

func (s *managedModelV2AffinityFaultCache) GetManagedModelAffinity(ctx context.Context, keys []string) (map[string]service.ManagedModelAffinityBinding, error) {
	if s.getError != nil {
		return nil, s.getError
	}
	return s.ManagedModelAffinityCache.GetManagedModelAffinity(ctx, keys)
}

func (s *managedModelV2AffinityFaultCache) BindManagedModelAffinity(ctx context.Context, keys []string, pin service.ManagedModelAffinityBinding, ttl time.Duration) error {
	s.bindCalls++
	if s.beforeBind != nil {
		s.beforeBind(ctx, keys, pin, ttl)
	}
	if s.bindError != nil {
		return s.bindError
	}
	return s.ManagedModelAffinityCache.BindManagedModelAffinity(ctx, keys, pin, ttl)
}

func TestManagedModelV2AffinityRedisFailuresNeverFallBackToProcessPin(t *testing.T) {
	cache, _ := managedModelV2AffinityRedisFixture(t)
	fault := &managedModelV2AffinityFaultCache{ManagedModelAffinityCache: cache}
	store := newManagedModelV2AffinityStore(fault)
	pin := managedModelV2Pin{AccountID: 41, BranchSelector: "branch-a"}
	recordManagedModelV2AffinityJSON(t, store, pin, `{"object":"response","id":"resp_known"}`)
	fault.getError = errors.New("cache unavailable")
	got, err := store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_known"}`))
	require.Nil(t, got)
	require.ErrorIs(t, err, errManagedModelV2AffinityStore)
	fault.getError = nil
	fault.bindError = errors.New("cache write unavailable")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	restore := store.Wrap(c, 7, 9, "public-model", pin)
	c.Header("Content-Type", "application/json")
	_, err = c.Writer.WriteString(`{"object":"response","id":"resp_unpersisted"}`)
	require.NoError(t, err)
	restore()
	require.True(t, c.GetBool(managedModelV2AffinityPersistenceFailureKey))
	_, err = store.Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_unpersisted"}`))
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
}

func TestManagedModelV2AffinityPersistenceBudgetMeasuresIOInsteadOfStreamAge(t *testing.T) {
	cache, _ := managedModelV2AffinityRedisFixture(t)
	fault := &managedModelV2AffinityFaultCache{ManagedModelAffinityCache: cache}
	store := newManagedModelV2AffinityStore(fault)
	pin := managedModelV2Pin{AccountID: 41, BranchSelector: "branch-a"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	restore := store.Wrap(c, 7, 9, "public-model", pin)
	c.Header("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_slow_stream\"}}\n\n")
	require.NoError(t, err)
	// Simulate upstream generation, not a slow cache call. The final signature
	// must still be durable even though the stream outlives the cache I/O budget.
	time.Sleep(managedModelV2AffinityIOBudget + 5*time.Millisecond)
	_, err = c.Writer.WriteString("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"late-cipher\"}}\n\n")
	require.NoError(t, err)
	restore()
	require.False(t, c.GetBool(managedModelV2AffinityPersistenceFailureKey))
	require.Equal(t, 2, fault.bindCalls)
	got, err := newManagedModelV2AffinityStore(cache).Resolve(7, 9, "public-model", []byte(`{"previous_response_id":"resp_slow_stream","input":[{"type":"reasoning","encrypted_content":"late-cipher"}]}`))
	require.NoError(t, err)
	require.Equal(t, &pin, got)
}

func managedModelV2AffinityLegacyRequest() *service.ManagedModelRequest {
	return &service.ManagedModelRequest{
		Version: 2, GroupID: 9, Endpoint: "responses",
		Route: service.ManagedModelRoute{PublicModel: "public-model", Branches: []service.ManagedModelRouteBranch{
			{Selector: "legacy-original", Accounts: []service.ManagedModelRouteAccount{{AccountID: 41, UpstreamModel: "original-target"}}},
			{Selector: "new-other-target", UpstreamProtocol: "responses", Accounts: []service.ManagedModelRouteAccount{{AccountID: 41, UpstreamModel: "different-target"}}},
		}},
	}
}

func TestManagedModelV2AffinityLegacyMigrationKeepsOnlyProvenOriginalBranch(t *testing.T) {
	cache, _ := managedModelV2AffinityRedisFixture(t)
	store := newManagedModelV2AffinityStore(cache)
	request := managedModelV2AffinityLegacyRequest()
	lookup := func(ctx context.Context, group int64, id string) (*service.ManagedModelLegacyResponseBinding, error) {
		require.NoError(t, ctx.Err())
		require.Equal(t, int64(9), group)
		require.Equal(t, "resp_legacy", id)
		return &service.ManagedModelLegacyResponseBinding{AccountID: 41, UserID: 3, APIKeyID: 7, OwnerKnown: true}, nil
	}
	body := []byte(`{"previous_response_id":"resp_legacy"}`)
	got, err := store.ResolveRequest(context.Background(), 7, 9, "public-model", body, request, lookup)
	require.NoError(t, err)
	require.Equal(t, &managedModelV2Pin{AccountID: 41, BranchSelector: "legacy-original"}, got)
	got, err = newManagedModelV2AffinityStore(cache).Resolve(7, 9, "public-model", body)
	require.NoError(t, err)
	require.Equal(t, "legacy-original", got.BranchSelector)

	for _, tc := range []struct {
		name   string
		mutate func(*service.ManagedModelRequest, *service.ManagedModelLegacyResponseBinding)
	}{
		{"two legacy targets", func(r *service.ManagedModelRequest, _ *service.ManagedModelLegacyResponseBinding) {
			r.Route.Branches[1].UpstreamProtocol = ""
		}},
		{"no retained legacy path", func(r *service.ManagedModelRequest, _ *service.ManagedModelLegacyResponseBinding) {
			r.Route.Branches[0].UpstreamProtocol = "responses"
		}},
		{"wrong recorded owner", func(_ *service.ManagedModelRequest, b *service.ManagedModelLegacyResponseBinding) { b.APIKeyID = 8 }},
		{"wrong group", func(r *service.ManagedModelRequest, _ *service.ManagedModelLegacyResponseBinding) { r.GroupID = 10 }},
		{"wrong public model", func(r *service.ManagedModelRequest, _ *service.ManagedModelLegacyResponseBinding) {
			r.Route.PublicModel = "other"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := managedModelV2AffinityLegacyRequest()
			binding := &service.ManagedModelLegacyResponseBinding{AccountID: 41, APIKeyID: 7, OwnerKnown: true}
			tc.mutate(r, binding)
			got, err := newManagedModelV2AffinityStore().ResolveRequest(context.Background(), 7, 9, "public-model", body, r, func(context.Context, int64, string) (*service.ManagedModelLegacyResponseBinding, error) {
				return binding, nil
			})
			require.Nil(t, got)
			require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
		})
	}
	called := false
	_, err = newManagedModelV2AffinityStore().ResolveRequest(context.Background(), 7, 9, "public-model", []byte(`{"previous_response_id":"resp_legacy","input":[{"type":"reasoning","encrypted_content":"unproven-opaque"}]}`), request, func(context.Context, int64, string) (*service.ManagedModelLegacyResponseBinding, error) {
		called = true
		return nil, nil
	})
	require.ErrorIs(t, err, errManagedModelV2AffinityMissing)
	require.False(t, called, "an old response ID cannot authorize an unrelated opaque blob")
}

func TestManagedModelV2AffinityLegacyMigrationReadsBackConcurrentConflict(t *testing.T) {
	cache, _ := managedModelV2AffinityRedisFixture(t)
	fault := &managedModelV2AffinityFaultCache{ManagedModelAffinityCache: cache}
	fault.beforeBind = func(ctx context.Context, keys []string, _ service.ManagedModelAffinityBinding, ttl time.Duration) {
		require.NoError(t, cache.BindManagedModelAffinity(ctx, keys, service.ManagedModelAffinityBinding{AccountID: 42, BranchSelector: "concurrent-other"}, ttl))
	}
	store := newManagedModelV2AffinityStore(fault)
	got, err := store.ResolveRequest(context.Background(), 7, 9, "public-model", []byte(`{"previous_response_id":"resp_race"}`), managedModelV2AffinityLegacyRequest(), func(context.Context, int64, string) (*service.ManagedModelLegacyResponseBinding, error) {
		return &service.ManagedModelLegacyResponseBinding{AccountID: 41, APIKeyID: 7, OwnerKnown: true}, nil
	})
	require.Nil(t, got)
	require.ErrorIs(t, err, errManagedModelV2AffinityConflict)
}

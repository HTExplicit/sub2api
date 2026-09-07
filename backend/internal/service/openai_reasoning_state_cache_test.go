package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIReasoningCacheScope(t *testing.T) {
	base := OpenAIReasoningScopeInput{
		UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4,
		SourceIdentityHash: strings.Repeat("a", 64), Endpoint: "https://provider.example/v1/responses", Model: "gpt-test",
		Reasoning: json.RawMessage(`{"effort":"xhigh","mode":"standard","context":{"count":9007199254740993}}`),
	}
	original, err := BuildOpenAIReasoningCacheScope(base)
	require.NoError(t, err)
	require.True(t, IsOpenAIReasoningCacheDigest(original.ScopeHash))
	require.True(t, IsOpenAIReasoningCacheDigest(original.TenantHash))
	reordered := base
	reordered.Reasoning = json.RawMessage(`{ "context": {"count": 9007199254740993}, "mode":"standard", "effort":"xhigh" }`)
	identical, err := BuildOpenAIReasoningCacheScope(reordered)
	require.NoError(t, err)
	require.Equal(t, original, identical)

	changes := map[string]func(*OpenAIReasoningScopeInput){
		"user":     func(v *OpenAIReasoningScopeInput) { v.UserID++ },
		"key":      func(v *OpenAIReasoningScopeInput) { v.APIKeyID++ },
		"group":    func(v *OpenAIReasoningScopeInput) { v.GroupID++ },
		"account":  func(v *OpenAIReasoningScopeInput) { v.AccountID++ },
		"identity": func(v *OpenAIReasoningScopeInput) { v.SourceIdentityHash = strings.Repeat("b", 64) },
		"endpoint": func(v *OpenAIReasoningScopeInput) { v.Endpoint += "/compact" },
		"model":    func(v *OpenAIReasoningScopeInput) { v.Model += "-other" },
		"mode": func(v *OpenAIReasoningScopeInput) {
			v.Reasoning = json.RawMessage(`{"effort":"xhigh","mode":"pro","context":{"count":9007199254740993}}`)
		},
		"integer beyond float precision": func(v *OpenAIReasoningScopeInput) {
			v.Reasoning = json.RawMessage(`{"effort":"xhigh","mode":"standard","context":{"count":9007199254740992}}`)
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			changed := base
			change(&changed)
			scope, err := BuildOpenAIReasoningCacheScope(changed)
			require.NoError(t, err)
			require.NotEqual(t, original.ScopeHash, scope.ScopeHash)
			if name != "user" && name != "key" {
				require.Equal(t, original.TenantHash, scope.TenantHash)
			} else {
				require.NotEqual(t, original.TenantHash, scope.TenantHash)
			}
		})
	}

	for _, raw := range []string{`{"mode":"pro","mode":"standard"}`, `{"context":{"x":1,"x":2}}`, `{} {}`, `{"effort":`} {
		invalid := base
		invalid.Reasoning = json.RawMessage(raw)
		_, err := BuildOpenAIReasoningCacheScope(invalid)
		require.ErrorIs(t, err, ErrOpenAIReasoningCacheInput)
	}
	base.UserID = 0
	_, err = BuildOpenAIReasoningCacheScope(base)
	require.ErrorIs(t, err, ErrOpenAIReasoningCacheInput)
}

func TestOpenAIReasoningCacheBudget(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	budget := openAIReasoningCacheBudgetForRequest(c)
	require.Same(t, budget, openAIReasoningCacheBudgetForRequest(c))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	require.ErrorIs(t, budget.Do(ctx, func(context.Context) error { called = true; return nil }), context.Canceled)
	require.False(t, called)

	budget.remaining = 10 * time.Millisecond
	err := budget.Do(context.Background(), func(ioCtx context.Context) error {
		<-ioCtx.Done()
		return ioCtx.Err()
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, budget.Do(context.Background(), func(context.Context) error { called = true; return nil }), ErrOpenAIReasoningCacheBudget)
	require.False(t, called)
}

func TestOpenAIReasoningCacheBudgetChargesIOOnly(t *testing.T) {
	budget := NewOpenAIReasoningCacheBudget()
	errMarker := errors.New("cache unavailable")
	require.ErrorIs(t, budget.Do(context.Background(), func(context.Context) error { return errMarker }), errMarker)
	remainingAfterIO := budget.remaining
	// Simulate time between operations (model generation). It must not consume
	// the write-back budget as a single absolute request deadline would.
	time.Sleep(15 * time.Millisecond)
	require.NoError(t, budget.Do(context.Background(), func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.Greater(t, time.Until(deadline), remainingAfterIO-10*time.Millisecond)
		return nil
	}))
}

func TestOpenAIReasoningCacheBudgetConcurrentRequestConsumers(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	results := make(chan *OpenAIReasoningCacheBudget, 8)
	for range cap(results) {
		go func() { results <- openAIReasoningCacheBudgetForRequest(c) }()
	}
	first := <-results
	for range cap(results) - 1 {
		require.Same(t, first, <-results)
	}
}

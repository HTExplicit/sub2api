package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBusinessSystemPromptWSTurnCacheDoesNotRetainCompletedBodies(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{}
	policy := NewBusinessSystemPromptService(store, nil)
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", nil)
	c.Set("unrelated-session-state", "keep")
	account := businessSystemPromptAPIKeyAccount(true)
	keyCount := 0
	for turn := int64(1); turn <= 4; turn++ {
		store.loaded = BusinessSystemPromptSnapshot{Enabled: true, Revision: turn, Body: fmt.Sprintf("server-%d", turn)}
		require.NoError(t, policy.Reload(context.Background()))
		beginBusinessSystemPromptRequestTurn(c)
		_, applied := businessSystemPromptApplicationFromRequest(c, BusinessSystemPromptProtocolResponses)
		require.False(t, applied, "a new turn must not inherit a completed response application")
		original := []byte(fmt.Sprintf(`{"instructions":%q,"prompt_cache_key":"seed","input":[]}`, strings.Repeat("client", 256)))
		body, application, err := gateway.applyBusinessSystemPromptForRequest(c, original, account, BusinessSystemPromptProtocolResponses, false)
		require.NoError(t, err)
		require.Equal(t, turn, application.Revision)
		require.Contains(t, gjson.GetBytes(body, "instructions").String(), store.loaded.Body)
		retried, repeated, err := gateway.applyBusinessSystemPromptForRequest(c, original, account, BusinessSystemPromptProtocolResponses, false)
		require.NoError(t, err)
		require.Equal(t, body, retried)
		require.Equal(t, application, repeated)
		chat, fallback, err := gateway.applyBusinessSystemPromptForRequest(c, []byte(`{"messages":[{"role":"user","content":"fallback"}]}`), account, BusinessSystemPromptProtocolChat, false)
		require.NoError(t, err)
		require.Equal(t, turn, fallback.Revision)
		require.True(t, chatBodyHasSystemPrompt(chat, store.loaded.Body))
		rewritten := gateway.rewriteBusinessSystemPromptJSONForRequest(c, body, BusinessSystemPromptProtocolResponses)
		require.Equal(t, gjson.GetBytes(original, "instructions").String(), gjson.GetBytes(rewritten, "instructions").String())
		wire := deriveBusinessSystemPromptCacheKey(c, "seed", application)
		require.Equal(t, wire, deriveBusinessSystemPromptCacheKey(c, wire, application))
		keys := c.Copy().Keys
		require.Equal(t, "keep", keys["unrelated-session-state"])
		if turn == 1 {
			keyCount = len(keys)
		} else {
			require.Equal(t, keyCount, len(keys), "a completed turn must not leave snapshot/body entries in the connection context")
		}
	}
}

func TestBusinessSystemPromptTurnCacheKeepsOnlyCurrentMetadata(t *testing.T) {
	businessSystemPromptRequestSet(nil, "nil", true)
	_, exists := businessSystemPromptRequestGet(nil, "nil")
	require.False(t, exists)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", nil)
	businessSystemPromptRequestSet(c, "http-fixture", "request scoped")
	value, exists := c.Get("http-fixture")
	require.True(t, exists)
	require.Equal(t, "request scoped", value)

	for turn := 0; turn < 2; turn++ {
		beginBusinessSystemPromptRequestTurn(c)
		key := businessSystemPromptContextKey(c, businessSystemPromptRequestObservationKey, BusinessSystemPromptProtocolResponses)
		_, exists = businessSystemPromptRequestGet(c, key)
		require.False(t, exists)
		logBusinessSystemPromptObservation(context.Background(), c, BusinessSystemPromptApplication{}, OpenAIUpstreamTransportAny, "fixture")
		value, exists = businessSystemPromptRequestGet(c, key)
		require.True(t, exists)
		require.Equal(t, true, value)
		var writes sync.WaitGroup
		for index := 0; index < 2; index++ {
			writes.Go(func() {
				businessSystemPromptRequestSet(c, "same-turn", "bounded")
				businessSystemPromptRequestGet(c, "same-turn")
			})
		}
		writes.Wait()
		current, exists := c.Get(businessSystemPromptTurnCacheKey)
		require.True(t, exists)
		require.Len(t, current.(*businessSystemPromptTurnCache).values, 2, "only the current observation and current-turn fixture remain")
	}
	// Starting a prompt turn must not erase unrelated HTTP/session metadata.
	value, exists = c.Get("http-fixture")
	require.True(t, exists)
	require.Equal(t, "request scoped", value)
}

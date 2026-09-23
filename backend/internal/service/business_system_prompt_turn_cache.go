package service

import (
	"sync"

	"github.com/gin-gonic/gin"
)

const businessSystemPromptTurnCacheKey = "openai_business_system_prompt_turn_cache"

// A WS connection owns only its current prompt cache. The ingress parser starts
// a new turn after the previous relay/AfterTurn has finished; accounting has
// already captured its own metadata and never reads these prompt cache keys.
// Replacing the pointer releases the completed turn's snapshots and full body
// instead of keeping one set of Gin keys for every turn in the connection.
type businessSystemPromptTurnCache struct {
	mu     sync.RWMutex
	values map[string]any
}

func businessSystemPromptRequestGet(ctx *gin.Context, key string) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	value, _ := ctx.Get(businessSystemPromptTurnCacheKey)
	if cache, ok := value.(*businessSystemPromptTurnCache); ok && cache != nil {
		cache.mu.RLock()
		defer cache.mu.RUnlock()
		value, exists := cache.values[key]
		return value, exists
	}
	// Ordinary HTTP requests keep their existing request-scoped storage.
	return ctx.Get(key)
}

func businessSystemPromptRequestSet(ctx *gin.Context, key string, value any) {
	if ctx == nil {
		return
	}
	current, _ := ctx.Get(businessSystemPromptTurnCacheKey)
	if cache, ok := current.(*businessSystemPromptTurnCache); ok && cache != nil {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.values == nil {
			cache.values = make(map[string]any)
		}
		cache.values[key] = value
		return
	}
	ctx.Set(key, value)
}

func businessSystemPromptRequestDelete(ctx *gin.Context, key string) {
	if ctx == nil {
		return
	}
	current, _ := ctx.Get(businessSystemPromptTurnCacheKey)
	if cache, ok := current.(*businessSystemPromptTurnCache); ok && cache != nil {
		cache.mu.Lock()
		delete(cache.values, key)
		cache.mu.Unlock()
		return
	}
	// HTTP callers never need this operation: it is reserved for the native
	// WS accumulator's proven clean, service-owned payloads.
}

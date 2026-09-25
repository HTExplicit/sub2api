package service

import (
	"sync"

	"github.com/gin-gonic/gin"
)

const businessSystemPromptTurnCacheKey = "openai_business_system_prompt_turn_cache"

const businessSystemPromptFirstWSTurnKey = "business_system_prompt_first_ws_turn_started"

// Re-entering a WS adapter after an initial dial/account failure still belongs
// to the first client frame. Only an actually received next frame starts a new
// snapshot; transport attempts must not observe a just-published revision.
func beginBusinessSystemPromptFirstWSTurn(c *gin.Context) {
	if c == nil {
		return
	}
	if started, _ := c.Get(businessSystemPromptFirstWSTurnKey); started == true {
		return
	}
	beginBusinessSystemPromptRequestTurn(c)
	c.Set(businessSystemPromptFirstWSTurnKey, true)
}

// A WS connection owns only its current prompt cache. The ingress parser starts
// a new turn after the previous relay/AfterTurn has finished; accounting has
// already captured its own metadata and never reads these prompt cache keys.
// Replacing the pointer releases the completed turn's snapshots and echo proofs
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

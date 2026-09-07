package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	OpenAIReasoningStateTTL              = 24 * time.Hour
	OpenAIReasoningStateIOBudget         = 100 * time.Millisecond
	OpenAIReasoningBatchMaxBytes         = 256 << 10
	OpenAIReasoningBatchMaxItems         = 64
	OpenAIReasoningBatchMaxCalls         = 32
	OpenAIReasoningBatchTotalMaxBytes    = 64 << 20
	OpenAIReasoningBatchTotalMaxEntries  = 8192
	OpenAIReasoningBatchTenantMaxBytes   = 8 << 20
	OpenAIRejectedReasoningMaxBytes      = 8 << 20
	OpenAIRejectedReasoningMaxEntries    = 32768
	OpenAIReasoningStateMaxLookupEntries = 64
)

var (
	ErrOpenAIReasoningCacheInput  = errors.New("invalid reasoning state cache input")
	ErrOpenAIReasoningCacheBudget = errors.New("reasoning state cache I/O budget exhausted")
)

// OpenAIReasoningCacheScope contains only digests, never authentication values,
// upstream URLs, prompts, or ciphertext. TenantHash bounds one downstream key's
// storage independently of its current model, group, and upstream account.
type OpenAIReasoningCacheScope struct {
	ScopeHash  string
	TenantHash string
}

// OpenAIReasoningScopeInput describes the actual selected source and wire
// configuration, not the original requested model or an account-pool label.
// SourceIdentityHash must cover authentication identity and the selected proxy.
type OpenAIReasoningScopeInput struct {
	UserID             int64
	APIKeyID           int64
	GroupID            int64
	AccountID          int64
	SourceIdentityHash string
	Endpoint           string
	Model              string
	Reasoning          json.RawMessage
}

// BuildOpenAIReasoningCacheScope refuses anonymous/unknown-source caches. Every
// reasoning subfield participates in identity; mode is never inferred as effort.
func BuildOpenAIReasoningCacheScope(input OpenAIReasoningScopeInput) (OpenAIReasoningCacheScope, error) {
	if input.UserID <= 0 || input.APIKeyID <= 0 || input.GroupID < 0 || input.AccountID <= 0 ||
		!IsOpenAIReasoningCacheDigest(input.SourceIdentityHash) ||
		strings.TrimSpace(input.Endpoint) == "" || strings.TrimSpace(input.Model) == "" {
		return OpenAIReasoningCacheScope{}, ErrOpenAIReasoningCacheInput
	}
	canonical, err := canonicalReasoningCacheJSON(input.Reasoning)
	if err != nil {
		return OpenAIReasoningCacheScope{}, ErrOpenAIReasoningCacheInput
	}
	input.Reasoning = canonical
	identity, err := json.Marshal(input)
	if err != nil {
		return OpenAIReasoningCacheScope{}, ErrOpenAIReasoningCacheInput
	}
	tenant, _ := json.Marshal([2]int64{input.UserID, input.APIKeyID})
	return OpenAIReasoningCacheScope{
		ScopeHash:  reasoningStateDigest(identity),
		TenantHash: reasoningStateDigest(tenant),
	}, nil
}

func canonicalReasoningCacheJSON(raw json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("null"), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeReasoningCacheValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrOpenAIReasoningCacheInput
	}
	return json.Marshal(value)
}

// A standard map decode silently collapses duplicate keys. Cache identity must
// never merge two potentially different wire configurations, so ambiguous or
// excessive nesting disables only this optional cache, not normal forwarding.
func decodeReasoningCacheValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, ErrOpenAIReasoningCacheInput
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delim {
	case '{':
		value := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, valid := keyToken.(string)
			if !valid {
				return nil, ErrOpenAIReasoningCacheInput
			}
			if _, duplicate := value[key]; duplicate {
				return nil, ErrOpenAIReasoningCacheInput
			}
			child, err := decodeReasoningCacheValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			value[key] = child
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return nil, ErrOpenAIReasoningCacheInput
		}
		return value, nil
	case '[':
		value := make([]any, 0)
		for decoder.More() {
			child, err := decodeReasoningCacheValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			value = append(value, child)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return nil, ErrOpenAIReasoningCacheInput
		}
		return value, nil
	default:
		return nil, ErrOpenAIReasoningCacheInput
	}
}

func reasoningStateDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func IsOpenAIReasoningCacheDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

// OpenAIReasoningBatch is one complete, reversible Chat assistant projection.
// Output retains the original item sequence, including unknown subfields.
// InputPrefixHash proves the actual upstream prefix after earlier replay. No
// full input history is stored. PayloadHash is the CAS version returned by Get.
type OpenAIReasoningBatch struct {
	Output          []json.RawMessage `json:"output"`
	Projection      json.RawMessage   `json:"projection"`
	InputPrefixHash string            `json:"input_prefix_hash"`
	PayloadHash     string            `json:"-"`
}

type OpenAIRejectedReasoning struct {
	RejectedAt time.Time
	ExpiresAt  time.Time
}

// OpenAIReasoningStateStore is optional: production GatewayCache implements it,
// but unrelated cache adapters and their mocks need not grow new methods. Cache
// errors mean unavailable, never evidence that any history should be erased.
type OpenAIReasoningStateStore interface {
	GetOpenAIReasoningBatches(ctx context.Context, scope OpenAIReasoningCacheScope, keys []string) (map[string]OpenAIReasoningBatch, error)
	PutOpenAIReasoningBatch(ctx context.Context, scope OpenAIReasoningCacheScope, key string, batch OpenAIReasoningBatch) (bool, error)
	DeleteOpenAIReasoningBatchIfMatch(ctx context.Context, scope OpenAIReasoningCacheScope, key, payloadHash string) (bool, error)
	GetOpenAIRejectedReasoning(ctx context.Context, scope OpenAIReasoningCacheScope, cipherHashes []string) (map[string]OpenAIRejectedReasoning, error)
	// Put is only for a real structured rejection. Get never refreshes this TTL.
	PutOpenAIRejectedReasoning(ctx context.Context, scope OpenAIReasoningCacheScope, cipherHashes []string) error
}

func (s *OpenAIGatewayService) openAIReasoningStateStore() OpenAIReasoningStateStore {
	if s == nil || s.cache == nil {
		return nil
	}
	store, _ := s.cache.(OpenAIReasoningStateStore)
	return store
}

// OpenAIReasoningCacheBudget charges only I/O time, not model generation time.
// Replay, rejection lookup, invalidation, and write-back share this one budget.
// Callbacks must respect the derived context and must not start detached work.
type OpenAIReasoningCacheBudget struct {
	mu        sync.Mutex
	remaining time.Duration
}

func NewOpenAIReasoningCacheBudget() *OpenAIReasoningCacheBudget {
	return &OpenAIReasoningCacheBudget{remaining: OpenAIReasoningStateIOBudget}
}

const openAIReasoningCacheBudgetContextKey = "openai_reasoning_state_cache_io_budget"

// Gin's individual Get/Set methods are safe, but their get-or-create pair is
// not atomic. Keep concurrent consumers of one request on the same budget.
var openAIReasoningCacheBudgetInit sync.Mutex

func openAIReasoningCacheBudgetForRequest(c *gin.Context) *OpenAIReasoningCacheBudget {
	if c == nil {
		return NewOpenAIReasoningCacheBudget()
	}
	openAIReasoningCacheBudgetInit.Lock()
	defer openAIReasoningCacheBudgetInit.Unlock()
	if existing, ok := c.Get(openAIReasoningCacheBudgetContextKey); ok {
		if budget, valid := existing.(*OpenAIReasoningCacheBudget); valid && budget != nil {
			return budget
		}
	}
	budget := NewOpenAIReasoningCacheBudget()
	c.Set(openAIReasoningCacheBudgetContextKey, budget)
	return budget
}

func (b *OpenAIReasoningCacheBudget) Do(ctx context.Context, fn func(context.Context) error) error {
	if b == nil || fn == nil {
		return ErrOpenAIReasoningCacheBudget
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.remaining <= 0 {
		return ErrOpenAIReasoningCacheBudget
	}
	started := time.Now()
	ioCtx, cancel := context.WithTimeout(ctx, b.remaining)
	defer cancel()
	err := fn(ioCtx)
	b.remaining -= time.Since(started)
	if err != nil {
		return err
	}
	return ioCtx.Err()
}

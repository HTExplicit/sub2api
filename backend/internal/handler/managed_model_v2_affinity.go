package handler

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	managedModelV2AffinityTTL       = 24 * time.Hour
	managedModelV2AffinityLimit     = 8192
	managedModelV2AffinityFrameMax  = 1 << 20
	managedModelV2AffinityInputMax  = 1024
	managedModelV2AffinityBlockMax  = 128
	managedModelV2AffinityIOBudget  = 100 * time.Millisecond
	managedModelV2ResponseReference = byte(1)
	managedModelV2ItemReference     = byte(2)
	managedModelV2EncryptedContent  = byte(3)
	managedModelV2ThinkingSignature = byte(4)
	managedModelV2RedactedThinking  = byte(5)
)

const managedModelV2AffinityPersistenceFailureKey = "managed_model_v2_affinity_persistence_failed"

var (
	errManagedModelV2AffinityMissing  = errors.New("account-bound continuation is unavailable; start a new conversation")
	errManagedModelV2AffinityConflict = errors.New("account-bound continuation references do not share the same route")
	errManagedModelV2AffinityInvalid  = errors.New("invalid account-bound continuation reference")
	errManagedModelV2AffinityStore    = errors.New("account-bound continuation store is unavailable")
)

type managedModelV2Pin struct {
	AccountID      int64
	BranchSelector string
}

type managedModelV2AffinityEntry struct {
	key       [sha256.Size]byte
	pin       managedModelV2Pin
	expiresAt time.Time
	ambiguous bool
}

// Only scoped digests and routing identity are retained. Production Redis is
// authoritative across deployments and instances; the bounded local cache is
// also usable by isolated tests. Missing/expired state always fails closed.
type managedModelV2AffinityStore struct {
	mu          sync.Mutex
	entries     map[[sha256.Size]byte]*list.Element
	lru         *list.List
	now         func() time.Time
	ttl         time.Duration
	limit       int
	persistence service.ManagedModelAffinityCache
}

func newManagedModelV2AffinityStore(persistence ...service.ManagedModelAffinityCache) *managedModelV2AffinityStore {
	s := &managedModelV2AffinityStore{
		entries: make(map[[sha256.Size]byte]*list.Element), lru: list.New(),
		now: time.Now, ttl: managedModelV2AffinityTTL, limit: managedModelV2AffinityLimit,
	}
	if len(persistence) > 0 {
		s.persistence = persistence[0]
	}
	return s
}

func managedModelV2AffinityScope(apiKeyID, groupID int64, public string) [sha256.Size]byte {
	h := sha256.New()
	var ids [24]byte
	binary.BigEndian.PutUint64(ids[:8], uint64(apiKeyID))
	binary.BigEndian.PutUint64(ids[8:16], uint64(groupID))
	binary.BigEndian.PutUint64(ids[16:], uint64(len(public)))
	_, _ = h.Write(ids[:])
	_, _ = h.Write([]byte(public))
	var digest [sha256.Size]byte
	h.Sum(digest[:0])
	return digest
}

func managedModelV2ReferenceHasher(scope [sha256.Size]byte, kind byte) hash.Hash {
	h := sha256.New()
	_, _ = h.Write(scope[:])
	_, _ = h.Write([]byte{kind})
	return h
}

func managedModelV2ReferenceKey(scope [sha256.Size]byte, kind byte, value string) [sha256.Size]byte {
	h := managedModelV2ReferenceHasher(scope, kind)
	_, _ = h.Write([]byte(value))
	var digest [sha256.Size]byte
	h.Sum(digest[:0])
	return digest
}

func (s *managedModelV2AffinityStore) remember(key [sha256.Size]byte, pin managedModelV2Pin) {
	if s == nil || pin.AccountID <= 0 || pin.BranchSelector == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if element := s.entries[key]; element != nil {
		entry := element.Value.(*managedModelV2AffinityEntry)
		if now.Before(entry.expiresAt) {
			// A provider reusing an ID on another route is ambiguous, not a new
			// assignment. An old client must never silently follow the new pin.
			entry.ambiguous = entry.ambiguous || entry.pin != pin
			entry.expiresAt = now.Add(s.ttl)
			s.lru.MoveToFront(element)
			return
		}
		s.remove(element)
	}
	if s.limit <= 0 {
		return
	}
	for len(s.entries) >= s.limit {
		s.remove(s.lru.Back())
	}
	entry := &managedModelV2AffinityEntry{key: key, pin: pin, expiresAt: now.Add(s.ttl)}
	s.entries[key] = s.lru.PushFront(entry)
}

func (s *managedModelV2AffinityStore) remove(element *list.Element) {
	delete(s.entries, element.Value.(*managedModelV2AffinityEntry).key)
	s.lru.Remove(element)
}

func (s *managedModelV2AffinityStore) Resolve(apiKeyID, groupID int64, public string, body []byte) (*managedModelV2Pin, error) {
	return s.ResolveRequest(context.Background(), apiKeyID, groupID, public, body, nil)
}

type managedModelV2LegacyResponseLookup func(context.Context, int64, string) (*service.ManagedModelLegacyResponseBinding, error)

func (s *managedModelV2AffinityStore) ResolveRequest(ctx context.Context, apiKeyID, groupID int64, public string, body []byte, request *service.ManagedModelRequest, legacyLookup ...managedModelV2LegacyResponseLookup) (*managedModelV2Pin, error) {
	if !gjson.ValidBytes(body) {
		return nil, errManagedModelV2AffinityInvalid
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() || !managedModelV2UniqueReferenceFields(root, "previous_response_id", "input", "messages") {
		return nil, errManagedModelV2AffinityInvalid
	}
	scope := managedModelV2AffinityScope(apiKeyID, groupID, public)
	keys := make(map[[sha256.Size]byte]struct{})
	var invalid bool
	add := func(kind byte, value gjson.Result) {
		if !value.Exists() || value.Type == gjson.Null {
			return
		}
		if value.Type != gjson.String {
			invalid = true
			return
		}
		if value.Str == "" {
			return
		}
		key := managedModelV2ReferenceKey(scope, kind, value.Str)
		if _, exists := keys[key]; exists {
			return
		}
		if len(keys) >= managedModelV2AffinityInputMax {
			invalid = true
			return
		}
		keys[key] = struct{}{}
	}
	add(managedModelV2ResponseReference, root.Get("previous_response_id"))
	managedModelV2EachObject(root.Get("input"), func(item gjson.Result) {
		if !managedModelV2UniqueReferenceFields(item, "type", "id", "encrypted_content", "content", "role") {
			invalid = true
			return
		}
		switch item.Get("type").Str {
		case "":
			// Responses ItemReference.type is optional. A concrete inline
			// message is not a server-side reference; null/invalid payload
			// padding must not turn a reference into an unpinned request.
			role, content := item.Get("role").Str, item.Get("content")
			inlineMessage := (role == "user" || role == "assistant" || role == "system" || role == "developer") && (content.Type == gjson.String || content.IsArray())
			if !inlineMessage {
				add(managedModelV2ItemReference, item.Get("id"))
			} else if !managedModelV2ThinkingReferences(content, add) {
				invalid = true
			}
		case "item_reference":
			add(managedModelV2ItemReference, item.Get("id"))
		case "reasoning", "compaction":
			add(managedModelV2EncryptedContent, item.Get("encrypted_content"))
		case "message":
			if !managedModelV2ThinkingReferences(item.Get("content"), add) {
				invalid = true
			}
		}
	})
	managedModelV2EachObject(root.Get("messages"), func(message gjson.Result) {
		if !managedModelV2UniqueReferenceFields(message, "content") || !managedModelV2ThinkingReferences(message.Get("content"), add) {
			invalid = true
		}
	})
	if invalid {
		return nil, errManagedModelV2AffinityInvalid
	}
	if len(keys) == 0 {
		return nil, nil
	}
	if s == nil {
		return nil, errManagedModelV2AffinityMissing
	}
	cacheCtx, cancel := context.WithTimeout(ctx, managedModelV2AffinityIOBudget)
	defer cancel()
	bindings, err := s.bindings(cacheCtx, keys)
	if err != nil {
		return nil, errManagedModelV2AffinityStore
	}
	previousID := root.Get("previous_response_id").Str
	previousKey := managedModelV2ReferenceKey(scope, managedModelV2ResponseReference, previousID)
	// A legacy response/account cache can establish only that response ID, not
	// unrelated opaque signatures added beside it by the caller.
	var existingPin *managedModelV2Pin
	for key := range keys {
		binding, found := bindings[key]
		if !found {
			if key != previousKey || previousID == "" {
				return nil, errManagedModelV2AffinityMissing
			}
			continue
		}
		candidate := managedModelV2Pin{AccountID: binding.AccountID, BranchSelector: binding.BranchSelector}
		if binding.Ambiguous || existingPin != nil && *existingPin != candidate {
			return nil, errManagedModelV2AffinityConflict
		}
		if candidate.AccountID <= 0 || candidate.BranchSelector == "" {
			return nil, errManagedModelV2AffinityStore
		}
		existingPin = &candidate
	}
	if _, needed := keys[previousKey]; needed {
		if _, found := bindings[previousKey]; !found {
			if len(legacyLookup) == 0 || legacyLookup[0] == nil {
				return nil, errManagedModelV2AffinityMissing
			}
			legacy, err := legacyLookup[0](cacheCtx, groupID, previousID)
			if err != nil {
				return nil, errManagedModelV2AffinityStore
			}
			migrated := managedModelV2LegacyPin(apiKeyID, groupID, public, request, legacy)
			if migrated == nil {
				return nil, errManagedModelV2AffinityMissing
			}
			if existingPin != nil && *existingPin != *migrated {
				return nil, errManagedModelV2AffinityConflict
			}
			binding := service.ManagedModelAffinityBinding{AccountID: migrated.AccountID, BranchSelector: migrated.BranchSelector}
			if s.persistence != nil {
				if err := s.persistence.BindManagedModelAffinity(cacheCtx, []string{hex.EncodeToString(previousKey[:])}, binding, s.ttl); err != nil {
					return nil, errManagedModelV2AffinityStore
				}
			}
			s.remember(previousKey, *migrated)
			// Bind is conflict-preserving: another instance may have observed
			// this digest between our miss and the migration. Read its result
			// before trusting the inferred legacy pin.
			confirmed, err := s.bindings(cacheCtx, map[[sha256.Size]byte]struct{}{previousKey: {}})
			if err != nil {
				return nil, errManagedModelV2AffinityStore
			}
			if value, found := confirmed[previousKey]; found {
				bindings[previousKey] = value
			}
		}
	}
	var pin *managedModelV2Pin
	for key := range keys {
		binding, found := bindings[key]
		if !found {
			return nil, errManagedModelV2AffinityMissing
		}
		candidate := managedModelV2Pin{AccountID: binding.AccountID, BranchSelector: binding.BranchSelector}
		if binding.Ambiguous || pin != nil && *pin != candidate {
			return nil, errManagedModelV2AffinityConflict
		}
		if candidate.AccountID <= 0 || candidate.BranchSelector == "" {
			return nil, errManagedModelV2AffinityStore
		}
		pin = &candidate
	}
	return pin, nil
}

func (s *managedModelV2AffinityStore) bindings(ctx context.Context, keys map[[sha256.Size]byte]struct{}) (map[[sha256.Size]byte]service.ManagedModelAffinityBinding, error) {
	result := make(map[[sha256.Size]byte]service.ManagedModelAffinityBinding, len(keys))
	if s.persistence != nil {
		hashes := make([]string, 0, len(keys))
		for key := range keys {
			hashes = append(hashes, hex.EncodeToString(key[:]))
		}
		persisted, err := s.persistence.GetManagedModelAffinity(ctx, hashes)
		if err != nil {
			return nil, err
		}
		for key := range keys {
			if binding, found := persisted[hex.EncodeToString(key[:])]; found {
				result[key] = binding
			}
		}
		return result, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key := range keys {
		if element := s.entries[key]; element != nil {
			entry := element.Value.(*managedModelV2AffinityEntry)
			if !now.Before(entry.expiresAt) {
				s.remove(element)
				continue
			}
			result[key] = service.ManagedModelAffinityBinding{AccountID: entry.pin.AccountID, BranchSelector: entry.pin.BranchSelector, Ambiguous: entry.ambiguous}
			s.lru.MoveToFront(element)
		}
	}
	return result, nil
}

func managedModelV2LegacyPin(apiKeyID, groupID int64, public string, request *service.ManagedModelRequest, legacy *service.ManagedModelLegacyResponseBinding) *managedModelV2Pin {
	if request == nil || request.Version < 2 || request.GroupID != groupID || request.Route.PublicModel != public || legacy == nil || legacy.AccountID <= 0 || (legacy.OwnerKnown && legacy.APIKeyID != apiKeyID) {
		return nil
	}
	var pin *managedModelV2Pin
	var upstream string
	for _, branch := range request.Route.Branches {
		if branch.UpstreamProtocol != "" || branch.Selector == "" {
			continue
		}
		for _, account := range branch.Accounts {
			if account.AccountID != legacy.AccountID || account.UpstreamModel == "" {
				continue
			}
			candidate := managedModelV2Pin{AccountID: account.AccountID, BranchSelector: branch.Selector}
			if pin != nil && (*pin != candidate || upstream != account.UpstreamModel) {
				return nil
			}
			pin, upstream = &candidate, account.UpstreamModel
		}
	}
	return pin
}

func managedModelV2EachObject(value gjson.Result, visit func(gjson.Result)) {
	if value.IsObject() {
		visit(value)
	} else if value.IsArray() {
		value.ForEach(func(_, item gjson.Result) bool {
			if item.IsObject() {
				visit(item)
			}
			return true
		})
	}
}

// Raw forwarding and Go/JavaScript decoders can disagree on duplicate or
// case-variant field names. Reject that ambiguity at protocol object boundaries,
// without recursively policing arbitrary tool outputs or JSON schema content.
func managedModelV2UniqueReferenceFields(object gjson.Result, fields ...string) bool {
	seen := make(map[string]struct{}, len(fields))
	valid := true
	object.ForEach(func(key, _ gjson.Result) bool {
		for _, field := range fields {
			if !strings.EqualFold(key.Str, field) {
				continue
			}
			_, exists := seen[field]
			if exists || key.Str != field {
				valid = false
				return false
			}
			seen[field] = struct{}{}
			break
		}
		return true
	})
	return valid
}

func managedModelV2ThinkingReferences(content gjson.Result, add func(byte, gjson.Result)) bool {
	valid := true
	managedModelV2EachObject(content, func(block gjson.Result) {
		if !managedModelV2UniqueReferenceFields(block, "type") {
			valid = false
			return
		}
		switch block.Get("type").Str {
		case "thinking":
			if !managedModelV2UniqueReferenceFields(block, "signature") {
				valid = false
				return
			}
			add(managedModelV2ThinkingSignature, block.Get("signature"))
		case "redacted_thinking":
			if !managedModelV2UniqueReferenceFields(block, "data") {
				valid = false
				return
			}
			add(managedModelV2RedactedThinking, block.Get("data"))
		}
	})
	return valid
}

// Wrap observes only bytes delivered by this forwarding attempt. Restore must
// run after that attempt (and its heartbeat) ends, even on a partial stream error.
func (s *managedModelV2AffinityStore) Wrap(c *gin.Context, apiKeyID, groupID int64, public string, pin managedModelV2Pin) func() {
	if s == nil || c == nil || c.Writer == nil {
		return func() {}
	}
	w := &managedModelV2AffinityWriter{
		ResponseWriter: c.Writer, store: s, scope: managedModelV2AffinityScope(apiKeyID, groupID, public), pin: pin,
		ioBudget: managedModelV2AffinityIOBudget,
	}
	w.requestContext = context.Background()
	if c.Request != nil {
		w.requestContext = context.WithoutCancel(c.Request.Context())
	}
	c.Writer = w
	var once sync.Once
	return func() {
		once.Do(func() {
			w.mu.Lock()
			w.finish()
			w.finished = true
			w.mu.Unlock()
			if w.persistenceFailed {
				c.Set(managedModelV2AffinityPersistenceFailureKey, true)
			}
			if c.Writer == w {
				c.Writer = w.ResponseWriter
			}
		})
	}
}

type managedModelV2SignatureHash struct {
	hash  hash.Hash
	bytes int
	bad   bool
}

type managedModelV2AffinityWriter struct {
	gin.ResponseWriter
	mu                sync.Mutex
	store             *managedModelV2AffinityStore
	scope             [sha256.Size]byte
	pin               managedModelV2Pin
	mode              byte // 0: not yet known, 1: JSON, 2: SSE
	line              []byte
	lineBytes         int
	lineLast          byte
	frame             []byte
	frameBytes        int
	event             string
	dropFrame         bool
	finished          bool
	signatures        map[int64]*managedModelV2SignatureHash
	observed          map[[sha256.Size]byte]struct{}
	pending           map[[sha256.Size]byte]struct{}
	requestContext    context.Context
	ioBudget          time.Duration
	persistenceFailed bool
}

var _ gin.ResponseWriter = (*managedModelV2AffinityWriter)(nil)

func (w *managedModelV2AffinityWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *managedModelV2AffinityWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.observe(p[:n])
	}
	return n, err
}

func (w *managedModelV2AffinityWriter) WriteString(s string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.ResponseWriter.WriteString(s)
	// Never allocate a response-sized string conversion just for observation.
	for offset := 0; offset < n; {
		end := min(offset+32*1024, n)
		w.observe([]byte(s[offset:end]))
		offset = end
	}
	return n, err
}

func (w *managedModelV2AffinityWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.Flush()
}

func (w *managedModelV2AffinityWriter) observe(data []byte) {
	if w.finished || w.Status() < 200 || w.Status() >= 300 || len(data) == 0 {
		return
	}
	if w.mode == 0 {
		contentType := strings.ToLower(w.Header().Get("Content-Type"))
		switch {
		case strings.Contains(contentType, "text/event-stream"):
			w.mode = 2
		case strings.Contains(contentType, "json"):
			w.mode = 1
		default:
			trimmed := bytes.TrimSpace(data)
			if len(trimmed) == 0 {
				return
			}
			w.mode = 2
			if trimmed[0] == '{' || trimmed[0] == '[' {
				w.mode = 1
			}
		}
	}
	if w.mode == 1 {
		if w.dropFrame {
			return
		}
		if len(w.frame)+len(data) > managedModelV2AffinityFrameMax {
			w.dropFrame = true
			clear(w.frame)
			w.frame = nil
			return
		}
		w.frame = append(w.frame, data...)
		return
	}
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		terminated := end >= 0
		if !terminated {
			end = len(data)
		}
		part := data[:end]
		w.frameBytes = min(managedModelV2AffinityFrameMax+1, w.frameBytes+len(part))
		w.lineBytes = min(managedModelV2AffinityFrameMax+1, w.lineBytes+len(part))
		if len(part) > 0 {
			w.lineLast = part[len(part)-1]
		}
		if w.frameBytes > managedModelV2AffinityFrameMax {
			w.dropFrame = true
			clear(w.line)
			w.line = w.line[:0]
			clear(w.frame)
			w.frame = w.frame[:0]
		}
		if !w.dropFrame {
			w.line = append(w.line, part...)
		}
		if !terminated {
			return
		}
		if !w.dropFrame {
			w.processLine()
		} else if w.lineBytes == 0 || w.lineBytes == 1 && w.lineLast == '\r' {
			// Ignore an oversized event completely; resume at its blank delimiter.
			w.dropFrame = false
			w.frameBytes = 0
			w.event = ""
		}
		w.lineBytes = 0
		data = data[end+1:]
	}
}

func (w *managedModelV2AffinityWriter) processLine() {
	line := bytes.TrimSuffix(w.line, []byte{'\r'})
	if len(line) == 0 {
		w.processJSON(w.frame)
		clear(w.frame)
		w.frame = w.frame[:0]
		w.frameBytes = 0
		w.event = ""
	} else if bytes.HasPrefix(line, []byte("data:")) {
		data := line[len("data:"):]
		if len(data) > 0 && data[0] == ' ' {
			data = data[1:]
		}
		w.frame = append(w.frame, data...)
		w.frame = append(w.frame, '\n')
	} else if bytes.HasPrefix(line, []byte("event:")) && len(line) < 128 {
		w.event = strings.TrimSpace(string(line[len("event:"):]))
	}
	clear(w.line)
	w.line = w.line[:0]
}

func (w *managedModelV2AffinityWriter) finish() {
	if !w.dropFrame {
		if w.mode == 2 && len(w.line) > 0 {
			w.processLine()
		}
		w.processJSON(w.frame)
	}
	for index := range w.signatures {
		w.finishSignature(index)
	}
	w.persistReferences()
	clear(w.line)
	clear(w.frame)
	w.line, w.frame, w.signatures = nil, nil, nil
	w.observed, w.pending = nil, nil
	w.event = ""
}

func (w *managedModelV2AffinityWriter) record(kind byte, value gjson.Result) {
	if value.Type == gjson.String && value.Str != "" {
		w.recordKey(managedModelV2ReferenceKey(w.scope, kind, value.Str))
	}
}

func (w *managedModelV2AffinityWriter) recordKey(key [sha256.Size]byte) {
	if _, seen := w.observed[key]; seen {
		return
	}
	if len(w.observed) >= managedModelV2AffinityInputMax {
		w.persistenceFailed = true
		return
	}
	if w.observed == nil {
		w.observed = make(map[[sha256.Size]byte]struct{})
	}
	w.observed[key] = struct{}{}
	w.store.remember(key, w.pin)
	if w.store.persistence != nil {
		if w.pending == nil {
			w.pending = make(map[[sha256.Size]byte]struct{})
		}
		w.pending[key] = struct{}{}
	}
}

func (w *managedModelV2AffinityWriter) persistReferences() {
	if len(w.pending) == 0 {
		return
	}
	defer clear(w.pending)
	if w.ioBudget <= 0 || w.persistenceFailed {
		w.persistenceFailed = true
		return
	}
	hashes := make([]string, 0, len(w.pending))
	for key := range w.pending {
		hashes = append(hashes, hex.EncodeToString(key[:]))
	}
	ctx, cancel := context.WithTimeout(w.requestContext, w.ioBudget)
	started := time.Now()
	err := w.store.persistence.BindManagedModelAffinity(ctx, hashes, service.ManagedModelAffinityBinding{AccountID: w.pin.AccountID, BranchSelector: w.pin.BranchSelector}, w.store.ttl)
	w.ioBudget -= time.Since(started)
	cancel()
	if err != nil {
		w.persistenceFailed = true
	}
}

func (w *managedModelV2AffinityWriter) recordOutputItem(item gjson.Result) {
	w.record(managedModelV2ItemReference, item.Get("id"))
	switch item.Get("type").Str {
	case "reasoning", "compaction":
		w.record(managedModelV2EncryptedContent, item.Get("encrypted_content"))
	}
}

func (w *managedModelV2AffinityWriter) recordResponse(response gjson.Result) {
	w.record(managedModelV2ResponseReference, response.Get("id"))
	managedModelV2EachObject(response.Get("output"), w.recordOutputItem)
}

func (w *managedModelV2AffinityWriter) processJSON(data []byte) {
	if !gjson.ValidBytes(data) {
		return
	}
	defer w.persistReferences()
	root := gjson.ParseBytes(data)
	kind := root.Get("type").Str
	if kind == "" {
		kind = w.event
	}
	if strings.HasPrefix(kind, "response.") {
		w.record(managedModelV2ResponseReference, root.Get("response_id"))
	}
	if response := root.Get("response"); response.IsObject() {
		w.recordResponse(response)
	}
	object := root.Get("object").Str
	if object == "response" || object == "response.compaction" || strings.HasPrefix(root.Get("id").Str, "resp_") {
		w.recordResponse(root)
	}
	switch kind {
	case "response.output_item.added", "response.output_item.done":
		w.recordOutputItem(root.Get("item"))
	case "message":
		managedModelV2ThinkingReferences(root.Get("content"), w.record)
	case "message_start":
		managedModelV2ThinkingReferences(root.Get("message.content"), w.record)
	case "content_block_start":
		block := root.Get("content_block")
		if block.Get("type").Str == "thinking" {
			w.addSignature(root.Get("index"), block.Get("signature"))
		} else if block.Get("type").Str == "redacted_thinking" {
			w.record(managedModelV2RedactedThinking, block.Get("data"))
		}
	case "content_block_delta":
		if root.Get("delta.type").Str == "signature_delta" {
			w.addSignature(root.Get("index"), root.Get("delta.signature"))
		}
	case "content_block_stop":
		w.finishSignature(root.Get("index").Int())
	case "message_stop":
		for index := range w.signatures {
			w.finishSignature(index)
		}
	}
}

func (w *managedModelV2AffinityWriter) addSignature(index, value gjson.Result) {
	if index.Type != gjson.Number || index.Int() < 0 || value.Type != gjson.String || value.Str == "" {
		return
	}
	if w.signatures == nil {
		w.signatures = make(map[int64]*managedModelV2SignatureHash)
	}
	state := w.signatures[index.Int()]
	if state == nil {
		if len(w.signatures) >= managedModelV2AffinityBlockMax {
			return
		}
		state = &managedModelV2SignatureHash{hash: managedModelV2ReferenceHasher(w.scope, managedModelV2ThinkingSignature)}
		w.signatures[index.Int()] = state
	}
	state.bytes += len(value.Str)
	if state.bytes > managedModelV2AffinityFrameMax {
		state.bad = true
		return
	}
	if !state.bad {
		_, _ = state.hash.Write([]byte(value.Str))
	}
}

func (w *managedModelV2AffinityWriter) finishSignature(index int64) {
	if state := w.signatures[index]; state != nil {
		if !state.bad {
			var key [sha256.Size]byte
			state.hash.Sum(key[:0])
			w.recordKey(key)
		}
		delete(w.signatures, index)
	}
}

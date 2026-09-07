package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIChatReasoningReplayContextKey = "openai_chat_reasoning_replay"

// This is a cache of completed, reversible tool batches, not a conversation
// archive. Prefixes, ownership and source identity are retained only as hashes.
type openAIChatReasoningReplay struct {
	ctx             context.Context
	c               *gin.Context
	store           OpenAIReasoningStateStore
	budget          *OpenAIReasoningCacheBudget
	scope           OpenAIReasoningCacheScope
	projectedPrefix string
	actualPrefix    string
	replayed        []openAIChatReasoningReplayHit
	recorder        openAIChatReasoningReplayRecorder
}

type openAIChatReasoningReplayHit struct {
	key         string
	payloadHash string
	ciphers     map[string]struct{}
}

type openAIChatReasoningProjection struct {
	Content   string                              `json:"content"`
	Reasoning string                              `json:"reasoning_content"`
	Calls     []openAIChatReasoningProjectionCall `json:"tool_calls"`
}

type openAIChatReasoningProjectionCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// normalizeOpenAIChatReasoningProjection is deliberately narrower than the
// general compatibility converter. If a client changes a semantic field that
// Chat cannot round-trip, a cache miss is safer than replacing that field.
func normalizeOpenAIChatReasoningProjection(message apicompat.ChatMessage) (json.RawMessage, bool) {
	if message.Role != "assistant" || message.Name != "" || message.ToolCallID != "" || message.FunctionCall != nil || len(message.ToolCalls) == 0 || len(message.ToolCalls) > 32 {
		return nil, false
	}
	projection := openAIChatReasoningProjection{Reasoning: message.ReasoningContent}
	if message.Reasoning != "" {
		if projection.Reasoning != "" && projection.Reasoning != message.Reasoning {
			return nil, false
		}
		projection.Reasoning = message.Reasoning
	}
	if len(message.Content) > 0 && string(bytes.TrimSpace(message.Content)) != "null" {
		if err := json.Unmarshal(message.Content, &projection.Content); err != nil {
			var parts []map[string]json.RawMessage
			if err := json.Unmarshal(message.Content, &parts); err != nil {
				return nil, false
			}
			for _, part := range parts {
				var kind, text string
				if len(part) != 2 || json.Unmarshal(part["type"], &kind) != nil || kind != "text" || json.Unmarshal(part["text"], &text) != nil {
					return nil, false
				}
				projection.Content += text
			}
		}
	}
	seen := make(map[string]struct{}, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if call.Type != "function" || call.ID == "" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
			return nil, false
		}
		if _, exists := seen[call.ID]; exists {
			return nil, false
		}
		seen[call.ID] = struct{}{}
		projection.Calls = append(projection.Calls, openAIChatReasoningProjectionCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	raw, err := json.Marshal(projection)
	return raw, err == nil
}

func openAIChatReasoningRawMessageSupported(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for name, value := range fields {
		switch name {
		case "role", "content", "reasoning_content", "reasoning", "tool_calls":
		case "refusal":
			if string(bytes.TrimSpace(value)) != "null" {
				return false
			}
		default:
			return false
		}
	}
	var calls []map[string]json.RawMessage
	if json.Unmarshal(fields["tool_calls"], &calls) != nil {
		return false
	}
	for _, call := range calls {
		if len(call) != 3 {
			return false
		}
		for key := range call {
			if key != "id" && key != "type" && key != "function" {
				return false
			}
		}
		var function map[string]json.RawMessage
		if json.Unmarshal(call["function"], &function) != nil || len(function) != 2 || function["name"] == nil || function["arguments"] == nil {
			return false
		}
	}
	return true
}

func openAIChatReasoningHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func openAIChatReasoningCanonical(raw json.RawMessage) ([]byte, error) {
	// Duplicate members have no portable wire meaning. Disable replay for an
	// ambiguous history instead of collapsing distinct inputs onto one key.
	return canonicalReasoningCacheJSON(raw)
}

// The prefix is the exact Responses history and instruction/tool contract, not
// the gateway's prompt-cache key (which is only a coarse scheduling hint).
// Tool choice, streaming and per-turn output limits intentionally do not bind a
// previous completed batch: callers normally release a forced tool on round 2.
func openAIChatReasoningPrefixHash(body []byte, input []json.RawMessage) (string, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", err
	}
	stable := make(map[string]json.RawMessage)
	for _, name := range []string{"instructions", "tools", "parallel_tool_calls", "text", "previous_response_id", "conversation"} {
		if value, ok := envelope[name]; ok {
			stable[name] = value
		}
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	stable["input"] = inputJSON
	encoded, err := json.Marshal(stable)
	if err != nil {
		return "", err
	}
	canonical, err := openAIChatReasoningCanonical(encoded)
	if err != nil {
		return "", err
	}
	return openAIChatReasoningHash(canonical), nil
}

func openAIChatReasoningBatchKey(prefix string, projection json.RawMessage) string {
	var visible openAIChatReasoningProjection
	if json.Unmarshal(projection, &visible) != nil {
		return ""
	}
	// reasoning_content is a nonstandard Chat extension. A normal SDK may
	// omit it on replay; completing precisely that lost state is this cache's
	// purpose. Visible assistant content and tool identity are always required.
	visible.Reasoning = ""
	canonical, _ := json.Marshal(visible)
	return openAIChatReasoningHash(append([]byte("chat-replay-v1:"+prefix+":"), canonical...))
}

func openAIChatReasoningProjectionMatches(stored, incoming json.RawMessage) bool {
	var original, client openAIChatReasoningProjection
	if json.Unmarshal(stored, &original) != nil || json.Unmarshal(incoming, &client) != nil {
		return false
	}
	if client.Reasoning != "" && client.Reasoning != original.Reasoning {
		return false
	}
	original.Reasoning, client.Reasoning = "", ""
	left, _ := json.Marshal(original)
	right, _ := json.Marshal(client)
	return bytes.Equal(left, right)
}

func openAIChatReasoningInput(body []byte) ([]json.RawMessage, bool) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal([]byte(input.Raw), &items) != nil {
		return nil, false
	}
	return items, true
}

func openAIChatReasoningFindProjection(input, projection []json.RawMessage) (int, bool) {
	if len(projection) == 0 || len(projection) > len(input) {
		return 0, false
	}
	wanted := make([][]byte, len(projection))
	for i := range projection {
		var err error
		wanted[i], err = openAIChatReasoningCanonical(projection[i])
		if err != nil {
			return 0, false
		}
	}
	match := -1
	for start := 0; start+len(projection) <= len(input); start++ {
		equal := true
		for i := range projection {
			actual, err := openAIChatReasoningCanonical(input[start+i])
			if err != nil || !bytes.Equal(actual, wanted[i]) {
				equal = false
				break
			}
		}
		if equal {
			if match >= 0 { // Reused tool identity is not an unambiguous batch.
				return 0, false
			}
			match = start
		}
	}
	return match, match >= 0
}

func openAIChatReasoningToolResultsComplete(messages []apicompat.ChatMessage, index int) bool {
	pending := make(map[string]struct{}, len(messages[index].ToolCalls))
	for _, call := range messages[index].ToolCalls {
		pending[call.ID] = struct{}{}
	}
	for i := index + 1; i < len(messages) && messages[i].Role == "tool"; i++ {
		if _, ok := pending[messages[i].ToolCallID]; !ok {
			return false
		}
		delete(pending, messages[i].ToolCallID)
	}
	return len(pending) == 0
}

type openAIChatReasoningReplayCandidate struct {
	key        string
	start      int
	length     int
	projection json.RawMessage
	canonical  []json.RawMessage
}

func (s *OpenAIGatewayService) prepareOpenAIChatReasoningReplay(ctx context.Context, c *gin.Context, account *Account, request *http.Request, chatBody, wireBody []byte, enabled bool) (*openAIChatReasoningReplay, []byte) {
	if c != nil {
		c.Set(openAIChatReasoningReplayContextKey, (*openAIChatReasoningReplay)(nil))
		c.Set("openai_chat_reasoning_replay_hits", 0)
		c.Set("openai_chat_reasoning_replay_stored", false)
	}
	if !enabled || account == nil || !account.IsOpenAIChatReasoningReplayEnabled() || ctx.Err() != nil {
		return nil, wireBody
	}
	store := s.openAIReasoningStateStore()
	if store == nil {
		return nil, wireBody
	}
	scope, err := buildOpenAIReasoningScope(c, account, request, wireBody)
	if err != nil {
		return nil, wireBody
	}
	input, ok := openAIChatReasoningInput(wireBody)
	if !ok {
		return nil, wireBody
	}
	replay := &openAIChatReasoningReplay{ctx: ctx, c: c, store: store, budget: openAIReasoningCacheBudgetForRequest(c), scope: scope}
	var requestMessages struct {
		Messages []json.RawMessage `json:"messages"`
	}
	var chatRequest apicompat.ChatCompletionsRequest
	if json.Unmarshal(chatBody, &requestMessages) != nil || json.Unmarshal(chatBody, &chatRequest) != nil || len(requestMessages.Messages) != len(chatRequest.Messages) {
		return nil, wireBody
	}
	var candidates []openAIChatReasoningReplayCandidate
	var keys []string
	for index, message := range chatRequest.Messages {
		if len(candidates) == OpenAIReasoningStateMaxLookupEntries {
			break
		}
		projection, supported := normalizeOpenAIChatReasoningProjection(message)
		if !supported || !openAIChatReasoningRawMessageSupported(requestMessages.Messages[index]) || !openAIChatReasoningToolResultsComplete(chatRequest.Messages, index) {
			continue
		}
		items, err := apicompat.ChatMessageResponsesInput(message)
		if err != nil {
			continue
		}
		start, unique := openAIChatReasoningFindProjection(input, items)
		if !unique || (len(candidates) > 0 && start < candidates[len(candidates)-1].start+candidates[len(candidates)-1].length) {
			continue
		}
		withoutReasoning := message
		withoutReasoning.ReasoningContent, withoutReasoning.Reasoning = "", ""
		canonical, err := apicompat.ChatMessageResponsesInput(withoutReasoning)
		if err != nil {
			continue
		}
		candidates = append(candidates, openAIChatReasoningReplayCandidate{start: start, length: len(items), projection: projection, canonical: canonical})
	}
	// Normalize optional reasoning only in the hash projection; the real input
	// is never modified on a miss. This keeps keys stable across ordinary Chat
	// SDKs that omit reasoning_content and extended clients that return it.
	projected := make([]json.RawMessage, 0, len(input))
	previousEnd := 0
	for i := range candidates {
		candidate := &candidates[i]
		projected = append(projected, input[previousEnd:candidate.start]...)
		prefix, hashErr := openAIChatReasoningPrefixHash(wireBody, projected)
		if hashErr != nil {
			return nil, wireBody
		}
		candidate.key = openAIChatReasoningBatchKey(prefix, candidate.projection)
		keys = append(keys, candidate.key)
		projected = append(projected, candidate.canonical...)
		previousEnd = candidate.start + candidate.length
	}
	projected = append(projected, input[previousEnd:]...)
	replay.projectedPrefix, err = openAIChatReasoningPrefixHash(wireBody, projected)
	if err != nil {
		return nil, wireBody
	}
	if len(keys) > 0 {
		var batches map[string]OpenAIReasoningBatch
		err = replay.budget.Do(ctx, func(ioCtx context.Context) error {
			var readErr error
			batches, readErr = store.GetOpenAIReasoningBatches(ioCtx, scope, keys)
			return readErr
		})
		if err == nil {
			// Every lookup is batched. Replacements remain ordered so a missing
			// earlier batch cannot grant authority to a later opaque prefix.
			offset := 0
			for _, candidate := range candidates {
				batch, found := batches[candidate.key]
				if !found || !openAIChatReasoningProjectionMatches(batch.Projection, candidate.projection) {
					continue
				}
				actualStart := candidate.start + offset
				prefix, hashErr := openAIChatReasoningPrefixHash(wireBody, input[:actualStart])
				projection, valid := projectOpenAIChatReasoningRawBatch(batch.Output)
				if hashErr != nil || prefix != batch.InputPrefixHash || !valid || !bytes.Equal(projection, batch.Projection) {
					continue
				}
				next := make([]json.RawMessage, 0, len(input)-candidate.length+len(batch.Output))
				next = append(next, input[:actualStart]...)
				next = append(next, batch.Output...)
				next = append(next, input[actualStart+candidate.length:]...)
				input = next
				offset += len(batch.Output) - candidate.length
				replay.replayed = append(replay.replayed, openAIChatReasoningReplayHit{key: candidate.key, payloadHash: batch.PayloadHash, ciphers: openAIChatReasoningCipherHashes(batch.Output)})
			}
		}
	}
	if len(replay.replayed) > 0 {
		encoded, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return nil, wireBody
		}
		rewritten, rewriteErr := sjson.SetRawBytes(wireBody, "input", encoded)
		if rewriteErr != nil {
			return nil, wireBody
		}
		wireBody = rewritten
	}
	if c != nil {
		c.Set(openAIChatReasoningReplayContextKey, replay)
		c.Set("openai_chat_reasoning_replay_hits", len(replay.replayed))
	}
	return replay, wireBody
}

// SetSentBody runs after both the replay and negative-cache preparation so the
// next batch binds to what actually reached the transport, including a visible
// lossy recovery on this request. It does not retain any history plaintext.
func (r *openAIChatReasoningReplay) SetSentBody(body []byte) {
	if r == nil {
		return
	}
	r.recorder = openAIChatReasoningReplayRecorder{}
	r.actualPrefix = ""
	if input, ok := openAIChatReasoningInput(body); ok {
		r.actualPrefix, _ = openAIChatReasoningPrefixHash(body, input)
	}
}

func (r *openAIChatReasoningReplay) InvalidateRejected(cipherHashes []string) {
	if r == nil || len(cipherHashes) == 0 {
		return
	}
	for _, hit := range r.replayed {
		matches := false
		for _, hash := range cipherHashes {
			if _, found := hit.ciphers[hash]; found {
				matches = true
				break
			}
		}
		if matches {
			_ = r.budget.Do(r.ctx, func(ioCtx context.Context) error {
				_, err := r.store.DeleteOpenAIReasoningBatchIfMatch(ioCtx, r.scope, hit.key, hit.payloadHash)
				return err
			})
		}
	}
}

func (r *openAIChatReasoningReplay) Commit() {
	if r == nil || r.actualPrefix == "" || r.ctx.Err() != nil || (r.c != nil && r.c.Request != nil && r.c.Request.Context().Err() != nil) {
		return
	}
	batch, ok := r.recorder.Batch()
	if !ok {
		return
	}
	batch.InputPrefixHash = r.actualPrefix
	key := openAIChatReasoningBatchKey(r.projectedPrefix, batch.Projection)
	stored := false
	err := r.budget.Do(r.ctx, func(ioCtx context.Context) error {
		var writeErr error
		stored, writeErr = r.store.PutOpenAIReasoningBatch(ioCtx, r.scope, key, batch)
		return writeErr
	})
	if r.c != nil {
		r.c.Set("openai_chat_reasoning_replay_stored", err == nil && stored)
	}
}

func openAIChatReasoningReplayFromContext(c *gin.Context) *openAIChatReasoningReplay {
	if c == nil {
		return nil
	}
	value, _ := c.Get(openAIChatReasoningReplayContextKey)
	replay, _ := value.(*openAIChatReasoningReplay)
	return replay
}

func openAIChatReasoningRecoveryFromContext(c *gin.Context) *openAIReasoningRecoveryState {
	if c == nil {
		return nil
	}
	value, _ := c.Get(openAIReasoningRecoveryContextKey)
	recovery, _ := value.(*openAIReasoningRecoveryState)
	return recovery
}

func observeOpenAIChatReasoningReplayPayload(c *gin.Context, payload []byte) {
	if replay := openAIChatReasoningReplayFromContext(c); replay != nil {
		replay.recorder.ObservePayload(payload)
	}
}

func openAIChatReasoningCipherHashes(output []json.RawMessage) map[string]struct{} {
	hashes := make(map[string]struct{})
	for _, item := range output {
		if gjson.GetBytes(item, "type").String() == "reasoning" {
			cipher := gjson.GetBytes(item, "encrypted_content")
			if cipher.Type == gjson.String && cipher.String() != "" {
				hashes[openAIChatReasoningHash([]byte(cipher.String()))] = struct{}{}
			}
		}
	}
	return hashes
}

// Only known function batches are reversible. Unknown fields on known items
// are retained verbatim, but unknown item/content kinds are never truncated.
func projectOpenAIChatReasoningRawBatch(output []json.RawMessage) (json.RawMessage, bool) {
	if len(output) == 0 || len(output) > 64 {
		return nil, false
	}
	message := apicompat.ChatMessage{Role: "assistant"}
	var text strings.Builder
	var reasoning strings.Builder
	totalBytes := 0
	hasCipher := false
	for _, raw := range output {
		totalBytes += len(raw)
		if totalBytes > 256*1024 || !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
			return nil, false
		}
		if _, err := canonicalReasoningCacheJSON(raw); err != nil {
			return nil, false
		}
		if status := gjson.GetBytes(raw, "status"); status.Exists() && status.String() != "completed" {
			return nil, false
		}
		switch gjson.GetBytes(raw, "type").String() {
		case "reasoning":
			summary := gjson.GetBytes(raw, "summary")
			if !summary.IsArray() {
				return nil, false
			}
			for _, part := range summary.Array() {
				if part.Get("type").String() != "summary_text" || part.Get("text").Type != gjson.String {
					return nil, false
				}
				reasoning.WriteString(part.Get("text").String())
			}
			cipher := gjson.GetBytes(raw, "encrypted_content")
			if cipher.Exists() && cipher.Type != gjson.Null && cipher.Type != gjson.String {
				return nil, false
			}
			hasCipher = hasCipher || cipher.String() != ""
		case "message":
			if gjson.GetBytes(raw, "role").String() != "assistant" || !gjson.GetBytes(raw, "content").IsArray() {
				return nil, false
			}
			for _, part := range gjson.GetBytes(raw, "content").Array() {
				if part.Get("type").String() != "output_text" || part.Get("text").Type != gjson.String {
					return nil, false
				}
				text.WriteString(part.Get("text").String())
			}
		case "function_call":
			if namespace := gjson.GetBytes(raw, "namespace"); namespace.Exists() && namespace.Type != gjson.Null && namespace.String() != "" {
				return nil, false
			}
			args := gjson.GetBytes(raw, "arguments")
			if args.Type != gjson.String || gjson.GetBytes(raw, "call_id").Type != gjson.String || gjson.GetBytes(raw, "name").Type != gjson.String {
				return nil, false
			}
			message.ToolCalls = append(message.ToolCalls, apicompat.ChatToolCall{ID: gjson.GetBytes(raw, "call_id").String(), Type: "function", Function: apicompat.ChatFunctionCall{Name: gjson.GetBytes(raw, "name").String(), Arguments: args.String()}})
		default:
			return nil, false
		}
	}
	if !hasCipher {
		return nil, false
	}
	message.Content, _ = json.Marshal(text.String())
	message.ReasoningContent = reasoning.String()
	return normalizeOpenAIChatReasoningProjection(message)
}

type openAIChatReasoningReplayRecorder struct {
	output     []json.RawMessage
	projection openAIChatReasoningProjection
	completed  bool
	invalid    bool
	finished   bool
	bytes      int
}

func (r *openAIChatReasoningReplayRecorder) ObservePayload(payload []byte) {
	if r == nil || r.invalid || r.completed {
		return
	}
	kind := gjson.GetBytes(payload, "type").String()
	if kind == "response.failed" || kind == "response.incomplete" || kind == "error" {
		r.invalid = true
		return
	}
	if kind != "response.completed" && kind != "response.done" {
		return
	}
	response := gjson.GetBytes(payload, "response")
	if response.Get("status").String() != "completed" || (response.Get("error").Exists() && response.Get("error").Type != gjson.Null) || (response.Get("incomplete_details").Exists() && response.Get("incomplete_details").Type != gjson.Null) || !response.Get("output").IsArray() {
		r.invalid = true
		return
	}
	// No reconstruction from partial done items: this path requires a complete
	// authoritative terminal output array and a matching downstream projection.
	outputRaw := []byte(response.Get("output").Raw)
	if len(outputRaw) > 256*1024 || json.Unmarshal(outputRaw, &r.output) != nil {
		r.invalid = true
		return
	}
	if _, valid := projectOpenAIChatReasoningRawBatch(r.output); !valid {
		r.invalid = true
		return
	}
	r.completed = true
}

func (r *openAIChatReasoningReplayRecorder) ObserveMessage(message apicompat.ChatMessage) {
	if r == nil || r.invalid {
		return
	}
	projection, ok := normalizeOpenAIChatReasoningProjection(message)
	if !ok || json.Unmarshal(projection, &r.projection) != nil {
		r.invalid = true
		return
	}
	r.finished = true
}

func (r *openAIChatReasoningReplayRecorder) ObserveChunks(chunks []apicompat.ChatCompletionsChunk) {
	if r == nil || r.invalid {
		return
	}
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			if choice.Index != 0 || (choice.Delta.Role != "" && choice.Delta.Role != "assistant") || choice.Delta.Reasoning != nil {
				r.invalid = true
				return
			}
			if choice.Delta.Content != nil {
				r.projection.Content += *choice.Delta.Content
				r.bytes += len(*choice.Delta.Content)
			}
			if choice.Delta.ReasoningContent != nil {
				r.projection.Reasoning += *choice.Delta.ReasoningContent
				r.bytes += len(*choice.Delta.ReasoningContent)
			}
			for _, call := range choice.Delta.ToolCalls {
				if call.Index == nil || *call.Index < 0 || *call.Index >= 32 || *call.Index > len(r.projection.Calls) {
					r.invalid = true
					return
				}
				if *call.Index == len(r.projection.Calls) {
					if call.ID == "" || call.Type != "function" || call.Function.Name == "" {
						r.invalid = true
						return
					}
					r.projection.Calls = append(r.projection.Calls, openAIChatReasoningProjectionCall{ID: call.ID, Name: call.Function.Name})
				} else if call.ID != "" || call.Type != "" || call.Function.Name != "" {
					r.invalid = true
					return
				}
				r.projection.Calls[*call.Index].Arguments += call.Function.Arguments
				r.bytes += len(call.ID) + len(call.Function.Name) + len(call.Function.Arguments)
				if r.bytes > OpenAIReasoningBatchMaxBytes {
					r.Stop()
					return
				}
			}
			if choice.FinishReason != nil {
				if *choice.FinishReason != "tool_calls" || r.finished {
					r.invalid = true
					return
				}
				r.finished = true
			}
		}
	}
	if r.bytes > OpenAIReasoningBatchMaxBytes {
		r.Stop()
	}
}

func (r *openAIChatReasoningReplayRecorder) Batch() (OpenAIReasoningBatch, bool) {
	if r == nil || r.invalid || !r.completed || !r.finished {
		return OpenAIReasoningBatch{}, false
	}
	wanted, ok := projectOpenAIChatReasoningRawBatch(r.output)
	actual, err := json.Marshal(r.projection)
	if !ok || err != nil || !bytes.Equal(wanted, actual) {
		return OpenAIReasoningBatch{}, false
	}
	return OpenAIReasoningBatch{Output: r.output, Projection: actual}, true
}

func (r *openAIChatReasoningReplayRecorder) Stop() {
	if r != nil {
		r.invalid = true
		r.output = nil
		r.projection = openAIChatReasoningProjection{}
	}
}

// Semantic output, unlike a role chunk or SSE comment, commits this generation
// and disallows hidden re-execution. This is intentionally independent of usage.
func openAIChatChunksHaveSemanticOutput(chunks []apicompat.ChatCompletionsChunk) bool {
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			delta := choice.Delta
			if len(delta.ToolCalls) > 0 || (delta.Content != nil && *delta.Content != "") || (delta.ReasoningContent != nil && *delta.ReasoningContent != "") || (delta.Reasoning != nil && *delta.Reasoning != "") {
				return true
			}
		}
	}
	return false
}

func openAIChatReasoningReplayError(err error) error {
	return fmt.Errorf("prepare chat reasoning replay request: %w", err)
}

func cloneOpenAIChatRequestWithBody(request *http.Request, body []byte) *http.Request {
	cloned := request.Clone(request.Context())
	snapshot := bytes.Clone(body)
	cloned.Body = io.NopCloser(bytes.NewReader(snapshot))
	cloned.ContentLength = int64(len(snapshot))
	cloned.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(snapshot)), nil }
	return cloned
}

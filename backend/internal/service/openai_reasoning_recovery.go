package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const openAIReasoningRecoveryContextKey = "openai_reasoning_recovery_state"
const openAIReasoningRecoveryHeader = "X-Sub2API-Reasoning-Recovery"

// The reasoning_recovery Ops detail is bounded by its encoded size: the Ops
// queue keeps at most OpsErrorLogQueueBodyMaxBytes per detail and shrinks a
// larger JSON detail to fields it does not know, which would lose everything.
// The upstream message and payload start at these caps and are halved until
// the encoded detail fits; action, items, usage and payload_sha256 always stay.
const (
	openAIReasoningRecoveryDetailEncodedLimit = 6 << 10
	openAIReasoningRecoveryMessageDetailLimit = 1 << 10
	openAIReasoningRecoveryPayloadDetailLimit = 4 << 10
)

// openAIReasoningRecoveryState owns the single, explicitly lossy retry for an
// HTTP Responses request on one account. It never re-enters a protocol
// converter or scheduler itself; when the account cannot serve the request
// because of ciphertext it cannot verify, StopError says so and the handler
// decides whether another account is tried.
// original is immutable; wire is the exact payload of the latest sent attempt.
//
// Apart from that retry it may re-send once more to undo a side effect of its
// own strip: an upstream that looks up by id an item left without ciphertext
// and does not find it (TryRepairUnfoundItemIDs).
type openAIReasoningRecoveryState struct {
	ctx      context.Context
	c        *gin.Context
	account  *Account
	enabled  bool
	token    string
	original []byte
	wire     []byte
	scope    OpenAIReasoningCacheScope
	store    OpenAIReasoningStateStore
	budget   *OpenAIReasoningCacheBudget

	identity      string
	attemptUsage  map[string]int64
	retryBody     []byte
	retryUsed     bool
	stopRecorded  bool
	retryCleanups []func()

	diagnosticIncoming       []byte
	diagnosticRequest        *http.Request
	responseStatus           int
	responseHeaders          http.Header
	failurePayload           []byte
	semanticCommitted        bool
	cacheSkippedItems        int
	diagnosticState          string
	retryDispatched          bool
	diagnosticStopReason     string
	failureObservedBytes     int
	failureTerminalForwarded bool
	// redactPayload applies the upstream Agent Identity credential redaction
	// before an upstream payload is recorded for administrators.
	redactPayload func([]byte) []byte

	// switchAccounts is the global switch for this request, whether or not
	// this account type supports the stripped retry.
	switchAccounts bool
	// accountMismatch records that this attempt ended as an account mismatch.
	accountMismatch bool
	// accountFailure records that the stripped retry failed for a reason of
	// the account's own.
	accountFailure bool

	// stripped lists the reasoning items whose ciphertext this attempt removed,
	// by their position in the wire body.
	stripped []openAIReasoningCipherItem
	// dropItemIDs makes a strip remove the item's id with its ciphertext. It
	// is set by the repair, for the rest of this attempt.
	dropItemIDs bool
	// idRepairUsed records the one re-send without the ids of stripped items.
	idRepairUsed   bool
	itemIDsRemoved int
}

func (s *OpenAIGatewayService) newOpenAIReasoningRecoveryState(ctx context.Context, c *gin.Context, account *Account, token string) *openAIReasoningRecoveryState {
	if ctx == nil {
		ctx = context.Background()
	}
	// The global switch is read here, once for this forwarding attempt.
	switchedOn := s.reasoningRecovery.Enabled() && !isOpenAICompatMessagesBridgeContext(c)
	r := &openAIReasoningRecoveryState{
		ctx: ctx, c: c, account: account, token: token,
		enabled: switchedOn && account.supportsOpenAIReasoningRecovery(), switchAccounts: switchedOn,
		store: s.openAIReasoningStateStore(), budget: openAIReasoningCacheBudgetForRequest(c),
	}
	r.redactPayload = func(payload []byte) []byte { return s.redactAgentIdentitySensitiveBody(ctx, account, payload) }
	if c != nil {
		c.Set(openAIReasoningRecoveryContextKey, r)
	}
	return r
}

// SetReasoningRecoveryService installs the global switch. Without one, recovery
// is enabled.
func (s *OpenAIGatewayService) SetReasoningRecoveryService(recovery *ReasoningRecoveryService) {
	if s != nil {
		s.reasoningRecovery = recovery
	}
}

func (r *openAIReasoningRecoveryState) Close() {
	if r == nil {
		return
	}
	for _, cleanup := range r.retryCleanups {
		cleanup()
	}
	r.retryCleanups = nil
}

func (r *openAIReasoningRecoveryState) RecoveryAttempt() bool { return r != nil && r.retryUsed }

func openAIReasoningRecoveryStateFromContext(c *gin.Context) *openAIReasoningRecoveryState {
	if c == nil {
		return nil
	}
	value, _ := c.Get(openAIReasoningRecoveryContextKey)
	state, _ := value.(*openAIReasoningRecoveryState)
	return state
}

// BindDiagnosticRequest retains only the forwarding-entry body and the exact
// request used by this attempt. The immutable wire snapshot already captured by
// PrepareRequest remains the source of truth after GetBody is disabled.
func (r *openAIReasoningRecoveryState) BindDiagnosticRequest(incoming []byte, req *http.Request) {
	if r == nil {
		return
	}
	if r.diagnosticIncoming == nil {
		r.diagnosticIncoming = bytes.Clone(incoming)
	}
	r.diagnosticRequest = req
	r.responseStatus, r.responseHeaders = 0, nil
	r.failurePayload = nil
	r.semanticCommitted = false
	r.failureObservedBytes = 0
	r.failureTerminalForwarded = false
}

func (r *openAIReasoningRecoveryState) ObserveResponse(resp *http.Response) {
	if r == nil || resp == nil {
		return
	}
	r.responseStatus, r.responseHeaders = resp.StatusCode, resp.Header.Clone()
}

func (r *openAIReasoningRecoveryState) MarkAttemptDispatched() {
	if r != nil && r.retryUsed {
		r.retryDispatched = true
		r.diagnosticState = "retry_attempted"
	}
}

// ObserveFailure is observation only. A bare SSE error can be superseded by a
// completed response; neither the retry budget nor rejection cache changes here.
func (r *openAIReasoningRecoveryState) ObserveFailure(payload []byte, semanticCommitted bool) {
	if r == nil {
		return
	}
	r.failurePayload = bytes.Clone(payload)
	r.semanticCommitted = semanticCommitted
	r.failureTerminalForwarded = false
	if r.c != nil && r.c.Writer != nil {
		r.failureObservedBytes = max(0, OpenAICompactKeepaliveAdjustedWrittenSize(r.c))
	}
}

// Called only after a stream parser has dispatched and flushed a complete
// failure terminal without a write error. Semantic output or an HTTP header
// alone is not evidence that the client has received the failure itself.
func markOpenAIReasoningFailureTerminalForwarded(c *gin.Context) {
	r := openAIReasoningRecoveryStateFromContext(c)
	if r == nil || r.diagnosticIncoming == nil || len(r.failurePayload) == 0 || c.Writer == nil {
		return
	}
	if max(0, OpenAICompactKeepaliveAdjustedWrittenSize(c)) > r.failureObservedBytes {
		r.failureTerminalForwarded = true
	}
}

// buildOpenAIReasoningScope identifies the source whose rejections are remembered.
// Credentials and route identity enter only a digest; neither is stored or logged.
func buildOpenAIReasoningScope(c *gin.Context, account *Account, req *http.Request, wireBody []byte) (OpenAIReasoningCacheScope, error) {
	if c == nil || account == nil || req == nil || req.URL == nil {
		return OpenAIReasoningCacheScope{}, errors.New("reasoning cache source unavailable")
	}
	key := getAPIKeyFromContext(c)
	if key == nil {
		return OpenAIReasoningCacheScope{}, errors.New("reasoning cache tenant unavailable")
	}
	groupID := int64(0)
	if key.GroupID != nil {
		groupID = *key.GroupID
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	identity := openAIReasoningRequestIdentity(account, req, proxyURL, "")
	return BuildOpenAIReasoningCacheScope(OpenAIReasoningScopeInput{
		UserID: key.UserID, APIKeyID: key.ID, GroupID: groupID, AccountID: account.ID,
		SourceIdentityHash: identity, Endpoint: req.URL.String(),
		Model:     gjson.GetBytes(wireBody, "model").String(),
		Reasoning: json.RawMessage(gjson.GetBytes(wireBody, "reasoning").Raw),
	})
}

func openAIReasoningRequestIdentity(account *Account, req *http.Request, proxyURL, token string) string {
	identity := struct {
		AccountID                                                    int64
		AccountType, Endpoint, Host, Method, Proxy, Token            string
		Authorization, Organization, Project, ChatGPTAccount         string
		CredentialAccount, CredentialOrganization, CredentialProject string
	}{
		AccountID: account.ID, AccountType: account.Type, Endpoint: req.URL.String(),
		Host: req.Host, Method: req.Method, Proxy: proxyURL, Token: token,
		Authorization: req.Header.Get("Authorization"), Organization: req.Header.Get("OpenAI-Organization"),
		Project: req.Header.Get("OpenAI-Project"), ChatGPTAccount: req.Header.Get("ChatGPT-Account-ID"),
		CredentialAccount: account.GetCredential("account_id"), CredentialOrganization: account.GetCredential("organization_id"),
		CredentialProject: account.GetCredential("project_id"),
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	return openAIReasoningDigest(encoded)
}

func openAIReasoningDigest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// PrepareRequest is called at the actual HTTP send boundary, after all normal
// transformations. Recovery compares the entire final body and source, so a
// later builder cannot silently remap or reinject state.
func (r *openAIReasoningRecoveryState) PrepareRequest(req *http.Request, body []byte, proxyURL string) (*http.Request, []byte, error) {
	if r == nil || !r.enabled {
		return req, body, nil
	}
	if req == nil || req.URL == nil || req.Method != http.MethodPost ||
		(!strings.HasSuffix(req.URL.Path, "/responses") && !strings.HasSuffix(req.URL.Path, "/responses/compact")) {
		if r.retryUsed {
			r.diagnosticStopReason = "endpoint_changed"
			return nil, nil, r.StopError(errors.New("reasoning recovery endpoint changed"))
		}
		r.enabled = false
		return req, body, nil
	}
	// NewRequest's GetBody reflects endpoint adaptations in the builder, not the
	// caller's earlier request. Never infer the final wire input from that caller.
	if req.GetBody != nil {
		reader, err := req.GetBody()
		if err != nil {
			return nil, nil, errors.New("reasoning recovery request snapshot unavailable")
		}
		body, err = io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return nil, nil, errors.New("reasoning recovery request snapshot unavailable")
		}
	}
	identity := openAIReasoningRequestIdentity(r.account, req, proxyURL, r.token)
	if identity == "" {
		return nil, nil, errors.New("reasoning recovery source identity unavailable")
	}
	if r.retryUsed {
		if r.ctx.Err() != nil {
			r.diagnosticStopReason = "request_cancelled"
			return nil, nil, r.StopError(r.ctx.Err())
		}
		if identity != r.identity || !bytes.Equal(body, r.retryBody) {
			r.diagnosticStopReason = "source_changed"
			return nil, nil, r.StopError(errors.New("reasoning recovery source changed"))
		}
		// The normal gateway may detach cancellation to drain usage. An extra
		// generation has no such permission: preserve the original deadline and
		// cancellation while retaining transport-profile values on req.Context().
		retryCtx, cancel := context.WithCancel(req.Context())
		if deadline, ok := r.ctx.Deadline(); ok {
			var deadlineCancel context.CancelFunc
			retryCtx, deadlineCancel = context.WithDeadline(retryCtx, deadline)
			r.retryCleanups = append(r.retryCleanups, deadlineCancel)
		}
		stop := context.AfterFunc(r.ctx, cancel)
		r.retryCleanups = append(r.retryCleanups, func() { stop(); cancel() })
		req = req.Clone(retryCtx)
	} else {
		if r.original == nil {
			r.original = bytes.Clone(body)
		}
		r.identity = identity
		r.scope, _ = buildOpenAIReasoningScope(r.c, r.account, req, body)
		body = r.skipRejectedHistory(body)
	}
	r.wire = bytes.Clone(body)
	r.attemptUsage = nil
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(req.Context()))
	if req.Body != nil {
		_ = req.Body.Close()
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	// The context flag blocks all redirect statuses, including 301/302/303's
	// POST-to-GET conversion. Clearing GetBody also disallows transparent POST
	// replay on a reused-connection error.
	req.GetBody = nil
	r.diagnosticRequest = req
	return req, body, nil
}

func (r *openAIReasoningRecoveryState) skipRejectedHistory(body []byte) []byte {
	if r.store == nil || r.scope.ScopeHash == "" {
		return body
	}
	if !openAIReasoningToolHistoryAllowsRecovery(body) {
		return body
	}
	items := openAIReasoningCipherItems(body)
	if len(items) == 0 {
		return body
	}
	if _, err := canonicalReasoningCacheJSON(body); err != nil {
		return body
	}
	hashes := make([]string, 0, len(items))
	for _, item := range items {
		hashes = append(hashes, item.hash)
	}
	// The store takes a bounded number of hashes per call; a long history is
	// looked up in batches inside the one I/O budget.
	rejected := make(map[string]OpenAIRejectedReasoning, len(hashes))
	err := r.budget.Do(r.ctx, func(ioCtx context.Context) error {
		for batch := range slices.Chunk(hashes, OpenAIReasoningStateMaxLookupEntries) {
			found, readErr := r.store.GetOpenAIRejectedReasoning(ioCtx, r.scope, batch)
			if readErr != nil {
				return readErr
			}
			maps.Copy(rejected, found)
		}
		return nil
	})
	if err != nil {
		return body
	}
	now := time.Now()
	skipped := make([]openAIReasoningCipherItem, 0, len(items))
	for _, item := range items {
		if old, ok := rejected[item.hash]; ok && !old.RejectedAt.IsZero() &&
			!old.RejectedAt.After(now) && now.Before(old.ExpiresAt) && now.Before(old.RejectedAt.Add(OpenAIReasoningStateTTL)) {
			skipped = append(skipped, item)
		}
	}
	if len(skipped) == 0 {
		return body
	}
	stripped, err := r.strip(body, skipped)
	if err != nil {
		return body
	}
	r.cacheSkippedItems += len(skipped)
	r.record("rejected_history_skipped", 0, "", nil, len(skipped))
	return stripped
}

// strip removes the ciphertext of items from body and remembers them as
// stripped by this attempt. After the repair the id goes with the ciphertext.
func (r *openAIReasoningRecoveryState) strip(body []byte, items []openAIReasoningCipherItem) ([]byte, error) {
	edit := openAIReasoningRecoveryEdit{}
	for _, item := range items {
		edit.cipher = append(edit.cipher, item.index)
		if r.dropItemIDs && item.id != "" && openAIReasoningItemStandsWithoutID(gjson.GetBytes(body, fmt.Sprintf("input.%d", item.index))) {
			edit.ids = append(edit.ids, item.index)
		}
	}
	stripped, err := applyOpenAIReasoningRecoveryEdit(body, edit)
	if err != nil {
		return nil, err
	}
	r.stripped = append(r.stripped, items...)
	r.itemIDsRemoved += len(edit.ids)
	return stripped, nil
}

type openAIReasoningCipherItem struct {
	index int
	hash  string
	id    string
}

func openAIReasoningCipherItems(body []byte) []openAIReasoningCipherItem {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return nil
	}
	var out []openAIReasoningCipherItem
	for index, item := range input.Array() {
		cipher := item.Get("encrypted_content")
		if item.Get("type").String() == "reasoning" && cipher.Type == gjson.String && cipher.String() != "" {
			entry := openAIReasoningCipherItem{index: index, hash: openAIReasoningDigest([]byte(cipher.String()))}
			if id := item.Get("id"); id.Type == gjson.String {
				entry.id = id.String()
			}
			out = append(out, entry)
		}
	}
	return out
}

func openAIReasoningToolHistoryAllowsRecovery(body []byte) bool {
	var input []any
	if err := decodeOpenAIJSONUseNumber([]byte(gjson.GetBytes(body, "input").Raw), &input); err != nil {
		return false
	}
	conversation := gjson.GetBytes(body, "conversation")
	hasServerContext := gjson.GetBytes(body, "previous_response_id").String() != "" || (conversation.Exists() && conversation.Type != gjson.Null)
	return validateOpenAIResponsesToolOutputs(input, hasServerContext) == nil
}

func openAIReasoningRejectedIndices(body []byte, rejection openAIReasoningRejection) ([]int, []string) {
	indices, _ := selectOpenAIRecoveryIndices(body, rejection)
	if len(indices) == 0 {
		return nil, nil
	}
	byIndex := map[int]string{}
	for _, item := range openAIReasoningCipherItems(body) {
		byIndex[item.index] = item.hash
	}
	hashes := make([]string, 0, len(indices))
	seen := map[string]bool{}
	for _, index := range indices {
		hash := byIndex[index]
		if !seen[hash] {
			hashes = append(hashes, hash)
			seen[hash] = true
		}
	}
	return indices, hashes
}

// Evidence preserves numeric field presence, not default-zero struct members.
func openAIReasoningUsageEvidence(payload []byte) map[string]int64 {
	var result map[string]int64
	for _, prefix := range []string{"usage", "response.usage"} {
		usage := gjson.GetBytes(payload, prefix)
		if !usage.IsObject() {
			continue
		}
		for field, path := range map[string]string{
			"input_tokens": "input_tokens", "output_tokens": "output_tokens", "total_tokens": "total_tokens",
			"cached_tokens": "input_tokens_details.cached_tokens", "reasoning_tokens": "output_tokens_details.reasoning_tokens",
			"cache_creation_input_tokens": "cache_creation_input_tokens", "cache_read_input_tokens": "cache_read_input_tokens",
		} {
			value := usage.Get(path)
			if value.Type != gjson.Number {
				continue
			}
			count, err := strconv.ParseInt(value.Raw, 10, 64)
			if err != nil || count < 0 {
				continue
			}
			if result == nil {
				result = make(map[string]int64)
			}
			result[field] = count
		}
	}
	return result
}

func observeOpenAIReasoningAttemptUsage(c *gin.Context, payload []byte) {
	if c == nil {
		return
	}
	value, _ := c.Get(openAIReasoningRecoveryContextKey)
	state, _ := value.(*openAIReasoningRecoveryState)
	if state == nil || !state.enabled {
		return
	}
	if usage := openAIReasoningUsageEvidence(payload); len(usage) > 0 {
		state.attemptUsage = usage
	}
}

func countOpenAIEncryptedFields(value any) int {
	count := 0
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "encrypted_content" && child != nil {
				count++
			}
			count += countOpenAIEncryptedFields(child)
		}
	case []any:
		for _, child := range v {
			count += countOpenAIEncryptedFields(child)
		}
	}
	return count
}

func stripOpenAIReasoningCipherIndices(body []byte, indices []int) ([]byte, error) {
	return deleteOpenAIReasoningItemField(body, indices, "encrypted_content")
}

func deleteOpenAIReasoningItemField(body []byte, indices []int, field string) ([]byte, error) {
	out := bytes.Clone(body)
	for _, index := range indices {
		path := fmt.Sprintf("input.%d", index)
		if gjson.GetBytes(out, path+".type").String() != "reasoning" {
			return nil, errors.New("reasoning recovery target mismatch")
		}
		var err error
		out, err = sjson.DeleteBytes(out, path+"."+field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// openAIReasoningRecoveryEdit is everything recovery may do to a request: at
// the listed input positions, all reasoning items, delete encrypted_content
// and delete id. Positions never move.
type openAIReasoningRecoveryEdit struct{ cipher, ids []int }

// applyOpenAIReasoningRecoveryEdit makes the edit in one fixed order, so that
// the same edit yields the same bytes wherever it is repeated.
func applyOpenAIReasoningRecoveryEdit(body []byte, edit openAIReasoningRecoveryEdit) ([]byte, error) {
	out, err := deleteOpenAIReasoningItemField(body, edit.cipher, "encrypted_content")
	if err != nil {
		return nil, err
	}
	return deleteOpenAIReasoningItemField(out, edit.ids, "id")
}

// openAIReasoningItemHasCipher is the test openAIReasoningCipherItems applies.
func openAIReasoningItemHasCipher(item gjson.Result) bool {
	cipher := item.Get("encrypted_content")
	return cipher.Type == gjson.String && cipher.String() != ""
}

// openAIReasoningItemStandsWithoutID reports an item the upstream takes as it
// is once its id is gone. An item without a summary is refused then, so such
// an item keeps its id.
func openAIReasoningItemStandsWithoutID(item gjson.Result) bool {
	return item.Get("summary").IsArray()
}

// Signal is deliberately pure. A bare SSE error may be superseded by a later
// authoritative failed/completed event and must not consume retry or cache state.
func openAIReasoningRecoverySignal(c *gin.Context, payload []byte, semanticCommitted bool) error {
	if c == nil || semanticCommitted {
		return nil
	}
	v, _ := c.Get(openAIReasoningRecoveryContextKey)
	r, _ := v.(*openAIReasoningRecoveryState)
	if r == nil || !r.enabled || r.retryUsed || r.ctx.Err() != nil {
		return nil
	}
	rejection, ok := parseOpenAIReasoningRejection(payload)
	if !ok {
		return nil
	}
	indices, _ := openAIReasoningRejectedIndices(r.wire, rejection)
	if len(indices) == 0 {
		return nil
	}
	return &openAIReasoningRecoverySignalError{payload: bytes.Clone(payload)}
}

type openAIReasoningRecoverySignalError struct{ payload []byte }

func (*openAIReasoningRecoverySignalError) Error() string {
	return "reasoning signature recovery eligible before semantic output"
}

func (r *openAIReasoningRecoveryState) TryRecoverError(err error) ([]byte, bool) {
	var signal *openAIReasoningRecoverySignalError
	if r == nil || !errors.As(err, &signal) {
		return nil, false
	}
	return r.TryRecover(r.upstreamStatus(http.StatusBadRequest), r.responseHeaders, signal.payload, false)
}

func (r *openAIReasoningRecoveryState) TryRecover(status int, headers http.Header, payload []byte, semanticCommitted bool) ([]byte, bool) {
	if r != nil {
		r.ObserveFailure(payload, semanticCommitted)
		if r.responseStatus == 0 {
			r.responseStatus, r.responseHeaders = status, headers.Clone()
		}
	}
	if r == nil || !r.enabled || r.retryUsed || semanticCommitted || r.ctx.Err() != nil || openAIReasoningRecoveryProtectedStatus(status) {
		return nil, false
	}
	rejection, ok := parseOpenAIReasoningRejection(payload)
	if !ok {
		return nil, false
	}
	indices, hashes := openAIReasoningRejectedIndices(r.wire, rejection)
	if len(indices) == 0 {
		return nil, false
	}
	selected := make([]openAIReasoningCipherItem, 0, len(indices))
	for _, item := range openAIReasoningCipherItems(r.wire) {
		if slices.Contains(indices, item.index) {
			selected = append(selected, item)
		}
	}
	if len(selected) != len(indices) {
		return nil, false
	}
	stripped, err := r.strip(r.wire, selected)
	if err != nil {
		return nil, false
	}
	r.retryUsed = true
	r.retryBody = bytes.Clone(stripped)
	r.diagnosticState = "retry_prepared"
	if r.store != nil && r.scope.ScopeHash != "" {
		_ = r.budget.Do(r.ctx, func(ioCtx context.Context) error {
			for batch := range slices.Chunk(hashes, OpenAIReasoningStateMaxLookupEntries) {
				if err := r.store.PutOpenAIRejectedReasoning(ioCtx, r.scope, batch); err != nil {
					return err
				}
			}
			return nil
		})
	}
	r.record("retry_without_encrypted_content", status, rejection.code, payload, len(indices))
	return stripped, true
}

// TryRepairUnfoundItemIDs undoes a side effect of this attempt's own strip. A
// reasoning item left without ciphertext still carries its id, and an upstream
// may take such an item for a reference to one it stores. When it does not
// find it, it says so and names the id (officially with status 404); without
// the id it takes the item as it is. The repair is made only for that answer
// naming an item this attempt stripped, removes the id of every such item, and
// is re-sent once. It is not the stripped retry and does not spend it.
//
// Nothing is remembered: every attempt that meets the answer repairs again.
func (r *openAIReasoningRecoveryState) TryRepairUnfoundItemIDs(status int, headers http.Header, payload []byte) ([]byte, bool) {
	if r == nil || !r.enabled || r.idRepairUsed || len(r.stripped) == 0 || r.ctx.Err() != nil ||
		status < http.StatusBadRequest || openAIReasoningRecoveryProtectedStatus(status) {
		return nil, false
	}
	// The official answer to an item looked up by id and not found. A relay may
	// pass it on under another status; the named id is what identifies it.
	message := extractUpstreamErrorMessage(payload)
	if !strings.Contains(message, "Item with id '") {
		return nil, false
	}
	found := false
	ids := make([]int, 0, len(r.stripped))
	for _, item := range r.stripped {
		// What was stripped is checked against what was sent.
		sent := gjson.GetBytes(r.wire, fmt.Sprintf("input.%d", item.index))
		id := sent.Get("id")
		if item.id == "" || sent.Get("type").String() != "reasoning" || openAIReasoningItemHasCipher(sent) ||
			id.Type != gjson.String || id.String() != item.id || !openAIReasoningItemStandsWithoutID(sent) ||
			slices.Contains(ids, item.index) {
			continue
		}
		ids = append(ids, item.index)
		found = found || strings.Contains(message, "Item with id '"+item.id+"' not found")
	}
	if !found {
		return nil, false
	}
	repaired, err := applyOpenAIReasoningRecoveryEdit(r.wire, openAIReasoningRecoveryEdit{ids: ids})
	if err != nil {
		return nil, false
	}
	r.idRepairUsed, r.dropItemIDs = true, true
	r.itemIDsRemoved += len(ids)
	if r.retryUsed {
		// The stripped retry was already sent; the send boundary accepts only
		// the body it knows, and this one is not sent yet.
		r.retryBody = bytes.Clone(repaired)
		r.retryDispatched, r.diagnosticState = false, "retry_prepared"
	}
	if r.responseStatus == 0 {
		r.responseStatus, r.responseHeaders = status, headers.Clone()
	}
	r.record("retry_without_item_ids", status, "", payload, len(ids))
	return repaired, true
}

func openAIReasoningRecoveryProtectedStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusProxyAuthRequired, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

// OpenAIReasoningRecoveryTerminalError preserves classification for rendering
// and account-health accounting, but deliberately does not implement Unwrap.
// A caller must handle this terminal explicitly, never feed Failure back into
// scheduling: the single same-source recovery budget has already been spent.
// Two outcomes are not terminals, and StopError returns the attempt's failure
// marked for the next account instead: an account mismatch
// (openAICiphertextAccountMismatch) and a stripped retry that failed for a
// reason of the account's own (openAIRecoveryRetryAccountFailure).
type OpenAIReasoningRecoveryTerminalError struct {
	Failure *UpstreamFailoverError
	// FailureTerminalForwarded is explicit parser/write evidence, not a claim
	// inferred from semantic output or Writer.Written. Callers still finalize
	// accounting and health, but must not append a second response.failed.
	FailureTerminalForwarded bool
}

func (*OpenAIReasoningRecoveryTerminalError) Error() string {
	return "reasoning signature recovery stopped; no further upstream retry permitted"
}

func newOpenAIReasoningRecoveryTerminalError(failure *UpstreamFailoverError) *OpenAIReasoningRecoveryTerminalError {
	if failure == nil {
		failure = &UpstreamFailoverError{StatusCode: http.StatusBadGateway}
	}
	copyFailure := *failure
	copyFailure.ResponseBody = nil
	copyFailure.ResponseHeaders = failure.ResponseHeaders.Clone()
	copyFailure.NextAccountAction = NextAccountStop
	copyFailure.RetryableOnSameAccount = false
	copyFailure.SameAccountRetryDelay = 0
	copyFailure.SameAccountRetryDeadline = time.Time{}
	copyFailure.SameAccountRetryMax = 0
	return &OpenAIReasoningRecoveryTerminalError{Failure: &copyFailure}
}

// FailureForResponse preserves the rejection's existing request/provider
// classification without executing another compatibility retry or scheduler.
func (r *openAIReasoningRecoveryState) FailureForResponse(status int, headers http.Header, payload []byte) *UpstreamFailoverError {
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(payload)))
	if classifyOpenAIContinuationStateError(message, payload) != openAIContinuationStateErrorNone {
		return NewOpenAIContinuationStateUnavailableError(status, headers, bytes.Clone(payload))
	}
	if classifyOpenAIRequestRejection(status, message, payload) != "" {
		return NewOpenAIRequestRejectedError(status, headers)
	}
	return newOpenAIUpstreamFailoverError(status, headers, bytes.Clone(payload), message, false)
}

// StopError never erases request-level semantics in order to forbid retries.
// It also captures signature exits where no safe recovery was possible; those
// used to return only the fixed client message and lose the actual rejection.
func (r *openAIReasoningRecoveryState) StopError(err error) error {
	if err == nil || r == nil {
		return err
	}
	if r.diagnosticIncoming == nil {
		// Protocol-conversion callers have their own error contract. This repair
		// opts in only at native Responses/passthrough send boundaries.
		if !r.retryUsed {
			return err
		}
		if !r.stopRecorded {
			r.stopRecorded = true
			r.record("recovery_failed", 0, "", nil, 0)
		}
		return errors.New("reasoning signature recovery failed; no further upstream retry permitted")
	}
	var stopped *OpenAIReasoningRecoveryTerminalError
	if errors.As(err, &stopped) {
		return err
	}
	// returned is the failure the attempt itself produced, if any; failure may
	// be replaced below by one built from the observed payload.
	var returned *UpstreamFailoverError
	errors.As(err, &returned)
	var failure *UpstreamFailoverError
	var signal *openAIReasoningRecoverySignalError
	if errors.As(err, &signal) {
		r.ObserveFailure(signal.payload, false)
		failure = NewOpenAIContinuationStateUnavailableError(r.upstreamStatus(http.StatusBadRequest), r.responseHeaders, signal.payload)
	} else if errors.As(err, &failure) && len(failure.ResponseBody) > 0 && len(r.failurePayload) == 0 {
		r.ObserveFailure(failure.ResponseBody, r.semanticCommitted)
	}
	rejection, signatureRejected := parseOpenAIReasoningRejection(r.failurePayload)
	if failure == nil {
		failure = r.requestRejectionFromStream(r.failurePayload)
	}
	cacheSkipRejected := r.cacheSkippedItems > 0 && failure != nil && failure.IsOpenAIRequestRejected()
	r.accountMismatch = r.anotherAccountMayAccept(failure, signatureRejected)
	r.accountFailure = !r.accountMismatch && r.accountFailedAfterRetry(returned)
	if !r.retryUsed && !signatureRejected && !cacheSkipRejected && !r.accountMismatch {
		return err
	}
	if failure == nil {
		if signatureRejected {
			failure = NewOpenAIContinuationStateUnavailableError(r.upstreamStatus(http.StatusBadRequest), r.responseHeaders, nil)
		} else {
			failure = &UpstreamFailoverError{StatusCode: r.upstreamStatus(http.StatusBadGateway), ResponseHeaders: r.responseHeaders.Clone()}
		}
	}
	if !r.stopRecorded {
		r.stopRecorded = true
		action := "recovery_not_attempted"
		r.diagnosticState = "not_attempted"
		if r.retryUsed {
			action = "recovery_failed"
			r.diagnosticState = "budget_exhausted"
		}
		r.record(action, r.upstreamStatus(failure.StatusCode), rejection.code, r.failurePayload, 0)
	}
	if r.accountMismatch {
		return openAICiphertextAccountMismatch(failure)
	}
	if r.accountFailure {
		handOff := openAIRecoveryRetryAccountFailure(returned)
		if r.redactPayload != nil && len(handOff.ResponseBody) > 0 {
			handOff.ResponseBody = r.redactPayload(handOff.ResponseBody)
		}
		return handOff
	}
	if !r.retryUsed && !r.semanticCommitted {
		return failure
	}
	terminal := newOpenAIReasoningRecoveryTerminalError(failure)
	terminal.FailureTerminalForwarded = r.failureTerminalForwarded
	return terminal
}

// anotherAccountMayAccept decides whether this attempt ended as an account
// mismatch: the upstream said it could not decrypt or verify ciphertext in the
// request, on the first send or on the stripped one, nothing on this account
// repaired the request, and no output or failure reached the client. Any other
// rejection of the request would be the same on every account. The handler
// still confirms the client-side state before it switches.
func (r *openAIReasoningRecoveryState) anotherAccountMayAccept(failure *UpstreamFailoverError, signatureRejected bool) bool {
	if !r.switchAccounts || r.semanticCommitted || r.failureTerminalForwarded || r.ctx.Err() != nil ||
		openAIReasoningRecoveryProtectedStatus(r.responseStatus) ||
		// wire is absent when this account type has no stripped retry.
		openAIRequestHoldsServerContext(r.wire) || openAIRequestHoldsServerContext(r.diagnosticIncoming) {
		return false
	}
	if r.retryUsed && !r.retryDispatched {
		// The stripped retry was prepared but never sent. That is a fault in
		// how this request was built, not evidence about the account.
		return false
	}
	switch {
	case failure == nil:
		return signatureRejected
	case !failure.IsOpenAIContinuationStateUnavailable() && !failure.IsOpenAIRequestRejected():
		return false
	default:
		return signatureRejected || openAIUpstreamReportsUndecryptableCiphertext(r.failurePayload)
	}
}

// accountFailedAfterRetry reports that the stripped retry was sent and the
// attempt then ended, before anything reached the client, with a failure of
// the account itself: one the attempt returned as a failover to the next
// account, which is how the same answer is treated on a first send. A request
// rejection, continuation state and a refusal are not the account's, and an
// error the attempt did not classify as a failover ends the request as before.
func (r *openAIReasoningRecoveryState) accountFailedAfterRetry(returned *UpstreamFailoverError) bool {
	if returned == nil || !r.switchAccounts || !r.retryUsed || !r.retryDispatched ||
		r.semanticCommitted || r.failureTerminalForwarded || r.ctx.Err() != nil {
		return false
	}
	return returned.ShouldRetryNextAccount() && !returned.IsOpenAIRequestRejected() &&
		!returned.IsOpenAIContinuationStateUnavailable() && !returned.IsOpenAIRefusalRecovery()
}

// ResetOpenAIReasoningRecoveryAttempt detaches the state of a rejected attempt
// before the request moves to another account, so that a forwarder which
// creates no state of its own does not read this one. The response header that
// names the last recovery action is left as it is.
func ResetOpenAIReasoningRecoveryAttempt(c *gin.Context) {
	if c != nil {
		c.Set(openAIReasoningRecoveryContextKey, (*openAIReasoningRecoveryState)(nil))
	}
}

func (r *openAIReasoningRecoveryState) upstreamStatus(fallback int) int {
	if r != nil && r.responseStatus > 0 {
		return r.responseStatus
	}
	return fallback
}

func (r *openAIReasoningRecoveryState) requestRejectionFromStream(payload []byte) *UpstreamFailoverError {
	if r == nil || r.diagnosticIncoming == nil || (!r.retryUsed && r.cacheSkippedItems == 0) || !gjson.ValidBytes(payload) || openAIHTTPResponseTerminalError(payload) == nil {
		return nil
	}
	message := extractOpenAISSEErrorMessage(payload)
	semanticStatus := openAIStreamFailedEventSemanticStatus(payload, message)
	if classifyOpenAIRequestRejection(semanticStatus, message, payload) == "" {
		return nil
	}
	return NewOpenAIRequestRejectedError(r.upstreamStatus(semanticStatus), r.responseHeaders)
}

func (r *openAIReasoningRecoveryState) continuationDiagnosticRecovery(payload []byte) *openAIContinuationRecoveryShape {
	shape := &openAIContinuationRecoveryShape{
		CacheSkippedItems: r.cacheSkippedItems,
		ItemIDsRemoved:    r.itemIDsRemoved,
		RetryAttempted:    r.retryDispatched,
		Disposition:       r.diagnosticState,
		AccountMismatch:   r.accountMismatch,
		AccountFailure:    r.accountFailure,
	}
	if shape.Disposition == "" {
		shape.Disposition = "not_attempted"
	}
	if shape.Disposition == "not_attempted" {
		shape.NotAttemptedReason = r.recoveryNotAttemptedReason(payload)
	} else if r.retryUsed && !r.retryDispatched && r.stopRecorded {
		shape.NotAttemptedReason = r.diagnosticStopReason
		if shape.NotAttemptedReason == "" {
			shape.NotAttemptedReason = "recovery_not_dispatched"
		}
	}
	return shape
}

func (r *openAIReasoningRecoveryState) recoveryNotAttemptedReason(payload []byte) string {
	switch {
	case !r.enabled:
		return "disabled"
	case r.semanticCommitted:
		return "semantic_output_committed"
	case r.ctx.Err() != nil:
		return "request_cancelled"
	case openAIReasoningRecoveryProtectedStatus(r.responseStatus):
		return "protected_status"
	}
	rejection, ok := parseOpenAIReasoningRejection(payload)
	if !ok {
		return "not_signature_rejection"
	}
	indices, reason := selectOpenAIRecoveryIndices(r.wire, rejection)
	if len(indices) > 0 {
		if _, err := stripOpenAIReasoningCipherIndices(r.wire, indices); err != nil {
			return "rewrite_failed"
		}
	}
	return reason
}

func (r *openAIReasoningRecoveryState) diagnosticClassification(payload []byte) string {
	message := extractOpenAISSEErrorMessage(payload)
	if kind := classifyOpenAIContinuationStateError(message, payload); kind != openAIContinuationStateErrorNone {
		return string(kind)
	}
	status := r.responseStatus
	if status < 400 {
		status = openAIStreamFailedEventSemanticStatus(payload, message)
	}
	return classifyOpenAIRequestRejection(status, message, payload)
}

func (r *openAIReasoningRecoveryState) record(action string, status int, code string, payload []byte, count int) {
	if r.c == nil {
		return
	}
	if !r.c.Writer.Written() {
		r.c.Header(openAIReasoningRecoveryHeader, action)
	}
	detail := map[string]any{"action": action, "items": count, "usage_status": "unavailable"}
	usage := openAIReasoningUsageEvidence(payload)
	recordedPayload := payload
	if r.redactPayload != nil && len(payload) > 0 {
		recordedPayload = r.redactPayload(payload)
	}
	upstreamMessage := ""
	if len(payload) > 0 {
		detail["payload_sha256"] = openAIReasoningDigest(payload)
		upstreamMessage = extractOpenAISSEErrorMessage(recordedPayload)
	}
	if len(usage) == 0 && action == "retry_without_encrypted_content" {
		usage = r.attemptUsage
		if len(usage) > 0 {
			detail["usage_source"] = "earlier_stream_event"
		}
	}
	if len(usage) > 0 {
		detail["usage_status"] = "available"
		detail["usage"] = usage
	}
	encoded := encodeOpenAIReasoningRecoveryDetail(detail, upstreamMessage, recordedPayload)
	logger.FromContext(r.ctx).Info("openai.reasoning_recovery",
		zap.Int64("account_id", r.account.ID), zap.String("action", action),
		zap.String("code", code), zap.Int("upstream_status", status),
		zap.String("evidence", string(encoded)))
	appendOpsUpstreamError(r.c, OpsUpstreamErrorEvent{
		Platform: r.account.Platform, AccountID: r.account.ID,
		ProxyID: opsUpstreamProxyID(r.account), ProxyName: opsUpstreamProxyName(r.account),
		Kind: "reasoning_recovery", Stage: "inference", Scope: "request", Reason: code,
		UpstreamStatusCode: status, Message: action, Detail: string(encoded),
		UpstreamRequestID:      r.responseHeaders.Get("x-request-id"),
		ContinuationDiagnostic: r.diagnosticForRecordedAttempt(action, recordedPayload),
	})
}

// encodeOpenAIReasoningRecoveryDetail adds the upstream message and the rejected
// payload to the essential detail, halving their bounds until the encoded
// detail fits openAIReasoningRecoveryDetailEncodedLimit.
func encodeOpenAIReasoningRecoveryDetail(detail map[string]any, upstreamMessage string, payload []byte) []byte {
	essential, _ := json.Marshal(detail)
	if upstreamMessage == "" && len(payload) == 0 {
		return essential
	}
	messageLimit, payloadLimit := openAIReasoningRecoveryMessageDetailLimit, openAIReasoningRecoveryPayloadDetailLimit
	for messageLimit > 0 || payloadLimit > 0 {
		candidate := make(map[string]any, len(detail)+5)
		for key, value := range detail {
			candidate[key] = value
		}
		if upstreamMessage != "" && messageLimit > 0 {
			text, truncated := continuationDiagnosticBoundValue(upstreamMessage, messageLimit)
			candidate["upstream_message"] = text
			if truncated {
				candidate["upstream_message_truncated"] = true
			}
		}
		if len(payload) > 0 {
			if payloadLimit > 0 {
				text, truncated := continuationDiagnosticBoundValue(string(payload), payloadLimit)
				candidate["payload"] = text
				if truncated {
					candidate["payload_truncated"] = true
				}
			} else {
				candidate["payload_truncated"] = true
			}
			candidate["payload_bytes"] = len(payload)
		}
		if encoded, err := json.Marshal(candidate); err == nil && len(encoded) <= openAIReasoningRecoveryDetailEncodedLimit {
			return encoded
		}
		messageLimit, payloadLimit = messageLimit/2, payloadLimit/2
		if messageLimit < 64 {
			messageLimit = 0
		}
		if payloadLimit < 64 {
			payloadLimit = 0
		}
	}
	return essential
}

func (r *openAIReasoningRecoveryState) diagnosticForRecordedAttempt(action string, payload []byte) *OpenAIContinuationDiagnostic {
	if r.diagnosticIncoming == nil || action == "rejected_history_skipped" {
		// Called before PrepareRequest freezes the stripped wire. A later failure
		// will report the final snapshot and skipped-item count together.
		return nil
	}
	return buildOpenAIContinuationDiagnostic(r.c, r.diagnosticIncoming, r.diagnosticRequest, r.wire, payload, r.diagnosticClassification(payload))
}

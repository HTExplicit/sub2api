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
	"net/http"
	"regexp"
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

// openAIReasoningRecoveryState owns the single, explicitly lossy retry for an
// HTTP Responses request. It never re-enters a protocol converter or scheduler.
// original is immutable; wire is the exact payload of the latest sent attempt.
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
	onRejected    func([]string)
	retryCleanups []func()
}

func (s *OpenAIGatewayService) newOpenAIReasoningRecoveryState(ctx context.Context, c *gin.Context, account *Account, token string) *openAIReasoningRecoveryState {
	if ctx == nil {
		ctx = context.Background()
	}
	r := &openAIReasoningRecoveryState{
		ctx: ctx, c: c, account: account, token: token,
		enabled: account.IsOpenAIReasoningSignatureRecoveryEnabled() && !isOpenAICompatMessagesBridgeContext(c),
		store:   s.openAIReasoningStateStore(), budget: openAIReasoningCacheBudgetForRequest(c),
	}
	if c != nil {
		c.Set(openAIReasoningRecoveryContextKey, r)
	}
	return r
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

func (r *openAIReasoningRecoveryState) SetRejectedCallback(fn func([]string)) {
	if r != nil {
		r.onRejected = fn
	}
}

func (r *openAIReasoningRecoveryState) RecoveryAttempt() bool { return r != nil && r.retryUsed }

// buildOpenAIReasoningScope is shared by positive replay and rejection memory.
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
	identity := openAIReasoningRequestIdentity(account, req, wireBody, proxyURL, "")
	return BuildOpenAIReasoningCacheScope(OpenAIReasoningScopeInput{
		UserID: key.UserID, APIKeyID: key.ID, GroupID: groupID, AccountID: account.ID,
		SourceIdentityHash: identity, Endpoint: req.URL.String(),
		Model:     gjson.GetBytes(wireBody, "model").String(),
		Reasoning: json.RawMessage(gjson.GetBytes(wireBody, "reasoning").Raw),
	})
}

func openAIReasoningRequestIdentity(account *Account, req *http.Request, _ []byte, proxyURL, token string) string {
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
// transformations and any positive replay. Recovery compares the entire final
// body and source, so a later builder cannot silently remap or reinject state.
func (r *openAIReasoningRecoveryState) PrepareRequest(req *http.Request, body []byte, proxyURL string) (*http.Request, []byte, error) {
	if r == nil || !r.enabled {
		return req, body, nil
	}
	if req == nil || req.URL == nil || req.Method != http.MethodPost ||
		(!strings.HasSuffix(req.URL.Path, "/responses") && !strings.HasSuffix(req.URL.Path, "/responses/compact")) {
		if r.retryUsed {
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
	identity := openAIReasoningRequestIdentity(r.account, req, body, proxyURL, r.token)
	if identity == "" {
		return nil, nil, errors.New("reasoning recovery source identity unavailable")
	}
	if r.retryUsed {
		if r.ctx.Err() != nil {
			return nil, nil, r.StopError(r.ctx.Err())
		}
		if identity != r.identity || !bytes.Equal(body, r.retryBody) {
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
	var rejected map[string]OpenAIRejectedReasoning
	err := r.budget.Do(r.ctx, func(ioCtx context.Context) error {
		var readErr error
		rejected, readErr = r.store.GetOpenAIRejectedReasoning(ioCtx, r.scope, hashes)
		return readErr
	})
	if err != nil {
		return body
	}
	now := time.Now()
	indices := make([]int, 0, len(items))
	for _, item := range items {
		if old, ok := rejected[item.hash]; ok && !old.RejectedAt.IsZero() &&
			!old.RejectedAt.After(now) && now.Before(old.ExpiresAt) && now.Before(old.RejectedAt.Add(OpenAIReasoningStateTTL)) {
			indices = append(indices, item.index)
		}
	}
	if len(indices) == 0 {
		return body
	}
	stripped, err := stripOpenAIReasoningCipherIndices(body, indices)
	if err != nil {
		return body
	}
	r.record("rejected_history_skipped", 0, "", nil, len(indices))
	return stripped
}

type openAIReasoningCipherItem struct {
	index int
	hash  string
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
			out = append(out, openAIReasoningCipherItem{index, openAIReasoningDigest([]byte(cipher.String()))})
		}
	}
	return out
}

type openAIReasoningRejection struct{ code, param string }

func parseOpenAIReasoningRejection(payload []byte) (openAIReasoningRejection, bool) {
	if _, err := canonicalReasoningCacheJSON(payload); err != nil {
		return openAIReasoningRejection{}, false
	}
	root := gjson.ParseBytes(payload)
	for _, path := range []string{"status_code", "error.status_code", "error.status", "response.error.status_code", "response.error.status"} {
		if value := root.Get(path); value.Type == gjson.Number || value.Type == gjson.String {
			if openAIReasoningRecoveryProtectedStatus(int(value.Int())) {
				return openAIReasoningRejection{}, false
			}
		}
	}
	if kind := root.Get("type").String(); kind != "" && kind != "error" && kind != "response.failed" && kind != "response.done" {
		return openAIReasoningRejection{}, false
	}
	if root.Get("type").String() == "response.done" && root.Get("response.status").String() != "failed" {
		return openAIReasoningRejection{}, false
	}
	if status := root.Get("status").String(); status != "" && status != "failed" {
		return openAIReasoningRejection{}, false
	}
	var source gjson.Result
	switch {
	case root.Get("response.error").IsObject():
		if root.Get("type").String() != "response.failed" && root.Get("response.status").String() != "failed" {
			return openAIReasoningRejection{}, false
		}
		source = root.Get("response.error")
	case root.Get("error").IsObject():
		source = root.Get("error")
	case root.Get("type").String() == "error":
		source = root
	default:
		return openAIReasoningRejection{}, false
	}
	code := source.Get("code")
	if code.Type != gjson.String || (code.String() != "thinking_signature_invalid" && code.String() != "invalid_encrypted_content") {
		return openAIReasoningRejection{}, false
	}
	param := source.Get("param")
	if param.Exists() && param.Type != gjson.String && param.Type != gjson.Null {
		return openAIReasoningRejection{}, false
	}
	return openAIReasoningRejection{code.String(), param.String()}, true
}

var openAIReasoningErrorInputParam = regexp.MustCompile(`^input(?:\[(\d+)\]|\.(\d+))(?:\.encrypted_content)?$`)

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
	if _, err := canonicalReasoningCacheJSON(body); err != nil {
		return nil, nil
	}
	// Removing ciphertext cannot repair a locally provable orphan tool result.
	if !openAIReasoningToolHistoryAllowsRecovery(body) {
		return nil, nil
	}
	items := openAIReasoningCipherItems(body)
	if len(items) == 0 {
		return nil, nil
	}
	if match := openAIReasoningErrorInputParam.FindStringSubmatch(rejection.param); match != nil {
		indexText := match[1]
		if indexText == "" {
			indexText = match[2]
		}
		index, err := strconv.Atoi(indexText)
		if err != nil {
			return nil, nil
		}
		for _, item := range items {
			if item.index == index {
				return []int{index}, []string{item.hash}
			}
		}
		return nil, nil
	}
	if rejection.param != "" && rejection.param != "input" && rejection.param != "reasoning.encrypted_content" {
		return nil, nil
	}
	// Without an index, recovery concerns the rejected request's old reasoning
	// candidate set, not a claim that each member is individually invalid. Other
	// encrypted carriers or server-held references make that scope ambiguous.
	if gjson.GetBytes(body, "previous_response_id").String() != "" ||
		(gjson.GetBytes(body, "conversation").Exists() && gjson.GetBytes(body, "conversation").Type != gjson.Null) {
		return nil, nil
	}
	var parsed any
	if json.Unmarshal(body, &parsed) != nil || countOpenAIEncryptedFields(parsed) != len(items) {
		return nil, nil
	}
	indices := make([]int, 0, len(items))
	hashes := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		indices = append(indices, item.index)
		if !seen[item.hash] {
			seen[item.hash] = true
			hashes = append(hashes, item.hash)
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
	out := bytes.Clone(body)
	for _, index := range indices {
		path := fmt.Sprintf("input.%d", index)
		if gjson.GetBytes(out, path+".type").String() != "reasoning" {
			return nil, errors.New("reasoning recovery target mismatch")
		}
		var err error
		out, err = sjson.DeleteBytes(out, path+".encrypted_content")
		if err != nil {
			return nil, err
		}
	}
	return out, nil
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
	if !errors.As(err, &signal) {
		return nil, false
	}
	return r.TryRecover(http.StatusBadRequest, nil, signal.payload, false)
}

func (r *openAIReasoningRecoveryState) TryRecover(status int, _ http.Header, payload []byte, semanticCommitted bool) ([]byte, bool) {
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
	stripped, err := stripOpenAIReasoningCipherIndices(r.wire, indices)
	if err != nil {
		return nil, false
	}
	r.retryUsed = true
	r.retryBody = bytes.Clone(stripped)
	if r.onRejected != nil {
		r.onRejected(append([]string(nil), hashes...))
	}
	if r.store != nil && r.scope.ScopeHash != "" {
		_ = r.budget.Do(r.ctx, func(ioCtx context.Context) error {
			return r.store.PutOpenAIRejectedReasoning(ioCtx, r.scope, hashes)
		})
	}
	r.record("retry_without_encrypted_content", status, rejection.code, payload, len(indices))
	return stripped, true
}

func openAIReasoningRecoveryProtectedStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusProxyAuthRequired, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

// StopError intentionally does not unwrap. In particular an UpstreamFailoverError
// from the one recovery attempt must never cause a third POST or account switch.
func (r *openAIReasoningRecoveryState) StopError(err error) error {
	if err == nil || r == nil || !r.retryUsed {
		return err
	}
	if !r.stopRecorded {
		r.stopRecorded = true
		r.record("recovery_failed", 0, "", nil, 0)
	}
	return errors.New("reasoning signature recovery failed; no further upstream retry permitted")
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
	if len(payload) > 0 {
		detail["payload_sha256"] = openAIReasoningDigest(payload)
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
	encoded, _ := json.Marshal(detail)
	logger.FromContext(r.ctx).Info("openai.reasoning_recovery",
		zap.Int64("account_id", r.account.ID), zap.String("action", action),
		zap.String("code", code), zap.Int("upstream_status", status),
		zap.String("evidence", string(encoded)))
	appendOpsUpstreamError(r.c, OpsUpstreamErrorEvent{
		Platform: r.account.Platform, AccountID: r.account.ID,
		ProxyID: opsUpstreamProxyID(r.account), ProxyName: opsUpstreamProxyName(r.account),
		Kind: "reasoning_recovery", Stage: "inference", Scope: "request", Reason: code,
		UpstreamStatusCode: status, Message: action, Detail: string(encoded),
	})
}

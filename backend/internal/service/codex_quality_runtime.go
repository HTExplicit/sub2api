package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type codexQualityRuntime struct {
	s            *OpenAIGatewayService
	store        NativeCodexStateStore
	installation *NativeCodexMetadata
	host         *nativeCodexHost
}

type codexQualityExecutionKey struct{}
type codexQualityHeadersKey struct{}
type codexQualityHeaders struct {
	grant, trial string
	present      bool
}
type codexQualityExecution struct {
	runtime                                         *codexQualityRuntime
	runID, grantDigest, trialID, operationID, stage string
	accountID                                       int64
	requestModel, effort                            string
	qualification                                   *extensionv1.CodexRoutingQualification
	keyLookup                                       CodexQualityKeyLookup
}

type CodexQualityKeyLookup func(context.Context, int64) (*APIKey, error)

func codexQualityKeyUsable(key *APIKey, actor, id, group int64) bool {
	return key != nil && key.ID == id && key.UserID == actor && key.GroupID != nil && *key.GroupID == group && key.IsActive() && !key.IsExpired() && !key.IsQuotaExhausted()
}

func codexQualityExecutionFromContext(ctx context.Context) *codexQualityExecution {
	if ctx == nil {
		return nil
	}
	e, _ := ctx.Value(codexQualityExecutionKey{}).(*codexQualityExecution)
	return e
}

func IsCodexQualityRequest(ctx context.Context) bool {
	return codexQualityExecutionFromContext(ctx) != nil
}

// Remove the capability before any request copying, forwarding or diagnostics.
func StageCodexQualityHeaders(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	h := codexQualityHeaders{}
	grantCount, trialCount := 0, 0
	for key, values := range c.Request.Header {
		if strings.EqualFold(key, CodexQualityGrantHeader) || strings.EqualFold(key, CodexQualityTrialHeader) {
			h.present = true
			value := ""
			if len(values) == 1 {
				value = values[0]
			}
			if strings.EqualFold(key, CodexQualityGrantHeader) {
				h.grant = value
				grantCount += len(values)
			} else {
				h.trial = value
				trialCount += len(values)
			}
			delete(c.Request.Header, key)
		}
	}
	if grantCount != 1 || trialCount != 1 {
		h.grant, h.trial = "", ""
	}
	if h.present {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), codexQualityHeadersKey{}, h))
	}
}

func (s *OpenAIGatewayService) codexQualityRuntime() (*codexQualityRuntime, error) {
	if s == nil || s.nativeCodexRuntime == nil || s.accountRepo == nil {
		return nil, codexQualityUnavailable("native Codex runtime is not configured")
	}
	runtime := s.nativeCodexRuntime
	metadata, err := runtime.repo.LoadNativeCodexMetadata(context.Background())
	if err != nil {
		return nil, codexQualityUnavailable("load Codex runtime metadata: %v", err)
	}
	snapshot := runtime.current()
	var host *nativeCodexHost
	if snapshot != nil {
		host = snapshot.host
	}
	return &codexQualityRuntime{s: s, store: runtime.repo, installation: metadata, host: host}, nil
}

func (rt *codexQualityRuntime) ctx(ctx context.Context) context.Context {
	return WithNativeCodexExecution(ctx, rt.installation)
}

func (rt *codexQualityRuntime) scope(ctx context.Context, id int64) (extensionv1.CodexRoutingScope, error) {
	if rt.host == nil {
		return extensionv1.CodexRoutingScope{}, codexQualityUnavailable("Codex runtime host is not loaded")
	}
	raw, _ := json.Marshal(extensionv1.CodexRoutingQuery{AccountID: id, Transport: "http"})
	result, err := rt.host.Call(rt.ctx(ctx), extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingScope, Payload: raw})
	var scope extensionv1.CodexRoutingScope
	switch {
	case err != nil:
		return scope, codexQualityUnavailable("routing scope of account %d: %v", id, err)
	case result.Code != "":
		return scope, codexQualityUnavailable("routing scope of account %d returned %s: %s", id, result.Code, result.Message)
	}
	if err := json.Unmarshal(result.Payload, &scope); err != nil {
		return scope, codexQualityUnavailable("decode routing scope of account %d: %v", id, err)
	}
	if scope.AccountID != id {
		return scope, codexQualityUnavailable("routing scope names account %d instead of %d", scope.AccountID, id)
	}
	return scope, nil
}

func qualityProxyID(a *Account) int64 {
	if a.ProxyID != nil {
		return *a.ProxyID
	}
	return 0
}

func (rt *codexQualityRuntime) account(ctx context.Context, run codexQualityRun) (*Account, error) {
	a, err := rt.s.accountRepo.GetByID(ctx, run.AccountID)
	// A diagnostic grant never admits an account enabled for ordinary traffic.
	// Recheck the authoritative row here, including immediately before each send.
	switch {
	case err != nil:
		return nil, codexQualityUnavailable("read account %d: %v", run.AccountID, err)
	case a == nil || a.ID != run.AccountID:
		return nil, codexQualityUnavailable("account %d not found", run.AccountID)
	case a.Schedulable:
		return nil, codexQualityUnavailable("account %d is schedulable; stop its scheduling manually before a quality diagnosis", a.ID)
	case a.Platform != PlatformOpenAI || a.Type != AccountTypeOAuth || a.IsShadow():
		return nil, codexQualityUnavailable("account %d (%s/%s, shadow=%t) is not an OpenAI OAuth account", a.ID, a.Platform, a.Type, a.IsShadow())
	case !slices.Contains(a.GroupIDs, run.GroupID):
		return nil, codexQualityUnavailable("account %d is not in group %d of the API key (account groups %v)", a.ID, run.GroupID, a.GroupIDs)
	case qualityProxyID(a) != run.ProxyID:
		return nil, codexQualityUnavailable("account %d proxy is %d, the run was created with proxy %d", a.ID, qualityProxyID(a), run.ProxyID)
	case CodexTicketAccountIdentity(a) != run.Scope.Identity:
		return nil, codexQualityUnavailable("account %d credential owner changed since the run was created", a.ID)
	}
	plan := strings.ToLower(strings.TrimSpace(a.GetCredential("plan_type")))
	if plan != "pro" && plan != "chatgpt_pro" {
		return nil, codexQualityUnavailable("account %d plan_type is %q; quality diagnosis requires pro", a.ID, plan)
	}
	if rt.s.checkChannelPricingRestriction(ctx, &run.GroupID, codexQualityModel) {
		return nil, codexQualityUnavailable("channel pricing of group %d restricts %s", run.GroupID, codexQualityModel)
	}
	if rt.s.needsUpstreamChannelRestrictionCheck(ctx, &run.GroupID) && rt.s.isUpstreamModelRestrictedByChannel(ctx, run.GroupID, a, codexQualityModel, false) {
		return nil, codexQualityUnavailable("the upstream channel of group %d restricts %s for account %d", run.GroupID, codexQualityModel, a.ID)
	}
	// This private stack copy changes only the manual switch for eligibility.
	// It is never returned, cached, projected, or persisted.
	check := *a
	check.Schedulable = true
	if eligible, reason := openAICompatibleAccountEligibilityBeforeProfit(ctx, &check, PlatformOpenAI, codexQualityModel, false, OpenAIEndpointCapabilityResponses); !eligible {
		return nil, codexQualityUnavailable("account %d is not eligible for %s: %s", a.ID, codexQualityModel, reason)
	}
	if reason := rt.s.codexQualityHealthBlock(ctx, &check); reason != "" {
		return nil, codexQualityUnavailable("account %d: %s", a.ID, reason)
	}
	if rt.s.openAIGroupRequiresPrivacySet(ctx, &run.GroupID) && !a.IsPrivacySet() {
		return nil, codexQualityUnavailable("group %d requires the privacy setting, which account %d has not set", run.GroupID, a.ID)
	}
	scope, err := rt.scope(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if !run.Scope.SameOwner(scope) {
		return nil, codexQualityUnavailable("account %d %s", a.ID, codexRoutingScopeChange(run.Scope, scope))
	}
	return a, nil
}

func (s *OpenAIGatewayService) CreateCodexQualityRun(ctx context.Context, actor, accountID int64, key *APIKey, request CodexQualityCreateRequest) (*CodexQualityRunView, error) {
	id, validID := canonicalCodexQualityID(request.RunID)
	switch {
	case !validID:
		return nil, codexQualityUnavailable("run_id %q is not a canonical UUID", request.RunID)
	case !validCodexQualityHash(request.PromptSHA256):
		return nil, codexQualityUnavailable("prompt_sha256 must be 64 lowercase hex characters")
	case actor <= 0:
		return nil, codexQualityUnavailable("no authenticated administrator")
	case key == nil:
		return nil, codexQualityUnavailable("API key %d is not one of this administrator's keys", request.APIKeyID)
	case key.GroupID == nil:
		return nil, codexQualityUnavailable("API key %d has no group", key.ID)
	case !codexQualityKeyUsable(key, actor, request.APIKeyID, *key.GroupID):
		return nil, codexQualityUnavailable("API key %d is not usable (status %q, expired %t, quota exhausted %t)", key.ID, key.Status, key.IsExpired(), key.IsQuotaExhausted())
	}
	request.RunID = id
	if request.MaxSends == 0 {
		request.MaxSends = codexQualityMaxSends
	}
	if request.TTLSeconds == 0 {
		request.TTLSeconds = 7200
	}
	if request.MaxSends < 1 || request.MaxSends > codexQualityMaxSends {
		return nil, codexQualityUnavailable("max_sends must be 1 to %d, got %d", codexQualityMaxSends, request.MaxSends)
	}
	if request.TTLSeconds < 60 || request.TTLSeconds > 7200 {
		return nil, codexQualityUnavailable("ttl_seconds must be 60 to 7200, got %d", request.TTLSeconds)
	}
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return nil, err
	}
	ctx = rt.ctx(ctx)
	a, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, codexQualityUnavailable("read account %d: %v", accountID, err)
	}
	if a == nil {
		return nil, codexQualityUnavailable("account %d not found", accountID)
	}
	scope, err := rt.scope(ctx, accountID)
	if err != nil {
		return nil, err
	}
	wanted := codexQualityRun{RunID: request.RunID, ActorID: actor, APIKeyID: key.ID, AccountID: accountID, GroupID: *key.GroupID, ProxyID: qualityProxyID(a), Scope: scope, PromptSHA256: request.PromptSHA256, MaxSends: request.MaxSends, Status: "open", Attempts: []CodexQualityAttempt{}}
	if _, err = rt.account(ctx, wanted); err != nil {
		return nil, err
	}
	grant, digest, err := newCodexQualityGrant(request.RunID)
	if err != nil {
		return nil, err
	}
	// Reissuing a grant invalidates the old capability; spent entries survive.
	run, err := issueCodexQualityGrant(ctx, rt.store, wanted, digest, time.Now().UTC().Add(time.Duration(request.TTLSeconds)*time.Second))
	if err != nil {
		return nil, err
	}
	view := codexQualityView(run, rt.installation.RuntimeGeneration)
	view.Grant = grant
	return view, nil
}

// ReadCodexQualityRun is readable by every administrator, not only the one
// who created the run (the view names the creator in actor_id).
func (s *OpenAIGatewayService) ReadCodexQualityRun(ctx context.Context, actor, accountID int64, id string) (*CodexQualityRunView, error) {
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return nil, err
	}
	run, _, err := readCodexQualityRun(rt.ctx(ctx), rt.store, id)
	switch {
	case err != nil:
		return nil, err
	case run.RunID == "":
		return nil, codexQualityUnavailable("quality run %s not found", id)
	case run.AccountID != accountID:
		return nil, codexQualityUnavailable("quality run %s belongs to account %d, not %d", run.RunID, run.AccountID, accountID)
	}
	view := codexQualityView(run, rt.installation.RuntimeGeneration)
	if view.RouteReady {
		if _, err = rt.account(ctx, run); err != nil {
			view.RouteReady, view.RouteError = false, err.Error()
		} else if _, err = rt.qualification(ctx, run); err != nil {
			view.RouteReady, view.RouteError = false, err.Error()
		}
	}
	return view, nil
}

func (s *OpenAIGatewayService) CloseCodexQualityRun(ctx context.Context, actor, accountID int64, id string) (*CodexQualityRunView, error) {
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return nil, err
	}
	run, err := mutateCodexQualityRun(rt.ctx(ctx), rt.store, id, func(run *codexQualityRun) error {
		switch {
		case run.RunID == "":
			return codexQualityUnavailable("quality run %s not found", id)
		case run.ActorID != actor:
			return codexQualityUnavailable("quality run %s was created by administrator %d; only its creator can close it", run.RunID, run.ActorID)
		case run.AccountID != accountID:
			return codexQualityUnavailable("quality run %s belongs to account %d, not %d", run.RunID, run.AccountID, accountID)
		}
		run.Status, run.GrantDigest = "closed", ""
		return nil
	})
	if err != nil {
		return nil, err
	}
	if run.Qualification != nil {
		s.closeCodexQualityConnection(run.Qualification)
	}
	rt.clearClosedMaterial(ctx, run)
	return codexQualityView(run, rt.installation.RuntimeGeneration), nil
}

func (s *OpenAIGatewayService) AdmitCodexQualityRequest(c *gin.Context, key *APIKey, body []byte, lookups ...CodexQualityKeyLookup) error {
	h, exists := c.Request.Context().Value(codexQualityHeadersKey{}).(codexQualityHeaders)
	if !exists {
		return nil
	}
	if len(lookups) != 1 || lookups[0] == nil {
		return codexQualityUnavailable("no API key lookup for the quality grant")
	}
	id, secret, ok := strings.Cut(h.grant, ".")
	id, validID := canonicalCodexQualityID(id)
	trial, validTrial := canonicalCodexQualityID(h.trial)
	switch {
	case !ok || len(secret) != 43 || !validID:
		return codexQualityUnavailable("the %s header is not a valid grant (exactly one header is required)", CodexQualityGrantHeader)
	case !validTrial:
		return codexQualityUnavailable("the %s header must be exactly one canonical UUID", CodexQualityTrialHeader)
	case c.Request.Method != http.MethodPost || c.Request.URL.Path != "/v1/responses":
		return codexQualityUnavailable("quality grants apply only to POST /v1/responses, not %s %s", c.Request.Method, c.Request.URL.Path)
	case key == nil || key.GroupID == nil:
		return codexQualityUnavailable("the API key has no group")
	}
	h.trial = trial
	var fields map[string]json.RawMessage
	if !validCodexQualityJSON(body) {
		return codexQualityUnavailable("the request body must be at most 1 MiB of JSON without duplicate keys")
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return codexQualityUnavailable("decode request body: %v", err)
	}
	for name := range fields {
		if !slices.Contains([]string{"model", "input", "reasoning", "stream", "store", "prompt_cache_key", "client_metadata"}, name) {
			return codexQualityUnavailable("request field %q is not allowed in a quality diagnosis", name)
		}
	}
	input := gjson.GetBytes(body, "input")
	if input.Type != gjson.String || gjson.GetBytes(body, "model").String() != codexQualityModel || gjson.GetBytes(body, "reasoning.effort").String() != codexQualityEffort || !gjson.GetBytes(body, "stream").Bool() || gjson.GetBytes(body, "store").Bool() {
		return codexQualityUnavailable("a quality diagnosis needs a string input, model %s, reasoning.effort %s, stream true and store false", codexQualityModel, codexQualityEffort)
	}
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return err
	}
	run, _, err := readCodexQualityRun(rt.ctx(c.Request.Context()), rt.store, id)
	digest := codexQualityHash(h.grant)
	switch {
	case err != nil:
		return err
	case !codexQualityActive(run, time.Now()):
		return codexQualityUnavailable("%s", codexQualityInactiveReason(run, time.Now()))
	case run.APIKeyID != key.ID || run.ActorID != key.UserID || run.GroupID != *key.GroupID:
		return codexQualityUnavailable("quality run %s belongs to API key %d of user %d in group %d", run.RunID, run.APIKeyID, run.ActorID, run.GroupID)
	case run.PromptSHA256 != codexQualityHash(input.String()):
		return codexQualityUnavailable("the input does not match the prompt_sha256 of quality run %s", run.RunID)
	case !codexQualityGrantMatches(run, digest):
		return codexQualityUnavailable("the grant does not match quality run %s (it was reissued)", run.RunID)
	}
	for _, old := range run.Attempts {
		if old.Stage == "business" && old.TrialID == h.trial {
			return fmt.Errorf("%w: trial %s was already sent", ErrCodexQualitySpent, h.trial)
		}
	}
	ctx := ensureOpenAIRuntimeBreakerProbeOwner(c.Request.Context())
	if _, err = rt.account(ctx, run); err != nil {
		return err
	}
	q, err := rt.qualification(ctx, run)
	if err != nil {
		return err
	}
	e := &codexQualityExecution{runtime: rt, runID: id, grantDigest: digest, trialID: h.trial, stage: "business", accountID: run.AccountID, qualification: q, keyLookup: lookups[0]}
	c.Request = c.Request.WithContext(context.WithValue(ctx, codexQualityExecutionKey{}, e))
	return nil
}

// Reject duplicate object members: gjson and an upstream JSON parser must not
// disagree about the prompt or effort authorized by the capability.
func validCodexQualityJSON(body []byte) bool {
	if len(body) > 1<<20 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 16 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
		case '[':
			for decoder.More() {
				if !value(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		end, err := decoder.Token()
		return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !value(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func (rt *codexQualityRuntime) qualification(ctx context.Context, run codexQualityRun) (*extensionv1.CodexRoutingQualification, error) {
	q := run.Qualification
	if reason := codexQualityRouteReason(run, rt.installation.RuntimeGeneration, time.Now()); reason != "" {
		return nil, codexQualityUnavailable("%s", reason)
	}
	bundle, err := readCodexRoutingBundle(rt.ctx(ctx), rt.store, codexRuntimePluginKey, q.Bundle, q.Scope, true)
	switch {
	case err != nil:
		return nil, codexQualityUnavailable("route bundle: %v", err)
	case bundle.Model != codexQualityModel:
		return nil, codexQualityUnavailable("route bundle model is %q, not %s", bundle.Model, codexQualityModel)
	case bundle.Scope.ConnectionLeaseID != q.Scope.ConnectionLeaseID || bundle.Scope.Transport != q.Scope.Transport:
		return nil, codexQualityUnavailable("route bundle is bound to %s connection %q, the run to %s connection %q", bundle.Scope.Transport, bundle.Scope.ConnectionLeaseID, q.Scope.Transport, q.Scope.ConnectionLeaseID)
	case q.ExpiresAt.After(bundle.ExpiresAt):
		return nil, codexQualityUnavailable("route expires at %s, after its bundle (%s)", q.ExpiresAt.UTC().Format(time.RFC3339), bundle.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if err := rt.s.CheckCodexRoutingLease(ctx, q.Scope, q.ExpiresAt); err != nil {
		return nil, codexQualityUnavailable("connection lease %q: %v", q.Scope.ConnectionLeaseID, err)
	}
	return q, nil
}

func (e *codexQualityExecution) current(ctx context.Context) (codexQualityRun, error) {
	run, _, err := readCodexQualityRun(e.runtime.ctx(ctx), e.runtime.store, e.runID)
	switch {
	case err != nil:
		return run, err
	case !codexQualityActive(run, time.Now()):
		return run, codexQualityUnavailable("%s", codexQualityInactiveReason(run, time.Now()))
	case run.AccountID != e.accountID:
		return run, codexQualityUnavailable("quality run %s belongs to account %d, not %d", run.RunID, run.AccountID, e.accountID)
	case e.grantDigest != "" && !codexQualityGrantMatches(run, e.grantDigest):
		return run, codexQualityUnavailable("the grant does not match quality run %s (it was reissued)", run.RunID)
	case e.keyLookup == nil:
		return run, codexQualityUnavailable("no API key lookup for quality run %s", run.RunID)
	}
	key, err := e.keyLookup(ctx, run.APIKeyID)
	if err != nil {
		return run, codexQualityUnavailable("look up API key %d: %v", run.APIKeyID, err)
	}
	if !codexQualityKeyUsable(key, run.ActorID, run.APIKeyID, run.GroupID) {
		return run, codexQualityUnavailable("API key %d is no longer usable for quality run %s", run.APIKeyID, run.RunID)
	}
	return run, nil
}

func (s *OpenAIGatewayService) SelectCodexQualityAccount(ctx context.Context) (*AccountSelectionResult, error) {
	e := codexQualityExecutionFromContext(ctx)
	if e == nil {
		return nil, codexQualityUnavailable("not a quality diagnosis request")
	}
	run, err := e.current(ctx)
	if err != nil {
		return nil, err
	}
	a, err := e.runtime.account(ctx, run)
	if err != nil {
		return nil, err
	}
	if _, err = e.runtime.qualification(ctx, run); err != nil {
		return nil, err
	}
	if s.concurrencyService == nil {
		return nil, codexQualityUnavailable("no concurrency service")
	}
	acquired, err := s.concurrencyService.AcquireAccountSlot(ctx, a.ID, a.Concurrency)
	switch {
	case err != nil:
		return nil, codexQualityUnavailable("acquire a concurrency slot of account %d: %v", a.ID, err)
	case acquired == nil || !acquired.Acquired:
		return nil, codexQualityUnavailable("account %d has no free concurrency slot (limit %d)", a.ID, a.Concurrency)
	}
	return attachSelectionProfitGate(ctx, attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{Account: a, Acquired: true, ReleaseFunc: acquired.ReleaseFunc})), nil
}

func codexQualityRequestQualification(ctx context.Context, account *Account, model string) (*extensionv1.CodexRoutingQualification, *NativeCodexMetadata, error) {
	e := codexQualityExecutionFromContext(ctx)
	switch {
	case e == nil:
		return nil, nil, codexQualityUnavailable("not a quality diagnosis request")
	case account == nil || account.ID != e.accountID:
		return nil, nil, codexQualityUnavailable("the selected account is not the account of the quality run")
	case model != codexQualityModel:
		return nil, nil, codexQualityUnavailable("model %q is not %s", model, codexQualityModel)
	}
	run, err := e.current(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err = e.runtime.account(ctx, run); err != nil {
		return nil, nil, err
	}
	q, err := e.runtime.qualification(ctx, run)
	return q, e.runtime.installation, err
}

func codexQualityBundleKey(ctx context.Context, kind string) string {
	if e := codexQualityExecutionFromContext(ctx); e != nil {
		return "bundle.quality." + codexQualityHash(e.runID)[:32] + "." + kind
	}
	return ""
}

func codexQualityClockKey(ctx context.Context, fallback string) string {
	if e := codexQualityExecutionFromContext(ctx); e != nil {
		return "clock.quality." + codexQualityHash(e.runID)[:32]
	}
	return fallback
}

func (s *OpenAIGatewayService) closeCodexQualityConnection(q *extensionv1.CodexRoutingQualification) {
	if closer, ok := s.httpUpstream.(interface{ CloseCodexQualityConnection(string, int64, string) }); ok && q != nil {
		closer.CloseCodexQualityConnection(q.Scope.ConnectionLeaseID, q.Scope.AccountID, codexRoutingScopeKey(q.Scope))
	}
}

func (rt *codexQualityRuntime) clearClosedMaterial(ctx context.Context, run codexQualityRun) {
	ctx, cancel := context.WithTimeout(rt.ctx(context.WithoutCancel(ctx)), 3*time.Second)
	defer cancel()
	for _, kind := range []string{"candidate", "live"} {
		key := "bundle.quality." + codexQualityHash(run.RunID)[:32] + "." + kind
		record, err := rt.store.ReadExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key})
		var bundle codexRoutingPrivateBundle
		if err != nil || !record.Found || json.Unmarshal(record.Value, &bundle) != nil || !bundle.Scope.SameOwner(run.Scope) {
			continue
		}
		for i := range bundle.Cookies {
			bundle.Cookies[i].Value = ""
		}
		bundle.Status = "expired"
		raw, _ := json.Marshal(bundle)
		_, _ = rt.store.CompareSwapExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, ExpectedRevision: record.Revision, Value: raw})
	}
	key := "clock.quality." + codexQualityHash(run.RunID)[:32]
	record, err := rt.store.ReadExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key})
	var clock codexRoutingCookieClock
	if err == nil && record.Found && json.Unmarshal(record.Value, &clock) == nil && clock.Scope.SameOwner(run.Scope) {
		clock.Cookies = nil
		raw, _ := json.Marshal(clock)
		_, _ = rt.store.CompareSwapExtensionState(ctx, codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, ExpectedRevision: record.Revision, Value: raw})
	}
}

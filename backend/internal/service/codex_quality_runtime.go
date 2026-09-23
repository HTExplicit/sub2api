package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type codexQualityRuntime struct {
	s            *OpenAIGatewayService
	store        PluginExtensionStateStore
	installation *PluginInstallation
	host         *pluginExtensionHost
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
	if s == nil || s.pluginManager == nil || s.accountRepo == nil {
		return nil, ErrCodexQualityUnavailable
	}
	i, _ := s.pluginManager.installedByKey(codexRuntimePluginKey)
	store, ok := s.pluginManager.repo.(PluginExtensionStateStore)
	if !ok || i == nil {
		return nil, ErrCodexQualityUnavailable
	}
	host, ok := s.pluginManager.buildHostServices(i).(*pluginHostServiceServer)
	if !ok || host == nil || host.extension == nil {
		return nil, ErrCodexQualityUnavailable
	}
	broker, ok := host.extension.(*pluginExtensionHost)
	if !ok || broker == nil {
		return nil, ErrCodexQualityUnavailable
	}
	return &codexQualityRuntime{s: s, store: store, installation: i, host: broker}, nil
}

func (rt *codexQualityRuntime) ctx(ctx context.Context) context.Context {
	return WithPluginExecution(ctx, rt.installation)
}

func (rt *codexQualityRuntime) scope(ctx context.Context, id int64) (extensionv1.CodexRoutingScope, error) {
	raw, _ := json.Marshal(extensionv1.CodexRoutingQuery{AccountID: id, Transport: "http"})
	result, err := rt.host.Call(rt.ctx(ctx), extensionv1.HostInvocation{Operation: extensionv1.HostCodexRoutingScope, Payload: raw})
	var scope extensionv1.CodexRoutingScope
	if err != nil || result.Code != "" || json.Unmarshal(result.Payload, &scope) != nil || scope.AccountID != id {
		return scope, ErrCodexQualityUnavailable
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
	if err != nil || a == nil || a.Schedulable || a.ID != run.AccountID || a.Platform != PlatformOpenAI || a.Type != AccountTypeOAuth || a.IsShadow() || !slices.Contains(a.GroupIDs, run.GroupID) || qualityProxyID(a) != run.ProxyID || CodexTicketAccountIdentity(a) != run.Scope.Identity {
		return nil, ErrCodexQualityUnavailable
	}
	plan := strings.ToLower(strings.TrimSpace(a.GetCredential("plan_type")))
	if plan != "pro" && plan != "chatgpt_pro" {
		return nil, ErrCodexQualityUnavailable
	}
	if rt.s.checkChannelPricingRestriction(ctx, &run.GroupID, codexQualityModel) || (rt.s.needsUpstreamChannelRestrictionCheck(ctx, &run.GroupID) && rt.s.isUpstreamModelRestrictedByChannel(ctx, run.GroupID, a, codexQualityModel, false)) {
		return nil, ErrCodexQualityUnavailable
	}
	// This private stack copy changes only the manual switch for eligibility.
	// It is never returned, cached, projected, or persisted.
	check := *a
	check.Schedulable = true
	if eligible, _ := openAICompatibleAccountEligibilityBeforeProfit(ctx, &check, PlatformOpenAI, codexQualityModel, false, OpenAIEndpointCapabilityResponses); !eligible {
		return nil, ErrCodexQualityUnavailable
	}
	if rt.s.codexQualityHealthBlocked(ctx, &check) {
		return nil, ErrCodexQualityUnavailable
	}
	if rt.s.openAIGroupRequiresPrivacySet(ctx, &run.GroupID) && !a.IsPrivacySet() {
		return nil, ErrCodexQualityUnavailable
	}
	scope, err := rt.scope(ctx, a.ID)
	if err != nil || !run.Scope.SameOwner(scope) {
		return nil, ErrCodexQualityUnavailable
	}
	return a, nil
}

func (s *OpenAIGatewayService) CreateCodexQualityRun(ctx context.Context, actor, accountID int64, key *APIKey, request CodexQualityCreateRequest) (*CodexQualityRunView, error) {
	id, validID := canonicalCodexQualityID(request.RunID)
	if !validID || !validCodexQualityHash(request.PromptSHA256) || actor <= 0 || key == nil || key.GroupID == nil || !codexQualityKeyUsable(key, actor, request.APIKeyID, *key.GroupID) {
		return nil, ErrCodexQualityUnavailable
	}
	request.RunID = id
	if request.MaxSends == 0 {
		request.MaxSends = codexQualityMaxSends
	}
	if request.TTLSeconds == 0 {
		request.TTLSeconds = 7200
	}
	if request.MaxSends < 1 || request.MaxSends > codexQualityMaxSends || request.TTLSeconds < 60 || request.TTLSeconds > 7200 {
		return nil, ErrCodexQualityUnavailable
	}
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return nil, err
	}
	ctx = rt.ctx(ctx)
	a, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || a == nil {
		return nil, ErrCodexQualityUnavailable
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

func (s *OpenAIGatewayService) ReadCodexQualityRun(ctx context.Context, actor, accountID int64, id string) (*CodexQualityRunView, error) {
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return nil, err
	}
	run, _, err := readCodexQualityRun(rt.ctx(ctx), rt.store, id)
	if err != nil || run.RunID == "" || run.ActorID != actor || run.AccountID != accountID {
		return nil, ErrCodexQualityUnavailable
	}
	view := codexQualityView(run, rt.installation.RuntimeGeneration)
	if view.RouteReady {
		if _, err = rt.account(ctx, run); err != nil {
			view.RouteReady = false
		} else if _, err = rt.qualification(ctx, run); err != nil {
			view.RouteReady = false
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
		if run.RunID == "" || run.ActorID != actor || run.AccountID != accountID {
			return ErrCodexQualityUnavailable
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
		return ErrCodexQualityUnavailable
	}
	id, secret, ok := strings.Cut(h.grant, ".")
	id, validID := canonicalCodexQualityID(id)
	trial, validTrial := canonicalCodexQualityID(h.trial)
	if !ok || len(secret) != 43 || !validID || !validTrial || c.Request.Method != http.MethodPost || c.Request.URL.Path != "/v1/responses" || key == nil || key.GroupID == nil {
		return ErrCodexQualityUnavailable
	}
	h.trial = trial
	var fields map[string]json.RawMessage
	if !validCodexQualityJSON(body) || json.Unmarshal(body, &fields) != nil {
		return ErrCodexQualityUnavailable
	}
	for name := range fields {
		if !slices.Contains([]string{"model", "input", "reasoning", "stream", "store", "prompt_cache_key", "client_metadata"}, name) {
			return ErrCodexQualityUnavailable
		}
	}
	input := gjson.GetBytes(body, "input")
	if input.Type != gjson.String || gjson.GetBytes(body, "model").String() != codexQualityModel || gjson.GetBytes(body, "reasoning.effort").String() != codexQualityEffort || !gjson.GetBytes(body, "stream").Bool() || gjson.GetBytes(body, "store").Bool() {
		return ErrCodexQualityUnavailable
	}
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return err
	}
	run, _, err := readCodexQualityRun(rt.ctx(c.Request.Context()), rt.store, id)
	digest := codexQualityHash(h.grant)
	if err != nil || !codexQualityActive(run, time.Now()) || run.APIKeyID != key.ID || run.ActorID != key.UserID || run.GroupID != *key.GroupID || run.PromptSHA256 != codexQualityHash(input.String()) || !codexQualityGrantMatches(run, digest) {
		return ErrCodexQualityUnavailable
	}
	for _, old := range run.Attempts {
		if old.Stage == "business" && old.TrialID == h.trial {
			return ErrCodexQualitySpent
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
	if !codexQualityActive(run, time.Now()) || q == nil || run.RouteRuntimeGeneration != rt.installation.RuntimeGeneration || !q.Valid(time.Now(), run.AccountID, run.Scope.Identity, codexQualityModel) || !q.Scope.SameOwner(run.Scope) || q.Scope.Transport != "http" || q.Scope.ConnectionLeaseID == "" {
		return nil, ErrCodexQualityUnavailable
	}
	bundle, err := readCodexRoutingBundle(rt.ctx(ctx), rt.store, codexRuntimePluginKey, q.Bundle, q.Scope, true)
	if err != nil || bundle.Model != codexQualityModel || bundle.Scope.ConnectionLeaseID != q.Scope.ConnectionLeaseID {
		return nil, ErrCodexQualityUnavailable
	}
	if leases, ok := rt.s.httpUpstream.(interface {
		HasCodexQualityConnection(string, int64, string) bool
	}); !ok || !leases.HasCodexQualityConnection(q.Scope.ConnectionLeaseID, run.AccountID, codexRoutingScopeKey(q.Scope)) {
		return nil, ErrCodexQualityUnavailable
	}
	return q, nil
}

func (e *codexQualityExecution) current(ctx context.Context) (codexQualityRun, error) {
	run, _, err := readCodexQualityRun(e.runtime.ctx(ctx), e.runtime.store, e.runID)
	if err != nil || !codexQualityActive(run, time.Now()) || run.AccountID != e.accountID || (e.grantDigest != "" && !codexQualityGrantMatches(run, e.grantDigest)) {
		return run, ErrCodexQualityUnavailable
	}
	if e.keyLookup == nil {
		return run, ErrCodexQualityUnavailable
	}
	key, err := e.keyLookup(ctx, run.APIKeyID)
	if err != nil || !codexQualityKeyUsable(key, run.ActorID, run.APIKeyID, run.GroupID) {
		return run, ErrCodexQualityUnavailable
	}
	return run, nil
}

func (s *OpenAIGatewayService) SelectCodexQualityAccount(ctx context.Context) (*AccountSelectionResult, error) {
	e := codexQualityExecutionFromContext(ctx)
	if e == nil {
		return nil, ErrCodexQualityUnavailable
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
		return nil, ErrCodexQualityUnavailable
	}
	acquired, err := s.concurrencyService.AcquireAccountSlot(ctx, a.ID, a.Concurrency)
	if err != nil || acquired == nil || !acquired.Acquired {
		return nil, ErrCodexQualityUnavailable
	}
	return attachSelectionProfitGate(ctx, attachSelectionRuntimeBreakerProbe(ctx, &AccountSelectionResult{Account: a, Acquired: true, ReleaseFunc: acquired.ReleaseFunc})), nil
}

func codexQualityRequestQualification(ctx context.Context, account *Account, model string) (*extensionv1.CodexRoutingQualification, *PluginInstallation, error) {
	e := codexQualityExecutionFromContext(ctx)
	if e == nil || account == nil || account.ID != e.accountID || model != codexQualityModel {
		return nil, nil, ErrCodexQualityUnavailable
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

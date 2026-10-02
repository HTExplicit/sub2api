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

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type codexQualityRuntime struct {
	s            *OpenAIGatewayService
	store        NativeCodexStateStore
	installation *NativeCodexMetadata
}

type codexQualityExecutionKey struct{}
type codexQualityHeadersKey struct{}
type codexQualityHeaders struct {
	grant, trial string
	present      bool
}
type codexQualityExecution struct {
	runtime                     *codexQualityRuntime
	runID, grantDigest, trialID string
	accountID                   int64
	requestModel, effort        string
	keyLookup                   CodexQualityKeyLookup
}

type CodexQualityKeyLookup func(context.Context, int64) (*APIKey, error)

// codexQualityFallbackEfforts are the OpenAI reasoning efforts a run accepts
// when the account test resolver knows no levels for the model.
var codexQualityFallbackEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh"}

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
	if s == nil || s.nativeCodexRuntime == nil || s.nativeCodexRuntime.repo == nil || s.accountRepo == nil {
		return nil, codexQualityUnavailable("native Codex runtime is not configured")
	}
	repo := s.nativeCodexRuntime.repo
	metadata, err := repo.LoadNativeCodexMetadata(context.Background())
	if err != nil {
		return nil, codexQualityUnavailable("load Codex runtime metadata: %v", err)
	}
	return &codexQualityRuntime{s: s, store: repo, installation: metadata}, nil
}

func (rt *codexQualityRuntime) ctx(ctx context.Context) context.Context {
	return WithNativeCodexExecution(ctx, rt.installation)
}

func qualityProxyID(a *Account) int64 {
	if a.ProxyID != nil {
		return *a.ProxyID
	}
	return 0
}

// account rechecks the authoritative account row against the run binding,
// including immediately before each send. A diagnostic grant never admits an
// account enabled for ordinary traffic.
func (rt *codexQualityRuntime) account(ctx context.Context, run codexQualityRun) (*Account, error) {
	a, err := rt.s.accountRepo.GetByID(ctx, run.AccountID)
	model, _ := run.binding()
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
	case run.OwnerIdentity == "" || CodexCredentialOwnerIdentity(a) != run.OwnerIdentity:
		return nil, codexQualityUnavailable("account %d credential owner changed since the run was created", a.ID)
	}
	plan := strings.ToLower(strings.TrimSpace(a.GetCredential("plan_type")))
	if plan != "pro" && plan != "chatgpt_pro" {
		return nil, codexQualityUnavailable("account %d plan_type is %q; quality diagnosis requires pro", a.ID, plan)
	}
	// This private stack copy changes only the manual switch for eligibility.
	// It is never returned, cached, projected, or persisted.
	check := *a
	check.Schedulable = true
	if problem := rt.s.codexQualityModelProblem(ctx, &check, run.GroupID, model); problem != "" {
		return nil, codexQualityUnavailable("%s", problem)
	}
	if reason := rt.s.codexQualityHealthBlock(ctx, &check, model); reason != "" {
		return nil, codexQualityUnavailable("account %d: %s", a.ID, reason)
	}
	if rt.s.openAIGroupRequiresPrivacySet(ctx, &run.GroupID) && !a.IsPrivacySet() {
		return nil, codexQualityUnavailable("group %d requires the privacy setting, which account %d has not set", run.GroupID, a.ID)
	}
	return a, nil
}

// codexQualityModelProblem names why the group's channel or the account would
// not send model to the upstream unchanged, or returns "". It reuses the
// scheduler eligibility (account model support), the channel restrictions and
// the channel and account model mappings of ordinary forwarding.
func (s *OpenAIGatewayService) codexQualityModelProblem(ctx context.Context, account *Account, group int64, model string) string {
	if s.checkChannelPricingRestriction(ctx, &group, model) {
		return fmt.Sprintf("channel pricing of group %d restricts %s", group, model)
	}
	if mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, &group, model); mapping.Mapped && mapping.MappedModel != model {
		return fmt.Sprintf("the channel of group %d maps %s to %s; a quality diagnosis sends the bound model unchanged", group, model, mapping.MappedModel)
	}
	if s.needsUpstreamChannelRestrictionCheck(ctx, &group) && s.isUpstreamModelRestrictedByChannel(ctx, group, account, model, false) {
		return fmt.Sprintf("the upstream channel of group %d restricts %s for account %d", group, model, account.ID)
	}
	if eligible, reason := openAICompatibleAccountEligibilityBeforeProfit(ctx, account, PlatformOpenAI, model, false, OpenAIEndpointCapabilityResponses); !eligible {
		return fmt.Sprintf("account %d is not eligible for %s: %s", account.ID, model, reason)
	}
	if upstream := resolveOpenAIAccountUpstreamModelForRequest(account, model, false); upstream != model {
		return fmt.Sprintf("account %d sends %s upstream as %s; a quality diagnosis sends the bound model unchanged", account.ID, model, upstream)
	}
	return ""
}

// codexQualityEffortProblem validates effort with the per-model resolver of the
// account test; when it knows no levels for the model, the OpenAI efforts apply.
func codexQualityEffortProblem(account *Account, model, effort string) string {
	levels, _ := AccountTestReasoningOptions(account, model)
	if len(levels) == 0 {
		if metadata, known := account.GetUpstreamModelMetadata(account.GetMappedModel(model)); known && metadata.Reasoning != nil && !*metadata.Reasoning {
			return fmt.Sprintf("model %s takes no reasoning effort on account %d", model, account.ID)
		}
		levels = codexQualityFallbackEfforts
	}
	if !slices.Contains(levels, effort) {
		return fmt.Sprintf("reasoning_effort %q is not supported for model %s on account %d (supported: %s)", effort, model, account.ID, strings.Join(levels, ", "))
	}
	return ""
}

// codexQualityEffortPolicyProblem names how the reasoning-effort policy of the
// key's group would reject or rewrite the bound effort, or returns "". The
// OpenAI handler applies that policy to OpenAI and composite groups after a
// send is admitted, and a send whose final effort differs from the binding is
// refused, so such a run could never send.
func codexQualityEffortPolicyProblem(group *Group, model, effort string) string {
	if group == nil || (group.Platform != PlatformOpenAI && group.Platform != PlatformComposite) {
		return ""
	}
	body, err := json.Marshal(map[string]any{"model": model, "reasoning": map[string]string{"effort": effort}})
	if err != nil {
		return fmt.Sprintf("encode the reasoning-effort policy check: %v", err)
	}
	governed, _, err := ApplyOpenAIReasoningEffortPolicy(body, group.MaxReasoningEffort, group.ReasoningEffortMappings, group.MaxReasoningEffortOverLimit)
	if err != nil {
		return fmt.Sprintf("the reasoning-effort policy of group %d rejects %s: %v", group.ID, effort, err)
	}
	if sent := gjson.GetBytes(governed, "reasoning.effort").String(); sent != effort {
		return fmt.Sprintf("the reasoning-effort policy of group %d sends %s as %s; a quality diagnosis sends the bound effort unchanged", group.ID, effort, sent)
	}
	return ""
}

func (s *OpenAIGatewayService) CreateCodexQualityRun(ctx context.Context, actor, accountID int64, key *APIKey, request CodexQualityCreateRequest) (*CodexQualityRunView, error) {
	id, validID := canonicalCodexQualityID(request.RunID)
	request.Model = strings.TrimSpace(request.Model)
	request.ReasoningEffort = strings.TrimSpace(request.ReasoningEffort)
	switch {
	case !validID:
		return nil, codexQualityUnavailable("run_id %q is not a canonical UUID", request.RunID)
	case !validCodexQualityHash(request.PromptSHA256):
		return nil, codexQualityUnavailable("prompt_sha256 must be 64 lowercase hex characters")
	case request.Model == "":
		return nil, codexQualityUnavailable("model is required")
	case len(request.Model) > codexQualityModelNameLimit:
		// The model guard fails any longer model declaration.
		return nil, codexQualityUnavailable("model is %d bytes; at most %d are accepted", len(request.Model), codexQualityModelNameLimit)
	case request.ReasoningEffort == "":
		return nil, codexQualityUnavailable("reasoning_effort is required")
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
		request.TTLSeconds = codexQualityMaxTTL
	}
	if request.MaxSends < 1 || request.MaxSends > codexQualityMaxSends {
		return nil, codexQualityUnavailable("max_sends must be 1 to %d, got %d", codexQualityMaxSends, request.MaxSends)
	}
	if request.TTLSeconds < 60 || request.TTLSeconds > codexQualityMaxTTL {
		return nil, codexQualityUnavailable("ttl_seconds must be 60 to %d, got %d", codexQualityMaxTTL, request.TTLSeconds)
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
	wanted := codexQualityRun{RunID: request.RunID, ActorID: actor, APIKeyID: key.ID, AccountID: accountID, GroupID: *key.GroupID, ProxyID: qualityProxyID(a), OwnerIdentity: CodexCredentialOwnerIdentity(a), Model: request.Model, ReasoningEffort: request.ReasoningEffort, PromptSHA256: request.PromptSHA256, MaxSends: request.MaxSends, Status: "open", Attempts: []CodexQualityAttempt{}}
	checked, err := rt.account(ctx, wanted)
	if err != nil {
		return nil, err
	}
	if problem := codexQualityEffortProblem(checked, wanted.Model, wanted.ReasoningEffort); problem != "" {
		return nil, codexQualityUnavailable("%s", problem)
	}
	if problem := codexQualityEffortPolicyProblem(key.Group, wanted.Model, wanted.ReasoningEffort); problem != "" {
		return nil, codexQualityUnavailable("%s", problem)
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
	view := codexQualityView(run)
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
	return codexQualityView(run), nil
}

// CloseCodexQualityRun revokes the grant and keeps the ledger.
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
	return codexQualityView(run), nil
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
	input, model, effort := gjson.GetBytes(body, "input"), gjson.GetBytes(body, "model"), gjson.GetBytes(body, "reasoning.effort")
	if input.Type != gjson.String || model.Type != gjson.String || effort.Type != gjson.String || !gjson.GetBytes(body, "stream").Bool() || gjson.GetBytes(body, "store").Bool() {
		return codexQualityUnavailable("a quality diagnosis needs a string input, a model, a reasoning.effort, stream true and store false")
	}
	rt, err := s.codexQualityRuntime()
	if err != nil {
		return err
	}
	run, _, err := readCodexQualityRun(rt.ctx(c.Request.Context()), rt.store, id)
	digest := codexQualityHash(h.grant)
	boundModel, boundEffort := run.binding()
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
	case model.String() != boundModel || effort.String() != boundEffort:
		return codexQualityUnavailable("quality run %s is bound to model %s with reasoning.effort %s; the request asks for model %q with %q", run.RunID, boundModel, boundEffort, qualityRecordedModel(model.String()), qualityRecordedModel(effort.String()))
	}
	for _, old := range run.Attempts {
		if old.Stage == codexQualityStage && old.TrialID == h.trial {
			return fmt.Errorf("%w: trial %s was already sent", ErrCodexQualitySpent, h.trial)
		}
	}
	ctx := ensureOpenAIRuntimeBreakerProbeOwner(c.Request.Context())
	if _, err = rt.account(ctx, run); err != nil {
		return err
	}
	e := &codexQualityExecution{runtime: rt, runID: id, grantDigest: digest, trialID: h.trial, accountID: run.AccountID, keyLookup: lookups[0]}
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

// SelectCodexQualityAccount is the private selection of a diagnostic send:
// only the run's account, never the ordinary scheduler and never a fallback.
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

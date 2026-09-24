package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/google/uuid"
)

type codexRoutingHostDirectory interface {
	PrepareCodexRoutingScope(context.Context, int64, string) (extensionv1.CodexRoutingScope, error)
	ExecuteCodexRoutingProbe(context.Context, extensionv1.CodexRoutingQuery, string, time.Time) (*http.Response, extensionv1.CodexRoutingScope, error)
}

type codexRoutingLeaseDirectory interface {
	CheckCodexRoutingLease(context.Context, extensionv1.CodexRoutingScope, time.Time) error
}

func isCodexRoutingHostOperation(operation extensionv1.HostOperation) bool {
	return operation == extensionv1.HostCodexRoutingScope || operation == extensionv1.HostCodexRoutingProbe || operation == extensionv1.HostCodexRoutingCheck || operation == extensionv1.HostCodexRoutingCleanup
}

func (h *pluginExtensionHost) callCodexRouting(ctx context.Context, in extensionv1.HostInvocation) (extensionv1.Result, error) {
	if in.Operation == extensionv1.HostCodexRoutingCleanup {
		return h.redactExpiredCodexRoutingMaterial(ctx)
	}
	var query extensionv1.CodexRoutingQuery
	if h.key != codexRuntimePluginKey || len(in.Payload) > 16384 || json.Unmarshal(in.Payload, &query) != nil || query.AccountID <= 0 {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	directory, ok := h.directory.(codexRoutingHostDirectory)
	if !ok {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	account, err := h.directory.ReadExtensionAccount(ctx, query.AccountID)
	if err != nil || account == nil || account.Shadow || account.Platform != PlatformOpenAI || (account.Type != AccountTypeOAuth && account.Type != AccountTypeSetupToken) || !h.permitsAccount(account.Platform, account.Type, account.ID, true) {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	if query.Transport == "" {
		query.Transport = "http"
	}
	if query.Transport != "http" && query.Transport != "ws" {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	scope, err := directory.PrepareCodexRoutingScope(ctx, query.AccountID, query.Transport)
	if err != nil {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	marshal := func(value any) (extensionv1.Result, error) {
		raw, err := json.Marshal(value)
		return extensionv1.Result{Payload: raw}, err
	}
	if in.Operation == extensionv1.HostCodexRoutingScope {
		return marshal(scope)
	}
	if query.Model == "" || len(query.Model) > 256 {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	query.ReasoningEffort, err = codexRoutingProbeEffort(query.ReasoningEffort)
	if err != nil {
		return extensionv1.Result{}, err
	}
	result := extensionv1.CodexRoutingProbeResult{Scope: scope, Observation: extensionv1.CodexRoutingObservation{Stage: query.Stage, Code: "routing_unavailable", RequestedModel: query.Model, ReasoningEffort: query.ReasoningEffort, Transport: query.Transport, ObservedAt: time.Now().UTC()}}
	var bundle codexRoutingPrivateBundle
	var bundleRef extensionv1.CodexRoutingBundleRef
	cookieHeader := ""
	expires := time.Now().Add(extensionv1.CodexRoutingMaxAge)
	if query.Bundle != nil {
		bundle, err = readCodexRoutingBundle(ctx, h.state, h.key, *query.Bundle, scope, false)
		if err != nil {
			result.Observation.Code = "routing_stale"
			return marshal(result)
		}
		bundleRef = *query.Bundle
		cookieHeader, expires = codexRoutingCookieHeader(bundle.Cookies, time.Now())
		if cookieHeader == "" {
			result.Observation.Code = "routing_cookie_expired"
			return marshal(result)
		}
	}
	if in.Operation == extensionv1.HostCodexRoutingCheck {
		result.Scope, result.Bundle = bundle.Scope, query.Bundle
		checker, ok := h.directory.(codexRoutingLeaseDirectory)
		if !ok || query.Bundle == nil || bundle.Status != "qualified" || bundle.Model != query.Model ||
			bundle.Scope.Transport != query.Transport || query.Scope == nil || !query.Scope.SameOwner(bundle.Scope) ||
			query.Scope.Transport != bundle.Scope.Transport || query.Scope.ConnectionLeaseID != bundle.Scope.ConnectionLeaseID ||
			checker.CheckCodexRoutingLease(ctx, bundle.Scope, bundle.ExpiresAt) != nil {
			result.Observation.Code = "routing_connection_expired"
			return marshal(result)
		}
		result.Valid = true
		result.Observation.Code = "routing_verified"
		return marshal(result)
	}
	if query.OperationID == "" || len(query.OperationID) > 256 || (query.Stage != "acquire" && query.Stage != "verify") || (query.Stage == "verify" && query.Bundle == nil) {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	if query.Scope != nil && !query.Scope.SameOwner(scope) {
		result.Observation.Code = "routing_stale"
		return marshal(result)
	}
	// Persist a spent stage before IO. A lost RPC reply is not permission to
	// replay the same upstream request, even after a process restart.
	spentKey := "spent." + codexRoutingDigest(fmt.Sprint(query.AccountID), query.OperationID, query.Stage, query.Model, query.Transport)
	spent, err := h.state.CompareSwapExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: spentKey, Value: json.RawMessage(`{"spent":true}`)})
	if err != nil || !spent.Applied {
		result.Observation.Code = "ticket_interrupted"
		return marshal(result)
	}
	if err := consumeCodexValidationBudget(ctx, h.state, h.key, query); err != nil {
		result.Observation.Code = "routing_budget_spent"
		return marshal(result)
	}
	clockKey := codexQualityClockKey(ctx, "clock."+codexRoutingScopeKey(scope))
	clockRecord, err := h.state.ReadExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey})
	if err != nil {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	clock := codexRoutingCookieClock{Scope: scope}
	if clockRecord.Found && (json.Unmarshal(clockRecord.Value, &clock) != nil || !clock.Scope.SameOwner(scope)) {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	// Acquisition is cold; existing business-route material never crosses to
	// an unverified rotating acquisition connection.
	if query.Stage == "acquire" {
		cookieHeader = ""
	}
	start := time.Now()
	if h.activity != nil {
		observationID, activityErr := h.activity.BeginUnbilled(ctx, query.AccountID, h.key)
		if activityErr != nil {
			result.Observation.Code = "routing_observation_unavailable"
			return marshal(result)
		}
		defer func() {
			finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer finishCancel()
			_ = h.activity.FinishUnbilled(finishCtx, query.AccountID, h.key, observationID)
		}()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	response, wireScope, probeErr := directory.ExecuteCodexRoutingProbe(probeCtx, query, cookieHeader, expires)
	result.Scope = wireScope
	result.Observation.CookieSent = cookieHeader != ""
	result.Observation.DurationMS = time.Since(start).Milliseconds()
	if probeErr != nil || response == nil {
		result.Observation.Code = "routing_transport"
		if errors.Is(probeErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			result.Observation.Code = "routing_cancelled"
		}
		return marshal(result)
	}
	defer func() { _ = response.Body.Close() }()
	result.Observation.HTTPStatus = response.StatusCode
	result.Observation.StateLength = len(strings.TrimSpace(response.Header.Get(openAICodexTurnStateHeader)))
	target, _ := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	changes := parseCodexRoutingCookies(response.Header, target, start)
	if err := preserveCodexCookieFirstSeen(ctx, h.state, h.key, scope, &clock, changes, start); err != nil {
		result.Observation.Code = "routing_persist"
		return marshal(result)
	}
	// Deletions are authoritative even on an unsuccessful response. A stale
	// CAS loses rather than rebasing old snapshots over a newer tombstone.
	clock.apply(changes, start)
	clockJSON, _ := json.Marshal(clock)
	savedClock, err := h.state.CompareSwapExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey, ExpectedRevision: clockRecord.Revision, Value: clockJSON, NextAt: codexCookieClockDeadline(clock)})
	if err != nil || !savedClock.Applied {
		result.Observation.Code = "routing_stale"
		return marshal(result)
	}
	if response.StatusCode != http.StatusOK && (query.Transport != "ws" || response.StatusCode != http.StatusSwitchingProtocols) {
		result.Observation.Code = "routing_upstream"
		return marshal(result)
	}
	if err := readCodexRoutingCompletion(response.Body, response.Header.Get("Content-Type"), query.Model, &result.Observation, response.Header); err != nil {
		// A completed cold acquisition can issue a useful routing Cookie even
		// when that cold turn selected another model. It is only a candidate;
		// business-exit verification remains the sole model qualification gate.
		if query.Stage != "acquire" || !result.Observation.Completed || result.Observation.ResponseModel == "" || result.Observation.Code != "routing_model_mismatch" {
			return marshal(result)
		}
	}
	result.Observation.DurationMS = time.Since(start).Milliseconds()
	current, err := directory.PrepareCodexRoutingScope(ctx, query.AccountID, query.Transport)
	if err != nil || !scope.SameOwner(current) || ctx.Err() != nil {
		result.Observation.Code = "routing_stale"
		return marshal(result)
	}
	if query.Stage == "acquire" {
		bundle = codexRoutingPrivateBundle{Schema: extensionv1.CodexRoutingSchema, Scope: scope, Status: "candidate", Model: query.Model, Cookies: clock.selected(nil, changes, time.Now()), ClockKey: clockKey, ClockRevision: savedClock.Revision}
		bundleRef.Key = fixedCodexRoutingBundleKey(ctx, query, "candidate")
	} else {
		// Verification may refresh a routing cookie on this very connection.
		// Publish exactly the values observed by the verified response.
		bundle.Cookies, bundle.ClockRevision = clock.selected(bundle.Cookies, changes, time.Now()), savedClock.Revision
		bundle.Scope, bundle.Status, bundle.Model = wireScope, "qualified", query.Model
		bundleRef.Key = fixedCodexRoutingBundleKey(ctx, query, "live")
		if wireScope.ConnectionLeaseID == "" {
			result.Observation.Code = "routing_connection_unknown"
			return marshal(result)
		}
	}
	_, deadline := codexRoutingCookieHeader(bundle.Cookies, time.Now())
	if deadline.IsZero() {
		result.Observation.Code = "routing_cookie_missing"
		return marshal(result)
	}
	bundle.ExpiresAt = earlierCodexTime(deadline, expires)
	bundle.Observation = result.Observation
	raw, _ := json.Marshal(bundle)
	previousBundle, readErr := h.state.ReadExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundleRef.Key})
	if readErr != nil {
		result.Observation.Code = "routing_persist"
		return marshal(result)
	}
	saved, err := h.state.CompareSwapExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundleRef.Key, ExpectedRevision: previousBundle.Revision, Value: raw, NextAt: &bundle.ExpiresAt})
	if err != nil || !saved.Applied {
		result.Observation.Code = "routing_stale"
		return marshal(result)
	}
	result.Bundle = &extensionv1.CodexRoutingBundleRef{Key: bundleRef.Key, Revision: saved.Revision, ExpiresAt: bundle.ExpiresAt, ConnectionLeaseID: bundle.Scope.ConnectionLeaseID}
	for _, cookie := range bundle.Cookies {
		result.Observation.CookieNames = append(result.Observation.CookieNames, cookie.Name)
	}
	result.Valid = true
	if query.Stage == "acquire" {
		result.Observation.Code = "routing_candidate"
	} else {
		result.Observation.Code = "routing_verified"
	}
	return marshal(result)
}

func readCodexRoutingBundle(ctx context.Context, store PluginExtensionStateStore, key string, ref extensionv1.CodexRoutingBundleRef, scope extensionv1.CodexRoutingScope, qualified bool) (codexRoutingPrivateBundle, error) {
	var bundle codexRoutingPrivateBundle
	if store == nil || !strings.HasPrefix(ref.Key, "bundle.") || len(ref.Key) > 80 || ref.Revision <= 0 || !time.Now().Before(ref.ExpiresAt) {
		return bundle, errCodexRoutingUnavailable
	}
	record, err := store.ReadExtensionState(ctx, key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: ref.Key})
	if err != nil || !record.Found || record.Revision != ref.Revision || json.Unmarshal(record.Value, &bundle) != nil || bundle.Schema != extensionv1.CodexRoutingSchema || !scope.SameOwner(bundle.Scope) || !time.Now().Before(bundle.ExpiresAt) || !ref.ExpiresAt.Equal(bundle.ExpiresAt) || ref.ConnectionLeaseID != bundle.Scope.ConnectionLeaseID || (qualified && bundle.Status != "qualified") {
		return bundle, errCodexRoutingUnavailable
	}
	clockRecord, err := store.ReadExtensionState(ctx, key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundle.ClockKey})
	var clock codexRoutingCookieClock
	if err != nil || !clockRecord.Found || json.Unmarshal(clockRecord.Value, &clock) != nil || !clock.Scope.SameOwner(scope) {
		return bundle, errCodexRoutingUnavailable
	}
	for _, cookie := range bundle.Cookies {
		digest := codexRoutingDigest(cookie.Name, cookie.Value)
		first, exists := clock.Seen[digest]
		if !exists {
			record, readErr := store.ReadExtensionState(ctx, key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "seen." + codexRoutingDigest(codexRoutingScopeKey(scope), digest)})
			var seen struct {
				First time.Time `json:"first_seen"`
			}
			if readErr == nil && record.Found && json.Unmarshal(record.Value, &seen) == nil {
				first, exists = seen.First, !seen.First.IsZero()
			}
		}
		if !exists || !first.Equal(cookie.FirstSeen) || !time.Now().Before(cookie.ExpiresAt) {
			return bundle, errCodexRoutingUnavailable
		}
		if deleted, exists := clock.Tombstones[cookie.Name]; exists && !cookie.FirstSeen.After(deleted) {
			return bundle, errCodexRoutingUnavailable
		}
	}
	return bundle, nil
}

func (s *OpenAIGatewayService) PrepareCodexRoutingScope(ctx context.Context, id int64, transport string) (extensionv1.CodexRoutingScope, error) {
	a, err := s.accountRepo.GetByID(ctx, id)
	if err != nil || !isOpenAICodexTicketAccount(a) {
		return extensionv1.CodexRoutingScope{}, errCodexRoutingUnavailable
	}
	identity, err := resolveCodexOutboundIdentityForAccountContext(ctx, a, s.codexIdentityOverrideUA(a))
	if err != nil {
		return extensionv1.CodexRoutingScope{}, err
	}
	seed, _ := codexFingerprintSeed(a.Extra)
	profile := codexRoutingDigest(identity.userAgent, identity.originator, identity.version, seed, resolveConvergedInstallationID(a, seed), string(a.GetCodexFingerprintMode()), "go-crypto-tls", "native-http1")
	config := s.openAICodexTicketConfig()
	return extensionv1.CodexRoutingScope{AccountID: id, Identity: CodexTicketAccountIdentity(a), ProfileHash: profile, RouteHash: codexRoutingDigest(resolveAccountProxyURL(a), config.HarvestProxyURL), RouteEvidence: "connection_required", Transport: transport}, nil
}

func (s *OpenAIGatewayService) CheckCodexRoutingLease(ctx context.Context, scope extensionv1.CodexRoutingScope, expiresAt time.Time) error {
	if ctx.Err() != nil || scope.Transport != "http" || scope.ConnectionLeaseID == "" {
		return ErrCodexConnectionLeaseExpired
	}
	inspector, ok := s.httpUpstream.(CodexConnectionLeaseInspector)
	if !ok {
		return ErrCodexConnectionLeaseExpired
	}
	return inspector.CheckCodexConnectionLease(scope.AccountID, codexRoutingScopeKey(scope), scope.ConnectionLeaseID, expiresAt)
}

func (s *OpenAIGatewayService) ExecuteCodexRoutingProbe(ctx context.Context, query extensionv1.CodexRoutingQuery, cookies string, deadline time.Time) (*http.Response, extensionv1.CodexRoutingScope, error) {
	scope, err := s.PrepareCodexRoutingScope(ctx, query.AccountID, query.Transport)
	if err != nil {
		return nil, scope, err
	}
	cfg := s.openAICodexTicketConfig()
	_, validation := codexValidationFromContext(ctx)
	if !cfg.Enabled || (!slices.Contains(cfg.Models, query.Model) && !validation) {
		return nil, scope, errCodexRoutingUnavailable
	}
	if query.Transport == "ws" {
		return s.executeCodexRoutingWSProbe(ctx, query, cookies, deadline, scope)
	}
	a, err := s.accountRepo.GetByID(ctx, query.AccountID)
	if err != nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	token, _, err := s.GetAccessToken(withCodexTicketCredentials(ctx), a)
	if err != nil {
		return nil, scope, errCodexRoutingUnavailable
	}
	request, err := s.buildCodexRoutingProbe(ctx, a, query.Model, token, cookies, query.ReasoningEffort)
	if err != nil {
		return nil, scope, err
	}
	if query.Stage == "acquire" {
		if cfg.HarvestProxyURL == "" {
			return nil, scope, errCodexRoutingUnavailable
		}
		response, err := s.doCodexRoutingAcquisition(request, cfg.HarvestProxyURL, a.ID)
		return response, scope, err
	}
	transport, ok := s.httpUpstream.(CodexConnectionLeaseUpstream)
	if !ok {
		return nil, scope, errCodexRoutingUnavailable
	}
	if err := reserveCodexQualitySend(request, a, nil); err != nil {
		return nil, scope, err
	}
	response, lease, err := transport.DoWithCodexConnectionLease(request, resolveAccountProxyURL(a), a.ID, codexRoutingScopeKey(scope), "", deadline, nil)
	scope.ConnectionLeaseID, scope.RouteEvidence = lease, "connection"
	observeCodexQualityResponse(ctx, response, err, &extensionv1.CodexRoutingQualification{Scope: scope, Model: query.Model})
	if err == nil && response != nil {
		observeCodexInfrastructureCookies(scope, response.Header, deadline)
		s.observeCodexWire(ctx, a, request, response, &extensionv1.CodexRoutingQualification{Scope: scope, Model: query.Model})
	}
	return response, scope, err
}

func (s *OpenAIGatewayService) buildCodexRoutingProbe(ctx context.Context, account *Account, model, token, cookies string, explicitEffort ...string) (*http.Request, error) {
	effort := ""
	if len(explicitEffort) > 0 {
		effort = explicitEffort[0]
	}
	effort, err := codexRoutingProbeEffort(effort)
	if err != nil {
		return nil, err
	}
	session, turn := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	metadata := map[string]any{"session_id": session, "thread_id": session, "turn_id": turn, "x-codex-window-id": session + ":0"}
	body := map[string]any{"model": model, "store": false, "stream": true, "instructions": "Reply with exactly: pong", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "ping"}}}}, "client_metadata": metadata, "prompt_cache_key": session}
	if effort != "" {
		body["reasoning"] = map[string]any{"effort": effort}
	}
	identity, err := resolveCodexOutboundIdentityForAccountContext(ctx, account, s.codexIdentityOverrideUA(account))
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	// A probe has no inbound client, so it speaks as the account's outbound
	// identity: the same triple the routing scope profile hashes.
	headers.Set("originator", identity.originator)
	headers.Set("user-agent", identity.userAgent)
	headers.Set("version", identity.version)
	headers.Set("session-id", session)
	headers.Set("thread-id", session)
	headers.Set("x-client-request-id", session)
	headers.Set("x-codex-window-id", session+":0")
	applyCodexAccountIdentityClientMetadataMap(body, account, 0)
	applyCodexAccountIdentityHeaders(headers, account, 0)
	ids := resolveCodexFingerprintIDsFromRequest(account, headers)
	applyCodexFingerprintClientMetadata(body, ids)
	applyCodexFingerprintHeaders(headers, ids)
	if err := s.finalizeCodexOutboundHeaders(ctx, nil, account, headers, model, ""); err != nil {
		return nil, err
	}
	applyOpenAICodexBetaFeatures(nil, account, headers)
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Accept", "text/event-stream")
	headers.Set("Content-Type", "application/json")
	if cookies != "" {
		headers.Set("Cookie", cookies)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(ctx), http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header = headers
	return prepareCodexTransport(req, account)
}

var _ codexRoutingHostDirectory = (*OpenAIGatewayService)(nil)

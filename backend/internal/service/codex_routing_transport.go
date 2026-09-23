package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type codexRoutingModelKey struct{}

func withCodexRoutingModel(request *http.Request, model string) *http.Request {
	if request == nil {
		return nil
	}
	return request.WithContext(context.WithValue(request.Context(), codexRoutingModelKey{}, strings.TrimSpace(model)))
}

func (s *OpenAIGatewayService) codexRoutingApplies(account *Account, model string) bool {
	cfg := s.openAICodexTicketConfig()
	return cfg.Enabled && isOpenAICodexTicketAccount(account) && slices.Contains(cfg.Models, model)
}

func (m *PluginManager) codexRoutingQualification(ctx context.Context, account *Account, model string) (*extensionv1.CodexRoutingQualification, *PluginInstallation, error) {
	installation, _ := m.installedByKey(codexRuntimePluginKey)
	if installation == nil {
		return nil, nil, errCodexRoutingUnavailable
	}
	raw, _ := json.Marshal(extensionv1.SchedulingRequest{Account: *extensionAccount(account), Model: model, Now: time.Now().UTC()})
	result, err := m.InvokeExtension(ctx, installation.ID, account.Platform, account.Type, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "inject", AccountID: account.ID, Payload: raw})
	var injection extensionv1.CodexRoutingInjection
	if err != nil || result.Code != "" || json.Unmarshal(result.Payload, &injection) != nil || !injection.Qualification.Valid(time.Now(), account.ID, CodexTicketAccountIdentity(account), model) {
		return nil, installation, errCodexRoutingUnavailable
	}
	return injection.Qualification, installation, nil
}

func (s *OpenAIGatewayService) prepareQualifiedCodexRequest(request *http.Request, account *Account, model string) (*http.Request, *extensionv1.CodexRoutingQualification, *PluginInstallation, error) {
	if request.URL == nil || request.URL.Scheme != "https" || request.URL.Hostname() != "chatgpt.com" || (request.URL.Path != "/backend-api/codex/responses" && request.URL.Path != "/backend-api/codex/responses/compact") {
		return request, nil, nil, errCodexRoutingUnavailable
	}
	q, installation, err := s.pluginManager.codexRoutingQualification(request.Context(), account, model)
	if err != nil {
		return request, nil, installation, err
	}
	scope, err := s.PrepareCodexRoutingScope(request.Context(), account.ID, "http")
	if err != nil || !q.Scope.SameOwner(scope) || q.Scope.Transport != "http" || q.Scope.ConnectionLeaseID == "" {
		return request, q, installation, errCodexRoutingUnavailable
	}
	store, _ := s.pluginManager.repo.(PluginExtensionStateStore)
	bundle, err := readCodexRoutingBundle(request.Context(), store, installation.PluginKey, q.Bundle, scope, true)
	if err != nil || bundle.Model != model || bundle.Scope.ConnectionLeaseID != q.Scope.ConnectionLeaseID || bundle.Scope.Transport != q.Scope.Transport || q.ExpiresAt.After(bundle.ExpiresAt) {
		return request, q, installation, errCodexRoutingUnavailable
	}
	cookies, _ := codexRoutingCookieHeader(bundle.Cookies, time.Now())
	if cookies == "" {
		return request, q, installation, errCodexRoutingUnavailable
	}
	wire := request.Clone(request.Context())
	wire.Header.Set("Cookie", cookies)
	applyCodexInfrastructureCookies(wire, q.Scope, q.ExpiresAt)
	return wire, q, installation, nil
}

func (s *OpenAIGatewayService) doQualifiedCodexUpstream(request *http.Request, account *Account, proxyURL string) (*http.Response, bool, error) {
	model, _ := request.Context().Value(codexRoutingModelKey{}).(string)
	if !s.codexRoutingApplies(account, model) {
		return nil, false, nil
	}
	wire, q, installation, err := s.prepareQualifiedCodexRequest(request, account, model)
	if err != nil {
		if !s.openAICodexTicketConfig().FailClosed {
			return nil, false, nil
		}
		return nil, true, err
	}
	transport, ok := s.httpUpstream.(CodexConnectionLeaseUpstream)
	if !ok {
		return nil, true, errCodexRoutingUnavailable
	}
	start := time.Now()
	response, _, err := transport.DoWithCodexConnectionLease(wire, proxyURL, account.ID, codexRoutingScopeKey(q.Scope), q.Scope.ConnectionLeaseID, q.ExpiresAt, nil)
	if err != nil {
		s.publishCodexRoutingObservation(request.Context(), account, installation, q, extensionv1.CodexRoutingObservation{Code: "routing_connection_expired", Stage: "business", RequestedModel: model, ObservedAt: time.Now().UTC(), Transport: "http", CookieSent: false}, nil)
		return nil, true, err
	}
	if observer := codexWireObserverFromContext(wire.Context()); observer != nil {
		observer(wire)
	}
	observeCodexInfrastructureCookies(q.Scope, response.Header, q.ExpiresAt)
	s.observeCodexWire(request.Context(), account, wire, response, q)
	observation := extensionv1.CodexRoutingObservation{Stage: "business", Code: "routing_incomplete", HTTPStatus: response.StatusCode, RequestedModel: model, StateLength: len(response.Header.Get(openAICodexTurnStateHeader)), ObservedAt: time.Now().UTC(), Transport: "http", CookieSent: true}
	deleted := s.recordCodexRoutingDeletions(request.Context(), installation, q, response.Header, observation.ObservedAt)
	if wire.URL.Path == "/backend-api/codex/responses/compact" {
		// Compaction is not a model completion and cannot renew qualification.
		// Cookie revocation still applies immediately to the existing route.
		if deleted {
			observation.Code = "routing_cookie_deleted"
			s.publishCodexRoutingObservation(request.Context(), account, installation, q, observation, nil)
		}
		return response, true, nil
	}
	for _, cookie := range (&http.Request{Header: wire.Header}).Cookies() {
		if isCodexRoutingCookie(cookie.Name) {
			observation.CookieNames = append(observation.CookieNames, cookie.Name)
		}
	}
	body := &codexRoutingObservedBody{ReadCloser: response.Body, sse: strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")}
	body.finish = func(completion codexRoutingCompletion) {
		observation.Completed = completion.Completed && !completion.Failed && response.StatusCode == http.StatusOK
		observation.ResponseModel = completion.Model
		observation.ModelMatched = observation.Completed && completion.Model == model
		observation.DurationMS = time.Since(start).Milliseconds()
		if observation.ModelMatched {
			observation.Code = "routing_verified"
		} else if observation.Completed {
			observation.Code = "routing_model_mismatch"
		}
		if deleted {
			observation.Code = "routing_cookie_deleted"
		}
		var replacement *extensionv1.CodexRoutingQualification
		if observation.ModelMatched && !deleted {
			replacement = s.refreshObservedCodexCookies(request.Context(), installation, q, response.Header, observation)
		}
		s.publishCodexRoutingObservation(request.Context(), account, installation, q, observation, replacement)
	}
	response.Body = body
	return response, true, nil
}

type codexRoutingObservedBody struct {
	io.ReadCloser
	sse        bool
	completion codexRoutingCompletion
	json       []byte
	once       sync.Once
	finish     func(codexRoutingCompletion)
}

func (body *codexRoutingObservedBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if n > 0 {
		if body.sse {
			body.completion.feed(p[:n])
		} else if len(body.json)+n <= codexRoutingProbeReadLimit {
			body.json = append(body.json, p[:n]...)
		} else {
			body.completion.Failed = true
		}
	}
	if err != nil {
		body.complete()
	}
	return n, err
}

func (body *codexRoutingObservedBody) complete() {
	body.once.Do(func() {
		if !body.sse && !body.completion.Failed {
			body.completion.json(body.json)
		}
		body.finish(body.completion)
	})
}

func (body *codexRoutingObservedBody) Close() error { body.complete(); return body.ReadCloser.Close() }

func (s *OpenAIGatewayService) publishCodexRoutingObservation(ctx context.Context, account *Account, installation *PluginInstallation, q *extensionv1.CodexRoutingQualification, observation extensionv1.CodexRoutingObservation, replacement *extensionv1.CodexRoutingQualification) {
	if s.pluginManager == nil || installation == nil || q == nil {
		return
	}
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	raw, _ := json.Marshal(extensionv1.CodexRoutingResponse{AccountID: account.ID, Identity: CodexTicketAccountIdentity(account), Model: q.Model, Qualification: *q, Observation: observation, Replacement: replacement})
	_, _ = s.pluginManager.InvokeExtension(call, installation.ID, account.Platform, account.Type, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.routing.observe", AccountID: account.ID, Payload: raw})
}

func (s *OpenAIGatewayService) refreshObservedCodexCookies(ctx context.Context, installation *PluginInstallation, q *extensionv1.CodexRoutingQualification, headers http.Header, observation extensionv1.CodexRoutingObservation) *extensionv1.CodexRoutingQualification {
	store, ok := s.pluginManager.repo.(PluginExtensionStateStore)
	if !ok {
		return nil
	}
	ctx = WithPluginExecution(context.WithoutCancel(ctx), installation)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	bundle, err := readCodexRoutingBundle(ctx, store, installation.PluginKey, q.Bundle, q.Scope, true)
	if err != nil {
		return nil
	}
	target, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", nil)
	changes := parseCodexRoutingCookies(headers, target.URL, observation.ObservedAt)
	changes = changedCodexRoutingCookies(bundle.Cookies, changes)
	if len(changes) == 0 {
		return nil
	}
	record, err := store.ReadExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundle.ClockKey})
	var clock codexRoutingCookieClock
	if err != nil || !record.Found || json.Unmarshal(record.Value, &clock) != nil || !clock.Scope.SameOwner(q.Scope) {
		return nil
	}
	for _, cookie := range bundle.Cookies {
		if deleted, exists := clock.Tombstones[cookie.Name]; exists && !cookie.FirstSeen.After(deleted) {
			return nil
		}
	}
	if err := preserveCodexCookieFirstSeen(ctx, store, installation.PluginKey, q.Scope, &clock, changes, observation.ObservedAt); err != nil {
		return nil
	}
	clock.apply(changes, observation.ObservedAt)
	raw, _ := json.Marshal(clock)
	saved, err := store.CompareSwapExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundle.ClockKey, ExpectedRevision: record.Revision, Value: raw, NextAt: codexCookieClockDeadline(clock)})
	if err != nil || !saved.Applied {
		return nil
	}
	// A refreshed Cookie cannot extend a connection lease. A new connection
	// always needs a new complete validation even if cookies remain live.
	bundle.Cookies, bundle.ClockRevision, bundle.Observation = clock.selected(bundle.Cookies, changes, time.Now()), saved.Revision, observation
	_, expiry := codexRoutingCookieHeader(bundle.Cookies, time.Now())
	if expiry.IsZero() {
		return nil
	}
	bundle.ExpiresAt = earlierCodexTime(expiry, q.ExpiresAt)
	raw, _ = json.Marshal(bundle)
	key := q.Bundle.Key
	next, err := store.CompareSwapExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, ExpectedRevision: q.Bundle.Revision, Value: raw, NextAt: &bundle.ExpiresAt})
	if err != nil || !next.Applied {
		return nil
	}
	replacement := *q
	replacement.Bundle = extensionv1.CodexRoutingBundleRef{Key: key, Revision: next.Revision, ExpiresAt: bundle.ExpiresAt, ConnectionLeaseID: q.Scope.ConnectionLeaseID}
	replacement.VerifiedAt, replacement.ExpiresAt = observation.ObservedAt, bundle.ExpiresAt
	return &replacement
}

func (s *OpenAIGatewayService) recordCodexRoutingDeletions(ctx context.Context, installation *PluginInstallation, q *extensionv1.CodexRoutingQualification, headers http.Header, now time.Time) bool {
	target, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", nil)
	var deletions []codexRoutingCookieChange
	for _, change := range parseCodexRoutingCookies(headers, target.URL, now) {
		if change.Delete {
			deletions = append(deletions, change)
		}
	}
	if len(deletions) == 0 {
		return false
	}
	store, ok := s.pluginManager.repo.(PluginExtensionStateStore)
	if !ok {
		return true
	}
	ctx = WithPluginExecution(context.WithoutCancel(ctx), installation)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	key := "clock." + codexRoutingScopeKey(q.Scope)
	for range 3 {
		record, err := store.ReadExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key})
		var clock codexRoutingCookieClock
		if err != nil || !record.Found || json.Unmarshal(record.Value, &clock) != nil {
			break
		}
		clock.apply(deletions, now)
		raw, _ := json.Marshal(clock)
		result, err := store.CompareSwapExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, ExpectedRevision: record.Revision, Value: raw, NextAt: codexCookieClockDeadline(clock)})
		if err != nil || result.Applied {
			break
		}
	}
	return true
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/google/uuid"
)

type codexRoutingHostDirectory interface {
	PrepareCodexRoutingScope(context.Context, int64, string) (extensionv1.CodexRoutingScope, error)
	ExecuteCodexRoutingProbe(context.Context, extensionv1.CodexRoutingQuery, string, time.Time) (*http.Response, extensionv1.CodexRoutingScope, error)
}

type codexRoutingLeaseDirectory interface {
	CheckCodexRoutingLease(context.Context, extensionv1.CodexRoutingScope, time.Time) error
}

// errCodexRoutingToken marks a probe that could not obtain the account access
// token; the host reports it as ticket_token instead of a transport failure.
var errCodexRoutingToken = errors.New("account access token unavailable")

func codexRoutingUnavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errCodexRoutingUnavailable, fmt.Sprintf(format, args...))
}

func isCodexRoutingHostOperation(operation extensionv1.HostOperation) bool {
	return operation == extensionv1.HostCodexRoutingScope || operation == extensionv1.HostCodexRoutingProbe || operation == extensionv1.HostCodexRoutingCheck || operation == extensionv1.HostCodexRoutingCleanup
}

func (h *nativeCodexHost) callCodexRouting(ctx context.Context, in extensionv1.HostInvocation) (extensionv1.Result, error) {
	if in.Operation == extensionv1.HostCodexRoutingCleanup {
		return h.redactExpiredCodexRoutingMaterial(ctx)
	}
	var query extensionv1.CodexRoutingQuery
	switch {
	case h.key != codexRuntimePluginKey:
		return extensionv1.Result{}, codexRoutingUnavailable("routing operations are reserved for the Codex runtime (caller %s)", h.key)
	case len(in.Payload) > 16384:
		return extensionv1.Result{}, codexRoutingUnavailable("routing query of %d bytes exceeds 16384 bytes", len(in.Payload))
	}
	if err := json.Unmarshal(in.Payload, &query); err != nil {
		return extensionv1.Result{}, codexRoutingUnavailable("decode routing query: %v", err)
	}
	if query.AccountID <= 0 {
		return extensionv1.Result{}, codexRoutingUnavailable("routing query has no account id")
	}
	directory, ok := h.directory.(codexRoutingHostDirectory)
	if !ok {
		return extensionv1.Result{}, codexRoutingUnavailable("account directory cannot execute routing probes")
	}
	account, err := h.directory.ReadExtensionAccount(ctx, query.AccountID)
	switch {
	case err != nil:
		return extensionv1.Result{}, codexRoutingUnavailable("read account %d: %v", query.AccountID, err)
	case account == nil:
		return extensionv1.Result{}, codexRoutingUnavailable("account %d not found", query.AccountID)
	case account.Shadow || account.Platform != PlatformOpenAI || (account.Type != AccountTypeOAuth && account.Type != AccountTypeSetupToken):
		return extensionv1.Result{}, codexRoutingUnavailable("account %d (%s/%s, shadow=%t) is not an OpenAI OAuth/setup-token account", account.ID, account.Platform, account.Type, account.Shadow)
	case !h.permitsAccount(account.Platform, account.Type, account.ID, true):
		return extensionv1.Result{}, codexRoutingUnavailable("account %d is not permitted for the Codex runtime", account.ID)
	}
	if query.Transport == "" {
		query.Transport = "http"
	}
	if query.Transport != "http" && query.Transport != "ws" {
		return extensionv1.Result{}, codexRoutingUnavailable("unsupported routing transport %q", query.Transport)
	}
	scope, err := directory.PrepareCodexRoutingScope(ctx, query.AccountID, query.Transport)
	if err != nil {
		return extensionv1.Result{}, codexRoutingUnavailable("prepare routing scope: %v", err)
	}
	marshal := func(value any) (extensionv1.Result, error) {
		raw, err := json.Marshal(value)
		return extensionv1.Result{Payload: raw}, err
	}
	if in.Operation == extensionv1.HostCodexRoutingScope {
		return marshal(scope)
	}
	if query.Model == "" || len(query.Model) > 256 {
		return extensionv1.Result{}, codexRoutingUnavailable("routing query model must be 1 to 256 bytes (got %d)", len(query.Model))
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
			result.Observation.Error = err.Error()
			return marshal(result)
		}
		bundleRef = *query.Bundle
		cookieHeader, expires = codexRoutingCookieHeader(bundle.Cookies, time.Now())
		if cookieHeader == "" {
			result.Observation.Code = "routing_cookie_expired"
			result.Observation.Error = fmt.Sprintf("bundle %s has no live routing cookie left (%d stored)", query.Bundle.Key, len(bundle.Cookies))
			return marshal(result)
		}
	}
	if in.Operation == extensionv1.HostCodexRoutingCheck {
		result.Scope, result.Bundle = bundle.Scope, query.Bundle
		if reason := h.codexRoutingCheckFailure(ctx, query, bundle); reason != "" {
			result.Observation.Code = "routing_connection_expired"
			result.Observation.Error = reason
			return marshal(result)
		}
		result.Valid = true
		result.Observation.Code = "routing_verified"
		return marshal(result)
	}
	if query.OperationID == "" || len(query.OperationID) > 256 || (query.Stage != "acquire" && query.Stage != "verify") || (query.Stage == "verify" && query.Bundle == nil) {
		return extensionv1.Result{}, codexRoutingUnavailable("routing probe needs an operation id of 1 to 256 bytes and stage acquire or verify (verify with a bundle); got operation %q stage %q bundle=%t", query.OperationID, query.Stage, query.Bundle != nil)
	}
	if query.Scope != nil && !query.Scope.SameOwner(scope) {
		result.Observation.Code = "routing_stale"
		result.Observation.Error = codexRoutingScopeChange(*query.Scope, scope)
		return marshal(result)
	}
	// Persist a spent stage before IO. A lost RPC reply is not permission to
	// replay the same upstream request, even after a process restart.
	spentKey := "spent." + codexRoutingDigest(fmt.Sprint(query.AccountID), query.OperationID, query.Stage, query.Model, query.Transport)
	spent, err := h.state.CompareSwapExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: spentKey, Value: json.RawMessage(`{"spent":true}`)})
	if err != nil || !spent.Applied {
		result.Observation.Code = "ticket_interrupted"
		if err != nil {
			result.Observation.Error = "record spent stage: " + err.Error()
		} else {
			result.Observation.Error = fmt.Sprintf("stage %s of operation %s was already sent once; it is never replayed", query.Stage, query.OperationID)
		}
		return marshal(result)
	}
	if err := consumeCodexValidationBudget(ctx, h.state, h.key, query); err != nil {
		result.Observation.Code = "routing_budget_spent"
		result.Observation.Error = err.Error()
		return marshal(result)
	}
	clockKey := codexQualityClockKey(ctx, "clock."+codexRoutingScopeKey(scope))
	clockRecord, err := h.state.ReadExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey})
	if err != nil {
		return extensionv1.Result{}, codexRoutingUnavailable("read cookie clock: %v", err)
	}
	clock := codexRoutingCookieClock{Scope: scope}
	if clockRecord.Found {
		if err := json.Unmarshal(clockRecord.Value, &clock); err != nil {
			return extensionv1.Result{}, codexRoutingUnavailable("decode cookie clock: %v", err)
		}
		if !clock.Scope.SameOwner(scope) {
			return extensionv1.Result{}, codexRoutingUnavailable("cookie clock belongs to another routing owner: %s", codexRoutingScopeChange(clock.Scope, scope))
		}
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
			result.Observation.Error = activityErr.Error()
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
		switch {
		case errors.Is(probeErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
			result.Observation.Code = "routing_cancelled"
		case errors.Is(probeErr, errCodexRoutingToken):
			result.Observation.Code = "ticket_token"
		}
		if probeErr != nil {
			result.Observation.Error = probeErr.Error()
		} else {
			result.Observation.Error = "probe returned no response"
		}
		return marshal(result)
	}
	defer func() { _ = response.Body.Close() }()
	result.Observation.HTTPStatus = response.StatusCode
	result.Observation.RequestID = response.Header.Get("x-request-id")
	result.Observation.CFRay = response.Header.Get("cf-ray")
	result.Observation.State = strings.TrimSpace(response.Header.Get(openAICodexTurnStateHeader))
	result.Observation.StateLength = len(result.Observation.State)
	result.Observation.ResponseHeaders, result.Observation.ResponseHeadersOmitted = codexRoutingResponseHeaders(response.Header)
	target, _ := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	changes := parseCodexRoutingCookies(response.Header, target, start)
	if err := preserveCodexCookieFirstSeen(ctx, h.state, h.key, scope, &clock, changes, start); err != nil {
		result.Observation.Code = "routing_persist"
		result.Observation.Error = "record cookie first-seen time: " + err.Error()
		return marshal(result)
	}
	// Deletions are authoritative even on an unsuccessful response. A stale
	// CAS loses rather than rebasing old snapshots over a newer tombstone.
	clock.apply(changes, start)
	clockJSON, _ := json.Marshal(clock)
	savedClock, err := h.state.CompareSwapExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey, ExpectedRevision: clockRecord.Revision, Value: clockJSON, NextAt: codexCookieClockDeadline(clock)})
	if err != nil || !savedClock.Applied {
		result.Observation.Code = "routing_stale"
		if err != nil {
			result.Observation.Error = "save cookie clock: " + err.Error()
		} else {
			result.Observation.Error = fmt.Sprintf("cookie clock changed concurrently (expected revision %d, current %d)", clockRecord.Revision, savedClock.Revision)
		}
		return marshal(result)
	}
	if response.StatusCode != http.StatusOK && (query.Transport != "ws" || response.StatusCode != http.StatusSwitchingProtocols) {
		result.Observation.Code = "routing_upstream"
		body, readErr := io.ReadAll(io.LimitReader(response.Body, codexRoutingProbeReadLimit))
		if readErr != nil {
			result.Observation.Error = "read upstream error body: " + readErr.Error()
		}
		found := codexUpstreamErrorFromBody(body)
		result.Observation.UpstreamErrorType, result.Observation.UpstreamErrorCode = found.Type, found.Code
		result.Observation.UpstreamErrorMessage, result.Observation.UpstreamErrorParam = found.Message, found.Param
		result.Observation.UpstreamBody = string(boundedCodexUpstreamBody(body))
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
		switch {
		case err != nil:
			result.Observation.Error = "re-read routing scope: " + err.Error()
		case ctx.Err() != nil:
			result.Observation.Error = "probe context: " + ctx.Err().Error()
		default:
			result.Observation.Error = codexRoutingScopeChange(scope, current)
		}
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
			result.Observation.Error = "the verified response did not report the connection lease it used"
			return marshal(result)
		}
	}
	_, deadline := codexRoutingCookieHeader(bundle.Cookies, time.Now())
	if deadline.IsZero() {
		result.Observation.Code = "routing_cookie_missing"
		var names []string
		for _, cookie := range (&http.Response{Header: response.Header}).Cookies() {
			names = append(names, cookie.Name)
		}
		result.Observation.Error = fmt.Sprintf("no live __cflb/__oailb routing cookie after this response (Set-Cookie names: %s)", strings.Join(names, ", "))
		return marshal(result)
	}
	bundle.ExpiresAt = earlierCodexTime(deadline, expires)
	bundle.Observation = codexRoutingPrivateObservation(result.Observation)
	raw, _ := json.Marshal(bundle)
	previousBundle, readErr := h.state.ReadExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundleRef.Key})
	if readErr != nil {
		result.Observation.Code = "routing_persist"
		result.Observation.Error = "read bundle " + bundleRef.Key + ": " + readErr.Error()
		return marshal(result)
	}
	saved, err := h.state.CompareSwapExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundleRef.Key, ExpectedRevision: previousBundle.Revision, Value: raw, NextAt: &bundle.ExpiresAt})
	if err != nil || !saved.Applied {
		result.Observation.Code = "routing_stale"
		if err != nil {
			result.Observation.Error = "save bundle " + bundleRef.Key + ": " + err.Error()
		} else {
			result.Observation.Error = fmt.Sprintf("bundle %s changed concurrently (expected revision %d, current %d)", bundleRef.Key, previousBundle.Revision, saved.Revision)
		}
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

// codexRoutingPrivateObservation is the copy kept inside host-private bundle
// and validation records. The raw STATE and response headers (Set-Cookie
// values included) stay only in the probe result and the runtime's tickets
// records, so bundle expiry and quality-run close keep clearing cookie values.
func codexRoutingPrivateObservation(observation extensionv1.CodexRoutingObservation) extensionv1.CodexRoutingObservation {
	observation.State, observation.ResponseHeaders, observation.ResponseHeadersOmitted = "", nil, nil
	return observation
}

// codexRoutingResponseHeaders keeps upstream response header values whole, in
// name order, while they fit CodexRoutingHeaderLimit (name plus value bytes);
// the names of values that did not fit are returned separately.
func codexRoutingResponseHeaders(headers http.Header) ([]extensionv1.CodexRoutingHeader, []string) {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	slices.Sort(names)
	var kept []extensionv1.CodexRoutingHeader
	var omitted []string
	used := 0
	for _, name := range names {
		for _, value := range headers[name] {
			if used+len(name)+len(value) > extensionv1.CodexRoutingHeaderLimit {
				if len(omitted) == 0 || omitted[len(omitted)-1] != name {
					omitted = append(omitted, name)
				}
				continue
			}
			used += len(name) + len(value)
			kept = append(kept, extensionv1.CodexRoutingHeader{Name: name, Value: value})
		}
	}
	return kept, omitted
}

// codexRoutingScopeChange names the parts of a routing owner that differ.
func codexRoutingScopeChange(before, after extensionv1.CodexRoutingScope) string {
	var changed []string
	for _, field := range []struct{ name, before, after string }{
		{"account", fmt.Sprint(before.AccountID), fmt.Sprint(after.AccountID)},
		{"identity", before.Identity, after.Identity},
		{"profile_hash", before.ProfileHash, after.ProfileHash},
		{"route_hash", before.RouteHash, after.RouteHash},
	} {
		if field.before != field.after {
			changed = append(changed, fmt.Sprintf("%s %q -> %q", field.name, field.before, field.after))
		}
	}
	if len(changed) == 0 {
		return "routing owner is incomplete (account, identity, profile and route hash are all required)"
	}
	return "routing owner changed: " + strings.Join(changed, "; ")
}

// codexRoutingCheckFailure returns why a qualified bundle cannot serve this
// request, or "" when it can.
func (h *nativeCodexHost) codexRoutingCheckFailure(ctx context.Context, query extensionv1.CodexRoutingQuery, bundle codexRoutingPrivateBundle) string {
	checker, ok := h.directory.(codexRoutingLeaseDirectory)
	switch {
	case !ok:
		return "account directory cannot check connection leases"
	case query.Bundle == nil:
		return "check requires a bundle reference"
	case bundle.Status != "qualified":
		return fmt.Sprintf("bundle status is %q, not qualified", bundle.Status)
	case bundle.Model != query.Model:
		return fmt.Sprintf("bundle model %q differs from requested model %q", bundle.Model, query.Model)
	case bundle.Scope.Transport != query.Transport:
		return fmt.Sprintf("bundle transport %q differs from requested transport %q", bundle.Scope.Transport, query.Transport)
	case query.Scope == nil:
		return "check requires the qualified routing scope"
	case !query.Scope.SameOwner(bundle.Scope):
		return codexRoutingScopeChange(bundle.Scope, *query.Scope)
	case query.Scope.Transport != bundle.Scope.Transport:
		return fmt.Sprintf("qualified transport %q differs from bundle transport %q", query.Scope.Transport, bundle.Scope.Transport)
	case query.Scope.ConnectionLeaseID != bundle.Scope.ConnectionLeaseID:
		return fmt.Sprintf("qualified connection lease %q differs from bundle lease %q", query.Scope.ConnectionLeaseID, bundle.Scope.ConnectionLeaseID)
	}
	if err := checker.CheckCodexRoutingLease(ctx, bundle.Scope, bundle.ExpiresAt); err != nil {
		return fmt.Sprintf("connection lease %q: %v", bundle.Scope.ConnectionLeaseID, err)
	}
	return ""
}

func readCodexRoutingBundle(ctx context.Context, store NativeCodexStateStore, key string, ref extensionv1.CodexRoutingBundleRef, scope extensionv1.CodexRoutingScope, qualified bool) (codexRoutingPrivateBundle, error) {
	var bundle codexRoutingPrivateBundle
	now := time.Now()
	switch {
	case store == nil:
		return bundle, codexRoutingUnavailable("routing state store unavailable")
	case !strings.HasPrefix(ref.Key, "bundle.") || len(ref.Key) > 80 || ref.Revision <= 0:
		return bundle, codexRoutingUnavailable("invalid bundle reference %q revision %d", ref.Key, ref.Revision)
	case !now.Before(ref.ExpiresAt):
		return bundle, codexRoutingUnavailable("bundle %s expired at %s", ref.Key, ref.ExpiresAt.UTC().Format(time.RFC3339))
	}
	record, err := store.ReadExtensionState(ctx, key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: ref.Key})
	switch {
	case err != nil:
		return bundle, codexRoutingUnavailable("read bundle %s: %v", ref.Key, err)
	case !record.Found:
		return bundle, codexRoutingUnavailable("bundle %s not found", ref.Key)
	case record.Revision != ref.Revision:
		return bundle, codexRoutingUnavailable("bundle %s is at revision %d, reference names revision %d", ref.Key, record.Revision, ref.Revision)
	}
	if err := json.Unmarshal(record.Value, &bundle); err != nil {
		return bundle, codexRoutingUnavailable("decode bundle %s: %v", ref.Key, err)
	}
	switch {
	case bundle.Schema != extensionv1.CodexRoutingSchema:
		return bundle, codexRoutingUnavailable("bundle %s has schema %d, expected %d", ref.Key, bundle.Schema, extensionv1.CodexRoutingSchema)
	case !scope.SameOwner(bundle.Scope):
		return bundle, codexRoutingUnavailable("bundle %s: %s", ref.Key, codexRoutingScopeChange(bundle.Scope, scope))
	case !now.Before(bundle.ExpiresAt):
		return bundle, codexRoutingUnavailable("bundle %s expired at %s", ref.Key, bundle.ExpiresAt.UTC().Format(time.RFC3339))
	case !ref.ExpiresAt.Equal(bundle.ExpiresAt):
		return bundle, codexRoutingUnavailable("bundle %s expiry %s differs from reference expiry %s", ref.Key, bundle.ExpiresAt.UTC().Format(time.RFC3339), ref.ExpiresAt.UTC().Format(time.RFC3339))
	case ref.ConnectionLeaseID != bundle.Scope.ConnectionLeaseID:
		return bundle, codexRoutingUnavailable("bundle %s is bound to connection lease %q, reference names %q", ref.Key, bundle.Scope.ConnectionLeaseID, ref.ConnectionLeaseID)
	case qualified && bundle.Status != "qualified":
		return bundle, codexRoutingUnavailable("bundle %s status is %q, not qualified", ref.Key, bundle.Status)
	}
	clockRecord, err := store.ReadExtensionState(ctx, key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: bundle.ClockKey})
	var clock codexRoutingCookieClock
	switch {
	case err != nil:
		return bundle, codexRoutingUnavailable("read cookie clock %s: %v", bundle.ClockKey, err)
	case !clockRecord.Found:
		return bundle, codexRoutingUnavailable("cookie clock %s not found", bundle.ClockKey)
	}
	if err := json.Unmarshal(clockRecord.Value, &clock); err != nil {
		return bundle, codexRoutingUnavailable("decode cookie clock %s: %v", bundle.ClockKey, err)
	}
	if !clock.Scope.SameOwner(scope) {
		return bundle, codexRoutingUnavailable("cookie clock %s: %s", bundle.ClockKey, codexRoutingScopeChange(clock.Scope, scope))
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
		switch {
		case !exists:
			return bundle, codexRoutingUnavailable("bundle %s: first-seen record of cookie %s is missing", ref.Key, cookie.Name)
		case !first.Equal(cookie.FirstSeen):
			return bundle, codexRoutingUnavailable("bundle %s: cookie %s first seen at %s, bundle records %s", ref.Key, cookie.Name, first.UTC().Format(time.RFC3339), cookie.FirstSeen.UTC().Format(time.RFC3339))
		case !time.Now().Before(cookie.ExpiresAt):
			return bundle, codexRoutingUnavailable("bundle %s: cookie %s expired at %s", ref.Key, cookie.Name, cookie.ExpiresAt.UTC().Format(time.RFC3339))
		}
		if deleted, exists := clock.Tombstones[cookie.Name]; exists && !cookie.FirstSeen.After(deleted) {
			return bundle, codexRoutingUnavailable("bundle %s: upstream deleted cookie %s at %s", ref.Key, cookie.Name, deleted.UTC().Format(time.RFC3339))
		}
	}
	return bundle, nil
}

func (s *OpenAIGatewayService) PrepareCodexRoutingScope(ctx context.Context, id int64, transport string) (extensionv1.CodexRoutingScope, error) {
	a, err := s.accountRepo.GetByID(ctx, id)
	switch {
	case err != nil:
		return extensionv1.CodexRoutingScope{}, codexRoutingUnavailable("read account %d: %v", id, err)
	case a == nil:
		return extensionv1.CodexRoutingScope{}, codexRoutingUnavailable("account %d not found", id)
	case !isOpenAICodexTicketAccount(a):
		return extensionv1.CodexRoutingScope{}, codexRoutingUnavailable("account %d (%s/%s, shadow=%t) is not an OpenAI OAuth/setup-token Codex account", a.ID, a.Platform, a.Type, a.IsShadow())
	}
	identity, err := resolveCodexOutboundIdentityForAccountContext(ctx, a, s.codexIdentityOverrideUA(a))
	if err != nil {
		return extensionv1.CodexRoutingScope{}, err
	}
	seed, _ := codexFingerprintSeed(a.Extra)
	profile := codexRoutingDigest(identity.userAgent, identity.originator, identity.version, seed, resolveConvergedInstallationID(a, seed), string(a.GetCodexFingerprintMode()), "go-crypto-tls", "native-http1")
	config := s.openAICodexTicketConfig()
	return extensionv1.CodexRoutingScope{AccountID: id, Identity: CodexTicketAccountIdentity(a), ProfileHash: profile, RouteHash: codexRoutingDigest(resolveAccountProxyURL(a), config.HarvestProxyURL), RouteEvidence: "connection_required", Transport: transport, AccountProxyID: qualityProxyID(a)}, nil
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
	if !cfg.Enabled {
		return nil, scope, codexRoutingUnavailable("Codex route acquisition is switched off")
	}
	if !slices.Contains(cfg.Models, query.Model) && !validation {
		return nil, scope, codexRoutingUnavailable("model %q is not in the routing model list %v", query.Model, cfg.Models)
	}
	if query.Transport == "ws" {
		return s.executeCodexRoutingWSProbe(ctx, query, cookies, deadline, scope)
	}
	a, err := s.accountRepo.GetByID(ctx, query.AccountID)
	if err != nil {
		return nil, scope, codexRoutingUnavailable("read account %d: %v", query.AccountID, err)
	}
	token, _, err := s.GetAccessToken(withCodexTicketCredentials(ctx), a)
	if err != nil {
		return nil, scope, fmt.Errorf("%w: %v", errCodexRoutingToken, err)
	}
	request, err := s.buildCodexRoutingProbe(ctx, a, query.Model, token, cookies, query.ReasoningEffort)
	if err != nil {
		return nil, scope, err
	}
	if query.Stage == "acquire" {
		if cfg.HarvestProxyURL == "" {
			return nil, scope, codexRoutingUnavailable("no acquisition proxy is configured")
		}
		response, err := s.doCodexRoutingAcquisition(request, cfg.HarvestProxyURL, a.ID)
		return response, scope, err
	}
	transport, ok := s.httpUpstream.(CodexConnectionLeaseUpstream)
	if !ok {
		return nil, scope, codexRoutingUnavailable("the upstream transport cannot hold a verified connection lease")
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

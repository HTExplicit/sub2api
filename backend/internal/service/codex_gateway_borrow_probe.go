package service

// Adapted from the scoped-cookie and STATE probe protocol in ranxi2001/sub2api,
// commit 5ca3cca21eeaf4ca8a694a7f2f8f0ecd9575c549 (LGPL-3.0), specifically
// backend/internal/service/openai_codex_state_probe.go. These are observation
// requests: no normal response handlers, quota writes, recovery or health hooks.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const codexGatewayBorrowProbeMaxBody = 1 << 20
const codexGatewayBorrowPelicanMaxBody = 32 << 20

type codexGatewayBorrowObservation struct {
	status            int
	state             string
	model             string
	answer            string
	raw               string
	errorText         string
	complete          bool
	incomplete        bool
	modelMismatch     bool
	cookies           []*http.Cookie
	cookiesObservedAt time.Time
}

// accountTemplate reuses the native credential and identity builders without
// entering business forwarding or AccountTest's mutating response handlers.
// Its model is exact; account mappings that select another model cannot lend
// one model's qualification to the requested model.
func (s *CodexGatewayBorrowService) accountTemplate(ctx context.Context, id int64, model string, allowInactive ...bool) (*Account, *http.Request, string, error) {
	ctx = WithCodexGatewayBorrowObservation(ctx)
	if s == nil || s.accounts == nil || s.gateway == nil {
		return nil, nil, "", ErrCodexGatewayBorrowUnavailable
	}
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil || !codexGatewayBorrowAccountSupported(a) {
		return nil, nil, "", fmt.Errorf("account %d is unavailable or not an eligible OAuth-like account", id)
	}
	if a.Status != StatusActive && (len(allowInactive) == 0 || !allowInactive[0]) {
		return nil, nil, "", fmt.Errorf("account %d is inactive", id)
	}
	if !a.IsModelSupported(model) {
		return nil, nil, "", fmt.Errorf("account %d does not allow model %s", id, model)
	}
	actual := normalizeOpenAIModelForUpstream(a, a.GetMappedModel(model))
	if actual != model {
		return nil, nil, "", fmt.Errorf("account %d maps %s to a different model %s", id, model, actual)
	}
	credential, err := resolveCredentialAccount(ctx, s.accounts, a)
	if err != nil {
		return nil, nil, "", err
	}
	// GetAccessToken may perform the existing normal token refresh. It is the
	// only ordinary account write allowed in this observation path.
	token, _, err := s.gateway.GetAccessToken(ctx, a)
	if err != nil {
		return nil, nil, "", err
	}
	if strings.TrimSpace(token) == "" {
		return nil, nil, "", errors.New("account access token is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, nil)
	if err != nil {
		return nil, nil, "", err
	}
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	setOpenAIChatGPTAccountHeaders(req.Header, credential)
	applyOpenAICodexProbeHeaders(req.Header)
	enforceCodexIdentityHeadersForAccount(req.Header, credential, s.gateway.codexIdentityOverrideUA(a))
	setOpenAICodexRoutingHint(req.Header, a, model, "")
	req = req.WithContext(WithCodexGatewayBorrowModel(req.Context(), actual))
	proxy := ""
	// Same resolver used by the native HTTP and WS forwarding paths. A shadow
	// takes its parent's credential, but retains its own selected proxy.
	if a.ProxyID != nil && a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	return a, req, proxy, nil
}

func (s *CodexGatewayBorrowService) acquireSource(ctx context.Context, id int64) (*codexGatewayBorrowCandidate, string, error) {
	a, template, proxy, err := s.accountTemplate(ctx, id, codexGatewayBorrowSourceModel)
	if err != nil {
		return nil, err.Error(), err
	}
	shot, err := s.fireObservation(ctx, template.Header, a, codexGatewayBorrowSourceModel, "Reply with OK only.", "medium", proxy, nil,
		"", nil, HTTPUpstreamProfileCodexBorrowSource, codexGatewayBorrowProbeMaxBody)
	if err != nil {
		return nil, shot.errorText, err
	}
	if shot.status != http.StatusOK || !shot.complete || strings.TrimSpace(shot.answer) == "" || shot.model != codexGatewayBorrowSourceModel || shot.modelMismatch {
		detail := shot.errorText
		if detail == "" {
			detail = fmt.Sprintf("source response is unqualified: HTTP %d, completed=%t, reported model=%q, visible text=%t", shot.status, shot.complete, shot.model, strings.TrimSpace(shot.answer) != "")
		}
		return nil, detail, errors.New("source_response_not_qualified")
	}
	candidate := borrowCandidateFromCookies(shot.cookies, "/backend-api/codex/responses", shot.cookiesObservedAt)
	if candidate == nil {
		return nil, "routing cookie missing, invalid, deleted or expired", errors.New("source_routing_cookie_unavailable")
	}
	candidate.sourceID = id
	return candidate, "", nil
}

func borrowCookieScoped(cookie *http.Cookie, path string) bool {
	if cookie == nil || (cookie.Domain != "" && !strings.EqualFold(strings.TrimPrefix(cookie.Domain, "."), "chatgpt.com")) {
		return false
	}
	scope := cookie.Path
	if scope == "" {
		scope = path[:strings.LastIndex(path, "/")]
	}
	return strings.HasPrefix(scope, "/") && borrowCookiePathMatches(path, scope)
}

func borrowCookieDeleted(cookie *http.Cookie, now time.Time) bool {
	return cookie.Value == "" || cookie.MaxAge < 0 || (cookie.MaxAge == 0 && !cookie.Expires.IsZero() && !now.Before(cookie.Expires))
}

func borrowCandidateFromCookies(cookies []*http.Cookie, path string, now time.Time) *codexGatewayBorrowCandidate {
	var candidate *codexGatewayBorrowCandidate
	for _, cookie := range cookies {
		if cookie.Name != "__oailb" || !cookie.Secure || !borrowCookieScoped(cookie, path) {
			continue
		}
		// Deletion applies even after an otherwise usable Set-Cookie line.
		if borrowCookieDeleted(cookie, now) {
			return nil
		}
		expires := now.Add(codexGatewayBorrowTTL)
		if cookie.MaxAge > 0 {
			if cookie.MaxAge < int(codexGatewayBorrowTTL/time.Second) {
				expires = now.Add(time.Duration(cookie.MaxAge) * time.Second)
			}
		} else {
			if !cookie.Expires.IsZero() && cookie.Expires.Before(expires) {
				expires = cookie.Expires
			}
		}
		if !now.Before(expires) {
			continue
		}
		copy := *cookie
		if copy.Path == "" {
			copy.Path = path[:strings.LastIndex(path, "/")]
		}
		candidate = &codexGatewayBorrowCandidate{cookie: copy, expires: expires}
	}
	return candidate
}

func (s *CodexGatewayBorrowService) probeTarget(ctx context.Context, template *http.Request, account *Account, model, proxy string, profile *tlsfingerprint.Profile, candidate *codexGatewayBorrowCandidate) CodexGatewayBorrowVerification {
	expires := candidate.expires
	result := CodexGatewayBorrowVerification{AccountID: account.ID, Model: model, CheckedAt: time.Now(), ExpiresAt: &expires, Reason: "target_probe_failed"}
	cookies := []*http.Cookie{{Name: "__oailb", Value: candidate.cookie.Value}}
	fire := func(state string, cookies []*http.Cookie) (codexGatewayBorrowObservation, error) {
		shotCtx, cancel := context.WithTimeout(ctx, codexGatewayBorrowShotTimeout)
		defer cancel()
		return s.fireObservation(shotCtx, template.Header, account, model, "Reply with OK.", "", proxy, profile, state, cookies, HTTPUpstreamProfileCodexBorrowTarget, codexGatewayBorrowProbeMaxBody)
	}
	mint, err := fire("", cookies)
	result.MintStatus, result.ReportedModel = mint.status, mint.model
	if err != nil || !borrowShotUsable(mint) {
		result.Error = borrowObservationError(mint, err)
		return result
	}
	if borrowTargetRouteChanged(mint.cookies, candidate, time.Now()) {
		result.Reason, result.Error = "target_route_changed", "target response changed or deleted the fixed __oailb route"
		return result
	}
	if mint.state == "" {
		result.Reason, result.Error = "target_state_missing", "first response did not return x-codex-turn-state"
		return result
	}
	result.Minted = true
	// Only the target's newly returned __cflb travels into the second shot.
	// __oailb remains the exact borrowed candidate, never a target replacement.
	for _, cookie := range mint.cookies {
		if cookie.Name == "__cflb" && borrowCookieScoped(cookie, "/backend-api/codex/responses") && !borrowCookieDeleted(cookie, time.Now()) {
			cookies = append(cookies, &http.Cookie{Name: cookie.Name, Value: cookie.Value})
			break
		}
	}
	continued, err := fire(mint.state, cookies)
	result.ContinueStatus = continued.status
	if continued.model != "" {
		result.ReportedModel = continued.model
	}
	if err != nil || !borrowShotUsable(continued) {
		result.Error = borrowObservationError(continued, err)
		return result
	}
	if borrowTargetRouteChanged(continued.cookies, candidate, time.Now()) {
		result.Reason, result.Error = "target_route_changed", "target response changed or deleted the fixed __oailb route"
		return result
	}
	result.NewTicket = continued.state != "" && continued.state != mint.state
	if result.NewTicket {
		result.Reason = "target_state_changed"
		return result
	}
	result.Success, result.Reason = true, "target_probe_passed"
	return result
}

func borrowShotUsable(shot codexGatewayBorrowObservation) bool {
	return shot.status == http.StatusOK && shot.complete && shot.errorText == ""
}

func borrowObservationError(shot codexGatewayBorrowObservation, err error) string {
	if shot.errorText != "" {
		return shot.errorText
	}
	if err != nil {
		return err.Error()
	}
	if shot.status != http.StatusOK {
		return fmt.Sprintf("API returned %d: %s", shot.status, shot.raw)
	}
	if !shot.complete {
		return "upstream SSE ended without a completed response"
	}
	return "completed response contained no visible output text"
}

func borrowTargetRouteChanged(cookies []*http.Cookie, candidate *codexGatewayBorrowCandidate, now time.Time) bool {
	for _, cookie := range cookies {
		if cookie.Name == "__oailb" && borrowCookieScoped(cookie, "/backend-api/codex/responses") && (cookie.Value != candidate.cookie.Value || borrowCookieDeleted(cookie, now)) {
			return true
		}
	}
	return false
}

func borrowObservationPayload(model, prompt, effort string) []byte {
	body := map[string]any{"model": model, "instructions": prompt,
		"input":  []map[string]any{{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}}}},
		"stream": true, "store": false, "parallel_tool_calls": true, "include": []string{"reasoning.encrypted_content"}}
	if effort != "" {
		body["reasoning"] = map[string]string{"effort": effort}
	}
	raw, _ := json.Marshal(body)
	return raw
}

func borrowObservationRequest(ctx context.Context, headers http.Header, model, prompt, effort, state string, cookies []*http.Cookie, purpose HTTPUpstreamProfile) (*http.Request, error) {
	ctx = WithCodexGatewayBorrowObservation(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(ctx, purpose)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, bytes.NewReader(borrowObservationPayload(model, prompt, effort)))
	if err != nil {
		return nil, err
	}
	req.Host, req.Close, req.Header = "chatgpt.com", true, headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for _, name := range []string{"Cookie", "Content-Length", "Content-Encoding", "Content-MD5", "X-Codex-Turn-State", "X-Codex-Turn-Metadata", "X-OpenAI-Internal-Codex-Responses-Lite", "conversation_id", "session_id", "session-id", "thread-id", "x-client-request-id", "x-codex-window-id"} {
		req.Header.Del(name)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	ensureCodexSessionIdentityHeaders(req.Header, uuid.NewString())
	if state != "" {
		req.Header.Set("X-Codex-Turn-State", state)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	return req, nil
}

func (s *CodexGatewayBorrowService) fireObservation(ctx context.Context, headers http.Header, account *Account, model, prompt, effort, proxy string, profile *tlsfingerprint.Profile, state string, cookies []*http.Cookie, purpose HTTPUpstreamProfile, maxBody int64) (codexGatewayBorrowObservation, error) {
	var observation codexGatewayBorrowObservation
	if s.upstream == nil {
		return observation, ErrCodexGatewayBorrowUnavailable
	}
	req, err := borrowObservationRequest(ctx, headers, model, prompt, effort, state, cookies, purpose)
	if err != nil {
		return observation, err
	}
	return s.sendObservation(req, account, proxy, profile, model, maxBody)
}

func (s *CodexGatewayBorrowService) sendObservation(req *http.Request, account *Account, proxy string, profile *tlsfingerprint.Profile, expectedModel string, maxBody int64) (codexGatewayBorrowObservation, error) {
	var shot codexGatewayBorrowObservation
	leasedCtx, release, acquireErr := AcquirePelicanExecution(req.Context(), account.ID)
	if acquireErr != nil {
		shot.errorText = acquireErr.Error()
		return shot, acquireErr
	}
	defer release()
	req = req.WithContext(leasedCtx)
	if pelicanExecutionFromContext(leasedCtx) != nil {
		var capacity *ConcurrencyService
		if capture := pelicanCaptureFromContext(leasedCtx); capture != nil {
			capacity = capture.concurrency
		} else if s.gateway != nil {
			capacity = s.gateway.concurrencyService
		}
		releaseCapacity, capacityErr := acquirePelicanBusinessAccountSlot(leasedCtx, capacity, account.ID, account.Concurrency)
		if capacityErr != nil {
			shot.errorText = capacityErr.Error()
			return shot, capacityErr
		}
		defer releaseCapacity()
	}
	wire, err := prepareOpenAICodexWireRequest(req, account)
	if err != nil {
		return shot, err
	}
	resp, err := s.upstream.DoWithTLS(wire, proxy, account.ID, account.Concurrency, profile)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		shot.errorText = err.Error()
		return shot, err
	}
	if resp == nil {
		return shot, errors.New("nil upstream observation response")
	}
	shot.status, shot.state, shot.cookies, shot.cookiesObservedAt = resp.StatusCode, strings.TrimSpace(extractOpenAICodexTurnState(resp.Header)), resp.Cookies(), time.Now()
	if resp.Body == nil {
		shot.errorText = "upstream observation response has no body"
		return shot, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var data []byte
	var readErr error
	if shot.status == http.StatusOK {
		data, readErr = readCodexGatewayBorrowSSE(resp.Body, maxBody)
	} else {
		data, readErr = io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	}
	shot.raw = string(data)
	if shot.status == http.StatusOK {
		stream := parseCodexGatewayBorrowSSE(data, expectedModel, maxBody)
		shot.model, shot.answer, shot.complete, shot.modelMismatch, shot.errorText, shot.incomplete = stream.model, stream.answer, stream.complete, stream.modelMismatch, stream.errorText, stream.incomplete
	}
	if readErr != nil {
		shot.complete, shot.incomplete = false, true
		if shot.errorText != "" {
			shot.errorText += "\n"
		}
		shot.errorText += readErr.Error()
		return shot, readErr
	}
	if int64(len(data)) > maxBody {
		shot.complete, shot.incomplete, shot.errorText = false, true, fmt.Sprintf("observation response exceeded %d bytes", maxBody)
		return shot, errors.New(shot.errorText)
	}
	if shot.status != http.StatusOK {
		shot.errorText = fmt.Sprintf("API returned %d: %s", shot.status, shot.raw)
		return shot, nil
	}
	return shot, nil
}

// A Responses terminal event is authoritative even when the HTTP connection
// remains open. Collect the original bytes, without waiting for EOF or reading
// beyond that terminal payload; sendObservation closes the body immediately.
// The existing pure parser still decides completion, text, models and errors.
func readCodexGatewayBorrowSSE(body io.Reader, maxBody int64) ([]byte, error) {
	reader := bufio.NewReader(io.LimitReader(body, maxBody+1))
	var raw bytes.Buffer
	var lines []string
	terminal := func() bool {
		payload := strings.TrimSpace(strings.Join(lines, "\n"))
		if !gjson.Valid(payload) {
			return false
		}
		switch gjson.Get(payload, "type").String() {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
			return true
		default:
			return false
		}
	}
	for {
		line, err := reader.ReadString('\n')
		_, _ = raw.WriteString(line)
		if int64(raw.Len()) > maxBody {
			return raw.Bytes(), nil // The caller reports the unchanged body bound.
		}
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if text == "" {
			lines = nil
		} else if strings.HasPrefix(text, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(text, "data:"), " "))
			if terminal() {
				// Preserve an already-received blank event delimiter byte for
				// byte. Never block waiting for a delimiter or subsequent EOF.
				if buffered := reader.Buffered(); buffered > 0 {
					remaining, _ := reader.Peek(buffered)
					length := 0
					if remaining[0] == '\n' {
						length = 1
					} else if len(remaining) >= 2 && remaining[0] == '\r' && remaining[1] == '\n' {
						length = 2
					}
					_, _ = raw.Write(remaining[:length])
					_, _ = reader.Discard(length)
				}
				return raw.Bytes(), nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return raw.Bytes(), nil
			}
			return raw.Bytes(), err
		}
	}
}

// Parsing is local and records full error payloads. A bare [DONE], malformed
// event, premature EOF, cancellation or failed/incomplete response cannot be
// mistaken for a completed observation. Source and manual-generation callers
// separately require a visible answer; the target STATE criterion does not.
func parseCodexGatewayBorrowSSE(data []byte, expectedModel string, maxEvent int64) codexGatewayBorrowObservation {
	var out codexGatewayBorrowObservation
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), int(maxEvent)+1)
	var lines []string
	var deltas strings.Builder
	var terminal bool
	flush := func() {
		if len(lines) == 0 {
			return
		}
		payload := strings.TrimSpace(strings.Join(lines, "\n"))
		lines = nil
		if payload == "" || payload == "[DONE]" {
			return
		}
		if !gjson.Valid(payload) {
			out.errorText = "malformed upstream SSE data: " + payload
			return
		}
		event := gjson.Parse(payload)
		model := event.Get("response.model").String()
		if model != "" {
			out.model = model
			if model != expectedModel {
				out.modelMismatch = true
			}
		}
		switch event.Get("type").String() {
		case "response.output_text.delta":
			_, _ = deltas.WriteString(event.Get("delta").String())
		case "error", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			out.errorText = payload
			out.incomplete = event.Get("type").String() == "response.incomplete"
			terminal = true
		case "response.completed", "response.done":
			if terminal {
				out.errorText = "multiple upstream terminal events: " + payload
				return
			}
			terminal = true
			// The source requires the terminal response's explicit model, not
			// a model advertised only by an earlier response.created event.
			out.model = model
			if event.Get("response.status").String() != "completed" || (event.Get("response.error").Exists() && event.Get("response.error").Type != gjson.Null) {
				out.errorText = payload
				return
			}
			var final strings.Builder
			for _, item := range event.Get("response.output").Array() {
				for _, content := range item.Get("content").Array() {
					if content.Get("type").String() == "output_text" {
						_, _ = final.WriteString(content.Get("text").String())
					}
				}
			}
			out.answer = final.String()
			if out.answer == "" {
				out.answer = deltas.String()
			}
			out.complete = true
		}
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		out.errorText = err.Error()
	}
	if out.answer == "" {
		out.answer = deltas.String()
	}
	if out.errorText != "" {
		out.complete = false
	}
	if !terminal && out.errorText == "" {
		out.incomplete = true
	}
	return out
}

package service

import (
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const codexRoutingTurnContextKey = "codex_routing_turn_id"

type codexRoutingIngressKey struct{}

//go:embed codex_routing_reference.json
var codexRoutingReference json.RawMessage

// A turn identifier is independent of the conversation/session. Opaque state
// cannot cross a turn simply because the same downstream session continues.
func stageCodexRoutingTurn(c *gin.Context, body []byte) {
	stageCodexRoutingTurnWithHeader(c, body, true)
}

// A persistent WS handshake header describes the initial request only. Each
// frame needs its own explicit turn identity before opaque state may be reused.
func stageCodexRoutingWSTurn(c *gin.Context, body []byte) {
	stageCodexRoutingTurnWithHeader(c, body, false)
}

func stageCodexRoutingTurnWithHeader(c *gin.Context, body []byte, allowHeader bool) {
	if c == nil {
		return
	}
	turn := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.turn_id").String())
	if turn == "" {
		metadata := gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()
		turn = strings.TrimSpace(gjson.Get(metadata, "turn_id").String())
	}
	if turn == "" && allowHeader && c.Request != nil {
		turn = strings.TrimSpace(gjson.Get(c.Request.Header.Get(openAIWSTurnMetadataHeader), "turn_id").String())
	}
	if len(turn) > 128 {
		turn = ""
	}
	c.Set(codexRoutingTurnContextKey, turn)
}

func codexRoutingTurnID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, exists := c.Get(codexRoutingTurnContextKey); exists {
		text, _ := value.(string)
		return text
	}
	if c.Request == nil {
		return ""
	}
	turn := strings.TrimSpace(gjson.Get(c.Request.Header.Get(openAIWSTurnMetadataHeader), "turn_id").String())
	if len(turn) > 128 {
		return ""
	}
	return turn
}

// CodexWireCookie is one cookie of the final outbound request, exactly as sent.
type CodexWireCookie struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Routing bool   `json:"routing"`
}

// codexWireCookies splits the Cookie headers as sent, without dropping pairs
// that the stricter net/http parser would reject.
func codexWireCookies(headers http.Header) []CodexWireCookie {
	cookies := []CodexWireCookie{}
	for _, line := range headers.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, value, _ := strings.Cut(part, "=")
			cookies = append(cookies, CodexWireCookie{Name: name, Value: value, Routing: isCodexRoutingCookie(name)})
		}
	}
	return cookies
}

type CodexWireFingerprint struct {
	IdentityFields      map[string]CodexIdentityFieldObservation `json:"identity_fields"`
	Ingress             string                                   `json:"ingress"`
	ObservedAt          time.Time                                `json:"observed_at"`
	Source              string                                   `json:"source"`
	Transport           string                                   `json:"transport"`
	UserAgent           string                                   `json:"user_agent"`
	Originator          string                                   `json:"originator"`
	Version             string                                   `json:"version"`
	HTTPProtocol        string                                   `json:"http_protocol"`
	RequestEncoding     string                                   `json:"request_encoding"`
	TLSImplementation   string                                   `json:"tls_implementation"`
	TLSVersion          string                                   `json:"tls_version,omitempty"`
	CipherSuite         string                                   `json:"cipher_suite,omitempty"`
	ALPN                string                                   `json:"alpn,omitempty"`
	JA3                 string                                   `json:"ja3_status"`
	ProfileHash         string                                   `json:"profile_hash"`
	RouteHash           string                                   `json:"route_hash"`
	RouteEvidence       string                                   `json:"route_evidence"`
	ConnectionEvidence  string                                   `json:"connection_evidence"`
	ConnectionLeaseID   string                                   `json:"connection_lease_id,omitempty"`
	AccountProxyID      int64                                    `json:"account_proxy_id,omitempty"`
	Cookies             []CodexWireCookie                        `json:"cookies"`
	CookieNames         []string                                 `json:"cookie_names"`
	CookieVersions      map[string]string                        `json:"cookie_versions"`
	SessionPresent      bool                                     `json:"session_present"`
	ThreadPresent       bool                                     `json:"thread_present"`
	WindowPresent       bool                                     `json:"window_present"`
	StatePresent        bool                                     `json:"state_present"`
	StateLength         int                                      `json:"state_length"`
	State               string                                   `json:"state,omitempty"`
	WebSocketExtensions string                                   `json:"websocket_extensions,omitempty"`
}

// CodexRoutingModelStatus shows the stored routing record of one model as is,
// including a record written for a previous credential owner.
type CodexRoutingModelStatus struct {
	Model               string                                 `json:"model"`
	Phase               string                                 `json:"phase"`
	Enrolled            bool                                   `json:"enrolled"`
	Failures            int                                    `json:"failed_cycles"`
	Qualified           bool                                   `json:"qualified"`
	VerifiedAt          *time.Time                             `json:"verified_at,omitempty"`
	ExpiresAt           *time.Time                             `json:"expires_at,omitempty"`
	NextAt              *time.Time                             `json:"next_attempt_at,omitempty"`
	LastObservation     *extensionv1.CodexRoutingObservation   `json:"last_observation,omitempty"`
	Schema              int                                    `json:"schema,omitempty"`
	StateRevision       int64                                  `json:"state_revision,omitempty"`
	StateError          string                                 `json:"state_error,omitempty"`
	RecordedIdentity    string                                 `json:"recorded_identity,omitempty"`
	IdentityMatches     bool                                   `json:"identity_matches"`
	LastAttemptAt       *time.Time                             `json:"last_attempt_at,omitempty"`
	OperationID         string                                 `json:"operation_id,omitempty"`
	ManualWasEnrolled   bool                                   `json:"manual_was_enrolled"`
	LastCode            string                                 `json:"last_code,omitempty"`
	RevokedAt           *time.Time                             `json:"revoked_at,omitempty"`
	RevocationReason    string                                 `json:"revocation_reason,omitempty"`
	Qualification       *extensionv1.CodexRoutingQualification `json:"qualification,omitempty"`
	RouteMatchesCurrent bool                                   `json:"route_matches_current"`
}

// CodexFingerprintProxy identifies the account proxy that enters RouteHash.
type CodexFingerprintProxy struct {
	ID       int64  `json:"id"`
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Status   string `json:"status,omitempty"`
}

type CodexFingerprintView struct {
	ConfiguredProfile   *extensionv1.CodexClientProfile `json:"configured_profile,omitempty"`
	CapturedReference   json.RawMessage                 `json:"captured_reference"`
	AccountID           int64                           `json:"account_id"`
	Configured          codexIdentitySnapshot           `json:"configured"`
	Observed            *CodexWireFingerprint           `json:"observed,omitempty"`
	ObservedError       string                          `json:"observed_error,omitempty"`
	ReferenceCommit     string                          `json:"reference_commit"`
	ReferenceSource     string                          `json:"reference_source"`
	Alignment           string                          `json:"alignment"`
	CookieMaxAgeSeconds int                             `json:"cookie_max_age_seconds"`
	RefreshLeadSeconds  int                             `json:"refresh_lead_seconds"`
	RoutingEnabled      bool                            `json:"routing_enabled"`
	FailClosed          bool                            `json:"fail_closed"`
	CurrentScope        *extensionv1.CodexRoutingScope  `json:"current_scope,omitempty"`
	ScopeError          string                          `json:"scope_error,omitempty"`
	AccountProxy        *CodexFingerprintProxy          `json:"account_proxy,omitempty"`
	HarvestProxyURL     string                          `json:"harvest_proxy_url,omitempty"`
	Models              []CodexRoutingModelStatus       `json:"models"`
}

func (s *OpenAIGatewayService) observeCodexWire(ctx context.Context, account *Account, request *http.Request, response *http.Response, qualification *extensionv1.CodexRoutingQualification) {
	if s == nil || s.nativeCodexRuntime == nil || account == nil || request == nil || response == nil {
		return
	}
	installation := s.nativeCodexRuntime.metadata()
	store := s.nativeCodexRuntime.repo
	ok := store != nil
	if installation == nil || !ok {
		return
	}
	value := CodexWireFingerprint{ObservedAt: time.Now().UTC(), Source: "final_outbound", Transport: "http", UserAgent: request.Header.Get("User-Agent"), Originator: request.Header.Get("originator"), Version: request.Header.Get("version"), HTTPProtocol: response.Proto, RequestEncoding: request.Header.Get("Content-Encoding"), TLSImplementation: "go-crypto-tls", JA3: "unknown", CookieNames: []string{}, SessionPresent: extractClientSessionID(request.Header) != "", ThreadPresent: request.Header.Get("thread-id") != "", WindowPresent: request.Header.Get("x-codex-window-id") != "", StatePresent: request.Header.Get(openAICodexTurnStateHeader) != "", StateLength: len(request.Header.Get(openAICodexTurnStateHeader))}
	value.Ingress = "http"
	value.CookieVersions = map[string]string{}
	value.Cookies = codexWireCookies(request.Header)
	value.State = request.Header.Get(openAICodexTurnStateHeader)
	value.AccountProxyID = qualityProxyID(account)
	body, carried := request.Context().Value(codexIdentityBodyKey{}).(codexIdentityBodyObservation)
	if !carried {
		// Only a rewritten (zstd) wire copy carries a pre-encoding observation;
		// an unrewritten request still exposes its plaintext replay body.
		body = inspectCodexIdentityBody(request)
	}
	value.IdentityFields = codexIdentityFieldObservations(request.Header, body)
	if ingress, ok := request.Context().Value(codexRoutingIngressKey{}).(string); ok && ingress == "ws" {
		value.Ingress = ingress
	}
	if response.TLS != nil {
		value.TLSVersion = tls.VersionName(response.TLS.Version)
		value.CipherSuite = tls.CipherSuiteName(response.TLS.CipherSuite)
		value.ALPN = response.TLS.NegotiatedProtocol
	}
	if qualification != nil {
		value.ProfileHash, value.RouteHash, value.RouteEvidence = qualification.Scope.ProfileHash, qualification.Scope.RouteHash, qualification.Scope.RouteEvidence
		value.ConnectionEvidence = codexRoutingDigest(qualification.Scope.ConnectionLeaseID)[:16]
		value.ConnectionLeaseID = qualification.Scope.ConnectionLeaseID
		value.Transport = qualification.Scope.Transport
	}
	if value.Transport == "ws" {
		value.WebSocketExtensions = response.Header.Get("Sec-WebSocket-Extensions")
	}
	for _, cookie := range request.Cookies() {
		if isCodexRoutingCookie(cookie.Name) {
			value.CookieNames = append(value.CookieNames, cookie.Name)
			value.CookieVersions[cookie.Name] = codexRoutingDigest(strconv.FormatInt(account.ID, 10), cookie.Name, cookie.Value)[:16]
		}
	}
	ctx = WithNativeCodexExecution(context.WithoutCancel(ctx), installation)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	key := "wire." + strconv.FormatInt(account.ID, 10)
	previous, err := store.ReadExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key})
	if err != nil {
		return
	}
	raw, _ := json.Marshal(value)
	_, _ = store.CompareSwapExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, ExpectedRevision: previous.Revision, Value: raw})
}

// CodexFingerprint is read-only: opening an account never refreshes OAuth,
// acquires cookies, pings a proxy, or sends a model request.
func (s *OpenAIGatewayService) CodexFingerprint(ctx context.Context, id int64) (*CodexFingerprintView, error) {
	if s.nativeCodexRuntime == nil {
		return nil, codexRoutingUnavailable("native Codex runtime is not configured")
	}
	a, err := s.accountRepo.GetByID(ctx, id)
	switch {
	case err != nil:
		return nil, codexRoutingUnavailable("read account %d: %v", id, err)
	case !isOpenAICodexTicketAccount(a):
		return nil, codexRoutingUnavailable("account %d is not an OpenAI OAuth/setup-token Codex account", id)
	}
	installation := s.nativeCodexRuntime.metadata()
	store := s.nativeCodexRuntime.repo
	ok := store != nil
	if installation == nil || !ok {
		return nil, codexRoutingUnavailable("native Codex runtime is not loaded")
	}
	cfg := s.openAICodexTicketConfig()
	view := &CodexFingerprintView{AccountID: id, Configured: resolveCodexIdentitySnapshotContext(ctx, a, a, codexAccountIdentityOverrideUA(a)), ReferenceCommit: "4b664e0ef0397f82e68c60088a90fcd035deb796", ReferenceSource: "official_source", Alignment: "application_fields_checked_tls_capture_unknown", CookieMaxAgeSeconds: 120, RefreshLeadSeconds: 20, RoutingEnabled: cfg.Enabled, FailClosed: cfg.FailClosed, HarvestProxyURL: cfg.HarvestProxyURL, Models: []CodexRoutingModelStatus{}}
	currentScope, scopeErr := s.PrepareCodexRoutingScope(ctx, id, "http")
	if scopeErr != nil {
		view.ScopeError = scopeErr.Error()
	} else {
		view.CurrentScope = &currentScope
	}
	if a.ProxyID != nil {
		proxy := &CodexFingerprintProxy{ID: *a.ProxyID}
		if a.Proxy != nil {
			proxy.Name, proxy.Protocol, proxy.Host, proxy.Port, proxy.Username, proxy.Status = a.Proxy.Name, a.Proxy.Protocol, a.Proxy.Host, a.Proxy.Port, a.Proxy.Username, a.Proxy.Status
		}
		view.AccountProxy = proxy
	}
	wire, err := store.ReadExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "wire." + strconv.FormatInt(id, 10)})
	if err != nil {
		return nil, fmt.Errorf("read the last outbound observation of account %d: %w", id, err)
	}
	if wire.Found {
		var observation CodexWireFingerprint
		if err := json.Unmarshal(wire.Value, &observation); err != nil {
			view.ObservedError = "decode the last outbound observation: " + err.Error()
		} else {
			view.Observed = &observation
		}
	}
	for _, model := range cfg.Models {
		status := CodexRoutingModelStatus{Model: model, Phase: "idle"}
		record, readErr := store.ReadExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: "tickets", Key: strconv.FormatInt(id, 10) + "." + codexRoutingDigest(model)})
		if readErr != nil {
			return nil, fmt.Errorf("read the routing record of %s: %w", model, readErr)
		}
		if record.Found {
			var state struct {
				Schema            int                                    `json:"schema"`
				Identity          string                                 `json:"identity"`
				Phase             string                                 `json:"phase"`
				Enrolled          bool                                   `json:"enrolled"`
				Failures          int                                    `json:"failures"`
				ExpiresAt         *time.Time                             `json:"expires_at"`
				NextAt            *time.Time                             `json:"next_attempt_at"`
				LastAttemptAt     *time.Time                             `json:"last_attempt_at"`
				OperationID       string                                 `json:"operation_id"`
				ManualWasEnrolled bool                                   `json:"manual_was_enrolled"`
				LastCode          string                                 `json:"last_code"`
				RevokedAt         *time.Time                             `json:"revoked_at"`
				RevocationReason  string                                 `json:"revocation_reason"`
				Qualification     *extensionv1.CodexRoutingQualification `json:"qualification"`
				Observation       *extensionv1.CodexRoutingObservation   `json:"observation"`
			}
			status.StateRevision = record.Revision
			if err := json.Unmarshal(record.Value, &state); err != nil {
				status.StateError = err.Error()
			} else {
				status.RecordedIdentity, status.IdentityMatches, status.Schema = state.Identity, state.Identity == CodexTicketAccountIdentity(a), state.Schema
				status.Phase, status.Enrolled, status.Failures, status.ExpiresAt, status.NextAt, status.LastObservation = state.Phase, state.Enrolled, state.Failures, state.ExpiresAt, state.NextAt, state.Observation
				status.LastAttemptAt, status.OperationID, status.ManualWasEnrolled, status.LastCode, status.Qualification = state.LastAttemptAt, state.OperationID, state.ManualWasEnrolled, state.LastCode, state.Qualification
				status.RevokedAt, status.RevocationReason = state.RevokedAt, state.RevocationReason
				status.Qualified = status.IdentityMatches && scopeErr == nil && state.Schema == extensionv1.CodexRoutingSchema && state.Qualification.Valid(time.Now(), id, state.Identity, model) && state.Qualification.Scope.SameOwner(currentScope)
				if state.Qualification != nil {
					status.RouteMatchesCurrent = scopeErr == nil && state.Qualification.Scope.RouteHash == currentScope.RouteHash
					if state.Qualification.Model == model && !state.Qualification.VerifiedAt.IsZero() {
						verified := state.Qualification.VerifiedAt
						status.VerifiedAt = &verified
					}
				}
				if state.Schema < extensionv1.CodexRoutingSchema && (state.Phase == "ready" || state.Phase == "retry") {
					status.Phase = "needs_cookie_verification"
				}
			}
		}
		view.Models = append(view.Models, status)
	}
	if profile, ok := a.codexClientIdentityContext(ctx); ok {
		public := extensionv1.CodexClientProfile(profile)
		view.ConfiguredProfile = &public
	}
	view.Alignment = "application_reference_available_transport_unverified"
	view.CapturedReference = append(json.RawMessage(nil), codexRoutingReference...)
	return view, nil
}

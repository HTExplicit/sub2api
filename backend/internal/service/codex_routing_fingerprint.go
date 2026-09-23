package service

import (
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
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
	if c == nil {
		return
	}
	turn := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.turn_id").String())
	if turn == "" {
		metadata := gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()
		turn = strings.TrimSpace(gjson.Get(metadata, "turn_id").String())
	}
	if turn == "" && c.Request != nil {
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
	CookieNames         []string                                 `json:"cookie_names"`
	CookieVersions      map[string]string                        `json:"cookie_versions"`
	SessionPresent      bool                                     `json:"session_present"`
	ThreadPresent       bool                                     `json:"thread_present"`
	WindowPresent       bool                                     `json:"window_present"`
	StatePresent        bool                                     `json:"state_present"`
	StateLength         int                                      `json:"state_length"`
	WebSocketExtensions string                                   `json:"websocket_extensions,omitempty"`
}

type CodexRoutingModelStatus struct {
	Model           string                               `json:"model"`
	Phase           string                               `json:"phase"`
	Enrolled        bool                                 `json:"enrolled"`
	Failures        int                                  `json:"failed_cycles"`
	Qualified       bool                                 `json:"qualified"`
	VerifiedAt      *time.Time                           `json:"verified_at,omitempty"`
	ExpiresAt       *time.Time                           `json:"expires_at,omitempty"`
	NextAt          *time.Time                           `json:"next_attempt_at,omitempty"`
	LastObservation *extensionv1.CodexRoutingObservation `json:"last_observation,omitempty"`
}

type CodexFingerprintView struct {
	ConfiguredProfile   *extensionv1.CodexClientProfile `json:"configured_profile,omitempty"`
	CapturedReference   json.RawMessage                 `json:"captured_reference"`
	AccountID           int64                           `json:"account_id"`
	Configured          codexIdentitySnapshot           `json:"configured"`
	Observed            *CodexWireFingerprint           `json:"observed,omitempty"`
	ReferenceCommit     string                          `json:"reference_commit"`
	ReferenceSource     string                          `json:"reference_source"`
	Alignment           string                          `json:"alignment"`
	CookieMaxAgeSeconds int                             `json:"cookie_max_age_seconds"`
	RefreshLeadSeconds  int                             `json:"refresh_lead_seconds"`
	Models              []CodexRoutingModelStatus       `json:"models"`
}

func (s *OpenAIGatewayService) observeCodexWire(ctx context.Context, account *Account, request *http.Request, response *http.Response, qualification *extensionv1.CodexRoutingQualification) {
	if s == nil || s.pluginManager == nil || account == nil || request == nil || response == nil {
		return
	}
	installation, _ := s.pluginManager.installedByKey(codexRuntimePluginKey)
	store, ok := s.pluginManager.repo.(PluginExtensionStateStore)
	if installation == nil || !ok {
		return
	}
	value := CodexWireFingerprint{ObservedAt: time.Now().UTC(), Source: "final_outbound", Transport: "http", UserAgent: request.Header.Get("User-Agent"), Originator: request.Header.Get("originator"), Version: request.Header.Get("version"), HTTPProtocol: response.Proto, RequestEncoding: request.Header.Get("Content-Encoding"), TLSImplementation: "go-crypto-tls", JA3: "unknown", CookieNames: []string{}, SessionPresent: extractClientSessionID(request.Header) != "", ThreadPresent: request.Header.Get("thread-id") != "", WindowPresent: request.Header.Get("x-codex-window-id") != "", StatePresent: request.Header.Get(openAICodexTurnStateHeader) != "", StateLength: len(request.Header.Get(openAICodexTurnStateHeader))}
	value.Ingress = "http"
	value.CookieVersions = map[string]string{}
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
	ctx = WithPluginExecution(context.WithoutCancel(ctx), installation)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	key := "wire." + strconv.FormatInt(account.ID, 10)
	previous, err := store.ReadExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key})
	if err != nil {
		return
	}
	raw, _ := json.Marshal(value)
	_, _ = store.CompareSwapExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, ExpectedRevision: previous.Revision, Value: raw})
}

// CodexFingerprint is read-only: opening an account never refreshes OAuth,
// acquires cookies, pings a proxy, or sends a model request.
func (s *OpenAIGatewayService) CodexFingerprint(ctx context.Context, id int64) (*CodexFingerprintView, error) {
	a, err := s.accountRepo.GetByID(ctx, id)
	if err != nil || !isOpenAICodexTicketAccount(a) || s.pluginManager == nil {
		return nil, errCodexRoutingUnavailable
	}
	installation, _ := s.pluginManager.installedByKey(codexRuntimePluginKey)
	store, ok := s.pluginManager.repo.(PluginExtensionStateStore)
	if installation == nil || !ok {
		return nil, errCodexRoutingUnavailable
	}
	view := &CodexFingerprintView{AccountID: id, Configured: resolveCodexIdentitySnapshotContext(ctx, a, a, codexAccountIdentityOverrideUA(a)), ReferenceCommit: "4b664e0ef0397f82e68c60088a90fcd035deb796", ReferenceSource: "official_source", Alignment: "application_fields_checked_tls_capture_unknown", CookieMaxAgeSeconds: 120, RefreshLeadSeconds: 20, Models: []CodexRoutingModelStatus{}}
	currentScope, scopeErr := s.PrepareCodexRoutingScope(ctx, id, "http")
	wire, err := store.ReadExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "wire." + strconv.FormatInt(id, 10)})
	if err != nil {
		return nil, err
	}
	if wire.Found {
		var observation CodexWireFingerprint
		if json.Unmarshal(wire.Value, &observation) == nil {
			view.Observed = &observation
		}
	}
	for _, model := range s.openAICodexTicketConfig().Models {
		status := CodexRoutingModelStatus{Model: model, Phase: "idle"}
		record, readErr := store.ReadExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: "tickets", Key: strconv.FormatInt(id, 10) + "." + codexRoutingDigest(model)})
		if readErr != nil {
			return nil, readErr
		}
		if record.Found {
			var state struct {
				Schema        int                                    `json:"schema"`
				Identity      string                                 `json:"identity"`
				Phase         string                                 `json:"phase"`
				Enrolled      bool                                   `json:"enrolled"`
				Failures      int                                    `json:"failures"`
				ExpiresAt     *time.Time                             `json:"expires_at"`
				NextAt        *time.Time                             `json:"next_attempt_at"`
				Qualification *extensionv1.CodexRoutingQualification `json:"qualification"`
				Observation   *extensionv1.CodexRoutingObservation   `json:"observation"`
			}
			if json.Unmarshal(record.Value, &state) == nil && state.Identity == CodexTicketAccountIdentity(a) {
				status.Phase, status.Enrolled, status.Failures, status.ExpiresAt, status.NextAt, status.LastObservation = state.Phase, state.Enrolled, state.Failures, state.ExpiresAt, state.NextAt, state.Observation
				status.Qualified = scopeErr == nil && state.Schema == extensionv1.CodexRoutingSchema && state.Qualification.Valid(time.Now(), id, state.Identity, model) && state.Qualification.Scope.SameOwner(currentScope)
				if state.Qualification != nil && state.Qualification.Model == model && !state.Qualification.VerifiedAt.IsZero() {
					verified := state.Qualification.VerifiedAt
					status.VerifiedAt = &verified
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

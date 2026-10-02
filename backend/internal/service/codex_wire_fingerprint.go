package service

import (
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// codexWireIngressKey marks an outbound request that serves a downstream
// WebSocket turn (the HTTP bridge), so its observation reports ingress "ws".
type codexWireIngressKey struct{}

//go:embed codex_client_reference.json
var codexClientReference json.RawMessage

// CodexWireFingerprint is the last final outbound request observed for an
// account: identity headers, encoding and transport facts, never credentials
// or the request body.
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
	AccountProxyID      int64                                    `json:"account_proxy_id,omitempty"`
	SessionPresent      bool                                     `json:"session_present"`
	ThreadPresent       bool                                     `json:"thread_present"`
	WindowPresent       bool                                     `json:"window_present"`
	StatePresent        bool                                     `json:"state_present"`
	StateLength         int                                      `json:"state_length"`
	State               string                                   `json:"state,omitempty"`
	WebSocketExtensions string                                   `json:"websocket_extensions,omitempty"`
}

type CodexFingerprintView struct {
	ConfiguredProfile *extensionv1.CodexClientProfile `json:"configured_profile,omitempty"`
	CapturedReference json.RawMessage                 `json:"captured_reference"`
	AccountID         int64                           `json:"account_id"`
	Configured        codexIdentitySnapshot           `json:"configured"`
	Observed          *CodexWireFingerprint           `json:"observed,omitempty"`
	ObservedError     string                          `json:"observed_error,omitempty"`
	ReferenceCommit   string                          `json:"reference_commit"`
	ReferenceSource   string                          `json:"reference_source"`
	Alignment         string                          `json:"alignment"`
}

// observeCodexWire records the final outbound request of an OAuth account
// (transport "http", or "ws" for a native WebSocket) for the fingerprint view.
func (s *OpenAIGatewayService) observeCodexWire(ctx context.Context, account *Account, request *http.Request, response *http.Response, transport string) {
	if s == nil || s.nativeCodexRuntime == nil || account == nil || request == nil || response == nil {
		return
	}
	installation := s.nativeCodexRuntime.metadata()
	store := s.nativeCodexRuntime.repo
	if installation == nil || store == nil {
		return
	}
	value := CodexWireFingerprint{ObservedAt: time.Now().UTC(), Source: "final_outbound", Transport: transport, UserAgent: request.Header.Get("User-Agent"), Originator: request.Header.Get("originator"), Version: request.Header.Get("version"), HTTPProtocol: response.Proto, RequestEncoding: request.Header.Get("Content-Encoding"), TLSImplementation: "go-crypto-tls", JA3: "unknown", SessionPresent: extractClientSessionID(request.Header) != "", ThreadPresent: request.Header.Get("thread-id") != "", WindowPresent: request.Header.Get("x-codex-window-id") != "", StatePresent: request.Header.Get(openAICodexTurnStateHeader) != "", StateLength: len(request.Header.Get(openAICodexTurnStateHeader))}
	value.Ingress = "http"
	value.State = request.Header.Get(openAICodexTurnStateHeader)
	value.AccountProxyID = qualityProxyID(account)
	body, carried := request.Context().Value(codexIdentityBodyKey{}).(codexIdentityBodyObservation)
	if !carried {
		// Only a rewritten (zstd) wire copy carries a pre-encoding observation;
		// an unrewritten request still exposes its plaintext replay body.
		body = inspectCodexIdentityBody(request)
	}
	value.IdentityFields = codexIdentityFieldObservations(request.Header, body)
	if ingress, ok := request.Context().Value(codexWireIngressKey{}).(string); ok && ingress == "ws" {
		value.Ingress = ingress
	}
	if response.TLS != nil {
		value.TLSVersion = tls.VersionName(response.TLS.Version)
		value.CipherSuite = tls.CipherSuiteName(response.TLS.CipherSuite)
		value.ALPN = response.TLS.NegotiatedProtocol
	}
	if value.Transport == "ws" {
		value.WebSocketExtensions = response.Header.Get("Sec-WebSocket-Extensions")
	}
	ctx = WithNativeCodexExecution(context.WithoutCancel(ctx), installation)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	key := "wire." + strconv.FormatInt(account.ID, 10)
	previous, err := store.ReadExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexPrivateStateNamespace, Key: key})
	if err != nil {
		return
	}
	raw, _ := json.Marshal(value)
	_, _ = store.CompareSwapExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexPrivateStateNamespace, Key: key, ExpectedRevision: previous.Revision, Value: raw})
}

// CodexFingerprint is read-only: opening an account never refreshes OAuth,
// pings a proxy, or sends a model request.
func (s *OpenAIGatewayService) CodexFingerprint(ctx context.Context, id int64) (*CodexFingerprintView, error) {
	if s.nativeCodexRuntime == nil {
		return nil, codexIdentityUnavailable("native Codex runtime is not configured")
	}
	a, err := s.accountRepo.GetByID(ctx, id)
	switch {
	case err != nil:
		return nil, codexIdentityUnavailable("read account %d: %v", id, err)
	case !isCodexCredentialOwner(a):
		return nil, codexIdentityUnavailable("account %d is not an OpenAI OAuth/setup-token Codex account", id)
	}
	installation := s.nativeCodexRuntime.metadata()
	store := s.nativeCodexRuntime.repo
	if installation == nil || store == nil {
		return nil, codexIdentityUnavailable("native Codex runtime is not loaded")
	}
	view := &CodexFingerprintView{AccountID: id, Configured: resolveCodexIdentitySnapshotContext(ctx, a, a, codexAccountIdentityOverrideUA(a)), ReferenceCommit: "4b664e0ef0397f82e68c60088a90fcd035deb796", ReferenceSource: "official_source"}
	wire, err := store.ReadExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: codexPrivateStateNamespace, Key: "wire." + strconv.FormatInt(id, 10)})
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
	if profile, ok := a.codexClientIdentityContext(ctx); ok {
		public := extensionv1.CodexClientProfile(profile)
		view.ConfiguredProfile = &public
	}
	view.Alignment = "application_reference_available_transport_unverified"
	view.CapturedReference = append(json.RawMessage(nil), codexClientReference...)
	return view, nil
}

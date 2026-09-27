package service

import (
	"time"

	proxytransport "github.com/Wei-Shaw/sub2api/internal/proxytransport"
)

type CodexTicketProxyTrust struct {
	Certificates [][]byte `json:"certificates"`
	Fingerprint  string   `json:"fingerprint"`
}

type CodexTicketProxyStage struct {
	Name       string `json:"name"`
	Success    bool   `json:"success"`
	DurationMS int64  `json:"duration_ms"`
	Message    string `json:"message,omitempty"`
}

// CodexTicketProxyCertificate is one certificate presented during the test
// handshake (HTTPS proxy, target, or TLS-inspecting proxy chain).
type CodexTicketProxyCertificate struct {
	ServerName   string    `json:"server_name,omitempty"`
	Subject      string    `json:"subject"`
	Issuer       string    `json:"issuer"`
	SerialNumber string    `json:"serial_number,omitempty"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	DNSNames     []string  `json:"dns_names,omitempty"`
	SHA256       string    `json:"sha256"`
	SPKISHA256   string    `json:"spki_sha256"`
}

// FailureDetail is the original error of the failed request and every stage
// keeps its own original error text; certificate facts come from the handshake.
type CodexTicketProxyTestResult struct {
	FailureDetail          string                        `json:"failure_detail,omitempty"`
	Success                bool                          `json:"success"`
	NetworkReachable       bool                          `json:"network_reachable"`
	Protocol               string                        `json:"protocol"`
	HTTPStatus             int                           `json:"http_status,omitempty"`
	Code                   string                        `json:"code"`
	Message                string                        `json:"message"`
	Stages                 []CodexTicketProxyStage       `json:"stages"`
	CertificateTrust       string                        `json:"certificate_trust,omitempty"`
	CertificateFingerprint string                        `json:"certificate_fingerprint,omitempty"`
	Certificates           []CodexTicketProxyCertificate `json:"certificates,omitempty"`
	CertificateError       string                        `json:"certificate_error,omitempty"`
	ProtocolSuggestion     string                        `json:"protocol_suggestion,omitempty"`
}

// NormalizeCodexTicketProxy is shared by settings validation and the preview
// test. Parsing never performs IO or changes the configured proxy.
func NormalizeCodexTicketProxy(raw string) (string, error) {
	return proxytransport.Normalize(raw)
}

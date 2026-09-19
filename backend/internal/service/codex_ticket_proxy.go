package service

import (
	proxytransport "github.com/Wei-Shaw/sub2api/pkg/extensionapi/proxy"
)

const codexTicketProxyTarget = "https://chatgpt.com/backend-api/codex/responses"

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

type CodexTicketProxyTestResult struct {
	FailureDetail          string                  `json:"failure_detail,omitempty"`
	Success                bool                    `json:"success"`
	NetworkReachable       bool                    `json:"network_reachable"`
	Protocol               string                  `json:"protocol"`
	HTTPStatus             int                     `json:"http_status,omitempty"`
	Code                   string                  `json:"code"`
	Message                string                  `json:"message"`
	Stages                 []CodexTicketProxyStage `json:"stages"`
	CertificateTrust       string                  `json:"certificate_trust,omitempty"`
	CertificateFingerprint string                  `json:"certificate_fingerprint,omitempty"`
	ProtocolSuggestion     string                  `json:"protocol_suggestion,omitempty"`
}

// NormalizeCodexTicketProxy is shared by settings validation and the preview
// test. Parsing never performs IO or changes the configured proxy.
func NormalizeCodexTicketProxy(raw string) (string, error) {
	return proxytransport.Normalize(raw)
}

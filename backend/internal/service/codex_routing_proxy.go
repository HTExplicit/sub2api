package service

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"time"

	proxytransport "github.com/Wei-Shaw/sub2api/pkg/extensionapi/proxy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// This is the existing explicit proxy.test record. Only acquisition reads it;
// business connection leases and native WebSockets retain their own TLS policy.
type codexRoutingProxyTrust struct {
	Certificates [][]byte `json:"certificates"`
	Fingerprint  string   `json:"fingerprint"`
}

func (s *OpenAIGatewayService) doCodexRoutingAcquisition(request *http.Request, proxyURL string, accountID int64) (*http.Response, error) {
	if request == nil || request.URL == nil || request.Method != http.MethodPost || request.URL.Scheme != "https" || request.URL.Host != "chatgpt.com" || request.URL.Path != "/backend-api/codex/responses" || request.URL.RawQuery != "" || request.URL.User != nil || s.pluginManager == nil {
		return nil, errCodexRoutingUnavailable
	}
	normal, err := proxytransport.Normalize(proxyURL)
	if err != nil || normal == "" {
		return nil, errCodexRoutingUnavailable
	}
	installation, _ := s.pluginManager.installedByKey(codexRuntimePluginKey)
	store, ok := s.pluginManager.repo.(PluginExtensionStateStore)
	if installation == nil || !ok {
		return nil, errCodexRoutingUnavailable
	}
	ctx := WithPluginExecution(request.Context(), installation)
	record, err := store.ReadExtensionState(ctx, installation.PluginKey, extensionv1.StateRequest{Namespace: "proxy-trust", Key: codexRoutingDigest(normal, "chatgpt.com")})
	if err != nil {
		return nil, errCodexRoutingUnavailable
	}
	wire := request.Clone(WithHTTPUpstreamProfile(request.Context(), HTTPUpstreamProfileOpenAIHarvest))
	wire.Header.Del("Cookie")
	if !record.Found {
		if s.httpUpstream == nil {
			return nil, errCodexRoutingUnavailable
		}
		return s.httpUpstream.Do(wire, normal, accountID, 1)
	}
	var trust codexRoutingProxyTrust
	if len(record.Value) > 128*1024 || json.Unmarshal(record.Value, &trust) != nil || len(trust.Certificates) != 1 {
		return nil, errCodexRoutingUnavailable
	}
	anchor, err := x509.ParseCertificate(trust.Certificates[0])
	if err != nil || codexRoutingDigest(string(anchor.RawSubjectPublicKeyInfo)) != trust.Fingerprint {
		return nil, errCodexRoutingUnavailable
	}
	roots := x509.NewCertPool()
	roots.AddCert(anchor)
	transport, err := codexRoutingAcquisitionTransport(normal, roots)
	if err != nil {
		return nil, errCodexRoutingUnavailable
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client.Do(wire)
}

func verifyCodexRoutingProxyCertificate(state tls.ConnectionState, pinnedRoots *x509.CertPool) error {
	if state.ServerName == "" || len(state.PeerCertificates) == 0 {
		return errCodexRoutingUnavailable
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range state.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	opts := x509.VerifyOptions{DNSName: state.ServerName, Intermediates: intermediates}
	if _, err := state.PeerCertificates[0].Verify(opts); err == nil {
		return nil
	}
	// The explicit anchor cannot authorize an HTTPS proxy endpoint, another
	// origin, an expired certificate or a changed certificate chain. No IO in
	// this path learns or writes certificate trust, with or without OAuth.
	if state.ServerName != "chatgpt.com" || pinnedRoots == nil {
		return errCodexRoutingUnavailable
	}
	opts.Roots = pinnedRoots
	if _, err := state.PeerCertificates[0].Verify(opts); err != nil {
		return errCodexRoutingUnavailable
	}
	return nil
}

func codexRoutingAcquisitionTransport(raw string, pinnedRoots *x509.CertPool) (*http.Transport, error) {
	address, err := url.Parse(raw)
	if err != nil {
		return nil, errCodexRoutingUnavailable
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 12 * time.Second,
		DisableKeepAlives:     true,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // #nosec G402 -- complete system/pinned chain, hostname and time validation below.
			VerifyConnection: func(state tls.ConnectionState) error {
				return verifyCodexRoutingProxyCertificate(state, pinnedRoots)
			},
		},
	}
	if err := proxytransport.Configure(transport, address, nil); err != nil {
		return nil, err
	}
	return transport, nil
}

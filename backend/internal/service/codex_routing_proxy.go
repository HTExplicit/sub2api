package service

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	proxytransport "github.com/Wei-Shaw/sub2api/internal/proxytransport"
)

// This is the existing explicit proxy.test record. Only acquisition reads it;
// business connection leases and native WebSockets retain their own TLS policy.
type codexRoutingProxyTrust struct {
	Certificates [][]byte `json:"certificates"`
	Fingerprint  string   `json:"fingerprint"`
}

func (s *OpenAIGatewayService) doCodexRoutingAcquisition(request *http.Request, proxyURL string, accountID int64) (*http.Response, error) {
	if request == nil || request.URL == nil || request.Method != http.MethodPost || request.URL.Scheme != "https" || request.URL.Host != "chatgpt.com" || request.URL.Path != "/backend-api/codex/responses" || request.URL.RawQuery != "" || request.URL.User != nil || s.nativeCodexRuntime == nil {
		return nil, codexRoutingUnavailable("acquisition accepts only POST https://chatgpt.com/backend-api/codex/responses with a loaded Codex runtime")
	}
	_, err := proxytransport.ParseEndpoint(proxyURL)
	if err != nil {
		return nil, codexRoutingUnavailable("acquisition proxy: %v", err)
	}
	normal := proxyURL
	installation := s.nativeCodexRuntime.metadata()
	store := s.nativeCodexRuntime.repo
	ok := store != nil
	if installation == nil || !ok {
		return nil, codexRoutingUnavailable("Codex runtime state is not loaded")
	}
	ctx := WithNativeCodexExecution(request.Context(), installation)
	record, err := store.ReadExtensionState(ctx, NativeCodexPluginKey, extensionv1.StateRequest{Namespace: "proxy-trust", Key: codexRoutingDigest(normal, "chatgpt.com")})
	if err != nil {
		return nil, codexRoutingUnavailable("read pinned acquisition proxy certificate: %v", err)
	}
	wire := request.Clone(WithHTTPUpstreamProfile(request.Context(), HTTPUpstreamProfileOpenAIHarvest))
	wire.Header.Del("Cookie")
	if !record.Found {
		if s.httpUpstream == nil {
			return nil, codexRoutingUnavailable("no upstream transport is configured")
		}
		if err := reserveCodexQualityAcquisition(wire, accountID); err != nil {
			return nil, err
		}
		response, err := s.httpUpstream.Do(wire, normal, accountID, 1)
		observeCodexQualityResponse(wire.Context(), response, err, nil)
		return response, err
	}
	var trust codexRoutingProxyTrust
	if len(record.Value) > 128*1024 {
		return nil, codexRoutingUnavailable("pinned proxy certificate record of %d bytes exceeds 128 KiB", len(record.Value))
	}
	if err := json.Unmarshal(record.Value, &trust); err != nil {
		return nil, codexRoutingUnavailable("decode pinned proxy certificate: %v", err)
	}
	if len(trust.Certificates) != 1 {
		return nil, codexRoutingUnavailable("pinned proxy certificate record holds %d certificates, expected 1", len(trust.Certificates))
	}
	anchor, err := x509.ParseCertificate(trust.Certificates[0])
	if err != nil {
		return nil, codexRoutingUnavailable("parse pinned proxy certificate: %v", err)
	}
	if digest := codexRoutingDigest(string(anchor.RawSubjectPublicKeyInfo)); digest != trust.Fingerprint {
		return nil, codexRoutingUnavailable("pinned proxy certificate key digest %s does not match its record %s", digest, trust.Fingerprint)
	}
	roots := x509.NewCertPool()
	roots.AddCert(anchor)
	transport, err := codexRoutingAcquisitionTransport(normal, roots)
	if err != nil {
		return nil, codexRoutingUnavailable("acquisition transport: %v", err)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err := reserveCodexQualityAcquisition(wire, accountID); err != nil {
		return nil, err
	}
	response, err := client.Do(wire)
	observeCodexQualityResponse(wire.Context(), response, err, nil)
	return response, err
}

func verifyCodexRoutingProxyCertificate(state tls.ConnectionState, pinnedRoots *x509.CertPool) error {
	if state.ServerName == "" || len(state.PeerCertificates) == 0 {
		return codexRoutingUnavailable("TLS handshake reported no server name or peer certificate")
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range state.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	opts := x509.VerifyOptions{DNSName: state.ServerName, Intermediates: intermediates}
	_, systemErr := state.PeerCertificates[0].Verify(opts)
	if systemErr == nil {
		return nil
	}
	// The explicit anchor cannot authorize an HTTPS proxy endpoint, another
	// origin, an expired certificate or a changed certificate chain. No IO in
	// this path learns or writes certificate trust, with or without OAuth.
	leaf := state.PeerCertificates[0]
	if state.ServerName != "chatgpt.com" || pinnedRoots == nil {
		return codexRoutingUnavailable("certificate %q (issuer %q) for %s is not trusted by the system roots and no pinned proxy certificate applies: %v", leaf.Subject.String(), leaf.Issuer.String(), state.ServerName, systemErr)
	}
	opts.Roots = pinnedRoots
	if _, err := leaf.Verify(opts); err != nil {
		return codexRoutingUnavailable("certificate %q (issuer %q) for %s is trusted neither by the system roots (%v) nor by the pinned proxy certificate (%v); test the acquisition proxy again", leaf.Subject.String(), leaf.Issuer.String(), state.ServerName, systemErr, err)
	}
	return nil
}

func codexRoutingAcquisitionTransport(raw string, pinnedRoots *x509.CertPool) (*http.Transport, error) {
	address, err := proxytransport.ParseEndpoint(raw)
	if err != nil {
		return nil, codexRoutingUnavailable("acquisition proxy: %v", err)
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

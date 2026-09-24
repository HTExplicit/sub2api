package tickets

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	proxytransport "github.com/Wei-Shaw/sub2api/internal/proxytransport"
)

type ProxyTrust struct {
	Certificates [][]byte `json:"certificates"`
	Fingerprint  string   `json:"fingerprint"`
}
type ProxyStage struct {
	Name       string `json:"name"`
	Success    bool   `json:"success"`
	DurationMS int64  `json:"duration_ms"`
	Message    string `json:"message,omitempty"`
}
type ProxyResult struct {
	Success                bool         `json:"success"`
	NetworkReachable       bool         `json:"network_reachable"`
	Protocol               string       `json:"protocol"`
	HTTPStatus             int          `json:"http_status,omitempty"`
	Code                   string       `json:"code"`
	Stages                 []ProxyStage `json:"stages"`
	CertificateTrust       string       `json:"certificate_trust,omitempty"`
	CertificateFingerprint string       `json:"certificate_fingerprint,omitempty"`
}

func verifyProxyCertificate(state tls.ConnectionState, trust *ProxyTrust, learn bool) (*ProxyTrust, error) {
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("missing peer certificate")
	}
	name := state.ServerName
	if name == "" {
		name = "chatgpt.com"
	}
	leaf := state.PeerCertificates[0]
	intermediates := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	opts := x509.VerifyOptions{DNSName: name, Intermediates: intermediates}
	if _, err := leaf.Verify(opts); err == nil {
		return nil, nil
	}
	if name != "chatgpt.com" {
		return nil, errors.New("untrusted proxy TLS issuer")
	}
	if trust != nil && len(trust.Certificates) > 0 {
		roots := x509.NewCertPool()
		for _, der := range trust.Certificates {
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, err
			}
			roots.AddCert(cert)
		}
		opts.Roots = roots
		if _, err := leaf.Verify(opts); err == nil {
			return trust, nil
		}
	}
	if !learn {
		return nil, errors.New("proxy certificate changed or is untrusted")
	}
	anchor := state.PeerCertificates[len(state.PeerCertificates)-1]
	if time.Now().Before(anchor.NotBefore) || !time.Now().Before(anchor.NotAfter) {
		return nil, errors.New("proxy certificate outside validity period")
	}
	roots := x509.NewCertPool()
	roots.AddCert(anchor)
	opts.Roots = roots
	if _, err := leaf.Verify(opts); err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(anchor.RawSubjectPublicKeyInfo)
	return &ProxyTrust{Certificates: [][]byte{anchor.Raw}, Fingerprint: hex.EncodeToString(fingerprint[:])}, nil
}

func ticketProxyTransport(raw string, trust *ProxyTrust, learn bool, observe func(*ProxyTrust)) (*http.Transport, error) {
	address, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid proxy")
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 8 * time.Second}).DialContext, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 12 * time.Second, DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: func(state tls.ConnectionState) error { // #nosec G402 -- hostname, time and constrained roots are verified explicitly.
		next, err := verifyProxyCertificate(state, trust, learn)
		if err == nil && observe != nil {
			observe(next)
		}
		return err
	}}
	if err := proxytransport.Configure(transport, address, nil); err != nil {
		return nil, err
	}
	return transport, nil
}

func proxyStateKey(raw string) string {
	sum := sha256.Sum256([]byte(raw + "\x00chatgpt.com"))
	return hex.EncodeToString(sum[:])
}

func loadProxyTrust(ctx context.Context, host HostCaller, raw string) (*ProxyTrust, int64, error) {
	var state extensionv1.StateResult
	if err := hostCall(ctx, host, extensionv1.HostStateRead, extensionv1.StateRequest{Namespace: "proxy-trust", Key: proxyStateKey(raw)}, &state); err != nil {
		return nil, 0, err
	}
	if !state.Found {
		return nil, 0, nil
	}
	var trust ProxyTrust
	if err := json.Unmarshal(state.Value, &trust); err != nil {
		return nil, 0, err
	}
	return &trust, state.Revision, nil
}

func prepareProxy(ctx context.Context, host HostCaller, raw string, explicitTest bool) (*http.Client, *ProxyResult, error) {
	normal, err := proxytransport.Normalize(raw)
	result := &ProxyResult{Stages: []ProxyStage{}, Code: "invalid_proxy"}
	if err != nil || normal == "" {
		return nil, result, errors.New("invalid proxy")
	}
	address, _ := url.Parse(normal)
	result.Protocol = address.Scheme
	trust, revision, err := loadProxyTrust(ctx, host, normal)
	if err != nil {
		return nil, result, err
	}
	var mu sync.Mutex
	var observed *ProxyTrust
	transport, err := ticketProxyTransport(normal, trust, explicitTest || trust == nil, func(next *ProxyTrust) { mu.Lock(); observed = next; mu.Unlock() })
	if err != nil {
		return nil, result, err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	trace := &httptrace.ClientTrace{
		ConnectDone: func(_, _ string, err error) {
			mu.Lock()
			defer mu.Unlock()
			result.Stages = append(result.Stages, ProxyStage{Name: "tcp", Success: err == nil, DurationMS: time.Since(start).Milliseconds()})
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			mu.Lock()
			defer mu.Unlock()
			result.Stages = append(result.Stages, ProxyStage{Name: "tls", Success: err == nil, DurationMS: time.Since(start).Milliseconds()})
		},
	}
	request, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodHead, "https://chatgpt.com/backend-api/codex/responses", nil)
	response, err := client.Do(request)
	if err != nil {
		result.Code = "proxy_connection_failed"
		return nil, result, errors.New("proxy connection or certificate validation failed")
	}
	_ = response.Body.Close()
	result.HTTPStatus = response.StatusCode
	result.NetworkReachable = true
	result.Code = "target_http_status"
	result.Stages = append(result.Stages, ProxyStage{Name: "http", Success: response.StatusCode >= 200 && response.StatusCode < 400, Message: fmt.Sprintf("HTTP %d", response.StatusCode)})
	mu.Lock()
	learned := observed
	mu.Unlock()
	result.CertificateTrust = "system"
	if learned != nil {
		result.CertificateTrust = "proxy_certificate_pinned"
		result.CertificateFingerprint = learned.Fingerprint
		if trust == nil || trust.Fingerprint != learned.Fingerprint {
			rawTrust, _ := json.Marshal(learned)
			var saved extensionv1.StateResult
			if err := hostCall(ctx, host, extensionv1.HostStateCompareSwap, extensionv1.StateRequest{Namespace: "proxy-trust", Key: proxyStateKey(normal), ExpectedRevision: revision, Value: rawTrust}, &saved); err != nil || !saved.Applied {
				return nil, result, errors.New("proxy trust changed while preparing connection")
			}
		}
	}
	finalTransport, err := ticketProxyTransport(normal, learned, false, nil)
	if err != nil {
		return nil, result, err
	}
	finalClient := &http.Client{Transport: finalTransport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if explicitTest {
		request, _ = http.NewRequestWithContext(ctx, http.MethodHead, "https://chatgpt.com/backend-api/codex/responses", nil)
		response, err = finalClient.Do(request)
		if err != nil {
			finalTransport.CloseIdleConnections()
			result.Code = "pinned_connection_failed"
			result.Stages = append(result.Stages, ProxyStage{Name: "pinned_connection", Success: false})
			return nil, result, errors.New("pinned proxy connection failed")
		}
		_ = response.Body.Close()
		result.HTTPStatus = response.StatusCode
		result.Stages = append(result.Stages, ProxyStage{Name: "pinned_connection", Success: true, Message: fmt.Sprintf("HTTP %d", response.StatusCode)})
	}
	result.Success = result.HTTPStatus >= 200 && result.HTTPStatus < 400
	if result.Success {
		result.Code = "proxy_reachable"
	}
	return finalClient, result, nil
}

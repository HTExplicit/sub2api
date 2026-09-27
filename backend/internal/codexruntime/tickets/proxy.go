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
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	proxytransport "github.com/Wei-Shaw/sub2api/internal/proxytransport"
)

const proxyTestTarget = "https://chatgpt.com/backend-api/codex/responses"

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

// ProxyCertificate is one certificate presented during a proxy test handshake:
// the HTTPS proxy's own chain, the target chain, or a TLS-inspecting proxy's.
type ProxyCertificate struct {
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

// A proxy test reports every stage with its original error text. FailureDetail
// is the complete error of the failed request; nothing is redacted because the
// administrator configured (and can read) the proxy URL itself.
type ProxyResult struct {
	Success                bool               `json:"success"`
	NetworkReachable       bool               `json:"network_reachable"`
	Protocol               string             `json:"protocol"`
	HTTPStatus             int                `json:"http_status,omitempty"`
	Code                   string             `json:"code"`
	Stages                 []ProxyStage       `json:"stages"`
	CertificateTrust       string             `json:"certificate_trust,omitempty"`
	CertificateFingerprint string             `json:"certificate_fingerprint,omitempty"`
	Certificates           []ProxyCertificate `json:"certificates,omitempty"`
	CertificateError       string             `json:"certificate_error,omitempty"`
	FailureDetail          string             `json:"failure_detail,omitempty"`
	ProtocolSuggestion     string             `json:"protocol_suggestion,omitempty"`
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
	systemErr := func() error { _, err := leaf.Verify(opts); return err }()
	if systemErr == nil {
		return nil, nil
	}
	if name != "chatgpt.com" {
		return nil, fmt.Errorf("untrusted proxy TLS issuer for %s: %w", name, systemErr)
	}
	var pinnedErr error
	if trust != nil && len(trust.Certificates) > 0 {
		roots := x509.NewCertPool()
		for _, der := range trust.Certificates {
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, fmt.Errorf("stored pinned proxy certificate cannot be parsed: %w", err)
			}
			roots.AddCert(cert)
		}
		opts.Roots = roots
		if _, pinnedErr = leaf.Verify(opts); pinnedErr == nil {
			return trust, nil
		}
	}
	if !learn {
		if pinnedErr != nil {
			return nil, fmt.Errorf("proxy certificate changed or is untrusted: system roots: %v; pinned certificate %s: %v", systemErr, trust.Fingerprint, pinnedErr)
		}
		return nil, fmt.Errorf("proxy certificate changed or is untrusted: system roots: %v; no pinned certificate", systemErr)
	}
	anchor := state.PeerCertificates[len(state.PeerCertificates)-1]
	if now := time.Now(); now.Before(anchor.NotBefore) || !now.Before(anchor.NotAfter) {
		return nil, fmt.Errorf("proxy certificate outside validity period: %q valid from %s to %s", anchor.Subject.String(), anchor.NotBefore.UTC().Format(time.RFC3339), anchor.NotAfter.UTC().Format(time.RFC3339))
	}
	roots := x509.NewCertPool()
	roots.AddCert(anchor)
	opts.Roots = roots
	if _, err := leaf.Verify(opts); err != nil {
		return nil, fmt.Errorf("proxy certificate chain does not verify against its anchor %q: %w", anchor.Subject.String(), err)
	}
	fingerprint := sha256.Sum256(anchor.RawSubjectPublicKeyInfo)
	return &ProxyTrust{Certificates: [][]byte{anchor.Raw}, Fingerprint: hex.EncodeToString(fingerprint[:])}, nil
}

// proxyObserver collects stages and certificate facts from transport callbacks,
// which may run on dial goroutines. Results copy a snapshot, never the slices.
type proxyObserver struct {
	mu               sync.Mutex
	start            time.Time
	prefix           string
	stages           []ProxyStage
	certificates     []ProxyCertificate
	certificateError string
	connectStatus    int
	learned          *ProxyTrust
}

func newProxyObserver(prefix string) *proxyObserver {
	return &proxyObserver{start: time.Now(), prefix: prefix}
}

func (o *proxyObserver) add(stage ProxyStage) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	stage.Name = o.prefix + stage.Name
	if stage.DurationMS == 0 {
		stage.DurationMS = time.Since(o.start).Milliseconds()
	}
	o.stages = append(o.stages, stage)
}

func (o *proxyObserver) inspect(state tls.ConnectionState) {
	if o == nil {
		return
	}
	described := describeProxyCertificates(state.ServerName, state.PeerCertificates)
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, certificate := range described {
		duplicate := false
		for _, existing := range o.certificates {
			if existing.ServerName == certificate.ServerName && existing.SHA256 == certificate.SHA256 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			o.certificates = append(o.certificates, certificate)
		}
	}
}

func (o *proxyObserver) verified(next *ProxyTrust, err error) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		o.certificateError = err.Error()
		return
	}
	o.learned = next
}

func (o *proxyObserver) connectResponse(_ context.Context, _ *url.URL, _ *http.Request, response *http.Response) error {
	o.mu.Lock()
	o.connectStatus = response.StatusCode
	o.mu.Unlock()
	message := "HTTP " + response.Status
	if challenge := response.Header.Get("Proxy-Authenticate"); challenge != "" {
		message += " (Proxy-Authenticate: " + challenge + ")"
	}
	o.add(ProxyStage{Name: "connect", Success: response.StatusCode == http.StatusOK, Message: message})
	return nil
}

func (o *proxyObserver) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSDone: func(info httptrace.DNSDoneInfo) {
			message := ""
			if info.Err != nil {
				message = info.Err.Error()
			} else {
				addresses := make([]string, 0, len(info.Addrs))
				for _, address := range info.Addrs {
					addresses = append(addresses, address.String())
				}
				message = strings.Join(addresses, ", ")
			}
			o.add(ProxyStage{Name: "dns", Success: info.Err == nil, Message: message})
		},
		ConnectDone: func(network, addr string, err error) {
			message := strings.TrimSpace(network + " " + addr)
			if err != nil {
				message += ": " + err.Error()
			}
			o.add(ProxyStage{Name: "tcp", Success: err == nil, Message: message})
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			message := ""
			if err != nil {
				message = err.Error()
			} else {
				message = strings.TrimSpace(state.ServerName + " " + tls.VersionName(state.Version) + " " + tls.CipherSuiteName(state.CipherSuite) + " " + state.NegotiatedProtocol)
			}
			o.add(ProxyStage{Name: "tls", Success: err == nil, Message: message})
		},
	}
}

// snapshot copies the observed facts into result and returns the trust learned
// by the last successful handshake.
func (o *proxyObserver) snapshot(result *ProxyResult) *ProxyTrust {
	o.mu.Lock()
	defer o.mu.Unlock()
	result.Stages = append(result.Stages, o.stages...)
	for _, certificate := range o.certificates {
		duplicate := false
		for _, existing := range result.Certificates {
			if existing.ServerName == certificate.ServerName && existing.SHA256 == certificate.SHA256 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.Certificates = append(result.Certificates, certificate)
		}
	}
	if o.certificateError != "" {
		result.CertificateError = o.certificateError
	}
	return o.learned
}

func describeProxyCertificates(serverName string, chain []*x509.Certificate) []ProxyCertificate {
	out := make([]ProxyCertificate, 0, len(chain))
	for _, cert := range chain {
		if cert == nil {
			continue
		}
		der := sha256.Sum256(cert.Raw)
		spki := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		described := ProxyCertificate{ServerName: serverName, Subject: cert.Subject.String(), Issuer: cert.Issuer.String(), NotBefore: cert.NotBefore.UTC(), NotAfter: cert.NotAfter.UTC(), DNSNames: append([]string(nil), cert.DNSNames...), SHA256: hex.EncodeToString(der[:]), SPKISHA256: hex.EncodeToString(spki[:])}
		if cert.SerialNumber != nil {
			described.SerialNumber = cert.SerialNumber.Text(16)
		}
		out = append(out, described)
	}
	return out
}

func ticketProxyTransport(raw string, trust *ProxyTrust, learn bool, observer *proxyObserver) (*http.Transport, error) {
	address, err := proxytransport.ParseEndpoint(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy: %w", err)
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 8 * time.Second}).DialContext, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 12 * time.Second, DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: func(state tls.ConnectionState) error { // #nosec G402 -- hostname, time and constrained roots are verified explicitly.
		observer.inspect(state)
		next, err := verifyProxyCertificate(state, trust, learn)
		observer.verified(next, err)
		return err
	}}
	if observer != nil {
		transport.OnProxyConnectResponse = observer.connectResponse
	}
	if err := proxytransport.Configure(transport, address, nil); err != nil {
		return nil, err
	}
	return transport, nil
}

// failureCode classifies a failed proxy request for the result catalog; the
// observed CONNECT status decides proxy authentication and rejection.
// FailureDetail always carries the unclassified original error.
func (o *proxyObserver) failureCode(err error) string {
	o.mu.Lock()
	status := o.connectStatus
	o.mu.Unlock()
	switch {
	case status == http.StatusProxyAuthRequired:
		return "ticket_proxy_auth"
	case status != 0 && status != http.StatusOK:
		return "ticket_proxy_connect"
	}
	return proxyFailureCode(err)
}

// proxyFailureCode classifies a failed proxy request by its error alone.
func proxyFailureCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "ticket_canceled"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return "ticket_timeout"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "authentication failed"): // SOCKS5 username/password rejection
		return "ticket_proxy_auth"
	case strings.Contains(text, "certificate") || strings.Contains(text, "tls") || strings.Contains(text, "x509"):
		return "ticket_proxy_tls"
	case strings.Contains(text, "no such host") || strings.Contains(text, "name resolution"):
		return "ticket_proxy_dns"
	case strings.Contains(text, "malformed http response") || strings.Contains(text, "unexpected protocol") || strings.Contains(text, "socks version"):
		return "ticket_proxy_protocol"
	case strings.Contains(text, "connection refused") || strings.Contains(text, "connect rejected"):
		return "ticket_proxy_connect"
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		return "ticket_proxy_eof"
	case strings.Contains(text, "connection reset") || strings.Contains(text, "forcibly closed"):
		return "ticket_proxy_reset"
	}
	return "proxy_connection_failed"
}

func proxyProtocolSuggestion(scheme string, err error) string {
	text := strings.ToLower(err.Error())
	var timeout net.Error
	switch {
	case strings.HasPrefix(scheme, "socks") && (errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout()):
		return "尝试确认该端口是否为HTTP CONNECT代理"
	case scheme == "https" && strings.Contains(text, "first record does not look like a tls handshake"):
		return "该端口未使用TLS，请尝试 http 协议"
	case (scheme == "http" || scheme == "https") && strings.Contains(text, "malformed http response"):
		return "该端口可能不是HTTP代理，请尝试 socks5 或 socks5h 协议"
	}
	return ""
}

func proxyStateKey(raw string) string {
	sum := sha256.Sum256([]byte(raw + "\x00chatgpt.com"))
	return hex.EncodeToString(sum[:])
}

func loadProxyTrust(ctx context.Context, host HostCaller, raw string) (*ProxyTrust, int64, error) {
	var state extensionv1.StateResult
	if err := hostCall(ctx, host, extensionv1.HostStateRead, extensionv1.StateRequest{Namespace: "proxy-trust", Key: proxyStateKey(raw)}, &state); err != nil {
		return nil, 0, fmt.Errorf("read pinned proxy certificate: %w", err)
	}
	if !state.Found {
		return nil, 0, nil
	}
	var trust ProxyTrust
	if err := json.Unmarshal(state.Value, &trust); err != nil {
		return nil, 0, fmt.Errorf("decode pinned proxy certificate: %w", err)
	}
	return &trust, state.Revision, nil
}

func noProxyRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func prepareProxy(ctx context.Context, host HostCaller, raw string, explicitTest bool) (*http.Client, *ProxyResult, error) {
	address, err := proxytransport.ParseEndpoint(raw)
	result := &ProxyResult{Stages: []ProxyStage{}, Code: "invalid_proxy"}
	if err != nil {
		result.FailureDetail = err.Error()
		return nil, result, fmt.Errorf("invalid proxy: %w", err)
	}
	normal := raw
	result.Protocol = address.Scheme
	trust, revision, err := loadProxyTrust(ctx, host, normal)
	if err != nil {
		result.Code, result.FailureDetail = "proxy_trust_unavailable", err.Error()
		return nil, result, err
	}
	observer := newProxyObserver("")
	transport, err := ticketProxyTransport(normal, trust, explicitTest || trust == nil, observer)
	if err != nil {
		result.FailureDetail = err.Error()
		return nil, result, err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: noProxyRedirect}
	request, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, observer.trace()), http.MethodHead, proxyTestTarget, nil)
	response, err := client.Do(request)
	if err != nil {
		observer.snapshot(result)
		result.Code = observer.failureCode(err)
		result.FailureDetail = err.Error()
		result.ProtocolSuggestion = proxyProtocolSuggestion(address.Scheme, err)
		return nil, result, fmt.Errorf("proxy connection or certificate validation failed: %w", err)
	}
	_ = response.Body.Close()
	observer.add(ProxyStage{Name: "http", Success: response.StatusCode >= 200 && response.StatusCode < 400, Message: "HTTP " + response.Status})
	learned := observer.snapshot(result)
	result.HTTPStatus = response.StatusCode
	result.NetworkReachable = true
	result.Code = "target_http_status"
	result.CertificateTrust = "system"
	if learned != nil {
		result.CertificateTrust = "proxy_certificate_pinned"
		result.CertificateFingerprint = learned.Fingerprint
		if trust == nil || trust.Fingerprint != learned.Fingerprint {
			rawTrust, _ := json.Marshal(learned)
			var saved extensionv1.StateResult
			saveErr := hostCall(ctx, host, extensionv1.HostStateCompareSwap, extensionv1.StateRequest{Namespace: "proxy-trust", Key: proxyStateKey(normal), ExpectedRevision: revision, Value: rawTrust}, &saved)
			if saveErr == nil && !saved.Applied {
				saveErr = fmt.Errorf("pinned proxy certificate changed concurrently (expected revision %d, current revision %d)", revision, saved.Revision)
			}
			if saveErr != nil {
				result.Code, result.FailureDetail = "proxy_trust_changed", saveErr.Error()
				return nil, result, fmt.Errorf("proxy trust changed while preparing connection: %w", saveErr)
			}
		}
	}
	finalTransport, err := ticketProxyTransport(normal, learned, false, nil)
	if err != nil {
		result.FailureDetail = err.Error()
		return nil, result, err
	}
	finalClient := &http.Client{Transport: finalTransport, Timeout: 25 * time.Second, CheckRedirect: noProxyRedirect}
	if explicitTest {
		// The pinned check uses the same verifier as finalClient on its own
		// transport, so its observer never touches the returned client.
		checkObserver := newProxyObserver("pinned_")
		checkTransport, err := ticketProxyTransport(normal, learned, false, checkObserver)
		if err != nil {
			finalTransport.CloseIdleConnections()
			result.FailureDetail = err.Error()
			return nil, result, err
		}
		started := time.Now()
		request, _ = http.NewRequestWithContext(httptrace.WithClientTrace(ctx, checkObserver.trace()), http.MethodHead, proxyTestTarget, nil)
		response, err = (&http.Client{Transport: checkTransport, Timeout: 25 * time.Second, CheckRedirect: noProxyRedirect}).Do(request)
		checkTransport.CloseIdleConnections()
		checkObserver.snapshot(result)
		if err != nil {
			finalTransport.CloseIdleConnections()
			result.Success = false
			// The pinned re-test failed after a successful first connection.
			result.Code = "pinned_connection_failed"
			result.FailureDetail = err.Error()
			result.ProtocolSuggestion = proxyProtocolSuggestion(address.Scheme, err)
			result.Stages = append(result.Stages, ProxyStage{Name: "pinned_connection", Success: false, DurationMS: time.Since(started).Milliseconds(), Message: err.Error()})
			return nil, result, fmt.Errorf("pinned proxy connection failed: %w", err)
		}
		_ = response.Body.Close()
		result.HTTPStatus = response.StatusCode
		result.Stages = append(result.Stages, ProxyStage{Name: "pinned_connection", Success: true, DurationMS: time.Since(started).Milliseconds(), Message: "HTTP " + response.Status})
	}
	result.Success = result.HTTPStatus >= 200 && result.HTTPStatus < 400
	if result.Success {
		result.Code = "proxy_reachable"
	}
	return finalClient, result, nil
}

package service

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
)

const codexTicketProxyTarget = "https://chatgpt.com/backend-api/codex/responses"

type CodexTicketProxyTrust struct {
	Certificates [][]byte `json:"certificates"`
	Fingerprint  string   `json:"fingerprint"`
}

type CodexTicketProxyTrustRepository interface {
	CodexTicketProxyTrustGeneration(context.Context) (string, error)
	LoadCodexTicketProxyTrust(context.Context, string) (*CodexTicketProxyTrust, error)
	SaveCodexTicketProxyTrust(context.Context, string, *CodexTicketProxyTrust) error
}

type CodexTicketProxyStage struct {
	Name       string `json:"name"`
	Success    bool   `json:"success"`
	DurationMS int64  `json:"duration_ms"`
	Message    string `json:"message,omitempty"`
}

type CodexTicketProxyTestResult struct {
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
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > 16384 {
		return "", errors.New("proxy input is too long")
	}
	if strings.ContainsAny(raw, "\r\n") {
		fields := map[string]string{}
		for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line == "(" || line == ")" {
				continue
			}
			i := strings.IndexAny(line, ":：=")
			if i < 0 {
				return "", errors.New("each proxy field needs a label")
			}
			key := strings.ToLower(strings.TrimSpace(line[:i]))
			value := strings.TrimSpace(line[i+len(string([]rune(line[i:])[0])):])
			switch key {
			case "proxy server", "proxy_server", "server", "host", "hostname", "主机", "服务器", "节点", "代理服务器":
				key = "host"
			case "port", "端口":
				key = "port"
			case "user", "username", "用户名", "账号", "帐号":
				key = "username"
			case "pass", "password", "密码":
				key = "password"
			case "scheme", "protocol", "协议":
				key = "scheme"
			default:
				return "", errors.New("unrecognized proxy field label")
			}
			if _, exists := fields[key]; exists {
				return "", errors.New("duplicate proxy field")
			}
			fields[key] = value
		}
		if fields["host"] == "" || fields["port"] == "" {
			return "", errors.New("proxy host and port are required")
		}
		scheme := fields["scheme"]
		if scheme == "" {
			scheme = "http"
		}
		u := url.URL{Scheme: strings.ToLower(scheme), Host: net.JoinHostPort(strings.Trim(fields["host"], "[]"), fields["port"])}
		_, hasUser := fields["username"]
		_, hasPass := fields["password"]
		if hasUser != hasPass {
			return "", errors.New("both username and password are required")
		}
		if hasUser {
			u.User = url.UserPassword(fields["username"], fields["password"])
		}
		raw = u.String()
	} else if !strings.Contains(raw, "://") {
		if strings.Contains(raw, "@") {
			raw = "http://" + raw
		} else {
			parts := strings.SplitN(raw, ":", 4)
			if len(parts) == 4 && !strings.HasPrefix(raw, "[") {
				if _, err := strconv.Atoi(parts[1]); err != nil {
					return "", errors.New("ambiguous proxy fields; use labeled host, port, username and password")
				}
				u := url.URL{Scheme: "http", Host: net.JoinHostPort(parts[0], parts[1]), User: url.UserPassword(parts[2], parts[3])}
				raw = u.String()
			} else {
				raw = "http://" + raw
			}
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return "", errors.New("invalid proxy URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if err = ValidateOpenAICodexTicketHarvestProxyURL(u.String()); err != nil {
		return "", err
	}
	if u.Port() == "" {
		return "", errors.New("proxy port is required")
	}
	if strings.ContainsAny(u.Hostname(), " \t\r\n") {
		return "", errors.New("invalid proxy host")
	}
	u.Path = ""
	return u.String(), nil
}

func codexTicketProxyKey(raw string) string {
	sum := sha256.Sum256([]byte(raw + "\x00chatgpt.com"))
	return hex.EncodeToString(sum[:])
}

// Verify against system roots first, then only the certificates pinned to this
// configured proxy and target. Learning is limited to unauthenticated preflight.
// Hostname and validity checks always apply, including during first-use trust.
func verifyCodexTicketProxyTLS(cs tls.ConnectionState, trust *CodexTicketProxyTrust, learn bool) (*CodexTicketProxyTrust, error) {
	if len(cs.PeerCertificates) == 0 {
		return nil, errors.New("no peer certificate")
	}
	leaf := cs.PeerCertificates[0]
	name := cs.ServerName
	if name == "" {
		name = "chatgpt.com"
	}
	intermediates := x509.NewCertPool()
	for _, cert := range cs.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	opts := x509.VerifyOptions{DNSName: name, Intermediates: intermediates}
	if _, err := leaf.Verify(opts); err == nil {
		return nil, nil
	}
	if name != "chatgpt.com" {
		return nil, errors.New("proxy TLS issuer is not trusted")
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
		return nil, errors.New("ticket proxy certificate changed or is not trusted; test the proxy connection")
	}
	// Trust the provided chain anchor (or a pinned leaf if there is no chain).
	anchor := cs.PeerCertificates[len(cs.PeerCertificates)-1]
	if time.Now().Before(anchor.NotBefore) || !time.Now().Before(anchor.NotAfter) {
		return nil, errors.New("proxy certificate is expired or not yet valid")
	}
	roots := x509.NewCertPool()
	roots.AddCert(anchor)
	opts.Roots = roots
	if _, err := leaf.Verify(opts); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(anchor.RawSubjectPublicKeyInfo)
	return &CodexTicketProxyTrust{Certificates: [][]byte{anchor.Raw}, Fingerprint: hex.EncodeToString(sum[:])}, nil
}

type codexTicketPreparedProxy struct {
	client    *http.Client
	transport *http.Transport
	trust     *CodexTicketProxyTrust
}

type codexTicketProxyObservation struct {
	mu     sync.Mutex
	trust  *CodexTicketProxyTrust
	stages []CodexTicketProxyStage
}

func (o *codexTicketProxyObservation) add(stage CodexTicketProxyStage) {
	if o != nil {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.stages = append(o.stages, stage)
	}
}
func (o *codexTicketProxyObservation) snapshot() (*CodexTicketProxyTrust, []CodexTicketProxyStage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.trust, append([]CodexTicketProxyStage(nil), o.stages...)
}

func newCodexTicketProxyTransport(raw string, trust *CodexTicketProxyTrust, learn bool, observed *codexTicketProxyObservation) (*http.Transport, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid proxy")
	}
	tr := &http.Transport{DialContext: (&net.Dialer{Timeout: 8 * time.Second}).DialContext, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 12 * time.Second, DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	// InsecureSkipVerify only disables Go's default verifier; VerifyConnection
	// below fully verifies hostname, time and system or per-proxy pinned roots.
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: func(cs tls.ConnectionState) error { // #nosec G402 -- custom constrained certificate verification below
		next, err := verifyCodexTicketProxyTLS(cs, trust, learn)
		if err == nil && observed != nil {
			observed.mu.Lock()
			observed.trust = next
			observed.mu.Unlock()
		}
		return err
	}}
	tr.OnProxyConnectResponse = func(ctx context.Context, proxyURL *url.URL, connectReq *http.Request, connectRes *http.Response) error {
		observed.add(CodexTicketProxyStage{Name: "auth", Success: connectRes.StatusCode != 407, Message: fmt.Sprintf("HTTP %d", connectRes.StatusCode)})
		observed.add(CodexTicketProxyStage{Name: "connect", Success: connectRes.StatusCode == 200, Message: fmt.Sprintf("HTTP %d", connectRes.StatusCode)})
		if connectRes.StatusCode == 407 {
			return errors.New("ticket proxy authentication failed (407)")
		}
		if connectRes.StatusCode != 200 {
			return errors.New("ticket proxy CONNECT rejected")
		}
		return nil
	}
	if err = proxyutil.ConfigureTransportProxy(tr, u); err != nil {
		return nil, err
	}
	return tr, nil
}

func (s *OpenAIGatewayService) prepareCodexTicketProxy(ctx context.Context, raw string, learn, persist bool) (*codexTicketPreparedProxy, *CodexTicketProxyTestResult, error) {
	result := &CodexTicketProxyTestResult{Stages: []CodexTicketProxyStage{}}
	normal, err := NormalizeCodexTicketProxy(raw)
	if err != nil || normal == "" {
		result.Code = "invalid_proxy"
		result.Message = "代理格式不完整或无法识别"
		return nil, result, errors.New(result.Message)
	}
	u, _ := url.Parse(normal)
	result.Protocol = u.Scheme
	result.Stages = append(result.Stages, CodexTicketProxyStage{Name: "parse", Success: true})
	var trust *CodexTicketProxyTrust
	store, _ := s.accountRepo.(CodexTicketProxyTrustRepository)
	trustKey := codexTicketProxyKey(normal)
	if store != nil {
		generation, generationErr := store.CodexTicketProxyTrustGeneration(ctx)
		if generationErr != nil {
			return nil, result, generationErr
		}
		trustKey = codexTicketProxyKey(normal + "\x00" + generation)
		trust, err = store.LoadCodexTicketProxyTrust(ctx, trustKey)
		if err != nil {
			return nil, result, err
		}
	}
	if !learn {
		tr, e := newCodexTicketProxyTransport(normal, trust, false, nil)
		if e != nil {
			return nil, result, e
		}
		return &codexTicketPreparedProxy{client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: tr, trust: trust}, result, nil
	}
	observation := &codexTicketProxyObservation{}
	tr, err := newCodexTicketProxyTransport(normal, trust, true, observation)
	if err != nil {
		return nil, result, err
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	stageStart := time.Now()
	trace := &httptrace.ClientTrace{
		DNSDone: func(i httptrace.DNSDoneInfo) {
			observation.add(CodexTicketProxyStage{Name: "dns", Success: i.Err == nil})
		},
		ConnectDone: func(network, addr string, e error) {
			observation.add(CodexTicketProxyStage{Name: "tcp", Success: e == nil, DurationMS: time.Since(stageStart).Milliseconds()})
		},
		TLSHandshakeDone: func(cs tls.ConnectionState, e error) {
			observation.add(CodexTicketProxyStage{Name: "tls", Success: e == nil, DurationMS: time.Since(stageStart).Milliseconds()})
		},
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodHead, codexTicketProxyTarget, nil)
	if err != nil {
		return nil, result, err
	}
	resp, err := client.Do(req)
	observed, stages := observation.snapshot()
	result.Stages = append(result.Stages, stages...)
	if err != nil {
		result.Code = codexTicketTransportFailureCode(err)
		result.Message = CodexTicketFailure(result.Code).Message
		if strings.HasPrefix(u.Scheme, "socks") && errors.Is(err, context.DeadlineExceeded) {
			result.ProtocolSuggestion = "尝试确认该端口是否为HTTP CONNECT代理"
		}
		return nil, result, errors.New(result.Message)
	}
	_ = resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	result.NetworkReachable = true
	result.Success = resp.StatusCode >= 200 && resp.StatusCode < 400
	result.Code = "proxy_reachable"
	result.Message = "代理连接和TLS正常；连接测试不代表已取得292票据"
	if !result.Success {
		result.Code = "target_http_status"
		result.Message = fmt.Sprintf("连接和TLS正常，目标返回HTTP %d；未发送OAuth凭据，尚未验证打票能力", resp.StatusCode)
	}
	result.Stages = append(result.Stages, CodexTicketProxyStage{Name: "http", Success: result.Success, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)})
	result.CertificateTrust = "system"
	if observed != nil && len(observed.Certificates) > 0 {
		result.CertificateTrust = "proxy_certificate_pinned"
		result.CertificateFingerprint = observed.Fingerprint
		if persist && store != nil {
			if err = store.SaveCodexTicketProxyTrust(ctx, trustKey, observed); err != nil {
				return nil, result, err
			}
		}
	}
	finalTr, err := newCodexTicketProxyTransport(normal, observed, false, nil)
	if err != nil {
		return nil, result, err
	}
	return &codexTicketPreparedProxy{client: &http.Client{Transport: finalTr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: finalTr, trust: observed}, result, nil
}

func (s *OpenAIGatewayService) TestCodexTicketProxy(ctx context.Context, raw string) (*CodexTicketProxyTestResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	normalized, _ := NormalizeCodexTicketProxy(raw)
	configured, _ := NormalizeCodexTicketProxy(s.openAICodexTicketHarvestProxyURLContext(ctx))
	prepared, result, err := s.prepareCodexTicketProxy(ctx, raw, true, normalized != "" && normalized == configured)
	if prepared != nil {
		defer prepared.transport.CloseIdleConnections()
		// Validate the next, pinned connection too. Harvesting uses this same
		// verifier after preflight, so a rotating certificate cannot falsely pass
		// a draft test that only exercised first-use learning.
		if err == nil {
			started := time.Now()
			req, requestErr := http.NewRequestWithContext(ctx, http.MethodHead, codexTicketProxyTarget, nil)
			if requestErr != nil {
				return nil, requestErr
			}
			resp, followErr := prepared.client.Do(req)
			if followErr != nil {
				result.Success = false
				result.Code = codexTicketTransportFailureCode(followErr)
				result.Message = CodexTicketFailure(result.Code).Message
				result.Stages = append(result.Stages, CodexTicketProxyStage{Name: "pinned_connection", Success: false, DurationMS: time.Since(started).Milliseconds(), Message: result.Message})
				return result, nil
			}
			_ = resp.Body.Close()
			result.HTTPStatus = resp.StatusCode
			result.Success = resp.StatusCode >= 200 && resp.StatusCode < 400
			result.Stages = append(result.Stages, CodexTicketProxyStage{Name: "pinned_connection", Success: true, DurationMS: time.Since(started).Milliseconds(), Message: fmt.Sprintf("HTTP %d", resp.StatusCode)})
			if !result.Success {
				result.Code = "target_http_status"
				result.Message = fmt.Sprintf("连接和固定证书验证正常，目标返回HTTP %d；未发送OAuth凭据，尚未验证打票能力", resp.StatusCode)
			} else {
				result.Code = "proxy_reachable"
				result.Message = "代理连接和固定证书验证正常；连接测试不代表已取得292票据"
			}
		}
	}
	if err != nil && result != nil && result.Code != "" {
		return result, nil
	}
	return result, err
}

func codexTicketTransportFailureCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "ticket_canceled"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return "ticket_timeout"
	}
	t := strings.ToLower(err.Error())
	if strings.Contains(t, "407") || strings.Contains(t, "authentication") {
		return "ticket_proxy_auth"
	}
	if strings.Contains(t, "certificate") || strings.Contains(t, "tls") || strings.Contains(t, "x509") {
		return "ticket_proxy_tls"
	}
	if strings.Contains(t, "no such host") || strings.Contains(t, "name resolution") {
		return "ticket_proxy_dns"
	}
	if strings.Contains(t, "malformed http response") || strings.Contains(t, "unexpected protocol") || strings.Contains(t, "socks version") {
		return "ticket_proxy_protocol"
	}
	if strings.Contains(t, "connection refused") || strings.Contains(t, "connect rejected") {
		return "ticket_proxy_connect"
	}
	return "ticket_transport"
}

var ticketSensitiveValuePattern = regexp.MustCompile(`(?i)(?:bearer\s+\S+|(?:https?|socks5h?)://\S+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|sk-[A-Za-z0-9_-]+|gAAAAA[A-Za-z0-9_=-]+)`)

func codexTicketSafeUpstreamMessage(raw string, secretValues ...string) string {
	for _, v := range secretValues {
		if v != "" {
			raw = strings.ReplaceAll(raw, v, "[redacted]")
		}
	}
	raw = ticketSensitiveValuePattern.ReplaceAllString(sanitizeUpstreamErrorMessage(raw), "[redacted]")
	runes := []rune(raw)
	if len(runes) > 512 {
		runes = runes[:512]
	}
	return string(runes)
}

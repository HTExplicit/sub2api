package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketProxyInput(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"password: p@ss:#\nPort: 2721\nProxy Server: proxy.example.com\nusername: u+s", "http://u+s:p%40ss%3A%23@proxy.example.com:2721"},
		{"密码：abc\n用户名：user\n端口：8080\n主机：proxy.example.com", "http://user:abc@proxy.example.com:8080"},
		{"proxy.example.com:8080:user:p:a", "http://user:p%3Aa@proxy.example.com:8080"},
		{"user:pass@proxy.example.com:8080", "http://user:pass@proxy.example.com:8080"},
		{"socks5h://u:p@proxy.example.com:1080", "socks5h://u:p@proxy.example.com:1080"},
		{"[::1]:8080", "http://[::1]:8080"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := NormalizeCodexTicketProxy(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, input := range []string{"host: x\nport: 8080\nport: 90", "host: x\nport: 8080\nusername: u", "username: x\npassword: y", "user:pass:host:1234", "http://x:70000", "ftp://x:21"} {
		_, err := NormalizeCodexTicketProxy(input)
		require.Error(t, err)
	}
}

func ticketPrivateCertificate(t *testing.T, host string, expired bool) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Now()
	until := now.Add(time.Hour)
	if expired {
		until = now.Add(-time.Minute)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: now.Add(-time.Hour), NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, cert
}

func TestCodexTicketProxyTrustScopeAndRotation(t *testing.T) {
	_, cert := ticketPrivateCertificate(t, "chatgpt.com", false)
	cs := tls.ConnectionState{ServerName: "chatgpt.com", PeerCertificates: []*x509.Certificate{cert}}
	_, err := verifyCodexTicketProxyTLS(cs, nil, false)
	require.Error(t, err)
	trust, err := verifyCodexTicketProxyTLS(cs, nil, true)
	require.NoError(t, err)
	require.NotEmpty(t, trust.Fingerprint)
	_, err = verifyCodexTicketProxyTLS(cs, trust, false)
	require.NoError(t, err)
	_, other := ticketPrivateCertificate(t, "chatgpt.com", false)
	cs.PeerCertificates = []*x509.Certificate{other}
	_, err = verifyCodexTicketProxyTLS(cs, trust, false)
	require.Error(t, err, "automatic renewal cannot learn a changed certificate")
	next, err := verifyCodexTicketProxyTLS(cs, trust, true)
	require.NoError(t, err)
	require.NotEqual(t, trust.Fingerprint, next.Fingerprint)
	_, wrong := ticketPrivateCertificate(t, "another.example.com", false)
	cs.PeerCertificates = []*x509.Certificate{wrong}
	_, err = verifyCodexTicketProxyTLS(cs, nil, true)
	require.Error(t, err)
	_, expired := ticketPrivateCertificate(t, "chatgpt.com", true)
	cs.PeerCertificates = []*x509.Certificate{expired}
	_, err = verifyCodexTicketProxyTLS(cs, nil, true)
	require.Error(t, err)
	require.NotEqual(t, codexTicketProxyKey("http://u:p@one:8080"), codexTicketProxyKey("http://u:p@two:8080"))
}

func TestCodexTicketProxyCONNECTPreflightAndHarvestShareTrust(t *testing.T) {
	certificate, _ := ticketPrivateCertificate(t, "chatgpt.com", false)
	var mu sync.Mutex
	var methods, auth []string
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		require.Equal(t, "/backend-api/codex/responses", r.URL.Path)
		if r.Method == http.MethodHead {
			w.WriteHeader(401)
			return
		}
		w.Header().Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
		w.WriteHeader(200)
	}))
	target.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	target.StartTLS()
	defer target.Close()
	addr := target.Listener.Addr().String()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(405)
			return
		}
		require.Equal(t, "chatgpt.com:443", r.Host)
		if r.Header.Get("Proxy-Authorization") == "" {
			w.WriteHeader(407)
			return
		}
		dst, err := net.Dial("tcp", addr)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = dst.Close()
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() {
			defer conn.Close()
			defer dst.Close()
			go func() { _, _ = io.Copy(dst, conn) }()
			_, _ = io.Copy(conn, dst)
		}()
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("demo", "synthetic")
	svc := &OpenAIGatewayService{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	prepared, result, err := svc.prepareCodexTicketProxy(ctx, proxyURL.String(), true, false)
	require.NoError(t, err)
	defer prepared.transport.CloseIdleConnections()
	require.True(t, result.NetworkReachable)
	require.False(t, result.Success)
	require.Equal(t, 401, result.HTTPStatus)
	require.Equal(t, "proxy_certificate_pinned", result.CertificateTrust)
	a := ticketTestAccount(41)
	state, status, err := svc.fireCodexTicketProbe(ctx, a, "synthetic-oauth", "gpt-5.6-sol", proxyURL.String(), time.Second, prepared)
	require.NoError(t, err)
	require.Equal(t, 200, status)
	require.Len(t, state, 292)
	mu.Lock()
	require.Equal(t, []string{"HEAD", "POST"}, methods)
	require.Equal(t, []string{"", "Bearer synthetic-oauth"}, auth)
	mu.Unlock()
	// A regular system-verifying client remains unable to trust this certificate.
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, codexTicketProxyTarget, nil)
	_, err = (&http.Client{Transport: tr}).Do(req)
	require.Error(t, err)
}

func TestCodexTicketProxyAuthenticationFailureAndErrorRedaction(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(407) }))
	defer proxy.Close()
	svc := &OpenAIGatewayService{}
	result, err := svc.TestCodexTicketProxy(context.Background(), proxy.URL)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, "ticket_proxy_auth", result.Code)
	msg := codexTicketSafeUpstreamMessage("Bearer sensitive http://u:p@proxy:1 "+fakeCodexTicketState(292)+" secret-value", "secret-value")
	require.NotContains(t, msg, "sensitive")
	require.NotContains(t, msg, "u:p")
	require.NotContains(t, msg, "secret-value")
	require.False(t, strings.Contains(msg, "gAAAAA"))
}

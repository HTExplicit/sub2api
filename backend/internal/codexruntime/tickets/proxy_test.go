package tickets

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func syntheticCertificate(t *testing.T, hostname string, expired bool) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	until := now.Add(time.Hour)
	if expired {
		until = now.Add(-time.Minute)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: hostname}, DNSNames: []string{hostname}, NotBefore: now.Add(-time.Hour), NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}, parsed
}

func TestProxyPrivateCertificateCannotRotateDuringAuthenticatedRequest(t *testing.T) {
	_, first := syntheticCertificate(t, "chatgpt.com", false)
	state := tls.ConnectionState{ServerName: "chatgpt.com", PeerCertificates: []*x509.Certificate{first}}
	if _, err := verifyProxyCertificate(state, nil, false); err == nil {
		t.Fatal("untrusted certificate accepted without preflight")
	}
	trust, err := verifyProxyCertificate(state, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyProxyCertificate(state, trust, false); err != nil {
		t.Fatal(err)
	}
	_, changed := syntheticCertificate(t, "chatgpt.com", false)
	state.PeerCertificates = []*x509.Certificate{changed}
	if _, err := verifyProxyCertificate(state, trust, false); err == nil {
		t.Fatal("authenticated request learned a changed certificate")
	}
	for _, item := range []struct {
		host    string
		expired bool
	}{{"wrong.example", false}, {"chatgpt.com", true}} {
		_, cert := syntheticCertificate(t, item.host, item.expired)
		state.PeerCertificates = []*x509.Certificate{cert}
		if _, err := verifyProxyCertificate(state, nil, true); err == nil {
			t.Fatal("preflight bypassed hostname or validity checks")
		}
	}
}

func TestProxyPreflightAndHarvestUseSamePinnedTransport(t *testing.T) {
	certificate, _ := syntheticCertificate(t, "chatgpt.com", false)
	var mu sync.Mutex
	var methods, authorization []string
	material := "gAAAAA" + strings.Repeat("b", 286)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		authorization = append(authorization, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Error("unexpected target path")
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("x-codex-turn-state", material)
		w.WriteHeader(200)
	}))
	target.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	target.StartTLS()
	t.Cleanup(target.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "chatgpt.com:443" {
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("Proxy-Authorization") == "" {
			w.WriteHeader(407)
			return
		}
		upstream, err := net.Dial("tcp", target.Listener.Addr().String())
		if err != nil {
			w.WriteHeader(502)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() {
			defer client.Close()
			defer upstream.Close()
			go func() { _, _ = io.Copy(upstream, client) }()
			_, _ = io.Copy(client, upstream)
		}()
	}))
	t.Cleanup(proxy.Close)
	address, _ := url.Parse(proxy.URL)
	address.User = url.UserPassword("test", "test")
	host := &memoryHost{state: make(map[string]extensionv1.StateResult), lease: make(map[string]int64)}
	clientAPI := testHostClient(t, host)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, result, err := prepareProxy(ctx, clientAPI, address.String(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if result.Success || !result.NetworkReachable || result.HTTPStatus != 405 || result.CertificateTrust != "proxy_certificate_pinned" {
		t.Fatalf("preflight confused transport success with inference success: %+v", result)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"synthetic-model"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer synthetic-oauth")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("x-codex-turn-state") != material {
		t.Fatal("authenticated request did not use the pinned transport")
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(methods, ",") != "HEAD,HEAD,POST" {
		t.Fatalf("unexpected requests: %v", methods)
	}
	if authorization[0] != "" || authorization[1] != "" || authorization[2] != "Bearer synthetic-oauth" {
		t.Fatal("OAuth credential appeared in a preflight")
	}
}

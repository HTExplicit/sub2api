package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func routingProxyTestCertificate(t *testing.T, name string, expired bool) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	until := now.Add(time.Hour)
	if expired {
		until = now.Add(-time.Minute)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: now.Add(-time.Hour), NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, certificate
}

type routingProxySystemUpstream struct {
	HTTPUpstream
	client *http.Client
}

func (upstream *routingProxySystemUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return upstream.client.Do(request)
}

func TestCodexRoutingAcquisitionUsesOnlyExplicitProxyTrust(t *testing.T) {
	certificate, anchor := routingProxyTestCertificate(t, "chatgpt.com", false)
	var active atomic.Pointer[tls.Certificate]
	active.Store(&certificate)
	var requests, redirects atomic.Int64
	var redirect atomic.Bool
	sink := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirects.Add(1) }))
	t.Cleanup(sink.Close)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/backend-api/codex/responses" || request.Header.Get("Authorization") != "Bearer synthetic-oauth" || request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Error("acquisition credential or cold-cookie boundary changed")
		}
		if redirect.Load() {
			http.Redirect(w, request, sink.URL, http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	target.Config.ErrorLog = log.New(io.Discard, "", 0)
	target.TLS = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return active.Load(), nil }}
	target.StartTLS()
	t.Cleanup(target.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect || request.Host != "chatgpt.com:443" || request.Header.Get("Proxy-Authorization") == "" || request.Header.Get("Authorization") != "" {
			t.Error("CONNECT credential boundary changed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		upstream, err := net.Dial("tcp", target.Listener.Addr().String())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			_ = upstream.Close()
			t.Error("CONNECT fixture requires HTTP hijacking")
			return
		}
		client, _, err := hijacker.Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() {
			defer func() { _ = client.Close() }()
			defer func() { _ = upstream.Close() }()
			go func() { _, _ = io.Copy(upstream, client) }()
			_, _ = io.Copy(client, upstream)
		}()
	}))
	t.Cleanup(proxy.Close)
	address, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	address.User = url.UserPassword("proxy-user", "synthetic-password")
	transport, err := codexRoutingAcquisitionTransport(address.String(), nil)
	require.NoError(t, err)
	t.Cleanup(transport.CloseIdleConnections)
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	manager := nativeTicketTestRuntime(t, config.OpenAICodexTicketConfig{}, nil)
	manager.repo = store
	service := &OpenAIGatewayService{nativeCodexRuntime: manager, httpUpstream: &routingProxySystemUpstream{client: &http.Client{Transport: transport, Timeout: 3 * time.Second}}}
	send := func() (*http.Response, error) {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"synthetic-model"}`))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer synthetic-oauth")
		request.Header.Set("Cookie", "__cflb=old-business-cookie")
		return service.doCodexRoutingAcquisition(request, address.String(), 7)
	}
	_, err = send()
	require.Error(t, err, "an untrusted certificate must not be learned during acquisition")
	require.Zero(t, requests.Load(), "OAuth was sent before certificate verification")
	require.Empty(t, store.values)
	trust := codexRoutingProxyTrust{Certificates: [][]byte{anchor.Raw}, Fingerprint: codexRoutingDigest(string(anchor.RawSubjectPublicKeyInfo))}
	raw, err := json.Marshal(trust)
	require.NoError(t, err)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: "proxy-trust", Key: codexRoutingDigest(address.String(), "chatgpt.com"), Value: raw})
	require.NoError(t, err)
	response, err := send()
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.EqualValues(t, 1, requests.Load())
	rotated, _ := routingProxyTestCertificate(t, "chatgpt.com", false)
	active.Store(&rotated)
	_, err = send()
	require.Error(t, err)
	require.EqualValues(t, 1, requests.Load(), "changed certificate received OAuth")
	require.Len(t, store.values, 1, "acquisition must never write proxy trust")
	active.Store(&certificate)
	redirect.Store(true)
	response, err = send()
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, http.StatusTemporaryRedirect, response.StatusCode)
	require.Zero(t, redirects.Load(), "OAuth request followed a redirect")
	for _, test := range []struct {
		name    string
		expired bool
	}{{"proxy.example", false}, {"chatgpt.com", true}} {
		_, certificate := routingProxyTestCertificate(t, test.name, test.expired)
		roots := x509.NewCertPool()
		roots.AddCert(certificate)
		require.Error(t, verifyCodexRoutingProxyCertificate(tls.ConnectionState{ServerName: test.name, PeerCertificates: []*x509.Certificate{certificate}}, roots))
	}
}

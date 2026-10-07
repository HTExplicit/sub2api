package repository

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func codexBorrowProbeTestConfig(maxClients int) *config.Config {
	return &config.Config{Gateway: config.GatewayConfig{
		ConnectionPoolIsolation: config.ConnectionPoolIsolationAccountProxy,
		MaxIdleConns:            4,
		MaxIdleConnsPerHost:     2,
		MaxConnsPerHost:         2,
		MaxUpstreamClients:      maxClients,
		ClientIdleTTLSeconds:    900,
		OpenAIHTTP2: config.GatewayOpenAIHTTP2Config{
			Enabled:                   true,
			AllowProxyFallbackToHTTP1: true,
			FallbackErrorThreshold:    1,
		},
	}}
}

func codexBorrowProbeTestRequest(t *testing.T, purpose service.HTTPUpstreamProfile, rawURL string) *http.Request {
	t.Helper()
	ctx := service.WithHTTPUpstreamProfile(t.Context(), purpose)
	ctx = service.WithHTTPUpstreamRedirectsDisabled(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	return req
}

func closeCodexBorrowProbeTestClients(t *testing.T, managers ...*httpUpstreamService) {
	t.Helper()
	t.Cleanup(func() {
		for _, manager := range managers {
			manager.mu.RLock()
			clients := make([]*http.Client, 0, len(manager.clients))
			for _, entry := range manager.clients {
				clients = append(clients, entry.client)
			}
			manager.mu.RUnlock()
			for _, client := range clients {
				client.CloseIdleConnections()
			}
		}
	})
}

func TestCodexGatewayBorrowProbeUpstream_IsolatesClientBudgetsAndFallbacks(t *testing.T) {
	cfg := codexBorrowProbeTestConfig(1)
	business, ok := NewHTTPUpstream(cfg).(*httpUpstreamService)
	require.True(t, ok)
	probe, ok := NewCodexGatewayBorrowProbeUpstream(cfg).(*codexGatewayBorrowProbeUpstream)
	require.True(t, ok)
	closeCodexBorrowProbeTestClients(t, business, probe.source, probe.target)
	require.NotSame(t, cfg, probe.source.cfg)
	require.NotSame(t, cfg, probe.target.cfg)
	require.NotSame(t, probe.source.cfg, probe.target.cfg)

	businessEntry, err := business.acquireClientWithProfile("", 7, 1, service.HTTPUpstreamProfileOpenAI)
	require.NoError(t, err)
	sourceEntry, err := probe.source.acquireClientWithProfile("", 7, 0, service.HTTPUpstreamProfileOpenAI)
	require.NoError(t, err)
	targetEntry, err := probe.target.acquireClientWithProfile("", 7, 0, service.HTTPUpstreamProfileCodexBorrowTarget)
	require.NoError(t, err)
	require.NotSame(t, businessEntry.client, sourceEntry.client)
	require.NotSame(t, businessEntry.client, targetEntry.client)
	require.NotSame(t, sourceEntry.client, targetEntry.client)

	_, err = probe.source.acquireClientWithProfile("", 8, 0, service.HTTPUpstreamProfileOpenAI)
	require.ErrorIs(t, err, errUpstreamClientLimitReached)
	// Releasing one source client permits only that manager's eviction.
	atomic.AddInt64(&sourceEntry.inFlight, -1)
	replacement, err := probe.source.acquireClientWithProfile("", 8, 0, service.HTTPUpstreamProfileOpenAI)
	require.NoError(t, err)
	require.NotSame(t, sourceEntry, replacement)
	require.Len(t, business.clients, 1)
	require.Len(t, probe.source.clients, 1)
	require.Len(t, probe.target.clients, 1)
	for _, entry := range business.clients {
		require.Same(t, businessEntry, entry)
	}
	for _, entry := range probe.target.clients {
		require.Same(t, targetEntry, entry)
	}
	for key := range probe.source.clients {
		require.True(t, strings.HasPrefix(key, string(service.HTTPUpstreamProfileCodexBorrowSource)+"|"))
	}
	for key := range probe.target.clients {
		require.True(t, strings.HasPrefix(key, string(service.HTTPUpstreamProfileCodexBorrowTarget)+"|"))
	}

	proxyKey := "http://probe-proxy.example:8080"
	parsedProxy, err := url.Parse(proxyKey)
	require.NoError(t, err)
	probe.source.recordOpenAIHTTP2Failure(service.HTTPUpstreamProfileOpenAI, upstreamProtocolModeOpenAIH2, proxyKey, errors.New("GOAWAY"))
	require.Equal(t, upstreamProtocolModeOpenAIH1Fallback, probe.source.resolveProtocolMode(service.HTTPUpstreamProfileOpenAI, proxyKey, parsedProxy))
	require.Equal(t, upstreamProtocolModeOpenAIH2, business.resolveProtocolMode(service.HTTPUpstreamProfileOpenAI, proxyKey, parsedProxy))
	require.False(t, probe.target.isOpenAIHTTP2FallbackActive(proxyKey))
	require.Equal(t, upstreamProtocolModeCodexBorrowTargetH1, probe.target.resolveProtocolMode(service.HTTPUpstreamProfileCodexBorrowTarget, proxyKey, parsedProxy))

	// TLS clients consume the same bounded cache within their own purpose only.
	_, err = probe.source.acquireClientWithTLS("", 9, 0, &tlsfingerprint.Profile{Name: "test"}, service.HTTPUpstreamProfileOpenAI)
	require.ErrorIs(t, err, errUpstreamClientLimitReached)
	require.Same(t, businessEntry, business.clients[buildCacheKey(config.ConnectionPoolIsolationAccountProxy, directProxyKey, 7, upstreamProtocolModeOpenAIH2)])
}

func TestCodexGatewayBorrowProbeUpstream_UsesGlobalProbeCapacity(t *testing.T) {
	for _, purpose := range []service.HTTPUpstreamProfile{
		service.HTTPUpstreamProfileCodexBorrowSource,
		service.HTTPUpstreamProfileCodexBorrowTarget,
	} {
		t.Run(string(purpose), func(t *testing.T) {
			cfg := codexBorrowProbeTestConfig(2)
			cfg.Security.URLAllowlist.UpstreamHosts = []string{"original.example"}
			probe, ok := NewCodexGatewayBorrowProbeUpstream(cfg).(*codexGatewayBorrowProbeUpstream)
			require.True(t, ok)
			closeCodexBorrowProbeTestClients(t, probe.source, probe.target)
			cfg.Gateway.MaxConnsPerHost = 1
			cfg.Security.URLAllowlist.UpstreamHosts[0] = "changed.example"
			require.Equal(t, 2, probe.source.cfg.Gateway.MaxConnsPerHost)
			require.Equal(t, "original.example", probe.target.cfg.Security.URLAllowlist.UpstreamHosts[0])

			arrived := make(chan struct{}, 2)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				arrived <- struct{}{}
				<-release
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(unblock)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			results := make(chan error, 2)
			for range 2 {
				req := codexBorrowProbeTestRequest(t, purpose, srv.URL)
				req = req.WithContext(service.WithHTTPUpstreamProfile(ctx, purpose))
				go func() {
					resp, err := probe.Do(req, "", 7, 1)
					if err == nil {
						err = resp.Body.Close()
					}
					results <- err
				}()
			}
			for range 2 {
				select {
				case <-arrived:
				case <-ctx.Done():
					unblock()
					t.Fatal("probe requests inherited the account concurrency of one")
				}
			}
			unblock()
			for range 2 {
				require.NoError(t, <-results)
			}
		})
	}
}

func TestCodexGatewayBorrowProbeUpstream_TargetUsesFreshHTTP1Connections(t *testing.T) {
	type observedRequest struct {
		protocol int
		remote   string
		close    bool
		cookie   string
		identity string
	}
	observed := make(chan observedRequest, 3)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedRequest{r.ProtoMajor, r.RemoteAddr, r.Close, r.Header.Get("Cookie"), r.Header.Get("User-Agent")}
		http.SetCookie(w, &http.Cookie{Name: "__cflb", Value: "response-cookie"})
		_, _ = io.WriteString(w, "probe")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	probe, ok := NewCodexGatewayBorrowProbeUpstream(codexBorrowProbeTestConfig(4)).(*codexGatewayBorrowProbeUpstream)
	require.True(t, ok)
	closeCodexBorrowProbeTestClients(t, probe.source, probe.target)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	for _, pair := range []struct {
		manager *httpUpstreamService
		profile service.HTTPUpstreamProfile
	}{
		{probe.source, service.HTTPUpstreamProfileOpenAI},
		{probe.target, service.HTTPUpstreamProfileCodexBorrowTarget},
	} {
		entry, err := pair.manager.getClientEntry("", 7, 0, pair.profile, false, true)
		require.NoError(t, err)
		require.Nil(t, entry.client.Jar)
		transport, ok := entry.client.Transport.(*http.Transport)
		require.True(t, ok)
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport.TLSClientConfig.RootCAs = roots
	}

	for _, purpose := range []service.HTTPUpstreamProfile{
		service.HTTPUpstreamProfileCodexBorrowSource,
		service.HTTPUpstreamProfileCodexBorrowTarget,
		service.HTTPUpstreamProfileCodexBorrowTarget,
	} {
		req := codexBorrowProbeTestRequest(t, purpose, srv.URL)
		req.Header.Set("Cookie", "__cflb=explicit-target-cookie")
		req.Header.Set("User-Agent", "codex-probe-identity")
		resp, err := probe.Do(req, "", 7, 1)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.False(t, req.Close, "wrapper must not mutate the original request")
	}
	source, firstTarget, secondTarget := <-observed, <-observed, <-observed
	require.Equal(t, 2, source.protocol, "source retains configured OpenAI protocol policy")
	for _, target := range []observedRequest{firstTarget, secondTarget} {
		require.Equal(t, 1, target.protocol)
		require.True(t, target.close)
		require.Equal(t, "__cflb=explicit-target-cookie", target.cookie)
		require.Equal(t, "codex-probe-identity", target.identity)
	}
	require.NotEqual(t, firstTarget.remote, secondTarget.remote, "target attempts require new TCP connections")
}

func TestCodexGatewayBorrowProbeUpstream_TLSProfileCacheAndTargetALPN(t *testing.T) {
	probe, ok := NewCodexGatewayBorrowProbeUpstream(codexBorrowProbeTestConfig(8)).(*codexGatewayBorrowProbeUpstream)
	require.True(t, ok)
	closeCodexBorrowProbeTestClients(t, probe.source, probe.target)
	profile := &tlsfingerprint.Profile{
		Name:          "bound-profile",
		CipherSuites:  []uint16{tls.TLS_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
		ALPNProtocols: []string{"h2", "http/1.1"},
	}
	first, err := probe.source.getClientEntryWithTLS("", 7, 0, profile, service.HTTPUpstreamProfileOpenAI, false, true)
	require.NoError(t, err)
	second, err := probe.source.getClientEntryWithTLS("", 7, 0, cloneCodexBorrowTLSProfile(profile), service.HTTPUpstreamProfileOpenAI, false, true)
	require.NoError(t, err)
	require.Same(t, first, second)
	changed := cloneCodexBorrowTLSProfile(profile)
	changed.CipherSuites[0] = tls.TLS_AES_256_GCM_SHA384
	third, err := probe.source.getClientEntryWithTLS("", 7, 0, changed, service.HTTPUpstreamProfileOpenAI, false, true)
	require.NoError(t, err)
	require.NotSame(t, first, third, "same-name profiles with different TLS values require separate clients")

	targetEntry, err := probe.target.getClientEntryWithTLS("", 7, 0, profile, service.HTTPUpstreamProfileCodexBorrowTarget, false, true)
	require.NoError(t, err)
	require.NotSame(t, first.client, targetEntry.client)
	targetTransport, ok := targetEntry.client.Transport.(*http.Transport)
	require.True(t, ok)
	require.True(t, targetTransport.DisableKeepAlives)
	require.False(t, targetTransport.ForceAttemptHTTP2)
	require.False(t, targetTransport.Protocols.HTTP2())
	require.True(t, targetTransport.Protocols.HTTP1())
	require.NotNil(t, targetTransport.DialTLSContext)
	require.Equal(t, codexBorrowProbeHeaderTimeout, targetTransport.ResponseHeaderTimeout)
	require.Contains(t, targetEntry.poolKey, upstreamProtocolModeCodexBorrowTargetH1)
	require.Nil(t, targetEntry.client.Jar)
	for key := range probe.target.clients {
		require.Contains(t, key, "|profile:"+codexBorrowTLSProfileKey(profile))
		require.Contains(t, key, upstreamProtocolModeCodexBorrowTargetH1)
	}

	// Read the actual ClientHello, then reject it before certificate verification.
	// This checks the configured dialers without an external TLS/model request.
	type hello struct {
		protocols []string
		ciphers   []uint16
	}
	hellos := make(chan hello, 2)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			tlsConn := tls.Server(conn, &tls.Config{GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
				hellos <- hello{slices.Clone(info.SupportedProtos), slices.Clone(info.CipherSuites)}
				return nil, errors.New("local ClientHello capture complete")
			}})
			_ = tlsConn.Handshake()
			_ = conn.Close()
		}
	}()
	for _, purpose := range []service.HTTPUpstreamProfile{service.HTTPUpstreamProfileCodexBorrowSource, service.HTTPUpstreamProfileCodexBorrowTarget} {
		req := codexBorrowProbeTestRequest(t, purpose, "https://"+listener.Addr().String())
		resp, err := probe.DoWithTLS(req, "", 7, 1, profile)
		require.Error(t, err, "local capture rejects the handshake after observing ClientHello")
		require.Nil(t, resp)
	}
	readHello := func() hello {
		select {
		case captured := <-hellos:
			return captured
		case <-time.After(5 * time.Second):
			t.Fatal("fingerprint request did not send a ClientHello")
			return hello{}
		}
	}
	sourceHello, targetHello := readHello(), readHello()
	require.Equal(t, profile.ALPNProtocols, sourceHello.protocols)
	require.Equal(t, []string{"http/1.1"}, targetHello.protocols)
	require.Equal(t, profile.CipherSuites, sourceHello.ciphers)
	require.Equal(t, profile.CipherSuites, targetHello.ciphers)
	require.Equal(t, []string{"h2", "http/1.1"}, profile.ALPNProtocols, "target must not change the caller's profile")
}

func TestCodexGatewayBorrowProbeUpstream_PreservesProxyAndURLPolicy(t *testing.T) {
	for _, purpose := range []service.HTTPUpstreamProfile{service.HTTPUpstreamProfileCodexBorrowSource, service.HTTPUpstreamProfileCodexBorrowTarget} {
		t.Run(string(purpose), func(t *testing.T) {
			var calls atomic.Int64
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(proxy.Close)
			probe, ok := NewCodexGatewayBorrowProbeUpstream(nil).(*codexGatewayBorrowProbeUpstream)
			require.True(t, ok)
			closeCodexBorrowProbeTestClients(t, probe.source, probe.target)
			req := codexBorrowProbeTestRequest(t, purpose, "http://upstream.invalid/borrow")
			resp, err := probe.DoWithTLS(req, proxy.URL, 7, 1, &tlsfingerprint.Profile{Name: "unused-over-http"})
			require.NoError(t, err)
			require.Equal(t, http.StatusNoContent, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, int64(1), calls.Load())

			cfg := codexBorrowProbeTestConfig(2)
			cfg.Security.URLAllowlist.Enabled = true
			protected := NewCodexGatewayBorrowProbeUpstream(cfg)
			req = codexBorrowProbeTestRequest(t, purpose, "https://127.0.0.1/borrow")
			_, err = protected.Do(req, proxy.URL, 7, 1)
			require.Error(t, err)
			require.Equal(t, int64(1), calls.Load(), "blocked private URL must not reach the proxy")
		})
	}

	probe, ok := NewCodexGatewayBorrowProbeUpstream(nil).(*codexGatewayBorrowProbeUpstream)
	require.True(t, ok)
	proxyURL, err := url.Parse("https://proxy.example:8443")
	require.NoError(t, err)
	transport, err := probe.target.buildCodexBorrowTLSFingerprintTransport(poolSettings{}, proxyURL, &tlsfingerprint.Profile{Name: "bound"})
	require.NoError(t, err)
	// Preserve the existing HTTPS-proxy fingerprint fallback, still with H1 and
	// no connection reuse for the target, rather than bypassing the proxy.
	require.NotNil(t, transport.Proxy)
	require.Nil(t, transport.DialTLSContext)
	require.True(t, transport.DisableKeepAlives)
	require.False(t, transport.Protocols.HTTP2())
	resolved, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "upstream.example"}})
	require.NoError(t, err)
	require.Equal(t, proxyURL.String(), resolved.String())
}

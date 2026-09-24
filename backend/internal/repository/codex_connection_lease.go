package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type codexConnectionLease struct {
	accountID     int64
	scope         string
	origin        string
	transportHash [32]byte
	expiresAt     time.Time
	client        *http.Client
	transport     *http.Transport
	alive         *atomic.Bool
}

type codexLeasedConn struct {
	net.Conn
	alive *atomic.Bool
}

func (c *codexLeasedConn) Close() error {
	c.alive.Store(false)
	return c.Conn.Close()
}

func (s *httpUpstreamService) CheckCodexConnectionLease(accountID int64, scope, leaseID string, expiresAt time.Time) error {
	s.mu.RLock()
	lease := s.codexConnectionLeases[leaseID]
	s.mu.RUnlock()
	now := time.Now()
	if lease == nil || lease.alive == nil || !lease.alive.Load() || !now.Before(lease.expiresAt) || !now.Before(expiresAt) {
		return service.ErrCodexConnectionLeaseExpired
	}
	if accountID <= 0 || lease.accountID != accountID || scope == "" || lease.scope != scope || expiresAt.After(lease.expiresAt) {
		return service.ErrCodexConnectionLeaseScope
	}
	return nil
}

func (s *httpUpstreamService) DoWithCodexConnectionLease(req *http.Request, proxyURL string, accountID int64, scope string, leaseID string, expiresAt time.Time, profile *tlsfingerprint.Profile) (*http.Response, string, error) {
	if req == nil || req.URL == nil || accountID <= 0 || scope == "" || len(scope) > 1024 {
		return nil, "", service.ErrCodexConnectionLeaseScope
	}
	if err := s.validateRequestHost(req); err != nil {
		return nil, "", service.ErrCodexConnectionLeaseScope
	}
	proxyKey, parsedProxy, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return nil, "", service.ErrCodexConnectionLeaseScope
	}
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return nil, "", service.ErrCodexConnectionLeaseScope
	}
	transportHash := sha256.Sum256(append([]byte(proxyKey+"\x00"), profileJSON...))
	origin := strings.ToLower(req.URL.Scheme + "://" + req.URL.Host)
	now := time.Now()
	var lease *codexConnectionLease
	if leaseID != "" {
		s.mu.RLock()
		lease = s.codexConnectionLeases[leaseID]
		s.mu.RUnlock()
		if lease == nil || !now.Before(lease.expiresAt) {
			return nil, "", service.ErrCodexConnectionLeaseExpired
		}
		if lease.accountID != accountID || lease.scope != scope || lease.origin != origin || lease.transportHash != transportHash {
			return nil, "", service.ErrCodexConnectionLeaseScope
		}
		checkDeadline := expiresAt
		if checkDeadline.IsZero() {
			checkDeadline = lease.expiresAt
		}
		if err := s.CheckCodexConnectionLease(accountID, scope, leaseID, checkDeadline); err != nil {
			return nil, "", err
		}
	} else {
		if expiresAt.IsZero() || expiresAt.After(now.Add(120*time.Second)) {
			expiresAt = now.Add(120 * time.Second)
		}
		if !expiresAt.After(now) {
			return nil, "", service.ErrCodexConnectionLeaseExpired
		}
		settings := defaultPoolSettings(s.cfg)
		settings.maxIdleConns, settings.maxIdleConnsPerHost, settings.maxConnsPerHost = 1, 1, 1
		settings.idleConnTimeout = time.Until(expiresAt)
		transport, buildErr := buildUpstreamTransport(settings, parsedProxy, upstreamProtocolModeOpenAIH1)
		if profile != nil && strings.EqualFold(req.URL.Scheme, "https") {
			transport, buildErr = buildUpstreamTransportWithTLSFingerprint(settings, parsedProxy, profile)
		}
		if buildErr != nil {
			return nil, "", service.ErrCodexConnectionLeaseTransport
		}
		transport.DisableCompression = true
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
		// Exactly one physical dial is authorized. A disconnected/expired lease
		// must fail before any request can reach a different residential exit.
		var dialed atomic.Bool
		alive := &atomic.Bool{}
		wrap := func(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
			return func(ctx context.Context, network, address string) (net.Conn, error) {
				if !time.Now().Before(expiresAt) || !dialed.CompareAndSwap(false, true) {
					return nil, service.ErrCodexConnectionLeaseExpired
				}
				connection, err := dial(ctx, network, address)
				if err != nil {
					return nil, err
				}
				alive.Store(true)
				return &codexLeasedConn{Conn: connection, alive: alive}, nil
			}
		}
		if transport.DialContext != nil {
			transport.DialContext = wrap(transport.DialContext)
		}
		if transport.DialTLSContext != nil {
			transport.DialTLSContext = wrap(transport.DialTLSContext)
		}
		var id [24]byte
		if _, err := rand.Read(id[:]); err != nil {
			transport.CloseIdleConnections()
			return nil, "", service.ErrCodexConnectionLeaseTransport
		}
		leaseID = hex.EncodeToString(id[:])
		lease = &codexConnectionLease{accountID: accountID, scope: scope, origin: origin, transportHash: transportHash,
			expiresAt: expiresAt, transport: transport, alive: alive,
			client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		s.mu.Lock()
		if s.codexConnectionLeases == nil {
			s.codexConnectionLeases = make(map[string]*codexConnectionLease)
		}
		if len(s.codexConnectionLeases) >= s.maxUpstreamClients() {
			s.mu.Unlock()
			transport.CloseIdleConnections()
			return nil, "", service.ErrCodexConnectionLeaseTransport
		}
		s.codexConnectionLeases[leaseID] = lease
		s.mu.Unlock()
		createdID := leaseID
		time.AfterFunc(time.Until(expiresAt), func() { s.removeCodexConnectionLease(createdID, lease) })
	}
	// Keep caller request state immutable. Closing an expired idle pool does not
	// interrupt a response admitted before expiry; it only prevents later use.
	wire := req.Clone(req.Context())
	wire.Header = req.Header.Clone()
	wire.Close = false
	response, err := lease.client.Do(wire)
	if err != nil {
		s.removeCodexConnectionLease(leaseID, lease)
		if errors.Is(err, service.ErrCodexConnectionLeaseExpired) {
			return nil, "", service.ErrCodexConnectionLeaseExpired
		}
		return nil, "", service.ErrCodexConnectionLeaseTransport
	}
	decompressResponseBody(response)
	restoreCodexEventStreamContentType(response)
	return response, leaseID, nil
}

func (s *httpUpstreamService) removeCodexConnectionLease(id string, lease *codexConnectionLease) {
	s.mu.Lock()
	if s.codexConnectionLeases[id] == lease {
		delete(s.codexConnectionLeases, id)
	}
	s.mu.Unlock()
	lease.transport.CloseIdleConnections()
}

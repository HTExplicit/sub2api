package repository

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	upstreamProtocolModeCodexBorrowTargetH1 = "codex_borrow_target_h1_no_reuse"
	codexBorrowProbeHeaderTimeout           = 45 * time.Second
)

// codexGatewayBorrowProbeUpstream owns two independent client managers. In
// particular, probes cannot consume the business client's eviction budget or
// change its HTTP/2 proxy fallback state, even for the same account and proxy.
type codexGatewayBorrowProbeUpstream struct {
	source *httpUpstreamService
	target *httpUpstreamService
}

func NewCodexGatewayBorrowProbeUpstream(cfg *config.Config) service.CodexGatewayBorrowProbeUpstream {
	newManager := func(purpose service.HTTPUpstreamProfile) *httpUpstreamService {
		return &httpUpstreamService{
			cfg:                cloneCodexBorrowProbeHTTPConfig(cfg),
			clients:            make(map[string]*upstreamClientEntry),
			codexBorrowPurpose: purpose,
		}
	}
	return &codexGatewayBorrowProbeUpstream{
		source: newManager(service.HTTPUpstreamProfileCodexBorrowSource),
		target: newManager(service.HTTPUpstreamProfileCodexBorrowTarget),
	}
}

func (s *codexGatewayBorrowProbeUpstream) Do(req *http.Request, proxyURL string, accountID int64, _ int) (*http.Response, error) {
	manager, req, err := s.requestManager(req)
	if err != nil {
		return nil, err
	}
	// Probe capacity follows the copied global transport settings, independently
	// of the account's gateway concurrency slots and business pool dimensions.
	return manager.Do(req, proxyURL, accountID, 0)
}

func (s *codexGatewayBorrowProbeUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	manager, req, err := s.requestManager(req)
	if err != nil {
		return nil, err
	}
	return manager.DoWithTLS(req, proxyURL, accountID, 0, profile)
}

func (s *codexGatewayBorrowProbeUpstream) requestManager(req *http.Request) (*httpUpstreamService, *http.Request, error) {
	if req == nil || req.URL == nil {
		return nil, nil, errors.New("borrow probe request URL is nil")
	}
	switch service.HTTPUpstreamProfileFromContext(req.Context()) {
	case service.HTTPUpstreamProfileCodexBorrowSource:
		// Source sends the normal OpenAI transport policy through its own pool.
		ctx := service.WithHTTPUpstreamProfile(req.Context(), service.HTTPUpstreamProfileOpenAI)
		return s.source, req.Clone(ctx), nil
	case service.HTTPUpstreamProfileCodexBorrowTarget:
		clone := req.Clone(req.Context())
		clone.Close = true
		return s.target, clone, nil
	default:
		return nil, nil, errors.New("borrow probe request purpose is missing")
	}
}

// Copy only the configuration read by the upstream transport. The managers
// neither mutate nor retain slices belonging to the business configuration.
func cloneCodexBorrowProbeHTTPConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	allowlist := cfg.Security.URLAllowlist
	allowlist.UpstreamHosts = slices.Clone(allowlist.UpstreamHosts)
	allowlist.PricingHosts = slices.Clone(allowlist.PricingHosts)
	allowlist.CRSHosts = slices.Clone(allowlist.CRSHosts)
	return &config.Config{
		Gateway: config.GatewayConfig{
			ConnectionPoolIsolation:     cfg.Gateway.ConnectionPoolIsolation,
			MaxIdleConns:                cfg.Gateway.MaxIdleConns,
			MaxIdleConnsPerHost:         cfg.Gateway.MaxIdleConnsPerHost,
			MaxConnsPerHost:             cfg.Gateway.MaxConnsPerHost,
			IdleConnTimeoutSeconds:      cfg.Gateway.IdleConnTimeoutSeconds,
			ResponseHeaderTimeout:       cfg.Gateway.ResponseHeaderTimeout,
			OpenAIResponseHeaderTimeout: cfg.Gateway.OpenAIResponseHeaderTimeout,
			OpenAIHTTP2:                 cfg.Gateway.OpenAIHTTP2,
			MaxUpstreamClients:          cfg.Gateway.MaxUpstreamClients,
			ClientIdleTTLSeconds:        cfg.Gateway.ClientIdleTTLSeconds,
		},
		Security: config.SecurityConfig{URLAllowlist: allowlist},
	}
}

func (s *httpUpstreamService) codexBorrowClientCacheKey(key string) string {
	if s.codexBorrowPurpose == service.HTTPUpstreamProfileDefault {
		return key
	}
	return string(s.codexBorrowPurpose) + "|" + key
}

// Fingerprinted probe clients must be keyed by the full resolved profile, not
// merely its display name. A bound profile can change without changing its name.
func codexBorrowTLSProfileKey(profile *tlsfingerprint.Profile) string {
	encoded, _ := json.Marshal(profile) // Profile contains only JSON-safe values.
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func cloneCodexBorrowTLSProfile(profile *tlsfingerprint.Profile) *tlsfingerprint.Profile {
	if profile == nil {
		return nil
	}
	clone := *profile
	clone.CipherSuites = slices.Clone(profile.CipherSuites)
	clone.Curves = slices.Clone(profile.Curves)
	clone.PointFormats = slices.Clone(profile.PointFormats)
	clone.SignatureAlgorithms = slices.Clone(profile.SignatureAlgorithms)
	clone.ALPNProtocols = slices.Clone(profile.ALPNProtocols)
	clone.SupportedVersions = slices.Clone(profile.SupportedVersions)
	clone.KeyShareGroups = slices.Clone(profile.KeyShareGroups)
	clone.PSKModes = slices.Clone(profile.PSKModes)
	clone.Extensions = slices.Clone(profile.Extensions)
	return &clone
}

func (s *httpUpstreamService) buildCodexBorrowTLSFingerprintTransport(settings poolSettings, proxyURL *url.URL, profile *tlsfingerprint.Profile) (*http.Transport, error) {
	if s.codexBorrowPurpose != service.HTTPUpstreamProfileCodexBorrowTarget {
		return buildUpstreamTransportWithTLSFingerprint(settings, proxyURL, profile)
	}
	// Keep the selected cipher suites, extensions and other fingerprint fields,
	// while excluding H2 from the actual ClientHello used by every target dialer.
	profile = cloneCodexBorrowTLSProfile(profile)
	if profile != nil {
		profile.ALPNProtocols = []string{"http/1.1"}
	}
	transport, err := buildUpstreamTransportWithTLSFingerprint(settings, proxyURL, profile)
	if err != nil {
		return nil, err
	}
	configureCodexBorrowTargetTransport(transport)
	return transport, nil
}

func configureCodexBorrowTargetTransport(transport *http.Transport) {
	transport.ForceAttemptHTTP2 = false
	transport.DisableKeepAlives = true
	transport.DisableCompression = true
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	transport.Protocols = &http.Protocols{}
	transport.Protocols.SetHTTP1(true)
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
}

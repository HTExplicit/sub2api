package repository

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codextransport"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *httpUpstreamService) acquireCodexClient(proxyURL string, accountID int64, concurrency int, binding codextransport.Binding) (*upstreamClientEntry, error) {
	isolation := s.getIsolationMode()
	proxyKey, parsed, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	settings := s.applyProfilePoolSettings(s.resolvePoolSettings(isolation, concurrency), service.HTTPUpstreamProfileOpenAI)
	key := "codex:" + binding.Key() + ":" + buildCacheKey(isolation, proxyKey, accountID, "codex_verified")
	poolKey := buildPoolKey(settings, "codex_verified") + ":" + binding.Key()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if entry, ok := s.clients[key]; ok {
		if s.shouldReuseEntry(entry, isolation, proxyKey, poolKey) {
			atomic.StoreInt64(&entry.lastUsed, now.UnixNano())
			atomic.AddInt64(&entry.inFlight, 1)
			return entry, nil
		}
		s.removeClientLocked(key, entry)
	}
	if s.maxUpstreamClients() > 0 && len(s.clients) >= s.maxUpstreamClients() {
		s.evictIdleLocked(now)
		if len(s.clients) >= s.maxUpstreamClients() && !s.evictOldestIdleLocked() {
			return nil, errUpstreamClientLimitReached
		}
	}
	rt, err := codextransport.New(binding, codextransport.Options{ProxyURL: parsed, DialContext: newUpstreamDialer().DialContext,
		MaxIdleConns: settings.maxIdleConns, MaxIdleConnsPerHost: settings.maxIdleConnsPerHost, MaxConnsPerHost: settings.maxConnsPerHost,
		IdleConnTimeout: settings.idleConnTimeout, ResponseHeaderTimeout: settings.responseHeaderTimeout})
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: rt}
	if s.shouldValidateResolvedIP() {
		client.CheckRedirect = s.redirectChecker
	}
	entry := &upstreamClientEntry{client: client, proxyKey: proxyKey, poolKey: poolKey, protocolMode: "codex_verified", lastUsed: now.UnixNano(), inFlight: 1}
	s.clients[key] = entry
	return entry, nil
}

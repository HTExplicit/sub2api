// Adapted from ranxi2001/sub2api d977e7a5, astra_rotation_cache.go (LGPL-3.0).
// Each model owns an independent instance; the upstream cache rules are unchanged.
package service

import (
	"sync"
	"time"
)

// Cache decisions survive settings revisions, not process restarts. They never
// contain Cookies or credentials, and are not evidence of an upstream cache TTL.
type codexBorrowRotationCache struct {
	mu      sync.Mutex
	blocked map[codexBorrowRotationCacheKey]time.Time
	hosts   map[string]codexBorrowNodeHost
}
type codexBorrowRotationCacheKey struct {
	target      int64
	kind, value string
}
type codexBorrowNodeHost struct {
	host    string
	expires time.Time
}

func (c *codexBorrowRotationCache) prune(now time.Time) {
	for k, v := range c.blocked {
		if !now.Before(v) {
			delete(c.blocked, k)
		}
	}
	for k, v := range c.hosts {
		if !now.Before(v.expires) {
			delete(c.hosts, k)
		}
	}
}
func (c *codexBorrowRotationCache) observe(node, host string, now time.Time) {
	if node == "" || host == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.hosts == nil {
		c.hosts = map[string]codexBorrowNodeHost{}
	}
	if len(c.hosts) >= 4096 {
		for k := range c.hosts {
			delete(c.hosts, k)
			break
		}
	}
	c.hosts[node] = codexBorrowNodeHost{host: host, expires: now.Add(24 * time.Hour)}
}
func (c *codexBorrowRotationCache) reject(target int64, node, host string, until, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.blocked == nil {
		c.blocked = map[codexBorrowRotationCacheKey]time.Time{}
	}
	for kind, value := range map[string]string{"node": node, "host": host} {
		if value == "" {
			continue
		}
		k := codexBorrowRotationCacheKey{target: target, kind: kind, value: value}
		// A late duplicate observation must not slide an existing quiet window.
		if _, exists := c.blocked[k]; exists {
			continue
		}
		if len(c.blocked) >= 16384 {
			for old := range c.blocked {
				delete(c.blocked, old)
				break
			}
		}
		c.blocked[k] = until
	}
}
func (c *codexBorrowRotationCache) blockedUntil(target int64, node, host string, now time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if host == "" {
		host = c.hosts[node].host
	}
	a := c.blocked[codexBorrowRotationCacheKey{target: target, kind: "node", value: node}]
	b := c.blocked[codexBorrowRotationCacheKey{target: target, kind: "host", value: host}]
	if b.After(a) {
		return b
	}
	return a
}

// Source transport/qualification failures are scoped to the donor and node,
// independently from the longer target-host quiet window.
const codexBorrowSourceFailureCooldown = time.Minute

func (c *codexBorrowRotationCache) sourceCooling(source int64, node string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	return now.Before(c.blocked[codexBorrowRotationCacheKey{target: source, kind: "source", value: node}])
}

func (c *codexBorrowRotationCache) sourcesBlockedUntil(ids []int64, node string, now time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	var earliest time.Time
	for _, id := range ids {
		until := c.blocked[codexBorrowRotationCacheKey{target: id, kind: "source", value: node}]
		if !now.Before(until) {
			return time.Time{}
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	return earliest
}
func (c *codexBorrowRotationCache) rejectSource(source int64, node string, now time.Time) {
	if node == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.blocked == nil {
		c.blocked = map[codexBorrowRotationCacheKey]time.Time{}
	}
	key := codexBorrowRotationCacheKey{target: source, kind: "source", value: node}
	if _, ok := c.blocked[key]; ok {
		return
	}
	if len(c.blocked) >= 16384 {
		for old := range c.blocked {
			delete(c.blocked, old)
			break
		}
	}
	c.blocked[key] = now.Add(codexBorrowSourceFailureCooldown)
}

// A new exit which rediscovers a cooling host must not trigger another full
// source sweep on every request. Keep the pause across settings revisions.
func (c *codexBorrowRotationCache) pauseSourceSearch(target int64, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.blocked == nil {
		c.blocked = map[codexBorrowRotationCacheKey]time.Time{}
	}
	key := codexBorrowRotationCacheKey{target: target, kind: "source_search"}
	if _, exists := c.blocked[key]; !exists {
		c.blocked[key] = now.Add(5 * time.Minute)
	}
}
func (c *codexBorrowRotationCache) sourceSearchPausedUntil(target int64, now time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	return c.blocked[codexBorrowRotationCacheKey{target: target, kind: "source_search"}]
}

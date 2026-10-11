package service

// Node selection and cooling follow ranxi2001/sub2api d977e7a5:
// repository/astra_rotation{,_cache}.go (LGPL-3.0). Registered host proxies
// replace Mihomo's private listeners. Proxy/account rows are never modified.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type codexBorrowNode struct {
	id                    int64
	identity, name, proxy string
	expires               *time.Time
}

type codexBorrowNodeBinding struct {
	owner          *CodexGatewayBorrowService
	child          *CodexGatewayBorrowService
	revision       uint64
	accountID      int64
	model, gateway string
	node           codexBorrowNode
}

type codexBorrowNodeContextKey struct{}

type CodexGatewayBorrowNodeStatus struct {
	AccountID    int64      `json:"account_id"`
	Model        string     `json:"model"`
	ProxyID      int64      `json:"proxy_id"`
	ProxyName    string     `json:"proxy_name"`
	NodeIdentity string     `json:"node_identity"`
	Gateway      string     `json:"gateway,omitempty"`
	State        string     `json:"state"`
	RetryAfter   *time.Time `json:"retry_after,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Reason       string     `json:"reason,omitempty"`
}

type codexBorrowRotation struct {
	mu          sync.Mutex
	revision    uint64
	children    map[string]*CodexGatewayBorrowService
	active      map[codexGatewayBorrowTargetKey]string
	cursor      map[codexGatewayBorrowTargetKey]int
	retry       map[codexGatewayBorrowTargetKey]time.Time
	caches      map[string]*codexBorrowRotationCache
	sourceCache codexBorrowRotationCache
	leases      map[string]time.Time
	flights     codexBorrowFlights
}

func validateCodexBorrowNodeConfig(cfg CodexGatewayBorrowConfig) error {
	if cfg.MaxNodeAttempts < 0 || cfg.MaxNodeAttempts > 10 {
		return errors.New("node attempts must be 0–10 (0 uses 3)")
	}
	if cfg.NodeCooldownSeconds != 0 && (cfg.NodeCooldownSeconds < 60 || cfg.NodeCooldownSeconds > 86400) {
		return errors.New("node cooldown must be 60–86400 seconds (0 uses 3600)")
	}
	if len(cfg.NodeProxyIDs) > 64 || (cfg.RotateNodes && len(cfg.NodeProxyIDs) == 0) {
		return errors.New("node selection requires 1–64 explicit proxy IDs")
	}
	seen := map[int64]bool{}
	for _, id := range cfg.NodeProxyIDs {
		if id < 0 || seen[id] {
			return errors.New("node proxy IDs must be unique; 0 explicitly selects server direct")
		}
		seen[id] = true
	}
	return nil
}

func borrowNodeConfigEqual(a, b CodexGatewayBorrowConfig) bool {
	return a.RotateNodes == b.RotateNodes && a.MaxNodeAttempts == b.MaxNodeAttempts && a.NodeCooldownSeconds == b.NodeCooldownSeconds && slices.Equal(a.NodeProxyIDs, b.NodeProxyIDs)
}

func borrowNodeFromProxy(p *Proxy) (codexBorrowNode, error) {
	if p == nil || p.Status != StatusActive || (p.ExpiresAt != nil && !time.Now().Before(*p.ExpiresAt)) || (p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "socks5" && p.Protocol != "socks5h") {
		return codexBorrowNode{}, errors.New("node proxy is unavailable")
	}
	url := p.URL()
	return codexBorrowNode{id: p.ID, identity: borrowHash(url), name: p.Name, proxy: url, expires: p.ExpiresAt}, nil
}

func (s *CodexGatewayBorrowService) borrowNodes(ctx context.Context, ids []int64, strict ...bool) ([]codexBorrowNode, error) {
	if s.proxies == nil {
		return nil, errors.New("node proxy repository unavailable")
	}
	databaseIDs := []int64{}
	for _, id := range ids {
		if id > 0 {
			databaseIDs = append(databaseIDs, id)
		}
	}
	rows, err := s.proxies.ListByIDs(ctx, databaseIDs)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Proxy{}
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}
	seen := map[string]bool{}
	nodes := []codexBorrowNode{}
	for _, id := range ids {
		if id == 0 {
			nodes = append(nodes, codexBorrowNode{id: 0, identity: borrowHash("codex-borrow-server-direct"), name: "Server direct"})
			continue
		}
		node, err := borrowNodeFromProxy(byID[id])
		if err != nil {
			if len(strict) > 0 && strict[0] {
				return nil, fmt.Errorf("proxy %d: %w", id, err)
			}
			continue
		}
		if !seen[node.identity] {
			nodes = append(nodes, node)
			seen[node.identity] = true
		}
	}
	return nodes, nil
}

func (r *codexBorrowRotation) cache(model string) *codexBorrowRotationCache {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.caches == nil {
		r.caches = map[string]*codexBorrowRotationCache{}
	}
	if r.caches[model] == nil {
		r.caches[model] = &codexBorrowRotationCache{}
	}
	return r.caches[model]
}

func (s *CodexGatewayBorrowService) nodeChild(rev uint64, cfg CodexGatewayBorrowConfig, node codexBorrowNode) (*CodexGatewayBorrowService, error) {
	r := &s.rotation
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.initializeLocked(rev); err != nil {
		return nil, err
	}
	if child := r.children[node.identity]; child != nil {
		return child, nil
	}
	// Reuse the tested cookie/probe implementation with a separate cache for
	// each immutable node. Its gateway dependency still belongs to the host.
	child := NewCodexGatewayBorrowService(nil, s.accounts, nil, s.upstream, s.tlsProfiles)
	child.gateway = s.gateway
	child.node = &node
	child.nodeLease = r.cookieExpiry
	cfg.RotateNodes = false
	child.publishConfig(cfg, false)
	r.children[node.identity] = child
	return child, nil
}

func (r *codexBorrowRotation) initializeLocked(rev uint64) error {
	if r.revision > rev {
		return ErrCodexGatewayBorrowChanged
	}
	if r.revision != rev || r.children == nil {
		for _, child := range r.children {
			child.Stop()
		}
		r.children = map[string]*CodexGatewayBorrowService{}
		r.active = map[codexGatewayBorrowTargetKey]string{}
		r.cursor = map[codexGatewayBorrowTargetKey]int{}
		r.retry = map[codexGatewayBorrowTargetKey]time.Time{}
		r.revision = rev
	}
	return nil
}

func (r *codexBorrowRotation) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, child := range r.children {
		child.Stop()
	}
}

func (r *codexBorrowRotation) cookieExpiry(value string, proposed time.Time) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	key := borrowHash(value)
	if r.leases == nil {
		r.leases = map[string]time.Time{}
	}
	if old, ok := r.leases[key]; ok && now.Before(old) && old.Before(proposed) {
		return old
	}
	if len(r.leases) >= 4096 {
		for k, v := range r.leases {
			if !now.Before(v) {
				delete(r.leases, k)
			}
		}
		if len(r.leases) >= 4096 {
			for k := range r.leases {
				delete(r.leases, k)
				break
			}
		}
	}
	r.leases[key] = proposed
	return proposed
}

var codexBorrowGatewayPattern = regexp.MustCompile(`^chat\.gateway\.unified-[0-9]+\.api\.openai\.com$`)

// Descriptive routing metadata only, never JWT verification or model identity.
func codexBorrowGateway(value string) string {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Host string `json:"host"`
	}
	if json.Unmarshal(raw, &claims) != nil || !codexBorrowGatewayPattern.MatchString(claims.Host) {
		return ""
	}
	return claims.Host
}

func (s *CodexGatewayBorrowService) nodeCurrent(ctx context.Context, b *codexBorrowNodeBinding) error {
	if b == nil || b.owner != s || s.proxies == nil {
		return ErrCodexGatewayBorrowChanged
	}
	s.mu.Lock()
	current := s.config.Enabled && s.config.RotateNodes && s.revision == b.revision && s.revisionCtx.Err() == nil && slices.Contains(s.config.NodeProxyIDs, b.node.id)
	s.mu.Unlock()
	if !current {
		return ErrCodexGatewayBorrowChanged
	}
	if b.node.id != 0 {
		p, err := s.proxies.GetByID(ctx, b.node.id)
		if err != nil {
			return ErrCodexGatewayBorrowChanged
		}
		n, err := borrowNodeFromProxy(p)
		if err != nil || n.identity != b.node.identity {
			return ErrCodexGatewayBorrowChanged
		}
	} else if b.node.proxy != "" || b.node.identity != borrowHash("codex-borrow-server-direct") {
		return ErrCodexGatewayBorrowChanged
	}
	if time.Now().Before(s.rotation.cache(b.model).blockedUntil(b.accountID, b.node.identity, b.gateway, time.Now())) {
		return &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Stage: "node_selection", Reason: "node_cooling"}
	}
	return nil
}

func borrowNodeBinding(req *http.Request) *codexBorrowNodeBinding {
	if req == nil {
		return nil
	}
	if p, ok := req.Context().Value(codexGatewayBorrowHTTPPreparationContextKey{}).(codexGatewayBorrowHTTPPreparation); ok && p.application != nil && p.application.node != nil {
		return p.application.node
	}
	b, _ := req.Context().Value(codexBorrowNodeContextKey{}).(*codexBorrowNodeBinding)
	return b
}

func (s *CodexGatewayBorrowService) applyNode(req *http.Request, account *Account, model string, profile *tlsfingerprint.Profile, cachedOnly, force bool, b *codexBorrowNodeBinding) (*http.Request, *CodexGatewayBorrowApplication, error) {
	if b.accountID != account.ID || b.model != model {
		return req, nil, ErrCodexGatewayBorrowChanged
	}
	if err := s.nodeCurrent(req.Context(), b); err != nil {
		return req, nil, err
	}
	wire, app, err := b.child.apply(req, account, model, b.node.proxy, profile, cachedOnly, force)
	if app != nil {
		app.node = b
		app.Verification.ProxyID = b.node.id
		app.Verification.ProxyName = b.node.name
		app.Verification.NodeIdentity = b.node.identity
		app.Verification.Gateway = b.gateway
		wire = wire.WithContext(context.WithValue(wire.Context(), codexBorrowNodeContextKey{}, b))
		wire = wire.WithContext(WithHTTPUpstreamProfile(wire.Context(), HTTPUpstreamProfileOpenAIHarvest))
	}
	return wire, app, err
}

func (s *CodexGatewayBorrowService) applyRotated(req *http.Request, account *Account, model, _ string, profile *tlsfingerprint.Profile, cachedOnly, force bool, rev uint64, revisionCtx context.Context) (*http.Request, *CodexGatewayBorrowApplication, error) {
	if bound := borrowNodeBinding(req); bound != nil && cachedOnly {
		return s.applyNode(req, account, model, profile, true, force, bound)
	}
	cfg := s.ConfigSnapshot()
	key := codexGatewayBorrowTargetKey{account.ID, model}
	r := &s.rotation
	cache := r.cache(model)
	nodes, err := s.borrowNodes(req.Context(), cfg.NodeProxyIDs)
	if err != nil {
		return req, nil, &CodexGatewayBorrowFailure{Cause: err, Stage: "node_selection", Reason: "node_unavailable"}
	}
	if len(nodes) == 0 {
		return req, nil, ErrCodexGatewayBorrowUnavailable
	}
	// Cached-only dispatch may use the selected node, never search or probe.
	if cachedOnly {
		r.mu.Lock()
		identity := r.active[key]
		child := r.children[identity]
		r.mu.Unlock()
		for _, node := range nodes {
			if node.identity == identity && child != nil {
				child.mu.Lock()
				c := child.currentCandidateLocked()
				host := ""
				if c != nil {
					host = codexBorrowGateway(c.cookie.Value)
				}
				child.mu.Unlock()
				return s.applyNode(req, account, model, profile, true, force, &codexBorrowNodeBinding{s, child, rev, account.ID, model, host, node})
			}
		}
		return req, nil, ErrCodexGatewayBorrowUnavailable
	}
	r.mu.Lock()
	initializeErr := r.initializeLocked(rev)
	r.mu.Unlock()
	if initializeErr != nil {
		return req, nil, initializeErr
	}
	flightKey := fmt.Sprintf("%d:%d:%s:%s", rev, account.ID, model, borrowRequestFingerprint(req, model, "", profile, ""))
	value, err := r.flights.do(req.Context(), revisionCtx, flightKey, 180*time.Second, func(ctx context.Context) (any, error) {
		limit := cfg.MaxNodeAttempts
		if limit == 0 {
			limit = 3
		}
		seen := map[string]bool{}
		attempts := 0
		transientTargetFailure := false
		var lastErr error
		for scanned := 0; scanned < len(nodes)+1 && attempts < limit; scanned++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			s.mu.Lock()
			current := s.revision == rev && s.config.Enabled
			s.mu.Unlock()
			if !current {
				return nil, ErrCodexGatewayBorrowChanged
			}
			r.mu.Lock()
			identity := r.active[key]
			retry := r.retry[key]
			child := r.children[identity]
			r.mu.Unlock()
			if pausedUntil := cache.sourceSearchPausedUntil(account.ID, time.Now()); pausedUntil.After(retry) {
				retry = pausedUntil
			}
			if time.Now().Before(retry) {
				return nil, &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Stage: "node_selection", Reason: "node_search_cooling", RetryAfter: &retry}
			}
			var node codexBorrowNode
			if scanned == 0 && child != nil {
				child.mu.Lock()
				candidate := child.currentCandidateLocked()
				live := candidate != nil && time.Now().Before(candidate.expires)
				child.mu.Unlock()
				if live {
					for _, n := range nodes {
						if n.identity == identity {
							node = n
							break
						}
					}
				}
			}
			if node.identity == "" {
				r.mu.Lock()
				cursor := r.cursor[key]
				node = nodes[cursor%len(nodes)]
				if r.cursor == nil {
					r.cursor = map[codexGatewayBorrowTargetKey]int{}
				}
				r.cursor[key] = cursor + 1
				r.mu.Unlock()
			}
			if seen[node.identity] || time.Now().Before(cache.blockedUntil(account.ID, node.identity, "", time.Now())) {
				continue
			}
			allSourcesCooling := true
			for _, id := range cfg.SourceAccountIDs {
				if !r.sourceCache.sourceCooling(id, node.identity, time.Now()) {
					allSourcesCooling = false
				}
			}
			if allSourcesCooling {
				continue
			}
			seen[node.identity] = true
			attempts++
			child, err := s.nodeChild(rev, cfg, node)
			if err != nil {
				return nil, err
			}
			child.mu.Lock()
			candidate := child.currentCandidateLocked()
			childRev, childCtx := child.revision, child.revisionCtx
			child.mu.Unlock()
			if candidate == nil || !time.Now().Before(candidate.expires) {
				sourceCtx, sourceCancel := context.WithTimeout(ctx, 45*time.Second)
				err = child.prepareSource(sourceCtx, childRev, childCtx)
				sourceCancel()
				if err != nil {
					lastErr = err
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					for _, id := range cfg.SourceAccountIDs {
						r.sourceCache.rejectSource(id, node.identity, time.Now())
					}
					continue
				}
				child.mu.Lock()
				candidate = child.currentCandidateLocked()
				child.mu.Unlock()
			}
			if candidate == nil || !time.Now().Before(candidate.expires) {
				lastErr = ErrCodexGatewayBorrowUnavailable
				continue
			}
			host := codexBorrowGateway(candidate.cookie.Value)
			cache.observe(node.identity, host, time.Now())
			if time.Now().Before(cache.blockedUntil(account.ID, node.identity, host, time.Now())) {
				cache.pauseSourceSearch(account.ID, time.Now())
				retry := cache.sourceSearchPausedUntil(account.ID, time.Now())
				return nil, &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Stage: "node_selection", Reason: "gateway_cooling", RetryAfter: &retry}
			}
			binding := &codexBorrowNodeBinding{s, child, rev, account.ID, model, host, node}
			_, app, err := s.applyNode(req.WithContext(ctx), account, model, profile, false, force, binding)
			s.publishNodeObservation(rev, key, binding, candidate)
			if err == nil && app != nil {
				r.mu.Lock()
				if r.revision != rev {
					r.mu.Unlock()
					return nil, ErrCodexGatewayBorrowChanged
				}
				r.active[key] = node.identity
				delete(r.retry, key)
				r.mu.Unlock()
				return binding, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var failure *CodexGatewayBorrowFailure
			if errors.As(err, &failure) && failure.Verification != nil {
				seconds := cfg.NodeCooldownSeconds
				if seconds == 0 {
					seconds = 3600
				}
				if failure.Verification.RetryOnNextRequest {
					// Preserve v16: an interrupted target probe has no negative
					// cache. The next independent request runs a full new pair.
					transientTargetFailure = true
				} else {
					cache.reject(account.ID, node.identity, host, time.Now().Add(time.Duration(seconds)*time.Second), time.Now())
				}
			} else if errors.Is(err, ErrCodexGatewayBorrowChanged) {
				return nil, err
			}
			r.mu.Lock()
			if r.active[key] == node.identity {
				delete(r.active, key)
			}
			r.mu.Unlock()
		}
		retry := time.Now().Add(15 * time.Second)
		if transientTargetFailure {
			retry = time.Time{}
		}
		r.mu.Lock()
		if r.revision != rev {
			r.mu.Unlock()
			return nil, ErrCodexGatewayBorrowChanged
		}
		if r.retry == nil {
			r.retry = map[codexGatewayBorrowTargetKey]time.Time{}
		}
		r.retry[key] = retry
		r.mu.Unlock()
		failure := &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Stage: "node_selection", Reason: "node_candidates_exhausted", RetryAfter: &retry}
		if retry.IsZero() {
			failure.RetryAfter = nil
		}
		if lastErr != nil {
			failure.Detail = lastErr.Error()
			var previous *CodexGatewayBorrowFailure
			if errors.As(lastErr, &previous) {
				failure.Verification = previous.Verification
			}
		}
		return nil, failure
	})
	if err != nil {
		return req, nil, err
	}
	binding, ok := value.(*codexBorrowNodeBinding)
	if !ok {
		return req, nil, ErrCodexGatewayBorrowUnavailable
	}
	return s.applyNode(req, account, model, profile, true, false, binding)
}

func (s *CodexGatewayBorrowService) publishNodeObservation(rev uint64, key codexGatewayBorrowTargetKey, b *codexBorrowNodeBinding, candidate *codexGatewayBorrowCandidate) {
	b.child.mu.Lock()
	check := b.child.targets[key]
	sources := make(map[int64]CodexGatewayBorrowSourceStatus, len(b.child.sources))
	for id, row := range b.child.sources {
		sources[id] = row
	}
	b.child.mu.Unlock()
	check.result.ProxyID = b.node.id
	check.result.ProxyName = b.node.name
	check.result.NodeIdentity = b.node.identity
	check.result.Gateway = b.gateway
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != rev {
		return
	}
	s.targets[key] = check
	if s.rotationCandidates == nil {
		s.rotationCandidates = map[codexGatewayBorrowTargetKey]*codexGatewayBorrowCandidate{}
	}
	s.rotationCandidates[key] = candidate
	s.candidate = candidate
	for id, row := range sources {
		s.sources[id] = row
	}
}

func (r *codexBorrowRotation) snapshot(cfg CodexGatewayBorrowConfig, revision uint64) []CodexGatewayBorrowNodeStatus {
	if !cfg.RotateNodes {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	out := []CodexGatewayBorrowNodeStatus{}
	for _, id := range cfg.TargetAccountIDs {
		for _, model := range cfg.Models {
			for identity, child := range r.children {
				if child.node == nil {
					continue
				}
				node := child.node
				if !slices.Contains(cfg.NodeProxyIDs, node.id) {
					continue
				}
				row := CodexGatewayBorrowNodeStatus{AccountID: id, Model: model, ProxyID: node.id, ProxyName: node.name, NodeIdentity: identity, State: "candidate"}
				child.mu.Lock()
				candidate := child.currentCandidateLocked()
				if candidate != nil {
					expires := candidate.expires
					row.ExpiresAt = &expires
				}
				row.Reason = child.targets[codexGatewayBorrowTargetKey{id, model}].result.Reason
				child.mu.Unlock()
				if cache := r.caches[model]; cache != nil {
					cache.mu.Lock()
					row.Gateway = cache.hosts[identity].host
					cache.mu.Unlock()
					until := cache.blockedUntil(id, identity, row.Gateway, now)
					if now.Before(until) {
						row.State = "cooling"
						row.RetryAfter = &until
					}
				}
				if row.State != "cooling" {
					until := r.sourceCache.sourcesBlockedUntil(cfg.SourceAccountIDs, identity, now)
					if now.Before(until) {
						row.State = "cooling"
						row.Reason = "source_probe_failed"
						row.RetryAfter = &until
					}
				}
				if row.State != "cooling" && r.revision == revision && r.active[codexGatewayBorrowTargetKey{id, model}] == identity {
					row.State = "selected"
					if row.ExpiresAt == nil || !now.Before(*row.ExpiresAt) {
						row.State = "expired"
					}
				}
				if row.State == "candidate" && row.ExpiresAt != nil && !now.Before(*row.ExpiresAt) {
					row.State = "expired"
				}
				out = append(out, row)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.AccountID != b.AccountID {
			return a.AccountID < b.AccountID
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.ProxyID < b.ProxyID
	})
	return out
}

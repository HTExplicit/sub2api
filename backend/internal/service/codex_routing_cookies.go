package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

const codexRoutingPrivateNamespace = "codex-routing-private"

var errCodexRoutingUnavailable = errors.New("codex routing qualification unavailable")

type codexRoutingCookie struct {
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	Domain    string    `json:"domain"`
	Path      string    `json:"path"`
	FirstSeen time.Time `json:"first_seen"`
	ExpiresAt time.Time `json:"expires_at"`
}

type codexRoutingCookieChange struct {
	Cookie codexRoutingCookie
	Delete bool
}

// A clock keeps tombstones and first-seen times independently of bundles. A
// failed or late validation cannot resurrect deleted material or renew its age.
type codexRoutingCookieClock struct {
	Scope      extensionv1.CodexRoutingScope `json:"scope"`
	Cookies    map[string]codexRoutingCookie `json:"cookies"`
	Seen       map[string]time.Time          `json:"seen"`
	Tombstones map[string]time.Time          `json:"tombstones"`
}

type codexRoutingPrivateBundle struct {
	Schema        int                                 `json:"schema"`
	Scope         extensionv1.CodexRoutingScope       `json:"scope"`
	Cookies       []codexRoutingCookie                `json:"cookies"`
	ClockKey      string                              `json:"clock_key"`
	ClockRevision int64                               `json:"clock_revision"`
	Status        string                              `json:"status"`
	Model         string                              `json:"model"`
	Observation   extensionv1.CodexRoutingObservation `json:"observation"`
	ExpiresAt     time.Time                           `json:"expires_at"`
}

func codexRoutingDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func codexRoutingScopeKey(scope extensionv1.CodexRoutingScope) string {
	raw, _ := json.Marshal([]any{scope.AccountID, scope.Identity, scope.ProfileHash, scope.RouteHash})
	return codexRoutingDigest(string(raw))
}

func isCodexRoutingCookie(name string) bool { return name == "__cflb" || name == "__oailb" }

func parseCodexRoutingCookies(headers http.Header, target *url.URL, now time.Time) []codexRoutingCookieChange {
	if target == nil || (target.Scheme != "https" && target.Scheme != "wss") || target.Hostname() != "chatgpt.com" {
		return nil
	}
	var changes []codexRoutingCookieChange
	for _, cookie := range (&http.Response{Header: headers}).Cookies() {
		if !isCodexRoutingCookie(cookie.Name) || cookie.Valid() != nil || len(cookie.Value) > 8192 {
			continue
		}
		invalidScope := false
		for _, attribute := range cookie.Unparsed {
			name, _, _ := strings.Cut(attribute, "=")
			if strings.EqualFold(strings.TrimSpace(name), "domain") || strings.EqualFold(strings.TrimSpace(name), "path") {
				invalidScope = true
				break
			}
		}
		if invalidScope {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		if domain != "" && domain != target.Hostname() {
			continue
		}
		path := cookie.Path
		if path == "" || !strings.HasPrefix(path, "/") {
			path = target.Path[:strings.LastIndex(target.Path, "/")+1]
		}
		if !codexCookiePathMatches(target.Path, path) {
			continue
		}
		change := codexRoutingCookieChange{Cookie: codexRoutingCookie{Name: cookie.Name, Domain: target.Hostname(), Path: path, Value: cookie.Value, FirstSeen: now}}
		change.Delete = cookie.MaxAge < 0 || cookie.Value == "" || (!cookie.Expires.IsZero() && !now.Before(cookie.Expires) && cookie.MaxAge <= 0)
		deadline := now.Add(extensionv1.CodexRoutingMaxAge)
		if cookie.MaxAge > 0 {
			deadline = earlierCodexTime(deadline, now.Add(time.Duration(cookie.MaxAge)*time.Second))
		} else if !cookie.Expires.IsZero() {
			deadline = earlierCodexTime(deadline, cookie.Expires)
		}
		change.Cookie.ExpiresAt = deadline
		changes = append(changes, change)
	}
	return changes
}

func codexCookiePathMatches(path, scope string) bool {
	return path == scope || (strings.HasPrefix(path, scope) && (strings.HasSuffix(scope, "/") || (len(path) > len(scope) && path[len(scope)] == '/')))
}

func earlierCodexTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func (clock *codexRoutingCookieClock) apply(changes []codexRoutingCookieChange, now time.Time) {
	if clock.Cookies == nil {
		clock.Cookies = make(map[string]codexRoutingCookie)
	}
	if clock.Seen == nil {
		clock.Seen = make(map[string]time.Time)
	}
	if clock.Tombstones == nil {
		clock.Tombstones = make(map[string]time.Time)
	}
	// Never make the same value fresh merely because its old bundle expired.
	// A bounded history fails closed on exhaustion rather than renewing age.
	for _, change := range changes {
		cookie := change.Cookie
		if change.Delete {
			delete(clock.Cookies, cookie.Name)
			clock.Tombstones[cookie.Name] = now
			continue
		}
		digest := codexRoutingDigest(cookie.Name, cookie.Value)
		if first, found := clock.Seen[digest]; found {
			cookie.FirstSeen = first
		} else {
			clock.Seen[digest] = cookie.FirstSeen
		}
		if deleted, found := clock.Tombstones[cookie.Name]; found && !cookie.FirstSeen.After(deleted) {
			continue
		}
		cookie.ExpiresAt = earlierCodexTime(cookie.ExpiresAt, cookie.FirstSeen.Add(extensionv1.CodexRoutingMaxAge))
		if !now.Before(cookie.ExpiresAt) {
			continue
		}
		clock.Cookies[cookie.Name] = cookie
	}
	// The durable per-value index owns history. Keep this hot snapshot small;
	// older live bundle lookups can consult that index after eviction.
	for len(clock.Seen) > 128 {
		oldestKey := ""
		var oldest time.Time
		for key, first := range clock.Seen {
			if oldestKey == "" || first.Before(oldest) {
				oldestKey, oldest = key, first
			}
		}
		delete(clock.Seen, oldestKey)
	}
}

// First-seen records are indexed separately from the compact clock. Retaining
// only recent digests in a clock must never make an old identical value fresh.
func preserveCodexCookieFirstSeen(ctx context.Context, store NativeCodexStateStore, plugin string, scope extensionv1.CodexRoutingScope, clock *codexRoutingCookieClock, changes []codexRoutingCookieChange, now time.Time) error {
	for index := range changes {
		change := &changes[index]
		if change.Delete {
			continue
		}
		digest := codexRoutingDigest(change.Cookie.Name, change.Cookie.Value)
		key := "seen." + codexRoutingDigest(codexRoutingScopeKey(scope), digest)
		record, err := store.ReadExtensionState(ctx, plugin, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key})
		if err != nil {
			return err
		}
		var seen struct {
			First time.Time `json:"first_seen"`
		}
		if record.Found {
			if json.Unmarshal(record.Value, &seen) != nil || seen.First.IsZero() {
				return errCodexRoutingUnavailable
			}
		} else {
			seen.First = change.Cookie.FirstSeen
			if old, exists := clock.Seen[digest]; exists && old.Before(seen.First) {
				seen.First = old
			}
			raw, _ := json.Marshal(seen)
			saved, err := store.CompareSwapExtensionState(ctx, plugin, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: key, Value: raw})
			if err != nil {
				return err
			}
			if !saved.Applied && (json.Unmarshal(saved.Value, &seen) != nil || seen.First.IsZero()) {
				return errCodexRoutingUnavailable
			}
		}
		change.Cookie.FirstSeen = seen.First
	}
	for digest, first := range clock.Seen {
		if now.Sub(first) > 2*extensionv1.CodexRoutingMaxAge {
			delete(clock.Seen, digest)
		}
	}
	return nil
}

// Only cookies from this response can enter a cold acquisition candidate.
// Verification overlays this response onto its own immutable candidate, never
// onto another model's concurrently observed cookie pair.
func (clock *codexRoutingCookieClock) selected(previous []codexRoutingCookie, changes []codexRoutingCookieChange, now time.Time) []codexRoutingCookie {
	selected := map[string]codexRoutingCookie{}
	for _, cookie := range previous {
		selected[cookie.Name] = cookie
	}
	for _, change := range changes {
		if change.Delete {
			delete(selected, change.Cookie.Name)
			continue
		}
		cookie, exists := clock.Cookies[change.Cookie.Name]
		if exists && cookie.Value == change.Cookie.Value {
			selected[cookie.Name] = cookie
		}
	}
	var result []codexRoutingCookie
	for _, cookie := range selected {
		if deleted, exists := clock.Tombstones[cookie.Name]; exists && !cookie.FirstSeen.After(deleted) {
			continue
		}
		if now.Before(cookie.ExpiresAt) {
			result = append(result, cookie)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (clock *codexRoutingCookieClock) live(now time.Time) []codexRoutingCookie {
	var out []codexRoutingCookie
	for _, cookie := range clock.Cookies {
		if !cookie.FirstSeen.After(now) && now.Before(cookie.ExpiresAt) {
			out = append(out, cookie)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func codexRoutingCookieHeader(cookies []codexRoutingCookie, now time.Time) (string, time.Time) {
	parts := make([]string, 0, len(cookies))
	expires := now.Add(extensionv1.CodexRoutingMaxAge)
	for _, cookie := range cookies {
		if !isCodexRoutingCookie(cookie.Name) || cookie.Domain != "chatgpt.com" || cookie.FirstSeen.After(now) || !now.Before(cookie.ExpiresAt) || !codexCookiePathMatches("/backend-api/codex/responses", cookie.Path) {
			return "", time.Time{}
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
		expires = earlierCodexTime(expires, cookie.ExpiresAt)
	}
	if len(parts) == 0 {
		return "", time.Time{}
	}
	return strings.Join(parts, "; "), expires
}

func changedCodexRoutingCookies(previous []codexRoutingCookie, changes []codexRoutingCookieChange) []codexRoutingCookieChange {
	var result []codexRoutingCookieChange
	for _, change := range changes {
		unchanged := false
		if !change.Delete {
			for _, cookie := range previous {
				if cookie.Name == change.Cookie.Name && cookie.Value == change.Cookie.Value && cookie.Domain == change.Cookie.Domain && cookie.Path == change.Cookie.Path && !change.Cookie.ExpiresAt.Before(cookie.ExpiresAt) {
					unchanged = true
					break
				}
			}
		}
		if !unchanged {
			result = append(result, change)
		}
	}
	return result
}

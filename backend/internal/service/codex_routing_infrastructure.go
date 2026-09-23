package service

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"golang.org/x/net/publicsuffix"
)

// Infrastructure cookies do not confer model qualification. They are a
// separate allowlisted jar tied to an actual connection, never global to users
// and never carried to a freshly rotated proxy connection.
type codexInfrastructureCookies struct {
	mu      sync.Mutex
	jar     *cookiejar.Jar
	expires time.Time
}

var codexInfrastructureJars sync.Map // opaque connection scope -> bounded-lived jar

func isCodexInfrastructureCookie(name string) bool {
	switch name {
	case "__cf_bm", "__cflb", "__cfruid", "__cfseq", "__cfwaitingroom", "__oailb", "_cfuvid", "cf_clearance", "cf_ob_info", "cf_use_ob":
		return true
	default:
		return strings.HasPrefix(name, "cf_chl_") && len(name) <= 80
	}
}

func codexInfrastructureJar(scope extensionv1.CodexRoutingScope, expires time.Time, create bool) *codexInfrastructureCookies {
	if scope.ConnectionLeaseID == "" || !time.Now().Before(expires) {
		return nil
	}
	key := codexRoutingDigest(codexRoutingScopeKey(scope), scope.ConnectionLeaseID)
	if value, ok := codexInfrastructureJars.Load(key); ok {
		jar, valid := value.(*codexInfrastructureCookies)
		if valid && jar != nil && time.Now().Before(jar.expires) {
			return jar
		}
		codexInfrastructureJars.Delete(key)
	}
	if !create {
		return nil
	}
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil
	}
	value := &codexInfrastructureCookies{jar: jar, expires: earlierCodexTime(expires, time.Now().Add(extensionv1.CodexRoutingMaxAge))}
	actual, loaded := codexInfrastructureJars.LoadOrStore(key, value)
	if !loaded {
		time.AfterFunc(time.Until(value.expires), func() { codexInfrastructureJars.CompareAndDelete(key, value) })
	}
	stored, ok := actual.(*codexInfrastructureCookies)
	if !ok {
		return nil
	}
	return stored
}

func observeCodexInfrastructureCookies(scope extensionv1.CodexRoutingScope, headers http.Header, expires time.Time) {
	var cookies []*http.Cookie
	for _, cookie := range (&http.Response{Header: headers}).Cookies() {
		if !isCodexInfrastructureCookie(cookie.Name) || isCodexRoutingCookie(cookie.Name) || cookie.Valid() != nil || len(cookie.Value) > 8192 {
			continue
		}
		cookies = append(cookies, cookie)
	}
	if len(cookies) == 0 {
		return
	}
	jar := codexInfrastructureJar(scope, expires, true)
	if jar == nil {
		return
	}
	target, _ := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	jar.mu.Lock()
	defer jar.mu.Unlock()
	jar.jar.SetCookies(target, cookies)
}

func applyCodexInfrastructureCookies(request *http.Request, scope extensionv1.CodexRoutingScope, expires time.Time) {
	if request == nil || request.URL == nil || request.URL.Scheme != "https" || request.URL.Hostname() != "chatgpt.com" {
		return
	}
	jar := codexInfrastructureJar(scope, expires, false)
	if jar == nil {
		return
	}
	jar.mu.Lock()
	cookies := jar.jar.Cookies(request.URL)
	jar.mu.Unlock()
	for _, cookie := range cookies {
		if isCodexInfrastructureCookie(cookie.Name) && !isCodexRoutingCookie(cookie.Name) {
			request.AddCookie(cookie)
		}
	}
}

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"golang.org/x/net/http2"
	"golang.org/x/sync/singleflight"
)

// chatgptCodexModelsURL is the ChatGPT Codex models manifest endpoint.
// Package-level variable so tests can point it at a stub server.
var chatgptCodexModelsURL = "https://chatgpt.com/backend-api/codex/models"

const (
	codexModelsManifestBodyLimit       int64 = 8 << 20
	codexModelsManifestCacheBodyLimit        = 1 << 20
	codexModelsManifestCacheMaxEntries       = 64
	codexModelsManifestCacheTTL              = 30 * time.Second
	codexModelsManifestCacheStaleTTL         = 5 * time.Minute
	codexModelsManifestRequestTimeout        = 15 * time.Second
)

// CodexModelsManifest carries the client representation plus caching metadata.
type CodexModelsManifest struct {
	Body         []byte
	ETag         string
	upstreamETag string
	NotModified  bool
}

type codexModelsManifestUpstreamError struct {
	err        error
	retryable  bool
	statusCode int
	headers    http.Header
	body       []byte
}

func (e *codexModelsManifestUpstreamError) Error() string { return e.err.Error() }

func (e *codexModelsManifestUpstreamError) Unwrap() error { return e.err }

// IsRetryableCodexModelsManifestError reports whether another selected account
// may succeed without changing the request. Configuration and upstream 4xx
// responses, except 429 and ChatGPT-backend 401, are intentionally not
// retried. A manifest 401 from the ChatGPT Codex backend reflects the selected
// OAuth account's upstream token rather than the client request (the client's
// own API key was already validated locally), so a different account may still
// serve the manifest. Custom API key upstreams keep the old no-failover 401
// behavior because their /models auth semantics are not authoritative for the
// account.
func IsRetryableCodexModelsManifestError(err error) bool {
	var upstreamErr *codexModelsManifestUpstreamError
	return errors.As(err, &upstreamErr) && upstreamErr.retryable
}

// NormalizeCodexModelsManifestFailoverError adapts the manifest fetcher's
// private error envelope to the gateway-wide retry/cooldown contract.  It
// returns nil for client/configuration failures that must remain terminal.
func NormalizeCodexModelsManifestFailoverError(err error, account *Account) *UpstreamFailoverError {
	var upstreamErr *codexModelsManifestUpstreamError
	if !errors.As(err, &upstreamErr) || !upstreamErr.retryable {
		return nil
	}
	statusCode := upstreamErr.statusCode
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}
	failoverErr := &UpstreamFailoverError{
		StatusCode:        statusCode,
		ResponseHeaders:   upstreamErr.headers.Clone(),
		ResponseBody:      append([]byte(nil), upstreamErr.body...),
		NextAccountAction: NextAccountRetry,
	}
	if upstreamErr.statusCode <= 0 {
		failoverErr.Reason = OpenAITransientTransportFailureReason
	}
	if account != nil && account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode) {
		failoverErr.RetryableOnSameAccount = true
	}
	return failoverErr
}

func isRetryableCodexModelsManifestTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var goAwayErr http2.GoAwayError
	if errors.As(err, &goAwayErr) {
		return true
	}
	var streamErr http2.StreamError
	if errors.As(err, &streamErr) {
		return true
	}
	var connectionErr http2.ConnectionError
	if errors.As(err, &connectionErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// net/http uses unexported HTTP/2 error types, so typed matching is not
	// possible for errors produced by the standard library transport.
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "http2:") &&
		(strings.Contains(message, "goaway") ||
			strings.Contains(message, "refused_stream") ||
			strings.Contains(message, "frame too large")) {
		return true
	}
	if strings.Contains(message, "stream error: stream id ") {
		return true
	}
	for _, code := range []http2.ErrCode{
		http2.ErrCodeNo,
		http2.ErrCodeProtocol,
		http2.ErrCodeInternal,
		http2.ErrCodeFlowControl,
		http2.ErrCodeSettingsTimeout,
		http2.ErrCodeStreamClosed,
		http2.ErrCodeFrameSize,
		http2.ErrCodeRefusedStream,
		http2.ErrCodeCancel,
		http2.ErrCodeCompression,
		http2.ErrCodeConnect,
		http2.ErrCodeEnhanceYourCalm,
		http2.ErrCodeInadequateSecurity,
		http2.ErrCodeHTTP11Required,
	} {
		if strings.Contains(message, "connection error: "+strings.ToLower(code.String())) {
			return true
		}
	}
	return false
}

type codexModelsManifestRequest struct {
	url                 string
	headers             http.Header
	proxyURL            string
	accountID           int64
	credentialAccountID int64
	credentialAccount   *Account
	accountConcurrency  int
	useAPIKeyUpstream   bool
	cindyCatalogEnabled bool
	cindyCatalogVersion string
	cindyImageEnabled   bool
	projectCindyCatalog bool
}

type codexModelsManifestCacheEntry struct {
	manifest   *CodexModelsManifest
	order      uint64
	expiresAt  time.Time
	staleUntil time.Time
}

type codexModelsManifestCacheState uint8

const (
	codexModelsManifestCacheMiss codexModelsManifestCacheState = iota
	codexModelsManifestCacheFresh
	codexModelsManifestCacheStale
)

type codexModelsManifestCache struct {
	mu        sync.Mutex
	entries   map[string]codexModelsManifestCacheEntry
	nextOrder uint64
	refresh   singleflight.Group
}

func (c *codexModelsManifestCache) get(key string, now time.Time) (*CodexModelsManifest, codexModelsManifestCacheState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, codexModelsManifestCacheMiss
	}
	if !now.Before(entry.staleUntil) {
		delete(c.entries, key)
		return nil, codexModelsManifestCacheMiss
	}
	if now.Before(entry.expiresAt) {
		return entry.manifest, codexModelsManifestCacheFresh
	}
	return entry.manifest, codexModelsManifestCacheStale
}

func (c *codexModelsManifestCache) set(key string, manifest *CodexModelsManifest, now time.Time) {
	if manifest == nil || len(manifest.Body) > codexModelsManifestCacheBodyLimit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]codexModelsManifestCacheEntry)
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= codexModelsManifestCacheMaxEntries {
		oldestKey := ""
		var oldestOrder uint64
		for candidateKey, entry := range c.entries {
			if !now.Before(entry.staleUntil) {
				delete(c.entries, candidateKey)
				continue
			}
			if oldestKey == "" || entry.order < oldestOrder {
				oldestKey = candidateKey
				oldestOrder = entry.order
			}
		}
		if len(c.entries) >= codexModelsManifestCacheMaxEntries && oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	c.nextOrder++
	c.entries[key] = codexModelsManifestCacheEntry{
		manifest:   manifest,
		order:      c.nextOrder,
		expiresAt:  now.Add(codexModelsManifestCacheTTL),
		staleUntil: now.Add(codexModelsManifestCacheStaleTTL),
	}
}

// FetchCodexModelsManifest fetches the live Codex models manifest from either
// the ChatGPT backend for OAuth accounts or a custom upstream for API key accounts.
//
// After validating the stable top-level envelope, OAuth response bodies are
// passed through verbatim. Custom API key manifests receive only the narrowly
// scoped compatibility adjustments required by custom-provider Codex clients.
func (s *OpenAIGatewayService) FetchCodexModelsManifest(ctx context.Context, account *Account, clientVersion, ifNoneMatch string) (*CodexModelsManifest, error) {
	if account == nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_ACCOUNT_REQUIRED", "account is required")
	}
	credAccount, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_CREDENTIALS_FAILED", "resolve credential account: %v", err)
	}

	clientVersion = strings.TrimSpace(clientVersion)
	if clientVersion == "" {
		clientVersion = CodexCanonicalClientVersion()
	}

	requestEndpoint := chatgptCodexModelsURL
	authToken := ""
	useAPIKeyUpstream := false
	appendModelsPath := false
	switch {
	case credAccount.IsOpenAIOAuth():
		authToken = strings.TrimSpace(credAccount.GetOpenAIAccessToken())
		if authToken == "" && !credAccount.IsOpenAIAgentIdentity() {
			return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_TOKEN_MISSING", "account has no Codex backend access token")
		}
	case credAccount.IsOpenAIApiKey():
		baseURL := strings.TrimSpace(credAccount.GetCredential("base_url"))
		if baseURL == "" || isOfficialOpenAIModelsBaseURL(baseURL) {
			return nil, infraerrors.New(
				http.StatusBadGateway,
				"OPENAI_CODEX_MODELS_API_KEY_UPSTREAM_UNSUPPORTED",
				"Codex models manifest requires a custom API key upstream base URL",
			)
		}
		authToken = strings.TrimSpace(credAccount.GetOpenAIApiKey())
		if authToken == "" {
			return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_API_KEY_MISSING", "account has no API key for the Codex models upstream")
		}
		normalizedBaseURL, validateErr := s.validateUpstreamBaseURL(baseURL)
		if validateErr != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_API_KEY_UPSTREAM_INVALID", "invalid Codex models upstream base URL: %v", validateErr)
		}
		requestEndpoint = normalizedBaseURL
		useAPIKeyUpstream = true
		appendModelsPath = true
	default:
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_ACCOUNT_TYPE_UNSUPPORTED", "account type %q cannot fetch the Codex models manifest", credAccount.Type)
	}

	requestURL, err := buildCodexModelsManifestURL(requestEndpoint, appendModelsPath, clientVersion)
	if err != nil {
		if useAPIKeyUpstream {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_API_KEY_UPSTREAM_INVALID", "invalid Codex models upstream base URL: %v", err)
		}
		return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_REQUEST_FAILED", "parse codex models request URL: %v", err)
	}

	headers := make(http.Header)
	if useAPIKeyUpstream {
		headers.Set("Authorization", "Bearer "+authToken)
		credAccount.ApplyHeaderOverrides(headers)
	} else {
		authHeaders, authErr := s.buildOpenAIAuthenticationHeaders(ctx, credAccount, authToken)
		if authErr != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_AUTH_FAILED", "build Codex models authentication: %v", authErr)
		}
		for key, values := range authHeaders {
			for _, value := range values {
				headers.Add(key, value)
			}
		}
		setOpenAIChatGPTAccountHeaders(headers, credAccount)
	}
	headers.Set("Accept", "application/json")
	overrideUA := ""
	if !useAPIKeyUpstream {
		overrideUA = credAccount.GetOpenAIUserAgent()
	}
	identity := resolveCodexOutboundIdentity(overrideUA)
	headers.Set("Originator", identity.originator)
	headers.Set("User-Agent", identity.userAgent)
	// Version 头优先与 client_version 查询参数同源：客户端自报版本合法且不低于上游
	// 门槛时原样使用；否则回退规范版本，避免陈旧 version 触发上游 404（issue #3901）。
	// client_version 查询参数本身始终按客户端原值透传（内容协商语义，契约见
	// TestFetchCodexModelsManifestPassthrough）。
	headerVersion := NormalizeCodexClientVersion(clientVersion)
	if headerVersion == "" || CompareVersions(headerVersion, codexUpstreamMinVersion) < 0 {
		headerVersion = identity.version
	}
	headers.Set("Version", headerVersion)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	request := codexModelsManifestRequest{
		url:                 requestURL.String(),
		headers:             headers,
		proxyURL:            proxyURL,
		accountID:           account.ID,
		credentialAccountID: credAccount.ID,
		credentialAccount:   credAccount,
		accountConcurrency:  account.Concurrency,
		useAPIKeyUpstream:   useAPIKeyUpstream,
		cindyCatalogEnabled: CindyCapabilityCatalogFeatureEnabled(),
		cindyCatalogVersion: CindyCapabilityCatalogVersion,
		cindyImageEnabled:   CindyImageStudioFeatureEnabled(),
		projectCindyCatalog: CindyCapabilityCatalogFeatureEnabled() &&
			IsCindyAPIKeyAccount(credAccount.Platform, credAccount.Type, credAccount.Credentials),
	}
	if useAPIKeyUpstream {
		return s.fetchCachedAPIKeyCodexModelsManifest(ctx, request, ifNoneMatch)
	}
	manifest, fetchErr := s.fetchCodexModelsManifestUpstream(ctx, request, ifNoneMatch)
	if !credAccount.IsOpenAIAgentIdentity() || !isAgentIdentityTaskInvalidCodexModelsError(fetchErr) {
		s.handleCodexModelsManifestAccountAuthError(ctx, account, credAccount, fetchErr)
		return manifest, fetchErr
	}
	expectedTaskID := strings.TrimSpace(credAccount.GetCredential("task_id"))
	if recoverErr := s.recoverAgentIdentityTask(ctx, credAccount, expectedTaskID); recoverErr != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_AUTH_FAILED", "agent identity task recovery failed: %v", recoverErr)
	}
	authHeaders, authErr := s.buildOpenAIAuthenticationHeaders(ctx, credAccount, "")
	if authErr != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_AUTH_FAILED", "build Codex models authentication after task recovery: %v", authErr)
	}
	request.headers.Del("Authorization")
	request.headers.Del("ChatGPT-Account-ID")
	for key, values := range authHeaders {
		for _, value := range values {
			request.headers.Add(key, value)
		}
	}
	setOpenAIChatGPTAccountHeaders(request.headers, credAccount)
	return s.fetchCodexModelsManifestUpstream(ctx, request, ifNoneMatch)
}

func isAgentIdentityTaskInvalidCodexModelsError(err error) bool {
	var upstreamErr *codexModelsManifestUpstreamError
	return errors.As(err, &upstreamErr) &&
		isAgentIdentityTaskInvalidHTTPResponse(upstreamErr.statusCode, upstreamErr.body)
}

// handleCodexModelsManifestAccountAuthError feeds manifest 401s from the
// ChatGPT Codex backend into the shared upstream-error state machinery
// (token cache invalidation, temp-unschedulable cooldown, or permanent
// disable for token_revoked/token_invalidated). Without this, an account
// whose OAuth token was revoked upstream stays active and schedulable and
// keeps being selected for every subsequent /models request (#4544).
//
// Scope is deliberately limited to plain OAuth accounts: the manifest
// endpoint authenticates with the same token as /responses forwarding, so a
// 401 is authoritative for the account. Agent Identity accounts are excluded
// because their 401s can be task-scoped and have a dedicated recovery flow,
// and API key manifests come from custom upstreams whose /models auth may
// diverge from their chat endpoints.
func (s *OpenAIGatewayService) handleCodexModelsManifestAccountAuthError(ctx context.Context, account, credAccount *Account, err error) {
	if s == nil || account == nil || err == nil {
		return
	}
	if credAccount == nil || !credAccount.IsOpenAIOAuth() || credAccount.IsOpenAIAgentIdentity() {
		return
	}
	var upstreamErr *codexModelsManifestUpstreamError
	if !errors.As(err, &upstreamErr) || upstreamErr.statusCode != http.StatusUnauthorized {
		return
	}
	headers := upstreamErr.headers
	if headers == nil {
		headers = http.Header{}
	}
	s.handleOpenAIAccountUpstreamError(ctx, account, upstreamErr.statusCode, headers, upstreamErr.body)
}

func (s *OpenAIGatewayService) fetchCachedAPIKeyCodexModelsManifest(ctx context.Context, request codexModelsManifestRequest, ifNoneMatch string) (*CodexModelsManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cacheKey := buildCodexModelsManifestCacheKey(request)
	manifest, state := s.codexModelsManifestCache.get(cacheKey, time.Now())
	if state == codexModelsManifestCacheFresh {
		return codexModelsManifestForClient(manifest, ifNoneMatch), nil
	}
	resultCh := s.refreshCachedAPIKeyCodexModelsManifest(cacheKey, request)
	if state == codexModelsManifestCacheStale {
		return codexModelsManifestForClient(manifest, ifNoneMatch), nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		manifest, ok := result.Val.(*CodexModelsManifest)
		if !ok || manifest == nil {
			return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_REQUEST_FAILED", "invalid shared Codex models manifest result")
		}
		return codexModelsManifestForClient(manifest, ifNoneMatch), nil
	}
}

func (s *OpenAIGatewayService) refreshCachedAPIKeyCodexModelsManifest(cacheKey string, request codexModelsManifestRequest) <-chan singleflight.Result {
	return s.codexModelsManifestCache.refresh.DoChan(cacheKey, func() (any, error) {
		cached, _ := s.codexModelsManifestCache.get(cacheKey, time.Now())
		ifNoneMatch := ""
		if cached != nil {
			ifNoneMatch = cached.upstreamETag
		}
		manifest, err := s.fetchCodexModelsManifestUpstream(context.Background(), request, ifNoneMatch)
		if err != nil {
			return nil, err
		}
		if manifest.NotModified && cached != nil {
			s.codexModelsManifestCache.set(cacheKey, cached, time.Now())
			return cached, nil
		}
		if !manifest.NotModified {
			s.codexModelsManifestCache.set(cacheKey, manifest, time.Now())
		}
		return manifest, nil
	})
}

func (s *OpenAIGatewayService) fetchCodexModelsManifestUpstream(ctx context.Context, request codexModelsManifestRequest, ifNoneMatch string) (*CodexModelsManifest, error) {
	reqCtx, cancel := context.WithTimeout(ctx, codexModelsManifestRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, request.url, nil)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_REQUEST_FAILED", "create codex models request: %v", err)
	}
	req.Header = request.headers.Clone()
	if ifNoneMatch = strings.TrimSpace(ifNoneMatch); ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	var resp *http.Response
	if request.useAPIKeyUpstream {
		if s.httpUpstream == nil {
			return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_UPSTREAM_NOT_CONFIGURED", "Codex models upstream HTTP client is not configured")
		}
		req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
		resp, err = s.httpUpstream.Do(req, request.proxyURL, request.accountID, request.accountConcurrency)
	} else {
		client, clientErr := httpclient.GetClient(httpclient.Options{
			ProxyURL:              request.proxyURL,
			Timeout:               codexModelsManifestRequestTimeout,
			ResponseHeaderTimeout: 10 * time.Second,
		})
		if clientErr != nil {
			return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_PROXY_INVALID", "invalid proxy configuration: %v", clientErr)
		}
		resp, err = client.Do(req)
	}
	if err != nil {
		return nil, &codexModelsManifestUpstreamError{
			err:       infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "codex models manifest request failed: %v", err),
			retryable: isRetryableCodexModelsManifestTransportError(err),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return &CodexModelsManifest{ETag: resp.Header.Get("ETag"), NotModified: true}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		body = s.redactAgentIdentitySensitiveBody(reqCtx, request.credentialAccount, body)
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, &codexModelsManifestUpstreamError{
			err:        infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "codex models manifest upstream error %d: %s", resp.StatusCode, message),
			statusCode: resp.StatusCode,
			headers:    resp.Header.Clone(),
			body:       body,
			retryable: (resp.StatusCode == http.StatusUnauthorized && !request.useAPIKeyUpstream) ||
				resp.StatusCode == http.StatusTooManyRequests ||
				(resp.StatusCode >= http.StatusInternalServerError && resp.StatusCode < 600),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, codexModelsManifestBodyLimit))
	if err != nil {
		return nil, &codexModelsManifestUpstreamError{
			err:       infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "read codex models manifest response: %v", err),
			retryable: isRetryableCodexModelsManifestTransportError(err),
		}
	}
	upstreamBody := body
	if request.useAPIKeyUpstream {
		body = convertOpenAIModelListToCodexManifest(body)
	}
	if err := validateCodexModelsManifestEnvelope(body); err != nil {
		return nil, &codexModelsManifestUpstreamError{
			err: infraerrors.Newf(
				http.StatusBadGateway,
				"OPENAI_CODEX_MODELS_UPSTREAM_INVALID_MANIFEST",
				"codex models manifest upstream returned an invalid envelope: %v",
				err,
			),
			retryable: true,
		}
	}
	if request.projectCindyCatalog {
		body, err = projectCindyCodexModelsManifest(body)
		if err != nil {
			return nil, &codexModelsManifestUpstreamError{
				err: infraerrors.Newf(
					http.StatusBadGateway,
					"OPENAI_CODEX_MODELS_UPSTREAM_INVALID_MANIFEST",
					"codex models manifest upstream could not be projected onto the Cindy catalog: %v",
					err,
				),
				retryable: true,
			}
		}
	}
	if request.useAPIKeyUpstream {
		body, err = adjustAPIKeyCodexModelsManifest(body)
		if err != nil {
			return nil, &codexModelsManifestUpstreamError{
				err: infraerrors.Newf(
					http.StatusBadGateway,
					"OPENAI_CODEX_MODELS_UPSTREAM_INVALID_MANIFEST",
					"codex models manifest upstream could not be adjusted: %v",
					err,
				),
				retryable: true,
			}
		}
	}
	etag := resp.Header.Get("ETag")
	manifest := &CodexModelsManifest{Body: body, ETag: etag}
	if request.useAPIKeyUpstream {
		manifest.upstreamETag = etag
		if !bytes.Equal(body, upstreamBody) {
			manifest.ETag = codexModelsManifestBodyETag(body)
		}
	}
	return manifest, nil
}

// projectCindyCodexModelsManifest preserves usable upstream metadata while
// replacing exact live IDs and hidden compatibility aliases with stable Cindy
// public IDs. Only Responses-verified text models and the explicit image bridge
// are eligible for the Codex surface.
func projectCindyCodexModelsManifest(body []byte) ([]byte, error) {
	return rewriteCindyCodexModelsManifest(body)
}

type cindyCodexReasoningEffort struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type cindyCodexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

// cindyCodexModel mirrors every serialized field in the official
// openai/codex rust-v0.146.0 ModelInfo. Fields represented by Rust Option are
// deliberately emitted as null to keep the local manifest stable and explicit.
type cindyCodexModel struct {
	Slug                              string                      `json:"slug"`
	DisplayName                       string                      `json:"display_name"`
	Description                       *string                     `json:"description"`
	DefaultReasoningLevel             *string                     `json:"default_reasoning_level"`
	SupportedReasoningLevels          []cindyCodexReasoningEffort `json:"supported_reasoning_levels"`
	ShellType                         string                      `json:"shell_type"`
	Visibility                        string                      `json:"visibility"`
	SupportedInAPI                    bool                        `json:"supported_in_api"`
	Priority                          int                         `json:"priority"`
	AdditionalSpeedTiers              []string                    `json:"additional_speed_tiers"`
	ServiceTiers                      []any                       `json:"service_tiers"`
	DefaultServiceTier                any                         `json:"default_service_tier"`
	AvailabilityNUX                   any                         `json:"availability_nux"`
	Upgrade                           any                         `json:"upgrade"`
	BaseInstructions                  string                      `json:"base_instructions"`
	ModelMessages                     any                         `json:"model_messages"`
	IncludeSkillsUsageInstructions    bool                        `json:"include_skills_usage_instructions"`
	SupportsReasoningSummaryParameter bool                        `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary           string                      `json:"default_reasoning_summary"`
	SupportVerbosity                  bool                        `json:"support_verbosity"`
	DefaultVerbosity                  any                         `json:"default_verbosity"`
	ApplyPatchToolType                any                         `json:"apply_patch_tool_type"`
	WebSearchToolType                 string                      `json:"web_search_tool_type"`
	TruncationPolicy                  cindyCodexTruncationPolicy  `json:"truncation_policy"`
	SupportsParallelToolCalls         bool                        `json:"supports_parallel_tool_calls"`
	SupportsImageDetailOriginal       bool                        `json:"supports_image_detail_original"`
	ContextWindow                     *int                        `json:"context_window"`
	MaxContextWindow                  *int                        `json:"max_context_window"`
	AutoCompactTokenLimit             any                         `json:"auto_compact_token_limit"`
	CompHash                          any                         `json:"comp_hash"`
	EffectiveContextWindowPercent     int                         `json:"effective_context_window_percent"`
	ExperimentalSupportedTools        []string                    `json:"experimental_supported_tools"`
	InputModalities                   []string                    `json:"input_modalities"`
	SupportsSearchTool                bool                        `json:"supports_search_tool"`
	UseResponsesLite                  bool                        `json:"use_responses_lite"`
	AutoReviewModelOverride           any                         `json:"auto_review_model_override"`
	ToolMode                          any                         `json:"tool_mode"`
	MultiAgentVersion                 any                         `json:"multi_agent_version"`
}

func newCindyCodexModel(capability CindyCapability, priority int) cindyCodexModel {
	displayName := strings.TrimSpace(capability.DisplayName)
	if displayName == "" {
		displayName = capability.PublicID
	}
	var description *string
	if value := strings.TrimSpace(capability.Description); value != "" {
		description = &value
	}
	var defaultReasoningLevel *string
	if value := strings.TrimSpace(capability.DefaultReasoningEffort); value != "" {
		defaultReasoningLevel = &value
	}
	reasoningLevels := make([]cindyCodexReasoningEffort, 0, len(capability.CodexReasoningEfforts()))
	for _, effort := range capability.CodexReasoningEfforts() {
		reasoningLevels = append(reasoningLevels, cindyCodexReasoningEffort{
			Effort:      effort,
			Description: cindyCodexReasoningEffortDescription(effort),
		})
	}
	var contextWindow *int
	if value := capability.EffectiveCodexContextWindow(); value > 0 {
		contextWindow = &value
	}
	return cindyCodexModel{
		Slug:                              capability.PublicID,
		DisplayName:                       displayName,
		Description:                       description,
		DefaultReasoningLevel:             defaultReasoningLevel,
		SupportedReasoningLevels:          reasoningLevels,
		ShellType:                         "shell_command",
		Visibility:                        "list",
		SupportedInAPI:                    true,
		Priority:                          priority,
		AdditionalSpeedTiers:              []string{},
		ServiceTiers:                      []any{},
		BaseInstructions:                  openai.CodexBaseInstructionsForModel(capability.PublicID),
		IncludeSkillsUsageInstructions:    false,
		SupportsReasoningSummaryParameter: true,
		DefaultReasoningSummary:           "auto",
		SupportVerbosity:                  false,
		WebSearchToolType:                 "text",
		TruncationPolicy:                  cindyCodexTruncationPolicyForModel(capability.PublicID),
		SupportsParallelToolCalls:         false,
		SupportsImageDetailOriginal:       false,
		ContextWindow:                     contextWindow,
		MaxContextWindow:                  contextWindow,
		EffectiveContextWindowPercent:     95,
		ExperimentalSupportedTools:        []string{},
		InputModalities:                   append([]string(nil), capability.InputModalities...),
		SupportsSearchTool:                false,
		UseResponsesLite:                  false,
	}
}

func cindyCodexTruncationPolicyForModel(modelID string) cindyCodexTruncationPolicy {
	switch modelID {
	case "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra":
		// Exact openai/codex rust-v0.146.0 models.json contract.
		return cindyCodexTruncationPolicy{Mode: "tokens", Limit: 10000}
	default:
		// Official rust-v0.146.0 unknown-model fallback.
		return cindyCodexTruncationPolicy{Mode: "bytes", Limit: 10000}
	}
}

func cindyCodexReasoningEffortDescription(effort string) string {
	switch effort {
	case "minimal":
		return "Minimal reasoning"
	case "low":
		return "Low reasoning"
	case "medium":
		return "Medium reasoning"
	case "high":
		return "High reasoning"
	case "xhigh":
		return "Extra high reasoning"
	case "max":
		return "Maximum reasoning"
	case "ultra":
		return "Ultra reasoning"
	default:
		return effort
	}
}

// BuildCindyCodexModelsManifest builds the deterministic local manifest used
// by strict Cindy groups. It never fetches Cindy's upstream /models response,
// so live IDs, hidden 5.4 aliases, and unverified candidates cannot leak.
func BuildCindyCodexModelsManifest(ifNoneMatch string) (*CodexModelsManifest, error) {
	models := make([]cindyCodexModel, 0, len(cindyCapabilityCatalog))
	for priority, modelID := range CindyCodexPublicModelIDs() {
		capability, ok := resolveKnownCindyCapability(modelID)
		if !ok {
			return nil, fmt.Errorf("build local Cindy Codex models manifest: unknown catalog model %q", modelID)
		}
		models = append(models, newCindyCodexModel(capability, priority+1))
	}
	body, err := json.Marshal(map[string]any{"models": models})
	if err != nil {
		return nil, fmt.Errorf("encode local Cindy Codex models manifest: %w", err)
	}
	manifest := &CodexModelsManifest{Body: body, ETag: codexModelsManifestBodyETag(body)}
	return codexModelsManifestForClient(manifest, ifNoneMatch), nil
}

// MergeCindyCodexModelsManifest combines an ordinary account's legacy Codex
// manifest with every verified Cindy Codex model. All ordinary entries and
// metadata are preserved without Cindy reinterpretation, even when an
// ordinary slug happens to match a known Cindy ID or compatibility alias.
func MergeCindyCodexModelsManifest(manifest *CodexModelsManifest, ifNoneMatch string) (*CodexModelsManifest, error) {
	if manifest == nil {
		return nil, errors.New("ordinary Codex models manifest is required")
	}
	body, err := mergeCindyCodexModelsManifest(manifest.Body)
	if err != nil {
		return nil, err
	}
	merged := &CodexModelsManifest{
		Body:         body,
		ETag:         codexModelsManifestBodyETag(body),
		upstreamETag: manifest.upstreamETag,
	}
	return codexModelsManifestForClient(merged, ifNoneMatch), nil
}

func rewriteCindyCodexModelsManifest(body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, fmt.Errorf("decode top-level models array: %w", err)
	}

	projected := make([]json.RawMessage, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, rawModel := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(rawModel, &model); err != nil || model == nil {
			continue
		}
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil {
			continue
		}
		capability, knownCindy := resolveKnownCindyCapability(strings.TrimSpace(slug))
		if !knownCindy {
			continue
		}
		if !cindyCapabilitySupportsCodexModels(capability) {
			continue
		}
		if _, duplicate := seen[capability.PublicID]; duplicate {
			continue
		}
		seen[capability.PublicID] = struct{}{}
		publicSlug, err := json.Marshal(capability.PublicID)
		if err != nil {
			return nil, fmt.Errorf("encode public model ID %q: %w", capability.PublicID, err)
		}
		model["slug"] = publicSlug
		projectedModel, err := json.Marshal(model)
		if err != nil {
			return nil, fmt.Errorf("encode projected model %q: %w", capability.PublicID, err)
		}
		projected = append(projected, projectedModel)
	}

	projectedModels, err := json.Marshal(projected)
	if err != nil {
		return nil, fmt.Errorf("encode projected models array: %w", err)
	}
	envelope["models"] = projectedModels
	projectedBody, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode projected manifest: %w", err)
	}
	return projectedBody, nil
}

func mergeCindyCodexModelsManifest(body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, fmt.Errorf("decode top-level models array: %w", err)
	}

	// The ordinary manifest is an independent provider contract. Preserve its
	// entries without semantic rewriting, including IDs that happen
	// to match a Cindy live ID, hidden alias, or unverified candidate. Catalog
	// verification constrains only exact Cindy sources.
	merged := append([]json.RawMessage(nil), models...)
	seenExactSlugs := make(map[string]struct{}, len(models))
	for _, rawModel := range models {
		var model struct {
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(rawModel, &model); err == nil && model.Slug != "" {
			seenExactSlugs[model.Slug] = struct{}{}
		}
	}
	for _, publicID := range CindyCodexPublicModelIDs() {
		if _, duplicate := seenExactSlugs[publicID]; duplicate {
			continue
		}
		capability, ok := resolveKnownCindyCapability(publicID)
		if !ok {
			return nil, fmt.Errorf("merge Cindy Codex models manifest: unknown catalog model %q", publicID)
		}
		encoded, err := json.Marshal(newCindyCodexModel(capability, len(merged)+1))
		if err != nil {
			return nil, fmt.Errorf("encode catalog model %q: %w", publicID, err)
		}
		merged = append(merged, encoded)
		seenExactSlugs[publicID] = struct{}{}
	}

	mergedModels, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode merged models array: %w", err)
	}
	envelope["models"] = mergedModels
	mergedBody, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode merged manifest: %w", err)
	}
	return mergedBody, nil
}

func codexModelsManifestBodyETag(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf(`"%x"`, sum)
}

var apiKeyCodexModelsWithoutResponsesLite = map[string]struct{}{
	"gpt-5.6-sol":   {},
	"gpt-5.6-terra": {},
	"gpt-5.6-luna":  {},
}

// adjustAPIKeyCodexModelsManifest prevents Codex from selecting Responses
// Lite for custom API key providers. Those clients do not install web.run in
// Lite mode, so the affected model manifests must advertise the full Responses
// path. Return the original body when no targeted true value is present.
func adjustAPIKeyCodexModelsManifest(body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, fmt.Errorf("decode top-level models array: %w", err)
	}

	changed := false
	for i, rawModel := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(rawModel, &model); err != nil || model == nil {
			continue
		}
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil {
			continue
		}
		if _, targeted := apiKeyCodexModelsWithoutResponsesLite[slug]; !targeted {
			continue
		}
		var useResponsesLite bool
		if err := json.Unmarshal(model["use_responses_lite"], &useResponsesLite); err != nil || !useResponsesLite {
			continue
		}
		model["use_responses_lite"] = json.RawMessage("false")
		adjusted, err := json.Marshal(model)
		if err != nil {
			return nil, fmt.Errorf("encode model %q: %w", slug, err)
		}
		models[i] = adjusted
		changed = true
	}
	if !changed {
		return body, nil
	}

	adjustedModels, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("encode top-level models array: %w", err)
	}
	envelope["models"] = adjustedModels
	adjusted, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode JSON object: %w", err)
	}
	return adjusted, nil
}

// convertOpenAIModelListToCodexManifest rewrites a standard OpenAI
// GET /v1/models response ({"object":"list","data":[{"id":...},...]}) into the
// Codex manifest envelope ({"models":[{"slug":...},...]}) so custom API key
// upstreams that only implement the standard endpoint can serve Codex model
// discovery. Bodies that already carry a top-level models field, are not the
// standard list shape, or yield no usable model IDs are returned unchanged so
// envelope validation reports the original payload.
func convertOpenAIModelListToCodexManifest(body []byte) []byte {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return body
	}
	if _, ok := envelope["models"]; ok {
		return body
	}
	data, ok := envelope["data"]
	if !ok {
		return body
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return body
	}
	type codexModelEntry struct {
		Slug string `json:"slug"`
	}
	models := make([]codexModelEntry, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		models = append(models, codexModelEntry{Slug: id})
	}
	if len(models) == 0 {
		return body
	}
	converted, err := json.Marshal(map[string][]codexModelEntry{"models": models})
	if err != nil {
		return body
	}
	return converted
}

func validateCodexModelsManifestEnvelope(body []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode JSON object: %w", err)
	}
	if envelope == nil {
		return errors.New("expected a JSON object")
	}
	models, ok := envelope["models"]
	if !ok {
		return errors.New("missing top-level models array")
	}
	models = bytes.TrimSpace(models)
	var entries []json.RawMessage
	if len(models) == 0 || models[0] != '[' {
		return errors.New("top-level models field is not an array")
	}
	if err := json.Unmarshal(models, &entries); err != nil {
		return fmt.Errorf("decode top-level models array: %w", err)
	}
	return nil
}

func buildCodexModelsManifestCacheKey(request codexModelsManifestRequest) string {
	hasher := sha256.New()
	_, _ = fmt.Fprintf(
		hasher,
		"%d\n%d\n%s\n%s\ncindy-catalog:%t:%s\nimage-studio:%t\nproject-cindy:%t\n",
		request.accountID,
		request.credentialAccountID,
		request.proxyURL,
		request.url,
		request.cindyCatalogEnabled,
		request.cindyCatalogVersion,
		request.cindyImageEnabled,
		request.projectCindyCatalog,
	)
	headerNames := make([]string, 0, len(request.headers))
	for name := range request.headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		_, _ = fmt.Fprintf(hasher, "%s\n", strings.ToLower(name))
		for _, value := range request.headers[name] {
			_, _ = fmt.Fprintf(hasher, "%s\n", value)
		}
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func codexModelsManifestForClient(manifest *CodexModelsManifest, ifNoneMatch string) *CodexModelsManifest {
	if manifest == nil {
		return nil
	}
	if codexModelsManifestETagMatches(ifNoneMatch, manifest.ETag) {
		return &CodexModelsManifest{ETag: manifest.ETag, NotModified: true}
	}
	return manifest
}

func codexModelsManifestETagMatches(ifNoneMatch, etag string) bool {
	etag = strings.TrimSpace(etag)
	if etag == "" {
		return false
	}
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		if len(value) >= 2 && strings.EqualFold(value[:2], "W/") {
			value = strings.TrimSpace(value[2:])
		}
		return value
	}
	want := normalize(etag)
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || normalize(candidate) == want {
			return true
		}
	}
	return false
}

func isOfficialOpenAIModelsBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	hostname := strings.TrimSuffix(parsed.Hostname(), ".")
	return strings.EqualFold(hostname, "api.openai.com")
}

func buildCodexModelsManifestURL(endpoint string, appendModelsPath bool, clientVersion string) (*url.URL, error) {
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if requestURL.Fragment != "" {
		return nil, fmt.Errorf("URL fragments are not supported")
	}

	query := requestURL.Query()
	requestURL.RawQuery = ""
	requestURL.ForceQuery = false
	if appendModelsPath {
		requestURL, err = url.Parse(buildOpenAIModelsURL(requestURL.String()))
		if err != nil {
			return nil, err
		}
	}
	query.Set("client_version", clientVersion)
	requestURL.RawQuery = query.Encode()
	return requestURL, nil
}

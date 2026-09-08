package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const promptCacheDiagnosticContextKey = "openai_prompt_cache_diagnostic"

var (
	promptCacheDiagnosticSession = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	promptCacheDiagnosticModel   = regexp.MustCompile(`^gpt-[A-Za-z0-9._:-]{1,120}$`)
	promptCacheDiagnosticRay     = regexp.MustCompile(`^[0-9a-fA-F]{16,32}-[A-Z0-9]{3}$`)
	openAIPromptCacheDiagnostics = newPromptCacheDiagnosticRegistry()
)

// PromptCacheDiagnosticConfig selects one authenticated client's conversation.
// No account setting, model request, retry or persistent database state is changed.
type PromptCacheDiagnosticConfig struct {
	APIKeyID    int64  `json:"api_key_id"`
	SessionID   string `json:"session_id"`
	Model       string `json:"model"`
	TTLSeconds  int    `json:"ttl_seconds"`
	MaxRequests int    `json:"max_requests"`
}

type PromptCacheDiagnosticView struct {
	ID               string                         `json:"id"`
	Config           PromptCacheDiagnosticConfig    `json:"config"`
	CreatedAt        time.Time                      `json:"created_at"`
	ExpiresAt        time.Time                      `json:"expires_at"`
	Status           string                         `json:"status"`
	SkippedBusy      int                            `json:"skipped_busy"`
	SkippedOversized int                            `json:"skipped_oversized"`
	Records          []*promptCacheDiagnosticRecord `json:"records"`
}

type promptCacheDiagnosticUsage struct {
	Input              int64 `json:"input_tokens"`
	Output             int64 `json:"output_tokens"`
	Read               int64 `json:"cache_read_tokens"`
	Write              int64 `json:"cache_write_tokens"`
	CacheReadReported  bool  `json:"cache_read_reported"`
	CacheWriteReported bool  `json:"cache_write_reported"`
}

type promptCacheDiagnosticAttempt struct {
	Number            int                                    `json:"number"`
	AccountID         int64                                  `json:"account_id"`
	ProxyID           int64                                  `json:"proxy_id"`
	StartedAt         time.Time                              `json:"started_at"`
	HeadersAt         *time.Time                             `json:"headers_at,omitempty"`
	FirstEventAt      *time.Time                             `json:"first_event_at,omitempty"`
	HTTPStatus        int                                    `json:"http_status,omitempty"`
	TransportError    bool                                   `json:"transport_error"`
	RouteHash         string                                 `json:"route_hash"`
	Headers           map[string]promptCacheFieldFingerprint `json:"request_headers"`
	ChangedHeaders    []string                               `json:"changed_headers,omitempty"`
	RouteEqual        bool                                   `json:"route_equal_to_previous"`
	Wire              *promptCacheSnapshot                   `json:"wire"`
	IngressToWire     *promptCacheComparison                 `json:"ingress_to_wire"`
	PreviousWire      *promptCacheComparison                 `json:"previous_wire"`
	Terminal          string                                 `json:"terminal,omitempty"`
	UpstreamModel     string                                 `json:"upstream_model,omitempty"`
	ResponseIDHash    string                                 `json:"response_id_hash,omitempty"`
	SystemFingerprint promptCacheFieldFingerprint            `json:"system_fingerprint"`
	CacheDiagnostics  map[string]promptCacheFieldFingerprint `json:"upstream_cache_diagnostics,omitempty"`
	Usage             *promptCacheDiagnosticUsage            `json:"usage,omitempty"`
}

type promptCacheDiagnosticRecord struct {
	Sequence        int                             `json:"sequence"`
	ClientRequest   string                          `json:"client_request_id"`
	GatewayRequest  string                          `json:"gateway_request_id"`
	CFRay           string                          `json:"cf_ray,omitempty"`
	StartedAt       time.Time                       `json:"started_at"`
	FinishedAt      *time.Time                      `json:"finished_at,omitempty"`
	CanceledAt      *time.Time                      `json:"client_canceled_at,omitempty"`
	CancelReason    string                          `json:"cancel_reason,omitempty"`
	HTTPStatus      int                             `json:"downstream_status,omitempty"`
	Ingress         *promptCacheSnapshot            `json:"ingress"`
	PreviousIngress *promptCacheComparison          `json:"previous_ingress"`
	Attempts        []*promptCacheDiagnosticAttempt `json:"attempts"`
	OmittedAttempts int                             `json:"omitted_attempts"`
}

type promptCacheDiagnosticJob struct {
	view           PromptCacheDiagnosticView
	key            [32]byte
	stopped        bool
	inflight       int
	lastIngress    *promptCacheSnapshot
	lastIngressSeq int
	lastWire       *promptCacheSnapshot
	lastWireSeq    int
	lastHeaders    map[string]promptCacheFieldFingerprint
	lastRoute      string
}

type promptCacheDiagnosticRegistry struct {
	mu       sync.Mutex
	enabled  atomic.Bool
	jobs     map[string]*promptCacheDiagnosticJob
	inflight int
	now      func() time.Time
}

type promptCacheDiagnosticSpan struct {
	registry *promptCacheDiagnosticRegistry
	job      *promptCacheDiagnosticJob
	record   *promptCacheDiagnosticRecord
	ingress  *promptCacheSnapshot
	current  *promptCacheDiagnosticAttempt
	done     bool
}

func newPromptCacheDiagnosticRegistry() *promptCacheDiagnosticRegistry {
	return &promptCacheDiagnosticRegistry{jobs: make(map[string]*promptCacheDiagnosticJob), now: time.Now}
}

func (r *promptCacheDiagnosticRegistry) cleanupLocked() {
	for id, job := range r.jobs {
		if job.inflight == 0 && r.now().After(job.view.ExpiresAt.Add(time.Hour)) {
			delete(r.jobs, id)
		}
	}
	r.enabled.Store(len(r.jobs) != 0)
}

func (r *promptCacheDiagnosticRegistry) start(cfg PromptCacheDiagnosticConfig) (*PromptCacheDiagnosticView, error) {
	cfg.SessionID = strings.ToLower(strings.TrimSpace(cfg.SessionID))
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.TTLSeconds == 0 {
		cfg.TTLSeconds = 1800
	}
	if cfg.MaxRequests == 0 {
		cfg.MaxRequests = 32
	}
	if cfg.APIKeyID <= 0 || !promptCacheDiagnosticSession.MatchString(cfg.SessionID) || !promptCacheDiagnosticModel.MatchString(cfg.Model) ||
		cfg.TTLSeconds < 60 || cfg.TTLSeconds > 3600 || cfg.MaxRequests < 2 || cfg.MaxRequests > 64 {
		return nil, errors.New("invalid diagnostic scope or limits")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked()
	for _, job := range r.jobs {
		if !job.stopped && r.now().Before(job.view.ExpiresAt) && len(job.view.Records) < job.view.Config.MaxRequests && job.view.Config.APIKeyID == cfg.APIKeyID && job.view.Config.SessionID == cfg.SessionID && job.view.Config.Model == cfg.Model {
			return r.viewLocked(job), nil
		}
	}
	if len(r.jobs) >= 4 {
		return nil, errors.New("diagnostic capacity reached")
	}
	var entropy [48]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, errors.New("diagnostic entropy unavailable")
	}
	job := &promptCacheDiagnosticJob{view: PromptCacheDiagnosticView{
		ID: hex.EncodeToString(entropy[:16]), Config: cfg, CreatedAt: r.now().UTC(),
		ExpiresAt: r.now().Add(time.Duration(cfg.TTLSeconds) * time.Second).UTC(), Records: make([]*promptCacheDiagnosticRecord, 0, cfg.MaxRequests),
	}}
	copy(job.key[:], entropy[16:])
	r.jobs[job.view.ID] = job
	r.enabled.Store(true)
	return r.viewLocked(job), nil
}

func (r *promptCacheDiagnosticRegistry) viewLocked(job *promptCacheDiagnosticJob) *PromptCacheDiagnosticView {
	job.view.Status = "capturing"
	switch {
	case job.stopped:
		job.view.Status = "stopped"
	case !r.now().Before(job.view.ExpiresAt):
		job.view.Status = "expired"
	case len(job.view.Records) >= job.view.Config.MaxRequests:
		job.view.Status = "request_limit"
	}
	// Return an independent bounded copy; in-flight requests may update their records.
	encoded, err := json.Marshal(job.view)
	if err != nil {
		return nil
	}
	var out PromptCacheDiagnosticView
	if json.Unmarshal(encoded, &out) != nil {
		return nil
	}
	return &out
}

func (r *promptCacheDiagnosticRegistry) get(id string, stop bool) *PromptCacheDiagnosticView {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked()
	job := r.jobs[id]
	if job == nil {
		return nil
	}
	if stop {
		job.stopped = true
	}
	return r.viewLocked(job)
}

func StartOpenAIPromptCacheDiagnostic(cfg PromptCacheDiagnosticConfig) (*PromptCacheDiagnosticView, error) {
	return openAIPromptCacheDiagnostics.start(cfg)
}

func GetOpenAIPromptCacheDiagnostic(id string, stop bool) *PromptCacheDiagnosticView {
	return openAIPromptCacheDiagnostics.get(id, stop)
}

func promptCachePublicSnapshot(s *promptCacheSnapshot) *promptCacheSnapshot {
	if s == nil {
		return nil
	}
	copy := *s
	copy.items = nil
	return &copy
}

func promptCacheSafeRequestID(key []byte, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if promptCacheDiagnosticSession.MatchString(value) {
		return value
	}
	return "hmac:" + promptCacheDigest(key, []byte(value))
}

func (r *promptCacheDiagnosticRegistry) begin(c *gin.Context, apiKeyID int64, body []byte) *promptCacheDiagnosticSpan {
	if !r.enabled.Load() || c == nil || c.Request == nil || c.Request.URL == nil {
		return nil
	}
	if path := strings.TrimSuffix(c.Request.URL.Path, "/"); path != "/v1/responses" && path != "/responses" {
		return nil
	}
	session := strings.ToLower(ExtractClientSessionID(c))
	r.mu.Lock()
	r.cleanupLocked()
	var chosen *promptCacheDiagnosticJob
	for _, job := range r.jobs {
		cfg := job.view.Config
		if !job.stopped && r.now().Before(job.view.ExpiresAt) && len(job.view.Records) < cfg.MaxRequests && cfg.APIKeyID == apiKeyID && cfg.SessionID == session {
			if len(body) > promptCacheDiagnosticMaxBody {
				job.view.SkippedOversized++
				continue
			}
			if cfg.Model != gjson.GetBytes(body, "model").String() {
				continue
			}
			chosen = job
			break
		}
	}
	if chosen == nil {
		r.mu.Unlock()
		return nil
	}
	if r.inflight >= 4 {
		chosen.view.SkippedBusy++
		r.mu.Unlock()
		return nil
	}
	clientID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	gatewayID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	record := &promptCacheDiagnosticRecord{
		Sequence: len(chosen.view.Records) + 1, ClientRequest: promptCacheSafeRequestID(chosen.key[:], clientID),
		GatewayRequest: promptCacheSafeRequestID(chosen.key[:], gatewayID), StartedAt: r.now().UTC(),
		Attempts: make([]*promptCacheDiagnosticAttempt, 0, 2),
	}
	if ray := c.GetHeader("Cf-Ray"); promptCacheDiagnosticRay.MatchString(ray) {
		record.CFRay = ray
	}
	chosen.view.Records = append(chosen.view.Records, record)
	chosen.inflight++
	r.inflight++
	r.mu.Unlock()
	snapshot := buildPromptCacheSnapshot(chosen.key[:], body, "ingress_handler_before_normalization")
	r.mu.Lock()
	record.Ingress = promptCachePublicSnapshot(snapshot)
	record.PreviousIngress = comparePromptCacheSnapshots(chosen.lastIngress, snapshot, chosen.lastIngressSeq)
	chosen.lastIngress, chosen.lastIngressSeq = snapshot, record.Sequence
	r.mu.Unlock()
	return &promptCacheDiagnosticSpan{registry: r, job: chosen, record: record, ingress: snapshot}
}

// BeginOpenAIPromptCacheDiagnostic fingerprints only an explicitly enrolled
// request. The returned closure observes completion; it does not cancel work.
func BeginOpenAIPromptCacheDiagnostic(c *gin.Context, apiKeyID int64, body []byte) func() {
	span := openAIPromptCacheDiagnostics.begin(c, apiKeyID, body)
	if span == nil {
		return nil
	}
	c.Set(promptCacheDiagnosticContextKey, span)
	requestContext := c.Request.Context()
	stop := context.AfterFunc(requestContext, func() { span.canceled(requestContext.Err()) })
	return func() {
		stop()
		span.canceled(requestContext.Err())
		span.finish(c.Writer.Status())
	}
}

func (s *promptCacheDiagnosticSpan) canceled(err error) {
	if err == nil {
		return
	}
	s.registry.mu.Lock()
	defer s.registry.mu.Unlock()
	if s.done || s.record.CanceledAt != nil {
		return
	}
	now := s.registry.now().UTC()
	s.record.CanceledAt = &now
	s.record.CancelReason = "canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		s.record.CancelReason = "deadline_exceeded"
	}
}

func (s *promptCacheDiagnosticSpan) finish(status int) {
	s.registry.mu.Lock()
	defer s.registry.mu.Unlock()
	if s.done {
		return
	}
	s.done = true
	now := s.registry.now().UTC()
	s.record.FinishedAt = &now
	s.record.HTTPStatus = status
	s.ingress = nil
	s.job.inflight--
	s.registry.inflight--
}

func promptCacheSpan(c *gin.Context) *promptCacheDiagnosticSpan {
	if !openAIPromptCacheDiagnostics.enabled.Load() || c == nil {
		return nil
	}
	value, _ := c.Get(promptCacheDiagnosticContextKey)
	span, _ := value.(*promptCacheDiagnosticSpan)
	return span
}

var promptCacheDiagnosticHeaders = []string{"Authorization", "OpenAI-Organization", "OpenAI-Project", "ChatGPT-Account-Id", "Session_id", "Conversation_id", "X-Session-Id", "X-Codex-Turn-State", "OpenAI-Beta", "User-Agent", "Content-Type", "Content-Encoding"}

// Observe the actual send boundary without reading/replacing the live Body or
// restoring GetBody. preparedFinal is true only after the enabled recovery
// preparer has set Body and ContentLength from exactly the returned byte slice.
func beginOpenAIPromptCacheHTTPAttempt(c *gin.Context, account *Account, req *http.Request, prepared []byte, preparedFinal bool, proxyURL string) func(*http.Response, error) {
	s := promptCacheSpan(c)
	if s == nil || account == nil || req == nil || req.URL == nil {
		return nil
	}
	s.registry.mu.Lock()
	if s.done || len(s.record.Attempts) >= 8 {
		s.record.OmittedAttempts++
		s.current = nil
		s.registry.mu.Unlock()
		return nil
	}
	s.registry.mu.Unlock()
	var snapshot *promptCacheSnapshot
	if preparedFinal && req.ContentLength == int64(len(prepared)) {
		snapshot = buildPromptCacheSnapshot(s.job.key[:], prepared, "prepared_final_http_body")
	} else if req.GetBody != nil {
		reader, err := req.GetBody()
		if err == nil && reader != nil {
			body, readErr := io.ReadAll(io.LimitReader(reader, promptCacheDiagnosticMaxBody+1))
			_ = reader.Close()
			if readErr == nil {
				snapshot = buildPromptCacheSnapshot(s.job.key[:], body, "get_body_clone")
			}
		}
	}
	if snapshot == nil {
		snapshot = &promptCacheSnapshot{Source: "http_body_unavailable", Status: "unavailable", BodyBytes: len(prepared)}
	}
	headers := make(map[string]promptCacheFieldFingerprint, len(promptCacheDiagnosticHeaders))
	for _, name := range promptCacheDiagnosticHeaders {
		values := req.Header.Values(name)
		headers[name] = promptCacheValueFingerprint(s.job.key[:], values, len(values) != 0)
	}
	proxyID := int64(0)
	if account.ProxyID != nil {
		proxyID = *account.ProxyID
	}
	route := promptCacheDigest(s.job.key[:], []byte(fmt.Sprintf("%d\x00%d\x00%s\x00%s\x00%s\x00%s", account.ID, proxyID, req.Method, req.URL.String(), req.Host, proxyURL)))
	s.registry.mu.Lock()
	a := &promptCacheDiagnosticAttempt{Number: len(s.record.Attempts) + 1, AccountID: account.ID, ProxyID: proxyID, StartedAt: s.registry.now().UTC(),
		RouteHash: route, Headers: headers, Wire: promptCachePublicSnapshot(snapshot),
		IngressToWire: comparePromptCacheSnapshots(s.ingress, snapshot, s.record.Sequence),
		PreviousWire:  comparePromptCacheSnapshots(s.job.lastWire, snapshot, s.job.lastWireSeq), RouteEqual: route == s.job.lastRoute}
	if s.job.lastHeaders != nil {
		for _, name := range promptCacheDiagnosticHeaders {
			if headers[name] != s.job.lastHeaders[name] {
				a.ChangedHeaders = append(a.ChangedHeaders, name)
			}
		}
		sort.Strings(a.ChangedHeaders)
	}
	s.record.Attempts = append(s.record.Attempts, a)
	s.current = a
	s.job.lastWire, s.job.lastWireSeq = snapshot, s.record.Sequence
	s.job.lastHeaders, s.job.lastRoute = headers, route
	s.registry.mu.Unlock()
	return func(resp *http.Response, err error) {
		s.registry.mu.Lock()
		defer s.registry.mu.Unlock()
		now := s.registry.now().UTC()
		a.HeadersAt = &now
		a.TransportError = err != nil
		if resp != nil {
			a.HTTPStatus = resp.StatusCode
		}
	}
}

func observeOpenAIPromptCacheDiagnosticPayload(c *gin.Context, payload []byte) {
	s := promptCacheSpan(c)
	if s == nil || len(payload) > promptCacheDiagnosticMaxBody {
		return
	}
	s.registry.mu.Lock()
	defer s.registry.mu.Unlock()
	a := s.current
	if s.done || a == nil {
		return
	}
	now := s.registry.now().UTC()
	if a.FirstEventAt == nil {
		a.FirstEventAt = &now
	}
	kind := gjson.GetBytes(payload, "type").String()
	if isUpstreamResponseModelTerminalEvent(kind) {
		a.Terminal = kind
	}
	model := firstValidTrimmedGJSONString(payload, "response.model", "model")
	if promptCacheDiagnosticModel.MatchString(model) {
		a.UpstreamModel = model
	}
	if id := firstValidTrimmedGJSONString(payload, "response.id", "id"); id != "" {
		a.ResponseIDHash = promptCacheDigest(s.job.key[:], []byte(id))
	}
	for _, path := range []string{"response.system_fingerprint", "system_fingerprint"} {
		if value := gjson.GetBytes(payload, path); value.Exists() {
			a.SystemFingerprint = promptCacheValueFingerprint(s.job.key[:], value.Raw, true)
			break
		}
	}
	for _, prefix := range []string{"response.prompt_cache_diagnostics", "prompt_cache_diagnostics"} {
		if value := gjson.GetBytes(payload, prefix); value.IsObject() {
			a.CacheDiagnostics = make(map[string]promptCacheFieldFingerprint)
			for _, name := range []string{"type", "reason", "cache_missed_tokens", "comparison_reusable_tokens"} {
				v := value.Get(name)
				a.CacheDiagnostics[name] = promptCacheValueFingerprint(s.job.key[:], v.Raw, v.Exists())
			}
			break
		}
	}
	value := gjson.GetBytes(payload, "response.usage")
	if !value.IsObject() {
		value = gjson.GetBytes(payload, "usage")
	}
	if !value.IsObject() {
		return
	}
	u := &promptCacheDiagnosticUsage{Input: value.Get("input_tokens").Int(), Output: value.Get("output_tokens").Int()}
	if !value.Get("input_tokens").Exists() {
		u.Input = value.Get("prompt_tokens").Int()
	}
	if !value.Get("output_tokens").Exists() {
		u.Output = value.Get("completion_tokens").Int()
	}
	if u.Input < 0 {
		u.Input = 0
	}
	if u.Output < 0 {
		u.Output = 0
	}
	u.Read = int64(openAICacheReadTokensFromUsage(value))
	u.Write = int64(openAICacheCreationTokensFromUsage(value))
	for _, path := range []string{"input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens", "cache_read_input_tokens", "cache_read_tokens", "cached_tokens"} {
		u.CacheReadReported = u.CacheReadReported || value.Get(path).Exists()
	}
	for _, path := range []string{"input_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_write_tokens", "input_tokens_details.cache_creation_tokens", "prompt_tokens_details.cache_creation_tokens", "cache_write_tokens", "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens"} {
		u.CacheWriteReported = u.CacheWriteReported || value.Get(path).Exists()
	}
	a.Usage = u
}

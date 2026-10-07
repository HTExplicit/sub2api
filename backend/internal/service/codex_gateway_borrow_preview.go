package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	CodexGatewayBorrowPreviewTTL             = 30 * time.Minute
	codexGatewayBorrowPreviewMaxCapabilities = 4096
	// The HTTP header applies before any model-authored markup is parsed. The
	// iframe has the same sandbox without allow-same-origin, so its origin is opaque.
	CodexGatewayBorrowPreviewCSP = "default-src 'none'; sandbox allow-scripts; script-src 'unsafe-inline' http: https: data: blob:; style-src 'unsafe-inline' http: https: data: blob:; img-src http: https: data: blob:; font-src http: https: data: blob:; media-src data: blob:; connect-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; navigate-to 'none'"
)

var (
	codexGatewayBorrowHTMLMarker            = regexp.MustCompile(`(?i)<(?:!doctype\s+html|html|svg)[\s>]`)
	codexGatewayBorrowHTMLTag               = regexp.MustCompile(`(?i)<html[\s>]`)
	codexGatewayBorrowSVGTag                = regexp.MustCompile(`(?i)<svg[\s>]`)
	ErrCodexGatewayBorrowPreviewUnavailable = errors.New("preview capability unavailable or expired")
)

// Extraction follows ranxi2001/sub2api@5ca3cca's fenced/bare HTML/SVG
// selection and minimum SVG wrapper. Selecting the last valid fence follows
// iwyxdxl/gpttesticu@1b3e237's extractHtml. Their sanitizing/CSP rewrites are
// deliberately not used: raw source and JavaScript remain intact, with the
// boundary enforced by the response header and opaque sandbox instead.
// rawHTML is the selected source before any SVG wrapper is added.
func ExtractCodexGatewayBorrowHTML(answer string) (rawHTML, html string) {
	var selected string
	var outside strings.Builder
	for cursor := 0; cursor < len(answer); {
		opening := strings.Index(answer[cursor:], "```")
		if opening < 0 {
			_, _ = outside.WriteString(answer[cursor:])
			break
		}
		opening += cursor
		_, _ = outside.WriteString(answer[cursor:opening])
		bodyStart := opening + 3
		closing := strings.Index(answer[bodyStart:], "```")
		bodyEnd := len(answer)
		if closing >= 0 {
			bodyEnd = bodyStart + closing
		}
		body, allowed := codexGatewayBorrowHTMLFenceContent(answer[bodyStart:bodyEnd])
		if allowed && codexGatewayBorrowHTMLMarker.MatchString(body) {
			selected = strings.TrimSpace(body)
		}
		if closing < 0 {
			break
		}
		cursor = bodyEnd + 3
	}
	if selected == "" {
		selected = strings.TrimSpace(outside.String())
	}
	start := codexGatewayBorrowHTMLMarker.FindStringIndex(selected)
	if start == nil {
		return "", ""
	}
	selected = strings.TrimSpace(selected[start[0]:])
	lower := strings.ToLower(selected)
	if end := strings.LastIndex(lower, "</html>"); end >= 0 {
		selected = selected[:end+len("</html>")]
	}
	rawHTML = selected
	if !codexGatewayBorrowHTMLTag.MatchString(selected) && codexGatewayBorrowSVGTag.MatchString(selected) {
		return rawHTML, `<!doctype html><html><head><meta charset="utf-8"><title>Pelican test</title><style>html,body{margin:0;height:100%}body{display:flex;align-items:center;justify-content:center}svg{max-width:100%;max-height:100%}</style></head><body>` + selected + `</body></html>`
	}
	return rawHTML, selected
}

func codexGatewayBorrowHTMLFenceContent(body string) (string, bool) {
	if newline := strings.IndexByte(body, '\n'); newline >= 0 {
		language := strings.ToLower(strings.TrimSpace(body[:newline]))
		if language == "" || language == "html" || language == "htm" || language == "xml" || language == "svg" {
			return body[newline+1:], true
		}
	}
	// Inline fenced markup has no standalone language line.
	trimmed := strings.TrimSpace(body)
	for _, language := range []string{"html", "htm", "xml", "svg"} {
		if len(trimmed) > len(language) && strings.EqualFold(trimmed[:len(language)], language) && (trimmed[len(language)] == ' ' || trimmed[len(language)] == '\t') {
			return strings.TrimSpace(trimmed[len(language):]), true
		}
	}
	return trimmed, strings.HasPrefix(trimmed, "<")
}

type codexGatewayBorrowPreviewCapability struct {
	ResultID  string
	ExpiresAt time.Time
}

type CodexGatewayBorrowPreviewService struct {
	repo         CodexGatewayBorrowTestRepository
	mu           sync.Mutex
	capabilities map[string]codexGatewayBorrowPreviewCapability
	stopCh       chan struct{}
	doneCh       chan struct{}
	stopOnce     sync.Once
	now          func() time.Time
	randomToken  func() (string, error)
}

func NewCodexGatewayBorrowPreviewService(repo CodexGatewayBorrowTestRepository) *CodexGatewayBorrowPreviewService {
	p := &CodexGatewayBorrowPreviewService{repo: repo, capabilities: make(map[string]codexGatewayBorrowPreviewCapability),
		stopCh: make(chan struct{}), doneCh: make(chan struct{}), now: time.Now, randomToken: randomCodexGatewayBorrowPreviewToken}
	go p.cleanupLoop()
	return p
}

func randomCodexGatewayBorrowPreviewToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func (p *CodexGatewayBorrowPreviewService) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh); <-p.doneCh; p.mu.Lock(); clear(p.capabilities); p.mu.Unlock() })
}

func (p *CodexGatewayBorrowPreviewService) cleanupLoop() {
	defer close(p.doneCh)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.mu.Lock()
			p.pruneLocked(p.now().UTC())
			p.mu.Unlock()
		}
	}
}

func (p *CodexGatewayBorrowPreviewService) pruneLocked(now time.Time) {
	for token, capability := range p.capabilities {
		if !capability.ExpiresAt.After(now) {
			delete(p.capabilities, token)
		}
	}
}

// DecorateResult only adds an independently minted read capability. It makes no
// upstream request and never turns incomplete HTML into a successful test.
func (p *CodexGatewayBorrowPreviewService) DecorateResult(result *CodexGatewayBorrowTestResult) *CodexGatewayBorrowTestResult {
	decorated := *result
	decorated.PreviewURL, decorated.PreviewExpiresAt = "", nil
	if decorated.Status != "complete" {
		decorated.PreviewUnavailable = "test " + decorated.Status
		return &decorated
	}
	if decorated.HTML == "" {
		decorated.PreviewUnavailable = "answer contains no extractable HTML or SVG"
		return &decorated
	}
	now := p.now().UTC()
	if !decorated.ExpiresAt.After(now) {
		decorated.PreviewUnavailable = "test expired"
		return &decorated
	}
	expires := now.Add(CodexGatewayBorrowPreviewTTL)
	if decorated.ExpiresAt.Before(expires) {
		expires = decorated.ExpiresAt
	}
	token, err := p.randomToken()
	if err != nil {
		decorated.PreviewUnavailable = "preview capability creation failed: " + err.Error()
		return &decorated
	}
	p.mu.Lock()
	p.pruneLocked(now)
	// Bound the in-memory capability store even if many administrators keep
	// reloading results. The oldest capability expires first and is evicted.
	if len(p.capabilities) >= codexGatewayBorrowPreviewMaxCapabilities {
		var oldestToken string
		var oldest time.Time
		for candidate, capability := range p.capabilities {
			if oldestToken == "" || capability.ExpiresAt.Before(oldest) {
				oldestToken, oldest = candidate, capability.ExpiresAt
			}
		}
		delete(p.capabilities, oldestToken)
	}
	p.capabilities[token] = codexGatewayBorrowPreviewCapability{ResultID: decorated.ID, ExpiresAt: expires}
	p.mu.Unlock()
	decorated.PreviewURL = "/api/v1/codex-gateway-borrow/preview/" + token + "/index.html"
	decorated.PreviewExpiresAt, decorated.PreviewUnavailable = &expires, ""
	return &decorated
}

func (p *CodexGatewayBorrowPreviewService) DecorateTask(task *CodexGatewayBorrowTestTask) *CodexGatewayBorrowTestTask {
	decorated := *task
	if task.Results != nil {
		decorated.Results = make([]*CodexGatewayBorrowTestResult, 0, len(task.Results))
		for _, result := range task.Results {
			decorated.Results = append(decorated.Results, p.DecorateResult(result))
		}
	}
	return &decorated
}

// Resolve checks the capability and re-reads exactly its bound result. Expired
// task data is inaccessible even when the periodic SQL cleanup has not run.
func (p *CodexGatewayBorrowPreviewService) Resolve(ctx context.Context, token string) (string, error) {
	if len(token) != 43 {
		return "", ErrCodexGatewayBorrowPreviewUnavailable
	}
	p.mu.Lock()
	now := p.now().UTC()
	capability, found := p.capabilities[token]
	if !found || !capability.ExpiresAt.After(now) {
		delete(p.capabilities, token)
		p.mu.Unlock()
		return "", ErrCodexGatewayBorrowPreviewUnavailable
	}
	p.mu.Unlock()
	result, err := p.repo.GetResult(ctx, capability.ResultID)
	finishedRead := p.now().UTC()
	if err != nil || result == nil || result.ID != capability.ResultID || result.Status != "complete" || result.HTML == "" || !result.ExpiresAt.After(finishedRead) || !capability.ExpiresAt.After(finishedRead) {
		p.mu.Lock()
		delete(p.capabilities, token)
		p.mu.Unlock()
		return "", ErrCodexGatewayBorrowPreviewUnavailable
	}
	return result.HTML, nil
}

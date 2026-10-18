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
	PelicanPreviewTTL             = 30 * time.Minute
	pelicanPreviewMaxCapabilities = 4096
	// The HTTP header applies before any model-authored markup is parsed. The
	// iframe has the same sandbox without allow-same-origin, so its origin is opaque.
	PelicanPreviewCSP = "default-src 'none'; sandbox allow-scripts; script-src 'unsafe-inline' http: https: data: blob:; style-src 'unsafe-inline' http: https: data: blob:; img-src http: https: data: blob:; font-src http: https: data: blob:; media-src data: blob:; connect-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; navigate-to 'none'"
)

var (
	pelicanHTMLMarker            = regexp.MustCompile(`(?i)<(?:!doctype\s+html|html|svg)[\s>]`)
	pelicanHTMLTag               = regexp.MustCompile(`(?i)<html[\s>]`)
	pelicanSVGTag                = regexp.MustCompile(`(?i)<svg[\s>]`)
	ErrPelicanPreviewUnavailable = errors.New("preview capability unavailable or expired")
)

// Extraction follows ranxi2001/sub2api@5ca3cca's fenced/bare HTML/SVG
// selection and minimum SVG wrapper. Selecting the last valid fence follows
// iwyxdxl/gpttesticu@1b3e237's extractHtml. Their sanitizing/CSP rewrites are
// deliberately not used: raw source and JavaScript remain intact, with the
// boundary enforced by the response header and opaque sandbox instead.
// rawHTML is the selected source before any SVG wrapper is added.
func ExtractPelicanHTML(answer string) (rawHTML, html string) {
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
		body, allowed := pelicanHTMLFenceContent(answer[bodyStart:bodyEnd])
		if allowed && pelicanHTMLMarker.MatchString(body) {
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
	start := pelicanHTMLMarker.FindStringIndex(selected)
	if start == nil {
		return "", ""
	}
	selected = strings.TrimSpace(selected[start[0]:])
	lower := strings.ToLower(selected)
	if end := strings.LastIndex(lower, "</html>"); end >= 0 {
		selected = selected[:end+len("</html>")]
	}
	rawHTML = selected
	if !pelicanHTMLTag.MatchString(selected) && pelicanSVGTag.MatchString(selected) {
		return rawHTML, `<!doctype html><html><head><meta charset="utf-8"><title>Pelican test</title><style>html,body{margin:0;height:100%}body{display:flex;align-items:center;justify-content:center}svg{max-width:100%;max-height:100%}</style></head><body>` + selected + `</body></html>`
	}
	return rawHTML, selected
}

func pelicanHTMLFenceContent(body string) (string, bool) {
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

type pelicanPreviewCapability struct {
	ResultID  string
	ExpiresAt time.Time
}

type PelicanPreviewService struct {
	repo         PelicanTestRepository
	mu           sync.Mutex
	capabilities map[string]pelicanPreviewCapability
	stopCh       chan struct{}
	doneCh       chan struct{}
	stopOnce     sync.Once
	now          func() time.Time
	randomToken  func() (string, error)
}

func NewPelicanPreviewService(repo PelicanTestRepository) *PelicanPreviewService {
	p := &PelicanPreviewService{repo: repo, capabilities: make(map[string]pelicanPreviewCapability),
		stopCh: make(chan struct{}), doneCh: make(chan struct{}), now: time.Now, randomToken: randomPelicanPreviewToken}
	go p.cleanupLoop()
	return p
}

func randomPelicanPreviewToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func (p *PelicanPreviewService) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh); <-p.doneCh; p.mu.Lock(); clear(p.capabilities); p.mu.Unlock() })
}

func (p *PelicanPreviewService) cleanupLoop() {
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

func (p *PelicanPreviewService) pruneLocked(now time.Time) {
	for token, capability := range p.capabilities {
		if !capability.ExpiresAt.After(now) {
			delete(p.capabilities, token)
		}
	}
}

// PelicanResultHasPreview separates saved generated works from later test failures.
// Legacy partial HTML remains unavailable unless generation completed.
func PelicanResultHasPreview(result *PelicanTestResult) bool {
	return result.HTML != "" && (result.SVG != "" || result.Status == "complete" || result.Evaluation != nil && result.Evaluation.Generation == "passed")
}

// DecorateResult only adds an independently minted read capability. It makes no
// upstream request and never turns incomplete HTML into a successful test.
func (p *PelicanPreviewService) DecorateResult(result *PelicanTestResult) *PelicanTestResult {
	decorated := *result
	decorated.PreviewURL, decorated.PreviewExpiresAt = "", nil
	if !PelicanResultHasPreview(&decorated) && decorated.HTML != "" {
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
	expires := now.Add(PelicanPreviewTTL)
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
	if len(p.capabilities) >= pelicanPreviewMaxCapabilities {
		var oldestToken string
		var oldest time.Time
		for candidate, capability := range p.capabilities {
			if oldestToken == "" || capability.ExpiresAt.Before(oldest) {
				oldestToken, oldest = candidate, capability.ExpiresAt
			}
		}
		delete(p.capabilities, oldestToken)
	}
	p.capabilities[token] = pelicanPreviewCapability{ResultID: decorated.ID, ExpiresAt: expires}
	p.mu.Unlock()
	asset := "index.html"
	if decorated.SVG != "" {
		asset = "scene.svg"
	}
	decorated.PreviewURL = "/api/v1/pelican-tests/preview/" + token + "/" + asset
	decorated.PreviewExpiresAt, decorated.PreviewUnavailable = &expires, ""
	return &decorated
}

func (p *PelicanPreviewService) DecorateTask(task *PelicanTestTask) *PelicanTestTask {
	decorated := *task
	if task.Results != nil {
		decorated.Results = make([]*PelicanTestResult, 0, len(task.Results))
		for _, result := range task.Results {
			decorated.Results = append(decorated.Results, p.DecorateResult(result))
		}
	}
	return &decorated
}

// Resolve checks the capability and re-reads exactly its bound result. Expired
// task data is inaccessible even when the periodic SQL cleanup has not run.
func (p *PelicanPreviewService) Resolve(ctx context.Context, token string) (string, error) {
	if len(token) != 43 {
		return "", ErrPelicanPreviewUnavailable
	}
	p.mu.Lock()
	now := p.now().UTC()
	capability, found := p.capabilities[token]
	if !found || !capability.ExpiresAt.After(now) {
		delete(p.capabilities, token)
		p.mu.Unlock()
		return "", ErrPelicanPreviewUnavailable
	}
	p.mu.Unlock()
	var result *PelicanTestResult
	var err error
	if reader, ok := p.repo.(interface {
		GetPreviewResult(context.Context, string) (*PelicanTestResult, error)
	}); ok {
		result, err = reader.GetPreviewResult(ctx, capability.ResultID)
	} else {
		result, err = p.repo.GetResult(ctx, capability.ResultID)
	}
	finishedRead := p.now().UTC()
	if err != nil || result == nil || result.ID != capability.ResultID || !PelicanResultHasPreview(result) || !result.ExpiresAt.After(finishedRead) || !capability.ExpiresAt.After(finishedRead) {
		p.mu.Lock()
		delete(p.capabilities, token)
		p.mu.Unlock()
		return "", ErrPelicanPreviewUnavailable
	}
	return result.HTML, nil
}

func (p *PelicanPreviewService) DecorateSummary(summary *PelicanResultSummary) {
	if !summary.HasPreview {
		return
	}
	// Metadata is enough to issue a capability; no original answer is loaded.
	result := &PelicanTestResult{ID: summary.ID, Status: "complete", HTML: "preview", ExpiresAt: summary.ExpiresAt}
	if summary.SVGPreview {
		result.SVG = "svg"
	}
	summary.PreviewURL = p.DecorateResult(result).PreviewURL
}

func (p *PelicanPreviewService) ResolveSVG(ctx context.Context, token string) (string, error) {
	html, err := p.Resolve(ctx, token)
	if err != nil {
		return "", err
	}
	raw, _ := ExtractPelicanHTML(html)
	start := pelicanSVGTag.FindStringIndex(raw)
	if start == nil {
		return "", ErrPelicanPreviewUnavailable
	}
	end := strings.LastIndex(strings.ToLower(raw), "</svg>")
	if end < start[0] {
		return "", ErrPelicanPreviewUnavailable
	}
	return raw[start[0] : end+6], nil
}

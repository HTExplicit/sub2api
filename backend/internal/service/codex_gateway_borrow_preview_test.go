//go:build unit

package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCodexGatewayBorrowHTMLExtractionPreservesSelectedSourceAndScripts(t *testing.T) {
	const final = `<html><head><script src="https://example.test/animation.js"></script></head><body><svg><script>window.animatePelican()</script></svg></body></html>`
	raw, html := ExtractCodexGatewayBorrowHTML("reasoning\n```json\n{}\n```\n```html\n<html>old</html>\n```\n```html\n" + final + "\n```\nfinished")
	require.Equal(t, final, raw)
	require.Equal(t, final, html)
	require.NotContains(t, html, "Content-Security-Policy")
	const svg = `<svg viewBox="0 0 800 600"><script>const pelican = "keep & < >"</script><animate dur="2s"/></svg>`
	raw, html = ExtractCodexGatewayBorrowHTML("```svg\n" + svg + "\n```")
	require.Equal(t, svg, raw)
	require.Contains(t, html, svg)
	require.Contains(t, html, `<meta charset="utf-8">`)
	require.NotContains(t, html, "DOMPurify")
	raw, html = ExtractCodexGatewayBorrowHTML("Explanation\n<!DOCTYPE html><HTML><BODY>pelican</BODY></HTML>\nmore prose")
	require.Equal(t, `<!DOCTYPE html><HTML><BODY>pelican</BODY></HTML>`, raw)
	require.Equal(t, raw, html)
	raw, html = ExtractCodexGatewayBorrowHTML("There is a pelican riding a bicycle.")
	require.Empty(t, raw)
	require.Empty(t, html)
	raw, html = ExtractCodexGatewayBorrowHTML("```json\n{\"example\":\"<svg>quoted markup</svg>\"}\n```")
	require.Empty(t, raw, "markup quoted in a non-HTML code fence is not a preview")
	require.Empty(t, html)
}

func TestCodexGatewayBorrowHTMLExtractionAcceptsUnclosedValidFenceWithoutRepair(t *testing.T) {
	const partial = `<html><body><svg><script>animate(`
	raw, html := ExtractCodexGatewayBorrowHTML("```html\n" + partial)
	require.Equal(t, partial, raw)
	require.Equal(t, partial, html)
}

func TestCodexGatewayBorrowPreviewCapabilityExpiryAndRecordRecheck(t *testing.T) {
	now := time.Now().UTC()
	repo := newCodexGatewayBorrowMemoryTestRepo()
	result := &CodexGatewayBorrowTestResult{ID: uuid.NewString(), Status: "complete", HTML: "<html>one result</html>", ExpiresAt: now.Add(10 * time.Minute)}
	repo.resultByID[result.ID] = result
	preview := NewCodexGatewayBorrowPreviewService(repo)
	t.Cleanup(preview.Stop)
	preview.now = func() time.Time { return now }
	decorated := preview.DecorateResult(result)
	require.NotEmpty(t, decorated.PreviewURL)
	require.Empty(t, decorated.PreviewUnavailable)
	require.Equal(t, result.ExpiresAt, *decorated.PreviewExpiresAt)
	require.Empty(t, result.PreviewURL, "minting must not mutate the stored result")
	token := strings.TrimSuffix(strings.TrimPrefix(decorated.PreviewURL, "/api/v1/codex-gateway-borrow/preview/"), "/index.html")
	require.Len(t, token, 43)
	html, err := preview.Resolve(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, result.HTML, html)
	result.ExpiresAt = now.Add(-time.Nanosecond)
	_, err = preview.Resolve(context.Background(), token)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowPreviewUnavailable)
	require.NotContains(t, preview.capabilities, token)
	result.ExpiresAt = now.Add(time.Hour)
	decorated = preview.DecorateResult(result)
	require.Equal(t, now.Add(CodexGatewayBorrowPreviewTTL), *decorated.PreviewExpiresAt)
	token = strings.TrimSuffix(strings.TrimPrefix(decorated.PreviewURL, "/api/v1/codex-gateway-borrow/preview/"), "/index.html")
	now = now.Add(CodexGatewayBorrowPreviewTTL)
	reads := repo.resultReads
	_, err = preview.Resolve(context.Background(), token)
	require.ErrorIs(t, err, ErrCodexGatewayBorrowPreviewUnavailable)
	require.Equal(t, reads, repo.resultReads, "expired capabilities must not read or mint a result")
}

func TestCodexGatewayBorrowPreviewDoesNotMintFailedOrExpiredResultsAndBoundsStore(t *testing.T) {
	now := time.Now().UTC()
	preview := NewCodexGatewayBorrowPreviewService(newCodexGatewayBorrowMemoryTestRepo())
	t.Cleanup(preview.Stop)
	preview.now = func() time.Time { return now }
	for _, result := range []*CodexGatewayBorrowTestResult{
		{Status: "incomplete", HTML: "<html>partial</html>", ExpiresAt: now.Add(time.Hour)},
		{Status: "complete", ExpiresAt: now.Add(time.Hour)},
		{Status: "complete", HTML: "<html>old</html>", ExpiresAt: now},
	} {
		decorated := preview.DecorateResult(result)
		require.Empty(t, decorated.PreviewURL)
		require.NotEmpty(t, decorated.PreviewUnavailable)
	}
	for i := 0; i < codexGatewayBorrowPreviewMaxCapabilities; i++ {
		preview.capabilities[fmt.Sprintf("cap-%d", i)] = codexGatewayBorrowPreviewCapability{ResultID: "old", ExpiresAt: now.Add(time.Duration(i+1) * time.Second)}
	}
	decorated := preview.DecorateResult(&CodexGatewayBorrowTestResult{ID: "new", Status: "complete", HTML: "<html>new</html>", ExpiresAt: now.Add(time.Hour)})
	require.NotEmpty(t, decorated.PreviewURL)
	require.Len(t, preview.capabilities, codexGatewayBorrowPreviewMaxCapabilities)
	require.NotContains(t, preview.capabilities, "cap-0")
	require.Contains(t, CodexGatewayBorrowPreviewCSP, "sandbox allow-scripts")
	require.NotContains(t, CodexGatewayBorrowPreviewCSP, "allow-same-origin")
	for _, directive := range []string{"connect-src 'none'", "frame-src 'none'", "object-src 'none'", "form-action 'none'", "base-uri 'none'", "frame-ancestors 'self'"} {
		require.Contains(t, CodexGatewayBorrowPreviewCSP, directive)
	}
}

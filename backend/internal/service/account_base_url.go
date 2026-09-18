package service

import (
	"net/url"
	"strings"
)

// NormalizeUpstreamBaseURLInput accepts the shorthand form commonly pasted
// into the admin account form (for example, "relay.example.com/v1") while
// leaving explicit URL schemes untouched.  URL validation still happens at
// the request boundary, so this helper only supplies the conventional HTTPS
// scheme; it does not allow private hosts or bypass the configured allowlist.
func NormalizeUpstreamBaseURLInput(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "//") {
		return "https:" + trimmed
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "https://" + trimmed
}

// NormalizeAccountCredentialBaseURLs normalizes the URL fields used by
// OpenAI-compatible API-key accounts.  It is intentionally idempotent and
// handles both JSON-decoded map[string]any and typed map[string]string values
// used by imports/tests.
func NormalizeAccountCredentialBaseURLs(credentials map[string]any) {
	if credentials == nil {
		return
	}
	if raw, ok := credentials["base_url"].(string); ok {
		credentials["base_url"] = NormalizeUpstreamBaseURLInput(raw)
	}
	switch values := credentials["api_base_urls"].(type) {
	case map[string]any:
		for key, raw := range values {
			if value, ok := raw.(string); ok {
				values[key] = NormalizeUpstreamBaseURLInput(value)
			}
		}
	case map[string]string:
		for key, value := range values {
			values[key] = NormalizeUpstreamBaseURLInput(value)
		}
	}
}

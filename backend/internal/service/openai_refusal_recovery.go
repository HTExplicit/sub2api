package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/unicode/norm"
)

const (
	maxOpenAIRefusalKeywords        = 128
	maxOpenAIRefusalKeywordRunes    = 64
	maxOpenAIRefusalReplacementSize = 8192
	maxOpenAIRefusalParagraphRunes  = 2048
)

var ErrInvalidOpenAIRefusalRecovery = errors.New("invalid OpenAI refusal recovery settings")

const OpenAIRefusalRecoveryReason GatewayFailureReason = "openai_refusal_recovery"

const defaultOpenAIRefusalKeywordsJSON = `["抱歉","无法","违反","不能","拒绝","不允许","禁止","很抱歉","对不起","不好意思","我无法","我不能","sorry","cannot","apologize","violate","policy","as an AI","I cannot","I'm unable","not able to","against my","I won't","refuse to","unable to","I apologize","not permitted","not allowed"]`

var defaultOpenAIRefusalKeywords = []string{
	"抱歉", "无法", "违反", "不能", "拒绝", "不允许", "禁止", "很抱歉", "对不起", "不好意思", "我无法", "我不能",
	"sorry", "cannot", "apologize", "violate", "policy", "as an AI", "I cannot", "I'm unable", "not able to", "against my", "I won't", "refuse to", "unable to", "I apologize", "not permitted", "not allowed",
}

type openAIRefusalKeyword struct {
	original   string
	normalized string
}

type OpenAIRefusalMatcher struct {
	keywords    []openAIRefusalKeyword
	replacement string
}

func DefaultOpenAIRefusalKeywords() []string {
	return append([]string(nil), defaultOpenAIRefusalKeywords...)
}

func parseOpenAIRefusalKeywordsSetting(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return DefaultOpenAIRefusalKeywords()
	}
	var keywords []string
	if err := json.Unmarshal([]byte(raw), &keywords); err != nil {
		return DefaultOpenAIRefusalKeywords()
	}
	normalized, err := NormalizeOpenAIRefusalKeywords(keywords)
	if err != nil {
		return DefaultOpenAIRefusalKeywords()
	}
	return normalized
}

func NewOpenAIRefusalMatcher(keywords []string, replacement string) (*OpenAIRefusalMatcher, error) {
	normalizedKeywords, err := NormalizeOpenAIRefusalKeywords(keywords)
	if err != nil || len(normalizedKeywords) == 0 {
		return nil, ErrInvalidOpenAIRefusalRecovery
	}
	if strings.TrimSpace(replacement) == "" || len([]byte(replacement)) > maxOpenAIRefusalReplacementSize {
		return nil, ErrInvalidOpenAIRefusalRecovery
	}

	normalized := make([]openAIRefusalKeyword, 0, len(normalizedKeywords))
	for _, trimmed := range normalizedKeywords {
		normalizedKeyword := normalizeOpenAIRefusalText(trimmed)
		normalized = append(normalized, openAIRefusalKeyword{original: trimmed, normalized: normalizedKeyword})
	}

	return &OpenAIRefusalMatcher{keywords: normalized, replacement: replacement}, nil
}

func NormalizeOpenAIRefusalKeywords(keywords []string) ([]string, error) {
	if len(keywords) > maxOpenAIRefusalKeywords {
		return nil, fmt.Errorf("openai_refusal_keywords exceeds %d entries: %w", maxOpenAIRefusalKeywords, ErrInvalidOpenAIRefusalRecovery)
	}
	normalized := make([]string, 0, len(keywords))
	seen := make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		trimmed := strings.TrimSpace(keyword)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > maxOpenAIRefusalKeywordRunes {
			return nil, fmt.Errorf("openai_refusal_keywords entries must contain 1-%d Unicode characters: %w", maxOpenAIRefusalKeywordRunes, ErrInvalidOpenAIRefusalRecovery)
		}
		normalizedKeyword := normalizeOpenAIRefusalText(trimmed)
		if normalizedKeyword == "" {
			return nil, ErrInvalidOpenAIRefusalRecovery
		}
		if _, ok := seen[normalizedKeyword]; ok {
			continue
		}
		seen[normalizedKeyword] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized, nil
}

func ValidateOpenAIRefusalRecoverySettings(settings *SystemSettings) error {
	if settings == nil {
		return ErrInvalidOpenAIRefusalRecovery
	}
	if len([]byte(settings.OpenAIRefusalReplacement)) > maxOpenAIRefusalReplacementSize {
		return fmt.Errorf("openai_refusal_replacement exceeds %d bytes: %w", maxOpenAIRefusalReplacementSize, ErrInvalidOpenAIRefusalRecovery)
	}
	if settings.OpenAIRefusalRecoveryEnabled && settings.OpenAICyberFailoverEnabled && settings.CyberSessionBlockEnabled {
		return fmt.Errorf("disable cyber_session_block_enabled before enabling openai_cyber_failover_enabled: %w", ErrInvalidOpenAIRefusalRecovery)
	}
	if settings.OpenAIRefusalRecoveryEnabled && settings.OpenAIRefusalRewriteEnabled {
		if len(settings.OpenAIRefusalKeywords) == 0 || strings.TrimSpace(settings.OpenAIRefusalReplacement) == "" {
			return fmt.Errorf("openai_refusal_keywords and openai_refusal_replacement are required when rewrite is enabled: %w", ErrInvalidOpenAIRefusalRecovery)
		}
	}
	return nil
}

func (m *OpenAIRefusalMatcher) MatchFirstParagraph(text string) (bool, string) {
	if m == nil {
		return false, ""
	}
	paragraph := normalizeOpenAIRefusalText(firstOpenAIRefusalParagraph(text))
	for _, keyword := range m.keywords {
		if strings.Contains(paragraph, keyword.normalized) {
			return true, keyword.original
		}
	}
	return false, ""
}

func (m *OpenAIRefusalMatcher) Replacement() string {
	if m == nil {
		return ""
	}
	return m.replacement
}

func normalizeOpenAIRefusalText(value string) string {
	value = norm.NFKC.String(value)
	value = strings.NewReplacer(
		"‘", "'", "’", "'", "‚", "'", "‛", "'",
		"“", "\"", "”", "\"", "„", "\"", "‟", "\"",
	).Replace(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return unicode.ToLower(r)
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func firstOpenAIRefusalParagraph(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	paragraph := make([]string, 0, len(lines))
	for index, line := range lines {
		if index > 0 && strings.TrimSpace(line) == "" {
			break
		}
		paragraph = append(paragraph, line)
	}
	runes := []rune(strings.Join(paragraph, "\n"))
	if len(runes) > maxOpenAIRefusalParagraphRunes {
		runes = runes[:maxOpenAIRefusalParagraphRunes]
	}
	return string(runes)
}

func NewOpenAICyberFailoverError(_ []byte, upstreamHeaders http.Header) *UpstreamFailoverError {
	headers := make(http.Header)
	if upstreamHeaders != nil {
		headers = upstreamHeaders.Clone()
	}
	headers.Set("Retry-After", "1")
	return &UpstreamFailoverError{
		StatusCode:                   http.StatusServiceUnavailable,
		ResponseBody:                 []byte(`{"error":{"message":"Temporary upstream failure","type":"server_error","code":"server_error"}}`),
		ResponseHeaders:              headers,
		RetryableOnSameAccount:       false,
		SafeToFailoverAfterWrite:     true,
		Stage:                        GatewayFailureStageInference,
		Scope:                        GatewayFailureScopeRequest,
		Reason:                       OpenAIRefusalRecoveryReason,
		NextAccountAction:            NextAccountRetry,
		ClientStatusCode:             http.StatusServiceUnavailable,
		ClientMessage:                "Temporary upstream failure",
		SuppressAccountHealthPenalty: true,
	}
}

func (e *UpstreamFailoverError) IsOpenAIRefusalRecovery() bool {
	return e != nil && e.Reason == OpenAIRefusalRecoveryReason
}

func IsOpenAIRefusalRecoveryFailover(err error) bool {
	var failoverErr *UpstreamFailoverError
	return errors.As(err, &failoverErr) && failoverErr.IsOpenAIRefusalRecovery()
}

func (s *OpenAIGatewayService) openAIRefusalRecoveryRuntime(ctx context.Context) OpenAIRefusalRecoveryRuntime {
	if s == nil || s.settingService == nil {
		return OpenAIRefusalRecoveryRuntime{}
	}
	return s.settingService.GetOpenAIRefusalRecoveryRuntime(ctx)
}

func isOpenAIRefusalRecoveryResponsesRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	path := strings.TrimSuffix(c.Request.URL.Path, "/")
	return path == "/responses" || path == "/v1/responses"
}

func markOpenAICyberPolicyFromResponse(c *gin.Context, status int, body []byte) bool {
	hit, code, message := detectOpenAICyberPolicy(body)
	if !hit {
		return false
	}
	usage, _ := extractOpenAIUsageFromJSONBytes(body)
	MarkOpsCyberPolicy(c, CyberPolicyMark{
		Code:           code,
		Message:        message,
		Body:           truncateString(string(body), 4096),
		UpstreamStatus: status,
		UpstreamInTok:  usage.InputTokens,
		UpstreamOutTok: usage.OutputTokens,
	})
	return true
}

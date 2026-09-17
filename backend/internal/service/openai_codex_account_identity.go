package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// codexAccountIdentityNamespaceVersion 进入派生种子；v2 起 UUIDv7 保留时间位且同一原始值
// 在所有字段映射到同一结果。
const codexAccountIdentityNamespaceVersion = "v2"

var codexIdentityUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const codexAccountIdentitySourceContextKey = "openai_codex_account_identity_source"

// prepareCodexAccountIdentitySource resolves credential shadows once per selected
// attempt. The handler reuses gin.Context across failover attempts, so every entry
// point overwrites the staged source before projecting outbound identity.
func (s *OpenAIGatewayService) prepareCodexAccountIdentitySource(ctx context.Context, c *gin.Context, account *Account) (*Account, error) {
	source := account
	if account != nil && account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, err
		}
		source = resolved
	}
	if c != nil {
		c.Set(codexAccountIdentitySourceContextKey, source)
	}
	return source, nil
}

func codexAccountIdentitySource(c *gin.Context, fallback *Account) *Account {
	if c != nil {
		if staged, ok := c.Get(codexAccountIdentitySourceContextKey); ok {
			if source, ok := staged.(*Account); ok && source != nil {
				return source
			}
		}
	}
	return fallback
}

// codexAccountIdentityNamespace returns a stable, credential-scoped namespace.
// Multiple local rows that use the same ChatGPT account intentionally share the
// same namespace. Setup tokens use an irreversible bearer fingerprint because
// they have no refresh lifecycle or imported account metadata. Refreshable OAuth
// otherwise falls back only to a persistent fingerprint seed: local row IDs are
// deployment-relative and must never become upstream identity.
func codexAccountIdentityNamespace(account *Account) string {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return ""
	}
	if upstreamAccountID := strings.TrimSpace(account.GetChatGPTAccountID()); upstreamAccountID != "" {
		if upstreamUserID := strings.TrimSpace(account.GetCredential("chatgpt_user_id")); upstreamUserID != "" {
			return "chatgpt:" + upstreamAccountID + ":user:" + upstreamUserID
		}
		return "chatgpt:" + upstreamAccountID
	}
	if seed, ok := codexFingerprintSeed(account.Extra); ok {
		return "seed:" + seed
	}
	if account.Type == AccountTypeSetupToken {
		if token := strings.TrimSpace(account.GetOpenAIAccessToken()); token != "" {
			sum := sha256.Sum256([]byte("openai-setup-token:" + token))
			return fmt.Sprintf("setup-token:%x", sum[:16])
		}
	}
	return ""
}

// isolateOpenAIUpstreamSessionID preserves the existing API-key isolation while
// adding the selected OAuth credential namespace. A scheduler failover therefore
// cannot send the same session/conversation identity through two upstream accounts.
func isolateOpenAIUpstreamSessionID(apiKeyID int64, account *Account, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	namespace := codexAccountIdentityNamespace(account)
	if namespace == "" {
		return isolateOpenAISessionID(apiKeyID, raw)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("u%d:a%s:%s", apiKeyID, namespace, raw)))
	return fmt.Sprintf("%x", sum[:8])
}

// scopeCodexAccountIdentityValue 把客户端标识按 (API Key, OAuth 凭据) 作用域改写，
// 并保持真实 Codex 的形态不变式：
//   - 同一个原始 UUID 无论出现在哪个字段（session-id 头、thread-id 头、x-client-request-id、
//     client_metadata.session_id/thread_id、prompt_cache_key）都映射到同一个值，
//     因此 root 线程的 session == thread == request id == prompt_cache_key 仍然成立；
//   - UUIDv7 保留 48 位时间戳与版本位，只替换随机位，改写后仍是合法的 Codex 形态 UUIDv7；
//   - "<uuid>:<n>"（x-codex-window-id）只改写 uuid 前缀，保留 ":<n>"；
//   - "<source>:<uuid>"（内部子代理的 prompt_cache_key）只改写 uuid 部分；
//   - 其他形态退化为确定性 UUIDv4。
func scopeCodexAccountIdentityValue(account *Account, apiKeyID int64, raw string) string {
	raw = strings.TrimSpace(raw)
	namespace := codexAccountIdentityNamespace(account)
	if raw == "" || namespace == "" {
		return raw
	}
	if codexIdentityUUIDPattern.MatchString(raw) {
		return scopeCodexIdentityUUID(apiKeyID, namespace, raw)
	}
	if idx := strings.IndexByte(raw, ':'); idx > 0 && idx < len(raw)-1 {
		head, tail := raw[:idx], raw[idx+1:]
		if codexIdentityUUIDPattern.MatchString(head) {
			return scopeCodexIdentityUUID(apiKeyID, namespace, head) + ":" + tail
		}
		if codexIdentityUUIDPattern.MatchString(tail) {
			return head + ":" + scopeCodexIdentityUUID(apiKeyID, namespace, tail)
		}
	}
	return deriveStableUUIDv4(codexAccountIdentityScopeSeed(apiKeyID, namespace, raw))
}

func codexAccountIdentityScopeSeed(apiKeyID int64, namespace, raw string) string {
	return fmt.Sprintf(
		"sub2api:codex-account-identity:%s:user:%d:account:%s:value:%s",
		codexAccountIdentityNamespaceVersion,
		apiKeyID,
		namespace,
		raw,
	)
}

// scopeCodexIdentityUUID 改写单个 UUID：v7 保留前 48 位时间戳与版本位，其余位由
// 作用域种子确定性派生；非 v7 退化为确定性 UUIDv4（如 installation_id 本就是 v4）。
func scopeCodexIdentityUUID(apiKeyID int64, namespace, raw string) string {
	seed := codexAccountIdentityScopeSeed(apiKeyID, namespace, strings.ToLower(raw))
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed.Version() != 7 {
		return deriveStableUUIDv4(seed)
	}
	h := sha256.Sum256([]byte(seed))
	var out uuid.UUID
	copy(out[:6], parsed[:6])
	out[6] = 0x70 | (h[6] & 0x0f)
	out[7] = h[7]
	out[8] = 0x80 | (h[8] & 0x3f)
	copy(out[9:], h[9:16])
	return out.String()
}

// codexAccountIdentityFields 是需要按账号作用域改写的客户端标识字段（头名 / client_metadata 键）。
var codexAccountIdentityFields = []string{
	"installation_id",
	"x-codex-installation-id",
	"session_id",
	"session-id",
	"thread_id",
	"thread-id",
	"turn_id",
	"turn-id",
	"window_id",
	"x-codex-window-id",
	"x-client-request-id",
}

func applyCodexAccountIdentityFields(values map[string]any, account *Account, apiKeyID int64) bool {
	if values == nil || codexAccountIdentityNamespace(account) == "" {
		return false
	}
	changed := false
	for _, name := range codexAccountIdentityFields {
		raw, ok := values[name].(string)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		next := scopeCodexAccountIdentityValue(account, apiKeyID, raw)
		if next != raw {
			values[name] = next
			changed = true
		}
	}
	return changed
}

func applyCodexAccountIdentityEmbeddedMetadata(values map[string]any, account *Account, apiKeyID int64) bool {
	raw, ok := values[openAIWSTurnMetadataHeader].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return false
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata == nil {
		return false
	}
	if !applyCodexAccountIdentityFields(metadata, account, apiKeyID) {
		return false
	}
	rebuilt, err := json.Marshal(metadata)
	if err != nil {
		return false
	}
	values[openAIWSTurnMetadataHeader] = string(rebuilt)
	return true
}

func applyCodexAccountIdentityClientMetadataMap(requestBody map[string]any, account *Account, apiKeyID int64) bool {
	if requestBody == nil || codexAccountIdentityNamespace(account) == "" {
		return false
	}
	changed := false
	clientMetadata, _ := requestBody["client_metadata"].(map[string]any)
	if clientMetadata != nil {
		if applyCodexAccountIdentityFields(clientMetadata, account, apiKeyID) {
			changed = true
		}
		if applyCodexAccountIdentityEmbeddedMetadata(clientMetadata, account, apiKeyID) {
			changed = true
		}
	}
	if raw, ok := requestBody["prompt_cache_key"].(string); ok && strings.TrimSpace(raw) != "" {
		next := scopeCodexAccountIdentityValue(account, apiKeyID, raw)
		if next != raw {
			requestBody["prompt_cache_key"] = next
			changed = true
		}
	}
	return changed
}

// applyCodexAccountIdentityClientMetadataRaw scopes only the small identity
// subobjects with gjson/sjson. The passthrough hot path never unmarshals the
// potentially multi-megabyte request body.
func applyCodexAccountIdentityClientMetadataRaw(body []byte, account *Account, apiKeyID int64) ([]byte, bool, error) {
	if len(body) == 0 || codexAccountIdentityNamespace(account) == "" {
		return body, false, nil
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return body, false, nil
	}

	next := body
	changed := false
	if cm := gjson.GetBytes(body, "client_metadata"); cm.IsObject() {
		clientMetadata := map[string]any{}
		if err := json.Unmarshal([]byte(cm.Raw), &clientMetadata); err != nil {
			return body, false, fmt.Errorf("decode client_metadata for account identity: %w", err)
		}
		metadataChanged := applyCodexAccountIdentityFields(clientMetadata, account, apiKeyID)
		if applyCodexAccountIdentityEmbeddedMetadata(clientMetadata, account, apiKeyID) {
			metadataChanged = true
		}
		if metadataChanged {
			raw, err := json.Marshal(clientMetadata)
			if err != nil {
				return body, false, fmt.Errorf("encode account-scoped client_metadata: %w", err)
			}
			var setErr error
			next, setErr = sjson.SetRawBytes(next, "client_metadata", raw)
			if setErr != nil {
				return body, false, fmt.Errorf("splice account-scoped client_metadata: %w", setErr)
			}
			changed = true
		}
	}
	if promptCacheKey := gjson.GetBytes(body, "prompt_cache_key"); promptCacheKey.Type == gjson.String && strings.TrimSpace(promptCacheKey.String()) != "" {
		raw := promptCacheKey.String()
		scoped := scopeCodexAccountIdentityValue(account, apiKeyID, raw)
		if scoped != raw {
			rewritten, err := sjson.SetBytes(next, "prompt_cache_key", scoped)
			if err != nil {
				return body, false, fmt.Errorf("splice account-scoped prompt_cache_key: %w", err)
			}
			next = rewritten
			changed = true
		}
	}
	return next, changed, nil
}

func applyCodexAccountIdentityHeaders(headers http.Header, account *Account, apiKeyID int64) {
	if headers == nil || codexAccountIdentityNamespace(account) == "" {
		return
	}
	for _, name := range codexAccountIdentityFields {
		switch name {
		case "session_id":
			// Underscore session/conversation headers are rebuilt separately by the
			// compact request builders; the Codex inference paths never send them.
			continue
		case "x-codex-installation-id":
			// A real Codex client only carries the installation id inside body
			// client_metadata; never emit it as a request header.
			headers.Del(name)
			continue
		}
		raw := strings.TrimSpace(headers.Get(name))
		if raw != "" {
			headers.Set(name, scopeCodexAccountIdentityValue(account, apiKeyID, raw))
		}
	}
	if raw := strings.TrimSpace(headers.Get(openAIWSTurnMetadataHeader)); raw != "" {
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &metadata); err == nil && metadata != nil && applyCodexAccountIdentityFields(metadata, account, apiKeyID) {
			if rebuilt, err := json.Marshal(metadata); err == nil {
				headers.Set(openAIWSTurnMetadataHeader, string(rebuilt))
			}
		}
	}
}

// ensureCodexSessionIdentityHeaders 补齐真实 Codex 客户端必带、但入站请求可能缺失的
// 连字符会话头：session-id 缺失时取最终（已按账号作用域改写的）会话标识，
// thread-id 缺失时等于 session-id（root 线程不变式），x-client-request-id 缺失时等于
// thread-id。sessionID 必须已经是出站形态，这里不再二次改写。
// fillCodexSessionIdentityHeaders 是非 compact Codex 旁路（Messages / Chat 桥接、alpha search
// fallback）的最终会话头形态：删除下划线 session_id / conversation_id（真实 Codex 不发送），
// 已存在的连字符 session-id / thread-id / x-client-request-id（构造器已复制并作用域改写）
// 原样保留，只在缺失时用给定的会话回退值补齐。
func fillCodexSessionIdentityHeaders(h http.Header, fallbackSessionID string) {
	if h == nil {
		return
	}
	h.Del("session_id")
	h.Del("conversation_id")
	ensureCodexSessionIdentityHeaders(h, fallbackSessionID)
}

func ensureCodexSessionIdentityHeaders(h http.Header, sessionID string) {
	if h == nil {
		return
	}
	if strings.TrimSpace(h.Get("session-id")) == "" {
		if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
			h.Set("session-id", sessionID)
		}
	}
	if strings.TrimSpace(h.Get("thread-id")) == "" {
		if current := strings.TrimSpace(h.Get("session-id")); current != "" {
			h.Set("thread-id", current)
		}
	}
	if strings.TrimSpace(h.Get("x-client-request-id")) == "" {
		if current := strings.TrimSpace(h.Get("thread-id")); current != "" {
			h.Set("x-client-request-id", current)
		}
	}
}

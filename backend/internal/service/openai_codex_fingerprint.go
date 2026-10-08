package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// codexFingerprintIDsContextKey 是暂存在 gin context 的收敛 ID 集合键。
// 由 Forward（非透传）或 forwardOpenAIPassthrough（透传）解析后写入，请求
// 构造器读取用于出站头改写——请求体与出站头必须共享同一份 IDs，保证头与体的设备、线程和窗口标识一致。
const codexFingerprintIDsContextKey = "codex_fingerprint_ids"

// stageCodexFingerprintIDs 将本 attempt 解析出的收敛 ID 暂存到 gin context。
// 必须无条件覆写（含 nil）：failover 从收敛账号切到 off 账号时，上一账号的
// IDs 不得残留并被误应用到新账号的出站头（typed-nil 由应用侧 nil 守卫吸收）。
func stageCodexFingerprintIDs(c *gin.Context, ids *codexFingerprintIDs) {
	if c != nil {
		c.Set(codexFingerprintIDsContextKey, ids)
	}
}

func stagedCodexFingerprintIDs(c *gin.Context, account *Account) *codexFingerprintIDs {
	if c == nil || account == nil || !account.UsesOpenAICodexProtocol() {
		return nil
	}
	value, ok := c.Get(codexFingerprintIDsContextKey)
	if !ok {
		return nil
	}
	ids, ok := value.(*codexFingerprintIDs)
	if !ok || ids == nil || ids.accountID != account.ID {
		return nil
	}
	return ids
}

// applyStagedCodexFingerprintHeaders 读取 context 暂存的收敛 ID 并改写出站头。
// 非透传与透传两个请求构造器共用本函数，防止应用语义漂移。仅解析该
// snapshot 的 OAuth 账号可读取，避免 stale context 跨账号 failover 泄漏。
func applyStagedCodexFingerprintHeaders(c *gin.Context, account *Account, h http.Header) {
	applyCodexFingerprintHeaders(h, stagedCodexFingerprintIDs(c, account))
}

func applyStagedCodexFingerprintClientMetadata(c *gin.Context, account *Account, reqBody map[string]any) bool {
	return applyCodexFingerprintClientMetadata(reqBody, stagedCodexFingerprintIDs(c, account))
}

// codexFingerprintMode 控制 OAuth 账号出站请求的设备指纹收敛强度。
// 多人共享同一 OAuth 账号时，每个用户的 Codex 客户端会携带各自不同的
// installation_id / session_id / thread_id，上游据此判定设备数和会话数。
// 收敛模式将这些标识改写为账号级恒定值，减少上游可见的设备/会话指纹。
type codexFingerprintMode string

const (
	// codexFingerprintOff 不做任何收敛，原样透传客户端标识。
	// 非 OpenAI OAuth 类账号恒为此值；OAuth 类账号须显式写入 off 才是此值
	// （见 GetCodexFingerprintMode）。
	codexFingerprintOff codexFingerprintMode = "off"
	// codexFingerprintDevice 仅收敛 installation_id 为账号级恒定值。
	// 上游看到 1 台设备 + 多会话（每用户各自的 session）。
	codexFingerprintDevice codexFingerprintMode = "device"
	// codexFingerprintSession 收敛 installation_id + session_id，
	// thread_id 按客户端原始 session-id 确定性派生（每个真实 Codex 会话一个独立线程）。
	// 上游看到 1 台设备 + 1 会话 + N 线程，最接近正常用户 spawn 子代理的模式。
	codexFingerprintSession codexFingerprintMode = "session"
	// codexFingerprintFull 收敛所有标识：installation_id + session_id + thread_id。
	// 上游看到 1 台设备 + 1 会话 + 1 线程，最激进。
	codexFingerprintFull codexFingerprintMode = "full"
)

const (
	codexFingerprintModeExtraKey = "codex_fingerprint_mode"
	codexFingerprintSeedExtraKey = "codex_fingerprint_seed"
)

func canonicalCodexFingerprintSeed(value any) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed == uuid.Nil || trimmed != parsed.String() {
		return "", false
	}
	return trimmed, true
}

func newCodexFingerprintSeed() string {
	return uuid.NewString()
}

func stripCodexFingerprintSeed(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	stripped := maps.Clone(extra)
	delete(stripped, codexFingerprintSeedExtraKey)
	return stripped
}

// codexFingerprintModeFromExtra 读取账号完整 extra 上的收敛模式。
// 未设置、空值或非法值一律按 device 处理：多人共享同一 OAuth 账号时，上游只应看到
// 一台设备；显式 off 仍然生效（原样透传客户端标识）。
func codexFingerprintModeFromExtra(extra map[string]any) codexFingerprintMode {
	if mode, ok := codexFingerprintModeExplicit(extra); ok {
		return mode
	}
	return codexFingerprintDevice
}

// codexFingerprintModeExplicit 只识别显式写入的合法模式；缺失、空值、非法值返回 false。
// 键级 JSONB 增量更新（UpdateExtra / BulkUpdate）必须用它判断"是否在启用收敛"，
// 不能把缺失键当成 device 默认值。
func codexFingerprintModeExplicit(extra map[string]any) (codexFingerprintMode, bool) {
	if extra == nil {
		return "", false
	}
	raw, _ := extra[codexFingerprintModeExtraKey].(string)
	mode := codexFingerprintMode(strings.TrimSpace(raw))
	switch mode {
	case codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return mode, true
	}
	return "", false
}

func codexFingerprintModeRequiresSeed(mode codexFingerprintMode) bool {
	switch mode {
	case codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return true
	default:
		return false
	}
}

func codexFingerprintSeed(extra map[string]any) (string, bool) {
	if extra == nil {
		return "", false
	}
	return canonicalCodexFingerprintSeed(extra[codexFingerprintSeedExtraKey])
}

// prepareCodexFingerprintExtraForCreate 为新建的 OpenAI OAuth-like 账号补齐系统种子和
// Codex 客户端身份：种子始终由系统铸造（用户提交的值被剥离），身份缺失时按种子派生。
func prepareCodexFingerprintExtraForCreate(platform, accountType string, extra map[string]any) map[string]any {
	prepared := stripCodexFingerprintSeed(extra)
	if platform != PlatformOpenAI || (accountType != AccountTypeOAuth && accountType != AccountTypeSetupToken) {
		return prepared
	}
	if prepared == nil {
		prepared = make(map[string]any, 2)
	}
	prepared[codexFingerprintSeedExtraKey] = newCodexFingerprintSeed()
	// 身份是系统管理字段：不接受请求带入的值，只按新种子派生。
	delete(prepared, CodexClientIdentityExtraKey)
	return ensureCodexClientIdentityExtra(platform, accountType, prepared, time.Now())
}

// prepareCodexFingerprintExtraForUpdate 在整体覆盖 extra 时保留系统种子与已持久化的
// Codex 客户端身份（两者都不由管理端表单提交），缺失时补齐。
func prepareCodexFingerprintExtraForUpdate(account *Account, extra map[string]any) map[string]any {
	prepared := stripCodexFingerprintSeed(extra)
	if account == nil || !account.IsOpenAIOAuthLike() {
		return prepared
	}
	if prepared == nil {
		prepared = make(map[string]any, 2)
	}
	// General account edits omit the extension-owned mode. Preserve the latest
	// stored value instead of resetting it or replaying a stale editor draft.
	if _, submitted := prepared[codexFingerprintModeExtraKey]; !submitted {
		if existing, exists := account.Extra[codexFingerprintModeExtraKey]; exists {
			prepared[codexFingerprintModeExtraKey] = existing
		}
	}
	if seed, ok := codexFingerprintSeed(account.Extra); ok {
		prepared[codexFingerprintSeedExtraKey] = seed
	} else {
		prepared[codexFingerprintSeedExtraKey] = newCodexFingerprintSeed()
	}
	// 身份是系统管理字段：忽略表单带回的值（可能过期或被篡改），保留库中已持久化的
	// 合法身份；没有则按种子派生。派生确定性，与启动回填并发也只会写出同一个值。
	delete(prepared, CodexClientIdentityExtraKey)
	if _, ok := codexClientIdentityFromExtra(account.Extra); ok {
		prepared[CodexClientIdentityExtraKey] = account.Extra[CodexClientIdentityExtraKey]
	}
	return ensureCodexClientIdentityExtra(account.Platform, account.Type, prepared, time.Now())
}

func sanitizedCodexFingerprintExtraUpdates(updates map[string]any) map[string]any {
	if updates == nil {
		return nil
	}
	sanitized := maps.Clone(updates)
	delete(sanitized, codexFingerprintSeedExtraKey)
	delete(sanitized, CodexClientIdentityExtraKey)
	return sanitized
}

// ShouldEnsureCodexFingerprintSeedForExtraUpdates reports whether a JSONB key-level
// extra update is enabling Codex fingerprint convergence and therefore must atomically
// preserve or create the system-managed per-account seed in the repository update.
// ShouldEnsureCodexFingerprintSeedForExtraUpdates reports whether a JSONB key-level
// extra update is enabling Codex fingerprint convergence and therefore must atomically
// preserve or create the system-managed per-account seed in the repository update.
// Only an explicit device / session / full value counts; a missing key is not "device".
func ShouldEnsureCodexFingerprintSeedForExtraUpdates(updates map[string]any) bool {
	mode, ok := codexFingerprintModeExplicit(updates)
	if !ok {
		return false
	}
	return codexFingerprintModeRequiresSeed(mode)
}

// GetCodexFingerprintMode 返回账号的指纹收敛模式。
//
// 非 OpenAI OAuth 类账号恒为 off。OAuth 类账号取 extra 中显式写入的合法模式，
// 显式 off 生效；未设置、空值或非法值按 device 处理（见
// codexFingerprintModeFromExtra）。
func (a *Account) GetCodexFingerprintMode() codexFingerprintMode {
	if a == nil || !a.IsOpenAIOAuthLike() {
		return codexFingerprintOff
	}
	return codexFingerprintModeFromExtra(a.Extra)
}

// deriveStableUUIDv4 从种子确定性派生一个 UUIDv4 格式的字符串。
// 同一种子永远返回同一值。
func deriveStableUUIDv4(seed string) string {
	h := sha256.Sum256([]byte(seed))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

// resolveConvergedInstallationID 返回账号级恒定的 installation_id。
// 优先使用管理员配置的真实 device_id，无则从系统管理的账号随机种子确定性派生。
func resolveConvergedInstallationID(account *Account, seed string) string {
	if account == nil {
		return ""
	}
	if deviceID := account.GetOpenAIDeviceID(); deviceID != "" {
		return deviceID
	}
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-install-id:v2:" + seed)
}

// resolveConvergedSessionID 返回账号级恒定的 session_id。
func resolveConvergedSessionID(seed string) string {
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-session-id:v2:" + seed)
}

// resolveConvergedThreadID 按客户端原始 session-id 确定性派生 thread_id。
// 每个真实 Codex 会话（不同客户端启动实例）获得一个独立线程，
// 模拟正常用户 spawn 子代理或开多窗口的模式。
func resolveConvergedThreadID(seed, clientSessionID string) string {
	if seed == "" || clientSessionID == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-thread-id:v2:" + seed + ":" + clientSessionID)
}

// codexFingerprintIDs 收敛后的完整 ID 集合。
// 由 resolveCodexFingerprintIDs 一次性生成，同一个实例在头改写和体改写之间共享，
// 确保所有载体中的 turn_id 等随机字段一致。体改写时还会补记原始
// client_metadata.session_id，用于识别 root prompt_cache_key 的默认值。
type codexFingerprintIDs struct {
	seed                          string
	accountID                     int64
	mode                          codexFingerprintMode
	installationID                string
	sessionID                     string
	threadID                      string
	windowID                      string
	sandbox                       string
	originalBodySessionID         string
	originalBodySessionIDCaptured bool
}

// resolveCodexFingerprintIDs 按收敛模式计算出站 ID 集合。
// clientSessionID 是客户端原始的 session-id 头值（连字符形式），用于 session 模式下
// 的 thread_id 派生——每个真实 Codex 会话得到一个独立线程。
// 返回 nil 表示 off 模式，不需要改写。
// 调用方共享同一份结果给头改写和体改写；真实回合 ID 与开始时间不生成、不改写。
func resolveCodexFingerprintIDs(account *Account, clientSessionID string, mode codexFingerprintMode) *codexFingerprintIDs {
	if account == nil || mode == codexFingerprintOff {
		return nil
	}
	seed, ok := codexFingerprintSeed(account.Extra)
	if !ok {
		return nil
	}

	ids := &codexFingerprintIDs{
		seed:      seed,
		accountID: account.ID,
		mode:      mode,
	}
	if identity, ok := account.CodexClientIdentity(); ok {
		ids.sandbox = identity.Sandbox
	}

	ids.installationID = resolveConvergedInstallationID(account, seed)
	if ids.installationID == "" {
		return nil
	}

	switch mode {
	case codexFingerprintDevice:
		return ids

	case codexFingerprintSession:
		ids.sessionID = resolveConvergedSessionID(seed)
		ids.threadID = resolveConvergedThreadID(seed, clientSessionID)
		if ids.threadID == "" {
			ids.threadID = ids.sessionID
		}
		ids.windowID = ids.threadID + ":0"
		return ids

	case codexFingerprintFull:
		ids.sessionID = resolveConvergedSessionID(seed)
		ids.threadID = ids.sessionID
		ids.windowID = ids.threadID + ":0"
		return ids
	}

	return nil
}

// extractClientSessionID 从请求头中提取客户端原始的会话标识。
// 优先取 session-id（连字符形式，Codex CLI 标准），回退到 session_id（下划线形式）。
// 返回的值尚未被 isolateOpenAISessionID 改写，是客户端的真实标识。
func extractClientSessionID(h http.Header) string {
	if v := strings.TrimSpace(h.Get("session-id")); v != "" {
		return v
	}
	return strings.TrimSpace(h.Get("session_id"))
}

// resolveCodexFingerprintIDsFromRequest 从客户端原始请求头中提取 session-id，
// 结合账号配置一次性解析收敛 ID 集合。调用方应将返回的 ids 同时传给
// applyCodexFingerprintHeaders 和 applyCodexFingerprintClientMetadata。
func resolveCodexFingerprintIDsFromRequest(account *Account, clientHeaders http.Header, policies ...*codexFingerprintPolicy) *codexFingerprintIDs {
	policy := selectCodexFingerprintPolicy(policies)
	if account == nil || !policy.enabled {
		return nil
	}
	mode := account.GetCodexFingerprintMode()
	if mode == codexFingerprintOff {
		return nil
	}
	clientSessionID := ""
	if clientHeaders != nil {
		clientSessionID = extractClientSessionID(clientHeaders)
	}
	ids := resolveCodexFingerprintIDs(account, clientSessionID, mode)
	if ids != nil {
		ids.captureThreadAndWindow(clientHeaders.Get("thread-id"), clientHeaders.Get("x-codex-window-id"))
	}
	return ids
}

// applyCodexFingerprintHeaders 按预计算的收敛 ID 改写出站 HTTP 头中的设备指纹。
// 在 buildUpstreamRequest 的白名单透传之后、enforceCodexIdentityHeaders 之前调用。
func applyCodexFingerprintHeaders(h http.Header, ids *codexFingerprintIDs) {
	if h == nil || ids == nil {
		return
	}

	// 真实 Codex 从不以请求头形式发送 installation id（只在 body client_metadata 中），
	// 收敛后的值写入 turn metadata / client_metadata，头上一律不带。
	h.Del("x-codex-installation-id")

	if ids.mode == codexFingerprintDevice {
		ids.rewriteHeaderTurnMetadata(h)
		return
	}

	// session / full 模式：改写所有相关头。真实 Codex 只发连字符形式；下划线形式
	// 只在调用方（legacy compact 桥接）已经写入时同步改写，不主动添加。
	h.Set("x-codex-window-id", ids.windowID)
	h.Set("x-client-request-id", ids.threadID)
	h.Set("session-id", ids.sessionID)
	h.Set("thread-id", ids.threadID)
	if h.Get("session_id") != "" {
		h.Set("session_id", ids.sessionID)
	}

	if parent := h.Get("x-codex-parent-thread-id"); parent != "" {
		if ids.mode == codexFingerprintFull {
			h.Del("x-codex-parent-thread-id")
		} else {
			h.Set("x-codex-parent-thread-id", ids.mapThread(parent))
		}
	}
	ids.rewriteHeaderTurnMetadata(h)
}

// alignSandboxWithUserAgent 让 sandbox 跟随最终出站 UA 声明的系统（管理员显式 UA
// 覆盖时可能与账号身份不同）。UA 无法解析时不改写 sandbox。
func (ids *codexFingerprintIDs) alignSandboxWithUserAgent(userAgent string) {
	if ids == nil {
		return
	}
	ids.sandbox = codexSandboxForUserAgent(userAgent)
}

// applyCodexFingerprintClientMetadata 按预计算的收敛 ID 改写请求体中的 client_metadata。
// 使用与头改写相同的 ids 实例，确保 turn_id 等随机字段一致。
func applyCodexFingerprintClientMetadata(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	if reqBody == nil || ids == nil {
		return false
	}

	captureCodexFingerprintOriginalBodySessionID(ids, reqBody["client_metadata"])
	existing, _ := reqBody["client_metadata"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
	}

	modified := false
	if applyCodexFingerprintToClientMetadataMap(existing, ids) {
		reqBody["client_metadata"] = existing
		modified = true
	}
	if applyCodexFingerprintPromptCacheKey(reqBody, ids) {
		modified = true
	}
	return modified
}

// applyCodexFingerprintToClientMetadataMap 是 client_metadata 改写的共享核心，
// map 版（非透传，body 已解码）与 raw 字节版（透传热路径）都经由它，保证两条
// 路径的收敛语义永不漂移。
func applyCodexFingerprintToClientMetadataMap(existing map[string]any, ids *codexFingerprintIDs) bool {
	if existing == nil || ids == nil {
		return false
	}

	modified := false

	if ids.installationID != "" {
		existing["x-codex-installation-id"] = ids.installationID
		if _, exists := existing["installation_id"]; exists {
			existing["installation_id"] = ids.installationID
		}
		modified = true
	}

	if ids.mode == codexFingerprintDevice {
		ids.rewriteEmbeddedTurnMetadata(existing)
		return modified
	}

	// session / full 模式
	ids.captureThreadAndWindow(stringMetadataValue(existing, "thread_id"), stringMetadataValue(existing, "x-codex-window-id"))
	existing["session_id"] = ids.sessionID
	existing["thread_id"] = ids.threadID
	existing["x-codex-window-id"] = ids.windowID
	ids.rewriteThreadReferences(existing)
	ids.rewriteEmbeddedTurnMetadata(existing)
	return true
}

func stringMetadataValue(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}

func (ids *codexFingerprintIDs) mapThread(original string) string {
	if ids.mode == codexFingerprintFull {
		return ids.threadID
	}
	return resolveConvergedThreadID(ids.seed, original)
}

func (ids *codexFingerprintIDs) captureThreadAndWindow(thread, window string) {
	if ids.mode == codexFingerprintDevice {
		return
	}
	if ids.mode == codexFingerprintSession && strings.TrimSpace(thread) != "" {
		ids.threadID = ids.mapThread(thread)
	}
	suffix := "0"
	if index := strings.LastIndexByte(window, ':'); index >= 0 && index < len(window)-1 {
		suffix = window[index+1:]
	} else if index := strings.LastIndexByte(ids.windowID, ':'); index >= 0 {
		suffix = ids.windowID[index+1:]
	}
	ids.windowID = ids.threadID + ":" + suffix
}

func (ids *codexFingerprintIDs) rewriteThreadReferences(metadata map[string]any) {
	if ids.mode == codexFingerprintDevice {
		return
	}
	for _, key := range []string{"parent_thread_id", "x-codex-parent-thread-id", "forked_from_thread_id"} {
		if original := stringMetadataValue(metadata, key); original != "" {
			if ids.mode == codexFingerprintFull {
				delete(metadata, key)
				if key == "forked_from_thread_id" {
					delete(metadata, "forked_from_ordinal_exclusive")
				}
			} else {
				metadata[key] = ids.mapThread(original)
			}
		}
	}
}

func (ids *codexFingerprintIDs) rewriteTurnMetadata(metadata map[string]any) {
	metadata["installation_id"] = ids.installationID
	if ids.mode != codexFingerprintDevice {
		metadata["session_id"] = ids.sessionID
		metadata["thread_id"] = ids.threadID
		metadata["window_id"] = ids.windowID
		ids.rewriteThreadReferences(metadata)
	}
	alignCodexSandboxMetadata(metadata, ids.sandbox)
}

func alignCodexSandboxMetadata(metadata map[string]any, target string) bool {
	// These describe actual execution permissions, not an OS-only fingerprint.
	mode := stringMetadataValue(metadata, "sandbox_mode")
	sandbox := stringMetadataValue(metadata, "sandbox")
	if mode == "external-sandbox" {
		metadata["sandbox"] = "external"
		return sandbox != "external"
	}
	if sandbox == "none" || sandbox == "external" {
		return false
	}
	if mode == "danger-full-access" && sandbox == "" {
		return false
	}
	switch sandbox {
	case "seatbelt", "seccomp", "windows_sandbox", "windows_elevated", "windows_mxc":
		if target != "" && target != sandbox && !(strings.HasPrefix(sandbox, "windows_") && strings.HasPrefix(target, "windows_")) {
			metadata["sandbox"] = target
			return true
		}
	}
	return false
}

func alignCodexSandboxJSON(raw, target string) (string, bool) {
	var metadata map[string]any
	if json.Unmarshal([]byte(raw), &metadata) != nil || metadata == nil || !alignCodexSandboxMetadata(metadata, target) {
		return raw, false
	}
	encoded, err := marshalCodexTurnMetadata(metadata)
	if err != nil {
		return raw, false
	}
	return string(encoded), true
}

func alignCodexSandboxClientMetadata(body map[string]any, target string) bool {
	metadata, _ := body["client_metadata"].(map[string]any)
	if metadata == nil {
		return false
	}
	if raw := stringMetadataValue(metadata, "x-codex-turn-metadata"); raw != "" {
		if aligned, changed := alignCodexSandboxJSON(raw, target); changed {
			metadata["x-codex-turn-metadata"] = aligned
			return true
		}
	}
	return false
}

func alignCodexSandboxClientMetadataRaw(body []byte, target string) ([]byte, error) {
	raw := gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata")
	if raw.Type != gjson.String {
		return body, nil
	}
	if aligned, changed := alignCodexSandboxJSON(raw.String(), target); changed {
		return sjson.SetBytes(body, "client_metadata.x-codex-turn-metadata", aligned)
	}
	return body, nil
}

func resolveCodexFingerprintIDsWithSource(routed, source *Account, headers http.Header, policy *codexFingerprintPolicy) *codexFingerprintIDs {
	if routed == nil || source == nil {
		return nil
	}
	account := *source
	account.Extra = maps.Clone(source.Extra)
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[codexFingerprintModeExtraKey] = string(routed.GetCodexFingerprintMode())
	ids := resolveCodexFingerprintIDsFromRequest(&account, headers, policy)
	if ids != nil {
		ids.accountID = routed.ID
	}
	return ids
}

func (ids *codexFingerprintIDs) rewriteHeaderTurnMetadata(headers http.Header) {
	raw := headers.Get("x-codex-turn-metadata")
	if raw == "" {
		return
	}
	var metadata map[string]any
	if json.Unmarshal([]byte(raw), &metadata) != nil || metadata == nil {
		metadata = map[string]any{}
	}
	ids.rewriteTurnMetadata(metadata)
	if encoded, err := marshalCodexTurnMetadata(metadata); err == nil {
		headers.Set("x-codex-turn-metadata", string(encoded))
	}
}

func (ids *codexFingerprintIDs) rewriteEmbeddedTurnMetadata(clientMetadata map[string]any) {
	raw := stringMetadataValue(clientMetadata, "x-codex-turn-metadata")
	if raw == "" {
		return
	}
	var metadata map[string]any
	if json.Unmarshal([]byte(raw), &metadata) != nil || metadata == nil {
		metadata = map[string]any{}
	}
	ids.rewriteTurnMetadata(metadata)
	if encoded, err := marshalCodexTurnMetadata(metadata); err == nil {
		clientMetadata["x-codex-turn-metadata"] = string(encoded)
	}
}

func captureCodexFingerprintOriginalBodySessionID(ids *codexFingerprintIDs, clientMetadata any) {
	if ids == nil || ids.originalBodySessionIDCaptured {
		return
	}
	ids.originalBodySessionIDCaptured = true
	if clientMetadata == nil {
		return
	}
	switch metadata := clientMetadata.(type) {
	case map[string]any:
		if sessionID, ok := metadata["session_id"].(string); ok {
			ids.originalBodySessionID = strings.TrimSpace(sessionID)
		}
	case map[string]string:
		ids.originalBodySessionID = strings.TrimSpace(metadata["session_id"])
	}
}

func captureCodexFingerprintOriginalBodySessionIDRaw(ids *codexFingerprintIDs, value gjson.Result) {
	if ids == nil || ids.originalBodySessionIDCaptured {
		return
	}
	ids.originalBodySessionIDCaptured = true
	if value.Exists() && value.Type == gjson.String {
		ids.originalBodySessionID = strings.TrimSpace(value.String())
	}
}

func shouldRewriteCodexFingerprintPromptCacheKey(ids *codexFingerprintIDs, promptCacheKey string) bool {
	if ids == nil || !ids.originalBodySessionIDCaptured || ids.originalBodySessionID == "" || ids.sessionID == "" {
		return false
	}
	if ids.mode != codexFingerprintSession && ids.mode != codexFingerprintFull {
		return false
	}
	return promptCacheKey == ids.originalBodySessionID
}

func applyCodexFingerprintPromptCacheKey(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	if reqBody == nil {
		return false
	}
	promptCacheKey, ok := reqBody["prompt_cache_key"].(string)
	if !ok || strings.TrimSpace(promptCacheKey) == "" || !shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey) {
		return false
	}
	if promptCacheKey == ids.sessionID {
		return false
	}
	reqBody["prompt_cache_key"] = ids.sessionID
	return true
}

// applyCodexFingerprintClientMetadataRaw 在原始 JSON 字节上改写 client_metadata，
// 供透传路径使用——透传是热路径，禁止对可能高达数十 MB 的 body 做全量
// Unmarshal（见 forwardOpenAIPassthrough 的轻量提取注释）。实现为：gjson 提取
// client_metadata 小对象单独解码，经共享核心改写后 sjson 一次性拼回，body
// 其余字节原样保留；root prompt_cache_key 仅在可证明是 body session 默认值时
// 做标量改写。语义与 applyCodexFingerprintClientMetadata 逐点一致（含
// "非对象值整体替换为收敛集合"的行为）。
func applyCodexFingerprintClientMetadataRaw(body []byte, ids *codexFingerprintIDs) ([]byte, bool, error) {
	if len(body) == 0 || ids == nil {
		return body, false, nil
	}
	// 非 JSON 对象的 body（数组/标量/畸形）没有 client_metadata 语义，
	// sjson 在这类根上写字段会改写整体结构，直接放行保持原样。
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.Result{})
		return body, false, nil
	}

	existing := map[string]any{}
	if cm := gjson.GetBytes(body, "client_metadata"); cm.IsObject() {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.GetBytes(body, "client_metadata.session_id"))
		if err := json.Unmarshal([]byte(cm.Raw), &existing); err != nil {
			return body, false, fmt.Errorf("decode client_metadata for fingerprint: %w", err)
		}
	} else {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.Result{})
	}

	next := body
	modified := false
	if applyCodexFingerprintToClientMetadataMap(existing, ids) {
		raw, err := json.Marshal(existing)
		if err != nil {
			return body, false, fmt.Errorf("encode converged client_metadata: %w", err)
		}
		var setErr error
		next, setErr = sjson.SetRawBytes(body, "client_metadata", raw)
		if setErr != nil {
			return body, false, fmt.Errorf("splice converged client_metadata: %w", setErr)
		}
		modified = true
	}
	promptCacheKey := gjson.GetBytes(body, "prompt_cache_key")
	if promptCacheKey.Exists() && promptCacheKey.Type == gjson.String && strings.TrimSpace(promptCacheKey.String()) != "" && shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey.String()) {
		rewritten, err := sjson.SetBytes(next, "prompt_cache_key", ids.sessionID)
		if err != nil {
			return body, false, fmt.Errorf("splice converged prompt_cache_key: %w", err)
		}
		next = rewritten
		modified = true
	}
	return next, modified, nil
}

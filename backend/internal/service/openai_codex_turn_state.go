package service

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// openAICodexTurnStateHeader 是 Codex 的回合状态头。上游在响应头中铸造该
// 不透明 blob，客户端在同一回合的后续请求中原样回带（codex-rs 侧从
// /responses SSE、/responses/compact JSON 与 WS 握手三种响应中捕获，见
// codex-api/src/sse/responses.rs 与 endpoint/compact.rs）。
const openAICodexTurnStateHeader = "x-codex-turn-state"

// turn-state blob 是上游在"出站身份"（含 #5553 指纹收敛改写后的
// installation/session/thread 标识）下铸造的，同账号回放自洽；跨账号回放
// （failover 换号后客户端仍回带旧账号的 blob）是代理链独有、真实 Codex
// 永远不会产生的矛盾信号。溯源表记录每个下游会话最近一次铸造该 blob 的
// 账号，出站守卫据此剥离已知异账号的回带值。
type openAICodexTurnStateOrigin struct {
	accountID int64
	identity  string
	state     string
	expiresAt time.Time
}

// API-key destinations retain their existing session/owner contract. Codex
// OAuth state additionally belongs to one logical turn.
func openAICodexTurnStateUsesSessionContract(account *Account) bool {
	return account != nil && (account.Type == AccountTypeAPIKey || account.Platform == PlatformGrok)
}

// Optional account preserves the strict Codex scope for callers without a
// destination. Production reads, commits and clears always pass the account.
func openAICodexTurnStateSeed(c *gin.Context, accounts ...*Account) string {
	if c == nil || c.Request == nil {
		return ""
	}
	sessionID := extractClientSessionID(c.Request.Header)
	if sessionID == "" {
		return ""
	}
	seed := strconv.FormatInt(getAPIKeyIDFromContext(c), 10) + "\x00" + sessionID
	if len(accounts) > 0 && openAICodexTurnStateUsesSessionContract(accounts[0]) {
		return seed
	}
	turnID := codexRoutingTurnID(c)
	if turnID == "" {
		return ""
	}
	return seed + "\x00" + turnID
}

func openAIWSTurnStateScope(c *gin.Context, account *Account, sessionHash string) string {
	if sessionHash == "" || openAICodexTurnStateUsesSessionContract(account) {
		return sessionHash
	}
	if turn := codexRoutingTurnID(c); turn != "" {
		scope := sessionHash + "\x00" + turn
		if account != nil && account.IsOpenAIOAuthLike() {
			scope += "\x00" + CodexTicketAccountIdentity(account)
		}
		return scope
	}
	return ""
}

// relayOpenAICodexTurnState 将上游响应中的 turn-state 显式写入下游响应头，
// 并记录铸造账号。必须在响应头提交点调用（WriteHeader 之前、且确认本次
// 上游响应就是将要写回客户端的响应之后）。上游无该头时主动清除 writer 上
// 可能残留的上一 failover attempt 的值——否则换号后旧账号的 blob 会粘到
// 新账号的响应上，这正是本文件要防止的跨账号矛盾。
func (s *OpenAIGatewayService) relayOpenAICodexTurnState(c *gin.Context, account *Account, upstream http.Header) {
	if c == nil || c.Writer == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	state := s.firstCommittedCodexTurnState(c, account, extractOpenAICodexTurnState(upstream))
	if state == "" {
		c.Writer.Header().Del(canonical)
		s.clearOpenAICodexTurnStateProvenance(c, account)
		return
	}
	c.Writer.Header().Set(canonical, state)
	s.noteOpenAICodexTurnStateProvenance(c, account, state)
}

// stageOpenAICodexTurnState 将上游 turn-state 暂存到延迟提交的响应头集合
// （首输出守卫路径先缓存头、见到首个输出事件才提交）。此处**不**记录铸造
// 账号：该 attempt 仍可能在首输出超时后 failover，暂存头会被整体丢弃，
// 客户端从未收到该 blob。溯源必须在真正提交时记录，见
// noteStagedOpenAICodexTurnStateCommitted。
func stageOpenAICodexTurnState(dst *http.Header, upstream http.Header) {
	if dst == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		if *dst != nil {
			dst.Del(canonical)
		}
		return
	}
	if *dst == nil {
		*dst = http.Header{}
	}
	dst.Set(canonical, state)
}

// noteStagedOpenAICodexTurnStateCommitted 在暂存响应头真正写入下游时记录
// 铸造账号——只有此刻客户端才确定收到了该 blob，溯源表才与客户端持有的
// 值一致（否则被 failover 丢弃的 attempt 会污染溯源，导致后续误剥离）。
// OAuth 同主体同 turn 的首个已提交值保持不变；API Key 保留缺失即清除的契约。
func (s *OpenAIGatewayService) noteStagedOpenAICodexTurnStateCommitted(c *gin.Context, account *Account, staged http.Header) {
	state := s.firstCommittedCodexTurnState(c, account, extractOpenAICodexTurnState(staged))
	if state == "" {
		s.clearOpenAICodexTurnStateProvenance(c, account)
		return
	}
	s.noteOpenAICodexTurnStateProvenance(c, account, state)
}

func extractOpenAICodexTurnState(upstream http.Header) string {
	if upstream == nil {
		return ""
	}
	return strings.TrimSpace(upstream.Get(openAICodexTurnStateHeader))
}

// noteOpenAICodexTurnStateProvenance 记录（下游会话 → 铸造账号）。
func (s *OpenAIGatewayService) noteOpenAICodexTurnStateProvenance(c *gin.Context, account *Account, states ...string) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	seed := openAICodexTurnStateSeed(c, account)
	if seed == "" {
		return
	}
	state := ""
	if len(states) > 0 {
		state = strings.TrimSpace(states[0])
	}
	strict := account.IsOpenAIOAuthLike()
	if strict && state == "" {
		return
	}
	origin := openAICodexTurnStateOrigin{
		accountID: account.ID,
		identity:  CodexTicketAccountIdentity(account),
		state:     state,
		expiresAt: time.Now().Add(s.openAIWSSessionStickyTTL()),
	}
	if strict {
		// The first committed value wins atomically for this owner and turn.
		for {
			old, loaded := s.openaiCodexTurnStateOrigins.LoadOrStore(seed, origin)
			if !loaded {
				break
			}
			if previous, ok := old.(openAICodexTurnStateOrigin); ok && previous.accountID == origin.accountID &&
				previous.identity == origin.identity && previous.state != "" && time.Now().Before(previous.expiresAt) {
				return
			}
			if s.openaiCodexTurnStateOrigins.CompareAndSwap(seed, old, origin) {
				break
			}
		}
	} else {
		s.openaiCodexTurnStateOrigins.Store(seed, origin)
	}
	s.sweepOpenAICodexTurnStateOrigins()
}

func (s *OpenAIGatewayService) firstCommittedCodexTurnState(c *gin.Context, account *Account, candidate string) string {
	if s == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return candidate
	}
	if raw, ok := s.openaiCodexTurnStateOrigins.Load(openAICodexTurnStateSeed(c, account)); ok {
		if origin, ok := raw.(openAICodexTurnStateOrigin); ok && origin.accountID == account.ID &&
			origin.identity == CodexTicketAccountIdentity(account) && origin.state != "" && time.Now().Before(origin.expiresAt) {
			return origin.state
		}
	}
	return candidate
}

// Keep the client-facing header consistent with the first committed value,
// without rewriting the original headers used for upstream observations.
func (s *OpenAIGatewayService) codexTurnStateResponseHeaders(c *gin.Context, account *Account, upstream http.Header) http.Header {
	state := extractOpenAICodexTurnState(upstream)
	first := s.firstCommittedCodexTurnState(c, account, state)
	if first == state {
		return upstream
	}
	prepared := upstream.Clone()
	if prepared == nil {
		prepared = make(http.Header)
	}
	prepared.Set(openAICodexTurnStateHeader, first)
	return prepared
}

func (s *OpenAIGatewayService) clearOpenAICodexTurnStateProvenance(c *gin.Context, accounts ...*Account) {
	if s == nil {
		return
	}
	if seed := openAICodexTurnStateSeed(c, accounts...); seed != "" {
		s.openaiCodexTurnStateOrigins.Delete(seed)
	}
}

// commitOpenAIWSSessionTurnState updates the server-side WS fallback state at
// the same commit boundary as the downstream response. Codex retains its first
// value within the logical turn. API destinations retain the existing contract
// where an empty successful response clears the prior session owner. A failed
// attempt must never call this helper.
func (s *OpenAIGatewayService) commitOpenAIWSSessionTurnState(
	c *gin.Context,
	account *Account,
	stateStore OpenAIWSStateStore,
	groupID int64,
	sessionHash string,
	turnState string,
) {
	turnState = s.firstCommittedCodexTurnState(c, account, strings.TrimSpace(turnState))
	stateScope := openAIWSTurnStateScope(c, account, sessionHash)
	if stateStore != nil && stateScope != "" {
		if !openAICodexTurnStateUsesSessionContract(account) && account != nil {
			if first, ok := stateStore.GetSessionTurnState(groupID, stateScope, account.ID); ok {
				turnState = first
			}
		}
		if turnState == "" {
			stateStore.DeleteSessionTurnState(groupID, stateScope)
		} else if account != nil && account.ID > 0 {
			stateStore.BindSessionTurnState(groupID, stateScope, account.ID, turnState, s.openAIWSSessionStickyTTL())
		}
	}
	if turnState == "" {
		s.clearOpenAICodexTurnStateProvenance(c, account)
		return
	}
	s.noteOpenAICodexTurnStateProvenance(c, account, turnState)
}

// guardOpenAICodexTurnStateEcho 出站守卫：客户端回带的 turn-state 只有在
// 能证明由当前账号铸造时才保留。来源未知、过期、缺少会话标识或来自其他
// 账号的值均剥离。此守卫只处理 STATE 回显；Cookie 路由资格在独立的宿主
// 传输路径处理，不以 STATE 长度或此来源表授予模型资格。
func (s *OpenAIGatewayService) guardOpenAICodexTurnStateEcho(c *gin.Context, account *Account, h http.Header) {
	if h == nil {
		return
	}
	if strings.TrimSpace(h.Get(openAICodexTurnStateHeader)) == "" {
		return
	}
	if s == nil || account == nil || account.ID <= 0 {
		h.Del(openAICodexTurnStateHeader)
		return
	}
	seed := openAICodexTurnStateSeed(c, account)
	if seed == "" {
		h.Del(openAICodexTurnStateHeader)
		return
	}
	raw, ok := s.openaiCodexTurnStateOrigins.Load(seed)
	if !ok {
		h.Del(openAICodexTurnStateHeader)
		return
	}
	origin, ok := raw.(openAICodexTurnStateOrigin)
	if !ok {
		s.openaiCodexTurnStateOrigins.Delete(seed)
		h.Del(openAICodexTurnStateHeader)
		return
	}
	if !origin.expiresAt.IsZero() && time.Now().After(origin.expiresAt) {
		s.openaiCodexTurnStateOrigins.Delete(seed)
		h.Del(openAICodexTurnStateHeader)
		return
	}
	if origin.accountID != account.ID {
		h.Del(openAICodexTurnStateHeader)
		return
	}
	if account.IsOpenAIOAuthLike() && (origin.identity != CodexTicketAccountIdentity(account) ||
		origin.state == "" || strings.TrimSpace(h.Get(openAICodexTurnStateHeader)) != origin.state) {
		h.Del(openAICodexTurnStateHeader)
	}
}

// sweepOpenAICodexTurnStateOrigins 机会式清扫过期溯源记录：每 256 次写入
// 全量遍历一轮，防止仅靠读侧惰性删除导致的慢泄漏（会话键无上界）。
func (s *OpenAIGatewayService) sweepOpenAICodexTurnStateOrigins() {
	if s.openaiCodexTurnStateWrites.Add(1)%256 != 0 {
		return
	}
	now := time.Now()
	s.openaiCodexTurnStateOrigins.Range(func(key, value any) bool {
		origin, ok := value.(openAICodexTurnStateOrigin)
		if !ok || (!origin.expiresAt.IsZero() && now.After(origin.expiresAt)) {
			s.openaiCodexTurnStateOrigins.Delete(key)
		}
		return true
	})
}

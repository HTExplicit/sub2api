package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

const CodexTicketRuntimeExtraPrefix = "codex_ticket_runtime:"

// This metadata contains no ticket material. The account extra remains the
// sole ticket store; repository operations update both under the account lock.
type CodexTicketLifecycle struct {
	AccountID         int64              `json:"account_id"`
	Model             string             `json:"model"`
	Phase             string             `json:"phase"`
	Identity          string             `json:"-"`
	NextAt            *time.Time         `json:"next_attempt_at,omitempty"`
	ExpiresAt         *time.Time         `json:"expires_at,omitempty"`
	LeaseID           string             `json:"-"`
	LeaseUntil        *time.Time         `json:"-"`
	Operation         string             `json:"-"`
	JobID             int64              `json:"-"`
	ManualWasEnrolled bool               `json:"-"`
	LastAttemptAt     *time.Time         `json:"last_attempt_at,omitempty"`
	LastResult        *CodexTicketResult `json:"last_result,omitempty"`
}

type CodexTicketResult struct {
	Stage          string     `json:"stage,omitempty"`
	Code           string     `json:"code"`
	Success        bool       `json:"success"`
	HTTPStatus     int        `json:"http_status,omitempty"`
	ObservedLength int        `json:"observed_length,omitempty"`
	Message        string     `json:"message,omitempty"`
	DurationMS     int64      `json:"duration_ms,omitempty"`
	Fingerprint    string     `json:"fingerprint,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

type CodexTicketRecord struct {
	State      string    `json:"state"`
	Model      string    `json:"model"`
	AccountID  int64     `json:"account_id"`
	Identity   string    `json:"identity,omitempty"`
	Length     int       `json:"length"`
	CapturedAt time.Time `json:"captured_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Attempts   int       `json:"attempts"`
}

var ErrCodexTicketBusy = errors.New("ticket operation already running")
var ErrCodexTicketAlreadyAttempted = errors.New("ticket operation already attempted")
var ErrCodexTicketInactive = errors.New("ticket account is unavailable")
var ErrCodexTicketNotDue = errors.New("ticket renewal is not due")
var ErrCodexTicketValid = errors.New("ticket already valid")
var ErrCodexTicketDisabled = errors.New("ticket switch is disabled")

// Token refresh changes access/refresh tokens, not the credential owner. A
// reauthorization to another principal must not reuse that owner's ticket.
func CodexTicketAccountIdentity(a *Account) string {
	if a == nil {
		return ""
	}
	parts := []string{a.Platform, a.Type, a.GetCredential("chatgpt_account_id"), a.GetCredential("chatgpt_user_id"), a.GetCredential("organization_id")}
	if parts[2] == "" && parts[3] == "" {
		parts = append(parts, a.GetCredential("email"))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func CodexTicketAccountEligible(a *Account) bool {
	// Ticket ownership is independent of business scheduling. Administrators may
	// prepare and renew tickets while an OAuth-like account is disabled/in error.
	return isOpenAICodexTicketAccount(a)
}

func CodexTicketFailure(code string) CodexTicketResult {
	messages := map[string]string{
		"routing_candidate":          "已取得候选 Cookie，尚未通过业务出口验证",
		"routing_model_mismatch":     "业务出口返回模型不匹配，未发布资格",
		"routing_incomplete":         "响应未完整结束或缺少模型字段",
		"routing_cookie_missing":     "未取得可用路由 Cookie",
		"routing_cookie_expired":     "路由 Cookie 已到期",
		"routing_cookie_deleted":     "上游已撤销路由 Cookie",
		"routing_transport":          "采集或业务出口连接失败",
		"routing_stale":              "账号、设备、出口或 Cookie 代次已变化",
		"routing_connection_expired": "已验证连接不可复用，需要重新验证",
		"routing_upstream":           "上游拒绝路由验证请求",
		"ticket_plugin_unavailable":  "票据插件未启用或暂不可用",
		"ticket_disabled":            "票据总开关未开启",
		"ticket_proxy_missing":       "未配置打票代理",
		"ticket_ineligible":          "仅 OpenAI OAuth/Setup Token 非影子账号可打票",
		"ticket_model_invalid":       "模型不在票据配置范围内",
		"ticket_busy":                "该账号和模型已有打票任务",
		"ticket_interrupted":         "上次请求已开始，结果未确认；不会自动重复发送",
		"ticket_token":               "无法取得账号访问令牌",
		"ticket_timeout":             "请求超时",
		"ticket_transport":           "打票代理传输失败",
		"ticket_upstream":            "上游拒绝打票请求",
		"ticket_length":              "响应票据长度不符合292规则",
		"ticket_prefix":              "响应票据格式不符合规则",
		"ticket_missing_header":      "响应未包含票据头",
		"ticket_persist":             "票据持久化失败，未加入自动续期",
		"ticket_stale":               "账号身份、任务或续期状态已经变化",
		"ticket_canceled":            "任务已取消",
		"ticket_stopped":             "自动续期已停止",
		"ticket_proxy_auth":          "代理鉴权失败",
		"ticket_proxy_protocol":      "代理协议握手失败，请核对HTTP或SOCKS协议",
		"ticket_proxy_dns":           "代理主机DNS解析失败",
		"ticket_proxy_connect":       "代理连接被拒绝或CONNECT失败",
		"ticket_proxy_eof":           "连接被对端提前关闭（EOF）",
		"ticket_proxy_reset":         "连接被对端重置",
		"ticket_proxy_tls":           "代理TLS证书验证失败，请测试代理连接",
	}
	message, ok := messages[code]
	if !ok {
		code = "ticket_transport"
		message = messages[code]
	}
	stage := "request"
	switch code {
	case "ticket_disabled", "ticket_proxy_missing", "ticket_ineligible", "ticket_model_invalid":
		stage = "configuration"
	case "ticket_busy", "ticket_interrupted", "ticket_stale", "ticket_stopped", "ticket_canceled":
		stage = "lifecycle"
	case "ticket_token":
		stage = "account_auth"
	case "ticket_proxy_auth":
		stage = "proxy_auth"
	case "ticket_proxy_protocol":
		stage = "proxy_protocol"
	case "ticket_proxy_dns":
		stage = "proxy_dns"
	case "ticket_proxy_connect":
		stage = "proxy_connect"
	case "ticket_proxy_tls":
		stage = "tls"
	case "ticket_length", "ticket_prefix", "ticket_missing_header":
		stage = "ticket_validation"
	case "ticket_upstream":
		stage = "upstream_http"
	case "ticket_persist":
		stage = "persistence"
	}
	return CodexTicketResult{Code: code, Message: message, Stage: stage}
}

func codexTicketWireSummary(headers http.Header) map[string]any {
	state := strings.TrimSpace(headers.Get(openAICodexTurnStateHeader))
	result := map[string]any{"ticket_present": state != "", "ticket_length": len(state)}
	if state != "" {
		sum := sha256.Sum256([]byte(state))
		result["ticket_fingerprint"] = hex.EncodeToString(sum[:8])
	}
	return result
}

package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// openAIRawStreamTruncatedUpstreamMessage 是 raw CC 直转路径上游截断的 ops 消息。
const openAIRawStreamTruncatedUpstreamMessage = "Upstream Chat Completions stream ended before any terminal chunk"

// openAIRawStreamTerminalState 记录 raw Chat Completions SSE 流是否收到过
// **终止信号**。
//
// 背景：CC 直转路径把上游 SSE 原样透传，此前只要 HTTP 状态是 200 就按成功收尾——
// 上游中途断流（Cloudflare edge reset、后端 worker 掉线）会被伪装成
// `HTTP 200 + usage 0/0`：客户端拿到半截回答，网关既不报错也不计入 SLA，
// Ops 侧完全不可见。
//
// Only each observed choice's finish_reason establishes semantic termination.
// Usage and [DONE] are accounting/transport signals, not substitutes for it.
type openAIRawStreamTerminalState struct {
	// sawDataLine 表示上游至少发过一行 `data:`，即响应确实是 SSE 语义流。
	sawDataLine     bool
	sawDone         bool
	sawUsage        bool
	sawFinishReason bool
	choices         map[int64]bool
	tools           map[int64]map[int64]*openAIRawFunctionArguments
	toolChoices     map[int64]bool
	err             error
}

type openAIRawFunctionArguments struct {
	hasID, hasName bool
	kind           string
	arguments      strings.Builder
}

// ObserveDataLine 从单行 SSE `data:` 载荷中提取终止信号。payload 需已 TrimSpace。
func (t *openAIRawStreamTerminalState) ObserveDataLine(payload string) {
	if t == nil {
		return
	}
	t.sawDataLine = true
	if payload == "[DONE]" {
		t.sawDone = true
		return
	}
	if usage := gjson.Get(payload, "usage"); usage.Exists() && usage.IsObject() {
		t.sawUsage = true
	}
	for _, choice := range gjson.Get(payload, "choices").Array() {
		if t.choices == nil {
			t.choices = make(map[int64]bool)
		}
		index := choice.Get("index").Int()
		if _, exists := t.choices[index]; !exists {
			t.choices[index] = false
		}
		for _, call := range choice.Get("delta.tool_calls").Array() {
			if t.toolChoices == nil {
				t.toolChoices = make(map[int64]bool)
			}
			t.toolChoices[index] = true
			if t.tools == nil {
				t.tools = make(map[int64]map[int64]*openAIRawFunctionArguments)
			}
			if t.tools[index] == nil {
				t.tools[index] = make(map[int64]*openAIRawFunctionArguments)
			}
			toolIndex := call.Get("index").Int()
			tool := t.tools[index][toolIndex]
			if tool == nil {
				tool = &openAIRawFunctionArguments{kind: "function"}
				t.tools[index][toolIndex] = tool
			}
			if kind := call.Get("type").String(); kind != "" {
				tool.kind = kind
			}
			if tool.kind != "function" {
				continue // Non-function tools may intentionally have non-JSON input.
			}
			tool.hasID = tool.hasID || call.Get("id").String() != ""
			tool.hasName = tool.hasName || call.Get("function.name").String() != ""
			_, _ = tool.arguments.WriteString(call.Get("function.arguments").String())
		}
		if reason := choice.Get("finish_reason").String(); reason != "" {
			terminal := apicompat.ChatCompletionTerminalForReason(reason)
			t.choices[index] = terminal.Error == nil
			if terminal.Status == "completed" {
				invalid := reason == "tool_calls" && !t.toolChoices[index]
				for _, tool := range t.tools[index] {
					if tool.kind != "function" {
						continue
					}
					invalid = invalid || !tool.hasID || !tool.hasName || !json.Valid([]byte(tool.arguments.String()))
				}
				if invalid {
					t.err = ccStreamProtocolFailure("upstream_invalid_tool_call", "Upstream returned an incomplete or invalid tool call")
				}
			}
		}
	}
	t.sawFinishReason = len(t.choices) > 0
	for _, finished := range t.choices {
		t.sawFinishReason = t.sawFinishReason && finished
	}
}

// Terminated 表示上游给出过终止信号。
func (t *openAIRawStreamTerminalState) Terminated() bool {
	return t != nil && t.err == nil && t.sawFinishReason
}

// A stream request receiving no semantic terminal (including a non-SSE body)
// is incomplete regardless of whether a prefix was already forwarded.
func (t *openAIRawStreamTerminalState) IsTruncated(_ bool) bool {
	return t != nil && !t.Terminated()
}

// newOpenAIRawStreamTruncatedFailoverError 处理"上游截断且尚未向客户端写出任何
// 字节"的情况：响应头还没提交，可以透明换号重试，客户端不会看到半截流。
func newOpenAIRawStreamTruncatedFailoverError(
	c *gin.Context,
	account *Account,
	upstreamRequestID string,
	cause error,
) *UpstreamFailoverError {
	recordOpenAIRawStreamTruncation(c, account, upstreamRequestID, cause, "failover")

	headers := http.Header{}
	if id := strings.TrimSpace(upstreamRequestID); id != "" {
		headers.Set("x-request-id", id)
	}
	return &UpstreamFailoverError{
		StatusCode:      http.StatusBadGateway,
		ResponseBody:    openAIRawStreamTruncatedErrorBody(cause),
		ResponseHeaders: headers,
	}
}

// recordOpenAIRawStreamTruncation 把上游截断记入 ops 上下文，使其在错误日志与
// 账号健康度中可见——这正是此前"HTTP 200 假成功"丢掉的信息。
func recordOpenAIRawStreamTruncation(
	c *gin.Context,
	account *Account,
	upstreamRequestID string,
	cause error,
	kind string,
) {
	if c == nil {
		return
	}
	message := openAIRawStreamTruncatedMessage(cause)
	platform := PlatformOpenAI
	accountID := int64(0)
	accountName := ""
	if account != nil {
		platform = account.Platform
		accountID = account.ID
		accountName = account.Name
	}

	setOpsUpstreamError(c, http.StatusBadGateway, message, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           platform,
		AccountID:          accountID,
		AccountName:        accountName,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
		Kind:               kind,
		Message:            message,
	})
}

// openAIRawStreamTruncatedMessage 拼出 ops 消息：干净 EOF 没有底层错误可带，
// 传输层错误（connection reset / http2 stream error）则保留原因以便定位。
func openAIRawStreamTruncatedMessage(cause error) string {
	if cause == nil || errors.Is(cause, ErrOpenAIUpstreamStreamTruncated) {
		return openAIRawStreamTruncatedUpstreamMessage
	}
	return openAIRawStreamTruncatedUpstreamMessage + ": " + cause.Error()
}

// openAIRawStreamTruncatedErrorBody 构造 failover 错误体，code/message 与
// 写出后走 openAIUpstreamStreamReadError 的客户端分类保持一致。
func openAIRawStreamTruncatedErrorBody(cause error) []byte {
	code, message := classifyOpenAIUpstreamStreamReadError(cause)
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "upstream_error",
			"code":    code,
			"message": message,
		},
	})
	if err != nil {
		return []byte(`{"error":{"type":"upstream_error","code":"` + OpenAIUpstreamStreamTruncatedCode +
			`","message":"Upstream response stream ended before completion"}}`)
	}
	return body
}

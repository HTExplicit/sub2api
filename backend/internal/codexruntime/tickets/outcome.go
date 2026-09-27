package tickets

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

var outcomeMessages = map[string]string{
	"routing_verified": "业务出口响应完整、模型声明一致；本次未验证质量", "routing_candidate": "已取得候选路由材料，尚未通过业务出口验证",
	"routing_model_mismatch": "上游模型声明不匹配，未发布路由资格", "routing_incomplete": "响应未完整结束或缺少模型声明，未发布路由资格",
	"routing_capacity": "上游容量不足或流内限流，未完成路由验证", "routing_policy": "上游策略检查阻止请求，未完成路由验证",
	"routing_cancelled": "客户端取消，本次响应未完成验证", "routing_legacy_retired": "旧 STATE 材料已丢弃，尚未获得路由资格",
	"routing_cookie_missing": "响应未给出可用路由 Cookie", "routing_cookie_expired": "Cookie 已到期，需要重新验证",
	"routing_transport": "采集或业务出口验证连接失败", "routing_stale": "账号主体、出口、指纹或 Cookie 代次已变化",
	"routing_cookie_deleted": "上游已撤销路由 Cookie", "routing_budget_spent": "本次验证的固定调用预算已使用",
	"routing_upstream": "上游拒绝路由验证请求", "routing_connection_unknown": "无法绑定经过验证的实际连接",
	"routing_connection_expired": "已验证连接不可复用，需要重新验证", "routing_unavailable": "路由采集未能开始（账号出口范围不可用）",
	"routing_persist": "路由 Cookie 记录保存失败", "routing_observation_unavailable": "无法登记本次路由观测", "routing_validation_unavailable": "验证请求未能完成",
	"ticket_ready": "历史 STATE 采集记录，不代表当前路由资格", "ticket_skipped": "已有路由验证记录，本次跳过；本次未验证质量", "ticket_stopped": "路由自动续期已停止",
	"ticket_disabled": "路由采集与验证已关闭", "ticket_model_invalid": "模型不在路由配置范围内", "ticket_proxy_missing": "尚未配置采集代理",
	"ticket_missing": "该模型尚无有效路由资格", "ticket_operation_required": "手动采集缺少操作标识",
	"ticket_busy": "账号和模型已有进行中的操作", "ticket_interrupted": "前次请求结果未确认，未重复发送", "ticket_not_due": "未到续期时间",
	"ticket_attempt_in_progress": "该账号和模型的上一次尝试仍在进行", "ticket_ineligible": "仅 OpenAI OAuth/Setup Token 非影子账号支持路由采集",
	"ticket_token": "无法取得账号访问令牌", "ticket_transport": "采集连接失败", "ticket_upstream": "上游拒绝采集请求",
	"ticket_length": "历史 STATE 长度检查失败（该规则已停用）", "ticket_prefix": "历史 STATE 格式检查失败（该规则已停用）", "ticket_persist": "路由验证记录保存失败",
	"ticket_stale": "账号主体或操作状态已变化", "ticket_canceled": "操作已取消", "proxy_connection_failed": "代理连接或证书验证失败",
	"pinned_connection_failed": "固定证书验证失败", "invalid_proxy": "代理地址或协议不正确",
}

// OutcomeMessage describes a result code. An unknown code is named as is,
// never replaced by another category's sentence.
func OutcomeMessage(code string) string {
	if message := outcomeMessages[code]; message != "" {
		return message
	}
	return "未归类的结果代码：" + code
}

func responseHeaderText(headers []extensionv1.CodexRoutingHeader) string {
	lines := make([]string, 0, len(headers))
	for _, header := range headers {
		lines = append(lines, header.Name+": "+header.Value)
	}
	return strings.Join(lines, "\n")
}

func outcomeFact(zh, en, value string) extensionv1.DisplayFact {
	return extensionv1.DisplayFact{Label: map[string]string{"zh": zh, "en": en}, Value: value}
}

func (o Outcome) MarshalJSON() ([]byte, error) {
	type wire Outcome
	message := OutcomeMessage(o.Code)
	facts := []extensionv1.DisplayFact{outcomeFact("结果代码", "Result code", o.Code)}
	if o.HTTPStatus > 0 {
		facts = append(facts, outcomeFact("上游状态", "Upstream status", strconv.Itoa(o.HTTPStatus)))
	}
	if o.ObservedLength > 0 {
		facts = append(facts, outcomeFact("STATE 长度（仅观测）", "STATE length (observation only)", strconv.Itoa(o.ObservedLength)))
	}
	if observation := o.Observation; observation != nil {
		material, completed, matched := "未证实有效", "未证实完整", "未知"
		if o.Code == "routing_candidate" {
			material = "候选材料，待业务出口验证"
		} else if o.Code == "routing_verified" && o.Success {
			material = "验证时有效"
		}
		if observation.Completed {
			completed = "完整结束"
		}
		if observation.ResponseModel != "" {
			matched = "不一致"
			if observation.ModelMatched {
				matched = "一致"
			}
			facts = append(facts, outcomeFact("上游模型声明", "Upstream model declaration", observation.ResponseModel))
		}
		facts = append(facts,
			outcomeFact("路由材料", "Routing material", material),
			outcomeFact("完整响应", "Response completion", completed),
			outcomeFact("模型声明匹配", "Model declaration match", matched))
		for _, detail := range []struct{ zh, en, value string }{
			{"阶段", "Stage", observation.Stage},
			{"传输", "Transport", observation.Transport},
			{"请求模型", "Requested model", observation.RequestedModel},
			{"推理强度", "Reasoning effort", observation.ReasoningEffort},
			{"错误原文", "Error", observation.Error},
			{"上游错误类型", "Upstream error type", observation.UpstreamErrorType},
			{"上游错误代码", "Upstream error code", observation.UpstreamErrorCode},
			{"上游错误信息", "Upstream error message", observation.UpstreamErrorMessage},
			{"上游错误参数", "Upstream error param", observation.UpstreamErrorParam},
			{"上游请求 ID", "Upstream request ID", observation.RequestID},
			{"CF-Ray", "CF-Ray", observation.CFRay},
			{"Cookie 名称", "Cookie names", strings.Join(observation.CookieNames, ", ")},
			{"STATE", "STATE", observation.State},
			{"上游响应头", "Upstream response headers", responseHeaderText(observation.ResponseHeaders)},
			{"未收录的响应头（超出 4 KiB）", "Response headers not kept (over 4 KiB)", strings.Join(observation.ResponseHeadersOmitted, ", ")},
			{"上游响应正文", "Upstream response body", observation.UpstreamBody},
		} {
			if detail.value != "" {
				facts = append(facts, outcomeFact(detail.zh, detail.en, detail.value))
			}
		}
		if observation.DurationMS > 0 {
			facts = append(facts, outcomeFact("耗时（毫秒）", "Duration (ms)", strconv.FormatInt(observation.DurationMS, 10)))
		}
		if observation.CookieSent {
			facts = append(facts, outcomeFact("已发送路由 Cookie", "Routing cookie sent", "是 / yes"))
		}
		if !observation.ObservedAt.IsZero() {
			facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "观测时间", "en": "Observed at"}, Value: observation.ObservedAt.Format(time.RFC3339), Timestamp: true})
		}
	}
	if strings.HasPrefix(o.Code, "routing_") || o.Code == "ticket_skipped" || o.Code == "ticket_ready" {
		facts = append(facts, outcomeFact("质量验证", "Quality validation", "本次未验证 / Not tested by this operation"))
	}
	if o.ExpiresAt != nil {
		facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "路由记录有效期至", "en": "Routing record expires"}, Value: o.ExpiresAt.Format(time.RFC3339), Timestamp: true})
	}
	return json.Marshal(struct {
		wire
		Message string                    `json:"message"`
		Facts   []extensionv1.DisplayFact `json:"facts"`
	}{wire(o), message, facts})
}
